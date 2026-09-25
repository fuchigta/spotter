package configdiff_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/configdiff"
)

func TestLoosingString(t *testing.T) {
	tests := []struct {
		name string
		l    configdiff.Loosening
		want string
	}{
		{"両方ある（例の再現）", configdiff.Loosening{Kind: configdiff.KindLimitRaised, Path: "checks.diff-size.max_lines", Before: "1500", After: "5000"},
			"checks.diff-size.max_lines: 1500 → 5000（上限を上げました）"},
		{"どちらも無い", configdiff.Loosening{Kind: configdiff.KindChanged, Path: "checks.a.foo"},
			"checks.a.foo（変更しました）"},
		{"Before が無い", configdiff.Loosening{Kind: configdiff.KindAllowAdded, Path: "checks.a.exclude", After: "y"},
			"checks.a.exclude: → y（許可を追加しました）"},
		{"After が無い", configdiff.Loosening{Kind: configdiff.KindCheckRemoved, Path: "checks.a", Before: "a"},
			"checks.a: a →（検査が削除されました）"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.l.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoosingStringUnparsable(t *testing.T) {
	l := configdiff.Loosening{Kind: configdiff.KindUnparsable, Path: "比較元", After: "yaml: line 1: ..."}
	if got := l.String(); got == "" {
		t.Fatalf("String() が空文字列でした")
	}
}
