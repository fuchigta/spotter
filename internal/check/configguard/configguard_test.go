package configguard_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/configguard"
	"github.com/fuchigta/spotter/internal/configdiff"
)

// fakeEndpointSource は check.Source と check.EndpointReader の両方を実装する fake。
// BaseFile/TargetFile はパスごとの中身を保持する map から返す。
type fakeEndpointSource struct {
	base   map[string][]byte
	target map[string][]byte

	errBase   error
	errTarget error
}

var (
	_ check.Source         = fakeEndpointSource{}
	_ check.EndpointReader = fakeEndpointSource{}
)

func (f fakeEndpointSource) ChangedFiles() ([]string, error)       { return nil, nil }
func (f fakeEndpointSource) DiffLines(path string) (string, error) { return "", nil }
func (f fakeEndpointSource) BlobSize(path string) (int64, error)   { return 0, nil }
func (f fakeEndpointSource) Stats() ([]check.FileStat, error)      { return nil, nil }
func (f fakeEndpointSource) DeletedFiles() ([]string, error)       { return nil, nil }
func (f fakeEndpointSource) Exists(path string) (bool, error)      { return false, nil }

func (f fakeEndpointSource) BaseFile(path string) ([]byte, bool, error) {
	if f.errBase != nil {
		return nil, false, f.errBase
	}
	data, ok := f.base[path]
	return data, ok, nil
}

func (f fakeEndpointSource) TargetFile(path string) ([]byte, bool, error) {
	if f.errTarget != nil {
		return nil, false, f.errTarget
	}
	data, ok := f.target[path]
	return data, ok, nil
}

// fakeSourceWithoutEndpointReader は check.Source だけを実装し、check.EndpointReader には
// 対応しない fake（gitutil 以外の Source 実装で config-guard を走らせようとした場合を模す）。
type fakeSourceWithoutEndpointReader struct{}

var _ check.Source = fakeSourceWithoutEndpointReader{}

func (fakeSourceWithoutEndpointReader) ChangedFiles() ([]string, error)       { return nil, nil }
func (fakeSourceWithoutEndpointReader) DiffLines(path string) (string, error) { return "", nil }
func (fakeSourceWithoutEndpointReader) BlobSize(path string) (int64, error)   { return 0, nil }
func (fakeSourceWithoutEndpointReader) Stats() ([]check.FileStat, error)      { return nil, nil }
func (fakeSourceWithoutEndpointReader) DeletedFiles() ([]string, error)       { return nil, nil }
func (fakeSourceWithoutEndpointReader) Exists(path string) (bool, error)      { return false, nil }

const configPath = ".spotter.yml"

func TestGranularity(t *testing.T) {
	c := configguard.New(nil)
	if c.Granularity() != check.GranularitySquashed {
		t.Errorf("config-guard の granularity は squashed 固定のはず, got %v", c.Granularity())
	}
}

func TestRunNotEndpointReaderIsError(t *testing.T) {
	c := configguard.New(nil)
	_, err := c.Run(check.Context{Source: fakeSourceWithoutEndpointReader{}, ConfigPath: configPath})
	if err == nil {
		t.Fatal("EndpointReader 非対応の Source なら Run() はエラーになるはず")
	}
}

func TestRunEmptyConfigPathIsError(t *testing.T) {
	c := configguard.New(nil)
	_, err := c.Run(check.Context{Source: fakeEndpointSource{}, ConfigPath: ""})
	if err == nil {
		t.Fatal("ConfigPath が空なら Run() はエラーになるはず")
	}
}

func TestRunBaseFileErrorIsWrapped(t *testing.T) {
	c := configguard.New(nil)
	src := fakeEndpointSource{errBase: errors.New("boom")}
	_, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err == nil {
		t.Fatal("BaseFile がエラーを返したら Run() はエラーになるはず")
	}
	if !strings.HasPrefix(err.Error(), "configguard: ") {
		t.Errorf("エラーは configguard: で始まるはず, got %q", err.Error())
	}
	if !errors.Is(err, src.errBase) {
		t.Errorf("元のエラーを %%w でラップしているはず, got %v", err)
	}
}

func TestRunTargetFileErrorIsWrapped(t *testing.T) {
	c := configguard.New(nil)
	src := fakeEndpointSource{
		base:      map[string][]byte{configPath: []byte("checks: {}\n")},
		errTarget: errors.New("boom"),
	}
	_, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err == nil {
		t.Fatal("TargetFile がエラーを返したら Run() はエラーになるはず")
	}
	if !strings.HasPrefix(err.Error(), "configguard: ") {
		t.Errorf("エラーは configguard: で始まるはず, got %q", err.Error())
	}
	if !errors.Is(err, src.errTarget) {
		t.Errorf("元のエラーを %%w でラップしているはず, got %v", err)
	}
}

// TestRunBaseAbsentPasses は比較元に設定ファイルが無い（導入）場合、比較する緩和が無いので
// 合格になることを確認する。
func TestRunBaseAbsentPasses(t *testing.T) {
	c := configguard.New(nil)
	src := fakeEndpointSource{
		target: map[string][]byte{configPath: []byte("checks: {}\n")},
	}
	violations, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("比較元が無ければ違反 0 件のはず, got %v", violations)
	}
}

// TestRunTargetAbsentReportsOneViolation は終点で設定ファイルごと削除されていれば、
// 個々の checks.<key> を突き合わせず違反 1 件（最大の緩和）にすることを確認する。
func TestRunTargetAbsentReportsOneViolation(t *testing.T) {
	c := configguard.New(nil)
	src := fakeEndpointSource{
		base: map[string][]byte{configPath: []byte(`
checks:
  diff-size:
    type: diff-size
    max_lines: 10
`)},
	}
	violations, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("終点で削除されていれば違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Summary, configPath) {
		t.Errorf("Summary にファイルパスが含まれるはず, got %q", violations[0].Summary)
	}
}

// TestRunLoosingProducesOneViolationPerLoosening は configdiff.Diff の結果 1 件ごとに
// Violation を 1 件作り、Summary が Loosening.String() と一致することを確認する。
func TestRunLoosingProducesOneViolationPerLoosening(t *testing.T) {
	base := []byte(`
checks:
  diff-size:
    type: diff-size
    max_lines: 10
`)
	target := []byte(`
checks:
  diff-size:
    type: diff-size
    max_lines: 100
`)

	c := configguard.New(nil)
	src := fakeEndpointSource{
		base:   map[string][]byte{configPath: base},
		target: map[string][]byte{configPath: target},
	}
	violations, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	want := configdiff.Diff(base, target)
	if len(violations) != len(want) {
		t.Fatalf("違反件数 = %d, want %d（%v）", len(violations), len(want), want)
	}
	for i, l := range want {
		if violations[i].Summary != l.String() {
			t.Errorf("violations[%d].Summary = %q, want %q", i, violations[i].Summary, l.String())
		}
	}
	if len(violations) == 0 {
		t.Fatal("max_lines を上げているので違反が 1 件以上あるはず")
	}
}

// TestRunNoLoosingIsPassing は緩和が無い（同一）設定なら違反 0 件になることを確認する。
func TestRunNoLoosingIsPassing(t *testing.T) {
	data := []byte(`
checks:
  diff-size:
    type: diff-size
    max_lines: 10
`)

	c := configguard.New(nil)
	src := fakeEndpointSource{
		base:   map[string][]byte{configPath: data},
		target: map[string][]byte{configPath: data},
	}
	violations, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("比較元・終点が同一なら違反 0 件のはず, got %v", violations)
	}
}

// TestRunSetsViolationTarget は Run が各違反の Target を、checks.<key> 単位に絞った
// 値にすることを確認する（スコープ付き免除の照合キー）。
func TestRunSetsViolationTarget(t *testing.T) {
	base := []byte(`
checks:
  diff-size:
    type: diff-size
    max_lines: 10
`)
	target := []byte(`
checks:
  diff-size:
    type: diff-size
    max_lines: 100
`)

	c := configguard.New(nil)
	src := fakeEndpointSource{
		base:   map[string][]byte{configPath: base},
		target: map[string][]byte{configPath: target},
	}
	violations, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 || violations[0].Target != "checks.diff-size" {
		t.Fatalf("violations = %+v, want Target=checks.diff-size の 1 件", violations)
	}
}

// TestRunTargetAbsentViolationTargetIsConfigPath は、終点で設定ファイルごと削除された
// ときの違反 1 件の Target が ConfigPath 自身になることを確認する（個々の checks.<key>
// を突き合わせず、設定ファイル全体の緩和として扱うため）。
func TestRunTargetAbsentViolationTargetIsConfigPath(t *testing.T) {
	c := configguard.New(nil)
	src := fakeEndpointSource{
		base: map[string][]byte{configPath: []byte("checks: {}\n")},
	}
	violations, err := c.Run(check.Context{Source: src, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 || violations[0].Target != configPath {
		t.Fatalf("violations = %+v, want Target=%q の 1 件", violations, configPath)
	}
}

// TestExemptTargetsReturnsConstructorArgument は ExemptTargets が New に渡した targets を
// そのまま返すことを確認する（対象の一覧そのものの組み立ては internal/cli 側が
// configdiff.ExemptTargets で行うため、Check はそれを保持して返すだけ）。
func TestExemptTargetsReturnsConstructorArgument(t *testing.T) {
	targets := []string{"checks.diff-size", "required_version", configPath}
	c := configguard.New(targets)

	got := c.ExemptTargets()
	if len(got) != len(targets) {
		t.Fatalf("ExemptTargets() = %v, want %v", got, targets)
	}
	for i := range targets {
		if got[i] != targets[i] {
			t.Fatalf("ExemptTargets() = %v, want %v", got, targets)
		}
	}
}

// TestCheckImplementsScopedOnly は config-guard が対象を絞らない全体免除を受け付けない
// （check.ScopedOnly を実装する）ことを確認する。
func TestCheckImplementsScopedOnly(t *testing.T) {
	var _ check.ScopedOnly = configguard.New(nil)
}
