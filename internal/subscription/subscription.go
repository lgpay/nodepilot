package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExportItem 一个可导出为订阅链接的客户端配置
type ExportItem struct {
	Host       string // 节点接入 IP（客户端连接用）
	Port       int    // 入站端口
	Protocol   string // vmess|vless|trojan|ss|socks|http
	Transport  string // tcp|ws|grpc
	TLSEnabled bool
	WsPath     string // ws 路径（tcp/grpc 时为空）
	SNI        string // TLS SNI / ws Host 头（tls 时=节点接入 host）
	UUID       string
	Alias      string // 别名/备注（vmess ps）
}

// BuildVMess 生成 vmess 订阅内容：各 client 的 vmess:// 链接用 \n 连接后整体 base64
func BuildVMess(items []ExportItem) (string, error) {
	links := []string{}
	for _, it := range items {
		if it.Protocol != "vmess" {
			continue
		}
		links = append(links, vmessLink(it))
	}
	raw := strings.Join(links, "\n")
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

func vmessLink(it ExportItem) string {
	tls := ""
	if it.TLSEnabled {
		tls = "tls"
	}
	path := it.WsPath
	if path == "" {
		path = "/v2ray"
	}
	m := map[string]interface{}{
		"v":    "2",
		"ps":   it.Alias,
		"add":  it.Host,
		"port": it.Port,
		"id":   it.UUID,
		"aid":  "0",
		"scy":  "auto",
		"net":  it.Transport,
		"type": "none",
		"host": "",
		"path": path,
		"tls":  tls,
	}
	b, _ := json.Marshal(m)
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

// BuildClash 生成 Clash 配置（YAML，裸订阅：仅节点，无路由规则）
func BuildClash(items []ExportItem) (string, error) {
	cfg := clashConfig{
		Port:             7890,
		SocksPort:        7891,
		AllowLan:         false,
		Mode:             "rule",
		LogLevel:         "info",
		ExternalController: "127.0.0.1:9090",
	}
	groupMembers := []string{}
	for _, it := range items {
		if it.Protocol != "vmess" {
			continue
		}
		cfg.Proxies = append(cfg.Proxies, buildClashProxy(it))
		groupMembers = append(groupMembers, it.Alias)
	}
	cfg.ProxyGroups = []map[string]interface{}{
		{
			"name":    "NodePilot",
			"type":    "select",
			"proxies": groupMembers,
		},
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// buildClashProxy 构造单个 vmess 代理（含 sni / ws headers 修正）
func buildClashProxy(it ExportItem) clashProxy {
	path := it.WsPath
	if path == "" {
		path = "/v2ray"
	}
	wsOpts := map[string]interface{}{"path": path}
	if it.TLSEnabled && it.SNI != "" {
		wsOpts["headers"] = map[string]interface{}{"Host": it.SNI}
	}
	return clashProxy{
		Name:    it.Alias,
		Type:    "vmess",
		Server:  it.Host,
		Port:    it.Port,
		UUID:    it.UUID,
		AlterId: 0,
		Cipher:  "auto",
		Network: it.Transport,
		Sni:     ternary(it.TLSEnabled, it.SNI, ""),
		WsOpts:  wsOpts,
	}
}

// BuildSIP008 生成 SIP008 订阅（JSON）
func BuildSIP008(items []ExportItem) (string, error) {
	sip := sip008{Version: 1}
	for _, it := range items {
		if it.Protocol != "vmess" {
			continue
		}
		path := it.WsPath
		if path == "" {
			path = "/v2ray"
		}
		server := sipServer{
			ID:       it.UUID,
			Remarks:  it.Alias,
			Address:  it.Host,
			Port:     it.Port,
			Protocol: "vmess",
			Settings: map[string]interface{}{"uuid": it.UUID, "alterId": 0, "security": "auto"},
			StreamSettings: map[string]interface{}{
				"network":  it.Transport,
				"security": ternary(it.TLSEnabled, "tls", "none"),
				"wsSettings": map[string]interface{}{"path": path},
			},
		}
		sip.Servers = append(sip.Servers, server)
	}
	b, err := json.MarshalIndent(sip, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- 结构定义 ----

// acl4ssrBase ACL4SSR 的 Clash rule-provider 原始文件基址（已验证存在）。
// Surge/Loon 不能直接消费 Clash 格式规则集，故 ACL4SSR 规则仅用于 Clash/Surfboard。
const acl4ssrBase = "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Providers/"

// acl4ssrProviders 选用的 ACL4SSR rule-provider 名称（均已在仓库 Clash/Providers 下确认存在）。
// behavior 统一用 classical（最稳，可容纳 DOMAIN/IP-CIDR/PROCESS 各类规则）。
var acl4ssrProviders = []string{
	"BanAD", "BanProgramAD", "BanEasyList", "BanEasyPrivacy", "BanEasyListChina",
	"ProxyMedia", "ChinaMedia", "ChinaDomain", "ChinaIp", "ChinaIpV6", "ChinaCompanyIp",
	"Apple", "LocalAreaNetwork", "ProxyGFWlist", "ProxyLite", "Download", "UnBan",
}

// BuildClashACL4SSR 生成 Clash 配置（ACL4SSR 模式）：节点 + 分组 + ACL4SSR rule-providers + 路由规则。
// Surfboard 同为 Clash YAML，可直接复用本函数。
func BuildClashACL4SSR(items []ExportItem) (string, error) {
	cfg := clashConfig{
		Port:             7890,
		SocksPort:        7891,
		AllowLan:         false,
		Mode:             "rule",
		LogLevel:         "info",
		ExternalController: "127.0.0.1:9090",
	}
	groupMembers := []string{}
	for _, it := range items {
		if it.Protocol != "vmess" {
			continue
		}
		cfg.Proxies = append(cfg.Proxies, buildClashProxy(it))
		groupMembers = append(groupMembers, it.Alias)
	}

	// rule-providers
	rps := map[string]clashRuleProvider{}
	for _, name := range acl4ssrProviders {
		rps[name] = clashRuleProvider{
			Type:     "http",
			Behavior: "classical",
			URL:      acl4ssrBase + name + ".yaml",
			Path:     "./ruleset/" + name + ".yaml",
			Interval: 86400,
		}
	}

	// 分组
	cfg.RuleProviders = rps
	cfg.ProxyGroups = []map[string]interface{}{
		{"name": "🚀 节点选择", "type": "select", "proxies": groupMembers},
		{"name": "⚠️ 故障转移", "type": "fallback", "proxies": groupMembers, "url": "https://www.gstatic.com/generate_204", "interval": 300, "timeout": 5},
		{"name": "🐟 最低延迟", "type": "url-test", "proxies": groupMembers, "url": "https://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50},
		{"name": "📺 国外媒体", "type": "select", "proxies": append([]string{"🚀 节点选择"}, "ProxyMedia")},
		{"name": "📺 国内媒体", "type": "select", "proxies": append([]string{"DIRECT"}, "ChinaMedia")},
		{"name": "🚫 广告拦截", "type": "select", "proxies": append([]string{"REJECT"}, "BanAD")},
	}

	// 规则
	cfg.Rules = []string{
		"RULE-SET,BanAD,🚫 广告拦截",
		"RULE-SET,BanProgramAD,🚫 广告拦截",
		"RULE-SET,BanEasyList,🚫 广告拦截",
		"RULE-SET,BanEasyPrivacy,🚫 广告拦截",
		"RULE-SET,ProxyMedia,📺 国外媒体",
		"RULE-SET,ChinaMedia,📺 国内媒体",
		"RULE-SET,ChinaDomain,DIRECT",
		"RULE-SET,ChinaIp,DIRECT",
		"RULE-SET,ChinaIpV6,DIRECT",
		"RULE-SET,ChinaCompanyIp,DIRECT",
		"RULE-SET,Apple,DIRECT",
		"RULE-SET,LocalAreaNetwork,DIRECT",
		"RULE-SET,ProxyGFWlist,🚀 节点选择",
		"GEOIP,CN,DIRECT",
		"MATCH,🚀 节点选择",
	}

	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildLoon 生成 Loon 配置（.conf）。withRules=true（ACL4SSR 模式）时附加内置最小路由规则
// （ACL4SSR 规则是 Clash 格式，Loon 不能直接消费，故用内置规则保证可用）。
func BuildLoon(items []ExportItem, withRules bool) (string, error) {
	var sb strings.Builder
	sb.WriteString("[Proxy]\n")
	names := []string{}
	for _, it := range items {
		if it.Protocol != "vmess" {
			continue
		}
		path := it.WsPath
		if path == "" {
			path = "/v2ray"
		}
		sni := ""
		if it.TLSEnabled {
			sni = it.SNI
		}
		// Loon vmess 关键字语法
		line := fmt.Sprintf("%s = vmess, address=%s, port=%d, username=%s, vmess-aid=0, vmess-security=aes-128-gcm, tls=%v, sni=%s, ws=true, ws-path=%s, ws-headers=Host=%s",
			it.Alias, it.Host, it.Port, it.UUID, it.TLSEnabled, sni, path, it.SNI)
		sb.WriteString(line + "\n")
		names = append(names, it.Alias)
	}
	sb.WriteString("\n[Proxy Group]\n")
	sb.WriteString("NodePilot = select, " + strings.Join(names, ", ") + "\n")
	if withRules {
		sb.WriteString("\n[Rule]\n")
		sb.WriteString("GEOIP,CN,DIRECT\n")
		sb.WriteString("FINAL,NodePilot\n")
	}
	return sb.String(), nil
}

type clashProxy struct {
	Name     string                 `yaml:"name"`
	Type     string                 `yaml:"type"`
	Server   string                 `yaml:"server"`
	Port     int                    `yaml:"port"`
	UUID     string                 `yaml:"uuid"`
	AlterId  int                    `yaml:"alterId"`
	Cipher   string                 `yaml:"cipher"`
	Network  string                 `yaml:"network"`
	Sni      string                 `yaml:"sni,omitempty"`
	WsOpts   map[string]interface{} `yaml:"ws-opts,omitempty"`
}

type clashConfig struct {
	Port             int            `yaml:"port"`
	SocksPort        int            `yaml:"socks-port"`
	AllowLan         bool           `yaml:"allow-lan"`
	Mode             string         `yaml:"mode"`
	LogLevel         string         `yaml:"log-level"`
	ExternalController string       `yaml:"external-controller"`
	Proxies          []clashProxy   `yaml:"proxies"`
	ProxyGroups      []map[string]interface{} `yaml:"proxy-groups"`
	RuleProviders    map[string]clashRuleProvider `yaml:"rule-providers,omitempty"`
	Rules            []string       `yaml:"rules,omitempty"`
}

type clashRuleProvider struct {
	Type     string `yaml:"type"`
	Behavior string `yaml:"behavior"`
	URL      string `yaml:"url"`
	Path     string `yaml:"path"`
	Interval int    `yaml:"interval"`
}

type sipServer struct {
	ID             string                 `json:"id"`
	Remarks        string                 `json:"remarks"`
	Address        string                 `json:"address"`
	Port           int                    `json:"port"`
	Protocol       string                 `json:"protocol"`
	Settings       map[string]interface{} `json:"settings"`
	StreamSettings map[string]interface{} `json:"streamSettings"`
}

type sip008 struct {
	Version int         `json:"version"`
	Servers []sipServer `json:"servers"`
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
