package companionfiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/companionfiles"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	changed []string
	deleted []string
	exists  map[string]bool
}

func (f fakeSource) ChangedFiles() ([]string, error)       { return f.changed, nil }
func (f fakeSource) DiffLines(path string) (string, error) { return "", nil }
func (f fakeSource) BlobSize(path string) (int64, error)   { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)      { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error)       { return f.deleted, nil }
func (f fakeSource) Exists(path string) (bool, error)      { return f.exists[path], nil }

func mustNew(t *testing.T, cc config.CheckConfig) *companionfiles.Check {
	t.Helper()
	c, err := companionfiles.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

// writeFiles はテスト用の作業ツリーを root 配下に作る。
func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
}

func TestNewEmptyCompanionsIsError(t *testing.T) {
	if _, err := companionfiles.New(config.CheckConfig{}); err == nil {
		t.Fatal("companions が 0 件なら New() はエラーになるはず")
	}
}

func TestNewMissingFieldIsError(t *testing.T) {
	if _, err := companionfiles.New(config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test.ts"}},
	}); err == nil {
		t.Fatal("reason が無ければ New() はエラーになるはず")
	}
}

func TestNewUnknownTemplateVarIsError(t *testing.T) {
	if _, err := companionfiles.New(config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "{dir}/{basename}.test.ts", Reason: "テストが無い"}},
	}); err == nil {
		t.Fatal("未知のテンプレート変数があれば New() はエラーになるはず")
	}
}

func TestNewInvalidPathsPatternIsError(t *testing.T) {
	if _, err := companionfiles.New(config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "[", Companion: "{name}.test.ts", Reason: "テストが無い"}},
	}); err == nil {
		t.Fatal("paths が不正な doublestar パターンなら New() はエラーになるはず")
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test.ts", Reason: "テストが無い"}},
	})
	if c.Granularity() != check.GranularitySquashed {
		t.Errorf("companion-files の granularity は squashed 固定のはず, got %v", c.Granularity())
	}
}

func TestRunAllTemplateVars(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "src/api/client.ts")

	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Root:   root,
		Source: fakeSource{changed: []string{"src/api/client.ts"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "src/api/client.ts → src/api/client.test.ts" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunCompanionExists(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "src/api/client.ts", "src/api/client.test.ts")

	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Root:   root,
		Source: fakeSource{changed: []string{"src/api/client.ts"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("相方が既にあれば違反 0 件のはず, got %v", violations)
	}
}

func TestRunRootLevelFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "client.ts")

	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Root:   root,
		Source: fakeSource{changed: []string{"client.ts"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "client.ts → client.test.ts" {
		t.Errorf("ルート直下のファイルで先頭に / が残らないはず, got %v", got)
	}
}

func TestRunExcludeSkipsRule(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "src/types/foo.d.ts")

	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{
				Paths:     "src/**/*.ts",
				Companion: "{dir}/{name}.test{ext}",
				Reason:    "テストが無い",
				Exclude:   []string{"src/types/**"},
			},
		},
	})

	violations, err := c.Run(check.Context{
		Root:   root,
		Source: fakeSource{changed: []string{"src/types/foo.d.ts"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("exclude に一致するファイルは対象外のはず, got %v", violations)
	}
}

func TestRunMultipleRules(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "src/api/client.ts", "db/migrations/001.up.sql")

	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
			{Paths: "db/migrations/**/*.up.sql", Companion: "{dir}/{name}.down.sql", Reason: "ロールバック用のマイグレーションが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Root:   root,
		Source: fakeSource{changed: []string{"src/api/client.ts", "db/migrations/001.up.sql"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("両方のルールが違反するはず, got %d: %v", len(violations), violations)
	}
}

func TestRunNoChangedFilesIsSkipped(t *testing.T) {
	root := t.TempDir()
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"}},
	})
	violations, err := c.Run(check.Context{Root: root, Source: fakeSource{changed: nil}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更ファイルが 0 件なら違反 0 件のはず, got %v", violations)
	}
}
