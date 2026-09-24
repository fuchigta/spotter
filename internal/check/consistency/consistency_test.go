package consistency_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/consistency"
	"github.com/fuchigta/spotter/internal/config"
)

// mapFS は files（パス→内容）から fstest.MapFS を組み立てる。
func mapFS(files map[string]string) fstest.MapFS {
	m := make(fstest.MapFS, len(files))
	for p, content := range files {
		m[p] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

// errFakeRead はテストが注入する読み取り失敗のエラー。
var errFakeRead = errors.New("fake: 読み取りに失敗しました")

// failFS は fstest.MapFS を包み、failRead に載ったパスの Open・ReadFile、failStat に
// 載ったパスの Stat をそれぞれエラーにする。fs.ReadFile は引数の fs.FS が ReadFileFS を
// 実装していればそちらを優先して使い、fs.Stat も同様に StatFS を優先する
// （io/fs.ReadFile・io/fs.Stat の実装を参照）。MapFS はそれ自体 ReadFile・Stat を実装して
// いるため、Open だけ上書きしても素通りしてしまう。ここでは対象パスについて該当する
// メソッドを全て上書きし、検査本体がどの経路で読んでもテストの意図どおり失敗するように
// する。
type failFS struct {
	fstest.MapFS
	failRead map[string]bool
	failStat map[string]bool
}

func newFailReadFS(files map[string]string, failPaths ...string) failFS {
	fail := make(map[string]bool, len(failPaths))
	for _, p := range failPaths {
		fail[p] = true
	}
	return failFS{MapFS: mapFS(files), failRead: fail}
}

func newFailStatFS(files map[string]string, failPaths ...string) failFS {
	fail := make(map[string]bool, len(failPaths))
	for _, p := range failPaths {
		fail[p] = true
	}
	return failFS{MapFS: mapFS(files), failStat: fail}
}

func (f failFS) Open(name string) (fs.File, error) {
	if f.failRead[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errFakeRead}
	}
	return f.MapFS.Open(name)
}

func (f failFS) ReadFile(name string) ([]byte, error) {
	if f.failRead[name] {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: errFakeRead}
	}
	return f.MapFS.ReadFile(name)
}

func (f failFS) Stat(name string) (fs.FileInfo, error) {
	if f.failStat[name] {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: errFakeRead}
	}
	return f.MapFS.Stat(name)
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
	// chore(release) はスコープ限定の抑制指定であり、type としては通常の chore 行と
	// 同じ "chore" を抽出する想定の挙動。3 箇所とも chore を含めておくことで
	// この重複が無害であることも確認する。
	fsys := mapFS(map[string]string{
		"cliff.toml": `
commit_parsers = [
  { message = '^feat', group = 'Features' },
  { message = '^fix', group = 'Fixes' },
  { message = '^chore', group = 'Miscellaneous' },
  { message = '^chore\(release\)', skip = true },
  { message = '.*', group = 'その他' },
]
`,
		"check-commit-subject.sh": `PATTERN='^(feat|fix|chore)(\([a-zA-Z0-9._/-]+\))?!?: .+'` + "\n",
		"CLAUDE.md":               "| `feat` | 機能追加 |\n| `fix` | 不具合修正 |\n| `chore` | 雑務 |\n",
	})

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("3 箇所が一致していれば違反は出ないはず, got %v", violations)
	}
}

func TestRunMismatch(t *testing.T) {
	fsys := mapFS(map[string]string{
		"cliff.toml": `
commit_parsers = [
  { message = '^feat', group = 'Features' },
  { message = '^fix', group = 'Fixes' },
  { message = '^perf', group = 'Performance' },
]
`,
		"check-commit-subject.sh": `PATTERN='^(feat|fix)(\([a-zA-Z0-9._/-]+\))?!?: .+'` + "\n",
		"CLAUDE.md":               "| `feat` | 機能追加 |\n| `fix` | 不具合修正 |\n",
	})

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
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

// TestRunMultipleMatchesPerLineAreAllCollected は、1 行に extract が複数回マッチすると
// 全てが集合に加わることを確認する（最初の 1 マッチだけを拾うのではない）。
func TestRunMultipleMatchesPerLineAreAllCollected(t *testing.T) {
	fsys := mapFS(map[string]string{
		"a.txt": "feat fix chore\n",
		"b.txt": "allowed: [feat, fix, chore]\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a.txt", Extract: `(\w+)`},
			{File: "b.txt", Extract: `\[(.*)\]`, Split: ","},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("1 行の 3 マッチ全てが集合に入っていれば違反は出ないはず, got %v", violations)
	}
}

// TestRunMultipleMismatchesAreSorted は、食い違う要素が複数あるとき、抽出順（宣言順）
// ではなく要素の値でソートされて表示されることを確認する。
func TestRunMultipleMismatchesAreSorted(t *testing.T) {
	// ファイル中では zebra → apple の順（アルファベット逆順）で出現させる。
	fsys := mapFS(map[string]string{
		"a.txt": "zebra apple feat\n",
		"b.txt": "feat\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a.txt", Extract: `(\w+)`},
			{File: "b.txt", Extract: `(\w+)`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("食い違いは 1 つの Violation にまとめるはず, got %d: %v", len(violations), violations)
	}
	want := []string{
		"`apple`: b.txt に無い（a.txt にある）",
		"`zebra`: b.txt に無い（a.txt にある）",
	}
	got := violations[0].Files
	if len(got) != len(want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Files[%d] = %q, want %q（要素はアルファベット順にソートされるはず）", i, got[i], want[i])
		}
	}
}

func TestRunEmptyExtractionIsError(t *testing.T) {
	fsys := mapFS(map[string]string{
		"cliff.toml":              "何も一致しない内容\n",
		"check-commit-subject.sh": `PATTERN='^(feat|fix)(\([a-zA-Z0-9._/-]+\))?!?: .+'` + "\n",
		"CLAUDE.md":               "| `feat` | 機能追加 |\n",
	})

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := c.Run(check.Context{FS: fsys}); err == nil {
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

// extract のキャプチャグループが 2 個以上あると、どれを要素にするかが書き手の意図と
// ずれうるため、ちょうど 1 個でなければ起動時エラーにする。
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
	// check-commit-subject.sh をわざと作らない。
	fsys := mapFS(map[string]string{
		"cliff.toml": `
commit_parsers = [
  { message = '^feat', group = 'Features' },
]
`,
		"CLAUDE.md": "| `feat` | 機能追加 |\n",
	})

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.Run(check.Context{FS: fsys})
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

// TestRunFileReadFailureIsError は、sources[].file が実在するのに読み取り自体
// （fs.ReadFile）に失敗する場合、存在しない場合（TestRunMissingFileIsError）と違い
// fs.ErrNotExist を伴わないエラーとして Run が報告することを確認する。
func TestRunFileReadFailureIsError(t *testing.T) {
	fsys := newFailReadFS(map[string]string{
		"cliff.toml": `
commit_parsers = [
  { message = '^feat', group = 'Features' },
]
`,
		"check-commit-subject.sh": `PATTERN='^(feat)(\([a-zA-Z0-9._/-]+\))?!?: .+'` + "\n",
		"CLAUDE.md":               "| `feat` | 機能追加 |\n",
	}, "check-commit-subject.sh")

	c, err := consistency.New(commitTypesConfig())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.Run(check.Context{FS: fsys})
	if err == nil {
		t.Fatal("読み取りに失敗する source があれば Run() はエラーになるはず")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("実在するファイルの読み取り失敗は fs.ErrNotExist を伴わないはず, got %v", err)
	}
	if !strings.Contains(err.Error(), "check-commit-subject.sh") {
		t.Errorf("エラーメッセージにどの source のファイルが読めないか含まれるはず, got %q", err.Error())
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
	// ブロックの終端（until）に到達させるため、末尾に非インデントの番兵行を足す。
	fsys := mapFS(map[string]string{
		"a.yml": `allowed_types:
  - feat
  - fix
  - chore
done: true
`,
		"b.yml": "allowed_types: [feat, fix, chore]\n",
	})

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

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ブロック内の要素が一致していれば違反は出ないはず, got %v", violations)
	}
}

func TestRunUntilMissingTerminatorIsError(t *testing.T) {
	fsys := mapFS(map[string]string{
		"a.yml": `allowed_types:
  - feat
  - fix
`,
		"b.yml": "allowed_types: [feat, fix]\n",
	})

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

	if _, err := c.Run(check.Context{FS: fsys}); err == nil {
		t.Fatal("until にマッチする行がファイル末尾まで見つからなければ Run() はエラーになるはず")
	}
}

func TestNewRequiresAtLeastOneNonSubsetSource(t *testing.T) {
	if _, err := consistency.New(config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "a", Extract: "(x)", Subset: true},
			{File: "b", Extract: "(x)", Subset: true},
		},
	}); err == nil {
		t.Fatal("subset ではない source が 1 つも無ければ New() はエラーになるはず")
	}
}

// subset な source は、subset ではない source の和集合に無い要素を持つと違反になるが、
// 欠けていても違反にならない。
func TestRunSubsetMissingIsAllowedExtraIsViolation(t *testing.T) {
	// README には feat だけ抜粋しているが、余分に docs も書いてしまっている。
	fsys := mapFS(map[string]string{
		"cliff.toml": `
commit_parsers = [
  { message = '^feat', group = 'Features' },
  { message = '^fix', group = 'Fixes' },
  { message = '^chore', group = 'Miscellaneous' },
]
`,
		".spotter.yml": "allowed_types: [feat, fix, chore]\n",
		"README.md":    "| `feat` |\n| `docs` |\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "cliff.toml", Line: `message = .\^[A-Za-z]+.`, Extract: `\^([A-Za-z]+)`},
			{File: ".spotter.yml", Extract: `allowed_types:\s*\[(.*)\]`, Split: ","},
			{File: "README.md", Extract: "`([a-z]+)`", Subset: true},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("README.md が余分に docs を持つので違反が 1 件出るはず, got %d: %v", len(violations), violations)
	}
	want := "`docs`: .spotter.yml, cliff.toml に無い（README.md にある）"
	if len(violations[0].Files) != 1 || violations[0].Files[0] != want {
		t.Errorf("Files = %v, want [%q]", violations[0].Files, want)
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

// glob source の起動時バリデーション。
func TestNewGlobValidation(t *testing.T) {
	tests := []struct {
		name    string
		sources []config.ConsistencySource
		wantErr string
	}{
		{
			name: "file も glob も無い",
			sources: []config.ConsistencySource{
				{},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "file か glob のどちらか一方",
		},
		{
			name: "file と glob の両方がある",
			sources: []config.ConsistencySource{
				{File: "a", Glob: "**/*.md", Extract: "(x)"},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "file か glob のどちらか一方",
		},
		{
			name: "glob と extract の併用",
			sources: []config.ConsistencySource{
				{Glob: "**/*.md", Extract: "(x)"},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "併用できません",
		},
		{
			name: "glob と line の併用",
			sources: []config.ConsistencySource{
				{Glob: "**/*.md", Line: "^x"},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "併用できません",
		},
		{
			name: "glob と until の併用",
			sources: []config.ConsistencySource{
				{Glob: "**/*.md", Until: "^x"},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "併用できません",
		},
		{
			name: "glob と split の併用",
			sources: []config.ConsistencySource{
				{Glob: "**/*.md", Split: ","},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "併用できません",
		},
		{
			name: "不正な glob パターン",
			sources: []config.ConsistencySource{
				{Glob: "["},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "glob \"[\" が不正です",
		},
		{
			name: "不正な exclude パターン",
			sources: []config.ConsistencySource{
				{Glob: "**/*.md", Exclude: []string{"["}},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "exclude \"[\" が不正です",
		},
		{
			name: "file と base の併用",
			sources: []config.ConsistencySource{
				{File: "a", Extract: "(x)", Base: "docs"},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "base/exclude は glob と併用する場合のみ",
		},
		{
			name: "file と exclude の併用",
			sources: []config.ConsistencySource{
				{File: "a", Extract: "(x)", Exclude: []string{"*.md"}},
				{File: "b", Extract: "(x)"},
			},
			wantErr: "base/exclude は glob と併用する場合のみ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := consistency.New(config.CheckConfig{Sources: tt.sources})
			if err == nil {
				t.Fatal("New() はエラーになるはず")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// sources[].file の正規化（fs.FS のパス表記に揃える）の起動時バリデーション。
// file はリポジトリルートからの相対パスである必要があり、リポジトリの外を指す書き方は
// New() でエラーになる。
func TestNewFileOutsideRepoIsError(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{"親ディレクトリへ抜ける相対パス", "../secret.md"},
		{"先頭が / の絶対パス", "/etc/passwd"},
		{"ドライブ文字の絶対パス", "C:/secret.md"},
		{"ドライブ文字と \\ 区切りの絶対パス", `C:\secret.md`},
		{"ドライブ文字の相対パス", "C:secret.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := consistency.New(config.CheckConfig{
				Sources: []config.ConsistencySource{
					{File: tt.file, Extract: "(x)"},
					{File: "b", Extract: "(x)"},
				},
			})
			if err == nil {
				t.Fatalf("file %q はリポジトリの外を指すので New() はエラーになるはず", tt.file)
			}
		})
	}
}

// TestRunFileNormalizesDotSlashAndBackslash は、sources[].file に "./" を付けても、
// "\" 区切りで書いても、OS に依らず fs.FS のパス表記（"/" 区切り、"./" 無し）に
// 正規化されて読めることを確認する。
func TestRunFileNormalizesDotSlashAndBackslash(t *testing.T) {
	fsys := mapFS(map[string]string{
		"a.txt":     "feat\n",
		"sub/b.txt": "feat\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{File: "./a.txt", Extract: `(\w+)`},
			{File: `sub\b.txt`, Extract: `(\w+)`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("\"./\" 付きや OS 標準区切りの file も正規化されて読めるはず, got %v", violations)
	}
}

// glob source は、一致したファイルパスの一覧をそのまま集合にする。
func TestRunGlobMatchesFileSet(t *testing.T) {
	fsys := mapFS(map[string]string{
		"docs/a.md":        "",
		"docs/checks/b.md": "",
		"docs/README.md":   "",
		"index.txt":        "docs/a.md\ndocs/checks/b.md\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "docs/**/*.md", Exclude: []string{"docs/README.md"}},
			{File: "index.txt", Extract: `^(docs/\S+\.md)$`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("exclude で除いた README.md 以外は一致するはず, got %v", violations)
	}
}

// base を指定すると、一致したパスからその接頭辞ディレクトリを取り除いた相対パスが要素になる。
func TestRunGlobBase(t *testing.T) {
	fsys := mapFS(map[string]string{
		"docs/a.md":        "",
		"docs/checks/b.md": "",
		"index.txt":        "a.md\nchecks/b.md\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "docs/**/*.md", Base: "docs"},
			{File: "index.txt", Extract: `^(\S+\.md)$`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("base 除去後は一致するはず, got %v", violations)
	}
}

// base 配下に無いパスが一致したら実行時エラーになる。
func TestRunGlobBaseMismatchIsError(t *testing.T) {
	fsys := mapFS(map[string]string{
		"docs/a.md":  "",
		"other/b.md": "",
		"index.txt":  "x\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "**/*.md", Base: "docs"},
			{File: "index.txt", Extract: `(x)`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.Run(check.Context{FS: fsys})
	if err == nil {
		t.Fatal("base 配下に無いパスが一致したら Run() はエラーになるはず")
	}
	if !strings.Contains(err.Error(), "base") || !strings.Contains(err.Error(), "other/b.md") {
		t.Errorf("エラーメッセージに base と一致した対象パスを含むはず, got %q", err.Error())
	}
}

// glob が 1 件も一致しなければ、file の抽出結果が空のときと同じくエラーになる。
func TestRunGlobNoMatchIsError(t *testing.T) {
	fsys := mapFS(map[string]string{
		"readme.txt": "",
		"index.txt":  "x\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "docs/**/*.md"},
			{File: "index.txt", Extract: `(x)`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.Run(check.Context{FS: fsys})
	if err == nil {
		t.Fatal("glob が 1 件も一致しなければ Run() はエラーになるはず")
	}
	if !strings.Contains(err.Error(), "docs/**/*.md") {
		t.Errorf("エラーメッセージに glob パターンを含むはず, got %q", err.Error())
	}
}

// glob はディレクトリを要素に含めない。
func TestRunGlobExcludesDirectories(t *testing.T) {
	fsys := fstest.MapFS{
		"sub/a.txt":  &fstest.MapFile{Data: []byte("")},
		"sub/nested": &fstest.MapFile{Mode: fs.ModeDir},
		"index.txt":  &fstest.MapFile{Data: []byte("sub/a.txt\n")},
	}

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "sub/*"},
			{File: "index.txt", Extract: `^(sub/\S+)$`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ディレクトリ sub/nested が要素に混ざっているはず, got %v", violations)
	}
}

// subset は glob source でも使える。
func TestRunGlobSubset(t *testing.T) {
	// c.md は実在しないが index.txt には書かれている（余分な転記）。
	fsys := mapFS(map[string]string{
		"docs/a.md": "",
		"docs/b.md": "",
		"index.txt": "a.md\nb.md\nc.md\n",
	})

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "docs/**/*.md", Base: "docs"},
			{File: "index.txt", Extract: `^(\S+\.md)$`, Subset: true},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("index.txt が余分に c.md を持つので違反が 1 件出るはず, got %d: %v", len(violations), violations)
	}
	want := "`c.md`: docs/**/*.md に無い（index.txt にある）"
	if len(violations[0].Files) != 1 || violations[0].Files[0] != want {
		t.Errorf("Files = %v, want [%q]", violations[0].Files, want)
	}
}

// glob が一致させたパスの fs.Stat に失敗する場合も、対象ドキュメントの読み取り失敗と
// 同様に Run が [] check.Violation ではなく error を返すことを確認する。
func TestRunGlobStatFailureIsError(t *testing.T) {
	fsys := newFailStatFS(map[string]string{
		"docs/a.md": "",
		"index.txt": "a.md\n",
	}, "docs/a.md")

	cfg := config.CheckConfig{
		Sources: []config.ConsistencySource{
			{Glob: "docs/**/*.md", Base: "docs"},
			{File: "index.txt", Extract: `^(\S+\.md)$`},
		},
	}

	c, err := consistency.New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := c.Run(check.Context{FS: fsys}); err == nil {
		t.Fatal("glob が一致させたパスの Stat に失敗したら Run() はエラーになるはず")
	}
}
