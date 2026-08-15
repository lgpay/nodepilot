package subscription

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// ACL4SSRBaseURL 是 ACL4SSR 规则列表的 GitHub 源（Clash/ 目录）。
// 运行时可在服务端用 Setting(acl4ssr_base) 覆盖，指向兼容的镜像基址。
// 注意：GoogleFCM / SteamCN 实际位于 Clash/Ruleset/ 子目录下（见 acl4ssrRules）。
const ACL4SSRBaseURL = "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/"

// 静态快照（fallback）：下述 acl4ssrGroups / acl4ssrRules 是对 ACL4SSR 上游 master 分支
// ACL4SSR_Online.ini（Clash/config/）的静态复刻，核对时间 2026-08-12。
// 运行时优先加载仓库内 rules/ACL4SSR_Online.ini（由 GitHub Actions 每日同步），
// 文件缺失或解析失败时回退本静态快照。

// aclGroup 对应 ACL4SSR_Online.ini 的 custom_proxy_group（标准分组集）。
// Members 中 "*" 表示展开为全部节点名；其余为字面策略/分组名（DIRECT/REJECT/♻️ 自动选择 等）。
type aclGroup struct {
	Name      string
	Type      string // select | url-test | fallback | load-balance
	Members   []string
	TestURL   string
	Interval  int
	Tolerance int
}

// aclRule 对应 ACL4SSR_Online.ini 的 ruleset：把某规则列表(或内置规则)路由到某分组。
type aclRule struct {
	List    string // .list 基名（不含 .list）；内置规则时为空
	Builtin string // 内置规则原文，如 "GEOIP,CN"、"FINAL"
	Group   string
}

// ACLTemplate 由 ACL4SSR_Online.ini 解析出的分组与规则路由模板。
type ACLTemplate struct {
	Groups []aclGroup
	Rules  []aclRule
}

// defaultTemplate 内置静态快照（解析失败时的回退模板）。
var defaultTemplate = ACLTemplate{
	Groups: acl4ssrGroups,
	Rules:  acl4ssrRules,
}

// activeTemplate 当前生效模板，默认内置快照；可由 SetACLTemplate 覆盖（原子替换，并发安全）。
var activeTemplate atomic.Pointer[ACLTemplate]

func init() {
	t := defaultTemplate
	activeTemplate.Store(&t)
}

// SetACLTemplate 设置运行时模板（如从 rules/ACL4SSR_Online.ini 解析得到）。
func SetACLTemplate(t *ACLTemplate) { activeTemplate.Store(t) }

// currentTemplate 返回当前生效模板。
func currentTemplate() *ACLTemplate { return activeTemplate.Load() }

// LoadACLFile 读取并解析 ACL4SSR_Online.ini 文件（[custom] 段的 ruleset= / custom_proxy_group= 行）。
func LoadACLFile(path string) (*ACLTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseACLContent(string(data))
}

// ParseACLContent 解析 ACL4SSR_Online.ini 内容，返回分组与规则模板。
func ParseACLContent(content string) (*ACLTemplate, error) {
	t := &ACLTemplate{}
	inCustom := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inCustom = line == "[custom]"
			continue
		}
		if !inCustom {
			continue
		}
		switch {
		case strings.HasPrefix(line, "ruleset="):
			r, err := parseRuleSetLine(strings.TrimPrefix(line, "ruleset="))
			if err != nil {
				return nil, err
			}
			t.Rules = append(t.Rules, r)
		case strings.HasPrefix(line, "custom_proxy_group="):
			g, err := parseGroupLine(strings.TrimPrefix(line, "custom_proxy_group="))
			if err != nil {
				return nil, err
			}
			t.Groups = append(t.Groups, g)
		}
	}
	if len(t.Rules) == 0 || len(t.Groups) == 0 {
		return nil, errors.New("ACL4SSR ini 缺少 ruleset 或 custom_proxy_group，格式不符")
	}
	return t, nil
}

// parseRuleSetLine 解析 "分组,URL" 或 "分组,[]内置规则"（如 []GEOIP,CN / []FINAL）。
func parseRuleSetLine(s string) (aclRule, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return aclRule{}, fmt.Errorf("ruleset 格式不符: %q", s)
	}
	group := strings.TrimSpace(parts[0])
	target := strings.TrimSpace(parts[1])
	if strings.HasPrefix(target, "[]") {
		return aclRule{Group: group, Builtin: strings.TrimPrefix(target, "[]")}, nil
	}
	name, err := listNameFromURL(target)
	if err != nil {
		return aclRule{}, err
	}
	return aclRule{Group: group, List: name}, nil
}

// listNameFromURL 从规则 URL 提取 .list 相对基名（保留子目录路径）。
// 例：https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Ruleset/GoogleFCM.list → Ruleset/GoogleFCM
func listNameFromURL(u string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil || parsed.Path == "" {
		return "", fmt.Errorf("ruleset URL 非法: %q", u)
	}
	p := strings.Trim(parsed.Path, "/")
	if !strings.HasSuffix(p, ".list") {
		return "", fmt.Errorf("ruleset 非 .list 文件: %q", u)
	}
	// 去掉镜像基址中 /Clash/ 段（兼容上游及常见镜像），保留其后相对路径
	if i := strings.Index(p, "Clash/"); i >= 0 {
		p = p[i+len("Clash/"):]
	}
	return strings.TrimSuffix(p, ".list"), nil
}

// parseGroupLine 解析 custom_proxy_group 行，反引号分隔：
//
//	select / fallback / load-balance：名称`类型`成员...（成员前缀 [] 为字面策略，.* 展开全部节点）
//	url-test：名称`url-test`成员`URL`间隔`容忍度（如 300,,50）
func parseGroupLine(s string) (aclGroup, error) {
	fields := strings.Split(s, "`")
	if len(fields) < 3 {
		return aclGroup{}, fmt.Errorf("custom_proxy_group 格式不符: %q", s)
	}
	g := aclGroup{Name: strings.TrimSpace(fields[0]), Type: strings.TrimSpace(fields[1])}
	// 成员之后的参数：URL（http/https 开头）、间隔/容忍度（数字）
	for _, m := range fields[2:] {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		switch {
		case m == ".*" || m == "*":
			g.Members = append(g.Members, "*")
		case strings.HasPrefix(m, "[]"):
			g.Members = append(g.Members, strings.TrimPrefix(m, "[]"))
		case strings.HasPrefix(m, "http://") || strings.HasPrefix(m, "https://"):
			g.TestURL = m
		case isNumeric(m):
			if g.TestURL != "" && g.Interval == 0 {
				g.Interval, _ = strconv.Atoi(m)
			} else if g.TestURL != "" {
				g.Tolerance, _ = strconv.Atoi(m)
			}
		case g.TestURL != "" && strings.Contains(m, ","):
			// ACL4SSR url-test 标准写法把 间隔与容忍度 放在单个字段内，逗号分隔，
			// 如 "300,,50"（interval,,tolerance）。此处把其中数字段解析为 interval/tolerance，
			// 而非误当作分组成员。
			var nums []string
			for _, s := range strings.Split(m, ",") {
				s = strings.TrimSpace(s)
				if isNumeric(s) {
					nums = append(nums, s)
				}
			}
			if len(nums) > 0 && g.Interval == 0 {
				g.Interval, _ = strconv.Atoi(nums[0])
			}
			if len(nums) > 1 && g.Tolerance == 0 {
				g.Tolerance, _ = strconv.Atoi(nums[1])
			}
		default:
			g.Members = append(g.Members, m)
		}
	}
	if len(g.Members) == 0 {
		return aclGroup{}, fmt.Errorf("custom_proxy_group 无成员: %q", s)
	}
	return g, nil
}

func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// acl4ssrGroups 完整复刻 ACL4SSR_Online.ini 的 11 个分组（含节点自动选择）。
var acl4ssrGroups = []aclGroup{
	{Name: "🚀 节点选择", Type: "select", Members: []string{"♻️ 自动选择", "DIRECT", "*"}},
	{Name: "♻️ 自动选择", Type: "url-test", Members: []string{"*"}, TestURL: "http://www.gstatic.com/generate_204", Interval: 300, Tolerance: 50},
	{Name: "🌍 国外媒体", Type: "select", Members: []string{"🚀 节点选择", "♻️ 自动选择", "🎯 全球直连", "*"}},
	{Name: "📲 电报信息", Type: "select", Members: []string{"🚀 节点选择", "🎯 全球直连", "*"}},
	{Name: "Ⓜ️ 微软服务", Type: "select", Members: []string{"🎯 全球直连", "🚀 节点选择", "*"}},
	{Name: "🍎 苹果服务", Type: "select", Members: []string{"🚀 节点选择", "🎯 全球直连", "*"}},
	{Name: "📢 谷歌FCM", Type: "select", Members: []string{"🚀 节点选择", "🎯 全球直连", "♻️ 自动选择", "*"}},
	{Name: "🎯 全球直连", Type: "select", Members: []string{"DIRECT", "🚀 节点选择", "♻️ 自动选择"}},
	{Name: "🛑 全球拦截", Type: "select", Members: []string{"REJECT", "DIRECT"}},
	{Name: "🍃 应用净化", Type: "select", Members: []string{"REJECT", "DIRECT"}},
	{Name: "🐟 漏网之鱼", Type: "select", Members: []string{"🚀 节点选择", "🎯 全球直连", "♻️ 自动选择", "*"}},
}

// acl4ssrRules 复刻 ACL4SSR_Online.ini 的 ruleset 映射（顺序即输出顺序）。
var acl4ssrRules = []aclRule{
	{List: "LocalAreaNetwork", Group: "🎯 全球直连"},
	{List: "UnBan", Group: "🎯 全球直连"},
	{List: "BanAD", Group: "🛑 全球拦截"},
	{List: "BanProgramAD", Group: "🍃 应用净化"},
	{List: "Ruleset/GoogleFCM", Group: "📢 谷歌FCM"},
	{List: "GoogleCN", Group: "🎯 全球直连"},
	{List: "Ruleset/SteamCN", Group: "🎯 全球直连"},
	{List: "Microsoft", Group: "Ⓜ️ 微软服务"},
	{List: "Apple", Group: "🍎 苹果服务"},
	{List: "Telegram", Group: "📲 电报信息"},
	{List: "ProxyMedia", Group: "🌍 国外媒体"},
	{List: "ProxyLite", Group: "🚀 节点选择"},
	{List: "ChinaDomain", Group: "🎯 全球直连"},
	{List: "ChinaCompanyIp", Group: "🎯 全球直连"},
	{Builtin: "GEOIP,CN", Group: "🎯 全球直连"},
	{Builtin: "FINAL", Group: "🐟 漏网之鱼"},
}

// expandMembers 将分组成员中的 "*" 展开为实际节点名，其余字面保留。
func expandMembers(g aclGroup, proxyNames []string) []string {
	out := make([]string, 0, len(g.Members)+len(proxyNames))
	for _, m := range g.Members {
		if m == "*" {
			out = append(out, proxyNames...)
		} else {
			out = append(out, m)
		}
	}
	return out
}

// aclRuleLineClash 返回 Clash 的一条 rule 行（FINAL 转 MATCH）。
func aclRuleLineClash(r aclRule) string {
	if r.Builtin != "" {
		if r.Builtin == "FINAL" {
			return "MATCH," + r.Group
		}
		return r.Builtin + "," + r.Group
	}
	return "RULE-SET," + r.List + "," + r.Group
}

// aclRuleLineSurge 返回 Surge/Surfboard/Loon 的一条 rule 行；
// list 类用 RULE-SET,<rulesBaseURL>/<list>.list（指向 ACL4SSR 规则源），内置规则原样输出。
func aclRuleLineSurge(r aclRule, rulesBaseURL string) string {
	if r.Builtin != "" {
		return r.Builtin + "," + r.Group
	}
	return "RULE-SET," + strings.TrimRight(rulesBaseURL, "/") + "/" + r.List + ".list," + r.Group
}

// buildClashGroups 依据当前模板生成 Clash 的 proxy-groups，节点名展开。
func buildClashGroups(proxyNames []string) []map[string]interface{} {
	t := currentTemplate()
	groups := make([]map[string]interface{}, 0, len(t.Groups))
	for _, g := range t.Groups {
		m := map[string]interface{}{
			"name":    g.Name,
			"type":    g.Type,
			"proxies": expandMembers(g, proxyNames),
		}
		if g.Type == "url-test" || g.Type == "fallback" || g.Type == "load-balance" {
			m["url"] = g.TestURL
			m["interval"] = g.Interval
			if g.Type == "url-test" {
				m["tolerance"] = g.Tolerance
			}
		}
		groups = append(groups, m)
	}
	return groups
}

// buildSurgeGroups 生成 Surge/Surfboard/Loon 的 [Proxy Group] 段。
func buildSurgeGroups(proxyNames []string) string {
	var sb strings.Builder
	for _, g := range currentTemplate().Groups {
		members := expandMembers(g, proxyNames)
		switch g.Type {
		case "url-test":
			sb.WriteString(fmt.Sprintf("%s = %s, %s, url=%s, interval=%d, tolerance=%d\n",
				g.Name, g.Type, strings.Join(members, ", "), g.TestURL, g.Interval, g.Tolerance))
		case "fallback", "load-balance":
			sb.WriteString(fmt.Sprintf("%s = %s, %s, url=%s, interval=%d\n",
				g.Name, g.Type, strings.Join(members, ", "), g.TestURL, g.Interval))
		default:
			sb.WriteString(fmt.Sprintf("%s = %s, %s\n", g.Name, g.Type, strings.Join(members, ", ")))
		}
	}
	return sb.String()
}

// buildSurgeRules 生成 Surge/Surfboard/Loon 的 [Rule] 段（RULE-SET 指向 ACL4SSR 规则源）。
func buildSurgeRules(rulesBaseURL string) string {
	var sb strings.Builder
	for _, r := range currentTemplate().Rules {
		sb.WriteString(aclRuleLineSurge(r, rulesBaseURL) + "\n")
	}
	return sb.String()
}
