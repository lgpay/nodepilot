package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/store"
	"nodepilot/internal/subscription"
)

// RulesCacheDir 规则镜像的磁盘缓存目录（由 main 按 db 所在目录设置）。
var RulesCacheDir string

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
		lines := strings.Split(content, "\n")
		payload := make([]string, 0, len(lines))
		for _, l := range lines {
			if l != "" {
				payload = append(payload, l)
			}
		}
		b, err := json.Marshal(map[string]interface{}{"payload": payload})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.Data(200, "application/yaml; charset=utf-8", b)
		return
	}

	c.Data(200, "text/plain; charset=utf-8", []byte(content))
}

// isValidRuleName 仅允许字母数字、下划线、横线与点，且必须以 .list 结尾，防目录穿越。
func isValidRuleName(name string) bool {
	if !strings.HasSuffix(name, ".list") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
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
		_ = os.WriteFile(filepath.Join(RulesCacheDir, name), []byte(content), 0o644)
	}
}
