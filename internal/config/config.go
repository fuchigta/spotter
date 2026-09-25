// Package config は spotter の設定ファイル（既定 .spotter.yml）を読み込む。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultPath = ".spotter.yml"

// Config はリポジトリ直下の設定ファイル全体。
type Config struct {
	Checks map[string]CheckConfig `yaml:"checks"`
	// Types は組み込み type の default 上書き、または command 型（外部コマンド検査）の
	// 登録に使う。組み込み type は types に書かなくても checks から使える。
	Types map[string]TypeConfig `yaml:"types,omitempty"`
	// RequiredVersion は spotter バイナリの下限バージョン（例: "v0.3.0"）。満たさなければ
	// 検査を実行せずエラーにする（internal/version.Satisfies）。
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

	// doc-paths / doc-links 共用。
	Docs   []string `yaml:"docs,omitempty"`
	Ignore []string `yaml:"ignore,omitempty"`
	// PathPrefixes は doc-paths 専用。必須（省略すると起動時エラー）。
	PathPrefixes []string `yaml:"path_prefixes,omitempty"`

	// doc-links 用。既定 false（アンカー生成規則が処理系依存なため誤検知を避けオプトイン）。
	CheckAnchors bool `yaml:"check_anchors,omitempty"`

	// commit-subject 用。
	AllowedTypes []string `yaml:"allowed_types,omitempty"`

	// consistency 用。
	Sources []ConsistencySource `yaml:"sources,omitempty"`

	// commit-intent 用。
	Rules []CommitIntentRule `yaml:"rules,omitempty"`

	// companion-files 用。commit-intent の rules とはフィールドの形が違うため別キーにしている
	// （ファイルを触ったら相方が要るというルールで、commit type ごとの差分条件ではない）。
	Companions []CompanionRule `yaml:"companions,omitempty"`

	// diff-size 用。0 または省略で無効。exclude（doc-sync と共用、集計から除外する
	// doublestar パターンの一覧）は上の Exclude フィールドを使う。
	MaxFiles int `yaml:"max_files,omitempty"`
	MaxLines int `yaml:"max_lines,omitempty"`

	// Options は command 型向け。上記のどの組み込みフィールド名にも一致しない残りのキーが
	// ここに集まる（yaml.v3 の inline map）。types.<type>.schema で検証してから検査コマンドに渡す。
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
	// Exclude はこの pair だけに適用する除外パターン（doublestar）の一覧（省略可）。
	// CheckConfig.Exclude（全 pairs に共通の除外）と併用でき、どちらかに一致すれば
	// 対象から外れる。他の pairs には影響しない。
	Exclude []string `yaml:"exclude,omitempty"`
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
	// Net は diff-content 専用。on: removed のルールにだけ指定できる（それ以外で
	// 指定すると起動時エラー）。true にすると、ファイルごとに pattern に一致する
	// 削除行の数が同じ pattern に一致する追加行の数より多いときだけ違反にする
	// （改名のように削除と追加が対になっているケースを見逃さないための緩和）。
	Net bool `yaml:"net,omitempty"`
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
	// テキスト全体に当てる）。deny_diff を指定していないのに on だけ
	// 指定すると起動時エラーになる。
	On string `yaml:"on,omitempty"`
	// Reason は違反表示に出す説明。省略時は allow/require/deny_diff/deny の内容から組み立てる。
	Reason string `yaml:"reason,omitempty"`
}

// CompanionRule は companion-files の 1 ルール分。
type CompanionRule struct {
	// Paths は対象にするファイルの doublestar パターン（必須）。
	Paths string `yaml:"paths"`
	// Companion は相方ファイルの候補パスを組み立てるテンプレート（必須）。文字列 1 つでも、
	// 複数候補を並べた配列（いずれか 1 つが存在すれば満たす）でもよい。
	// {dir}/{name}/{stem}/{ext}/{path} の 5 変数が使える。
	Companion StringOrList `yaml:"companion"`
	// Reason は違反表示に出す理由（必須）。
	Reason string `yaml:"reason"`
	// Exclude はこのルールから外す doublestar パターンの一覧（省略可）。
	Exclude []string `yaml:"exclude,omitempty"`
}

// StringOrList は YAML 上でスカラー文字列 1 つとしても、文字列の配列としても書けるフィールド
// の値。companion-files の companion（相方の候補を複数書けるようにするため）で使う。
type StringOrList []string

// UnmarshalYAML はスカラーノードを要素 1 つの配列として、シーケンスノードをそのまま
// 配列として受け付ける。それ以外のノード種別（マッピングなど）はエラーにする。
func (s *StringOrList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var v string
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("文字列としてのデコードに失敗しました: %w", err)
		}
		*s = StringOrList{v}
		return nil
	case yaml.SequenceNode:
		var v []string
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("文字列配列としてのデコードに失敗しました: %w", err)
		}
		*s = StringOrList(v)
		return nil
	default:
		return fmt.Errorf("config: 文字列、または文字列の配列である必要があります")
	}
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

// ConsistencySource は consistency 検査が集合を抜き出す方法。File（1 ファイルを行単位で
// 抽出する）と Glob（ファイルパスの一覧をそのまま集合にする）はどちらか一方が必須で、
// 各フィールドがどちらの方式専用かはconsistency.New が検証する。詳細は docs/checks/consistency.md。
type ConsistencySource struct {
	File    string   `yaml:"file,omitempty"`
	Line    string   `yaml:"line,omitempty"`
	Until   string   `yaml:"until,omitempty"`
	Extract string   `yaml:"extract,omitempty"`
	Split   string   `yaml:"split,omitempty"`
	Subset  bool     `yaml:"subset,omitempty"`
	Glob    string   `yaml:"glob,omitempty"`
	Base    string   `yaml:"base,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

// 組み込み type の一覧（checks.<key>.type で使う識別子）。
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
	// TypeConfigGuard は .spotter.yml 自体の変更が検査を緩めていないかを見る型。他の
	// 組み込み type と違い checks.<key> 固有のフィールドを持たない（比較する対象は
	// 比較元・終点の .spotter.yml そのもののため）。
	TypeConfigGuard = "config-guard"
)

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
	TypeConfigGuard:    true,
}

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
	TypeConfigGuard:    {},
}

// commonCheckKeys は checks.<key> 直下で type を問わず使える共通キー。
var commonCheckKeys = []string{"type", "exempt"}

// BuiltinTypeKeys は checkType（組み込み type）で checks.<key> 直下に使える type 固有の
// キー（type / exempt を除く）をソート済みで返す。組み込み type でなければ nil を返す。
// config-guard のように固有キーを 1 つも持たない type でも、登録済みなら nil ではなく
// 空スライスを返す（`checks --json` のカタログ側と reflect.DeepEqual で突き合わせる
// checks_test.go が nil と空スライスを区別するため）。
func BuiltinTypeKeys(checkType string) []string {
	keys, ok := builtinTypeKeys[checkType]
	if !ok {
		return nil
	}
	out := make([]string, len(keys))
	copy(out, keys)
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

// builtinNestedKeys は複数の組み込み type で共用する構造体（DenyRule）について、type ごとに
// 使えるキーの一覧を定義する。DenyRule は unwanted-files と diff-content 両方のキーを正規に
// 持つため、KnownFields(true) だけでは他 type 専用キーの混入を検知できない。
var builtinNestedKeys = map[string]map[string][]string{
	TypeUnwantedFiles: {"deny": {"paths", "reason"}},
	TypeDiffContent:   {"deny": {"pattern", "reason", "on", "paths", "net"}},
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

	// CheckConfig の Options（inline map）に吸収される未知キーは KnownFields では検知できない
	// ため、checks.<key> 直下のキー検証は validateCheckKeys が別途行う。
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		// io.EOF は「ドキュメントが 1 つも無い」ケース（空ファイル・コメントのみ等）。
		// yaml.Unmarshal はこの場合エラーにせず cfg をゼロ値のまま返すため、それに合わせる。
		return nil, fmt.Errorf("config: %s の解析に失敗しました: %w", path, err)
	}

	for name, tc := range cfg.Types {
		if err := validateTypeConfig(name, tc); err != nil {
			return nil, fmt.Errorf("config: types.%s: %w", name, err)
		}
	}

	var configGuardKeys []string
	for key, cc := range cfg.Checks {
		if cc.Type == "" {
			return nil, fmt.Errorf("config: checks.%s に type がありません", key)
		}
		if IsBuiltinType(cc.Type) {
			if cc.Type == TypeConfigGuard {
				configGuardKeys = append(configGuardKeys, key)
			}
			continue
		}
		if tc, ok := cfg.Types[cc.Type]; !ok || tc.Command == "" {
			return nil, fmt.Errorf("config: checks.%s の type %q は未対応です（types.%s に command を登録してください）", key, cc.Type, cc.Type)
		}
	}
	if len(configGuardKeys) > 1 {
		sort.Strings(configGuardKeys)
		return nil, fmt.Errorf("config: checks に config-guard 型は 1 つまでしか置けません（%s）", strings.Join(configGuardKeys, ", "))
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
// CheckConfig.Options に集約され、types.<type>.schema で検証される。
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
