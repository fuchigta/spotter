// Package config は spotter の設定ファイル（既定 .spotter.yml）を読み込む。
//
// Load は yaml.Decoder.KnownFields(true) でデコードするため、CheckConfig 以外の構造体
// （Config・TypeConfig・ExemptConfig・TypeDefault・DocSyncPair 等）にある typo・未知の
// キーはデコードの時点でエラーになる。CheckConfig だけは Options（yaml.v3 の inline map。
// command 型のオプションを集約し internal/schema で検証する）を持つため、そこに吸収される
// 未知キーは KnownFields では捕まらない。そのため checks.<key> 直下・DenyRule のような
// 複数 type で共用する構造体の要素については、YAML 上のキーの有無を別途見て検証する
// （validateCheckKeys、builtinTypeKeys、builtinNestedKeys）。
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath は設定ファイルの既定の場所。
const DefaultPath = ".spotter.yml"

// Config はリポジトリ直下の設定ファイル全体。
type Config struct {
	Checks map[string]CheckConfig `yaml:"checks"`
	// Types は組み込み type の default 上書き、または command 型（外部コマンド検査）の
	// 登録に使う。組み込み type は types に書かなくても checks から使える。
	Types map[string]TypeConfig `yaml:"types,omitempty"`
	// RequiredVersion は spotter バイナリの下限バージョン（例: "v0.3.0"）。
	// 手元のバイナリがこれを満たさない場合、spotter は検査を実行せずエラーにする
	// （internal/version.Satisfies を参照）。
	RequiredVersion string `yaml:"required_version,omitempty"`
}

// CheckConfig は 1 つの検査インスタンスの設定。type によって解釈するフィールドが変わる。
type CheckConfig struct {
	Type   string        `yaml:"type"`
	Exempt *ExemptConfig `yaml:"exempt,omitempty"`

	// doc-sync 用。
	Pairs   []DocSyncPair `yaml:"pairs,omitempty"`
	Exclude []string      `yaml:"exclude,omitempty"`

	// unwanted-files / diff-content 共用。
	MaxBytes int64      `yaml:"max_bytes,omitempty"`
	Deny     []DenyRule `yaml:"deny,omitempty"`

	// doc-paths / doc-links 共用。Docs は doublestar パターン（"**" 対応）の一覧で、
	// 省略時は "**/*.md"（".git" 配下を除くリポジトリ内の全ての Markdown ファイル）。
	// Ignore は無視する候補・リンク先の完全一致リスト。
	Docs   []string `yaml:"docs,omitempty"`
	Ignore []string `yaml:"ignore,omitempty"`
	// PathPrefixes は doc-paths 専用。パス候補と認識するディレクトリ接頭辞。必須で、
	// 省略すると起動時にエラーになります。
	PathPrefixes []string `yaml:"path_prefixes,omitempty"`

	// doc-links 用。アンカー（#見出し）まで検証するか。既定 false
	// （アンカー生成規則は処理系依存のため、誤検知を避けるためオプトインにする）。
	CheckAnchors bool `yaml:"check_anchors,omitempty"`

	// commit-subject 用。
	AllowedTypes []string `yaml:"allowed_types,omitempty"`

	// consistency 用。
	Sources []ConsistencySource `yaml:"sources,omitempty"`

	// commit-intent 用。
	Rules []CommitIntentRule `yaml:"rules,omitempty"`

	// companion-files 用。commit-intent の rules と役割が異なるため別キーにしている
	// （companion-files 側は「ファイルを触ったら相方が要る」というルールで、
	// commit-intent の「commit type ごとの差分の条件」とは形が違う）。
	Companions []CompanionRule `yaml:"companions,omitempty"`

	// diff-size 用。0 または省略で無効。exclude（doc-sync と共用、集計から除外する
	// doublestar パターンの一覧）は上の Exclude フィールドを使う。
	MaxFiles int `yaml:"max_files,omitempty"`
	MaxLines int `yaml:"max_lines,omitempty"`

	// Options は command 型（外部コマンド検査）向け。上記のどの組み込みフィールド名にも
	// 一致しない残りのキーがここに集まる（yaml.v3 の inline map）。types.<type>.schema
	// で検証してから検査コマンドに渡す。
	Options map[string]any `yaml:",inline"`
}

// ExemptConfig は checks.<key>.exempt。フィールド単位でシステム既定にフォールバックするため
// Enable はポインタで「未指定」を表現する。
type ExemptConfig struct {
	Enable  *bool  `yaml:"enable,omitempty"`
	Trailer string `yaml:"trailer,omitempty"`
}

// DocSyncPair は doc-sync の対応表 1 行分。
type DocSyncPair struct {
	Paths string `yaml:"paths"`
	Doc   string `yaml:"doc"`
	When  string `yaml:"when,omitempty"`
	// On は when を当てる対象を絞る（"added" | "removed"）。when 未指定で指定すると
	// 起動時エラーになる。
	On string `yaml:"on,omitempty"`
	// DocWhen は doc の差分に当てる正規表現（省略可）。指定すると、doc が変更されていても
	// この正規表現に一致しない限り doc 側の条件を満たしたとみなさない
	// （空白 1 文字の変更のような形だけの更新を捕まえるためのオプトイン）。
	DocWhen string `yaml:"doc_when,omitempty"`
}

// DenyRule は unwanted-files（ファイルの deny）と diff-content（行の deny）が共用する
// 拒否ルール 1 件分。どのフィールドを必須・使用可とするかは検査ごとに異なるため、
// 検証は各検査の New で行う（unwantedfiles.New が Pattern/On の指定をエラーにし、
// diffcontent.New が Pattern を必須にする、など）。
type DenyRule struct {
	// Paths は unwanted-files では対象ファイルの doublestar パターン（必須）、
	// diff-content では対象ファイルを絞り込む doublestar パターン（省略可、省略時は全ファイル）。
	Paths  string `yaml:"paths,omitempty"`
	Reason string `yaml:"reason,omitempty"`
	// Pattern は diff-content 専用。行に当てる正規表現。
	Pattern string `yaml:"pattern,omitempty"`
	// On は diff-content 専用。"added"（既定）または "removed"。
	On string `yaml:"on,omitempty"`
}

// CommitIntentRule は commit-intent の 1 ルール分。allow / require / deny_diff / deny は
// 少なくとも 1 つ必要（各検査の New で検証する）。
type CommitIntentRule struct {
	// Types はこのルールを適用する commit type の一覧（必須）。
	Types []string `yaml:"types"`
	// Scopes を指定すると、その scope のときだけこのルールを適用する（省略時は scope を問わない）。
	Scopes []string `yaml:"scopes,omitempty"`
	// Breaking を指定すると、コミットが Conventional Commits の破壊的変更
	// （subject の "!" または本文フッタの "BREAKING CHANGE:"/"BREAKING-CHANGE:"）かどうかで
	// このルールの適用を絞る。true なら破壊的変更のときだけ、false なら破壊的変更でない
	// ときだけ適用する。省略時（nil）は問わない。
	Breaking *bool `yaml:"breaking,omitempty"`
	// Allow は変更・削除ファイルが全ていずれかに一致するべき doublestar パターンの一覧。
	// 外れたファイルが違反になる。
	Allow []string `yaml:"allow,omitempty"`
	// Require は変更ファイルの少なくとも 1 つがいずれかに一致するべき doublestar パターンの一覧。
	Require []string `yaml:"require,omitempty"`
	// Deny は変更・削除ファイルのいずれか 1 つでも一致したら違反にする doublestar パターンの
	// 一覧。「この type ではこのパスを触ってはいけない」を表す（allow の逆）。
	Deny []string `yaml:"deny,omitempty"`
	// DenyDiff は差分に一致したら違反にする正規表現（doc-sync の when と同じく (?m) を
	// 自動付与して行単位でマッチさせる）。
	DenyDiff string `yaml:"deny_diff,omitempty"`
	// On は deny_diff の対象を追加行（added）・削除行（removed）に絞る（省略時は差分
	// テキスト全体に当てる従来どおりの挙動）。deny_diff を指定していないのに on だけ
	// 指定すると起動時エラーになる。
	On string `yaml:"on,omitempty"`
	// Reason は違反表示に出す説明。省略時は allow/require/deny_diff/deny の内容から組み立てる。
	Reason string `yaml:"reason,omitempty"`
}

// CompanionRule は companion-files の 1 ルール分。
type CompanionRule struct {
	// Paths は対象にするファイルの doublestar パターン（必須）。
	Paths string `yaml:"paths"`
	// Companion は相方ファイルのパスを組み立てるテンプレート（必須）。
	// {dir}/{name}/{ext}/{path} の 4 変数が使える。
	Companion string `yaml:"companion"`
	// Reason は違反表示に出す理由（必須）。
	Reason string `yaml:"reason"`
	// Exclude はこのルールから外す doublestar パターンの一覧（省略可）。
	Exclude []string `yaml:"exclude,omitempty"`
}

// TypeConfig は types.<name> の内容。組み込み type と同名なら「default の上書き」、
// それ以外の名前なら command を必須とする「新しい type の登録」のどちらかを表す
// （両方の意味を同時には持てない。config.Load で検証する）。
type TypeConfig struct {
	// Command が設定されていれば、この type は外部コマンドで実装される。
	Command string `yaml:"command,omitempty"`
	// Args は Command に続けて渡す固定引数（省略可）。実際の呼び出しは
	// "<Command> <Args...> --mode ... --message-file ..." の順になる。
	// 例: command: go, args: [run, ./cmd/my-check] なら `go run ./cmd/my-check --mode ...`。
	Args []string `yaml:"args,omitempty"`
	// Transport は checks 側の Options を検査コマンドへどう渡すか（file | args | env）。
	// 省略時は file。command が無い（組み込み type の default 上書き）場合は無意味。
	Transport string `yaml:"transport,omitempty"`
	// Schema は checks 側で渡せる Options の形（省略可）。command が無い場合は無意味。
	Schema *SchemaConfig `yaml:"schema,omitempty"`
	// Default はこの type のインスタンス全体に効く既定値。
	Default *TypeDefault `yaml:"default,omitempty"`
}

// TypeDefault は types.<name>.default。
type TypeDefault struct {
	// Granularity は command 型の起動粒度（squashed | per-commit | worktree）。checks 側
	// からは上書きできない。組み込み type の default 上書きでは無意味（組み込みは Go 側で固定）。
	Granularity string `yaml:"granularity,omitempty"`
	// Exempt は免除設定の既定値。checks.<key>.exempt がこれを上書きする。
	Exempt *ExemptConfig `yaml:"exempt,omitempty"`
}

// SchemaConfig は command 型検査に渡せる Options の形。simple と json-schema は
// どちらか一方だけを指定する（同時指定はエラー）。
type SchemaConfig struct {
	// Simple はフィールド名 → 型・必須かどうかの簡易記法。
	Simple map[string]FieldSpec `yaml:"simple,omitempty"`
	// JSONSchema はフル JSON Schema（type / pattern / enum などの制約を含む）。
	JSONSchema map[string]any `yaml:"json-schema,omitempty"`
}

// FieldSpec は SchemaConfig.Simple の 1 フィールド分。
type FieldSpec struct {
	// Type は "string" / "integer" / "number" / "boolean" / "array" のいずれか。
	Type string `yaml:"type"`
	// Items は Type が "array" のときの要素の型（スカラーのみ）。
	Items string `yaml:"items,omitempty"`
	// Required はこのフィールドを必須にするか。
	Required bool `yaml:"required,omitempty"`
}

// ConsistencySource は consistency 検査が 1 つのファイルから集合を抜き出す方法。
//
//   - Line にマッチした行だけを対象にする（省略時は全行）
//   - Until を指定すると、Line にマッチした行から Until にマッチする行まで（両端含む）を
//     1 ブロックとし、ブロック内の各行を対象にする（複数行に折り返した配列などを拾うため。
//     Line なしでの指定は起動時エラー）
//   - その行に Extract（キャプチャグループ 1 つ必須）を当て、一致した全てを集める
//   - Split を指定すると、キャプチャした文字列をさらにその区切り文字で分割する
//     （例: "feat|fix|perf" を 1 つずつの要素にする）
//   - Subset を指定すると、この source は「他の（Subset ではない）source の和集合に無い
//     要素を持ってはいけないが、要素が欠けていても良い」対象になる（省略時 false）。
//     Subset ではない source どうしは従来どおり完全一致が要求される
type ConsistencySource struct {
	File    string `yaml:"file"`
	Line    string `yaml:"line,omitempty"`
	Until   string `yaml:"until,omitempty"`
	Extract string `yaml:"extract"`
	Split   string `yaml:"split,omitempty"`
	Subset  bool   `yaml:"subset,omitempty"`
}

// 組み込み type の一覧と、範囲モードでの起動粒度（checks 側からは上書きできない）。
const (
	TypeDocSync        = "doc-sync"
	TypeUnwantedFiles  = "unwanted-files"
	TypeDocPaths       = "doc-paths"
	TypeCommitSubject  = "commit-subject"
	TypeConsistency    = "consistency"
	TypeDiffContent    = "diff-content"
	TypeCommitIntent   = "commit-intent"
	TypeCompanionFiles = "companion-files"
	TypeDocLinks       = "doc-links"
	TypeDiffSize       = "diff-size"
)

// builtinTypes は組み込み type の一覧。
var builtinTypes = map[string]bool{
	TypeDocSync:        true,
	TypeUnwantedFiles:  true,
	TypeDocPaths:       true,
	TypeCommitSubject:  true,
	TypeConsistency:    true,
	TypeDiffContent:    true,
	TypeCommitIntent:   true,
	TypeCompanionFiles: true,
	TypeDocLinks:       true,
	TypeDiffSize:       true,
}

// IsBuiltinType は name が組み込み type かどうかを返す。
func IsBuiltinType(name string) bool {
	return builtinTypes[name]
}

// BuiltinTypeNames は組み込み type の一覧をソート済みで返す
// （`spotter checks --json` が静的なカタログと builtinTypes の対応漏れを検知するために使う）。
func BuiltinTypeNames() []string {
	names := make([]string, 0, len(builtinTypes))
	for name := range builtinTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// builtinTypeKeys は組み込み type ごとに checks.<key> 直下で使える type 固有のキー
// （共通キーの type / exempt を除く）の一覧。Load がここに無いキーをエラーにするための
// 正の情報源で、二重管理を避けるため `spotter checks --json` の静的カタログ
// （internal/cli/checks.go の checkCatalog）はここから取れる BuiltinTypeKeys と
// 一致することを internal/cli/checks_test.go で検証する。
var builtinTypeKeys = map[string][]string{
	TypeDocSync:        {"pairs", "exclude"},
	TypeUnwantedFiles:  {"max_bytes", "deny"},
	TypeDocPaths:       {"docs", "ignore", "path_prefixes"},
	TypeCommitSubject:  {"allowed_types"},
	TypeConsistency:    {"sources"},
	TypeDiffContent:    {"deny"},
	TypeCommitIntent:   {"rules"},
	TypeCompanionFiles: {"companions"},
	TypeDocLinks:       {"docs", "ignore", "check_anchors"},
	TypeDiffSize:       {"max_files", "max_lines", "exclude"},
}

// commonCheckKeys は checks.<key> 直下で type を問わず使える共通キー。
var commonCheckKeys = []string{"type", "exempt"}

// BuiltinTypeKeys は checkType（組み込み type）で checks.<key> 直下に使える type 固有の
// キー（type / exempt を除く）をソート済みで返す。組み込み type でなければ nil を返す。
func BuiltinTypeKeys(checkType string) []string {
	keys, ok := builtinTypeKeys[checkType]
	if !ok {
		return nil
	}
	out := append([]string(nil), keys...)
	sort.Strings(out)
	return out
}

// allowedCheckKeySet は checkType の checks.<key> 直下で使えるキー（共通キー込み）の集合。
func allowedCheckKeySet(checkType string) map[string]bool {
	set := make(map[string]bool, len(commonCheckKeys)+len(builtinTypeKeys[checkType]))
	for _, k := range commonCheckKeys {
		set[k] = true
	}
	for _, k := range builtinTypeKeys[checkType] {
		set[k] = true
	}
	return set
}

// builtinNestedKeys は「要素がオブジェクトの配列」フィールドのうち、複数の組み込み type で
// 共用する構造体（＝ Go の構造体タグだけでは type ごとの有効なキーを表せないもの）について、
// type ごとに使えるキーの一覧を定義する。
//
// DocSyncPair・ConsistencySource・CommitIntentRule・CompanionRule はそれぞれ 1 つの
// 組み込み type からしか使われないため、cfg への Decode を yaml.Decoder.KnownFields(true)
// で行うようにしたことで、それらの要素の未知キーは Decode の時点で自動的にエラーになる
// （ここに重複して持つ必要が無い）。DenyRule だけは unwanted-files と diff-content の
// 両方から使われ、しかも構造体としては両方のキー（paths/reason/pattern/on）を正規に
// 持っているため、KnownFields では「diff-content 専用のキーを unwanted-files の deny に
// 書いた」を検知できない。そのため DenyRule の分だけ type ごとの有効なキーをここに残す。
var builtinNestedKeys = map[string]map[string][]string{
	TypeUnwantedFiles: {"deny": {"paths", "reason"}},
	TypeDiffContent:   {"deny": {"pattern", "reason", "on", "paths"}},
}

// BuiltinNestedKeys は checkType の配列フィールド field（例: "pairs"）の要素で使える
// キーをソート済みで返す。field が要素オブジェクトの配列でなければ nil を返す
// （`spotter checks --json` の静的カタログとの突き合わせに internal/cli/checks_test.go
// から使う）。
func BuiltinNestedKeys(checkType, field string) []string {
	keys, ok := builtinNestedKeys[checkType][field]
	if !ok {
		return nil
	}
	out := append([]string(nil), keys...)
	sort.Strings(out)
	return out
}

// Load は path から設定を読み込み、最低限の妥当性を検証する。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %s の読み込みに失敗しました: %w", path, err)
	}

	// KnownFields(true) で、CheckConfig 以外の構造体（Config 自体・TypeConfig・
	// ExemptConfig・TypeDefault・DocSyncPair 等）にある未知のキー（typo を含む）を
	// Decode の時点でエラーにする。CheckConfig だけは Options（yaml.v3 の inline map）を
	// 持つため、そこに吸収される未知キーはここではエラーにならない（checks.<key> 直下の
	// キー検証は validateCheckKeys が別途行う。下記コメント参照）。
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && err != io.EOF {
		// io.EOF は「ドキュメントが 1 つも無い」ケース（空ファイル・コメントのみ等）。
		// yaml.Unmarshal はこの場合エラーにせず cfg をゼロ値のまま返すため、それに合わせる。
		return nil, fmt.Errorf("config: %s の解析に失敗しました: %w", path, err)
	}

	for name, tc := range cfg.Types {
		if err := validateTypeConfig(name, tc); err != nil {
			return nil, fmt.Errorf("config: types.%s: %w", name, err)
		}
	}

	for key, cc := range cfg.Checks {
		if cc.Type == "" {
			return nil, fmt.Errorf("config: checks.%s に type がありません", key)
		}
		if IsBuiltinType(cc.Type) {
			continue
		}
		if tc, ok := cfg.Types[cc.Type]; !ok || tc.Command == "" {
			return nil, fmt.Errorf("config: checks.%s の type %q は未対応です（types.%s に command を登録してください）", key, cc.Type, cc.Type)
		}
	}

	if err := validateCheckKeys(data, cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validateCheckKeys は組み込み type の checks.<key> について、その type で有効なキー
// （共通キー + type 固有のキー）以外が書かれていないかを検証する。あわせて、DenyRule
// （unwanted-files / diff-content が共用する構造体）については、要素で使える type ごとの
// キーも検証する（builtinNestedKeys）。
//
// CheckConfig は全組み込み type 共用の構造体で、かつ yaml.v3 の inline map（Options）が
// 未知のキーをフィールド名の一致漏れとして黙って吸収してしまうため、Load の
// KnownFields(true) デコードでは checks.<key> 直下の未知キー・他 type 用のキーを
// 検知できない（構造体へのデコード結果がゼロ値かどうかでも「書かれていない」と
// 「ゼロ値を明示的に書いた」を区別できない）。そのため checks を map[string]yaml.Node
// として別途デコードし直し、YAML 上に実際に書かれているキー名の集合を見て判定する。
//
// command 型（types に登録した外部コマンド検査）はここでは検証しない。そちらのオプションは
// 従来どおり CheckConfig.Options に集約され、types.<type>.schema で検証される。
func validateCheckKeys(data []byte, cfg Config) error {
	var raw struct {
		Checks map[string]yaml.Node `yaml:"checks"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		// cfg への Unmarshal が既に成功しているため、通常はここに到達しない。
		return fmt.Errorf("config: checks の再解析に失敗しました: %w", err)
	}

	var msgs []string
	for key, node := range raw.Checks {
		cc, ok := cfg.Checks[key]
		if !ok || !IsBuiltinType(cc.Type) {
			continue
		}
		msgs = append(msgs, checkNodeKeys(key, cc.Type, node)...)
	}
	if len(msgs) == 0 {
		return nil
	}
	sort.Strings(msgs)
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

// checkNodeKeys は checks.<key> 1 件分の YAML マッピングノードを検証し、エラーメッセージの
// 一覧を返す（トップレベルのキー、および既知の配列フィールドの要素キー）。
func checkNodeKeys(key, checkType string, node yaml.Node) []string {
	if node.Kind != yaml.MappingNode {
		return nil
	}

	topAllowed := allowedCheckKeySet(checkType)
	nestedFields := builtinNestedKeys[checkType]

	var msgs []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i].Value
		v := node.Content[i+1]

		if !topAllowed[k] {
			msgs = append(msgs, fmt.Sprintf(
				"config: checks.%s: type %s では %s は使えません（使えるキー: %s）",
				key, checkType, k, strings.Join(BuiltinTypeKeys(checkType), ", "),
			))
			continue
		}

		nestedKeys, ok := nestedFields[k]
		if !ok || v.Kind != yaml.SequenceNode {
			continue
		}
		nestedAllowed := make(map[string]bool, len(nestedKeys))
		for _, nk := range nestedKeys {
			nestedAllowed[nk] = true
		}
		sortedNestedKeys := append([]string(nil), nestedKeys...)
		sort.Strings(sortedNestedKeys)
		for _, item := range v.Content {
			for _, bad := range unknownYAMLKeys(*item, nestedAllowed) {
				msgs = append(msgs, fmt.Sprintf(
					"config: checks.%s: type %s の %s[].%s は使えません（使えるキー: %s）",
					key, checkType, k, bad, strings.Join(sortedNestedKeys, ", "),
				))
			}
		}
	}
	return msgs
}

// unknownYAMLKeys は node（マッピングノードのはず）のキーのうち allowed に無いものを
// ソート済みで返す。マッピングでなければ nil（型不一致は cfg への Unmarshal 側で既に
// エラーになっているはずなので、ここでは無視する）。
func unknownYAMLKeys(node yaml.Node, allowed map[string]bool) []string {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	var bad []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i].Value
		if !allowed[k] {
			bad = append(bad, k)
		}
	}
	sort.Strings(bad)
	return bad
}

// validateTypeConfig は types.<name> 単体の妥当性（組み込みとの衝突、command 型の
// 必須項目）を検証する。checks 側の Options が schema を満たすかは、各検査インスタンスの
// 構築時（internal/check/command）に個別に検証する。
func validateTypeConfig(name string, tc TypeConfig) error {
	if IsBuiltinType(name) {
		if tc.Command != "" {
			return fmt.Errorf("組み込み type と同名です。command を指定して差し替えることはできません（衝突。typo でなければ別名にしてください）")
		}
		if tc.Schema != nil || tc.Transport != "" {
			return fmt.Errorf("組み込み type の default 上書きでは schema/transport は指定できません")
		}
		if tc.Default != nil && tc.Default.Granularity != "" {
			return fmt.Errorf("組み込み type の granularity は上書きできません")
		}
		return nil
	}

	if tc.Command == "" {
		return fmt.Errorf("command が必要です（組み込み type の default 上書きでなければ）")
	}
	for _, a := range tc.Args {
		if a == "" {
			return fmt.Errorf("args に空文字は指定できません")
		}
	}
	switch tc.Transport {
	case "", "file", "args", "env":
	default:
		return fmt.Errorf("transport %q は未対応です（file | args | env）", tc.Transport)
	}
	if tc.Schema != nil && len(tc.Schema.Simple) > 0 && len(tc.Schema.JSONSchema) > 0 {
		return fmt.Errorf("schema には simple と json-schema のどちらか一方だけを指定してください")
	}
	if tc.Default == nil || tc.Default.Granularity == "" {
		return fmt.Errorf("default.granularity が必要です（squashed | per-commit | worktree）")
	}
	switch tc.Default.Granularity {
	case "squashed", "per-commit", "worktree":
	default:
		return fmt.Errorf("default.granularity %q は未対応です（squashed | per-commit | worktree）", tc.Default.Granularity)
	}
	return nil
}

// ResolveExempt は checks.<key>.exempt を、types.<type>.default.exempt →
// システム既定の順にフォールバックさせる。トレーラ名の既定値は type ではなく
// checks のキーから生成する（同じ type を複数インスタンス化したときにトレーラ名が
// 衝突しないようにするため）。
func (cfg *Config) ResolveExempt(key string, cc CheckConfig) (enable bool, trailer string) {
	enable = DefaultExemptEnable(cc.Type)
	trailer = defaultTrailer(key)

	if tc, ok := cfg.Types[cc.Type]; ok && tc.Default != nil && tc.Default.Exempt != nil {
		if tc.Default.Exempt.Enable != nil {
			enable = *tc.Default.Exempt.Enable
		}
		if tc.Default.Exempt.Trailer != "" {
			trailer = tc.Default.Exempt.Trailer
		}
	}

	if cc.Exempt != nil {
		if cc.Exempt.Enable != nil {
			enable = *cc.Exempt.Enable
		}
		if cc.Exempt.Trailer != "" {
			trailer = cc.Exempt.Trailer
		}
	}

	return enable, trailer
}

// DefaultExemptEnable は type ごとの免除の既定値。commit-subject はメッセージの体裁
// そのものを検証する検査なので、既定で免除を不可にする。
func DefaultExemptEnable(checkType string) bool {
	return checkType != TypeCommitSubject
}

// defaultTrailer は "doc-sync-frontend" のようなキーを "Doc-Sync-Frontend" に変換する。
func defaultTrailer(key string) string {
	parts := strings.Split(key, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "-")
}
