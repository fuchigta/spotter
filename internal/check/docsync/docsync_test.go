package docsync_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docsync"
	"github.com/fuchigta/spotter/internal/config"
)

// fakeSource はテスト用の固定応答 check.Source。
type fakeSource struct {
	changed []string
	diffs   map[string]string
	deleted []string
	exists  map[string]bool
}

func (f fakeSource) ChangedFiles() ([]string, error) { return f.changed, nil }
func (f fakeSource) DiffLines(path string) (string, error) {
	return f.diffs[path], nil
}
func (f fakeSource) BlobSize(path string) (int64, error) { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)    { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error)     { return f.deleted, nil }
func (f fakeSource) Exists(path string) (bool, error)    { return f.exists[path], nil }

func mustNew(t *testing.T, cc config.CheckConfig) *docsync.Check {
	t.Helper()
	c, err := docsync.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

func TestRunViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/root.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反が 1 件出るはず, got %d", len(violations))
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/cli/root.go" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunGroupsByDoc(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
			{Paths: "internal/config/*.go", Doc: "README.md"},
		},
	})

	src := fakeSource{changed: []string{
		"internal/cli/root.go",
		"internal/config/config.go",
	}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("同じ doc を持つ pairs は 1 件にまとまるはず, got %d: %+v", len(violations), violations)
	}

	want := "internal/cli/*.go, internal/config/*.go を変更していますが、README.md が一緒に入っていません:"
	if violations[0].Summary != want {
		t.Errorf("Summary = %q, want %q", violations[0].Summary, want)
	}
	wantFiles := []string{"internal/cli/root.go", "internal/config/config.go"}
	sort.Strings(wantFiles)
	if !reflect.DeepEqual(violations[0].Files, wantFiles) {
		t.Errorf("Files = %v, want %v（重複排除・ソート済みのはず）", violations[0].Files, wantFiles)
	}
}

func TestRunGroupsByDocDedupesOverlappingFiles(t *testing.T) {
	// 2 つの pairs の paths が同じファイルに一致する場合、Files には 1 回だけ出るはず。
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
			{Paths: "internal/cli/root.go", Doc: "README.md"},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/root.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d", len(violations))
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/cli/root.go" {
		t.Errorf("Files は重複排除されるはず, got %v", got)
	}
}

func TestRunDocUpdatedTogether(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/root.go", "README.md"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("ドキュメントも一緒に変更されていれば違反は出ないはず, got %v", violations)
	}
}

func TestRunDocWhenGatesSatisfaction(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", DocWhen: `^\+.*\S`},
		},
	})

	t.Run("doc の差分が doc_when に一致しなければ満たされない", func(t *testing.T) {
		src := fakeSource{
			changed: []string{"internal/cli/root.go", "README.md"},
			diffs:   map[string]string{"README.md": "+ \n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 1 {
			t.Fatalf("doc_when に一致しない形だけの更新は満たしたとみなさないはず, got %d", len(violations))
		}
	})

	t.Run("doc の差分が doc_when に一致すれば満たされる", func(t *testing.T) {
		src := fakeSource{
			changed: []string{"internal/cli/root.go", "README.md"},
			diffs:   map[string]string{"README.md": "+新しい説明\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 0 {
			t.Errorf("doc_when に一致する差分なら満たされるはず, got %v", violations)
		}
	})
}

func TestNewDocWhenInvalidRegex(t *testing.T) {
	if _, err := docsync.New(config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md", DocWhen: "("}},
	}); err == nil {
		t.Fatal("doc_when が不正な正規表現なら New() はエラーになるはず")
	}
}

func TestRunTestFileNotExcludedByDefault(t *testing.T) {
	// _test.go は自動では除外されない。除外したい場合は checks.<key>.exclude に
	// 明示する必要がある。
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/root_test.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("exclude 未指定なら _test.go も対象になるはず, got %v", violations)
	}
}

func TestRunTestFileExcludedWhenConfigured(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
		Exclude: []string{"**/*_test.go"},
	})

	src := fakeSource{changed: []string{"internal/cli/root_test.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("exclude に '**/*_test.go' を指定すれば除外されるはず, got %v", violations)
	}
}

func TestRunWhenRegexGatesFiring(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", When: `^[+-].*Use:`},
		},
	})

	t.Run("正規表現に一致しない差分では発火しない", func(t *testing.T) {
		src := fakeSource{
			changed: []string{"internal/cli/root.go"},
			diffs:   map[string]string{"internal/cli/root.go": "+func run() {}\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 0 {
			t.Errorf("when に一致しなければ違反は出ないはず, got %v", violations)
		}
	})

	t.Run("正規表現に一致する差分では発火する", func(t *testing.T) {
		src := fakeSource{
			changed: []string{"internal/cli/root.go"},
			diffs:   map[string]string{"internal/cli/root.go": `+	Use: "foo",` + "\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 1 {
			t.Fatalf("when に一致すれば違反が出るはず, got %d", len(violations))
		}
	})
}

// TestRunWhenMultilineDiffAnchorsPerLine は、実際の git diff 出力のように
// "diff --git"/"@@" ヘッダを含む複数行の差分に対して、"^"/"$" を使う when が
// （文字列全体の先頭ではなく）行単位で効くことを確認する。(?m) の自動付与が
// 無いと、1 行目が "diff --git ..." になるため "^[+-]" は常に不一致になる。
func TestRunWhenMultilineDiffAnchorsPerLine(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", When: `^[+-]\tUse:`},
		},
	})

	realisticDiff := "diff --git a/internal/cli/root.go b/internal/cli/root.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/internal/cli/root.go\n" +
		"+++ b/internal/cli/root.go\n" +
		"@@ -1 +1 @@\n" +
		"+\tUse: \"foo\",\n"

	src := fakeSource{
		changed: []string{"internal/cli/root.go"},
		diffs:   map[string]string{"internal/cli/root.go": realisticDiff},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("ヘッダ行を含む複数行の差分でも、対象行に \"^[+-]\" がマッチして違反が出るはず, got %d", len(violations))
	}
}

func TestRunOnAddedOnly(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", When: "Use:", On: "added"},
		},
	})

	t.Run("追加行にだけ一致すれば発火する", func(t *testing.T) {
		src := fakeSource{
			changed: []string{"internal/cli/root.go"},
			diffs:   map[string]string{"internal/cli/root.go": "@@ -1 +1 @@\n+\tUse: \"foo\",\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 1 {
			t.Fatalf("on: added で追加行に一致すれば違反が出るはず, got %d", len(violations))
		}
	})

	t.Run("削除行にしか無ければ発火しない", func(t *testing.T) {
		src := fakeSource{
			changed: []string{"internal/cli/root.go"},
			diffs:   map[string]string{"internal/cli/root.go": "@@ -1 +0,0 @@\n-\tUse: \"foo\",\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 0 {
			t.Errorf("on: added では削除行を見ないはず, got %v", violations)
		}
	})
}

func TestRunOnRemovedOnly(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", When: "Use:", On: "removed"},
		},
	})

	src := fakeSource{
		changed: []string{"internal/cli/root.go"},
		diffs:   map[string]string{"internal/cli/root.go": "@@ -1 +1 @@\n+\tUse: \"foo\",\n"},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("on: removed では追加行を見ないはず, got %v", violations)
	}
}

func TestNewOnRequiresWhen(t *testing.T) {
	if _, err := docsync.New(config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md", On: "added"}},
	}); err == nil {
		t.Fatal("when 未指定で on を指定すると New() はエラーになるはず")
	}
}

func TestNewOnInvalidValue(t *testing.T) {
	if _, err := docsync.New(config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md", When: "x", On: "both"}},
	}); err == nil {
		t.Fatal("on に added/removed 以外を指定すると New() はエラーになるはず")
	}
}

func TestRunExcludePattern(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
		Exclude: []string{"internal/cli/generated_*.go"},
	})

	src := fakeSource{changed: []string{"internal/cli/generated_foo.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("exclude に一致すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunPairExcludePattern(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", Exclude: []string{"internal/cli/checks.go"}},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/checks.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("pairs[].exclude に一致すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunPairExcludeDoesNotAffectOtherPairs(t *testing.T) {
	// internal/cli/checks.go は README.md の対応から除外するが、同じファイルを対象にする
	// 別の pair（OTHER.md 対応）には exclude を指定していないため、そちらは通常どおり違反になる。
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", Exclude: []string{"internal/cli/checks.go"}},
			{Paths: "internal/cli/checks.go", Doc: "OTHER.md"},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/checks.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("exclude していない pair 側では違反が出るはず, got %d: %+v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Summary, "OTHER.md") {
		t.Errorf("Summary は OTHER.md 側の違反であるはず, got %q", violations[0].Summary)
	}
}

func TestRunPairExcludeCombinesWithTopLevelExclude(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", Exclude: []string{"internal/cli/checks.go"}},
		},
		Exclude: []string{"internal/cli/generated_*.go"},
	})

	src := fakeSource{changed: []string{
		"internal/cli/checks.go",
		"internal/cli/generated_foo.go",
		"internal/cli/root.go",
	}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("除外されなかったファイルだけが違反になるはず, got %d: %+v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/cli/root.go" {
		t.Errorf("Files はトップレベル・pair 両方の exclude で除かれた残りだけのはず, got %v", got)
	}
}

func TestRunPairExcludeAppliesToDeletedFiles(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", Exclude: []string{"internal/cli/checks.go"}},
		},
	})

	src := fakeSource{deleted: []string{"internal/cli/checks.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("削除ファイルにも pairs[].exclude が効くはず, got %v", violations)
	}
}

func TestNewPairExcludeInvalidPattern(t *testing.T) {
	if _, err := docsync.New(config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md", Exclude: []string{"["}}},
	}); err == nil {
		t.Fatal("pairs[].exclude が不正なパターンなら New() はエラーになるはず")
	}
}

func TestRunNoChanges(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md"}},
	})
	violations, err := c.Run(check.Context{Source: fakeSource{}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更が無ければ nil のはず, got %v", violations)
	}
}

func TestRunDeletedFileIsCandidate(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
	})

	src := fakeSource{deleted: []string{"internal/cli/root.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("削除されたファイルも違反候補になるはず, got %d", len(violations))
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/cli/root.go（削除）" {
		t.Errorf("Files = %v, 削除だと分かる表示になっていない", got)
	}
}

func TestRunDeletedDocSatisfies(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "docs/legacy.md"},
		},
	})

	src := fakeSource{
		changed: []string{"internal/cli/root.go"},
		deleted: []string{"docs/legacy.md"},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("doc 自身が削除されていればドキュメント側も変更されたとみなすはず, got %v", violations)
	}
}

func TestRunDeletedFileGatedByWhen(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md", When: `^-.*Use:`},
		},
	})

	t.Run("削除ファイルの差分が when に一致しなければ発火しない", func(t *testing.T) {
		src := fakeSource{
			deleted: []string{"internal/cli/root.go"},
			diffs:   map[string]string{"internal/cli/root.go": "-func run() {}\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 0 {
			t.Errorf("when に一致しなければ違反は出ないはず, got %v", violations)
		}
	})

	t.Run("削除ファイルの差分が when に一致すれば発火する", func(t *testing.T) {
		src := fakeSource{
			deleted: []string{"internal/cli/root.go"},
			diffs:   map[string]string{"internal/cli/root.go": `-	Use: "foo",` + "\n"},
		}
		violations, err := c.Run(check.Context{Source: src})
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if len(violations) != 1 {
			t.Fatalf("when に一致すれば違反が出るはず, got %d", len(violations))
		}
	})
}

func TestRunNoChangesButDeletions(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md"}},
	})
	src := fakeSource{deleted: []string{"main.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("変更 0 件でも削除があれば検査を続けるはず, got %v", violations)
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go", Doc: "README.md"}},
	})
	if c.Granularity() != check.GranularitySquashed {
		t.Errorf("doc-sync の granularity は squashed 固定のはず, got %v", c.Granularity())
	}
}

func TestNewInvalidPair(t *testing.T) {
	if _, err := docsync.New(config.CheckConfig{
		Pairs: []config.DocSyncPair{{Paths: "*.go"}},
	}); err == nil {
		t.Fatal("doc が空なら New() はエラーになるはず")
	}
}

func TestNewEmptyPairs(t *testing.T) {
	if _, err := docsync.New(config.CheckConfig{}); err == nil {
		t.Fatal("pairs が 0 件なら New() はエラーになるはず")
	}
}

func TestRunViolationHasTarget(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
		},
	})

	src := fakeSource{changed: []string{"internal/cli/root.go"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反が 1 件出るはず, got %d", len(violations))
	}
	if violations[0].Target != "README.md" {
		t.Errorf("Target = %q, want %q（範囲付き免除と照合する doc のパス）", violations[0].Target, "README.md")
	}
}

func TestExemptTargets(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Pairs: []config.DocSyncPair{
			{Paths: "internal/cli/*.go", Doc: "README.md"},
			{Paths: "internal/config/*.go", Doc: "README.md"},
			{Paths: "internal/check/docsync/*.go", Doc: "docs/checks/doc-sync.md"},
		},
	})

	got := c.ExemptTargets()
	want := []string{"README.md", "docs/checks/doc-sync.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExemptTargets() = %v, want %v（重複排除済みの doc 一覧）", got, want)
	}
}
