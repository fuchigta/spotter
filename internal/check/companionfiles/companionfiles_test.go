package companionfiles_test

import (
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

func TestNewDotDotSegmentIsError(t *testing.T) {
	if _, err := companionfiles.New(config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "{dir}/../{name}.test.ts", Reason: "テストが無い"}},
	}); err == nil {
		t.Fatal("companion に .. セグメントがあれば New() はエラーになるはず")
	}
}

func TestNewAbsolutePathIsError(t *testing.T) {
	if _, err := companionfiles.New(config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "/etc/{name}.test.ts", Reason: "テストが無い"}},
	}); err == nil {
		t.Fatal("companion が絶対パスなら New() はエラーになるはず")
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
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
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
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			changed: []string{"src/api/client.ts"},
			exists:  map[string]bool{"src/api/client.test.ts": true},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("相方が既にあれば違反 0 件のはず, got %v", violations)
	}
}

func TestRunRootLevelFile(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
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
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"},
			{Paths: "db/migrations/**/*.up.sql", Companion: "{dir}/{name}.down.sql", Reason: "ロールバック用のマイグレーションが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{changed: []string{"src/api/client.ts", "db/migrations/001.up.sql"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("両方のルールが違反するはず, got %d: %v", len(violations), violations)
	}
}

// TestRunStemVariable は複合拡張子（001.up.sql）で {stem} が最初の "." より前だけを
// 取ることを確認する（{name}/{ext} の「最後の .」基準とは異なる）。
func TestRunStemVariable(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "db/migrations/**/*.up.sql", Companion: "{dir}/{stem}.down.sql", Reason: "ロールバック用のマイグレーションが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{changed: []string{"db/migrations/001.up.sql"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "db/migrations/001.up.sql → db/migrations/001.down.sql" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunNoChangedFilesIsSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: "{dir}/{name}.test{ext}", Reason: "テストが無い"}},
	})
	violations, err := c.Run(check.Context{Source: fakeSource{changed: nil}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更ファイルが 0 件なら違反 0 件のはず, got %v", violations)
	}
}
