package server

import (
	"testing"
	"time"

	"nodepilot/internal/model"
)

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
