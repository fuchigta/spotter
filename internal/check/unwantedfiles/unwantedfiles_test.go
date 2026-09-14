package unwantedfiles_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/unwantedfiles"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	changed []string
	sizes   map[string]int64
}

func (f fakeSource) ChangedFiles() ([]string, error)       { return f.changed, nil }
func (f fakeSource) DiffLines(path string) (string, error) { return "", nil }
func (f fakeSource) BlobSize(path string) (int64, error)   { return f.sizes[path], nil }
func (f fakeSource) Stats() ([]check.FileStat, error)      { return nil, nil }

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
	c := mustNew(t, config.CheckConfig{})
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
