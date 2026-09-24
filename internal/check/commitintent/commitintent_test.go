package commitintent_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/commitintent"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	changed []string
	diffs   map[string]string
	deleted []string
	exists  map[string]bool

	errChanged error
	errDeleted error
	errDiff    error
}

func (f fakeSource) ChangedFiles() ([]string, error) { return f.changed, f.errChanged }
func (f fakeSource) DiffLines(path string) (string, error) {
	return f.diffs[path], f.errDiff
}
func (f fakeSource) BlobSize(path string) (int64, error) { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)    { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error)     { return f.deleted, f.errDeleted }
func (f fakeSource) Exists(path string) (bool, error)    { return f.exists[path], nil }

func mustNew(t *testing.T, cc config.CheckConfig) *commitintent.Check {
	t.Helper()
	c, err := commitintent.New(cc)
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
			name:    "rules が 0 件",
			cc:      config.CheckConfig{},
			wantErr: "少なくとも 1 件",
		},
		{
			name: "types が無い",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Allow: []string{"**/*.md"}}},
			},
			wantErr: "types が必要",
		},
		{
			name: "types に空文字",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{""}, Allow: []string{"**/*.md"}}},
			},
			wantErr: "types に空文字",
		},
		{
			name: "allow/require/deny_diff/deny がどれも無い",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"docs"}}},
			},
			wantErr: "allow / require / deny_diff / deny のいずれか",
		},
		{
			name: "scopes に空文字",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}, Scopes: []string{""}}},
			},
			wantErr: "scopes に空文字",
		},
		{
			name: "allow のパターンが不正",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"["}}},
			},
			wantErr: "パターン",
		},
		{
			name: "require のパターンが不正",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Require: []string{"["}}},
			},
			wantErr: "パターン",
		},
		{
			name: "deny のパターンが不正",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Deny: []string{"["}}},
			},
			wantErr: "パターン",
		},
		{
			name: "deny_diff のコンパイルに失敗",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"refactor"}, DenyDiff: "("}},
			},
			wantErr: "コンパイルに失敗",
		},
		{
			name: "deny_diff の無い on 指定",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"refactor"}, Allow: []string{"**"}, On: "added"}},
			},
			wantErr: "on は deny_diff 指定時のみ",
		},
		{
			name: "on が added/removed 以外",
			cc: config.CheckConfig{
				Rules: []config.CommitIntentRule{{Types: []string{"refactor"}, DenyDiff: "func ", On: "both"}},
			},
			wantErr: "は未対応です",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := commitintent.New(tt.cc)
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
	want := "feat/fix は対応する変更を伴うはずです（次のいずれかに一致する変更が必要: **/*_test.go）:"
	if violations[0].Summary != want {
		t.Errorf("Summary = %q, want %q", violations[0].Summary, want)
	}
}

func TestRunRequireViolationWithReasonStillShowsPatterns(t *testing.T) {
	// reason を指定していても、何が不足しているか（require のどのパターンに一致する
	// 変更が要るか）が Summary から分かるようにする。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat", "fix"}, Require: []string{"**/*_test.go", "**/test_*.py"}, Reason: "振る舞いの変更にはテストを伴う"},
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
	want := "振る舞いの変更にはテストを伴う（次のいずれかに一致する変更が必要: **/*_test.go, **/test_*.py）:"
	if violations[0].Summary != want {
		t.Errorf("Summary = %q, want %q", violations[0].Summary, want)
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

func TestRunDenyViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Deny: []string{"internal/**"}, Reason: "docs は internal 配下を触ってはいけない"},
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
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/foo.go" {
		t.Errorf("Files = %v（deny に一致した internal/foo.go だけのはず）", got)
	}
}

func TestRunDenyViolationOnDeletedFile(t *testing.T) {
	// deny も allow / deny_diff と同じく削除ファイルを対象にする
	// （「この type ではこのパスを触ってはいけない」は削除も含むため）。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Deny: []string{"internal/**"}},
		},
	})

	src := fakeSource{changed: []string{"README.md"}, deleted: []string{"internal/foo.go"}}
	violations, err := c.Run(check.Context{Message: "docs: READMEを直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/foo.go（削除）" {
		t.Errorf("Files = %v（削除ファイルは「（削除）」付きで出るはず）", got)
	}
}

func TestRunDenySatisfied(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Deny: []string{"internal/**"}},
		},
	})

	src := fakeSource{changed: []string{"README.md", "docs/foo.md"}}
	violations, err := c.Run(check.Context{Message: "docs: 更新する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("deny に一致するファイルが無ければ違反 0 件のはず, got %v", violations)
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

func TestRunDenyDiffOnAddedProducesLineHits(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"refactor"}, DenyDiff: `^func `, On: "added"},
		},
	})

	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,0 +2,2 @@\n" +
		"+func NewThing() {}\n" +
		"+var x = 1\n"

	src := fakeSource{changed: []string{"foo.go"}, diffs: map[string]string{"foo.go": diff}}
	violations, err := c.Run(check.Context{Message: "refactor: 整理する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "foo.go:2: func NewThing() {}" {
		t.Errorf("Files = %v（path:line: text 形式で一致した行だけのはず）", got)
	}
}

func TestRunDenyDiffOnRemovedProducesLineHits(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"refactor"}, DenyDiff: `^func `, On: "removed"},
		},
	})

	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -3 +0,0 @@\n" +
		"-func OldThing() {}\n"

	src := fakeSource{changed: []string{"foo.go"}, diffs: map[string]string{"foo.go": diff}}
	violations, err := c.Run(check.Context{Message: "refactor: 整理する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "foo.go:3: func OldThing() {}" {
		t.Errorf("Files = %v（path:line: text 形式で一致した行だけのはず）", got)
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

func boolPtr(b bool) *bool { return &b }

func TestRunBreakingTrueRestriction(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat", "fix"}, Breaking: boolPtr(true), Require: []string{"docs/**"}, Reason: "破壊的変更には docs を伴う"},
		},
	})

	src := fakeSource{changed: []string{"internal/foo.go"}}

	// 破壊的変更ではないので、docs が無くてもこのルールは適用されない。
	violations, err := c.Run(check.Context{Message: "feat: 追加する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("breaking: true のルールは破壊的変更でなければ適用されないはず, got %v", violations)
	}

	// subject の "!" による破壊的変更なので適用される。
	violations, err = c.Run(check.Context{Message: "feat!: 変える", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("破壊的変更なら違反になるはず, got %d: %v", len(violations), violations)
	}

	// 本文フッタの BREAKING CHANGE による破壊的変更でも適用される。
	violations, err = c.Run(check.Context{Message: "fix: 直す\n\nBREAKING CHANGE: 挙動が変わる", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("フッタによる破壊的変更でも違反になるはず, got %d: %v", len(violations), violations)
	}
}

func TestRunBreakingFalseRestriction(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat"}, Breaking: boolPtr(false), Require: []string{"**/*_test.go"}},
		},
	})

	src := fakeSource{changed: []string{"internal/foo.go"}}

	// 破壊的変更なので、breaking: false のルールは適用されない。
	violations, err := c.Run(check.Context{Message: "feat!: 変える", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("breaking: false のルールは破壊的変更のときは適用されないはず, got %v", violations)
	}

	// 破壊的変更でないので適用される。
	violations, err = c.Run(check.Context{Message: "feat: 追加する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("破壊的変更でなければ違反になるはず, got %d: %v", len(violations), violations)
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

// TestRunMergeAndRevertAreSkipped は、"Merge " を特別扱いしないこと（マージコミット
// 自体は range の RevListNoMerges と staged の Repo.InMerge で既に検査対象から外れて
// いるため、ここでは判定不要）と、git revert が生成する `Revert "<元の subject>"`
// （ダブルクォート付き）だけを自動生成として黙らせることを確認する。いずれも
// Conventional Commits の一般形に合わないため、"Merge stuff" や "Revert stuff" は
// 「一般形になっていない subject には何も報告しない」規則（TestRunUnparseableMessageIsSkipped
// と同じ経路）にそのまま従って結果的に素通りする。
func TestRunMergeAndRevertAreSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	src := fakeSource{changed: []string{"internal/foo.go"}}

	for _, msg := range []string{
		"Merge branch 'main' into feature",
		"Merge stuff",
		"Revert \"feat: 何か\"",
		"Revert stuff",
	} {
		violations, err := c.Run(check.Context{Message: msg, Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if violations != nil {
			t.Errorf("%q: 違反にはならないはず, got %v", msg, violations)
		}
	}
}

func TestRunNoChangedFilesIsSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	src := fakeSource{changed: nil, deleted: nil}
	violations, err := c.Run(check.Context{Message: "docs: 更新する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更・削除ファイルが両方 0 件なら違反 0 件のはず, got %v", violations)
	}
}

func TestRunDeletionOnlyStillRuns(t *testing.T) {
	// 変更ファイルが 0 件でも、削除があれば検査を続ける（早期 return しない）。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Allow: []string{"**/*.md"}, Reason: "docs はドキュメントだけを変更する"},
		},
	})
	src := fakeSource{changed: nil, deleted: []string{"internal/foo.go"}}
	violations, err := c.Run(check.Context{Message: "docs: 更新する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("削除だけでも allow から外れていれば違反になるはず, got %d: %v", len(violations), violations)
	}
}

func TestRunAllowViolationIncludesDeletedFiles(t *testing.T) {
	// docs: を名乗ってコードを削除しても、ChangedFiles（ACMR）だけでは捕まらない
	// 抜け穴を塞ぐ。削除ファイルも allow に照らし、外れていれば「（削除）」付きで報告する。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"docs"}, Allow: []string{"**/*.md"}, Reason: "docs はドキュメントだけを変更する"},
		},
	})
	src := fakeSource{changed: []string{"README.md"}, deleted: []string{"internal/foo.go"}}
	violations, err := c.Run(check.Context{Message: "docs: 更新する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/foo.go（削除）" {
		t.Errorf("Files = %v（削除ファイルは「（削除）」付きで出るはず）", got)
	}
}

func TestRunRequireNotSatisfiedByDeletion(t *testing.T) {
	// require は「変更ファイルの少なくとも 1 つ」を求めるルール。削除したファイル自身が
	// require のパターンに一致しても満たしたことにはしない（例えばテストを消したコミットで
	// require: ['**/*_test.go'] を、消したテストファイル自身で満たせては本末転倒なため）。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat", "fix"}, Require: []string{"**/*_test.go"}},
		},
	})
	src := fakeSource{changed: []string{"internal/foo.go"}, deleted: []string{"internal/foo_test.go"}}
	violations, err := c.Run(check.Context{Message: "fix: バグを直す", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("削除したテストでは require を満たさないので違反になるはず, got %d: %v", len(violations), violations)
	}
}

func TestRunChangedFilesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	src := fakeSource{errChanged: errors.New("boom")}
	if _, err := c.Run(check.Context{Message: "docs: 更新する", Source: src}); err == nil {
		t.Fatal("ChangedFiles がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunDeletedFilesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"docs"}, Allow: []string{"**/*.md"}}},
	})
	src := fakeSource{errDeleted: errors.New("boom")}
	if _, err := c.Run(check.Context{Message: "docs: 更新する", Source: src}); err == nil {
		t.Fatal("DeletedFiles がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunDiffLinesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{{Types: []string{"refactor"}, DenyDiff: `^\+func `}},
	})
	src := fakeSource{changed: []string{"foo.go"}, errDiff: errors.New("boom")}
	if _, err := c.Run(check.Context{Message: "refactor: 整理する", Source: src}); err == nil {
		t.Fatal("DiffLines がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunMultipleConditionsInSameRuleAreIndependentViolations(t *testing.T) {
	// 1 つのルールに allow と require を同時に書くと、それぞれ独立に評価され、
	// 違反ごとに別の Violation になる。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"feat"}, Allow: []string{"internal/**"}, Require: []string{"**/*_test.go"}},
		},
	})

	src := fakeSource{changed: []string{"README.md"}}
	violations, err := c.Run(check.Context{Message: "feat: 追加する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("allow と require、それぞれ独立に違反になるはず, got %d: %v", len(violations), violations)
	}
}

func TestRunDenyDiffViolationOnDeletedFile(t *testing.T) {
	// 削除ファイルも差分を持つ（内容が "-" 行として出る）ため、deny_diff は削除ファイルの
	// 差分にも当てる。「refactor: と称してコードごと消す」ような抜け穴を防ぐ。
	c := mustNew(t, config.CheckConfig{
		Rules: []config.CommitIntentRule{
			{Types: []string{"refactor"}, DenyDiff: `^-\s*func `},
		},
	})

	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ /dev/null\n" +
		"@@ -1 +0,0 @@\n" +
		"-func OldThing() {}\n"

	src := fakeSource{deleted: []string{"foo.go"}, diffs: map[string]string{"foo.go": diff}}
	violations, err := c.Run(check.Context{Message: "refactor: 整理する", Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "foo.go（削除）" {
		t.Errorf("Files = %v（削除ファイルは「（削除）」付きで出るはず）", got)
	}
}
