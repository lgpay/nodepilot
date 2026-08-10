package server

import (
	"fmt"
	"log"
	"sync"
	"time"

	"nodepilot/internal/model"
	"nodepilot/internal/notify"
	"nodepilot/internal/store"
)

const alertInterval = 15 * time.Minute

// alerted 去重：key=clientID:reason:YYYY-MM-DD，当天同原因只通知一次，避免扫描器刷屏。
// 由互斥锁保护，避免定时扫描与潜在并发访问产生 data race。
var (
	alertMu sync.Mutex
	alerted = map[string]bool{}
)

// markAlerted 若 key 已标记则返回 false（已通知过），否则标记并返回 true。
func markAlerted(key string) bool {
	alertMu.Lock()
	defer alertMu.Unlock()
	if alerted[key] {
		return false
	}
	alerted[key] = true
	return true
}

// StartAlertScheduler 启动流量超额 / 客户端到期 的定时扫描预警
func StartAlertScheduler() {
	go func() {
		ticker := time.NewTicker(alertInterval)
		scanAlerts()
		for range ticker.C {
			scanAlerts()
		}
	}()
}

func scanAlerts() {
	log.Printf("[alert] scan run (traffic/expiry)")
	checkTrafficLimit()
	checkNodeMonthlyTraffic()
	checkExpiry()
}

// checkTrafficLimit 累计流量超过限额的客户端预警
func checkTrafficLimit() {
	type row struct {
		ClientID uint  `gorm:"column:client_id"`
		Total    int64 `gorm:"column:total"`
	}
	var rows []row
	store.DB.Model(&model.TrafficStat{}).
		Select("client_id, COALESCE(SUM(up_bytes),0)+COALESCE(SUM(down_bytes),0) AS total").
		Where("client_id > 0").
		Group("client_id").Scan(&rows)
	if len(rows) == 0 {
		return
	}
	cids := make([]uint, 0, len(rows))
	for _, r := range rows {
		cids = append(cids, r.ClientID)
	}
	var clients []model.Client
	store.DB.Where("id IN ? AND traffic_limit_bytes > 0", cids).Find(&clients)
	byID := map[uint]model.Client{}
	for _, c := range clients {
		byID[c.ID] = c
	}
	today := time.Now().UTC().Format("2006-01-02")
	for _, r := range rows {
		c, ok := byID[r.ClientID]
		if !ok {
			continue
		}
		if r.Total <= c.TrafficLimitBytes {
			continue
		}
		key := fmt.Sprintf("%d:traffic:%s", c.ID, today)
		if !markAlerted(key) {
			continue
		}
		notify.Dispatch("client_traffic_over", "🟡 流量超额", fmt.Sprintf("客户端 %s (#%d) 已用 %s / 限额 %s",
			c.Alias, c.ID, fmtBytes(r.Total), fmtBytes(c.TrafficLimitBytes)))
	}
}

// checkNodeMonthlyTraffic 节点级月流量控制：达 90% 提醒一次，达 100% 直接停用节点。
// 月流量上限 0 表示不限。统计按 UTC 自然月（date LIKE 'YYYY-MM%'）。
func checkNodeMonthlyTraffic() {
	var nodes []model.Node
	store.DB.Where("enabled = ? AND monthly_traffic_bytes > 0", true).Find(&nodes)
	if len(nodes) == 0 {
		return
	}
	month := time.Now().UTC().Format("2006-01")
	for _, n := range nodes {
		var agg struct {
			Up   int64 `gorm:"column:up"`
			Down int64 `gorm:"column:down"`
		}
		store.DB.Model(&model.TrafficStat{}).
			Select("COALESCE(SUM(up_bytes),0) AS up, COALESCE(SUM(down_bytes),0) AS down").
			Where("node_id = ? AND date LIKE ?", n.ID, month+"%").Scan(&agg)
		used := agg.Up + agg.Down
		limit := n.MonthlyTrafficBytes
		if used >= limit {
			// 100%：直接停用节点
			store.DB.Model(&model.Node{}).Where("id = ?", n.ID).Update("enabled", false)
			key := fmt.Sprintf("%d:node-monthly-off:%s", n.ID, month)
			if markAlerted(key) {
				notify.Dispatch("node_traffic_exhausted", "🔴 节点月流量耗尽", fmt.Sprintf("节点 #%d (%s) 本月已用 %s / 上限 %s，已达 100%%，已自动停用",
					n.ID, n.Name, fmtBytes(used), fmtBytes(limit)))
			}
			continue
		}
		if used >= limit*9/10 {
			// 90%：提醒一次（每月去重）
			key := fmt.Sprintf("%d:node-monthly-90:%s", n.ID, month)
			if markAlerted(key) {
				notify.Dispatch("node_traffic_warning", "🟡 节点月流量预警", fmt.Sprintf("节点 #%d (%s) 本月已用 %s / 上限 %s（约 %.0f%%）",
					n.ID, n.Name, fmtBytes(used), fmtBytes(limit), float64(used)*100/float64(limit)))
			}
		}
	}
}

// checkExpiry 客户端到期 / 临近到期预警
func checkExpiry() {
	var clients []model.Client
	store.DB.Find(&clients)
	now := time.Now()
	soon := now.Add(72 * time.Hour)
	today := now.UTC().Format("2006-01-02")
	for _, c := range clients {
		if c.ExpireTime.IsZero() {
			continue
		}
		if c.ExpireTime.Before(now) {
			key := fmt.Sprintf("%d:expired:%s", c.ID, today)
			if !markAlerted(key) {
				continue
			}
			notify.Dispatch("client_expired", "❌ 客户端已过期", fmt.Sprintf("客户端 %s (#%d) 已于 %s 过期",
				c.Alias, c.ID, c.ExpireTime.Format("2006-01-02 15:04")))
		} else if c.ExpireTime.Before(soon) {
			key := fmt.Sprintf("%d:expiring:%s", c.ID, today)
			if !markAlerted(key) {
				continue
			}
			days := int(c.ExpireTime.Sub(now).Hours() / 24)
			notify.Dispatch("client_expiring", "⏰ 客户端即将到期", fmt.Sprintf("客户端 %s (#%d) 将于 %s 到期（剩约 %d 天）",
				c.Alias, c.ID, c.ExpireTime.Format("2006-01-02 15:04"), days))
		}
	}
}

// fmtBytes 人类可读字节数
func fmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
