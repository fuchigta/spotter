package confighygiene_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/confighygiene"
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

func findField(findings []confighygiene.Finding, check, field string) bool {
	for _, f := range findings {
		if f.Check == check && f.Field == field {
			return true
		}
	}
	return false
}

func TestLintDocSyncDeadPattern(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-sync": {
				Type: config.TypeDocSync,
				Pairs: []config.DocSyncPair{
					{Paths: "no/such/dir/*.go", Doc: "README.md"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "doc-sync", "pairs[0].paths") {
		t.Errorf("dead pattern が検出されていません: %+v", findings)
	}
}

func TestLintDocSyncLivePattern(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "")
	writeFile(t, root, "internal/cli/root.go", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-sync": {
				Type: config.TypeDocSync,
				Pairs: []config.DocSyncPair{
					{Paths: "internal/cli/*.go", Doc: "README.md"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("一致するパターンなのに Finding が出ました: %+v", findings)
	}
}

func TestLintDocSyncMissingDoc(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/cli/root.go", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-sync": {
				Type: config.TypeDocSync,
				Pairs: []config.DocSyncPair{
					{Paths: "internal/cli/*.go", Doc: "NO_SUCH.md"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "doc-sync", "pairs[0].doc") {
		t.Errorf("存在しない doc が検出されていません: %+v", findings)
	}
}

func TestLintUnwantedFilesDenyIsExcluded(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"unwanted-files": {
				Type: config.TypeUnwantedFiles,
				Deny: []config.DenyRule{
					{Paths: "**/*.does-not-exist-ever", Reason: "x"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	// unwanted-files.deny は禁止パターンで、現在のワークツリーに一致するファイルが
	// 無いことこそが正常な状態（あったら検査自体が違反として拾う）。
	// doc-sync のような「対応するはずの実在物」とは性質が逆なので、意図的に
	// Lint の対象外にしている。
	if len(findings) != 0 {
		t.Errorf("unwanted-files.deny は対象外のはずが Finding が出ました: %+v", findings)
	}
}

func TestLintCompanionFilesDeadPattern(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"companion-files": {
				Type: config.TypeCompanionFiles,
				Companions: []config.CompanionRule{
					{Paths: "no/such/dir/*.go", Companion: "{dir}/{name}_test.go", Reason: "x"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "companion-files", "companions[0].paths") {
		t.Errorf("dead pattern が検出されていません: %+v", findings)
	}
}

func TestLintConsistencyMissingFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"consistency": {
				Type: config.TypeConsistency,
				Sources: []config.ConsistencySource{
					{File: "a.txt", Extract: "(a)"},
					{File: "no-such-file.txt", Extract: "(b)"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if findField(findings, "consistency", "sources[0].file") {
		t.Errorf("実在する source が誤って検出されました: %+v", findings)
	}
	if !findField(findings, "consistency", "sources[1].file") {
		t.Errorf("存在しない source が検出されていません: %+v", findings)
	}
}

func TestLintDiffContentDenyIsExcluded(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"diff-content": {
				Type: config.TypeDiffContent,
				Deny: []config.DenyRule{
					// paths 指定あり・無し（全ファイル対象）のどちらも、
					// unwanted-files と同じ理由（禁止パターン）で対象外。
					{Pattern: "x", Reason: "y", Paths: "no/such/dir/*.go"},
					{Pattern: "z", Reason: "w"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("diff-content.deny は対象外のはずが Finding が出ました: %+v", findings)
	}
}

func TestLintCommitIntentDeadPattern(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"commit-intent": {
				Type: config.TypeCommitIntent,
				Rules: []config.CommitIntentRule{
					{
						Types:   []string{"docs"},
						Allow:   []string{"no/such/dir/**"},
						Require: []string{"also/no/such/**"},
					},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "commit-intent", "rules[0].allow[0]") {
		t.Errorf("dead allow パターンが検出されていません: %+v", findings)
	}
	if !findField(findings, "commit-intent", "rules[0].require[0]") {
		t.Errorf("dead require パターンが検出されていません: %+v", findings)
	}
}

func TestLintUnusedType(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Types: map[string]config.TypeConfig{
			"my-command": {
				Command: "bash",
				Default: &config.TypeDefault{Granularity: "per-commit"},
			},
		},
		Checks: map[string]config.CheckConfig{},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "types", "my-command") {
		t.Errorf("未参照の type が検出されていません: %+v", findings)
	}
}

func TestLintUsedTypeNotReported(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Types: map[string]config.TypeConfig{
			"my-command": {
				Command: "bash",
				Default: &config.TypeDefault{Granularity: "per-commit"},
			},
		},
		Checks: map[string]config.CheckConfig{
			"my-check": {Type: "my-command"},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if findField(findings, "types", "my-command") {
		t.Errorf("参照されている type が誤って検出されました: %+v", findings)
	}
}

func TestLintEmptyConfigHasNoFindings(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("空の設定で Finding が出ました: %+v", findings)
	}
}
