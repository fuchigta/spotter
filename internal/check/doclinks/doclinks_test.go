package doclinks_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/doclinks"
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

func mustNew(t *testing.T, cc config.CheckConfig) *doclinks.Check {
	t.Helper()
	c, err := doclinks.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{})
	if c.Granularity() != check.GranularityWorktree {
		t.Errorf("doc-links の granularity は worktree 固定のはず, got %v", c.Granularity())
	}
}

// TestRunInvalidDocsPatternIsError は、doc-links が worktree 粒度でファイルを直接読む
// 検査であり Source を持たないため、docutil.ResolveDocs（対象ドキュメントの解決）の失敗が
// [] check.Violation ではなく error として Run から伝播することを確認する。
func TestRunInvalidDocsPatternIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{Docs: []string{"["}})
	if _, err := c.Run(check.Context{FS: mapFS(nil)}); err == nil {
		t.Fatal("docs のパターンが不正な doublestar パターンなら Run() は error を返すはず")
	}
}

// TestRunTargetDocReadFailureIsError は、対象ドキュメントの解決（docutil.ResolveDocs）には
// 成功するが、その後の読み取り（fs.ReadFile）に失敗する場合、Run が [] check.Violation では
// なく error を返すことを確認する。
func TestRunTargetDocReadFailureIsError(t *testing.T) {
	fsys := newFailFS(map[string]string{
		"README.md": "参照: [text](missing.md)\n",
	}, "README.md")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	if _, err := c.Run(check.Context{FS: fsys}); err == nil {
		t.Fatal("対象ドキュメントの読み取りに失敗したら Run() は error を返すはず")
	}
}

// TestRunSingleBrokenLink は、1 リンクだけが壊れているドキュメントで、その 1 件が
// 期待どおりの "raw:line → 詳細" になることをパターンごとにまとめて確認する。
func TestRunSingleBrokenLink(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		docs []string
		want string
	}{
		{
			name: "相対リンクがリポジトリの外へ抜けずに存在しないファイルを指す",
			doc:  "参照: [granularity](../granularity.md)\n",
			docs: []string{"docs/checks/doc-sync.md"},
			want: "../granularity.md:1 → docs/granularity.md が存在しません",
		},
		{
			name: "ルート相対リンク（/ 始まり）",
			doc:  "参照: [top](/README.md) と [無い](/missing.md)\n",
			docs: []string{"README.md"},
			want: "/missing.md:1 → missing.md が存在しません",
		},
		{
			name: "リポジトリの外を指す相対リンク",
			doc:  "参照: [外](../../../etc/passwd.md)\n",
			docs: []string{"README.md"},
			want: "../../../etc/passwd.md:1 → はリポジトリの外を指しています",
		},
		{
			name: "タイトル付きリンクは target 部分だけを見る",
			doc:  "参照: [text](missing.md \"タイトル\")\n",
			docs: []string{"README.md"},
			want: "missing.md:1 → missing.md が存在しません",
		},
		{
			name: "参照定義のリンク先も検証する",
			doc:  "参照: [text][ref]\n\n[ref]: missing.md\n",
			docs: []string{"README.md"},
			want: "missing.md:3 → missing.md が存在しません",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := mapFS(map[string]string{tt.docs[0]: tt.doc})

			c := mustNew(t, config.CheckConfig{Docs: tt.docs})
			violations, err := c.Run(check.Context{FS: fsys})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if len(violations) != 1 {
				t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
			}
			if got := violations[0].Files; len(got) != 1 || got[0] != tt.want {
				t.Errorf("Files = %v, want [%s]", got, tt.want)
			}
		})
	}
}

func TestRunRelativeLinkResolves(t *testing.T) {
	fsys := mapFS(map[string]string{
		"docs/granularity.md":     "# granularity\n",
		"docs/checks/doc-sync.md": "参照: [granularity](../granularity.md)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"docs/checks/doc-sync.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("実在すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunURLEncoding(t *testing.T) {
	fsys := mapFS(map[string]string{
		"docs/a b.md": "# a b\n",
		"README.md":   "参照: [ab](docs/a%20b.md)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("%%20 をデコードして実在確認するはず, got %v", violations)
	}
}

func TestRunAngleBracketTarget(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "参照: [text](<a missing.md>)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("<...> を剥がして target を見るはず, got %d: %v", len(violations), violations)
	}
}

func TestRunExternalURLsAreSkipped(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "" +
			"[a](https://example.com/foo)\n" +
			"[b](http://example.com/foo)\n" +
			"[c](mailto:foo@example.com)\n" +
			"[d](//example.com/foo)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("外部 URL は対象外のはず, got %v", violations)
	}
}

func TestRunIgnoreList(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "[gen](docs/generated/missing.md)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, Ignore: []string{"docs/generated/missing.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ignore に載っていれば違反は出ないはず, got %v", violations)
	}
}

func TestRunLinksInsideCodeFenceAreSkipped(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "```md\n[text](missing.md)\n```\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("コードフェンス内のリンクは対象外のはず, got %v", violations)
	}
}

func TestRunDuplicateLinkTargetReportedOnce(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "[a](missing.md)\n本文\n[b](missing.md)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 {
		t.Fatalf("同じリンク先は 1 件にまとめるはず, got %v", got)
	}
}

func TestRunCheckAnchorsDisabledByDefault(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "# タイトル\n\n[text](#no-such-heading)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("check_anchors 既定 false ではアンカーを検証しないはず, got %v", violations)
	}
}

func TestRunCheckAnchorsEnabled(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "# My Heading\n\n[ok](#my-heading) [ng](#no-such-heading)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, CheckAnchors: true})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("存在しない見出しへのアンカーだけ違反になるはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "#no-such-heading:3 → 見出し \"no-such-heading\" が README.md に見つかりません" {
		t.Errorf("Files = %v", got)
	}
}

// TestRunCheckAnchorsDuplicateHeadingSlugsGetSequentialSuffix は、同名の見出しが複数ある
// 場合、2 番目以降のスラグに "-1" "-2" ... と連番が付くことを確認する（GitHub 準拠）。
func TestRunCheckAnchorsDuplicateHeadingSlugsGetSequentialSuffix(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "" +
			"# 概要\n\n" +
			"# 概要\n\n" +
			"# 概要\n\n" +
			"[a](#概要) [b](#概要-1) [c](#概要-2) [d](#概要-3)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, CheckAnchors: true})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("3 つの同名見出しは #概要・#概要-1・#概要-2 の 3 つに解決されるので、存在しない #概要-3 だけ違反になるはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "#概要-3:7 → 見出し \"概要-3\" が README.md に見つかりません" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunCheckAnchorsCrossFile(t *testing.T) {
	fsys := mapFS(map[string]string{
		"docs/guide.md": "# Getting Started\n",
		"README.md":     "[ok](docs/guide.md#getting-started) [ng](docs/guide.md#no-such)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, CheckAnchors: true})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
}

func TestRunLinkSyntaxInsideInlineCodeIsSkipped(t *testing.T) {
	// ドキュメントがリンク記法そのものを例示する場合（インラインコードスパンの中）、
	// 本物のリンクとして誤検知してはいけない。
	fsys := mapFS(map[string]string{
		"README.md": "この検査は `[text](target)` のような記法を拾います。\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("インラインコード内のリンク記法の例示は対象外のはず, got %v", violations)
	}
}

func TestRunImageLink(t *testing.T) {
	fsys := mapFS(map[string]string{
		"README.md": "![alt](missing.png)\n",
	})

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{FS: fsys})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("画像リンクも対象のはず, got %d: %v", len(violations), violations)
	}
}
