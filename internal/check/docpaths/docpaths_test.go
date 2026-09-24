package docpaths_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docpaths"
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

// failFS は fstest.MapFS を包み、fail に載ったパスの Open・ReadFile だけエラーを返す。
// fs.ReadFile は引数の fs.FS が ReadFileFS を実装していればそちらを優先して使う
// （io/fs.ReadFile の実装を参照）。MapFS はそれ自体 ReadFile を実装しているため、
// Open だけ上書きしても ReadFile 経由の呼び出しは素通りしてしまう。ここでは対象パスに
// ついて Open・ReadFile の両方を上書きし、検査本体がどちらの経路で読んでもテストの
// 意図どおり失敗するようにする。
type failFS struct {
	fstest.MapFS
	fail map[string]bool
}

func newFailFS(files map[string]string, failPaths ...string) failFS {
	fail := make(map[string]bool, len(failPaths))
	for _, p := range failPaths {
		fail[p] = true
	}
	return failFS{MapFS: mapFS(files), fail: fail}
}

func (f failFS) Open(name string) (fs.File, error) {
	if f.fail[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errFakeRead}
	}
	return f.MapFS.Open(name)
}

func (f failFS) ReadFile(name string) ([]byte, error) {
	if f.fail[name] {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: errFakeRead}
	}
	return f.MapFS.ReadFile(name)
}

func TestRunMissingPath(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "参照先は `internal/cli/root.go` です。\n",
	})

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d", len(violations))
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/cli/root.go" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunExistingPath(t *testing.T) {
	fsys := mapFS(map[string]string{
		"internal/cli/root.go": "package cli\n",
		"README.md":            "参照先は `internal/cli/root.go` です。\n",
	})

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("実在すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunGlobPattern(t *testing.T) {
	fsys := mapFS(map[string]string{
		"internal/cli/root.go": "package cli\n",
		"README.md":            "参照先は `internal/cli/*.go` です。\n",
	})

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("グロブに 1 つでも一致すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunIgnoreList(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "将来の拡張点は `internal/source/codex` です。\n",
	})

	c, err := docpaths.New(config.CheckConfig{
		Docs:         []string{"README.md"},
		Ignore:       []string{"internal/source/codex"},
		PathPrefixes: []string{"internal"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ignore に載っていれば違反は出ないはず, got %v", violations)
	}
}

func TestRunIgnoresNonPathBackticks(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "コマンドは `spotter check` を使います。\n",
	})

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("path_prefixes に一致しない候補は無視するはず, got %v", violations)
	}
}

func TestRunIgnoresBackticksInsideCodeFence(t *testing.T) {
	// フェンス内のバッククォート（ここでは奇数個）を数えてしまうと、それ以降の
	// インラインスパンの対応がずれて誤抽出・抽出漏れの原因になる。
	fsys := mapFS(map[string]string{
		"README.md": "" +
			"# タイトル\n\n" +
			"```sh\n" +
			"echo `date`\n" +
			"```\n\n" +
			"参照先は `internal/missing.go` です。\n",
	})

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("フェンス後の `internal/missing.go` は候補として抽出されるはず, got %d件: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/missing.go" {
		t.Errorf("Files = %v", got)
	}
}

// TestRunGlobPatternHandling は、地の文の候補や docs 側の指定に doublestar の
// 特殊文字が絡むケースをまとめて確認する。
func TestRunGlobPatternHandling(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		docs     []string
		prefixes []string
	}{
		{
			// "[" を含む地の文が候補として拾われても、不正な glob として検査全体を
			// 異常終了させてはいけない（「存在しない」として違反に倒す）。
			name:     "不正な glob 候補でも Run は異常終了せず違反として報告する",
			files:    map[string]string{"README.md": "参照先は `internal/cli/[abc*.go` です。\n"},
			docs:     []string{"README.md"},
			prefixes: []string{"internal"},
		},
		{
			name:     "docs の \"**\" パターンで深い階層のファイルも見つかる",
			files:    map[string]string{"a/b/c/guide.md": "参照先は `internal/missing.go` です。\n"},
			docs:     []string{"a/**/*.md"},
			prefixes: []string{"internal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := mapFS(tt.files)

			c, err := docpaths.New(config.CheckConfig{Docs: tt.docs, PathPrefixes: tt.prefixes})
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}

			violations, err := c.Run(check.Context{FS: fsys})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if len(violations) != 1 {
				t.Fatalf("違反は 1 件のはず, got %d件: %v", len(violations), violations)
			}
		})
	}
}

func TestRunDefaultDocs(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md":           "参照先は `internal/missing.go` です。\n",
		"docs/guide.md":       "参照先は `cmd/missing.go` です。\n",
		"docs/checks/deep.md": "参照先は `internal/deep-missing.go` です。\n",
	})

	c, err := docpaths.New(config.CheckConfig{PathPrefixes: []string{"internal", "cmd"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("既定では \"**/*.md\" を再帰的に見るはず, got %d件: %v", len(violations), violations)
	}
}

func TestRunPathPrefixesUnsetMatchesNothing(t *testing.T) {
	// path_prefixes は必須なので、未指定の設定は New でエラーになる。
	if _, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}}); err == nil {
		t.Fatal("path_prefixes 未指定なら New() はエラーになるはず")
	}
}

// TestNewPathPrefixesOnlyEmptyStringIsError は、path_prefixes が空文字だけの配列
// （[""]）の場合も New() がエラーになることを確認する。未指定（長さ 0）とは別に、
// 要素はあるが有効な接頭辞が 1 つも無いケースを compilePathLikeRe 側で弾いている。
func TestNewPathPrefixesOnlyEmptyStringIsError(t *testing.T) {
	if _, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{""}}); err == nil {
		t.Fatal("path_prefixes が空文字だけの配列なら New() はエラーになるはず")
	}
}

// TestRunPathPrefixesFiltersCandidates は、path_prefixes に含めた接頭辞だけが
// 候補になり、含めていない接頭辞（.github/ も他と同じ扱い）は無視されることを確認する。
func TestRunPathPrefixesFiltersCandidates(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		wantFiles []string
	}{
		{
			name:      ".github/ は path_prefixes に含めない限り候補にならない",
			doc:       "参照先は `.github/missing.yml` です。\n",
			wantFiles: nil,
		},
		{
			name:      "path_prefixes に含めた接頭辞は候補になる",
			doc:       "参照先は `src/missing.ts` です。\n",
			wantFiles: []string{"src/missing.ts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := mapFS(map[string]string{"README.md": tt.doc})

			c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"src"}})
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}

			violations, err := c.Run(check.Context{FS: fsys})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if tt.wantFiles == nil {
				if violations != nil {
					t.Errorf("違反は無いはず, got %v", violations)
				}
				return
			}
			if len(violations) != 1 {
				t.Fatalf("違反は 1 件のはず, got %d件: %v", len(violations), violations)
			}
			if got := violations[0].Files; len(got) != 1 || got[0] != tt.wantFiles[0] {
				t.Errorf("Files = %v, want %v", got, tt.wantFiles)
			}
		})
	}
}

// TestRunInvalidDocsPatternIsError は、doc-paths が worktree 粒度でファイルを直接読む
// 検査であり Source を持たないため、docutil.ResolveDocs（対象ドキュメントの解決）の失敗が
// [] check.Violation ではなく error として Run から伝播することを確認する。
func TestRunInvalidDocsPatternIsError(t *testing.T) {
	c, err := docpaths.New(config.CheckConfig{Docs: []string{"["}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := c.Run(check.Context{FS: mapFS(nil)}); err == nil {
		t.Fatal("docs のパターンが不正な doublestar パターンなら Run() は error を返すはず")
	}
}

// TestRunTargetDocReadFailureIsError は、対象ドキュメントの解決（docutil.ResolveDocs）には
// 成功するが、その後の読み取り（fs.ReadFile）に失敗する場合、Run が [] check.Violation では
// なく error を返すことを確認する。
func TestRunTargetDocReadFailureIsError(t *testing.T) {
	fsys := newFailFS(map[string]string{
		"README.md": "参照先は `internal/missing.go` です。\n",
	}, "README.md")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := c.Run(check.Context{FS: fsys}); err == nil {
		t.Fatal("対象ドキュメントの読み取りに失敗したら Run() は error を返すはず")
	}
}

func TestRunDuplicateCandidateReportedOnce(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "参照は `internal/missing.go` です。再掲: `internal/missing.go`。\n",
	})

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/missing.go" {
		t.Errorf("同じ候補パスが複数回出現しても 1 回だけ報告されるはず, got %v", got)
	}
}

func TestGranularity(t *testing.T) {
	c, err := docpaths.New(config.CheckConfig{PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.Granularity() != check.GranularityWorktree {
		t.Errorf("doc-paths の granularity は worktree 固定のはず, got %v", c.Granularity())
	}
}
