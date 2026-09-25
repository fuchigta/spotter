package configdiff_test

import (
	"sort"
	"testing"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/configdiff"
)

func TestParseEmptyDocuments(t *testing.T) {
	for _, data := range []string{"", "# comment\n", "   \n\n", "~\n"} {
		snap, err := configdiff.Parse([]byte(data))
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", data, err)
		}
		if keys := snap.CheckKeysOfType("doc-sync"); len(keys) != 0 {
			t.Fatalf("Parse(%q): 空のドキュメントなのに CheckKeysOfType が %v を返しました", data, keys)
		}
	}
}

func TestParseUnparsable(t *testing.T) {
	for _, data := range []string{"5\n", "- 1\n- 2\n", "checks: [1, 2\n"} {
		if _, err := configdiff.Parse([]byte(data)); err == nil {
			t.Fatalf("Parse(%q) がエラーになりませんでした", data)
		}
	}
}

func TestCheckKeysOfType(t *testing.T) {
	snap, err := configdiff.Parse([]byte("checks: {a: {type: doc-sync}, b: {type: unwanted-files}, c: {type: doc-sync}}\n"))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	got := snap.CheckKeysOfType("doc-sync")
	want := []string{"a", "c"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("CheckKeysOfType(doc-sync) = %v, want %v", got, want)
	}
	if got := snap.CheckKeysOfType("no-such-type"); len(got) != 0 {
		t.Fatalf("CheckKeysOfType(no-such-type) = %v, want empty", got)
	}
}

func boolPtr(b bool) *bool { return &b }

// TestResolveExemptAgreesWithConfig は Snapshot.ResolveExempt が config.Config.ResolveExempt
// と同じ結果になることを確かめる。config.Load を通らない設定（未知の type）でも
// 失敗しないことも合わせて確認する。
func TestResolveExemptAgreesWithConfig(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		cfg  config.Config
	}{
		{"システム既定（doc-sync）", "checks: {a: {type: doc-sync}}\n",
			config.Config{Checks: map[string]config.CheckConfig{"a": {Type: "doc-sync"}}}},
		{"システム既定（commit-subject は免除不可）", "checks: {a: {type: commit-subject}}\n",
			config.Config{Checks: map[string]config.CheckConfig{"a": {Type: "commit-subject"}}}},
		{"type の default で上書き",
			"types: {doc-sync: {default: {exempt: {enable: false, trailer: Custom-Trailer}}}}\nchecks: {a: {type: doc-sync}}\n",
			config.Config{
				Types:  map[string]config.TypeConfig{"doc-sync": {Default: &config.TypeDefault{Exempt: &config.ExemptConfig{Enable: boolPtr(false), Trailer: "Custom-Trailer"}}}},
				Checks: map[string]config.CheckConfig{"a": {Type: "doc-sync"}},
			}},
		{"checks.<key>.exempt が type の default をさらに上書き",
			"types: {doc-sync: {default: {exempt: {enable: false}}}}\nchecks: {a: {type: doc-sync, exempt: {enable: true, trailer: Per-Check}}}\n",
			config.Config{
				Types:  map[string]config.TypeConfig{"doc-sync": {Default: &config.TypeDefault{Exempt: &config.ExemptConfig{Enable: boolPtr(false)}}}},
				Checks: map[string]config.CheckConfig{"a": {Type: "doc-sync", Exempt: &config.ExemptConfig{Enable: boolPtr(true), Trailer: "Per-Check"}}},
			}},
		{"config.Load が拒否する未知の type でも解決できる", "checks: {a: {type: totally-unknown-type}}\n",
			config.Config{Checks: map[string]config.CheckConfig{"a": {Type: "totally-unknown-type"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, err := configdiff.Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			wantEnable, wantTrailer := tt.cfg.ResolveExempt("a", tt.cfg.Checks["a"])
			gotEnable, gotTrailer := snap.ResolveExempt("a")
			if gotEnable != wantEnable || gotTrailer != wantTrailer {
				t.Fatalf("ResolveExempt(a) = (%v, %q), want (%v, %q)", gotEnable, gotTrailer, wantEnable, wantTrailer)
			}
		})
	}
}

// find は ls の中から指定した Path の Loosening を 1 件探す。
func find(t *testing.T, ls []configdiff.Loosening, path string) configdiff.Loosening {
	t.Helper()
	for _, l := range ls {
		if l.Path == path {
			return l
		}
	}
	t.Fatalf("Path %q の Loosening が見つかりません: %+v", path, ls)
	return configdiff.Loosening{}
}

func TestDiffUnparsable(t *testing.T) {
	tests := []struct {
		name         string
		base, target string
		wantPath     string
	}{
		{"比較元", "checks: [1, 2\n", "checks: {}\n", "比較元"},
		{"終点", "checks: {}\n", "checks: [1, 2\n", "終点"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := configdiff.Diff([]byte(tt.base), []byte(tt.target))
			if len(ls) != 1 || ls[0].Kind != configdiff.KindUnparsable || ls[0].Path != tt.wantPath {
				t.Fatalf("Diff(...) = %+v, want 1 件・Path=%q", ls, tt.wantPath)
			}
		})
	}
}

// TestDiffKinds は Diff が各 Kind を正しい Path に付けて報告することを、緩和ではない
// ケース（wantPath が空）も含めて確かめる。config.Load なら拒否する壊れた形
// （数値のはずが文字列、要素がマッピングでないルール配列、NaN、checks の値が
// マッピングでない）でもパニックしないことも合わせて見る。
func TestDiffKinds(t *testing.T) {
	tests := []struct {
		name         string
		base, target string
		wantPath     string
		wantKind     configdiff.Kind
	}{
		{"検査が消えた", "checks: {a: {type: doc-sync}}\n", "checks: {}\n", "checks.a", configdiff.KindCheckRemoved},
		{"type を変更した", "checks: {a: {type: doc-sync}}\n", "checks: {a: {type: commit-subject}}\n", "checks.a.type", configdiff.KindTypeChanged},
		{"免除を有効にした", "checks: {a: {type: doc-sync, exempt: {enable: false}}}\n", "checks: {a: {type: doc-sync}}\n", "checks.a.exempt.enable", configdiff.KindExemptEnabled},
		{"上限を上げた", "checks: {a: {type: diff-size, max_lines: 100}}\n", "checks: {a: {type: diff-size, max_lines: 200}}\n", "checks.a.max_lines", configdiff.KindLimitRaised},
		{"上限を消した（無限大扱い）", "checks: {a: {type: diff-size, max_lines: 100}}\n", "checks: {a: {type: diff-size}}\n", "checks.a.max_lines", configdiff.KindLimitRaised},
		{"上限が小数でも比較できる", "checks: {a: {type: diff-size, max_lines: 10.5}}\n", "checks: {a: {type: diff-size, max_lines: 20.5}}\n", "checks.a.max_lines", configdiff.KindLimitRaised},
		{"ルールが削除された", "checks: {a: {type: doc-sync, pairs: [{paths: x, doc: y}]}}\n", "checks: {a: {type: doc-sync, pairs: []}}\n", "checks.a.pairs", configdiff.KindRuleRemoved},
		{"ルール配列の要素がマッピングでなくても報告できる", "checks: {a: {type: doc-sync, pairs: [1, 2]}}\n", "checks: {a: {type: doc-sync, pairs: [1]}}\n", "checks.a.pairs", configdiff.KindRuleRemoved},
		{"許可を追加した", "checks: {a: {type: doc-sync, exclude: [x]}}\n", "checks: {a: {type: doc-sync, exclude: [x, y]}}\n", "checks.a.exclude", configdiff.KindAllowAdded},
		{"対象を削除した", "checks: {a: {type: doc-paths, path_prefixes: [x, y]}}\n", "checks: {a: {type: doc-paths, path_prefixes: [x]}}\n", "checks.a.path_prefixes", configdiff.KindTargetRemoved},
		{"check_anchors を無効にした", "checks: {a: {type: doc-links, check_anchors: true}}\n", "checks: {a: {type: doc-links}}\n", "checks.a.check_anchors", configdiff.KindTurnedOff},
		{"required_version を下げた", "required_version: v1.2.0\n", "required_version: v1.0.0\n", "required_version", configdiff.KindVersionLowered},
		{"required_version を消した", "required_version: v1.2.0\n", "checks: {}\n", "required_version", configdiff.KindVersionLowered},
		{"トップレベルの未知のキーが変わった", "foo: 1\n", "foo: 2\n", "foo", configdiff.KindChanged},
		{"NaN のような JSON にできない値でもパニックしない", "foo: .nan\n", "foo: 1\n", "foo", configdiff.KindChanged},
		{"command 型のオプションが変わった", "types: {my-cmd: {command: bash, default: {granularity: worktree}}}\nchecks: {a: {type: my-cmd, option: 1}}\n", "types: {my-cmd: {command: bash, default: {granularity: worktree}}}\nchecks: {a: {type: my-cmd, option: 2}}\n", "checks.a.option", configdiff.KindChanged},
		{"command 型の command が変わった", "types: {my-cmd: {command: bash, default: {granularity: worktree}}}\nchecks: {a: {type: my-cmd}}\n", "types: {my-cmd: {command: sh, default: {granularity: worktree}}}\nchecks: {a: {type: my-cmd}}\n", "types.my-cmd.command", configdiff.KindChanged},
		{"参照されている type の未知のキーが変わった", "types: {my-cmd: {command: bash, foo: 1, default: {granularity: worktree}}}\nchecks: {a: {type: my-cmd}}\n", "types: {my-cmd: {command: bash, foo: 2, default: {granularity: worktree}}}\nchecks: {a: {type: my-cmd}}\n", "types.my-cmd.foo", configdiff.KindChanged},
		{"exempt.trailer の明示値が変わった", "checks: {a: {type: doc-sync, exempt: {trailer: Foo}}}\n", "checks: {a: {type: doc-sync, exempt: {trailer: Bar}}}\n", "checks.a.exempt.trailer", configdiff.KindChanged},
		{"改名は同じか厳しい別キーとして許す", "checks: {old: {type: doc-sync, exclude: [x]}}\n", "checks: {new: {type: doc-sync, exclude: [x]}}\n", "", ""},
		{"companion のスカラー表記と reason の違いは緩和にならない", "checks: {a: {type: companion-files, companions: [{paths: x, companion: y, reason: r1}]}}\n", "checks: {a: {type: companion-files, companions: [{paths: x, companion: [y], reason: r2}]}}\n", "", ""},
		{"参照されていない type の変更は見ない", "types: {unused: {command: bash, default: {granularity: worktree}}}\n", "types: {unused: {command: sh, default: {granularity: worktree}}}\n", "", ""},
		{"max_lines が数値でなければ無限大として扱う", "checks: {a: {type: diff-size, max_lines: many}}\n", "checks: {a: {type: diff-size, max_lines: 5}}\n", "", ""},
		{"組み込み type の未知のキーが両側で同じなら報告しない", "checks: {a: {type: doc-sync, 未知のキー: true}}\n", "checks: {a: {type: doc-sync, 未知のキー: true}}\n", "", ""},
		{"checks の値がマッピングでない候補は改名先の探索で読み飛ばす", "checks: {a: {type: doc-sync}}\n", "checks: {x: 5, y: {type: commit-subject}}\n", "checks.a", configdiff.KindCheckRemoved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := configdiff.Diff([]byte(tt.base), []byte(tt.target))
			if tt.wantPath == "" {
				if len(ls) != 0 {
					t.Fatalf("緩和が無いはずが報告されました: %+v", ls)
				}
				return
			}
			l := find(t, ls, tt.wantPath)
			if l.Kind != tt.wantKind {
				t.Fatalf("Path %q: Kind = %v, want %v (全体: %+v)", tt.wantPath, l.Kind, tt.wantKind, ls)
			}
		})
	}
}

// TestDiffSortedByPath は結果が Path 順に安定してソートされていることを確かめる。
func TestDiffSortedByPath(t *testing.T) {
	ls := configdiff.Diff([]byte("checks: {b: {type: doc-sync}, a: {type: doc-sync}}\n"), []byte("checks: {}\n"))
	paths := make([]string, len(ls))
	for i, l := range ls {
		paths[i] = l.Path
	}
	if !sort.StringsAreSorted(paths) {
		t.Fatalf("Path でソートされていません: %v", paths)
	}
}

func TestDiffStrings(t *testing.T) {
	tests := []struct {
		name         string
		base, target string
		want         []string
	}{
		{
			"検査の削除は消えたキーだけを示す",
			"checks: {a: {type: doc-sync}}\n", "checks: {}\n",
			[]string{"checks.a（検査が削除されました）"},
		},
		{
			"上限の引き上げは前後の値を示す",
			"checks: {a: {type: diff-size, max_lines: 1500}}\n", "checks: {a: {type: diff-size, max_lines: 5000}}\n",
			[]string{"checks.a.max_lines: 1500 → 5000（上限を上げました）"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, l := range configdiff.Diff([]byte(tt.base), []byte(tt.target)) {
				got = append(got, l.String())
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}
