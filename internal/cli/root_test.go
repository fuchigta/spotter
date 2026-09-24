package cli

import (
	"sort"
	"testing"
)

// TestNewRootCommandRegistersAllSubcommands は、NewRootCommand が組み立てる
// サブコマンドの一覧に漏れが無いことだけを確認するスモークテスト。個々の
// newXxxCommand の中身（フラグや Short 文言）はここでは検証しない。
func TestNewRootCommandRegistersAllSubcommands(t *testing.T) {
	root := NewRootCommand("v0.0.0-test")

	want := []string{"check", "checks", "config", "doctor", "hooks", "range", "skills", "update"}

	got := make([]string, 0, len(root.Commands()))
	for _, c := range root.Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("サブコマンドの数 = %d, want %d（got=%v, want=%v）", len(got), len(want), got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("サブコマンド一覧 = %v, want %v", got, want)
			break
		}
	}
}
