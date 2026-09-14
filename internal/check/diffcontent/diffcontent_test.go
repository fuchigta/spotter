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
	changed   []string
	diffs     map[string]string
	diffCalls *[]string
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
