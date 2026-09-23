package docsync_test

import (
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

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{})
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
