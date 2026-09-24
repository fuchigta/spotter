package diffcontent_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/diffcontent"
	"github.com/fuchigta/spotter/internal/config"
)

// fakeSource はテスト用の固定応答 check.Source。DiffLines を呼ばれたファイルを
// diffCalls に記録し、「paths で絞り込んだファイルは無駄に diff を取りに行かない」ことを検証する。
type fakeSource struct {
	changed       []string
	diffs         map[string]string
	diffCalls     *[]string
	deleted       []string
	deletedCalled *bool
	exists        map[string]bool

	errChanged error
	errDeleted error
	errDiff    error
}

func (f fakeSource) ChangedFiles() ([]string, error) { return f.changed, f.errChanged }
func (f fakeSource) DiffLines(path string) (string, error) {
	if f.diffCalls != nil {
		*f.diffCalls = append(*f.diffCalls, path)
	}
	return f.diffs[path], f.errDiff
}
func (f fakeSource) BlobSize(path string) (int64, error) { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)    { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error) {
	if f.deletedCalled != nil {
		*f.deletedCalled = true
	}
	return f.deleted, f.errDeleted
}
func (f fakeSource) Exists(path string) (bool, error) { return f.exists[path], nil }

func mustNew(t *testing.T, cc config.CheckConfig) *diffcontent.Check {
	t.Helper()
	c, err := diffcontent.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

// TestNewValidation は New() の起動時バリデーションをまとめて確認する（不正な設定は
// 1 パターンごとに 1 分岐ではなく、ここに追加する）。
func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		cc      config.CheckConfig
		wantErr string
	}{
		{
			name:    "deny が 0 件",
			cc:      config.CheckConfig{},
			wantErr: "少なくとも 1 件",
		},
		{
			name:    "reason が無い",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO"}}},
			wantErr: "reason が必要",
		},
		{
			name:    "pattern が無い",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Reason: "抑制"}}},
			wantErr: "pattern が必要",
		},
		{
			name:    "pattern のコンパイルに失敗",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Pattern: "(", Reason: "抑制"}}},
			wantErr: "コンパイルに失敗",
		},
		{
			name:    "on が added/removed 以外",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "changed"}}},
			wantErr: "は未対応です",
		},
		{
			name:    "net は on: added では使えない",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "added", Net: true}}},
			wantErr: "net は on: removed",
		},
		{
			// on を省略すると既定は added になるため、on を書かずに net: true だけ
			// 指定してもエラーになるはず。
			name:    "net は on 省略（既定 added）でも使えない",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", Net: true}}},
			wantErr: "net は on: removed",
		},
		{
			name:    "paths の doublestar パターンが不正",
			cc:      config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", Paths: "["}}},
			wantErr: "パターン",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := diffcontent.New(tt.cc)
			if err == nil {
				t.Fatal("New() はエラーになるはず")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制"}},
	})
	if c.Granularity() != check.GranularityPerCommit {
		t.Errorf("diff-content の granularity は per-commit 固定のはず, got %v", c.Granularity())
	}
}

func TestRunAddedLineMatch(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Pattern: `@ts-ignore`, Reason: "型/lint エラーの抑制"},
		},
	})

	diff := "diff --git a/src/api/client.ts b/src/api/client.ts\n" +
		"--- a/src/api/client.ts\n" +
		"+++ b/src/api/client.ts\n" +
		"@@ -41,0 +42 @@\n" +
		"+// @ts-ignore\n"

	src := fakeSource{
		changed: []string{"src/api/client.ts"},
		diffs:   map[string]string{"src/api/client.ts": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if violations[0].Summary != "型/lint エラーの抑制:" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "src/api/client.ts:42: // @ts-ignore" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunRemovedLineMatch(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Pattern: `^\s*func Test`, Reason: "テストの削除", On: "removed"},
		},
	})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"--- a/foo_test.go\n" +
		"+++ b/foo_test.go\n" +
		"@@ -10 +9,0 @@\n" +
		"-func TestFoo(t *testing.T) {\n"

	src := fakeSource{
		changed: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "foo_test.go:10: func TestFoo(t *testing.T) {" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunAddedLineDoesNotMatchOnRemoved(t *testing.T) {
	// on: removed のルールは追加行を見ない（逆も然り）ことの確認。
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Pattern: `func Test`, Reason: "テストの削除", On: "removed"},
		},
	})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"--- a/foo_test.go\n" +
		"+++ b/foo_test.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+func TestFoo(t *testing.T) {}\n"

	src := fakeSource{
		changed: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("追加行は on:removed に一致しないはず, got %v", violations)
	}
}

func TestRunPathsRestriction(t *testing.T) {
	var diffCalls []string
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Pattern: `\.only\(`, Reason: "テストの絞り込み", Paths: "**/*.spec.js"},
		},
	})

	diff := "diff --git a/a.spec.js b/a.spec.js\n" +
		"--- a/a.spec.js\n" +
		"+++ b/a.spec.js\n" +
		"@@ -0,0 +1 @@\n" +
		"+it.only('x', () => {})\n"

	src := fakeSource{
		changed:   []string{"a.spec.js", "b.go"},
		diffs:     map[string]string{"a.spec.js": diff},
		diffCalls: &diffCalls,
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	for _, f := range diffCalls {
		if f == "b.go" {
			t.Errorf("paths に一致しない b.go の DiffLines は呼ばれないはず, calls=%v", diffCalls)
		}
	}
}

func TestRunFirstRuleWinsOnSameLine(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Pattern: `TODO`, Reason: "先勝ちルール"},
			{Pattern: `TODO: fix`, Reason: "後勝ちルール（採用されない）"},
		},
	})

	diff := "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n" +
		"+++ b/a.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+// TODO: fix this\n"

	src := fakeSource{
		changed: []string{"a.go"},
		diffs:   map[string]string{"a.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if violations[0].Summary != "先勝ちルール:" {
		t.Errorf("最初に一致したルールの reason が使われるはず, got %q", violations[0].Summary)
	}
}

func TestRunBinaryDiffIsIgnored(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: `.`, Reason: "何にでも一致する"}},
	})

	diff := "diff --git a/img.png b/img.png\n" +
		"Binary files a/img.png and b/img.png differ\n"

	src := fakeSource{
		changed: []string{"img.png"},
		diffs:   map[string]string{"img.png": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("バイナリの差分は +/- 行を持たないため違反 0 件のはず, got %v", violations)
	}
}

func TestRunNoViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: `@ts-ignore`, Reason: "抑制"}},
	})
	src := fakeSource{changed: []string{"a.ts"}, diffs: map[string]string{"a.ts": ""}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("違反が無ければ nil のはず, got %v", violations)
	}
}

func TestRunDeletedFileWithRemovedContent(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: `@ts-ignore`, Reason: "抑制", On: "removed"}},
	})

	diff := "diff --git a/src/api/client.ts b/src/api/client.ts\n" +
		"deleted file mode 100644\n" +
		"--- a/src/api/client.ts\n" +
		"+++ /dev/null\n" +
		"@@ -1,3 +0,0 @@\n" +
		"-// @ts-ignore\n" +
		"-export async function fetch() {}\n"

	src := fakeSource{
		deleted: []string{"src/api/client.ts"},
		diffs:   map[string]string{"src/api/client.ts": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("削除ファイルの removed 行も検査対象のはず, got %d件: %v", len(violations), violations)
	}
	if violations[0].Summary != "抑制:" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
}

func TestRunSkipsDeletedFilesWhenNoRemovedRule(t *testing.T) {
	// deny のどのルールも on: removed（既定は on: added）を使っていなければ、
	// 削除ファイルの差分は追加行を持たず発火し得ないため、DeletedFiles を呼ばずに済ませる。
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制"}},
	})

	called := false
	src := fakeSource{
		changed:       []string{"a.go"},
		diffs:         map[string]string{"a.go": "@@ -1,0 +1 @@\n+TODO\n"},
		deleted:       []string{"b.go"},
		deletedCalled: &called,
	}
	if _, err := c.Run(check.Context{Source: src}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if called {
		t.Error("on: removed のルールが無ければ DeletedFiles は呼ばれないはず")
	}
}

func TestRunCallsDeletedFilesWhenRemovedRuleExists(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "removed"}},
	})

	called := false
	src := fakeSource{
		changed:       []string{"a.go"},
		diffs:         map[string]string{"a.go": ""},
		deletedCalled: &called,
	}
	if _, err := c.Run(check.Context{Source: src}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !called {
		t.Error("on: removed のルールがあれば DeletedFiles が呼ばれるはず")
	}
}

func TestRunDoesNotWriteIntoChangedFilesCapacity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "removed"}},
	})

	// 余剰容量を持つスライスを ChangedFiles として返し、削除ファイルを足すときに
	// その容量へ書き込まれていないことを確かめる。
	backing := []string{"a.go", "untouched"}
	src := fakeSource{
		changed: backing[:1],
		deleted: []string{"b.go"},
		diffs:   map[string]string{"a.go": "", "b.go": ""},
	}
	if _, err := c.Run(check.Context{Source: src}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if backing[1] != "untouched" {
		t.Errorf("ChangedFiles が返したスライスの容量に書き込まれました: %q", backing[1])
	}
}

func TestRunChangedFilesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制"}}})
	if _, err := c.Run(check.Context{Source: fakeSource{errChanged: errors.New("boom")}}); err == nil {
		t.Fatal("ChangedFiles がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunDeletedFilesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "removed"}}})
	if _, err := c.Run(check.Context{Source: fakeSource{errDeleted: errors.New("boom")}}); err == nil {
		t.Fatal("DeletedFiles がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunDiffLinesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制"}}})
	src := fakeSource{changed: []string{"a.go"}, errDiff: errors.New("boom")}
	if _, err := c.Run(check.Context{Source: src}); err == nil {
		t.Fatal("DiffLines がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunSameReasonAcrossFilesAreMergedIntoOneViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: `@ts-ignore`, Reason: "型/lint エラーの抑制"}},
	})

	diffA := "diff --git a/a.ts b/a.ts\n" +
		"--- a/a.ts\n" +
		"+++ b/a.ts\n" +
		"@@ -0,0 +1 @@\n" +
		"+// @ts-ignore\n"
	diffB := "diff --git a/b.ts b/b.ts\n" +
		"--- a/b.ts\n" +
		"+++ b/b.ts\n" +
		"@@ -0,0 +1 @@\n" +
		"+// @ts-ignore\n"

	src := fakeSource{
		changed: []string{"a.ts", "b.ts"},
		diffs:   map[string]string{"a.ts": diffA, "b.ts": diffB},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("同じ理由の違反は別々のファイルでも 1 つにまとまるはず, got %d: %v", len(violations), violations)
	}
	want := []string{
		"a.ts:1: // @ts-ignore",
		"b.ts:1: // @ts-ignore",
	}
	got := violations[0].Files
	if len(got) != len(want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Files[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// netTestRule は以下の net テスト群で共通して使うルール（テスト関数の削除を狙う想定）。
func netTestRule() config.DenyRule {
	return config.DenyRule{
		Pattern: `^\s*func Test\w+\(`,
		Reason:  "テストの削除",
		On:      "removed",
		Net:     true,
	}
}

// TestRunNet は on: removed + net の判定パターン（改名の許容、純粋な削除・ファイルごと
// 削除・別ファイルへの移動の違反、net 無しでの回帰）をまとめて確認する。
func TestRunNet(t *testing.T) {
	tests := []struct {
		name    string
		net     bool
		changed []string
		deleted []string
		diffs   map[string]string
		want    []string // nil は違反 0 件を表す
	}{
		{
			name:    "改名（削除1・追加1、同ファイル）は net なら違反にならない",
			net:     true,
			changed: []string{"foo_test.go"},
			diffs: map[string]string{"foo_test.go": "diff --git a/foo_test.go b/foo_test.go\n" +
				"--- a/foo_test.go\n" +
				"+++ b/foo_test.go\n" +
				"@@ -10 +10 @@\n" +
				"-func TestOld(t *testing.T) {\n" +
				"+func TestNew(t *testing.T) {\n"},
			want: nil,
		},
		{
			name:    "追加を伴わない純粋な削除は net でも違反になる",
			net:     true,
			changed: []string{"foo_test.go"},
			diffs: map[string]string{"foo_test.go": "diff --git a/foo_test.go b/foo_test.go\n" +
				"--- a/foo_test.go\n" +
				"+++ b/foo_test.go\n" +
				"@@ -10 +9,0 @@\n" +
				"-func TestOld(t *testing.T) {\n"},
			want: []string{"foo_test.go:10: func TestOld(t *testing.T) {"},
		},
		{
			name:    "削除2・追加1は net でも違反になり削除行を全部報告する",
			net:     true,
			changed: []string{"foo_test.go"},
			diffs: map[string]string{"foo_test.go": "diff --git a/foo_test.go b/foo_test.go\n" +
				"--- a/foo_test.go\n" +
				"+++ b/foo_test.go\n" +
				"@@ -10,2 +9,1 @@\n" +
				"-func TestA(t *testing.T) {\n" +
				"-func TestB(t *testing.T) {\n" +
				"+func TestC(t *testing.T) {\n"},
			want: []string{
				"foo_test.go:10: func TestA(t *testing.T) {",
				"foo_test.go:11: func TestB(t *testing.T) {",
			},
		},
		{
			name:    "ファイルごと削除は net でも違反になる",
			net:     true,
			deleted: []string{"foo_test.go"},
			diffs: map[string]string{"foo_test.go": "diff --git a/foo_test.go b/foo_test.go\n" +
				"deleted file mode 100644\n" +
				"--- a/foo_test.go\n" +
				"+++ /dev/null\n" +
				"@@ -1,2 +0,0 @@\n" +
				"-func TestA(t *testing.T) {}\n" +
				"-func TestB(t *testing.T) {}\n"},
			want: []string{
				"foo_test.go:1: func TestA(t *testing.T) {}",
				"foo_test.go:2: func TestB(t *testing.T) {}",
			},
		},
		{
			name:    "別ファイルへの移動は移動元がファイルごとの判定で違反のまま",
			net:     true,
			changed: []string{"a_test.go", "b_test.go"},
			diffs: map[string]string{
				"a_test.go": "diff --git a/a_test.go b/a_test.go\n" +
					"--- a/a_test.go\n" +
					"+++ b/a_test.go\n" +
					"@@ -10 +9,0 @@\n" +
					"-func TestMoved(t *testing.T) {}\n",
				"b_test.go": "diff --git a/b_test.go b/b_test.go\n" +
					"--- a/b_test.go\n" +
					"+++ b/b_test.go\n" +
					"@@ -0,0 +1 @@\n" +
					"+func TestMoved(t *testing.T) {}\n",
			},
			want: []string{"a_test.go:10: func TestMoved(t *testing.T) {}"},
		},
		{
			name:    "net を付けなければ削除・追加が同数でも違反になる",
			net:     false,
			changed: []string{"foo_test.go"},
			diffs: map[string]string{"foo_test.go": "diff --git a/foo_test.go b/foo_test.go\n" +
				"--- a/foo_test.go\n" +
				"+++ b/foo_test.go\n" +
				"@@ -10 +10 @@\n" +
				"-func TestOld(t *testing.T) {\n" +
				"+func TestNew(t *testing.T) {\n"},
			want: []string{"foo_test.go:10: func TestOld(t *testing.T) {"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := netTestRule()
			rule.Net = tt.net
			c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{rule}})

			src := fakeSource{changed: tt.changed, deleted: tt.deleted, diffs: tt.diffs}
			violations, err := c.Run(check.Context{Source: src})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}

			if tt.want == nil {
				if violations != nil {
					t.Errorf("違反 0 件のはず, got %v", violations)
				}
				return
			}
			if len(violations) != 1 {
				t.Fatalf("違反は 1 件のはず, got %d件: %v", len(violations), violations)
			}
			got := violations[0].Files
			if len(got) != len(tt.want) {
				t.Fatalf("Files = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("Files[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
