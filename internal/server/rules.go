package server

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	"nodepilot/internal/store"
	"nodepilot/internal/subscription"
)

// RulesCacheDir 规则镜像的磁盘缓存目录（由 main 按 db 所在目录设置）。
var RulesCacheDir string

// InitACLTemplate 加载仓库内 rules/ACL4SSR_Online.ini（由 GitHub Actions 每日同步）作为
// 订阅生成的分组/规则模板；文件缺失或解析失败时回退内置静态快照并告警。
func InitACLTemplate(rulesDir string) {
	path := filepath.Join(rulesDir, "ACL4SSR_Online.ini")
	tpl, err := subscription.LoadACLFile(path)
	if err != nil {
		log.Printf("[acl] ACL4SSR_Online.ini 加载失败，使用内置静态快照: %v", err)
		return
	}
	subscription.SetACLTemplate(tpl)
	log.Printf("[acl] ACL4SSR 模板已加载: %s (groups=%d rules=%d)", path, len(tpl.Groups), len(tpl.Rules))
}

const ruleCacheTTL = 24 * time.Hour

type ruleCacheEntry struct {
	content string
	expire  time.Time
}

var (
	ruleMemCache   = map[string]ruleCacheEntry{}
	ruleMemCacheMu sync.Mutex
)

// GetRuleFile 公开端点：从 ACL4SSR 源拉取 .list 规则列表，清洗后返回，
// 供客户端（Clash rule-provider / Surge RULE-SET）直接引用，避免依赖 GitHub。
// ?fmt=yaml 时包裹为 Clash classical rule-provider 的 payload 格式。
func GetRuleFile(c *gin.Context) {
	name := c.Param("name")
	// *name 通配参数带前导斜杠（如 /Ruleset/GoogleFCM.list），去除之
	name = strings.TrimPrefix(name, "/")
	if !isValidRuleName(name) {
		c.JSON(400, gin.H{"error": "invalid rule name"})
		return
	}

	content, ok := loadRule(name)
	if !ok {
		c.JSON(502, gin.H{"error": "failed to fetch rule list"})
		return
	}

	if c.Query("fmt") == "yaml" {
		// Clash classical rule-provider 的 YAML 格式为 payload 列表
		lines := strings.Split(content, "\n")
		payload := make([]string, 0, len(lines))
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				payload = append(payload, l)
			}
		}
		b, err := yaml.Marshal(map[string]interface{}{"payload": payload})
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}
		c.Data(200, "application/yaml; charset=utf-8", b)
		return
	}

	c.Data(200, "text/plain; charset=utf-8", []byte(content))
}

// isValidRuleName 允许字母数字、下划线、横线、点与子目录斜杠，且必须以 .list 结尾。
// 禁止 ".."（防目录穿越，避免把本地文件系统路径暴露给客户端）。
func isValidRuleName(name string) bool {
	if !strings.HasSuffix(name, ".list") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' && r != '/' {
			return false
		}
	}
	return true
}

// loadRule 依次尝试：内存缓存 → 远程拉取(并写缓存) → 磁盘缓存 → 陈旧内存缓存。
func loadRule(name string) (string, bool) {
	now := time.Now()

	ruleMemCacheMu.Lock()
	if e, found := ruleMemCache[name]; found && e.expire.After(now) {
		ruleMemCacheMu.Unlock()
		return e.content, true
	}
	ruleMemCacheMu.Unlock()

	if content, ok := fetchRule(name); ok {
		cacheRule(name, content, now.Add(ruleCacheTTL))
		return content, true
	}

	// 拉取失败：回退磁盘缓存
	if RulesCacheDir != "" {
		if b, err := os.ReadFile(filepath.Join(RulesCacheDir, name)); err == nil {
			cacheRule(name, string(b), now.Add(ruleCacheTTL))
			return string(b), true
		}
	}
	// 再回退陈旧内存
	ruleMemCacheMu.Lock()
	if e, found := ruleMemCache[name]; found {
		ruleMemCacheMu.Unlock()
		return e.content, true
	}
	ruleMemCacheMu.Unlock()
	return "", false
}

// fetchRule 从 ACL4SSR 源（可被 Setting acl4ssr_base 覆盖）拉取并清洗。
func fetchRule(name string) (string, bool) {
	base := subscription.ACL4SSRBaseURL
	if v, err := store.GetSetting("acl4ssr_base"); err == nil && v != "" {
		base = strings.TrimRight(v, "/") + "/"
	}
	url := base + name

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return cleanRuleList(string(raw)), true
}

// cleanRuleList 去掉 # 注释行与空行。
func cleanRuleList(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func cacheRule(name, content string, expire time.Time) {
	ruleMemCacheMu.Lock()
	ruleMemCache[name] = ruleCacheEntry{content: content, expire: expire}
	ruleMemCacheMu.Unlock()
	if RulesCacheDir != "" {
		_ = os.MkdirAll(RulesCacheDir, 0o755)
		// 规则名可能含子目录（如 Ruleset/GoogleFCM.list），确保对应缓存子目录存在
		if dir := filepath.Dir(name); dir != "." && dir != "" {
			_ = os.MkdirAll(filepath.Join(RulesCacheDir, dir), 0o755)
		}
		_ = os.WriteFile(filepath.Join(RulesCacheDir, name), []byte(content), 0o644)
	}
}
