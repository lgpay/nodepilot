package subscription

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseACLContentBaseline 用仓库内基线 rules/ACL4SSR_Online.ini 验证解析器：
// 解析出的分组/规则应与内置静态快照一致（基线由 GitHub Actions 每日同步上游）。
func TestParseACLContentBaseline(t *testing.T) {
	path := filepath.Join("..", "..", "rules", "ACL4SSR_Online.ini")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("基线文件缺失，跳过: %v", err)
	}
	tpl, err := ParseACLContent(string(data))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(tpl.Groups) != len(defaultTemplate.Groups) {
		t.Errorf("分组数不符: got %d, want %d", len(tpl.Groups), len(defaultTemplate.Groups))
	}
	if len(tpl.Rules) != len(defaultTemplate.Rules) {
		t.Errorf("规则数不符: got %d, want %d", len(tpl.Rules), len(defaultTemplate.Rules))
	}
	// 分组名集合一致
	gotGroups := map[string]bool{}
	for _, g := range tpl.Groups {
		gotGroups[g.Name] = true
	}
	for _, g := range defaultTemplate.Groups {
		if !gotGroups[g.Name] {
			t.Errorf("模板缺少分组 %q", g.Name)
		}
	}
	// 规则列表名集合一致（内置规则单独校验）
	gotLists := map[string]bool{}
	for _, r := range tpl.Rules {
		if r.List != "" {
			gotLists[r.List] = true
		}
	}
	for _, r := range defaultTemplate.Rules {
		if r.List != "" && !gotLists[r.List] {
			t.Errorf("模板缺少规则列表 %q", r.List)
		}
	}
}
