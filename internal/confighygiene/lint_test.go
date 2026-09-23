package confighygiene_test

import (
	"os"
	"path/filepath"
	"strings"
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
					{Paths: "no/such/dir/*.go", Companion: []string{"{dir}/{name}_test.go"}, Reason: "x"},
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

func TestLintConsistencyDeadGlob(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/a.md", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"consistency": {
				Type: config.TypeConsistency,
				Sources: []config.ConsistencySource{
					{Glob: "docs/**/*.md"},
					{Glob: "pages/**/*.md"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if findField(findings, "consistency", "sources[0].glob") {
		t.Errorf("一致する glob が誤って検出されました: %+v", findings)
	}
	if !findField(findings, "consistency", "sources[1].glob") {
		t.Errorf("一致しない glob が検出されていません: %+v", findings)
	}
	if findField(findings, "consistency", "sources[0].file") || findField(findings, "consistency", "sources[1].file") {
		t.Errorf("glob の source で file が誤って検査されました: %+v", findings)
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

func TestLintCommitIntentAllowIsExcluded(t *testing.T) {
	// allow は「このルールが変更を許すパス」という将来のコミットへの制約で、
	// unwanted-files.deny と同じ理由で対象外（今のワークツリーに実在物が
	// 無いことは異常ではない）。
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"commit-intent": {
				Type: config.TypeCommitIntent,
				Rules: []config.CommitIntentRule{
					{Types: []string{"docs"}, Allow: []string{"no/such/dir/**"}},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("commit-intent.rules[].allow は対象外のはずが Finding が出ました: %+v", findings)
	}
}

func TestLintCommitIntentRequireAllDeadIsReportedAsOneFinding(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"commit-intent": {
				Type: config.TypeCommitIntent,
				Rules: []config.CommitIntentRule{
					{
						Types:   []string{"feat", "fix"},
						Require: []string{"no/such/dir/**", "also/no/such/**"},
					},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "commit-intent", "rules[0].require") {
		t.Errorf("全要素が死んでいる require が検出されていません: %+v", findings)
	}
	// 要素単位では報告しない（OR 集合なので個々の要素は独立して評価しない）。
	if findField(findings, "commit-intent", "rules[0].require[0]") {
		t.Errorf("require を要素単位で報告しています: %+v", findings)
	}
}

func TestLintCommitIntentRequirePartiallyAliveIsNotReported(t *testing.T) {
	// require は OR 集合。複数言語のレシピをまとめて書いている場合、まだ
	// 使っていない言語向けの要素があっても、どれか1つが生きていれば
	// ルール全体としては死んでいない。
	root := t.TempDir()
	writeFile(t, root, "internal/foo_test.go", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"commit-intent": {
				Type: config.TypeCommitIntent,
				Rules: []config.CommitIntentRule{
					{
						Types:   []string{"feat", "fix"},
						Require: []string{"**/*_test.go", "**/test_*.py", "**/*.test.ts"},
					},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("一部の要素が生きているのに Finding が出ました: %+v", findings)
	}
}

func TestLintDocPathsDeadDocsPattern(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-paths": {
				Type:         config.TypeDocPaths,
				Docs:         []string{"./README.md"}, // 先頭の "./" のせいで doublestar 上は一致しない
				PathPrefixes: []string{"internal"},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "doc-paths", "docs[0]") {
		t.Errorf("死んだ docs パターンが検出されていません: %+v", findings)
	}
}

func TestLintDocPathsDeadPathPrefix(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-paths": {
				Type:         config.TypeDocPaths,
				PathPrefixes: []string{"no-such-dir"},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "doc-paths", "path_prefixes[0]") {
		t.Errorf("存在しない path_prefixes が検出されていません: %+v", findings)
	}
}

func TestLintDocPathsLivePathPrefix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/foo.go", "")
	writeFile(t, root, "README.md", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-paths": {
				Type:         config.TypeDocPaths,
				Docs:         []string{"**/*.md"},
				PathPrefixes: []string{"internal"},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("実在するディレクトリなのに Finding が出ました: %+v", findings)
	}
}

func TestLintDocLinksDeadDocsPattern(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-links": {
				Type: config.TypeDocLinks,
				Docs: []string{"no/such/dir/**/*.md"},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "doc-links", "docs[0]") {
		t.Errorf("死んだ docs パターンが検出されていません: %+v", findings)
	}
}

func TestLintDocPathsDocsOmittedIsNotChecked(t *testing.T) {
	// docs 省略時は既定の "**/*.md" が使われる（常に存在しうる）ので、
	// 空配列そのものは検証対象にならない。
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-paths": {
				Type:         config.TypeDocPaths,
				PathPrefixes: []string{"internal"},
			},
		},
	}
	writeFile(t, root, "internal/foo.go", "")

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("docs 省略なのに Finding が出ました: %+v", findings)
	}
}

func TestLintPatternMatchesHandlesBraceExpansion(t *testing.T) {
	// ExistsOrGlob（旧実装）は "*" を含まない doublestar 構文（brace 展開など）を
	// 見逃していた。companion-files.companions[].paths で確認する
	// （doc-sync.pairs でも同じロジックを通る）。
	root := t.TempDir()
	writeFile(t, root, "Makefile", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"companion-files": {
				Type: config.TypeCompanionFiles,
				Companions: []config.CompanionRule{
					{Paths: "{Makefile,Dockerfile}", Companion: []string{"{name}.companion"}, Reason: "x"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("brace 展開で実在するファイルなのに Finding が出ました: %+v", findings)
	}
}

func TestLintPatternMatchesInvalidSyntaxReportsDistinctMessage(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"companion-files": {
				Type: config.TypeCompanionFiles,
				Companions: []config.CompanionRule{
					{Paths: "[unterminated", Companion: []string{"{name}.companion"}, Reason: "x"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 1 {
		t.Fatalf("Finding が1件のはず: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "不正な") {
		t.Errorf("不正な構文には専用のメッセージが出るはず: %+v", findings[0])
	}
}

func TestLintPatternMatchesIgnoresGitDir(t *testing.T) {
	// リポジトリの .git 配下に偶然一致するパターンを「生きている」と
	// 誤判定しない（internal/check/docutil.ResolveDocs と同じ扱い）。
	root := t.TempDir()
	writeFile(t, root, ".git/HEAD", "ref: refs/heads/main")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"companion-files": {
				Type: config.TypeCompanionFiles,
				Companions: []config.CompanionRule{
					{Paths: "**/HEAD", Companion: []string{"{name}.companion"}, Reason: "x"},
				},
			},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "companion-files", "companions[0].paths") {
		t.Errorf(".git 配下のみ一致するパターンは死んでいると判定すべき: %+v", findings)
	}
}

func TestLintFileRefRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/README.md", "")

	cfg := &config.Config{
		Checks: map[string]config.CheckConfig{
			"doc-sync": {
				Type: config.TypeDocSync,
				Pairs: []config.DocSyncPair{
					{Paths: "**/*.go", Doc: "docs"}, // ファイルではなくディレクトリ
				},
			},
		},
	}
	writeFile(t, root, "main.go", "")

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "doc-sync", "pairs[0].doc") {
		t.Errorf("doc がディレクトリのケースが検出されていません: %+v", findings)
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

func TestLintUnusedTypeBuiltinDefaultOverride(t *testing.T) {
	// types.<name> が組み込み type と同名なら「default の上書き」の意味になる
	// （command は持たない）。checks 側がその組み込み type 名を使っていれば
	// 「使われている」と判定されるべき。
	root := t.TempDir()

	cfg := &config.Config{
		Types: map[string]config.TypeConfig{
			config.TypeCommitSubject: {
				Default: &config.TypeDefault{Exempt: &config.ExemptConfig{Enable: boolPtr(true)}},
			},
		},
		Checks: map[string]config.CheckConfig{
			"commit-subject": {Type: config.TypeCommitSubject, AllowedTypes: []string{"feat"}},
		},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if findField(findings, "types", config.TypeCommitSubject) {
		t.Errorf("default 上書きが checks から使われているのに検出されました: %+v", findings)
	}
}

func TestLintUnusedTypeBuiltinDefaultOverrideUnused(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Types: map[string]config.TypeConfig{
			config.TypeCommitSubject: {
				Default: &config.TypeDefault{Exempt: &config.ExemptConfig{Enable: boolPtr(true)}},
			},
		},
		Checks: map[string]config.CheckConfig{},
	}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if !findField(findings, "types", config.TypeCommitSubject) {
		t.Errorf("どの checks からも使われていない default 上書きが検出されていません: %+v", findings)
	}
}

func TestLintUnusedTypeChecksKeyDiffersFromTypeName(t *testing.T) {
	// checks のキー名（例: doc-sync-frontend）は type 名（doc-sync）と別。
	// used の判定は cc.Type（type 名）で追跡するので、キー名が違っても
	// 正しく「使用中」と判定できるはず。
	root := t.TempDir()
	writeFile(t, root, "frontend/app.go", "")

	cfg := &config.Config{
		Types: map[string]config.TypeConfig{
			config.TypeDocSync: {
				Default: &config.TypeDefault{Exempt: &config.ExemptConfig{Enable: boolPtr(true)}},
			},
		},
		Checks: map[string]config.CheckConfig{
			"doc-sync-frontend": {
				Type:  config.TypeDocSync,
				Pairs: []config.DocSyncPair{{Paths: "frontend/*.go", Doc: "README.md"}},
			},
		},
	}
	writeFile(t, root, "README.md", "")

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if findField(findings, "types", config.TypeDocSync) {
		t.Errorf("checks のキー名が違うだけで使用中の type が検出されました: %+v", findings)
	}
}

func TestLintFindingsAreOrderedByCheckKeyThenTypesLast(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Types: map[string]config.TypeConfig{
			"z-unused": {Command: "bash", Default: &config.TypeDefault{Granularity: "per-commit"}},
		},
		Checks: map[string]config.CheckConfig{
			"b-check": {Type: config.TypeDocSync, Pairs: []config.DocSyncPair{{Paths: "no/such/*.go", Doc: "README.md"}}},
			"a-check": {Type: config.TypeDocSync, Pairs: []config.DocSyncPair{{Paths: "also/no/such/*.go", Doc: "README.md"}}},
		},
	}
	writeFile(t, root, "README.md", "")

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 3 {
		t.Fatalf("Finding は3件のはず: %+v", findings)
	}
	if findings[0].Check != "a-check" || findings[1].Check != "b-check" {
		t.Errorf("checks キーの昇順になっていません: %+v", findings)
	}
	if findings[2].Check != "types" {
		t.Errorf("types 由来の Finding が最後に来ていません: %+v", findings)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestLintEmptyConfigHasNoFindings(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}

	findings := confighygiene.Lint(cfg, os.DirFS(root))
	if len(findings) != 0 {
		t.Errorf("空の設定で Finding が出ました: %+v", findings)
	}
}
