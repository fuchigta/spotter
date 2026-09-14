package diffsize_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/diffsize"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	stats []check.FileStat
}

func (f fakeSource) ChangedFiles() ([]string, error)       { return nil, nil }
func (f fakeSource) DiffLines(path string) (string, error) { return "", nil }
func (f fakeSource) BlobSize(path string) (int64, error)   { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)      { return f.stats, nil }

func mustNew(t *testing.T, cc config.CheckConfig) *diffsize.Check {
	t.Helper()
	c, err := diffsize.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

func TestNewBothZeroIsError(t *testing.T) {
	if _, err := diffsize.New(config.CheckConfig{}); err == nil {
		t.Fatal("max_files/max_lines がどちらも 0 なら New() はエラーになるはず")
	}
}

func TestNewInvalidExcludeIsError(t *testing.T) {
	if _, err := diffsize.New(config.CheckConfig{MaxFiles: 10, Exclude: []string{"["}}); err == nil {
		t.Fatal("exclude が不正な doublestar パターンなら New() はエラーになるはず")
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 10})
	if c.Granularity() != check.GranularityPerCommit {
		t.Errorf("diff-size の granularity は per-commit 固定のはず, got %v", c.Granularity())
	}
}

func TestRunMaxFilesViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 2})
	src := fakeSource{stats: []check.FileStat{
		{Path: "a.go", Added: 1},
		{Path: "b.go", Added: 1},
		{Path: "c.go", Added: 1},
	}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if violations[0].Summary != "変更ファイル数が上限を超えています: 3 件（上限 2 件）" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
}

func TestRunMaxFilesNotExceeded(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 3})
	src := fakeSource{stats: []check.FileStat{{Path: "a.go"}, {Path: "b.go"}}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("上限以下なら違反は出ないはず, got %v", violations)
	}
}

func TestRunMaxLinesViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxLines: 100})
	src := fakeSource{stats: []check.FileStat{
		{Path: "a.go", Added: 80, Deleted: 30},
	}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if violations[0].Summary != "変更行数が上限を超えています: 110 行（追加 80 / 削除 30、上限 100 行）" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
}

func TestRunBothViolations(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 1, MaxLines: 10})
	src := fakeSource{stats: []check.FileStat{
		{Path: "a.go", Added: 8},
		{Path: "b.go", Added: 8},
	}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("ファイル数・行数の両方が違反するはず, got %d: %v", len(violations), violations)
	}
}

func TestRunDeletedFilesAreCounted(t *testing.T) {
	// ChangedFiles と違い、Stats は削除されたファイルも含む前提。diff-size は
	// 「大量削除」を捕まえたいので、削除ファイルの行数もそのまま計上する。
	c := mustNew(t, config.CheckConfig{MaxLines: 5})
	src := fakeSource{stats: []check.FileStat{
		{Path: "deleted.go", Added: 0, Deleted: 10},
	}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("削除行も計上されて違反になるはず, got %d: %v", len(violations), violations)
	}
}

func TestRunBinaryFilesCountAsZeroLines(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxLines: 5})
	src := fakeSource{stats: []check.FileStat{
		{Path: "img.png", Binary: true},
		{Path: "a.go", Added: 3},
	}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("バイナリは 0 行として扱われ、合計 3 行なら上限 5 行を超えないはず, got %v", violations)
	}
}

func TestRunExcludeSkipsFiles(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 1, Exclude: []string{"**/*.lock"}})
	src := fakeSource{stats: []check.FileStat{
		{Path: "a.go", Added: 1},
		{Path: "vendor/foo.lock", Added: 1000},
	}}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("exclude に一致するファイルは集計から除外されるはず, got %v", violations)
	}
}

func TestRunTopOffendersLimitedTo5(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 1})
	stats := make([]check.FileStat, 0, 7)
	for i := 0; i < 7; i++ {
		stats = append(stats, check.FileStat{Path: string(rune('a' + i)), Added: 7 - i})
	}
	src := fakeSource{stats: stats}

	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	// 上位 5 件 + "ほか N 件" の 1 行で計 6 件。
	if len(violations[0].Files) != 6 {
		t.Fatalf("Files は上位 5 件 + まとめ 1 行のはず, got %d件: %v", len(violations[0].Files), violations[0].Files)
	}
	if last := violations[0].Files[5]; last != "ほか 2 件" {
		t.Errorf("最後の要素は残り件数のまとめのはず, got %q", last)
	}
}

func TestRunNoStats(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxFiles: 1})
	violations, err := c.Run(check.Context{Source: fakeSource{}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更が無ければ違反 0 件のはず, got %v", violations)
	}
}
