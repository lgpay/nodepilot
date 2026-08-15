package server

import "testing"

func TestIsValidRuleName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Apple.list", true},
		{"ChinaDomain.list", true},
		{"Ruleset/GoogleFCM.list", true}, // 子目录规则
		{"Ruleset/SteamCN.list", true},
		{"..%2f..%2fetc/passwd.list", false},
		{"../secret.list", false}, // 目录穿越
		{"..list", false},
		{"no-extension", false}, // 缺少 .list
		{"bad name.list", false}, // 空格
		{"x.list/", false},       // 结尾不是 .list
	}
	for _, c := range cases {
		if got := isValidRuleName(c.name); got != c.want {
			t.Errorf("isValidRuleName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
