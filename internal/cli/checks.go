package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/config"
)

// FieldInfo は組み込み type 1 つが受け付ける設定キー 1 つ分の説明。
type FieldInfo struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// CheckTypeInfo は組み込み type 1 つの機械可読な説明。
type CheckTypeInfo struct {
	Type                 string      `json:"type"`
	Granularity          string      `json:"granularity"`
	ExemptDefaultEnabled bool        `json:"exempt_default_enabled"`
	Fields               []FieldInfo `json:"fields"`
}

// checksOutput は `spotter checks --json` の出力全体。
type checksOutput struct {
	Types []CheckTypeInfo `json:"types"`
}

// checkCatalog は組み込み type の静的なカタログ。設定を自動生成・修正する外部ツールが
// 「幻の設定キー」を書かないための正となる。type の追加漏れや granularity のズレは
// checks_test.go が internal/config.BuiltinTypeNames() / 各検査パッケージの
// Granularity() 実装と突き合わせて検知する。
var checkCatalog = []CheckTypeInfo{
	{
		Type:                 config.TypeDocSync,
		Granularity:          "squashed",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeDocSync),
		Fields: []FieldInfo{
			{Key: "pairs", Type: "array", Required: true, Description: "コードとドキュメントの対応表。各要素は paths（doublestar パターン）/ doc（ドキュメントのパス）/ when（変更行に当てる正規表現、任意）"},
			{Key: "exclude", Type: "array", Required: false, Description: "集計・比較から除外する doublestar パターンの一覧"},
		},
	},
	{
		Type:                 config.TypeUnwantedFiles,
		Granularity:          "per-commit",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeUnwantedFiles),
		Fields: []FieldInfo{
			{Key: "max_bytes", Type: "integer", Required: false, Description: "1 ファイルあたりの上限バイト数"},
			{Key: "deny", Type: "array", Required: false, Description: "禁止パターンの一覧。各要素は paths（doublestar パターン、必須）/ reason（必須）"},
		},
	},
	{
		Type:                 config.TypeDocPaths,
		Granularity:          "worktree",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeDocPaths),
		Fields: []FieldInfo{
			{Key: "docs", Type: "array", Required: false, Description: "対象ドキュメントの doublestar パターンの一覧（省略時 **/*.md）"},
			{Key: "ignore", Type: "array", Required: false, Description: "無視するパス候補の完全一致リスト"},
			{Key: "path_prefixes", Type: "array", Required: false, Description: "パス候補と認識するディレクトリ接頭辞（省略するとパス候補が 0 件になる）"},
		},
	},
	{
		Type:                 config.TypeCommitSubject,
		Granularity:          "per-commit",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeCommitSubject),
		Fields: []FieldInfo{
			{Key: "allowed_types", Type: "array", Required: true, Description: "Conventional Commits の type として許可する一覧（1 件以上）"},
		},
	},
	{
		Type:                 config.TypeConsistency,
		Granularity:          "worktree",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeConsistency),
		Fields: []FieldInfo{
			{Key: "sources", Type: "array", Required: true, Description: "集合を抜き出す元。2 件以上必要。各要素は file（必須）/ extract（必須、キャプチャグループ 1 つの正規表現）/ line（任意）/ split（任意）"},
		},
	},
	{
		Type:                 config.TypeDiffContent,
		Granularity:          "per-commit",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeDiffContent),
		Fields: []FieldInfo{
			{Key: "deny", Type: "array", Required: true, Description: "禁止パターンの一覧（1 件以上）。各要素は pattern（必須、正規表現）/ reason（必須）/ on（任意、added|removed。既定 added）/ paths（任意、対象を絞る doublestar パターン）"},
		},
	},
	{
		Type:                 config.TypeCommitIntent,
		Granularity:          "per-commit",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeCommitIntent),
		Fields: []FieldInfo{
			{Key: "rules", Type: "array", Required: true, Description: "1 件以上。各要素は types（必須）/ scopes（任意）/ allow（任意）/ require（任意）/ deny_diff（任意、正規表現）/ reason（任意）。allow/require/deny_diff のいずれか 1 つは必須"},
		},
	},
	{
		Type:                 config.TypeCompanionFiles,
		Granularity:          "squashed",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeCompanionFiles),
		Fields: []FieldInfo{
			{Key: "companions", Type: "array", Required: true, Description: "1 件以上。各要素は paths（必須）/ companion（必須、{dir}/{name}/{ext}/{path} が使えるテンプレート）/ reason（必須）/ exclude（任意）"},
		},
	},
	{
		Type:                 config.TypeDocLinks,
		Granularity:          "worktree",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeDocLinks),
		Fields: []FieldInfo{
			{Key: "docs", Type: "array", Required: false, Description: "対象ドキュメントの doublestar パターンの一覧（省略時 **/*.md）"},
			{Key: "ignore", Type: "array", Required: false, Description: "無視するリンク先の完全一致リスト"},
			{Key: "check_anchors", Type: "boolean", Required: false, Description: "リンク先の見出し（アンカー）まで検証するか（既定 false）"},
		},
	},
	{
		Type:                 config.TypeDiffSize,
		Granularity:          "per-commit",
		ExemptDefaultEnabled: config.DefaultExemptEnable(config.TypeDiffSize),
		Fields: []FieldInfo{
			{Key: "max_files", Type: "integer", Required: false, Description: "1 コミットで変更してよいファイル数の上限（max_lines と併せてどちらか一方は必須）"},
			{Key: "max_lines", Type: "integer", Required: false, Description: "1 コミットで変更してよい行数（追加+削除）の上限（max_files と併せてどちらか一方は必須）"},
			{Key: "exclude", Type: "array", Required: false, Description: "集計から除外する doublestar パターンの一覧（doc-sync と共用のキー）"},
		},
	},
}

func newChecksCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "checks",
		Short: "組み込み検査 type の一覧・起動粒度・設定キーを表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChecks(cmd.OutOrStdout(), jsonOutput)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false,
		"機械可読な JSON で出力する（スキルなど外部ツールからの利用を想定）")

	return cmd
}

func runChecks(stdout io.Writer, jsonOutput bool) error {
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(checksOutput{Types: checkCatalog})
	}

	for _, t := range checkCatalog {
		fmt.Fprintf(stdout, "%s（granularity=%s, exempt_default=%t）\n", t.Type, t.Granularity, t.ExemptDefaultEnabled)
		for _, f := range t.Fields {
			req := ""
			if f.Required {
				req = "必須"
			} else {
				req = "任意"
			}
			fmt.Fprintf(stdout, "  - %s (%s, %s): %s\n", f.Key, f.Type, req, f.Description)
		}
	}

	return nil
}
