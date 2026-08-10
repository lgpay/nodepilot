package server

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/secret"
	"nodepilot/internal/store"
	"nodepilot/internal/subscription"
)

// getNode 按 id 取节点（id 可为数字主键）
func getNode(c *gin.Context, id string) (*model.Node, error) {
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		return nil, err
	}
	return &node, nil
}

// ---- 认证 ----

func LoginHandler(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var admin model.Admin
	if err := store.DB.Where("username = ?", body.Username).First(&admin).Error; err != nil ||
		!auth.CheckPassword(body.Password, admin.PasswordHash) {
		c.JSON(401, gin.H{"error": "invalid credentials"})
		return
	}
	token, err := auth.IssueJWT(admin.Username)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, gin.H{"token": token, "must_change_pwd": admin.MustChangePwd})
}

func LogoutHandler(c *gin.Context) {
	// MVP：无服务端会话存储，由客户端丢弃 token
	c.JSON(200, gin.H{"ok": true})
}

// ChangePassword 修改当前管理员密码（需校验旧密码）
func ChangePassword(c *gin.Context) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if body.NewPassword == "" {
		c.JSON(400, gin.H{"error": "新密码不能为空"})
		return
	}
	authz := c.GetHeader("Authorization")
	t := strings.TrimPrefix(authz, "Bearer ")
	claims, err := auth.ParseJWT(t)
	if err != nil {
		c.JSON(401, gin.H{"error": "invalid token"})
		return
	}
	username, _ := claims["sub"].(string)
	var admin model.Admin
	if err := store.DB.Where("username = ?", username).First(&admin).Error; err != nil {
		c.JSON(404, gin.H{"error": "admin not found"})
		return
	}
	if !auth.CheckPassword(body.OldPassword, admin.PasswordHash) {
		c.JSON(401, gin.H{"error": "旧密码不正确"})
		return
	}
	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	if err := store.DB.Model(&admin).Updates(map[string]interface{}{
		"password_hash":   hash,
		"must_change_pwd": false,
	}).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- 节点 CRUD ----

func CreateNode(c *gin.Context) {
	var body struct {
		Name      string `json:"name"`
		Address   string `json:"address"`
		Region    string `json:"region"`
		City      string `json:"city"`
		Tags      string `json:"tags"`
		PortRange string `json:"port_range"`
		MonthlyTrafficBytes int64 `json:"monthly_traffic_bytes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if !validPortRange(body.PortRange) {
		c.JSON(400, gin.H{"error": "端口范围格式不正确（如 10000-20000,30000-40000）"})
		return
	}
	token := auth.GenToken()
	tokenEnc, err := secret.Encrypt(token)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	node := model.Node{
		Name:      strings.TrimSpace(body.Name),
		Address:   strings.TrimSpace(body.Address),
		Region:    strings.TrimSpace(body.Region),
		City:      strings.TrimSpace(body.City),
		Tags:      strings.TrimSpace(body.Tags),
		PortRange: strings.TrimSpace(body.PortRange),
		MonthlyTrafficBytes: body.MonthlyTrafficBytes,
		TokenHash: auth.TokenHash(token),
		TokenEnc:  tokenEnc,
		Enabled:   true,
		Status:    "offline",
		Connectivity: "ok",
	}
	if err := store.DB.Create(&node).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	// token 仅在创建时明文返回一次，用于部署 agent（之后再获取请走 /nodes/:id/install）
	c.JSON(201, gin.H{"id": node.ID, "token": token, "address": node.Address})
}

func ListNodes(c *gin.Context) {
	var nodes []model.Node
	// 不返回 Token 字段
	store.DB.Select("id,name,address,region,city,tags,enabled,status,connectivity,agent_version,last_heartbeat,port_range,monthly_traffic_bytes,expires_at,created_at").
		Find(&nodes)
	for i := range nodes {
		nodes[i].Flag = subscription.FlagEmoji(nodes[i].Region)
	}
	c.JSON(200, nodes)
}

func GetNode(c *gin.Context) {
	id := c.Param("id")
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	var versions []model.ConfigVersion
	store.DB.Where("node_id = ?", node.ID).Order("version desc").Limit(20).Find(&versions)
	node.Flag = subscription.FlagEmoji(node.Region)
	// 不再返回明文 token；需要重新获取部署命令请调用 /nodes/:id/install
	c.JSON(200, gin.H{"node": node, "config_versions": versions})
}

// NodeInstall 生成该节点对应的 agent 一键安装命令：预填管理端地址、节点 token、节点 id、
// agent 监听端口。节点注册后即可把此命令拿到节点服务器执行，自动安装 xray + agent 并接入本管理端。
func NodeInstall(c *gin.Context) {
	id := c.Param("id")
	node, err := getNode(c, id)
	if err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	// 管理端面地址：优先用设置的 panel_base_url；否则取管理员当前访问地址（即节点应回连的地址）。
	panelURL, _ := store.GetSetting("panel_base_url")
	if panelURL == "" {
		scheme := c.GetHeader("X-Forwarded-Proto")
		if scheme == "" {
			if c.Request.TLS != nil {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		panelURL = scheme + "://" + c.Request.Host
	}
	// agent 监听端口：取节点 address 的端口部分；缺省 :8081。
	addr := ":8081"
	if i := strings.LastIndex(node.Address, ":"); i >= 0 {
		addr = ":" + node.Address[i+1:]
	}
	scriptURL := "https://github.com/lgpay/nodepilot/raw/main/scripts/install-agent.sh"
	// token 以密文存储，安装命令需要明文，这里解密后填入（仅管理员可见）
	token, err := secret.Decrypt(node.TokenEnc)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	command := fmt.Sprintf("NP_SERVER=%s NP_TOKEN=%s NP_NODE_ID=%d NP_ADDR=%s bash <(curl -L %s)",
		panelURL, token, node.ID, addr, scriptURL)
	c.JSON(200, gin.H{
		"node_id":   node.ID,
		"token":     token,
		"panel_url": panelURL,
		"agent_addr": addr,
		"script_url": scriptURL,
		"command":   command,
	})
}

func UpdateNode(c *gin.Context) {
	id := c.Param("id")
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	var body struct {
		Name      *string `json:"name"`
		Region    *string `json:"region"`
		City      *string `json:"city"`
		Tags      *string `json:"tags"`
		Enabled   *bool   `json:"enabled"`
		PortRange *string `json:"port_range"`
		MonthlyTrafficBytes *int64 `json:"monthly_traffic_bytes"`
		ExpiresAt *string `json:"expires_at"` // RFC3339；空串=清除（长期有效）
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.Region != nil {
		updates["region"] = *body.Region
	}
	if body.City != nil {
		updates["city"] = *body.City
	}
	if body.Tags != nil {
		updates["tags"] = *body.Tags
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.PortRange != nil {
		if !validPortRange(*body.PortRange) {
			c.JSON(400, gin.H{"error": "端口范围格式不正确（如 10000-20000,30000-40000）"})
			return
		}
		updates["port_range"] = strings.TrimSpace(*body.PortRange)
	}
	if body.MonthlyTrafficBytes != nil {
		v := *body.MonthlyTrafficBytes
		if v < 0 {
			v = 0 // 0 = 无限制，归一化负数
		}
		updates["monthly_traffic_bytes"] = v
	}
	if body.ExpiresAt != nil {
		if *body.ExpiresAt == "" {
			updates["expires_at"] = nil // 清除有效期
		} else {
			t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
			if err != nil {
				c.JSON(400, gin.H{"error": "服务器有效期格式不正确"})
				return
			}
			updates["expires_at"] = t
		}
	}
	store.DB.Model(&node).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteNode(c *gin.Context) {
	id := c.Param("id")
	// 事务内级联删除关联数据，避免孤儿记录
	err := store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", id).Delete(&model.Inbound{}).Error; err != nil {
			return err
		}
		// clients 通过 inbound 间接归属，按节点下所有 inbound 删除
		if err := tx.
			Where("inbound_id IN (SELECT id FROM inbounds WHERE node_id = ?)", id).
			Delete(&model.Client{}).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", id).Delete(&model.ConfigVersion{}).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", id).Delete(&model.TrafficStat{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Node{}, id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// NodeTraffic 返回节点当月已用流量与上限（字节）；月流量上限 0 表示不限。
func NodeTraffic(c *gin.Context) {
	id := c.Param("id")
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	month := time.Now().UTC().Format("2006-01")
	var agg struct {
		Up   int64 `gorm:"column:up"`
		Down int64 `gorm:"column:down"`
	}
	store.DB.Model(&model.TrafficStat{}).
		Select("COALESCE(SUM(up_bytes),0) AS up, COALESCE(SUM(down_bytes),0) AS down").
		Where("node_id = ? AND date LIKE ?", node.ID, month+"%").Scan(&agg)
	c.JSON(200, gin.H{
		"node_id":     node.ID,
		"month":       month,
		"used_bytes":  agg.Up + agg.Down,
		"limit_bytes": node.MonthlyTrafficBytes,
	})
}

// validPortRange 严格校验端口范围格式：空串合法（用全局默认）；非空须为逗号分隔的
// "N" 或 "N-M"，每段端口 1-65535 且 lo<=hi。
func validPortRange(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		var lo, hi int
		if strings.Contains(part, "-") {
			ps := strings.SplitN(part, "-", 2)
			a, err1 := strconv.Atoi(ps[0])
			b, err2 := strconv.Atoi(ps[1])
			if err1 != nil || err2 != nil {
				return false
			}
			lo, hi = a, b
			if lo > hi {
				return false
			}
		} else {
			v, err := strconv.Atoi(part)
			if err != nil {
				return false
			}
			lo, hi = v, v
		}
		if lo < 1 || hi > 65535 {
			return false
		}
	}
	return true
}

// Heartbeat agent 上报状态
func Heartbeat(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		AgentVersion string  `json:"agent_version"`
		Cpu          float64 `json:"cpu"`
		Mem          float64 `json:"mem"`
		XrayRunning  bool    `json:"xray_running"`
	}
	c.ShouldBindJSON(&body)
	store.DB.Model(&model.Node{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        "online",
		"agent_version": body.AgentVersion,
		"last_heartbeat": time.Now(),
	})
	c.JSON(200, gin.H{"ok": true})
}

// Traffic agent 上报按用户流量增量（已用 -reset 取得距上次采集的差值）。
// body: {"stats":[{"email":"<uuid>","up":N,"down":N}]}。email 即 xray 统计键（=client UUID），
// 按 uuid 解析 client/inbound/node，按天累加到 traffic_stats（唯一键已建复合唯一索引）。
func Traffic(c *gin.Context) {
	id := c.Param("id")
	node, err := getNode(c, id)
	if err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	var body struct {
		Stats []struct {
			Email string `json:"email"` // xray 统计键，等于 client UUID
			Up    int64  `json:"up"`
			Down  int64  `json:"down"`
		} `json:"stats"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	date := time.Now().UTC().Format("2006-01-02")

	for _, s := range body.Stats {
		if s.Email == "" {
			continue
		}
		clientID, inboundID, nodeID := uint(0), uint(0), node.ID
		var client model.Client
		if store.DB.Where("uuid = ?", s.Email).First(&client).Error == nil {
			clientID = client.ID
			inboundID = client.InboundID
			var in model.Inbound
			if store.DB.First(&in, inboundID).Error == nil {
				nodeID = in.NodeID
			}
		}
		row := model.TrafficStat{
			NodeID:    nodeID,
			InboundID: inboundID,
			ClientID:  clientID,
			Date:      date,
			UpBytes:   s.Up,
			DownBytes: s.Down,
		}
		if err := store.DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "node_id"}, {Name: "inbound_id"}, {Name: "client_id"}, {Name: "date"},
			},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"up_bytes":   gorm.Expr("up_bytes + ?", s.Up),
				"down_bytes": gorm.Expr("down_bytes + ?", s.Down),
			}),
		}).Create(&row).Error; err != nil {
			log.Printf("[traffic] node=%d upsert failed: %v", node.ID, err)
		}
	}
	c.JSON(200, gin.H{"ok": true})
}
