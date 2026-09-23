package consistency_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/consistency"
	"github.com/fuchigta/spotter/internal/config"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// commitTypesConfig は、それぞれ異なる書式でコミット type の一覧を持つ 3 箇所
// （cliff.toml・シェルスクリプト・Markdown 表）を突き合わせる設定。
func commitTypesConfig() config.CheckConfig {
	return config.CheckConfig{
		Sources: []config.ConsistencySource{
			{
				File:    "cliff.toml",
				Line:    `message = .\^[A-Za-z]+.`,
				Extract: `\^([A-Za-z]+)`,
			},
			{
				File:    "check-commit-subject.sh",
				Line:    `^PATTERN=`,
				Extract: `\(([a-zA-Z|]+)\)`,
				Split:   "|",
			},
			{
				File:    "CLAUDE.md",
				Line:    "^\\| `[a-zA-Z]+` \\|",
				Extract: "`([a-zA-Z]+)`",
			},
		},
	}
}

func TestRunConsistent(t *testing.T) {
	root := t.TempDir()
	// chore(release) はスコープ限定の抑制指定であり、type としては通常の chore 行と
	// 同じ "chore" を抽出する想定の挙動。3 箇所とも chore を含めておくことで
	// この重複が無害であることも確認する。
	writeFile(t, root, "cliff.toml", `
commit_parsers = [
  { message = '^feat', group = 'Features' },
  { message = '^fix', group = 'Fixes' },
  { message = '^chore', group = 'Miscellaneous' },
  { message = '^chore\(release\)', skip = true },
  { message = '.*', group = 'その他' },
]
`)
	writeFile(t, root, "check-commit-subject.sh", `PATTERN='^(feat|fix|chore)(\([a-zA-Z0-9._/-]+\))?!?: .+'`+"\n")
	writeFile(t, root, "CLAUDE.md", "| `feat` | 機能追加 |\n| `fix` | 不具合修正 |\n| `chore` | 雑務 |\n")

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("3 箇所が一致していれば違反は出ないはず, got %v", violations)
	}
}

func TestRunMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cliff.toml", `
commit_parsers = [
  { message = '^feat', group = 'Features' },
  { message = '^fix', group = 'Fixes' },
  { message = '^perf', group = 'Performance' },
]
`)
	writeFile(t, root, "check-commit-subject.sh", `PATTERN='^(feat|fix)(\([a-zA-Z0-9._/-]+\))?!?: .+'`+"\n")
	writeFile(t, root, "CLAUDE.md", "| `feat` | 機能追加 |\n| `fix` | 不具合修正 |\n")

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("食い違いは 1 つの Violation にまとめるはず, got %d: %v", len(violations), violations)
	}
	if len(violations[0].Files) != 1 {
		t.Fatalf("食い違う要素は perf の 1 つだけのはず, got %v", violations[0].Files)
	}
	want := "`perf`: CLAUDE.md, check-commit-subject.sh に無い（cliff.toml にある）"
	if violations[0].Files[0] != want {
		t.Errorf("Files[0] = %q, want %q", violations[0].Files[0], want)
	}
}

func TestRunEmptyExtractionIsError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cliff.toml", "何も一致しない内容\n")
	writeFile(t, root, "check-commit-subject.sh", `PATTERN='^(feat|fix)(\([a-zA-Z0-9._/-]+\))?!?: .+'`+"\n")
	writeFile(t, root, "CLAUDE.md", "| `feat` | 機能追加 |\n")

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := c.Run(check.Context{Root: root}); err == nil {
		t.Fatal("抽出結果が空なら Run() はエラーになるはず")
	}
}

func TestNewRequiresAtLeastTwoSources(t *testing.T) {
	if _, err := consistency.New(config.CheckConfig{
		Sources: []config.ConsistencySource{{File: "a", Extract: "(x)"}},
	}); err == nil {
		t.Fatal("sources が 1 つなら New() はエラーになるはず")
	}
}

func TestNewRequiresCaptureGroup(t *testing.T) {
	if _, err := consistency.New(config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a", Extract: "x"},
			{File: "b", Extract: "(y)"},
		},
	}); err == nil {
		t.Fatal("キャプチャグループが無ければ New() はエラーになるはず")
	}
}

// extract のキャプチャグループが 2 個以上あると、これまでは 1 個目だけを黙って使っていた。
// 起動時エラーにすることで、書き手の意図しない挙動を防ぐ。
func TestNewRequiresExactlyOneCaptureGroup(t *testing.T) {
	if _, err := consistency.New(config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a", Extract: "(x)(y)"},
			{File: "b", Extract: "(y)"},
		},
	}); err == nil {
		t.Fatal("キャプチャグループが 2 個以上なら New() はエラーになるはず")
	}
}

func TestRunMissingFileIsError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cliff.toml", `
commit_parsers = [
  { message = '^feat', group = 'Features' },
]
`)
	// check-commit-subject.sh をわざと作らない。
	writeFile(t, root, "CLAUDE.md", "| `feat` | 機能追加 |\n")

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.Run(check.Context{Root: root})
	if err == nil {
		t.Fatal("存在しない file を参照する source があれば Run() はエラーになるはず")
	}
	if !strings.Contains(err.Error(), "check-commit-subject.sh") {
		t.Errorf("エラーメッセージにどの source のファイルが無いか含まれるはず, got %q", err.Error())
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("エラーは os.ErrNotExist を wrap しているはず, got %v", err)
	}
}

func TestNewUntilRequiresLine(t *testing.T) {
	if _, err := consistency.New(config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a", Extract: "(x)", Until: `^\]`},
			{File: "b", Extract: "(x)"},
		},
	}); err == nil {
		t.Fatal("line なしで until を指定したら New() はエラーになるはず")
	}
}

// until を使うと、line にマッチした行から until にマッチする行まで（両端含む）を
// 1 ブロックとしてまとめ、ブロック内の各行に extract を当てられる。複数行に折り返した
// YAML 配列を拾うのが主な用途。
func TestRunUntilCollectsMultilineBlock(t *testing.T) {
	root := t.TempDir()
	// ブロックの終端（until）に到達させるため、末尾に非インデントの番兵行を足す。
	writeFile(t, root, "a.yml", `allowed_types:
  - feat
  - fix
  - chore
done: true
`)
	writeFile(t, root, "b.yml", "allowed_types: [feat, fix, chore]\n")

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{
				File:    "a.yml",
				Line:    `^allowed_types:`,
				Until:   `^\S`, // 次のトップレベルキー、またはファイル末尾側の非インデント行
				Extract: `-\s*(\w+)`,
			},
			{
				File:    "b.yml",
				Extract: `allowed_types:\s*\[(.*)\]`,
				Split:   ",",
			},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ブロック内の要素が一致していれば違反は出ないはず, got %v", violations)
	}
}

func TestRunUntilMissingTerminatorIsError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.yml", `allowed_types:
  - feat
  - fix
`)
	writeFile(t, root, "b.yml", "allowed_types: [feat, fix]\n")

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a.yml", Line: `^allowed_types:`, Until: `^\S`, Extract: `-\s*(\w+)`},
			{File: "b.yml", Extract: `allowed_types:\s*\[(.*)\]`, Split: ","},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := c.Run(check.Context{Root: root}); err == nil {
		t.Fatal("until にマッチする行がファイル末尾まで見つからなければ Run() はエラーになるはず")
	}
}

func TestGranularity(t *testing.T) {
	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.Granularity() != check.GranularityWorktree {
		t.Errorf("consistency の granularity は worktree 固定のはず, got %v", c.Granularity())
	}
}
