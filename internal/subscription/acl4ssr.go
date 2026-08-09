package subscription

import (
	"fmt"
	"strings"
)

// ACL4SSRBaseURL 是 ACL4SSR 规则列表的 GitHub 源（Clash/ 目录）。
// 运行时可在服务端用 Setting(acl4ssr_base) 覆盖，指向兼容的镜像基址。
// 注意：GoogleFCM / SteamCN 实际位于 Clash/Ruleset/ 子目录下（见 acl4ssrRules）。
const ACL4SSRBaseURL = "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/"

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

// buildClashGroups 依据模板生成 Clash 的 proxy-groups（11 个），节点名展开。
func buildClashGroups(proxyNames []string) []map[string]interface{} {
	groups := make([]map[string]interface{}, 0, len(acl4ssrGroups))
	for _, g := range acl4ssrGroups {
		m := map[string]interface{}{
			"name":    g.Name,
			"type":    g.Type,
			"proxies": expandMembers(g, proxyNames),
		}
		if g.Type == "url-test" || g.Type == "fallback" {
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
	for _, g := range acl4ssrGroups {
		members := expandMembers(g, proxyNames)
		if g.Type == "url-test" {
			sb.WriteString(fmt.Sprintf("%s = %s, %s, url=%s, interval=%d, tolerance=%d\n",
				g.Name, g.Type, strings.Join(members, ", "), g.TestURL, g.Interval, g.Tolerance))
		} else {
			sb.WriteString(fmt.Sprintf("%s = %s, %s\n", g.Name, g.Type, strings.Join(members, ", ")))
		}
	}
	return sb.String()
}

// buildSurgeRules 生成 Surge/Surfboard/Loon 的 [Rule] 段（RULE-SET 指向 ACL4SSR 规则源）。
func buildSurgeRules(rulesBaseURL string) string {
	var sb strings.Builder
	for _, r := range acl4ssrRules {
		sb.WriteString(aclRuleLineSurge(r, rulesBaseURL) + "\n")
	}
	return sb.String()
}
