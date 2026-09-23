package diffcontent_test

import (
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
}

func (f fakeSource) ChangedFiles() ([]string, error) { return f.changed, nil }
func (f fakeSource) DiffLines(path string) (string, error) {
	if f.diffCalls != nil {
		*f.diffCalls = append(*f.diffCalls, path)
	}
	return f.diffs[path], nil
}
func (f fakeSource) BlobSize(path string) (int64, error) { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)    { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error) {
	if f.deletedCalled != nil {
		*f.deletedCalled = true
	}
	return f.deleted, nil
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

func TestNewEmptyDenyIsError(t *testing.T) {
	if _, err := diffcontent.New(config.CheckConfig{}); err == nil {
		t.Fatal("deny が 0 件なら New() はエラーになるはず")
	}
}

func TestNewMissingReasonIsError(t *testing.T) {
	if _, err := diffcontent.New(config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO"}},
	}); err == nil {
		t.Fatal("reason が無ければ New() はエラーになるはず")
	}
}

func TestNewMissingPatternIsError(t *testing.T) {
	if _, err := diffcontent.New(config.CheckConfig{
		Deny: []config.DenyRule{{Reason: "抑制"}},
	}); err == nil {
		t.Fatal("pattern が無ければ New() はエラーになるはず")
	}
}

func TestNewInvalidPatternIsError(t *testing.T) {
	if _, err := diffcontent.New(config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "(", Reason: "抑制"}},
	}); err == nil {
		t.Fatal("pattern のコンパイルに失敗したら New() はエラーになるはず")
	}
}

func TestNewInvalidOnIsError(t *testing.T) {
	if _, err := diffcontent.New(config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "changed"}},
	}); err == nil {
		t.Fatal("on が added/removed 以外なら New() はエラーになるはず")
	}
}

func TestNewNetOnAddedIsError(t *testing.T) {
	if _, err := diffcontent.New(config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", On: "added", Net: true}},
	}); err == nil {
		t.Fatal("net は on: removed でなければ New() はエラーになるはず")
	}
}

func TestNewNetOnDefaultAddedIsError(t *testing.T) {
	// on を省略すると既定は added になるため、on を書かずに net: true だけ指定しても
	// エラーになるはず。
	if _, err := diffcontent.New(config.CheckConfig{
		Deny: []config.DenyRule{{Pattern: "TODO", Reason: "抑制", Net: true}},
	}); err == nil {
		t.Fatal("on 省略（既定 added）で net: true なら New() はエラーになるはず")
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

// netTestRule は以下の net テスト群で共通して使うルール（テスト関数の削除を狙う想定）。
func netTestRule() config.DenyRule {
	return config.DenyRule{
		Pattern: `^\s*func Test\w+\(`,
		Reason:  "テストの削除",
		On:      "removed",
		Net:     true,
	}
}

func TestRunNetRenameIsAllowed(t *testing.T) {
	// 同じファイル内でテスト関数を改名（削除 1・追加 1、どちらも pattern に一致）した場合、
	// net なら削除行数が追加行数を上回らないため違反にならない。
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{netTestRule()}})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"--- a/foo_test.go\n" +
		"+++ b/foo_test.go\n" +
		"@@ -10 +10 @@\n" +
		"-func TestOld(t *testing.T) {\n" +
		"+func TestNew(t *testing.T) {\n"

	src := fakeSource{
		changed: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("削除・追加が同数の改名は net なら違反にならないはず, got %v", violations)
	}
}

func TestRunNetRemovedOnlyIsViolation(t *testing.T) {
	// 追加を伴わない純粋な削除（削除 1・追加 0）は net でも従来どおり違反になる。
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{netTestRule()}})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"--- a/foo_test.go\n" +
		"+++ b/foo_test.go\n" +
		"@@ -10 +9,0 @@\n" +
		"-func TestOld(t *testing.T) {\n"

	src := fakeSource{
		changed: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("追加を伴わない削除は違反になるはず, got %d件: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "foo_test.go:10: func TestOld(t *testing.T) {" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunNetMoreRemovedThanAddedReportsAllRemovedLines(t *testing.T) {
	// 削除 2・追加 1 は net でも違反になり、どれが「本当に消えた」かは区別できないため
	// 一致した削除行を全部報告する。
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{netTestRule()}})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"--- a/foo_test.go\n" +
		"+++ b/foo_test.go\n" +
		"@@ -10,2 +9,1 @@\n" +
		"-func TestA(t *testing.T) {\n" +
		"-func TestB(t *testing.T) {\n" +
		"+func TestC(t *testing.T) {\n"

	src := fakeSource{
		changed: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d件: %v", len(violations), violations)
	}
	want := []string{
		"foo_test.go:10: func TestA(t *testing.T) {",
		"foo_test.go:11: func TestB(t *testing.T) {",
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

func TestRunNetWholeFileDeletionIsViolation(t *testing.T) {
	// ファイルごと削除された場合は追加行が 0 なので、net でも従来どおり違反になる。
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{netTestRule()}})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"deleted file mode 100644\n" +
		"--- a/foo_test.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-func TestA(t *testing.T) {}\n" +
		"-func TestB(t *testing.T) {}\n"

	src := fakeSource{
		deleted: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 || len(violations[0].Files) != 2 {
		t.Fatalf("ファイルごと削除は削除行が全て違反になるはず, got %v", violations)
	}
}

func TestRunNetMoveToAnotherFileIsStillViolation(t *testing.T) {
	// 別のファイルへの移動（A から削除、B に追加）はファイルごとの判定なので、
	// A 側は違反のまま（意図的な保守側の選択）。
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{netTestRule()}})

	diffA := "diff --git a/a_test.go b/a_test.go\n" +
		"--- a/a_test.go\n" +
		"+++ b/a_test.go\n" +
		"@@ -10 +9,0 @@\n" +
		"-func TestMoved(t *testing.T) {}\n"
	diffB := "diff --git a/b_test.go b/b_test.go\n" +
		"--- a/b_test.go\n" +
		"+++ b/b_test.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+func TestMoved(t *testing.T) {}\n"

	src := fakeSource{
		changed: []string{"a_test.go", "b_test.go"},
		diffs: map[string]string{
			"a_test.go": diffA,
			"b_test.go": diffB,
		},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("移動元ファイル単独では追加行が無いため違反になるはず, got %d件: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "a_test.go:10: func TestMoved(t *testing.T) {}" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunWithoutNetRenameStillViolates(t *testing.T) {
	// net を付けなければ、削除・追加が同数でも従来どおり違反になる（回帰確認）。
	rule := netTestRule()
	rule.Net = false
	c := mustNew(t, config.CheckConfig{Deny: []config.DenyRule{rule}})

	diff := "diff --git a/foo_test.go b/foo_test.go\n" +
		"--- a/foo_test.go\n" +
		"+++ b/foo_test.go\n" +
		"@@ -10 +10 @@\n" +
		"-func TestOld(t *testing.T) {\n" +
		"+func TestNew(t *testing.T) {\n"

	src := fakeSource{
		changed: []string{"foo_test.go"},
		diffs:   map[string]string{"foo_test.go": diff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("net が無ければ改名でも違反になるはず, got %d件: %v", len(violations), violations)
	}
}
