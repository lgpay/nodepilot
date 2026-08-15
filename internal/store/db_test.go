package store

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"nodepilot/internal/model"
)

var memDBCounter atomic.Int64

// newTestDB 打开唯一命名的内存 SQLite（cache=shared 下同名库进程内共享，
// 用唯一名避免多个测试串扰）并建表（单连接）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := fmt.Sprintf("file:memdb_%d?mode=memory&cache=shared", memDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.TrafficStat{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestUpsertTrafficStatAccumulate(t *testing.T) {
	db := newTestDB(t)
	stat := model.TrafficStat{NodeID: 1, InboundID: 2, ClientID: 3, Date: "2026-08-12", UpBytes: 100, DownBytes: 50}
	if err := UpsertTrafficStat(db, stat); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	stat.UpBytes, stat.DownBytes = 10, 20
	if err := UpsertTrafficStat(db, stat); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var rows []model.TrafficStat
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应合并为单行，got %d 行", len(rows))
	}
	if rows[0].UpBytes != 110 || rows[0].DownBytes != 70 {
		t.Errorf("累加错误: up=%d down=%d, want 110/70", rows[0].UpBytes, rows[0].DownBytes)
	}
}

func TestUpsertTrafficStatSeparateKeys(t *testing.T) {
	db := newTestDB(t)
	if err := UpsertTrafficStat(db, model.TrafficStat{NodeID: 1, InboundID: 1, ClientID: 1, Date: "2026-08-12", UpBytes: 1, DownBytes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertTrafficStat(db, model.TrafficStat{NodeID: 1, InboundID: 1, ClientID: 2, Date: "2026-08-12", UpBytes: 2, DownBytes: 2}); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&model.TrafficStat{}).Count(&n)
	if n != 2 {
		t.Errorf("不同 client 应独立成行，got %d", n)
	}
}
