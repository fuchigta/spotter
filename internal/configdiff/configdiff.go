// Package configdiff は .spotter.yml の 2 つのスナップショットを比較し、検査が
// 比較元より弱くなった変更（緩和）を判定する。git にも CLI にも依存しない純粋関数だけを
// 提供し、比較元・終点をどう用意するか（staged の HEAD/インデックス、range の比較元・
// 終点）は呼び出し側が決める。
package configdiff

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fuchigta/spotter/internal/config"
	"gopkg.in/yaml.v3"
)

// Kind は緩和の種類。
type Kind string

const (
	// KindCheckRemoved は checks.<key> が消え、同じか厳しい別キーも無いことを表す。
	KindCheckRemoved Kind = "check-removed"
	// KindTypeChanged は検査の type を変更したことを表す。
	KindTypeChanged Kind = "type-changed"
	// KindExemptEnabled は解決後の免除可否が false から true になったことを表す。
	KindExemptEnabled Kind = "exempt-enabled"
	// KindLimitRaised は max_* の上限を上げた・0 にした・消したことを表す。
	KindLimitRaised Kind = "limit-raised"
	// KindRuleRemoved はルール配列の要素が消えたことを表す（中身の変更も削除＋追加として扱う）。
	KindRuleRemoved Kind = "rule-removed"
	// KindAllowAdded は exclude / ignore / allowed_types の要素が増えたことを表す。
	KindAllowAdded Kind = "allow-added"
	// KindTargetRemoved は docs / path_prefixes の要素が減ったことを表す。
	KindTargetRemoved Kind = "target-removed"
	// KindTurnedOff は check_anchors が true から false・省略になったことを表す。
	KindTurnedOff Kind = "turned-off"
	// KindVersionLowered は required_version を下げた・消したことを表す。
	KindVersionLowered Kind = "version-lowered"
	// KindChanged は意味を判定できない項目の変更を表す。
	KindChanged Kind = "changed"
	// KindUnparsable は比較元・終点を YAML として解析できないことを表す。
	KindUnparsable Kind = "unparsable"
)

// Loosening は 1 件の緩和。
type Loosening struct {
	Kind   Kind
	Path   string
	Before string
	After  string
}

// Target は check.ScopedExemptable のスコープ付き免除の対象（Violation.Target）を返す。
// 対象の単位は検査キー単位（checks.<key> は中の全ての緩和をまとめて 1 つの対象にする）。
// KindUnparsable と、checks/types/required_version 以外のトップレベルキーの変更は
// 個々の checks.<key> を突き合わせられない設定ファイル全体の緩和なので、configPath
// （設定ファイルのパス）を対象にする。
func (l Loosening) Target(configPath string) string {
	switch {
	case l.Kind == KindUnparsable:
		return configPath
	case strings.HasPrefix(l.Path, "checks."):
		key, _, _ := strings.Cut(strings.TrimPrefix(l.Path, "checks."), ".")
		return "checks." + key
	case strings.HasPrefix(l.Path, "types."):
		name, _, _ := strings.Cut(strings.TrimPrefix(l.Path, "types."), ".")
		return "types." + name
	case l.Path == "required_version":
		return "required_version"
	default:
		return configPath
	}
}

// Snapshot は 1 つの .spotter.yml を汎用的な木として読み込んだもの。config.Load と
// 違い、未知の type や未知のキーでも失敗しない（比較元・終点のどちらが今の
// config.Load の検証を通らない状態でも比較できるようにするため）。
type Snapshot struct {
	root map[string]any
}

// Parse は data を YAML として解析する。ドキュメントが無い・null のときは空の
// マッピングとして扱う（config.Load が io.EOF を空の Config として扱うのに合わせる）。
// トップレベルがマッピングでない場合はエラーを返す。
func Parse(data []byte) (*Snapshot, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var raw any
	switch err := dec.Decode(&raw); {
	case errors.Is(err, io.EOF):
		return &Snapshot{root: map[string]any{}}, nil
	case err != nil:
		return nil, fmt.Errorf("configdiff: YAML の解析に失敗しました: %w", err)
	case raw == nil:
		return &Snapshot{root: map[string]any{}}, nil
	}

	root, ok := normalizeValue(raw).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("configdiff: トップレベルがマッピングではありません")
	}
	return &Snapshot{root: root}, nil
}

// normalizeValue は yaml.v3 が interface{} へのデコードで生成する値を、比較しやすい
// 形に正規化する。map[interface{}]interface{} は map[string]any に、整数はどの幅でも
// int64 に揃える。companions[].companion はスカラー文字列でも配列でも書けるため
// （config.StringOrList）、要素を 1 つの配列に揃えないと同じ設定が別物に見えてしまう。
func normalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return normalizeMap(t)
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = val
		}
		return normalizeMap(m)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeValue(e)
		}
		return out
	case int:
		return int64(t)
	case uint64:
		// yaml.v3 は int の範囲を超える正の整数だけ uint64 にする（int が 64 ビットの
		// 環境が前提。他の整数幅は生成されない）。
		return int64(t)
	default:
		return v
	}
}

func normalizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = normalizeValue(v)
	}
	if v, ok := out["companion"].(string); ok {
		out["companion"] = []any{v}
	}
	return out
}

// CheckKeysOfType は checks.<key>.type が checkType であるキーをソート済みで返す。
func (s *Snapshot) CheckKeysOfType(checkType string) []string {
	var keys []string
	for k, v := range mapAt(s.root, "checks") {
		if cm, ok := v.(map[string]any); ok && stringAt(cm, "type") == checkType {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// CheckKeys は type を問わず checks.<key> の全てのキーをソート済みで返す。
func (s *Snapshot) CheckKeys() []string {
	return sortedKeys(mapAt(s.root, "checks"))
}

// TypeNames は types.<name> の全てのキーをソート済みで返す。
func (s *Snapshot) TypeNames() []string {
	return sortedKeys(mapAt(s.root, "types"))
}

// ExemptTargets は base・target のスナップショットから、check.ScopedExemptable の
// スコープ付き免除で指定できる対象の一覧を返す（重複を除きソート済み）。両スナップショットに
// ある checks.<key>・types.<t> の和に加え、required_version と configPath（設定ファイル
// 全体の緩和向け）を常に含める。base・target のどちらかが nil（解析できない場合）でも
// 動く。
func ExemptTargets(base, target *Snapshot, configPath string) []string {
	set := map[string]bool{"required_version": true, configPath: true}
	for _, snap := range [2]*Snapshot{base, target} {
		if snap == nil {
			continue
		}
		for _, k := range snap.CheckKeys() {
			set["checks."+k] = true
		}
		for _, n := range snap.TypeNames() {
			set["types."+n] = true
		}
	}

	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ResolveExempt は checks.<key> の免除設定を config.Config.ResolveExempt と同じ
// 解決順（システム既定 → types.<type>.default → checks.<key>）で解決する。config.Load が
// 拒否する設定（未知の type など）でも失敗しない。
func (s *Snapshot) ResolveExempt(key string) (enable bool, trailer string) {
	cc := s.checkConfigAt(key)
	cfg := s.exemptConfigStruct()
	return cfg.ResolveExempt(key, cc)
}

func (s *Snapshot) checkConfigAt(key string) config.CheckConfig {
	cm := mapAt(mapAt(s.root, "checks"), key)
	if cm == nil {
		return config.CheckConfig{}
	}
	return config.CheckConfig{
		Type:   stringAt(cm, "type"),
		Exempt: exemptConfigFrom(cm),
	}
}

// exemptConfigStruct は types.<name>.default.exempt が設定されている type だけを
// 集めた最小限の config.Config を組み立てる。config.Config.ResolveExempt が参照するのは
// この部分だけなので、他のフィールド（checks 本体など）は不要。
func (s *Snapshot) exemptConfigStruct() *config.Config {
	cfg := &config.Config{Types: map[string]config.TypeConfig{}}
	for name, v := range mapAt(s.root, "types") {
		tm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ex := exemptConfigFrom(mapAt(tm, "default"))
		if ex == nil {
			continue
		}
		cfg.Types[name] = config.TypeConfig{Default: &config.TypeDefault{Exempt: ex}}
	}
	return cfg
}

func exemptConfigFrom(m map[string]any) *config.ExemptConfig {
	em := mapAt(m, "exempt")
	if em == nil {
		return nil
	}
	return &config.ExemptConfig{
		Enable:  boolPtrAt(em, "enable"),
		Trailer: stringAt(em, "trailer"),
	}
}

// explicitTrailer は checks.<key>.exempt.trailer、無ければ
// types.<type>.default.exempt.trailer の明示値を返す（どちらにも無ければ空文字列）。
// ResolveExempt と違い、キー名から作る既定のトレーラ名は含めない。改名（rename）を
// 同じか厳しい別キーとして許すとき、既定のトレーラ名の違い（キー名に由来するだけ）を
// 変更として報告しないため。
func explicitTrailer(s *Snapshot, key string) string {
	cc := s.checkConfigAt(key)
	if cc.Exempt != nil && cc.Exempt.Trailer != "" {
		return cc.Exempt.Trailer
	}
	dm := mapAt(mapAt(mapAt(s.root, "types"), cc.Type), "default")
	if ex := exemptConfigFrom(dm); ex != nil && ex.Trailer != "" {
		return ex.Trailer
	}
	return ""
}

// Diff は base から target への変更のうち緩和にあたるものを、Path 順にソートして返す。
// エラーは返さない。比較元・終点のどちらかが YAML として解析できない場合は、
// KindUnparsable の緩和を 1 件だけ返す。
func Diff(base, target []byte) []Loosening {
	baseSnap, errBase := Parse(base)
	if errBase != nil {
		return []Loosening{unparsableLoosening("比較元", errBase)}
	}
	targetSnap, errTarget := Parse(target)
	if errTarget != nil {
		return []Loosening{unparsableLoosening("終点", errTarget)}
	}

	var out []Loosening
	out = append(out, diffRequiredVersion(baseSnap, targetSnap)...)
	out = append(out, diffTopUnknownKeys(baseSnap, targetSnap)...)
	out = append(out, diffChecks(baseSnap, targetSnap)...)
	out = append(out, diffTypes(baseSnap, targetSnap)...)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Before != out[j].Before {
			return out[i].Before < out[j].Before
		}
		return out[i].After < out[j].After
	})
	return out
}

func unparsableLoosening(side string, err error) Loosening {
	return Loosening{Kind: KindUnparsable, Path: side, After: err.Error()}
}

func chg(kind Kind, path, before, after string) Loosening {
	return Loosening{Kind: kind, Path: path, Before: before, After: after}
}

func stringAt(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func mapAt(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func boolPtrAt(m map[string]any, key string) *bool {
	if b, ok := m[key].(bool); ok {
		return &b
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func unionKeys(a, b map[string]any) []string {
	set := make(map[string]bool, len(a)+len(b))
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
