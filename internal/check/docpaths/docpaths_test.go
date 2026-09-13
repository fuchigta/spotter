package docpaths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docpaths"
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

func TestRunMissingPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照先は `internal/cli/root.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
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
	root := t.TempDir()
	writeFile(t, root, "internal/cli/root.go", "package cli\n")
	writeFile(t, root, "README.md", "参照先は `internal/cli/root.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("実在すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunGlobPattern(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/cli/root.go", "package cli\n")
	writeFile(t, root, "README.md", "参照先は `internal/cli/*.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("グロブに 1 つでも一致すれば違反は出ないはず, got %v", violations)
	}
}

func TestRunGlobPatternSupportsDoublestar(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/cli/sub/deep.go", "package sub\n")
	writeFile(t, root, "README.md", "参照先は `internal/cli/**/*.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("候補パスの \"**\" もネストしたファイルに一致するはず, got %v", violations)
	}
}

func TestRunIgnoreList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "将来の拡張点は `internal/source/codex` です。\n")

	c, err := docpaths.New(config.CheckConfig{
		Docs:         []string{"README.md"},
		Ignore:       []string{"internal/source/codex"},
		PathPrefixes: []string{"internal"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("ignore に載っていれば違反は出ないはず, got %v", violations)
	}
}

func TestRunIgnoresNonPathBackticks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "コマンドは `spotter check` を使います。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("path_prefixes に一致しない候補は無視するはず, got %v", violations)
	}
}

func TestRunIgnoresBackticksInsideCodeFence(t *testing.T) {
	root := t.TempDir()
	// フェンス内のバッククォート（ここでは奇数個）を数えてしまうと、それ以降の
	// インラインスパンの対応がずれて誤抽出・抽出漏れの原因になる。
	writeFile(t, root, "README.md", ""+
		"# タイトル\n\n"+
		"```sh\n"+
		"echo `date`\n"+
		"```\n\n"+
		"参照先は `internal/missing.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
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

func TestRunInvalidGlobCandidateDoesNotAbortRun(t *testing.T) {
	root := t.TempDir()
	// "[" を含む地の文が候補として拾われても、不正な glob として検査全体を
	// 異常終了させてはいけない（「存在しない」として違反に倒す）。
	writeFile(t, root, "README.md", "参照先は `internal/cli/[abc*.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() は不正な glob 候補でもエラーを返さないはず: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("不正な glob 候補は違反として報告されるはず, got %d件: %v", len(violations), violations)
	}
}

func TestRunDefaultDocs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照先は `internal/missing.go` です。\n")
	writeFile(t, root, "docs/guide.md", "参照先は `cmd/missing.go` です。\n")
	writeFile(t, root, "docs/checks/deep.md", "参照先は `internal/deep-missing.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{PathPrefixes: []string{"internal", "cmd"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("既定では \"**/*.md\" を再帰的に見るはず, got %d件: %v", len(violations), violations)
	}
}

func TestRunDefaultDocsExcludesGitDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".git/COMMIT_EDITMSG.md", "参照先は `internal/missing.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{PathPrefixes: []string{"internal"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf(".git 配下は既定の対象から除くはず, got %v", violations)
	}
}

func TestRunDocsPatternSupportsDoublestar(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/b/c/guide.md", "参照先は `internal/missing.go` です。\n")

	c, err := docpaths.New(config.CheckConfig{
		Docs:         []string{"a/**/*.md"},
		PathPrefixes: []string{"internal"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("docs に指定した \"**\" パターンで深い階層のファイルも見つかるはず, got %d件: %v", len(violations), violations)
	}
}

func TestRunPathPrefixesUnsetMatchesNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照先は `internal/missing.go` です。\n")

	// path_prefixes 未設定なら候補は 1 つも見つからず、検査は実行されるが違反 0 件になる。
	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("path_prefixes 未指定なら候補が無いはず, got %v", violations)
	}
}

func TestRunPathPrefixesGitHubRequiresExplicitConfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照先は `.github/missing.yml` です。\n")

	// .github/.githooks も他の接頭辞と同じ 1 つの値であり、path_prefixes に含めない
	// 限り候補にならない。
	c, err := docpaths.New(config.CheckConfig{Docs: []string{"README.md"}, PathPrefixes: []string{"src"}})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf(".github/ は path_prefixes に含めない限り候補にならないはず, got %v", violations)
	}
}

func TestRunPathPrefixesConfigurable(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "参照先は `src/missing.ts` です。\n")

	c, err := docpaths.New(config.CheckConfig{
		Docs:         []string{"README.md"},
		PathPrefixes: []string{"src"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	violations, err := c.Run(check.Context{Root: root})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("path_prefixes に 'src' を指定すれば候補になるはず, got %d件: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "src/missing.ts" {
		t.Errorf("Files = %v", got)
	}
}

func TestGranularity(t *testing.T) {
	c, err := docpaths.New(config.CheckConfig{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.Granularity() != check.GranularityWorktree {
		t.Errorf("doc-paths の granularity は worktree 固定のはず, got %v", c.Granularity())
	}
}
