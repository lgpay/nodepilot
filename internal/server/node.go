package server

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
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
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"token": token})
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
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if err := store.DB.Model(&admin).Update("password_hash", hash).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
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
		Tags      string `json:"tags"`
		PortRange string `json:"port_range"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	token := auth.GenToken()
	node := model.Node{
		Name:      body.Name,
		Address:   body.Address,
		Region:    body.Region,
		Tags:      body.Tags,
		PortRange: body.PortRange,
		Token:     token,
		Enabled:   true,
		Status:    "offline",
		Connectivity: "ok",
	}
	if err := store.DB.Create(&node).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// token 仅在创建时明文返回一次，用于部署 agent
	c.JSON(201, gin.H{"id": node.ID, "token": token, "address": node.Address})
}

func ListNodes(c *gin.Context) {
	var nodes []model.Node
	// 不返回 Token 字段
	store.DB.Select("id,name,address,region,tags,enabled,status,connectivity,agent_version,last_heartbeat,port_range,created_at").
		Find(&nodes)
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
	c.JSON(200, gin.H{"node": node, "token": node.Token, "config_versions": versions})
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
	scriptURL := "https://gitee.com/lgpay/nodepilot/raw/main/scripts/install-agent.sh"
	command := fmt.Sprintf("NP_SERVER=%s NP_TOKEN=%s NP_NODE_ID=%d NP_ADDR=%s bash <(curl -L %s)",
		panelURL, node.Token, node.ID, addr, scriptURL)
	c.JSON(200, gin.H{
		"node_id":   node.ID,
		"token":     node.Token,
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
		Tags      *string `json:"tags"`
		Enabled   *bool   `json:"enabled"`
		PortRange *string `json:"port_range"`
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
	if body.Tags != nil {
		updates["tags"] = *body.Tags
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.PortRange != nil {
		updates["port_range"] = *body.PortRange
	}
	store.DB.Model(&node).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteNode(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Node{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
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
