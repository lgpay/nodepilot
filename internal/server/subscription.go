package server

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
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
		body.Mode = "bare"
	}
	if body.Filters != "" && !json.Valid([]byte(body.Filters)) {
		c.JSON(400, gin.H{"error": "filters 不是合法 JSON"})
		return
	}
	g := model.SubscriptionGroup{
		Name:    body.Name,
		Token:   auth.GenToken(),
		Format:  body.Format,
		Mode:    body.Mode,
		Filters: body.Filters,
		Enabled: true,
	}
	if err := store.DB.Create(&g).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": g.ID, "token": g.Token})
}

func ListSubscriptions(c *gin.Context) {
	var groups []model.SubscriptionGroup
	store.DB.Find(&groups)
	c.JSON(200, groups)
}

func GetSubscriptionDetail(c *gin.Context) {
	id := c.Param("id")
	var g model.SubscriptionGroup
	if err := store.DB.First(&g, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
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
	c.JSON(200, gin.H{"ok": true})
}

func DeleteSubscription(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.SubscriptionGroup{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- 对外订阅端点 /sub/{token} ----

func GetSubscription(c *gin.Context) {
	token := c.Param("token")
	var g model.SubscriptionGroup
	if err := store.DB.Where("token = ?", token).First(&g).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if !g.Enabled {
		c.JSON(403, gin.H{"error": "subscription disabled"})
		return
	}
	items, err := aggregate(g)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var content string
	var ctype string
	acl := g.Mode == "acl4ssr"
	switch g.Format {
	case "clash", "surfboard":
		if acl {
			content, err = subscription.BuildClashACL4SSR(items)
		} else {
			content, err = subscription.BuildClash(items)
		}
		ctype = "application/yaml; charset=utf-8"
	case "loon":
		content, err = subscription.BuildLoon(items, acl)
		ctype = "text/plain; charset=utf-8"
	case "sip008":
		content, err = subscription.BuildSIP008(items)
		ctype = "application/json; charset=utf-8"
	default: // vmess(V2Ray) 等：acl4ssr 模式对 V2Ray 等价裸链接
		content, err = subscription.BuildVMess(items)
		ctype = "text/plain; charset=utf-8"
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.Data(200, ctype, []byte(content))
}

// GetSubscriptionQR 公开端点：返回订阅链接的二维码 PNG（与 /sub/:token 同安全模型，以 token 鉴权）
func GetSubscriptionQR(c *gin.Context) {
	token := c.Param("token")
	var g model.SubscriptionGroup
	if err := store.DB.Where("token = ?", token).First(&g).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if !g.Enabled {
		c.JSON(403, gin.H{"error": "subscription disabled"})
		return
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	subURL := fmt.Sprintf("%s://%s/api/v1/sub/%s", scheme, c.Request.Host, token)
	png, err := qrcode.Encode(subURL, qrcode.Medium, 256)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
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
		store.DB.Where("inbound_id = ? AND enabled = ?", in.ID, true).Find(&clients)
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
