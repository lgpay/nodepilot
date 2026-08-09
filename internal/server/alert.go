package server

import (
	"fmt"
	"log"
	"time"

	"nodepilot/internal/model"
	"nodepilot/internal/notify"
	"nodepilot/internal/store"
)

const alertInterval = 15 * time.Minute

// alerted 去重：key=clientID:reason:YYYY-MM-DD，当天同原因只通知一次，避免扫描器刷屏。
var alerted = map[string]bool{}

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
	store.DB.Where("id IN ? AND traffic_limit_bytes >= 0", cids).Find(&clients)
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
		if alerted[key] {
			continue
		}
		alerted[key] = true
		notify.Dispatch("🟡 流量超额", fmt.Sprintf("客户端 %s (#%d) 已用 %s / 限额 %s",
			c.Alias, c.ID, fmtBytes(r.Total), fmtBytes(c.TrafficLimitBytes)))
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
			if alerted[key] {
				continue
			}
			alerted[key] = true
			notify.Dispatch("❌ 客户端已过期", fmt.Sprintf("客户端 %s (#%d) 已于 %s 过期",
				c.Alias, c.ID, c.ExpireTime.Format("2006-01-02 15:04")))
		} else if c.ExpireTime.Before(soon) {
			key := fmt.Sprintf("%d:expiring:%s", c.ID, today)
			if alerted[key] {
				continue
			}
			alerted[key] = true
			days := int(c.ExpireTime.Sub(now).Hours() / 24)
			notify.Dispatch("⏰ 客户端即将到期", fmt.Sprintf("客户端 %s (#%d) 将于 %s 到期（剩约 %d 天）",
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
