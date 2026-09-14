package commitintent_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/commitintent"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	changed []string
	diffs   map[string]string
}

func (f fakeSource) ChangedFiles() ([]string, error) { return f.changed, nil }
func (f fakeSource) DiffLines(path string) (string, error) {
	return f.diffs[path], nil
}
func (f fakeSource) BlobSize(path string) (int64, error) { return 0, nil }

func mustNew(t *testing.T, cc config.CheckConfig) *commitintent.Check {
	t.Helper()
	c, err := commitintent.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

func TestNewEmptyRulesIsError(t *testing.T) {
	if _, err := commitintent.New(config.CheckConfig{}); err == nil {
		t.Fatal("rules が 0 件なら New() はエラーになるはず")
	}
}

func TestNewMissingTypesIsError(t *testing.T) {
	if _, err := commitintent.New(config.CheckConfig{
		Rules: []config.CommitIntentRule{{Allow: []string{"**/*.md"}}},
	}); err == nil {
		t.Fatal("types が無ければ New() はエラーになるはず")
	}
}

func TestNewMissingConditionIsError(t *testing.T) {
	if _, err := commitintent.New(config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}}},
	}); err == nil {
		t.Fatal("allow/require/deny_diff がどれも無ければ New() はエラーになるはず")
	}
}

func TestNewInvalidDenyDiffIsError(t *testing.T) {
	if _, err := commitintent.New(config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"refactor"}, DenyDiff: "("}},
	}); err == nil {
		t.Fatal("deny_diff のコンパイルに失敗したら New() はエラーになるはず")
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	if c.Granularity() != check.GranularityPerCommit {
		t.Errorf("commit-intent の granularity は per-commit 固定のはず, got %v", c.Granularity())
	}
}

func TestRunAllowViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Allow: []string{"**/*.md", "docs/**"}, Reason: "docs はドキュメントだけを変更する"},
		},
	})

	src := fakeSource{changed: []string{"README.md", "internal/foo.go"}}
	violations, err := c.Run(check.Context{Message: "docs: READMEを直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if violations[0].Summary != "docs はドキュメントだけを変更する:" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/foo.go" {
		t.Errorf("Files = %v（allow から外れた internal/foo.go だけのはず）", got)
	}
}

func TestRunAllowSatisfied(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Allow: []string{"**/*.md"}},
		},
	})

	src := fakeSource{changed: []string{"README.md", "docs/foo.md"}}
	violations, err := c.Run(check.Context{Message: "docs: 更新する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("全ファイルが allow に一致すれば違反 0 件のはず, got %v", violations)
	}
}

func TestRunRequireViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat", "fix"}, Require: []string{"**/*_test.go"}},
		},
	})

	src := fakeSource{changed: []string{"internal/foo.go"}}
	violations, err := c.Run(check.Context{Message: "fix: バグを直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
}

func TestRunRequireSatisfied(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat", "fix"}, Require: []string{"**/*_test.go"}},
		},
	})

	src := fakeSource{changed: []string{"internal/foo.go", "internal/foo_test.go"}}
	violations, err := c.Run(check.Context{Message: "fix: バグを直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("テストファイルが含まれていれば違反 0 件のはず, got %v", violations)
	}
}

func TestRunDenyDiffViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"refactor"}, DenyDiff: `^\+\s*(func|def|class) `},
		},
	})

	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+func NewThing() {}\n"

	src := fakeSource{changed: []string{"foo.go"}, diffs: map[string]string{"foo.go": diff}}
	violations, err := c.Run(check.Context{Message: "refactor: 整理する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
}

func TestRunScopesRestriction(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"fix"}, Scopes: []string{"api"}, Require: []string{"**/*_test.go"}},
		},
	})

	src := fakeSource{changed: []string{"internal/foo.go"}}

	// scope が一致しないので、テストが無くてもこのルールは適用されない。
	violations, err := c.Run(check.Context{Message: "fix(ui): 直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("scope が一致しなければルールは適用されないはず, got %v", violations)
	}

	// scope が一致するので適用される。
	violations, err = c.Run(check.Context{Message: "fix(api): 直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("scope が一致すれば違反になるはず, got %d: %v", len(violations), violations)
	}
}

func TestRunMultipleRulesMatchSimultaneously(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat"}, Allow: []string{"internal/**"}, Reason: "feat は internal 配下だけを変更する"},
			{Types: []string{"feat"}, Require: []string{"**/*_test.go"}, Reason: "feat はテストを伴う"},
		},
	})

	src := fakeSource{changed: []string{"README.md"}}
	violations, err := c.Run(check.Context{Message: "feat: 新機能", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("両方のルールに違反するはず, got %d: %v", len(violations), violations)
	}
}

func TestRunUnparseableMessageIsSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})

	src := fakeSource{changed: []string{"internal/foo.go"}}
	violations, err := c.Run(check.Context{Message: "適当な文章", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("体裁の検証は commit-subject の責務。パース不能なら素通りするはず, got %v", violations)
	}
}

func TestRunMergeAndRevertAreSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	src := fakeSource{changed: []string{"internal/foo.go"}}

	for _, msg := range []string{"Merge branch 'main' into feature", "Revert \"feat: 何か\""} {
		violations, err := c.Run(check.Context{Message: msg, Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if violations != nil {
			t.Errorf("Merge/Revert は対象外のはず, got %v", violations)
		}
	}
}

func TestRunNoChangedFilesIsSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	src := fakeSource{changed: nil}
	violations, err := c.Run(check.Context{Message: "docs: 更新する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更ファイルが 0 件なら違反 0 件のはず, got %v", violations)
	}
}
