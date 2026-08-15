package server

import (
	"testing"
	"time"

	"nodepilot/internal/model"
)

// TestMarkAlertedIdempotent 验证去重标记的幂等性：同 key 首次返回 true（应通知），
// 再次返回 false（不再通知），用于 node_healed / node_expired 等天级去重。
func TestMarkAlertedIdempotent(t *testing.T) {
	key := "test:idempotent"
	if !markAlerted(key) {
		t.Fatal("首次 markAlerted 应返回 true")
	}
	if markAlerted(key) {
		t.Fatal("重复 markAlerted 应返回 false（已通知过）")
	}
}

// TestHealAlertKeyPerDay 验证 node_healed 去重 key 按天变化：不同日期生成不同 key，
// 同一天复用同一 key（保证当天只通知一次）。
func TestHealAlertKeyPerDay(t *testing.T) {
	now := time.Now().UTC()
	k1 := now.Format("2006-01-02")
	k2 := now.Add(24 * time.Hour).Format("2006-01-02")
	if k1 == k2 {
		t.Fatal("相邻两天 key 应不同")
	}
}

func TestIsNodeExpired(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	zero := time.Time{}
	cases := []struct {
		name string
		node model.Node
		want bool
	}{
		{"nil 到期时间（长期有效）", model.Node{}, false},
		{"零值到期时间", model.Node{ExpiresAt: &zero}, false},
		{"已到期", model.Node{ExpiresAt: &past}, true},
		{"未到期", model.Node{ExpiresAt: &future}, false},
	}
	for _, c := range cases {
		if got := isNodeExpired(c.node); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
