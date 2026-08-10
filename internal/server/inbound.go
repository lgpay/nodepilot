package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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
	AutoHeal    bool   `json:"auto_heal"`
	AutoHealInterval int `json:"auto_heal_interval"`
	PortAutoFixed bool `json:"port_auto_fixed"`
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
			AutoHeal:   in.AutoHeal,
			AutoHealInterval: in.AutoHealInterval,
			PortAutoFixed: in.PortAutoFixed,
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
		AutoHeal       *bool   `json:"auto_heal"`       // 端口不通时自动换端口（默认 true）
		AutoHealInterval int   `json:"auto_heal_interval"` // 自动修复最小间隔(秒)，0=不自动修复
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
		AutoHeal:       derefBool(body.AutoHeal, true),
		AutoHealInterval: body.AutoHealInterval,
	}
	if err := validateInbound(in.Protocol, in.TLSEnabled, in.TLSCertID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := store.DB.Create(&in).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	afterInboundSave(in)
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
		AutoHeal       *bool   `json:"auto_heal"`
		AutoHealInterval *int   `json:"auto_heal_interval"`
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
	if body.AutoHeal != nil {
		updates["auto_heal"] = *body.AutoHeal
	}
	if body.AutoHealInterval != nil {
		updates["auto_heal_interval"] = *body.AutoHealInterval
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
	// 用更新后的生效值触发后续动作
	in.TLSEnabled = effTLS
	in.TLSCertID = effCert
	afterInboundSave(in)
	c.JSON(200, gin.H{"ok": true})
}

// afterInboundSave 入站保存后：若使用 TLS 证书，把证书产物分发到该节点并触发配置下发（保存即生效）。
// 免去「保存入站 → 证书页手动分发 → 再下发配置」的步骤。
func afterInboundSave(in model.Inbound) {
	if !in.TLSEnabled || in.TLSCertID == 0 {
		return
	}
	var node model.Node
	if err := store.DB.First(&node, in.NodeID).Error; err != nil {
		log.Printf("[inbound] 节点 #%d 不存在，跳过证书分发", in.NodeID)
		return
	}
	pushCertToNode(node, in.TLSCertID)
	if _, err := syncNode(node); err != nil {
		log.Printf("[inbound] 节点 #%d 配置下发失败: %v", node.ID, err)
	}
}

// pushCertToNode 把证书签发产物推送到单个节点 agent（幂等；失败仅记录，不阻断保存）。
func pushCertToNode(node model.Node, certID uint) {
	if certID == 0 {
		return
	}
	var cert model.Certificate
	if err := store.DB.First(&cert, certID).Error; err != nil {
		log.Printf("[inbound] 证书 #%d 不存在，跳过对节点 %d 的分发", certID, node.ID)
		return
	}
	if cert.Status != "issued" {
		log.Printf("[inbound] 证书 #%d 状态 %s，跳过对节点 %d 的分发", certID, cert.Status, node.ID)
		return
	}
	base := filepath.Join(ctrlCertDir, "certificates", sanitize(cert.Domain))
	certPEM, err1 := os.ReadFile(base + ".crt")
	keyPEM, err2 := os.ReadFile(base + ".key")
	if err1 != nil || err2 != nil {
		log.Printf("[inbound] 读取证书产物失败(crt=%v key=%v)，跳过对节点 %d 的分发", err1, err2, node.ID)
		return
	}
	caPEM, _ := os.ReadFile(base + ".issuer.crt")
	if _, err := agentPut(node, "/agent/v1/cert", map[string]string{
		"cert_pem": string(certPEM),
		"key_pem":  string(keyPEM),
		"ca_pem":   string(caPEM),
	}); err != nil {
		log.Printf("[inbound] 证书分发到节点 %d 失败: %v", node.ID, err)
		return
	}
	log.Printf("[inbound] 证书 #%d 已分发到节点 %d", certID, node.ID)
}

func DeleteInbound(c *gin.Context) {
	id := c.Param("id")
	// 事务内级联清理：clients / traffic_stats / 订阅筛选引用，避免孤儿记录
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("inbound_id = ?", id).Delete(&model.Client{}).Error; err != nil {
			return err
		}
		if err := tx.Where("inbound_id = ?", id).Delete(&model.TrafficStat{}).Error; err != nil {
			return err
		}
		if err := removeInboundFromSubscriptionFilters(tx, id); err != nil {
			return err
		}
		return tx.Delete(&model.Inbound{}, id).Error
	})
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// removeInboundFromSubscriptionFilters 从所有订阅分组的 filters JSON 的 inbound_ids 中移除该入站，
// 保留其余筛选键（node_ids/protocol/tags）。
func removeInboundFromSubscriptionFilters(tx *gorm.DB, inboundID string) error {
	var groups []model.SubscriptionGroup
	if err := tx.Find(&groups).Error; err != nil {
		return err
	}
	var fid uint
	fmt.Sscanf(inboundID, "%d", &fid)
	if fid == 0 {
		return nil
	}
	for _, g := range groups {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(g.Filters), &m); err != nil {
			continue
		}
		var ids []uint
		_ = json.Unmarshal(m["inbound_ids"], &ids)
		changed := false
		out := ids[:0]
		for _, x := range ids {
			if x == fid {
				changed = true
			} else {
				out = append(out, x)
			}
		}
		if !changed {
			continue
		}
		m["inbound_ids"], _ = json.Marshal(out)
		nf, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := tx.Model(&g).Update("filters", string(nf)).Error; err != nil {
			return err
		}
	}
	return nil
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
		TrafficLimitBytes int64  `json:"traffic_limit_bytes"` // 0 不限（历史 -1 亦按不限处理）
		ExpireTime        string `json:"expire_time"`         // RFC3339，可空
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	trafficLimit := body.TrafficLimitBytes
	if trafficLimit < 0 {
		trafficLimit = 0 // 0 = 无限制，归一化历史 -1
	}
	client := model.Client{
		InboundID:         uint(atoiSafe(inboundID)),
		UUID:              configgen.GenUUID(),
		Alias:             body.Alias,
		TrafficLimitBytes: trafficLimit,
		Enabled:           true,
	}
	if body.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, body.ExpireTime); err == nil {
			client.ExpireTime = t
		}
	}
	if err := store.DB.Create(&client).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
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
		lim := *body.TrafficLimitBytes
		if lim < 0 {
			lim = 0 // 0 = 无限制，归一化历史 -1
		}
		updates["traffic_limit_bytes"] = lim
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
		c.JSON(500, gin.H{"error": "internal error"})
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

// derefBool 安全解引用 *bool，nil 时返回默认值
func derefBool(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}
