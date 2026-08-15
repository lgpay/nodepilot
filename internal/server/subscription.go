package server

import (
	"encoding/json"
	"log/slog"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/secret"
	"nodepilot/internal/store"
	"nodepilot/internal/subscription"
)

// ---- 订阅 CRUD ----

func CreateSubscription(c *gin.Context) {
	var body struct {
		Name    string `json:"name"`
		Format  string `json:"format"`
		Mode    string `json:"mode"`
		Filters string `json:"filters"` // 原始 JSON 字符串（与 Web 发送格式一致）
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if body.Format == "" {
		body.Format = "vmess"
	}
	if body.Mode == "" {
		body.Mode = "none"
	}
	if body.Filters != "" && !json.Valid([]byte(body.Filters)) {
		c.JSON(400, gin.H{"error": "filters 不是合法 JSON"})
		return
	}
	token := auth.GenToken()
	tokenEnc, err := secret.Encrypt(token)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	g := model.SubscriptionGroup{
		Name:    body.Name,
		Token:   tokenEnc, // 密文存储，仅创建时明文返回一次
		Format:  body.Format,
		Mode:    body.Mode,
		Filters: body.Filters,
		Enabled: true,
	}
	if err := store.DB.Create(&g).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	slog.Info("audit", "action", "subscription_create", "id", g.ID, "name", g.Name, "format", g.Format)
	c.JSON(201, gin.H{"id": g.ID, "token": token})
}

func ListSubscriptions(c *gin.Context) {
	var groups []model.SubscriptionGroup
	store.DB.Find(&groups)
	// token 以密文存储，返回给前端前解密为明文（用于拼订阅链接）
	for i := range groups {
		if t, err := secret.Decrypt(groups[i].Token); err == nil {
			groups[i].Token = t
		}
	}
	c.JSON(200, groups)
}

func GetSubscriptionDetail(c *gin.Context) {
	id := c.Param("id")
	var g model.SubscriptionGroup
	if err := store.DB.First(&g, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if t, err := secret.Decrypt(g.Token); err == nil {
		g.Token = t
	}
	items, _ := aggregate(g)
	c.JSON(200, gin.H{"group": g, "members": items})
}

func UpdateSubscription(c *gin.Context) {
	id := c.Param("id")
	var g model.SubscriptionGroup
	if err := store.DB.First(&g, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	var body struct {
		Name    *string `json:"name"`
		Format  *string `json:"format"`
		Mode    *string `json:"mode"`
		Filters *string `json:"filters"` // 原始 JSON 字符串
		Enabled *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.Format != nil {
		updates["format"] = *body.Format
	}
	if body.Mode != nil {
		updates["mode"] = *body.Mode
	}
	if body.Filters != nil {
		if !json.Valid([]byte(*body.Filters)) {
			c.JSON(400, gin.H{"error": "filters 不是合法 JSON"})
			return
		}
		updates["filters"] = *body.Filters
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	store.DB.Model(&g).Updates(updates)
	slog.Info("audit", "action", "subscription_update", "id", g.ID)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteSubscription(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.SubscriptionGroup{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	slog.Info("audit", "action", "subscription_delete", "id", id)
	c.JSON(200, gin.H{"ok": true})
}

// ---- 对外订阅端点 /sub/{token} ----

// schemeHost 返回请求的 scheme://host（含端口），用于构造对外可访问的绝对 URL。
func schemeHost(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

// findSubscriptionByToken 按明文 token 查找订阅分组：token 以密文存储，
// 逐个解密比对（订阅组数量少，遍历成本可忽略）。
func findSubscriptionByToken(token string) (model.SubscriptionGroup, bool) {
	var groups []model.SubscriptionGroup
	store.DB.Find(&groups)
	for _, g := range groups {
		// 兼容旧库：能解密则比对明文；解密失败（历史明文 token）直接比对存储值
		if t, err := secret.Decrypt(g.Token); err == nil {
			if t == token {
				return g, true
			}
		} else if g.Token == token {
			return g, true
		}
	}
	return model.SubscriptionGroup{}, false
}

func GetSubscription(c *gin.Context) {
	token := c.Param("token")
	g, ok := findSubscriptionByToken(token)
	if !ok {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if !g.Enabled {
		c.JSON(403, gin.H{"error": "subscription disabled"})
		return
	}
	items, err := aggregate(g)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	// Surfboard 严格遵循 Surge 配置格式，不支持 vless / Trojan-gRPC / ssr 等协议
	// （官方 FAQ 明确），且它并不解析 Clash YAML 的 proxies: 段，只认 Surge 的 [Proxy] 段。
	// 因此 surfboard 格式走 BuildSurfboard（Surge 语法）。这里预先剔除不被支持的协议/传输，
	// 避免 BuildSurfboard 里残留不支持类型导致整份订阅解析失败。
	// Loon 支持 vless，但不支持 gRPC/ssr；clash 保留全协议（Clash.Meta 支持 vless/gRPC）。
	if g.Format == "surfboard" {
		kept := make([]subscription.ExportItem, 0, len(items))
		for _, it := range items {
			if it.Protocol == "vless" || it.Protocol == "ssr" || it.Transport == "grpc" {
				continue
			}
			kept = append(kept, it)
		}
		items = kept
	} else if g.Format == "loon" {
		kept := make([]subscription.ExportItem, 0, len(items))
		for _, it := range items {
			if it.Protocol == "ssr" || it.Transport == "grpc" {
				continue
			}
			kept = append(kept, it)
		}
		items = kept
	}
	// 规则预设：mode 即“规则预设”键。none/bare=无规则(裸订阅)；acl4ssr/acl4ssr_online=ACL4SSR_Online。
	// 选中预设时，把 ACL4SSR 规则源基址(GitHub 原版，可用 Setting acl4ssr_base 覆盖)传给各构建器，
	// 客户端据此直接拉取 ACL4SSR 规则列表(.list 为 classical 格式，Clash/Surge 通用)。
	useRules := g.Mode != "none" && g.Mode != "bare" && g.Mode != ""
	subURL := schemeHost(c) + "/api/v1/sub/" + token
	var rulesBaseURL string
	if useRules {
		// 默认走本面板自托管规则镜像（/api/v1/rules/:name，服务端从 ACL4SSR 源拉取并缓存），
		// 避免客户端直连 GitHub 慢/不稳；可用 Setting acl4ssr_base 指向自定义镜像或上游源覆盖。
		rulesBaseURL = schemeHost(c) + "/api/v1/rules"
		if v, err := store.GetSetting("acl4ssr_base"); err == nil && v != "" {
			rulesBaseURL = v
		}
	}
	var content string
	var ctype string
	switch g.Format {
	case "surfboard":
		content, err = subscription.BuildSurfboard(items, subURL, rulesBaseURL)
		ctype = "text/plain; charset=utf-8"
	case "loon":
		geoipURL := subscription.DefaultLoonGeoIPURL
		if v, err := store.GetSetting("loon_geoip_url"); err == nil && v != "" {
			geoipURL = v
		}
		content, err = subscription.BuildLoon(items, subURL, rulesBaseURL, geoipURL)
		ctype = "text/plain; charset=utf-8"
	case "clash":
		if useRules {
			content, err = subscription.BuildClashACL4SSR(items, rulesBaseURL)
		} else {
			content, err = subscription.BuildClash(items)
		}
		ctype = "application/yaml; charset=utf-8"
	case "sip008":
		content, err = subscription.BuildSIP008(items)
		ctype = "application/json; charset=utf-8"
	default: // vmess(V2Ray) 等：acl4ssr 模式对 V2Ray 等价裸链接
		content, err = subscription.BuildVMess(items)
		ctype = "text/plain; charset=utf-8"
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.Data(200, ctype, []byte(content))
}

// GetSubscriptionQR 公开端点：返回订阅链接的二维码 PNG（与 /sub/:token 同安全模型，以 token 鉴权）
func GetSubscriptionQR(c *gin.Context) {
	token := c.Param("token")
	g, ok := findSubscriptionByToken(token)
	if !ok {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if !g.Enabled {
		c.JSON(403, gin.H{"error": "subscription disabled"})
		return
	}
	subURL := schemeHost(c) + "/api/v1/sub/" + token
	png, err := qrcode.Encode(subURL, qrcode.Medium, 256)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.Data(200, "image/png", png)
}

// ---- 聚合：按筛选规则收集可导出客户端 ----

func aggregate(g model.SubscriptionGroup) ([]subscription.ExportItem, error) {
	var f struct {
		NodeIDs    []uint   `json:"node_ids"`
		Protocol   []string `json:"protocol"`
		Tags       []string `json:"tags"`
		InboundIDs []uint   `json:"inbound_ids"`
		ClientIDs  []uint   `json:"client_ids"` // 精确到客户端(可跨入站)
	}
	_ = json.Unmarshal([]byte(g.Filters), &f)

	q := store.DB.Where("enabled = ?", true)
	if len(f.InboundIDs) > 0 {
		// 精确选择具体入站（按别名勾选）
		q = q.Where("id IN ?", f.InboundIDs)
	} else {
		if len(f.Protocol) > 0 {
			q = q.Where("protocol IN ?", f.Protocol)
		}
		if len(f.NodeIDs) > 0 {
			q = q.Where("node_id IN ?", f.NodeIDs)
		}
	}
	var inbounds []model.Inbound
	q.Find(&inbounds)

	items := []subscription.ExportItem{}
	for _, in := range inbounds {
		var node model.Node
		if err := store.DB.First(&node, in.NodeID).Error; err != nil {
			continue
		}
		if !node.Enabled {
			continue
		}
		// tags 过滤仅在未指定具体入站时生效（避免双重限制）
		if len(f.InboundIDs) == 0 && len(f.Tags) > 0 && !nodeHasAnyTag(node.Tags, f.Tags) {
			continue
		}
		var clients []model.Client
		if len(f.ClientIDs) > 0 {
			// 精确到客户端：仅取选中的客户端（按 id 过滤）
			store.DB.Where("inbound_id = ? AND enabled = ? AND id IN ?", in.ID, true, f.ClientIDs).Find(&clients)
		} else {
			store.DB.Where("inbound_id = ? AND enabled = ?", in.ID, true).Find(&clients)
		}
		host := hostOf(node.Address)
		wsPath := parseWsPath(in.StreamSettings)
		sni := ""
		if in.TLSEnabled {
			sni = host
		}
		for _, cl := range clients {
			items = append(items, subscription.ExportItem{
				Host:       host,
				Port:       in.Port,
				Protocol:   in.Protocol,
				Transport:  transportOf(in.Transport),
				TLSEnabled: in.TLSEnabled,
				WsPath:     wsPath,
				SNI:        sni,
				UUID:       cl.UUID,
				Alias:      cl.Alias,
				Region:     subscription.RegionCode(node.Region), // 订阅对外统一用英文 ISO 码
			})
		}
	}
	return items, nil
}

// ---- 工具 ----

func hostOf(address string) string {
	if h, _, err := net.SplitHostPort(address); err == nil {
		return h
	}
	return address
}

func transportOf(t string) string {
	if t == "" {
		return "tcp"
	}
	return t
}

func parseWsPath(streamSettings string) string {
	if streamSettings == "" {
		return ""
	}
	var s struct {
		WsPath     string `json:"wsPath"`
		WsSettings struct {
			Path string `json:"path"`
		} `json:"wsSettings"`
	}
	if err := json.Unmarshal([]byte(streamSettings), &s); err != nil {
		return ""
	}
	if s.WsSettings.Path != "" {
		return s.WsSettings.Path
	}
	return s.WsPath
}

func nodeHasAnyTag(tags string, wanted []string) bool {
	if tags == "" {
		return false
	}
	have := strings.Split(tags, ",")
	for _, w := range wanted {
		for _, h := range have {
			if strings.TrimSpace(h) == strings.TrimSpace(w) {
				return true
			}
		}
	}
	return false
}
