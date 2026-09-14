package doclinks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/doclinks"
	"github.com/fuchigta/spotter/internal/config"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
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

func TestRunBrokenRelativeLink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/checks/doc-sync.md", "参照: [granularity](../granularity.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"docs/checks/doc-sync.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "../granularity.md:1 → docs/granularity.md が存在しません" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunRelativeLinkResolves(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/granularity.md", "# granularity\n")
	writeFile(t, root, "docs/checks/doc-sync.md", "参照: [granularity](../granularity.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"docs/checks/doc-sync.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("実在すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunRootRelativeLink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照: [top](/README.md) と [無い](/missing.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "/missing.md:1 → missing.md が存在しません" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunEscapesRepoRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照: [外](../../../etc/passwd.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "../../../etc/passwd.md:1 → はリポジトリの外を指しています" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunURLEncoding(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/a b.md", "# a b\n")
	writeFile(t, root, "README.md", "参照: [ab](docs/a%20b.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("%%20 をデコードして実在確認するはず, got %v", violations)
	}
}

func TestRunTitledLink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照: [text](missing.md \"タイトル\")\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("タイトル部を落として target だけ見るはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "missing.md:1 → missing.md が存在しません" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunAngleBracketTarget(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照: [text](<a missing.md>)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("<...> を剥がして target を見るはず, got %d: %v", len(violations), violations)
	}
}

func TestRunReferenceDefinition(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照: [text][ref]\n\n[ref]: missing.md\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("参照定義のリンク先も検証するはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "missing.md:3 → missing.md が存在しません" {
		t.Errorf("Files = %v", got)
	}
}

func TestRunExternalURLsAreSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", ""+
		"[a](https://example.com/foo)\n"+
		"[b](http://example.com/foo)\n"+
		"[c](mailto:foo@example.com)\n"+
		"[d](//example.com/foo)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("外部 URL は対象外のはず, got %v", violations)
	}
}

func TestRunIgnoreList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "[gen](docs/generated/missing.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, Ignore: []string{"docs/generated/missing.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ignore に載っていれば違反は出ないはず, got %v", violations)
	}
}

func TestRunLinksInsideCodeFenceAreSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "```md\n[text](missing.md)\n```\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("コードフェンス内のリンクは対象外のはず, got %v", violations)
	}
}

func TestRunDuplicateLinkTargetReportedOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "[a](missing.md)\n本文\n[b](missing.md)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
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
	root := t.TempDir()
	writeFile(t, root, "README.md", "# タイトル\n\n[text](#no-such-heading)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("check_anchors 既定 false ではアンカーを検証しないはず, got %v", violations)
	}
}

func TestRunCheckAnchorsEnabled(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# My Heading\n\n[ok](#my-heading) [ng](#no-such-heading)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, CheckAnchors: true})
	violations, err := c.Run(check.Context{Root: root})
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

func TestRunCheckAnchorsCrossFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# Getting Started\n")
	writeFile(t, root, "README.md", "[ok](docs/guide.md#getting-started) [ng](docs/guide.md#no-such)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}, CheckAnchors: true})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
}

func TestRunLinkSyntaxInsideInlineCodeIsSkipped(t *testing.T) {
	root := t.TempDir()
	// ドキュメントがリンク記法そのものを例示する場合（インラインコードスパンの中）、
	// 本物のリンクとして誤検知してはいけない。
	writeFile(t, root, "README.md", "この検査は `[text](target)` のような記法を拾います。\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("インラインコード内のリンク記法の例示は対象外のはず, got %v", violations)
	}
}

func TestRunImageLink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "![alt](missing.png)\n")

	c := mustNew(t, config.CheckConfig{Docs: []string{"README.md"}})
	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("画像リンクも対象のはず, got %d: %v", len(violations), violations)
	}
}
