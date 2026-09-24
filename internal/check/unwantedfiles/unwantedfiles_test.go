package unwantedfiles_test

import (
	"errors"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/unwantedfiles"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	changed    []string
	changedErr error
	sizes      map[string]int64
	sizeErr    error
	deleted    []string
	exists     map[string]bool
}

func (f fakeSource) ChangedFiles() ([]string, error)       { return f.changed, f.changedErr }
func (f fakeSource) DiffLines(path string) (string, error) { return "", nil }
func (f fakeSource) BlobSize(path string) (int64, error)   { return f.sizes[path], f.sizeErr }
func (f fakeSource) Stats() ([]check.FileStat, error)      { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error)       { return f.deleted, nil }
func (f fakeSource) Exists(path string) (bool, error)      { return f.exists[path], nil }

func mustNew(t *testing.T, cc config.CheckConfig) *unwantedfiles.Check {
	t.Helper()
	c, err := unwantedfiles.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

func TestRunDenyPattern(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Paths: "*.jsonl", Reason: "セッションログ"},
		},
	})

	violations, err := c.Run(check.Context{Source: fakeSource{changed: []string{"a.jsonl", "b.go"}}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d", len(violations))
	}
	if violations[0].Summary != "a.jsonl: セッションログ" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
}

func TestRunDenyPatternMatchesNestedPathsOnlyWithDoubleStar(t *testing.T) {
	// doublestar の "*" は 1 階層しかまたがない。ネストしたパスも拾いたい場合は
	// "**/" を明示する必要がある（*.jsonl だけでは sub/a.jsonl に一致しない）。
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Paths: "*.jsonl", Reason: "ルート直下のみ"},
			{Paths: "**/*.log", Reason: "任意の階層"},
		},
	})

	src := fakeSource{changed: []string{"sub/a.jsonl", "sub/dir/b.log", "c.log"}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("**/*.log はネストした b.log と直下の c.log に一致するはず（*.jsonl は sub/a.jsonl に一致しない）, got %d件: %v", len(violations), violations)
	}
	for _, v := range violations {
		if v.Summary == "sub/a.jsonl: ルート直下のみ" {
			t.Errorf("*.jsonl は sub/a.jsonl のようなネストしたパスに一致しないはず, got %v", violations)
		}
	}
}

func TestRunMaxBytes(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxBytes: 100})

	src := fakeSource{
		changed: []string{"big.bin", "small.bin"},
		sizes:   map[string]int64{"big.bin": 200, "small.bin": 50},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d", len(violations))
	}
	if violations[0].Summary != "big.bin: 200 バイト（上限 100 バイト）" {
		t.Errorf("Summary = %q", violations[0].Summary)
	}
}

func TestRunMaxBytesExactlyAtLimitIsNotViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxBytes: 100})

	src := fakeSource{changed: []string{"a.bin"}, sizes: map[string]int64{"a.bin": 100}}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ちょうど上限なら違反にならないはず（超過だけが違反）, got %v", violations)
	}
}

func TestRunDenyFirstRuleWinsOnMultipleMatches(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Deny: []config.DenyRule{
			{Paths: "**/*.log", Reason: "最初のルール"},
			{Paths: "sub/*.log", Reason: "2番目のルール"},
		},
	})

	violations, err := c.Run(check.Context{Source: fakeSource{changed: []string{"sub/a.log"}}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d", len(violations))
	}
	if violations[0].Summary != "sub/a.log: 最初のルール" {
		t.Errorf("Summary = %q, 最初に一致したルールの reason を使うはず", violations[0].Summary)
	}
}

// TestRunDenyReasonWinsOverMaxBytesAndReportsOnce は、deny に一致し、かつ max_bytes も
// 超えるファイルが、deny の reason で 1 回だけ報告される（サイズ理由と二重に報告されない）
// ことを確認する。
func TestRunDenyReasonWinsOverMaxBytesAndReportsOnce(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		MaxBytes: 10,
		Deny: []config.DenyRule{
			{Paths: "**/*.log", Reason: "ログファイル"},
		},
	})

	src := fakeSource{
		changed: []string{"a.log"},
		sizes:   map[string]int64{"a.log": 1000},
	}
	violations, err := c.Run(check.Context{Source: src})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("deny と max_bytes の両方に該当しても 1 件だけ報告されるはず, got %d: %v", len(violations), violations)
	}
	if violations[0].Summary != "a.log: ログファイル" {
		t.Errorf("Summary = %q, deny の reason を使うはず（サイズ理由にはならない）", violations[0].Summary)
	}
}

func TestRunChangedFilesErrorPropagates(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxBytes: 1})
	wantErr := errors.New("変更ファイルの取得に失敗")

	_, err := c.Run(check.Context{Source: fakeSource{changedErr: wantErr}})
	if err == nil {
		t.Fatal("Source.ChangedFiles() がエラーなら Run() は error を返すはず")
	}
}

func TestRunBlobSizeErrorPropagates(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxBytes: 1})
	wantErr := errors.New("サイズの取得に失敗")

	src := fakeSource{changed: []string{"a.bin"}, sizeErr: wantErr}
	_, err := c.Run(check.Context{Source: src})
	if err == nil {
		t.Fatal("Source.BlobSize() がエラーなら Run() は error を返すはず")
	}
}

func TestRunNoViolation(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxBytes: 100})
	violations, err := c.Run(check.Context{Source: fakeSource{changed: []string{"ok.go"}, sizes: map[string]int64{"ok.go": 10}}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("違反が無ければ nil のはず, got %v", violations)
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{MaxBytes: 1})
	if c.Granularity() != check.GranularityPerCommit {
		t.Errorf("unwanted-files の granularity は per-commit 固定のはず, got %v", c.Granularity())
	}
}

func TestNewInvalidDeny(t *testing.T) {
	if _, err := unwantedfiles.New(config.CheckConfig{
		Deny: []config.DenyRule{{Paths: "*.jsonl"}},
	}); err == nil {
		t.Fatal("reason が空なら New() はエラーになるはず")
	}
}

func TestNewEmptyConfig(t *testing.T) {
	if _, err := unwantedfiles.New(config.CheckConfig{}); err == nil {
		t.Fatal("deny と max_bytes の両方が無い設定は New() はエラーになるはず")
	}
}

func TestNewNetIsRejected(t *testing.T) {
	// net は diff-content 専用のフィールドで、pattern/on と同様に unwanted-files では
	// 起動時エラーになる。
	if _, err := unwantedfiles.New(config.CheckConfig{
		Deny: []config.DenyRule{{Paths: "*.jsonl", Reason: "禁止", Net: true}},
	}); err == nil {
		t.Fatal("net を指定したら New() はエラーになるはず")
	}
}
