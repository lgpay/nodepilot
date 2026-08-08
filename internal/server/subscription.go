package server

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
	"nodepilot/internal/subscription"
)

// ---- 订阅 CRUD ----

func CreateSubscription(c *gin.Context) {
	var body struct {
		Name    string                 `json:"name"`
		Format  string                 `json:"format"`
		Filters map[string]interface{} `json:"filters"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if body.Format == "" {
		body.Format = "vmess"
	}
	fb, _ := json.Marshal(body.Filters)
	g := model.SubscriptionGroup{
		Name:    body.Name,
		Token:   auth.GenToken(),
		Format:  body.Format,
		Filters: string(fb),
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
		Name    *string                `json:"name"`
		Format  *string                `json:"format"`
		Filters map[string]interface{} `json:"filters"`
		Enabled *bool                  `json:"enabled"`
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
	if body.Filters != nil {
		fb, _ := json.Marshal(body.Filters)
		updates["filters"] = string(fb)
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
	switch g.Format {
	case "clash":
		content, err = subscription.BuildClash(items)
		ctype = "application/yaml; charset=utf-8"
	case "sip008":
		content, err = subscription.BuildSIP008(items)
		ctype = "application/json; charset=utf-8"
	default:
		content, err = subscription.BuildVMess(items)
		ctype = "text/plain; charset=utf-8"
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.Data(200, ctype, []byte(content))
}

// ---- 聚合：按筛选规则收集可导出客户端 ----

func aggregate(g model.SubscriptionGroup) ([]subscription.ExportItem, error) {
	var f struct {
		NodeIDs []uint   `json:"node_ids"`
		Protocol []string `json:"protocol"`
		Tags     []string `json:"tags"`
	}
	_ = json.Unmarshal([]byte(g.Filters), &f)

	q := store.DB.Where("enabled = ?", true)
	if len(f.Protocol) > 0 {
		q = q.Where("protocol IN ?", f.Protocol)
	}
	if len(f.NodeIDs) > 0 {
		q = q.Where("node_id IN ?", f.NodeIDs)
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
		if len(f.Tags) > 0 && !nodeHasAnyTag(node.Tags, f.Tags) {
			continue
		}
		var clients []model.Client
		store.DB.Where("inbound_id = ? AND enabled = ?", in.ID, true).Find(&clients)
		host := hostOf(node.Address)
		wsPath := parseWsPath(in.StreamSettings)
		for _, cl := range clients {
			items = append(items, subscription.ExportItem{
				Host:       host,
				Port:       in.Port,
				Protocol:   in.Protocol,
				Transport:  transportOf(in.Transport),
				TLSEnabled: in.TLSEnabled,
				WsPath:     wsPath,
				UUID:       cl.UUID,
				Email:      cl.Email,
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
