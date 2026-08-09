package server

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/configgen"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// validateInbound 在服务端校验入站配置，避免生成非法 xray 配置导致节点 xray 崩溃。
// trojan 协议必须启用 TLS 且指定证书（cert_id>0）：xray 不接受无 TLS 的 trojan，
// 且证书缺省路径 /root/cert 在节点上不存在，二者任一缺失都会拖垮整个节点（一个坏入站使整份配置校验失败）。
func validateInbound(protocol string, tlsEnabled bool, certID uint) error {
	if protocol == "trojan" {
		if !tlsEnabled {
			return fmt.Errorf("trojan 入站必须启用 TLS（tls_enabled=true）")
		}
		if certID == 0 {
			return fmt.Errorf("trojan 入站启用 TLS 时必须指定证书（cert_id>0）")
		}
	}
	return nil
}

// ---- 入站 CRUD ----

func ListInbounds(c *gin.Context) {
	id := c.Param("id")
	var inbounds []model.Inbound
	store.DB.Where("node_id = ?", id).Find(&inbounds)
	c.JSON(200, inbounds)
}

// InboundView 含节点名的入站视图（供订阅分组选择器）
type InboundView struct {
	ID          uint   `json:"id"`
	NodeID      uint   `json:"node_id"`
	NodeName    string `json:"node_name"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Port        int    `json:"port"`
	Transport   string `json:"transport"`
	TLSEnabled  bool   `json:"tls_enabled"`
	Enabled     bool   `json:"enabled"`
}

// ListAllInbounds 列出全部入站并附带所属节点名（供订阅分组精确选择）
func ListAllInbounds(c *gin.Context) {
	var inbounds []model.Inbound
	store.DB.Order("node_id, id").Find(&inbounds)
	nodeNames := map[uint]string{}
	for _, in := range inbounds {
		if _, ok := nodeNames[in.NodeID]; !ok {
			var n model.Node
			if err := store.DB.First(&n, in.NodeID).Error; err == nil {
				nodeNames[in.NodeID] = n.Name
			}
		}
	}
	views := make([]InboundView, 0, len(inbounds))
	for _, in := range inbounds {
		views = append(views, InboundView{
			ID:         in.ID,
			NodeID:     in.NodeID,
			NodeName:   nodeNames[in.NodeID],
			Name:       in.Name,
			Protocol:   in.Protocol,
			Port:       in.Port,
			Transport:  in.Transport,
			TLSEnabled: in.TLSEnabled,
			Enabled:    in.Enabled,
		})
	}
	c.JSON(200, views)
}

func CreateInbound(c *gin.Context) {
	nodeID := c.Param("id")
	var body struct {
		Name           *string `json:"name"`
		Protocol       string  `json:"protocol"`
		Port           int     `json:"port"`
		Transport      string  `json:"transport"`
		TLSEnabled     bool    `json:"tls_enabled"`
		TLSCertID      uint    `json:"cert_id"`
		StreamSettings string  `json:"stream_settings"` // JSON 字符串
		Fallback       string  `json:"fallback"`        // JSON 字符串
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	in := model.Inbound{
		NodeID:         uint(atoiSafe(nodeID)),
		Name:           derefStr(body.Name),
		Protocol:       body.Protocol,
		Port:           body.Port,
		Transport:      body.Transport,
		TLSEnabled:     body.TLSEnabled,
		TLSCertID:      body.TLSCertID,
		StreamSettings: body.StreamSettings,
		Fallback:       body.Fallback,
		Enabled:        true,
	}
	if err := validateInbound(in.Protocol, in.TLSEnabled, in.TLSCertID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := store.DB.Create(&in).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": in.ID})
}

func UpdateInbound(c *gin.Context) {
	id := c.Param("id")
	var in model.Inbound
	if err := store.DB.First(&in, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "inbound not found"})
		return
	}
	var body struct {
		Name           *string `json:"name"`
		Protocol       *string `json:"protocol"`
		Port           *int    `json:"port"`
		Transport      *string `json:"transport"`
		TLSEnabled     *bool   `json:"tls_enabled"`
		TLSCertID      *uint   `json:"cert_id"`
		StreamSettings *string `json:"stream_settings"`
		Fallback       *string `json:"fallback"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.Protocol != nil {
		updates["protocol"] = *body.Protocol
	}
	if body.Port != nil {
		updates["port"] = *body.Port
	}
	if body.Transport != nil {
		updates["transport"] = *body.Transport
	}
	if body.TLSEnabled != nil {
		updates["tls_enabled"] = *body.TLSEnabled
	}
	if body.TLSCertID != nil {
		updates["tls_cert_id"] = *body.TLSCertID
	}
	if body.StreamSettings != nil {
		updates["stream_settings"] = *body.StreamSettings
	}
	if body.Fallback != nil {
		updates["fallback"] = *body.Fallback
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	// 校验更新后的最终状态（trojan 必须 TLS + 证书），防止生成非法配置拖垮节点
	effProto := in.Protocol
	if body.Protocol != nil {
		effProto = *body.Protocol
	}
	effTLS := in.TLSEnabled
	if body.TLSEnabled != nil {
		effTLS = *body.TLSEnabled
	}
	effCert := in.TLSCertID
	if body.TLSCertID != nil {
		effCert = *body.TLSCertID
	}
	if err := validateInbound(effProto, effTLS, effCert); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	store.DB.Model(&in).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteInbound(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Inbound{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- 用户/客户端 CRUD ----

func ListClients(c *gin.Context) {
	id := c.Param("id")
	var clients []model.Client
	store.DB.Where("inbound_id = ?", id).Find(&clients)
	c.JSON(200, clients)
}

func CreateClient(c *gin.Context) {
	inboundID := c.Param("id")
	var body struct {
		Alias             string `json:"alias"`
		TrafficLimitBytes int64  `json:"traffic_limit_bytes"` // -1 不限
		ExpireTime        string `json:"expire_time"`         // RFC3339，可空
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	client := model.Client{
		InboundID:         uint(atoiSafe(inboundID)),
		UUID:              configgen.GenUUID(),
		Alias:             body.Alias,
		TrafficLimitBytes: body.TrafficLimitBytes,
		Enabled:           true,
	}
	if body.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, body.ExpireTime); err == nil {
			client.ExpireTime = t
		}
	}
	if err := store.DB.Create(&client).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": client.ID, "uuid": client.UUID})
}

func UpdateClient(c *gin.Context) {
	id := c.Param("id")
	var client model.Client
	if err := store.DB.First(&client, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "client not found"})
		return
	}
	var body struct {
		Alias             *string `json:"alias"`
		TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
		Enabled           *bool   `json:"enabled"`
		ExpireTime        *string `json:"expire_time"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Alias != nil {
		updates["alias"] = *body.Alias
	}
	if body.TrafficLimitBytes != nil {
		updates["traffic_limit_bytes"] = *body.TrafficLimitBytes
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.ExpireTime != nil && *body.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, *body.ExpireTime); err == nil {
			updates["expire_time"] = t
		}
	}
	store.DB.Model(&client).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteClient(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Client{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// atoiSafe 将路径参数转为 uint 前的 int
func atoiSafe(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// derefStr 安全解引用 *string，nil 时返回空串
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
