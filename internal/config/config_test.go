package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spotter.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("設定ファイルの書き込みに失敗しました: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeConfig(t, `
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: "internal/cli/*.go"
        doc: README.md
  unwanted-files:
    type: unwanted-files
    max_bytes: 1048576
    deny:
      - { paths: "*.jsonl", reason: "セッションログ" }
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Checks) != 2 {
		t.Fatalf("checks の数 = %d, want 2", len(cfg.Checks))
	}
	if cfg.Checks["doc-sync"].Type != TypeDocSync {
		t.Errorf("doc-sync.Type = %q", cfg.Checks["doc-sync"].Type)
	}
	if got := cfg.Checks["unwanted-files"].Deny[0].Reason; got != "セッションログ" {
		t.Errorf("deny[0].Reason = %q", got)
	}
}

func TestLoadDocPathsAndCommitSubject(t *testing.T) {
	path := writeConfig(t, `
checks:
  doc-paths:
    type: doc-paths
    path_prefixes: [internal]
    docs: ["README.md"]
    ignore: ["internal/source/codex"]
  commit-subject:
    type: commit-subject
    allowed_types: [feat, fix]
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got := cfg.Checks["doc-paths"].Docs; len(got) != 1 || got[0] != "README.md" {
		t.Errorf("doc-paths.Docs = %v", got)
	}
	if got := cfg.Checks["commit-subject"].AllowedTypes; len(got) != 2 {
		t.Errorf("commit-subject.AllowedTypes = %v", got)
	}
}

// TestLoadCompanionFilesAcceptsScalarOrListCompanion は companions[].companion が
// スカラー文字列でも配列でも読み込めることを確認する（StringOrList.UnmarshalYAML）。
func TestLoadCompanionFilesAcceptsScalarOrListCompanion(t *testing.T) {
	path := writeConfig(t, `
checks:
  companion-files:
    type: companion-files
    companions:
      - paths: "internal/**/*.go"
        companion: "{dir}/{name}_test.go"
        reason: "テストが無い"
      - paths: "src/**/*.go"
        companion: ["{dir}/{name}_test.go", "{dir}/testdata/{name}"]
        reason: "テストが無い"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	companions := cfg.Checks["companion-files"].Companions
	if len(companions) != 2 {
		t.Fatalf("companions の数 = %d, want 2", len(companions))
	}
	if got := []string(companions[0].Companion); len(got) != 1 || got[0] != "{dir}/{name}_test.go" {
		t.Errorf("スカラー指定の companion = %v", got)
	}
	if got := []string(companions[1].Companion); len(got) != 2 || got[0] != "{dir}/{name}_test.go" || got[1] != "{dir}/testdata/{name}" {
		t.Errorf("配列指定の companion = %v", got)
	}
}

func TestLoadCommandTypeAllowsWorktreeGranularity(t *testing.T) {
	path := writeConfig(t, `
types:
  my-check:
    command: ./scripts/my-check.sh
    default:
      granularity: worktree

checks:
  my-check:
    type: my-check
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got := cfg.Types["my-check"].Default.Granularity; got != "worktree" {
		t.Errorf("granularity = %q, want worktree", got)
	}
}

func TestLoadCommandTypeRejectsUnknownGranularity(t *testing.T) {
	path := writeConfig(t, `
types:
  my-check:
    command: ./scripts/my-check.sh
    default:
      granularity: bogus

checks:
  my-check:
    type: my-check
`)
	if _, err := Load(path); err == nil {
		t.Fatal("未対応の granularity で Load() がエラーになりませんでした")
	}
}

func TestLoadCommandTypeArgs(t *testing.T) {
	path := writeConfig(t, `
types:
  my-check:
    command: go
    args: [run, ./cmd/my-check]
    default:
      granularity: per-commit

checks:
  my-check:
    type: my-check
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	got := cfg.Types["my-check"].Args
	if len(got) != 2 || got[0] != "run" || got[1] != "./cmd/my-check" {
		t.Errorf("Args = %v", got)
	}
}

func TestLoadCommandTypeRejectsEmptyArg(t *testing.T) {
	path := writeConfig(t, `
types:
  my-check:
    command: go
    args: [run, ""]
    default:
      granularity: per-commit

checks:
  my-check:
    type: my-check
`)
	if _, err := Load(path); err == nil {
		t.Fatal("args に空文字があるのに Load() がエラーになりませんでした")
	}
}

// TestLoadRejectsInvalidCheckKeys は、組み込み type の checks.<key> に「その type で
// 有効なキー」以外が書かれた場合に Load がエラーにすることを確認する。未知キー（Options
// に吸収されうる）と「他の type 用のキー」（CheckConfig が全型共用のためデコード自体は
// 通る）の両方、およびゼロ値を明示的に書いたケース（構造体のゼロ値判定では捕まらない）を
// カバーする。
func TestLoadRejectsInvalidCheckKeys(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			"commit-subject に未知キー",
			`
checks:
  commit-subject:
    type: commit-subject
    allowed_types: [feat, fix]
    alow_scopes: [x]
`,
		},
		{
			"commit-subject に他 type 用のキー（pairs は doc-sync 用）",
			`
checks:
  commit-subject:
    type: commit-subject
    allowed_types: [feat, fix]
    pairs:
      - paths: "**/*.go"
        doc: README.md
`,
		},
		{
			"doc-paths に doc-links 専用の check_anchors をゼロ値で明示",
			`
checks:
  doc-paths:
    type: doc-paths
    path_prefixes: [internal]
    check_anchors: false
`,
		},
		{
			"unwanted-files に diff-content 専用の deny[].pattern",
			`
checks:
  unwanted-files:
    type: unwanted-files
    deny:
      - { paths: "*.log", reason: "ログ", pattern: "x" }
`,
		},
		{
			"unwanted-files に diff-content 専用の deny[].net",
			`
checks:
  unwanted-files:
    type: unwanted-files
    deny:
      - { paths: "*.log", reason: "ログ", net: true }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)
			if _, err := Load(path); err == nil {
				t.Fatal("無効なキーがあるのに Load() がエラーになりませんでした")
			}
		})
	}
}

// TestLoadValidCheckKeysPass は、組み込み type で有効なキーだけを使った設定が通ることを
// 確認する（TestLoadRejectsInvalidCheckKeys の裏取り。誤検知していないことの確認）。
func TestLoadValidCheckKeysPass(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			"doc-sync は pairs/exclude/exempt が使える",
			`
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: "**/*.go"
        doc: README.md
    exclude: ["**/*_test.go"]
    exempt:
      enable: false
      trailer: Custom
`,
		},
		{
			"doc-links は check_anchors をゼロ値以外で明示できる",
			`
checks:
  doc-links:
    type: doc-links
    check_anchors: true
`,
		},
		{
			"diff-content は deny[].pattern が使える",
			`
checks:
  diff-content:
    type: diff-content
    deny:
      - { pattern: "x", reason: "y" }
`,
		},
		{
			"diff-content は deny[].net が使える（on: removed 前提）",
			`
checks:
  diff-content:
    type: diff-content
    deny:
      - { pattern: "x", reason: "y", on: removed, net: true }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)
			if _, err := Load(path); err != nil {
				t.Fatalf("有効な設定なのに Load() がエラーになりました: %v", err)
			}
		})
	}
}

// TestLoadCommandTypeOptionsPassThrough は、command 型（types に登録した外部
// コマンド検査）の Options はキーの検証の対象外で、任意のキーが
// CheckConfig.Options に集約されることを確認する。
func TestLoadCommandTypeOptionsPassThrough(t *testing.T) {
	path := writeConfig(t, `
types:
  my-check:
    command: ./scripts/my-check.sh
    default:
      granularity: worktree

checks:
  my-check:
    type: my-check
    some_option: 1
    another_option: [a, b]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	opts := cfg.Checks["my-check"].Options
	if opts["some_option"] != 1 {
		t.Errorf("Options[\"some_option\"] = %v, want 1", opts["some_option"])
	}
	if _, ok := opts["another_option"]; !ok {
		t.Errorf("Options[\"another_option\"] がありません: %v", opts)
	}
}

// TestLoadRejectsTopLevelTypos は、CheckConfig の Options（inline map）を経由しない構造体
// （Config 自体・ExemptConfig・TypeConfig）の typo が、KnownFields(true) デコードの時点で
// エラーになることを確認する。
func TestLoadRejectsTopLevelTypos(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			"トップレベルの typo（required_versoin）",
			`
required_versoin: v9.0.0
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: "**/*.go"
        doc: README.md
`,
		},
		{
			"checks.<key>.exempt 内の typo（enabel）",
			`
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: "**/*.go"
        doc: README.md
    exempt: { enabel: true }
`,
		},
		{
			"types.<name> 内の typo（defualt）",
			`
types:
  commit-subject:
    defualt: {}
checks:
  commit-subject:
    type: commit-subject
    allowed_types: [feat]
`,
		},
		{
			"types.<name>.default 内の typo（command 型でも同様に捕まる）",
			`
types:
  my-check:
    command: bash
    default:
      granularity: per-commit
      exemtp: {}
checks:
  my-check:
    type: my-check
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)
			if _, err := Load(path); err == nil {
				t.Fatal("typo があるのに Load() がエラーになりませんでした")
			}
		})
	}
}

// TestLoadEmptyFileIsNotError は、空ファイル・コメントのみのファイルが
// KnownFields(true) でデコードしてもエラーにならない（io.EOF を特別扱いする）ことを確認する。
func TestLoadEmptyFileIsNotError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"完全に空", ""},
		{"コメントのみ", "# just a comment\n"},
		{"checks だけ（空）", "checks: {}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if len(cfg.Checks) != 0 {
				t.Errorf("Checks = %v, want 空", cfg.Checks)
			}
		})
	}
}

func TestLoadUnknownType(t *testing.T) {
	path := writeConfig(t, `
checks:
  my-check:
    type: my-custom-check
`)
	if _, err := Load(path); err == nil {
		t.Fatal("未対応の type で Load() がエラーになりませんでした")
	}
}

func TestLoadMissingType(t *testing.T) {
	path := writeConfig(t, `
checks:
  my-check: {}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("type 未指定で Load() がエラーになりませんでした")
	}
}

func TestResolveExempt(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		cfg         Config
		key         string
		cc          CheckConfig
		wantEnable  bool
		wantTrailer string
	}{
		{"既定はキーから生成", Config{}, "doc-sync", CheckConfig{}, true, "Doc-Sync"},
		{"ハイフン区切りも大文字化する", Config{}, "doc-sync-frontend", CheckConfig{}, true, "Doc-Sync-Frontend"},
		{"enable を上書きできる", Config{}, "doc-sync", CheckConfig{Exempt: &ExemptConfig{Enable: &falseVal}}, false, "Doc-Sync"},
		{"trailer を上書きできる", Config{}, "doc-sync", CheckConfig{Exempt: &ExemptConfig{Enable: &trueVal, Trailer: "Custom"}}, true, "Custom"},
		{"commit-subject は既定で免除不可", Config{}, "commit-subject", CheckConfig{Type: TypeCommitSubject}, false, "Commit-Subject"},
		{"commit-subject でも明示すれば免除できる", Config{}, "commit-subject", CheckConfig{Type: TypeCommitSubject, Exempt: &ExemptConfig{Enable: &trueVal}}, true, "Commit-Subject"},
		{
			"types.<type>.default.exempt が checks より弱い優先度",
			Config{Types: map[string]TypeConfig{
				"my-check": {Command: "./x", Default: &TypeDefault{Granularity: "squashed", Exempt: &ExemptConfig{Enable: &falseVal, Trailer: "MyCheck"}}},
			}},
			"my-check-a", CheckConfig{Type: "my-check"}, false, "MyCheck",
		},
		{
			"checks 側の指定は types.default より優先される",
			Config{Types: map[string]TypeConfig{
				"my-check": {Command: "./x", Default: &TypeDefault{Granularity: "squashed", Exempt: &ExemptConfig{Enable: &falseVal, Trailer: "MyCheck"}}},
			}},
			"my-check-a", CheckConfig{Type: "my-check", Exempt: &ExemptConfig{Enable: &trueVal}}, true, "MyCheck",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enable, trailer := tt.cfg.ResolveExempt(tt.key, tt.cc)
			if enable != tt.wantEnable || trailer != tt.wantTrailer {
				t.Errorf("ResolveExempt() = (%v, %q), want (%v, %q)", enable, trailer, tt.wantEnable, tt.wantTrailer)
			}
		})
	}
}
