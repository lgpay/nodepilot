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
		pr := buildClashProxy(it)
		cfg.Proxies = append(cfg.Proxies, pr)
		groupMembers = append(groupMembers, pr.Name)
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

// proxyName 返回代理显示名；别名空时回退为 <PROTOCOL>-<port>，避免空名导致客户端无法识别。
func proxyName(it ExportItem) string {
	if it.Alias != "" {
		return it.Alias
	}
	return fmt.Sprintf("%s-%d", strings.ToUpper(it.Protocol), it.Port)
}

// buildClashProxy 构造单个代理（vmess/vless/trojan/ss/socks/http），含 sni / tls / ws headers。
func buildClashProxy(it ExportItem) clashProxy {
	p := clashProxy{
		Name:    proxyName(it),
		Server:  it.Host,
		Port:    it.Port,
		Network: it.Transport,
	}
	switch it.Protocol {
	case "vless":
		p.Type = "vless"
		p.UUID = it.UUID
		p.Cipher = "auto"
		if it.TLSEnabled {
			p.Tls = true
			p.Sni = it.SNI
		}
	case "trojan":
		p.Type = "trojan"
		p.Password = it.UUID
		if it.TLSEnabled {
			p.Sni = it.SNI
		}
	case "ss":
		p.Type = "ss"
		p.Cipher = "aes-256-gcm"
		p.Password = it.UUID
		p.Network = "tcp"
	case "socks", "http":
		p.Type = it.Protocol
	default: // vmess
		p.Type = "vmess"
		p.UUID = it.UUID
		alterId := 0
		p.AlterId = &alterId
		p.Cipher = "auto"
		if it.TLSEnabled {
			p.Tls = true
			p.Sni = it.SNI
		}
	}
	if it.Transport == "ws" {
		path := it.WsPath
		if path == "" {
			path = "/v2ray"
		}
		opts := map[string]interface{}{"path": path}
		if it.TLSEnabled && it.SNI != "" {
			opts["headers"] = map[string]interface{}{"Host": it.SNI}
		}
		p.WsOpts = opts
	}
	return p
}

// BuildSurfboard 生成 Surfboard(=Surge 兼容) 订阅内容：[Proxy] INI 段。
// 关键：Surfboard 官方明确“兼容 Surge 配置”，并不解析 Clash YAML 的 proxies: 段；
// 之前输出 Clash YAML 会导致 Surfboard 扫到 0 代理。因此 surfboard 必须输出 Surge 语法。
// 支持的协议（官方 FAQ）：HTTP/HTTPS/SOCKS5、SS/SS-OBFS、VMess、Trojan。
func BuildSurfboard(items []ExportItem, subURL, rulesBaseURL string) (string, error) {
	var sb strings.Builder
	// #!MANAGED-CONFIG 是 Surge/Surfboard 的托管配置指令，必须位于配置文件首行。
	// 它把订阅更新地址写进文件本身，这样即使客户端是“导入配置文件”（而非“从 URL 导入订阅”），
	// 也知道从哪里重新拉取更新，避免“扫到代理但无法更新”。
	if subURL != "" {
		sb.WriteString(fmt.Sprintf("#!MANAGED-CONFIG %s interval=86400 strict=false\n", subURL))
	}
	// [General]：Surfboard 基础设置（对齐参考订阅样式：日志级别、DNS、局域网绕过、增强模式等）。
	sb.WriteString(surfboardGeneralBlock())
	sb.WriteString("[Proxy]\n")
	// DIRECT 是 Surge/Surfboard 内置策略；参考订阅中显式声明为 direct，使分组可指向 DIRECT。
	sb.WriteString("DIRECT = direct\n")
	names := []string{}
	for _, it := range items {
		line, ok := buildSurgeProxyLine(it)
		if !ok {
			continue // 跳过 Surfboard 不支持的协议（vless / grpc / ssr 等）
		}
		sb.WriteString(line + "\n")
		names = append(names, proxyName(it))
	}
	if len(names) == 0 {
		return "", fmt.Errorf("没有 Surfboard 支持的代理（仅支持 vmess/trojan/ss/socks5/http）")
	}
	// rulesBaseURL 非空（即选择了 ACL4SSR 规则预设）时输出完整分组与分流规则；
	// 否则退化为裸订阅（仅节点选择 + FINAL）。
	if rulesBaseURL != "" {
		sb.WriteString("\n[Proxy Group]\n")
		sb.WriteString(buildSurgeGroups(names))
		sb.WriteString("\n[Rule]\n")
		sb.WriteString(buildSurgeRules(rulesBaseURL))
	} else {
		sb.WriteString("\n[Proxy Group]\n")
		sb.WriteString("NodePilot = select, " + strings.Join(names, ", ") + "\n")
		sb.WriteString("\n[Rule]\n")
		sb.WriteString("FINAL,NodePilot\n")
	}
	return sb.String(), nil
}

// buildSurgeProxyLine 生成单条 Surge 风格代理定义行。ok=false 表示该协议不被 Surfboard 支持。
func buildSurgeProxyLine(it ExportItem) (string, bool) {
	name := proxyName(it)
	path := it.WsPath
	if path == "" {
		path = "/v2ray"
	}
	host := it.Host
	switch it.Protocol {
	case "vmess":
		// vmess, server, port, username=uuid, vmess-aead=true, tls=true, sni=host, ws=true, ws-path=, ws-headers=Host:host
		// 注意：Surfboard/Surge 原生用 vmess-aead=true 启用 AEAD（参考订阅样式），ws-headers 不加引号。
		parts := []string{
			fmt.Sprintf("%s = vmess, %s, %d", name, host, it.Port),
			"username=" + it.UUID,
			"vmess-aead=true",
		}
		if it.TLSEnabled {
			parts = append(parts, "tls=true", "sni="+it.SNI)
		}
		if it.Transport == "ws" {
			parts = append(parts, "ws=true", "ws-path="+path)
			if it.TLSEnabled && it.SNI != "" {
				parts = append(parts, "ws-headers=Host:"+it.SNI)
			}
		}
		return strings.Join(parts, ", "), true
	case "trojan":
		// trojan, server, port, password=pass, sni=host, ws=true, ws-path=, ws-headers=Host:host
		parts := []string{
			fmt.Sprintf("%s = trojan, %s, %d", name, host, it.Port),
			"password=" + it.UUID,
		}
		if it.TLSEnabled {
			parts = append(parts, "sni="+it.SNI)
		}
		if it.Transport == "ws" {
			parts = append(parts, "ws=true", "ws-path="+path)
			if it.TLSEnabled && it.SNI != "" {
				parts = append(parts, "ws-headers=Host:"+it.SNI)
			}
		}
		return strings.Join(parts, ", "), true
	case "ss":
		// ss, server, port, encrypt-method=aes-256-gcm, password=pass
		return fmt.Sprintf("%s = ss, %s, %d, encrypt-method=aes-256-gcm, password=%s", name, host, it.Port, it.UUID), true
	case "socks", "http":
		// socks5/http(s), server, port[, password=...]
		pType := it.Protocol
		if it.Protocol == "socks" {
			pType = "socks5"
		}
		if it.TLSEnabled {
			pType = "https" // Surge: https 类型即走 TLS
		}
		line := fmt.Sprintf("%s = %s, %s, %d", name, pType, host, it.Port)
		if it.UUID != "" {
			line += ", password=" + it.UUID
		}
		return line, true
	}
	return "", false
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

// BuildClashACL4SSR 生成 Clash 配置（ACL4SSR_Online 模式）：节点 + 11 标准分组 +
// rule-providers(指向 ACL4SSR 规则源 raw .list) + RULE-SET 路由规则。
// rulesBaseURL 为 ACL4SSR 规则源基址（默认 GitHub raw，可被 Setting acl4ssr_base 覆盖）。
func BuildClashACL4SSR(items []ExportItem, rulesBaseURL string) (string, error) {
	cfg := clashConfig{
		Port:             7890,
		SocksPort:        7891,
		AllowLan:         false,
		Mode:             "rule",
		LogLevel:         "info",
		ExternalController: "127.0.0.1:9090",
	}
	proxyNames := []string{}
	for _, it := range items {
		pr := buildClashProxy(it)
		cfg.Proxies = append(cfg.Proxies, pr)
		proxyNames = append(proxyNames, pr.Name)
	}

	// rule-providers：每个 ACL4SSR 列表一个，URL 指向 ACL4SSR 规则源（classical 格式，无需 yaml 包装）
	base := strings.TrimRight(rulesBaseURL, "/")
	rps := map[string]clashRuleProvider{}
	for _, r := range acl4ssrRules {
		if r.List == "" {
			continue
		}
		rps[r.List] = clashRuleProvider{
			Type:     "http",
			Behavior: "classical",
			URL:      base + "/" + r.List + ".list",
			Path:     "./ruleset/" + r.List + ".yaml",
			Interval: 86400,
		}
	}
	cfg.RuleProviders = rps

	// 分组（模板生成）
	cfg.ProxyGroups = buildClashGroups(proxyNames)

	// 规则
	rules := []string{}
	for _, r := range acl4ssrRules {
		rules = append(rules, aclRuleLineClash(r))
	}
	cfg.Rules = rules

	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildLoon 生成 Loon 配置（.conf）。Loon 兼容 Surge 语法，代理行采用位置风格（协议, server, port, key=value...），
// 支持 vmess/vless/trojan/ss/socks5/http（Loon 支持 vless，与 Surfboard 不同；但不支持 gRPC 传输）。
// subURL 非空时首行写入 #!MANAGED-CONFIG 指令，使客户端可自动更新。
// rulesBaseURL 非空（即选择了 ACL4SSR 规则预设）时输出完整分组与分流规则：
// Loon 原生用 [Rule](内置规则) + [Remote Rule](远程列表 url,组) 指向 ACL4SSR 规则源；
// 否则退化为裸订阅（仅节点选择 + FINAL）。
func BuildLoon(items []ExportItem, subURL, rulesBaseURL string) (string, error) {
	var sb strings.Builder
	if subURL != "" {
		sb.WriteString(fmt.Sprintf("#!MANAGED-CONFIG %s interval=60 strict=true\n", subURL))
	}
	// [General]：DNS / 局域网绕过 / geoip 库等基础设置（对齐 Loon 原生订阅样式）
	sb.WriteString(loonGeneralBlock())
	sb.WriteString("[Proxy]\n")
	names := []string{}
	for _, it := range items {
		line, ok := buildLoonProxyLine(it)
		if !ok {
			continue
		}
		sb.WriteString(line + "\n")
		names = append(names, proxyName(it))
	}
	if len(names) == 0 {
		return "", fmt.Errorf("没有 Loon 支持的代理")
	}
	if rulesBaseURL != "" {
		sb.WriteString("\n[Proxy Group]\n")
		sb.WriteString(buildSurgeGroups(names))
		// Loon 原生：[Rule] 放内置规则(GEOIP/FINAL)，[Remote Rule] 直接列远程规则 URL
		sb.WriteString("\n[Rule]\n")
		sb.WriteString(buildSurgeBuiltinRules())
		sb.WriteString("\n[Remote Rule]\n")
		sb.WriteString(buildLoonRemoteRules(rulesBaseURL))
	} else {
		sb.WriteString("\n[Proxy Group]\n")
		sb.WriteString("NodePilot = select, " + strings.Join(names, ", ") + "\n")
		sb.WriteString("\n[Rule]\n")
		sb.WriteString("FINAL,NodePilot\n")
	}
	return sb.String(), nil
}

// surfboardGeneralBlock 返回 Surfboard 的 [General] 基础设置（对齐参考订阅样式）。
func surfboardGeneralBlock() string {
	return "[General]\n" +
		"loglevel=notify\n" +
		"interface=127.0.0.1\n" +
		"ipv6=false\n" +
		"dns-server=system, 223.5.5.5\n" +
		"skip-proxy=192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local\n" +
		"exclude-simple-hostnames=true\n" +
		"enhanced-mode-by-rule=true\n\n"
}

// loonGeneralBlock 返回 Loon 的 [General] 基础设置（DNS、局域网绕过、geoip 库等）。
func loonGeneralBlock() string {
	return "[General]\n" +
		"ipv6=false\n" +
		"dns-server=119.29.29.29, 223.5.5.5\n" +
		"doh-server=https://223.5.5.5/resolve, https://sm2.doh.pub/dns-query\n" +
		"proxy-test-url=http://connectivitycheck.gstatic.com\n" +
		"geoip-url=https://gitlab.com/Masaiki/GeoIP2-CN/-/raw/release/Country.mmdb\n" +
		"bypass-tun=192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, 127.0.0.0/8\n" +
		"skip-proxy=192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local\n" +
		"sni-sniffing=true\n" +
		"disconnect-on-policy-change=true\n" +
		"switch-node-after-failure-times=3\n" +
		"test-timeout=2\n\n"
}

// buildSurgeBuiltinRules 输出 ACL4SSR 模板中的内置规则（GEOIP/FINAL），用于 Loon 的 [Rule] 段。
func buildSurgeBuiltinRules() string {
	var sb strings.Builder
	for _, r := range acl4ssrRules {
		if r.List != "" {
			continue
		}
		sb.WriteString(aclRuleLineSurge(r, "") + "\n")
	}
	return sb.String()
}

// buildLoonRemoteRules 输出 ACL4SSR 的远程规则列表，供 Loon 的 [Remote Rule] 段直接引用（url,组）。
func buildLoonRemoteRules(rulesBaseURL string) string {
	base := strings.TrimRight(rulesBaseURL, "/")
	var sb strings.Builder
	for _, r := range acl4ssrRules {
		if r.List == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s/%s.list,%s\n", base, r.List, r.Group))
	}
	return sb.String()
}

// buildLoonProxyLine 生成单条 Loon 风格代理定义行（Loon 兼容 Surge 语法，采用位置风格：
// 协议, server, port, key=value...，ws-headers 用 "Host: x" 形式）。ok=false 表示该协议/传输不被 Loon 支持。
func buildLoonProxyLine(it ExportItem) (string, bool) {
	name := proxyName(it)
	path := it.WsPath
	if path == "" {
		path = "/v2ray"
	}
	host := it.Host
	if it.Transport == "grpc" {
		return "", false // Loon 不支持 gRPC 传输
	}
	wsPart := func() []string {
		// Loon 与 Surge 一致：ws-headers 用 "Host: x" 形式
		if it.Transport == "ws" {
			if it.TLSEnabled && it.SNI != "" {
				return []string{"ws=true", "ws-path=" + path, fmt.Sprintf("ws-headers=\"Host: %s\"", it.SNI)}
			}
			return []string{"ws=true", "ws-path=" + path}
		}
		return nil
	}
	switch it.Protocol {
	case "vmess":
		// vmess, server, port, username=uuid, vmess-aid=0, vmess-security=auto[, tls=true, sni=host][, ws...]
		parts := []string{
			fmt.Sprintf("%s = vmess, %s, %d", name, host, it.Port),
			"username=" + it.UUID,
			"vmess-aid=0",
			"vmess-security=auto",
		}
		if it.TLSEnabled {
			parts = append(parts, "tls=true", "sni="+it.SNI)
		}
		parts = append(parts, wsPart()...)
		return strings.Join(parts, ", "), true
	case "vless":
		// Loon 支持 vless（与 Surfboard 不同）；flow 视服务端而定，此处不强制写入
		parts := []string{
			fmt.Sprintf("%s = vless, %s, %d", name, host, it.Port),
			"username=" + it.UUID,
		}
		if it.TLSEnabled {
			parts = append(parts, "tls=true", "sni="+it.SNI)
		}
		parts = append(parts, wsPart()...)
		return strings.Join(parts, ", "), true
	case "trojan":
		parts := []string{
			fmt.Sprintf("%s = trojan, %s, %d", name, host, it.Port),
			"password=" + it.UUID,
		}
		if it.TLSEnabled {
			parts = append(parts, "sni="+it.SNI)
		}
		parts = append(parts, wsPart()...)
		return strings.Join(parts, ", "), true
	case "ss":
		return fmt.Sprintf("%s = ss, %s, %d, encrypt-method=aes-256-gcm, password=%s", name, host, it.Port, it.UUID), true
	case "socks", "http":
		pType := it.Protocol
		if it.Protocol == "socks" {
			pType = "socks5"
		}
		if it.TLSEnabled {
			pType = "https" // Loon/ Surge: https 类型即走 TLS
		}
		parts := []string{
			fmt.Sprintf("%s = %s, %s, %d", name, pType, host, it.Port),
		}
		if it.UUID != "" {
			parts = append(parts, "password="+it.UUID)
		}
		return strings.Join(parts, ", "), true
	}
	return "", false
}

type clashProxy struct {
	Name     string                 `yaml:"name"`
	Type     string                 `yaml:"type"`
	Server   string                 `yaml:"server"`
	Port     int                    `yaml:"port"`
	UUID     string                 `yaml:"uuid,omitempty"`
	Password string                 `yaml:"password,omitempty"`
	AlterId  *int                   `yaml:"alterId,omitempty"`
	Cipher   string                 `yaml:"cipher,omitempty"`
	Network  string                 `yaml:"network,omitempty"`
	Tls      bool                   `yaml:"tls,omitempty"`
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
