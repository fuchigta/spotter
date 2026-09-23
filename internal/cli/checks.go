package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/config"
)

// checksSchemaVersion は `spotter checks --json` の出力スキーマ自体のバージョン。
// フィールドの追加は互換的に行い、既存フィールドの意味変更・削除・型変更のときだけ上げる。
const checksSchemaVersion = 1

// FieldInfo は 1 つの設定キーの機械可読な説明。ItemType/Fields で入れ子（配列の要素が
// オブジェクトの場合の子フィールド）を表現できる。
//
// Required は「これを省略すると検査が意味をなさなくなる（違反が常に 0 件になる、または
// New() が実際にエラーを返す）」ことを表す。RequiredOneOf / MinItems は New() が実際に
// 検証する機械的な制約（複数キーのうちどれか 1 つが必須、配列の最小要素数）を表す。
// 前者は「省略すべきでない」という意味的な必須、後者は「省略すると起動時エラーになる」
// という構文的な必須で、両方が真とは限らない（例: doc-paths.path_prefixes は省略しても
// New() はエラーにしないが、候補が 0 件になり検査として意味をなさないため Required=true）。
type FieldInfo struct {
	Key           string      `json:"key"`
	Type          string      `json:"type"`                // string | integer | boolean | array
	ItemType      string      `json:"item_type,omitempty"` // Type == "array" のときの要素の型（string | object）
	Required      bool        `json:"required"`
	MinItems      int         `json:"min_items,omitempty"`
	RequiredOneOf [][]string  `json:"required_one_of,omitempty"`
	Description   string      `json:"description"`
	Fields        []FieldInfo `json:"fields,omitempty"` // ItemType == "object" のときの子フィールド定義
}

// CheckTypeInfo は組み込み type 1 つの機械可読な説明。
type CheckTypeInfo struct {
	Type        string `json:"type"`
	Granularity string `json:"granularity"`
	// ExemptSupported が false（worktree 粒度）の検査は免除トレーラの仕組み自体を持たない
	// （internal/cli/check.go の check.GranularityWorktree 分岐、docs/granularity.md 参照）。
	// checks.<key>.exempt を書いても黙って無視される。
	ExemptSupported bool `json:"exempt_supported"`
	// ExemptDefaultEnabled は ExemptSupported が true のときだけ意味を持つ
	// （ExemptSupported が false のときは常に nil で、JSON では省略される）。
	ExemptDefaultEnabled *bool       `json:"exempt_default_enabled,omitempty"`
	RequiredOneOf        [][]string  `json:"required_one_of,omitempty"`
	Fields               []FieldInfo `json:"fields"`
}

// ChecksOutput は `spotter checks --json` の出力全体。
type ChecksOutput struct {
	SchemaVersion  int    `json:"schema_version"`
	SpotterVersion string `json:"spotter_version"`
	// CommonFields は type を問わず checks.<key> 直下に置ける共通キー（type 自身と exempt）。
	CommonFields []FieldInfo     `json:"common_fields"`
	Types        []CheckTypeInfo `json:"types"`
}

func boolPtr(b bool) *bool { return &b }

func exemptDefault(checkType string) *bool {
	return boolPtr(config.DefaultExemptEnable(checkType))
}

// commonFields は checks.<key> 直下で type を問わず有効なキー。
var commonFields = []FieldInfo{
	{Key: "type", Type: "string", Required: true, Description: "組み込み type 名、または types に登録した command 型の名前"},
	{
		Key: "exempt", Type: "object", Required: false,
		Description: "免除設定。granularity=worktree の検査には効かない（コミットメッセージに依存しないため）",
		Fields: []FieldInfo{
			{Key: "enable", Type: "boolean", Required: false, Description: "免除トレーラを有効にするか（既定値は type ごとの exempt_default_enabled）"},
			{Key: "trailer", Type: "string", Required: false, Description: "免除トレーラ名（既定はキー名から自動生成。例: doc-sync → Doc-Sync）"},
		},
	},
}

// checkCatalog は組み込み type の静的なカタログ。設定を自動生成・修正する外部ツールが
// 「幻の設定キー」を書かないための正となる。type の追加漏れ・granularity のズレ・設定キー名の
// タイポは checks_test.go が internal/config.BuiltinTypeNames() / CheckConfig の yaml タグ /
// 各検査パッケージの Granularity() 実装と突き合わせて検知する（ただし Required・
// RequiredOneOf・MinItems・Description の正しさまでは検知できない。変更時は該当する
// internal/check/<type>/<type>.go の New() を見て手で合わせること）。
//
// types の並び順はこの宣言順であり、internal/config.BuiltinTypeNames()（アルファベット順）
// とは一致しない。
var checkCatalog = []CheckTypeInfo{
	{
		Type:                 config.TypeDocSync,
		Granularity:          "squashed",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeDocSync),
		Fields: []FieldInfo{
			{
				Key: "pairs", Type: "array", ItemType: "object", Required: true, MinItems: 1,
				Description: "コードとドキュメントの対応表",
				Fields: []FieldInfo{
					{Key: "paths", Type: "string", Required: true, Description: "対象コードの doublestar パターン"},
					{Key: "doc", Type: "string", Required: true, Description: "対応するドキュメントのパス"},
					{Key: "when", Type: "string", Required: false, Description: "変更行に当てる正規表現（省略時は paths のどの変更にも反応）"},
					{Key: "on", Type: "string", Required: false, Description: "added | removed。when 指定時のみ有効で、追加行/削除行それぞれの中身に when を当てる（省略時は差分全体に当てる）"},
					{Key: "doc_when", Type: "string", Required: false, Description: "doc の差分に当てる正規表現。指定すると、この正規表現に一致しない doc の変更は条件を満たしたとみなさない（形だけの更新を捕まえるオプトイン）"},
				},
			},
			{Key: "exclude", Type: "array", ItemType: "string", Required: false, Description: "集計・比較から除外する doublestar パターンの一覧"},
		},
	},
	{
		Type:                 config.TypeUnwantedFiles,
		Granularity:          "per-commit",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeUnwantedFiles),
		Fields: []FieldInfo{
			{Key: "max_bytes", Type: "integer", Required: false, Description: "1 ファイルあたりの上限バイト数（省略時は無制限）"},
			{
				Key: "deny", Type: "array", ItemType: "object", Required: false,
				Description: "禁止パターンの一覧（省略時は違反 0 件）",
				Fields: []FieldInfo{
					{Key: "paths", Type: "string", Required: true, Description: "対象ファイルの doublestar パターン"},
					{Key: "reason", Type: "string", Required: true, Description: "違反表示に出す理由"},
				},
			},
		},
	},
	{
		Type:            config.TypeDocPaths,
		Granularity:     "worktree",
		ExemptSupported: false,
		Fields: []FieldInfo{
			{Key: "docs", Type: "array", ItemType: "string", Required: false, Description: "対象ドキュメントの doublestar パターンの一覧（省略時 **/*.md）"},
			{Key: "ignore", Type: "array", ItemType: "string", Required: false, Description: "無視するパス候補の完全一致リスト"},
			{Key: "path_prefixes", Type: "array", ItemType: "string", Required: true, Description: "パス候補と認識するディレクトリ接頭辞。省略すると検査が意味をなさない（候補が 0 件になり違反も常に 0 件）"},
		},
	},
	{
		Type:                 config.TypeCommitSubject,
		Granularity:          "per-commit",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeCommitSubject),
		Fields: []FieldInfo{
			{Key: "allowed_types", Type: "array", ItemType: "string", Required: true, MinItems: 1, Description: "Conventional Commits の type として許可する一覧"},
		},
	},
	{
		Type:            config.TypeConsistency,
		Granularity:     "worktree",
		ExemptSupported: false,
		Fields: []FieldInfo{
			{
				Key: "sources", Type: "array", ItemType: "object", Required: true, MinItems: 2,
				Description: "集合を抜き出す元",
				Fields: []FieldInfo{
					{Key: "file", Type: "string", Required: true, Description: "対象ファイルのパス"},
					{Key: "line", Type: "string", Required: false, Description: "マッチさせる行を絞る正規表現（省略時は全行）"},
					{Key: "until", Type: "string", Required: false, Description: "指定すると line にマッチした行から until にマッチする行まで（両端含む）を 1 ブロックとして対象にする（line とセットでのみ指定可）"},
					{Key: "extract", Type: "string", Required: true, Description: "行に当てる正規表現（キャプチャグループをちょうど 1 つ含む必要がある）"},
					{Key: "split", Type: "string", Required: false, Description: "キャプチャした文字列をさらに分割する区切り文字"},
					{Key: "subset", Type: "boolean", Required: false, Description: "true にすると、他の（subset ではない）source の和集合に無い要素を持つことだけを違反にする（欠けは許容、省略時 false）"},
				},
			},
		},
	},
	{
		Type:                 config.TypeDiffContent,
		Granularity:          "per-commit",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeDiffContent),
		Fields: []FieldInfo{
			{
				Key: "deny", Type: "array", ItemType: "object", Required: true, MinItems: 1,
				Description: "禁止パターンの一覧",
				Fields: []FieldInfo{
					{Key: "pattern", Type: "string", Required: true, Description: "行に当てる正規表現"},
					{Key: "reason", Type: "string", Required: true, Description: "違反表示に出す理由"},
					{Key: "on", Type: "string", Required: false, Description: "added（既定）| removed"},
					{Key: "paths", Type: "string", Required: false, Description: "対象ファイルを絞り込む doublestar パターン（省略時は全ファイル）"},
				},
			},
		},
	},
	{
		Type:                 config.TypeCommitIntent,
		Granularity:          "per-commit",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeCommitIntent),
		Fields: []FieldInfo{
			{
				Key: "rules", Type: "array", ItemType: "object", Required: true, MinItems: 1,
				RequiredOneOf: [][]string{{"allow", "require", "deny_diff", "deny"}},
				Description:   "commit type ごとのルール",
				Fields: []FieldInfo{
					{Key: "types", Type: "array", ItemType: "string", Required: true, MinItems: 1, Description: "このルールを適用する commit type の一覧"},
					{Key: "scopes", Type: "array", ItemType: "string", Required: false, Description: "指定した scope のときだけ適用する（省略時は scope を問わない）"},
					{Key: "breaking", Type: "boolean", Required: false, Description: "true なら破壊的変更（subject の ! または本文フッタの BREAKING CHANGE）のときだけ、false なら破壊的変更でないときだけ適用する（省略時は問わない）"},
					{Key: "allow", Type: "array", ItemType: "string", Required: false, Description: "変更・削除ファイルが全て一致すべき doublestar パターンの一覧"},
					{Key: "require", Type: "array", ItemType: "string", Required: false, Description: "変更ファイルの少なくとも 1 つが一致すべき doublestar パターンの一覧"},
					{Key: "deny", Type: "array", ItemType: "string", Required: false, Description: "変更・削除ファイルのいずれか 1 つでも一致したら違反にする doublestar パターンの一覧"},
					{Key: "deny_diff", Type: "string", Required: false, Description: "差分に一致したら違反にする正規表現"},
					{Key: "on", Type: "string", Required: false, Description: "deny_diff の対象を絞る（added | removed。省略時は差分テキスト全体に当てる）。deny_diff 未指定なら起動時エラー"},
					{Key: "reason", Type: "string", Required: false, Description: "違反表示に出す理由（省略時は allow/require/deny_diff/deny の内容から組み立てる）"},
				},
			},
		},
	},
	{
		Type:                 config.TypeCompanionFiles,
		Granularity:          "squashed",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeCompanionFiles),
		Fields: []FieldInfo{
			{
				Key: "companions", Type: "array", ItemType: "object", Required: true, MinItems: 1,
				Description: "触ったファイルに要求する相方ファイルのルール",
				Fields: []FieldInfo{
					{Key: "paths", Type: "string", Required: true, Description: "対象ファイルの doublestar パターン"},
					{Key: "companion", Type: "array", ItemType: "string", Required: true, MinItems: 1, Description: "相方ファイルの候補パスを組み立てるテンプレート（{dir}/{name}/{stem}/{ext}/{path} が使える）。文字列 1 つでも、複数候補の配列（いずれか 1 つが存在すれば満たす）でもよい"},
					{Key: "reason", Type: "string", Required: true, Description: "違反表示に出す理由"},
					{Key: "exclude", Type: "array", ItemType: "string", Required: false, Description: "このルールから外す doublestar パターンの一覧"},
				},
			},
		},
	},
	{
		Type:            config.TypeDocLinks,
		Granularity:     "worktree",
		ExemptSupported: false,
		Fields: []FieldInfo{
			{Key: "docs", Type: "array", ItemType: "string", Required: false, Description: "対象ドキュメントの doublestar パターンの一覧（省略時 **/*.md）"},
			{Key: "ignore", Type: "array", ItemType: "string", Required: false, Description: "無視するリンク先の完全一致リスト"},
			{Key: "check_anchors", Type: "boolean", Required: false, Description: "リンク先の見出し（アンカー）まで検証するか（既定 false）"},
		},
	},
	{
		Type:                 config.TypeDiffSize,
		Granularity:          "per-commit",
		ExemptSupported:      true,
		ExemptDefaultEnabled: exemptDefault(config.TypeDiffSize),
		RequiredOneOf:        [][]string{{"max_files", "max_lines"}},
		Fields: []FieldInfo{
			{Key: "max_files", Type: "integer", Required: false, Description: "1 コミットで変更してよいファイル数の上限"},
			{Key: "max_lines", Type: "integer", Required: false, Description: "1 コミットで変更してよい行数（追加+削除）の上限"},
			{Key: "exclude", Type: "array", ItemType: "string", Required: false, Description: "集計から除外する doublestar パターンの一覧（doc-sync と共用のキー）"},
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
		out := ChecksOutput{
			SchemaVersion:  checksSchemaVersion,
			SpotterVersion: buildVersion,
			CommonFields:   commonFields,
			Types:          checkCatalog,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Fprintf(stdout, "spotter %s（checks スキーマ v%d）\n\n", buildVersion, checksSchemaVersion)

	fmt.Fprintln(stdout, "共通キー（checks.<key> 直下）:")
	for _, f := range commonFields {
		printFieldLine(stdout, "  ", f)
	}
	fmt.Fprintln(stdout)

	for _, t := range checkCatalog {
		exemptInfo := "exempt=非対応（worktree）"
		if t.ExemptSupported {
			exemptInfo = fmt.Sprintf("exempt_default=%t", *t.ExemptDefaultEnabled)
		}
		fmt.Fprintf(stdout, "%s（granularity=%s, %s）\n", t.Type, t.Granularity, exemptInfo)
		for _, f := range t.Fields {
			printFieldLine(stdout, "  ", f)
		}
	}

	return nil
}

func printFieldLine(w io.Writer, indent string, f FieldInfo) {
	req := "任意"
	if f.Required {
		req = "必須"
	}
	typ := f.Type
	if f.ItemType != "" {
		typ = fmt.Sprintf("%s<%s>", f.Type, f.ItemType)
	}
	fmt.Fprintf(w, "%s- %s (%s, %s): %s\n", indent, f.Key, typ, req, f.Description)
	for _, child := range f.Fields {
		printFieldLine(w, indent+"    ", child)
	}
}
