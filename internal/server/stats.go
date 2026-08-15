package server

import (
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// StatsOverview 聚合流量统计：今日总量、各节点累计、各客户端累计、最近 30 天趋势。
// GET /api/v1/stats/overview （authed）
func StatsOverview(c *gin.Context) {
	today := time.Now().UTC().Format("2006-01-02")
	start := time.Now().UTC().AddDate(0, 0, -29).Format("2006-01-02")

	// 今日全站总量
	var todayAgg struct {
		Up   int64 `gorm:"column:up"`
		Down int64 `gorm:"column:down"`
	}
	store.DB.Model(&model.TrafficStat{}).
		Select("COALESCE(SUM(up_bytes),0) AS up, COALESCE(SUM(down_bytes),0) AS down").
		Where("date = ?", today).Scan(&todayAgg)

	// 各节点累计
	type kv struct {
		NodeID uint  `gorm:"column:node_id"`
		Up     int64 `gorm:"column:up"`
		Down   int64 `gorm:"column:down"`
	}
	var nodeRows []kv
	store.DB.Model(&model.TrafficStat{}).
		Select("node_id, SUM(up_bytes) AS up, SUM(down_bytes) AS down").
		Group("node_id").Scan(&nodeRows)
	nameByNode := map[uint]string{}
	limitByNode := map[uint]int64{}
	for _, n := range nodeRows {
		nameByNode[n.NodeID] = ""
	}
	if len(nameByNode) > 0 {
		var nodes []model.Node
		store.DB.Where("id IN ?", keys(nameByNode)).Find(&nodes)
		for _, n := range nodes {
			nameByNode[n.ID] = n.Name
			limitByNode[n.ID] = n.MonthlyTrafficBytes
		}
	}
	nodeStats := make([]gin.H, 0, len(nodeRows))
	for _, r := range nodeRows {
		nodeStats = append(nodeStats, gin.H{
			"node_id":              r.NodeID,
			"name":                 nameByNode[r.NodeID],
			"up":                   r.Up,
			"down":                 r.Down,
			"monthly_limit_bytes":  limitByNode[r.NodeID],
		})
	}

	// 各客户端累计（client_id>0 表示已归属）
	type ck struct {
		ClientID  uint  `gorm:"column:client_id"`
		InboundID uint  `gorm:"column:inbound_id"`
		NodeID    uint  `gorm:"column:node_id"`
		Up        int64 `gorm:"column:up"`
		Down      int64 `gorm:"column:down"`
	}
	var clientRows []ck
	store.DB.Model(&model.TrafficStat{}).
		Select("client_id, inbound_id, node_id, SUM(up_bytes) AS up, SUM(down_bytes) AS down").
		Where("client_id > 0").
		Group("client_id, inbound_id, node_id").Scan(&clientRows)
	clientByID := map[uint]model.Client{}
	cids := make([]uint, 0, len(clientRows))
	for _, r := range clientRows {
		cids = append(cids, r.ClientID)
	}
	if len(cids) > 0 {
		var clients []model.Client
		store.DB.Where("id IN ?", cids).Find(&clients)
		for _, cl := range clients {
			clientByID[cl.ID] = cl
		}
	}
	clientStats := make([]gin.H, 0, len(clientRows))
	for _, r := range clientRows {
		cl := clientByID[r.ClientID]
		limit := int64(0) // 0 = 无限制
		if cl.ID != 0 && cl.TrafficLimitBytes > 0 {
			limit = cl.TrafficLimitBytes
		}
		clientStats = append(clientStats, gin.H{
			"client_id": r.ClientID,
			"alias":     cl.Alias,
			"inbound_id": r.InboundID,
			"node_id":   r.NodeID,
			"up":        r.Up,
			"down":      r.Down,
			"traffic_limit_bytes": limit,
		})
	}

	// 最近 30 天趋势
	type dv struct {
		Date  string `gorm:"column:date"`
		Up    int64  `gorm:"column:up"`
		Down  int64  `gorm:"column:down"`
	}
	var dailyRows []dv
	store.DB.Model(&model.TrafficStat{}).
		Select("date, SUM(up_bytes) AS up, SUM(down_bytes) AS down").
		Where("date >= ?", start).
		Group("date").Order("date").Scan(&dailyRows)
	daily := make([]gin.H, 0, len(dailyRows))
	for _, r := range dailyRows {
		daily = append(daily, gin.H{"date": r.Date, "up": r.Up, "down": r.Down})
	}

	c.JSON(200, gin.H{
		"today":   gin.H{"up": todayAgg.Up, "down": todayAgg.Down},
		"nodes":   nodeStats,
		"clients": clientStats,
		"daily":   daily,
	})
}

// keys 提取 map 的键切片
func keys(m map[uint]string) []uint {
	out := make([]uint, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
