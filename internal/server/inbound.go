package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"nodepilot/internal/configgen"
	"nodepilot/internal/model"
	"nodepilot/internal/service"
	"nodepilot/internal/store"
)

// ---- 入站 CRUD ----

func ListInbounds(c *gin.Context) {
	id := c.Param("id")
	var inbounds []model.Inbound
	if err := store.DB.Where("node_id = ?", id).Find(&inbounds).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	// 填充每个入站端口的连通状态（探测结果，控制面内存态）
	for i := range inbounds {
		inbounds[i].Connectivity = ps.getConn(inbounds[i].ID)
	}
	c.JSON(200, inbounds)
}

// InboundView 含节点名的入站视图（供订阅分组选择器）
type InboundView struct {
	ID               uint   `json:"id"`
	NodeID           uint   `json:"node_id"`
	NodeName         string `json:"node_name"`
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	Transport        string `json:"transport"`
	TLSEnabled       bool   `json:"tls_enabled"`
	TLSCertID        uint   `json:"tls_cert_id"`
	StreamSettings   string `json:"stream_settings"`
	Fallback         string `json:"fallback"`
	Enabled          bool   `json:"enabled"`
	AutoHeal         bool   `json:"auto_heal"`
	AutoHealInterval int    `json:"auto_heal_interval"`
	PortAutoFixed    bool   `json:"port_auto_fixed"`
	HealCount        int    `json:"heal_count"`   // 自动修复次数
	Connectivity     string `json:"connectivity"` // 入站端口连通状态(ok/fail/空=未知)
}

// ListAllInbounds 列出全部入站并附带所属节点名（供订阅分组精确选择）
func ListAllInbounds(c *gin.Context) {
	var inbounds []model.Inbound
	if err := store.DB.Order("node_id, id").Find(&inbounds).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	// 批量加载节点名，避免 N+1 查询
	nodeNames := map[uint]string{}
	nodeIDs := map[uint]bool{}
	for _, in := range inbounds {
		nodeIDs[in.NodeID] = true
	}
	if len(nodeIDs) > 0 {
		ids := make([]uint, 0, len(nodeIDs))
		for id := range nodeIDs {
			ids = append(ids, id)
		}
		var nodes []model.Node
		if err := store.DB.Where("id IN ?", ids).Find(&nodes).Error; err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}
		for _, n := range nodes {
			nodeNames[n.ID] = n.Name
		}
	}
	views := make([]InboundView, 0, len(inbounds))
	for _, in := range inbounds {
		views = append(views, InboundView{
			ID:               in.ID,
			NodeID:           in.NodeID,
			NodeName:         nodeNames[in.NodeID],
			Name:             in.Name,
			Protocol:         in.Protocol,
			Port:             in.Port,
			Transport:        in.Transport,
			TLSEnabled:       in.TLSEnabled,
			TLSCertID:        in.TLSCertID,
			StreamSettings:   in.StreamSettings,
			Fallback:         in.Fallback,
			Enabled:          in.Enabled,
			AutoHeal:         in.AutoHeal,
			AutoHealInterval: in.AutoHealInterval,
			PortAutoFixed:    in.PortAutoFixed,
			HealCount:        in.HealCount,
			Connectivity:     ps.getConn(in.ID),
		})
	}
	c.JSON(200, views)
}

func CreateInbound(c *gin.Context) {
	nodeID := c.Param("id")
	var body struct {
		Name               *string `json:"name"`
		Protocol           string  `json:"protocol"`
		Port               int     `json:"port"`
		Transport          string  `json:"transport"`
		Security           string  `json:"security"` // "" | none | tls | reality
		TLSEnabled         bool    `json:"tls_enabled"`
		TLSCertID          uint    `json:"cert_id"`
		StreamSettings     string  `json:"stream_settings"`    // JSON 字符串
		Fallback           string  `json:"fallback"`           // JSON 字符串
		AutoHeal           *bool   `json:"auto_heal"`          // 端口不通时自动换端口（默认 true）
		AutoHealInterval   int     `json:"auto_heal_interval"` // 自动修复最小间隔(秒)，0=不自动修复
		RealityDest        string  `json:"reality_dest"`
		RealityServerNames string  `json:"reality_server_names"`
		RealityPrivateKey  string  `json:"reality_private_key"`
		RealityShortIds    string  `json:"reality_short_ids"`
		RealitySpiderX     string  `json:"reality_spider_x"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	in := model.Inbound{
		NodeID:             uint(atoiSafe(nodeID)),
		Name:               derefStr(body.Name),
		Protocol:           body.Protocol,
		Port:               body.Port,
		Transport:          body.Transport,
		Security:           body.Security,
		TLSEnabled:         body.TLSEnabled,
		TLSCertID:          body.TLSCertID,
		StreamSettings:     body.StreamSettings,
		Fallback:           body.Fallback,
		Enabled:            true,
		AutoHeal:           derefBool(body.AutoHeal, true),
		AutoHealInterval:   body.AutoHealInterval,
		RealityDest:        body.RealityDest,
		RealityServerNames: body.RealityServerNames,
		RealityPrivateKey:  body.RealityPrivateKey,
		RealityShortIds:    body.RealityShortIds,
		RealitySpiderX:     body.RealitySpiderX,
	}
	if err := service.ValidateInbound(in.Protocol, in.Transport, in.TLSEnabled, in.TLSCertID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// 归一化安全层并补全 REALITY 参数（校验协议/传输，按需生成密钥/shortId）
	if err := configgen.NormalizeReality(&in); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := store.DB.Create(&in).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	deployment := afterInboundSave(in)
	slog.Info("audit", "action", "inbound_create", "id", in.ID, "node_id", in.NodeID, "protocol", in.Protocol, "port", in.Port)
	c.JSON(201, gin.H{"id": in.ID, "deployment": deployment})
}

func UpdateInbound(c *gin.Context) {
	id := c.Param("id")
	var in model.Inbound
	if err := store.DB.First(&in, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "inbound not found"})
		return
	}
	var body struct {
		Name               *string `json:"name"`
		Protocol           *string `json:"protocol"`
		Port               *int    `json:"port"`
		Transport          *string `json:"transport"`
		Security           *string `json:"security"` // "" | none | tls | reality
		TLSEnabled         *bool   `json:"tls_enabled"`
		TLSCertID          *uint   `json:"cert_id"`
		StreamSettings     *string `json:"stream_settings"`
		Fallback           *string `json:"fallback"`
		Enabled            *bool   `json:"enabled"`
		AutoHeal           *bool   `json:"auto_heal"`
		AutoHealInterval   *int    `json:"auto_heal_interval"`
		RealityDest        *string `json:"reality_dest"`
		RealityServerNames *string `json:"reality_server_names"`
		RealityPrivateKey  *string `json:"reality_private_key"`
		RealityShortIds    *string `json:"reality_short_ids"`
		RealitySpiderX     *string `json:"reality_spider_x"`
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
	if body.Security != nil {
		updates["security"] = *body.Security
	}
	if body.RealityDest != nil {
		updates["reality_dest"] = *body.RealityDest
	}
	if body.RealityServerNames != nil {
		updates["reality_server_names"] = *body.RealityServerNames
	}
	if body.RealityPrivateKey != nil {
		updates["reality_private_key"] = *body.RealityPrivateKey
	}
	if body.RealityShortIds != nil {
		updates["reality_short_ids"] = *body.RealityShortIds
	}
	if body.RealitySpiderX != nil {
		updates["reality_spider_x"] = *body.RealitySpiderX
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
	effTransport := in.Transport
	if body.Transport != nil {
		effTransport = *body.Transport
	}
	if err := service.ValidateInbound(effProto, effTransport, effTLS, effCert); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// 用更新后的生效值归一化安全层 / 补全 REALITY 参数（可能生成私钥/shortId）
	eff := in
	if body.Protocol != nil {
		eff.Protocol = *body.Protocol
	}
	if body.Transport != nil {
		eff.Transport = *body.Transport
	}
	if body.TLSEnabled != nil {
		eff.TLSEnabled = *body.TLSEnabled
	}
	if body.TLSCertID != nil {
		eff.TLSCertID = *body.TLSCertID
	}
	if body.Security != nil {
		eff.Security = *body.Security
	}
	if body.RealityDest != nil {
		eff.RealityDest = *body.RealityDest
	}
	if body.RealityServerNames != nil {
		eff.RealityServerNames = *body.RealityServerNames
	}
	if body.RealityPrivateKey != nil {
		eff.RealityPrivateKey = *body.RealityPrivateKey
	}
	if body.RealityShortIds != nil {
		eff.RealityShortIds = *body.RealityShortIds
	}
	if body.RealitySpiderX != nil {
		eff.RealitySpiderX = *body.RealitySpiderX
	}
	if err := configgen.NormalizeReality(&eff); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// 归一化后的安全层/REALITY 参数回写 updates（确保生成的密钥/shortId/默认值落库）
	updates["security"] = eff.Security
	updates["reality_dest"] = eff.RealityDest
	updates["reality_server_names"] = eff.RealityServerNames
	updates["reality_private_key"] = eff.RealityPrivateKey
	updates["reality_short_ids"] = eff.RealityShortIds
	updates["reality_spider_x"] = eff.RealitySpiderX
	if eff.Security == model.SecurityReality {
		updates["tls_enabled"] = false
	}
	if err := store.DB.Model(&in).Updates(updates).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	// 用更新后的生效值触发后续动作
	in.Security = eff.Security
	in.RealityDest = eff.RealityDest
	in.RealityServerNames = eff.RealityServerNames
	in.RealityPrivateKey = eff.RealityPrivateKey
	in.RealityShortIds = eff.RealityShortIds
	in.RealitySpiderX = eff.RealitySpiderX
	in.TLSEnabled = eff.TLSEnabled
	in.TLSCertID = effCert
	deployment := afterInboundSave(in)
	slog.Info("audit", "action", "inbound_update", "id", in.ID)
	c.JSON(200, gin.H{"ok": true, "deployment": deployment})
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
	slog.Info("audit", "action", "inbound_delete", "id", id)
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
