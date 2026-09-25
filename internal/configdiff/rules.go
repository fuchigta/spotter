package configdiff

import (
	"reflect"
	"sort"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/version"
)

// ruleKind は checks.<key> 直下のキー 1 つをどう比較するかの分類。
type ruleKind int

const (
	// ruleLimit は省略・0 を無限大とみなし、after > before なら KindLimitRaised にする
	// （max_bytes / max_files / max_lines）。
	ruleLimit ruleKind = iota
	// ruleArray は要素（reason を除きキーをソートした JSON）の出現回数が減ったら
	// KindRuleRemoved にする（pairs / deny / rules / companions / sources）。
	ruleArray
	// ruleAllowSet は要素が増えたら KindAllowAdded にする（exclude / ignore / allowed_types）。
	ruleAllowSet
	// ruleTargetSet は要素が減ったら KindTargetRemoved にする（docs / path_prefixes）。
	ruleTargetSet
	// ruleBoolTurnedOff は true から false・省略になったら KindTurnedOff にする（check_anchors）。
	ruleBoolTurnedOff
)

// checkFieldRules は config.CheckConfig の yaml タグ（type と exempt を除く、Options の
// inline map も除く）と、比較の種類の対応表。同じ Go フィールドは複数の組み込み type が
// 共用する（例: Exclude は doc-sync と diff-size の両方）ため、type ごとではなく
// フィールド名で 1 つに定義する。rules_test.go がこの表と config.CheckConfig の
// リフレクション結果の突き合わせを取る。
var checkFieldRules = map[string]ruleKind{
	"pairs":         ruleArray,
	"exclude":       ruleAllowSet,
	"max_bytes":     ruleLimit,
	"deny":          ruleArray,
	"docs":          ruleTargetSet,
	"ignore":        ruleAllowSet,
	"path_prefixes": ruleTargetSet,
	"check_anchors": ruleBoolTurnedOff,
	"allowed_types": ruleAllowSet,
	"sources":       ruleArray,
	"rules":         ruleArray,
	"companions":    ruleArray,
	"max_files":     ruleLimit,
	"max_lines":     ruleLimit,
}

// checkCommonFields は checks.<key> 直下で type を問わず特別扱いするキー
// （checkFieldRules には含めない）。
var checkCommonFields = map[string]bool{"type": true, "exempt": true}

// topLevelFields は config.Config の yaml タグのうち、Diff がトップレベルで特別扱いする
// もの（checks は diffChecks、types は diffTypes、required_version は
// diffRequiredVersion が見る）。
var topLevelFields = map[string]bool{"checks": true, "types": true, "required_version": true}

// typeConfigFields は config.TypeConfig の yaml タグ。全て diffTypeConfig が
// KindChanged として比較する（default だけ内側の granularity のみを見る）。
var typeConfigFields = map[string]bool{"command": true, "args": true, "transport": true, "schema": true, "default": true}

// typeDefaultFields は config.TypeDefault の yaml タグ。granularity は diffTypeConfig が、
// exempt は checks.k の行（exempt.enable / exempt.trailer）が見る。
var typeDefaultFields = map[string]bool{"granularity": true, "exempt": true}

// exemptConfigFields は config.ExemptConfig の yaml タグ。どちらも checks.k の行が見る
// （enable は ResolveExempt で解決した値、trailer は明示値）。
var exemptConfigFields = map[string]bool{"enable": true, "trailer": true}

// diffRequiredVersion は required_version の緩和（下げた・消した）を判定する。
func diffRequiredVersion(base, target *Snapshot) []Loosening {
	return compareRequiredVersion(stringAt(base.root, "required_version"), stringAt(target.root, "required_version"))
}

func compareRequiredVersion(base, target string) []Loosening {
	if base == target || base == "" {
		return nil
	}
	if target == "" {
		return []Loosening{chg(KindVersionLowered, "required_version", base, target)}
	}
	cmp, err := version.Compare(target, base)
	if err != nil {
		// 数値として比較できない値は意味を判定できないため、変更としてだけ報告する。
		return []Loosening{chg(KindChanged, "required_version", base, target)}
	}
	if cmp < 0 {
		return []Loosening{chg(KindVersionLowered, "required_version", base, target)}
	}
	return nil
}

// diffTopUnknownKeys は checks/types/required_version 以外のトップレベルキーの変更を見る。
func diffTopUnknownKeys(base, target *Snapshot) []Loosening {
	var out []Loosening
	for _, k := range unionKeys(base.root, target.root) {
		if topLevelFields[k] {
			continue
		}
		if !reflect.DeepEqual(base.root[k], target.root[k]) {
			out = append(out, chg(KindChanged, k, render(base.root[k]), render(target.root[k])))
		}
	}
	return out
}

// diffChecks は base の各 checks.<key> について、target の同じキー・別キー（改名・統合）が
// 同じか厳しいかを確かめ、どちらでもなければ差分を報告する。
func diffChecks(base, target *Snapshot) []Loosening {
	baseChecks := mapAt(base.root, "checks")
	targetChecks := mapAt(target.root, "checks")
	targetKeys := sortedKeys(targetChecks)

	var out []Loosening
	for _, k := range sortedKeys(baseChecks) {
		bc, _ := baseChecks[k].(map[string]any)

		if tc, ok := targetChecks[k].(map[string]any); ok && len(compareCheck(base, k, bc, target, k, tc)) == 0 {
			continue
		}
		if renamedCleanMatch(base, k, bc, target, targetKeys, targetChecks) {
			continue
		}

		tc, ok := targetChecks[k].(map[string]any)
		if !ok {
			out = append(out, Loosening{Kind: KindCheckRemoved, Path: "checks." + k})
			continue
		}
		out = append(out, compareCheck(base, k, bc, target, k, tc)...)
	}
	return out
}

// renamedCleanMatch は base の checks.k を、k 以外の target のキー（ソート順）のどれかと
// 比べて緩和が無いものが 1 つでもあるかを返す（改名・統合を同じか厳しいとして許す）。
func renamedCleanMatch(base *Snapshot, k string, bc map[string]any, target *Snapshot, targetKeys []string, targetChecks map[string]any) bool {
	for _, k2 := range targetKeys {
		if k2 == k {
			continue
		}
		tc2, ok := targetChecks[k2].(map[string]any)
		if !ok {
			continue
		}
		if len(compareCheck(base, k, bc, target, k2, tc2)) == 0 {
			return true
		}
	}
	return false
}

// compareCheck は base 側 baseKey の checks 設定 bc と、target 側 targetKey の checks 設定
// tc を比較する。baseKey と targetKey が異なる呼び出しは「同じか厳しいか」の判定にだけ
// 使い、報告する Loosening の Path は常に baseKey を使う。
func compareCheck(baseSnap *Snapshot, baseKey string, bc map[string]any, targetSnap *Snapshot, targetKey string, tc map[string]any) []Loosening {
	path := "checks." + baseKey
	var out []Loosening

	baseType := stringAt(bc, "type")
	targetType := stringAt(tc, "type")
	if baseType != targetType {
		out = append(out, chg(KindTypeChanged, path+".type", baseType, targetType))
	}

	be, _ := baseSnap.ResolveExempt(baseKey)
	te, _ := targetSnap.ResolveExempt(targetKey)
	if !be && te {
		out = append(out, chg(KindExemptEnabled, path+".exempt.enable", "false", "true"))
	}
	if bt, tt := explicitTrailer(baseSnap, baseKey), explicitTrailer(targetSnap, targetKey); bt != tt {
		out = append(out, chg(KindChanged, path+".exempt.trailer", bt, tt))
	}

	if config.IsBuiltinType(baseType) {
		out = append(out, compareBuiltinFields(path, bc, tc)...)
	} else {
		out = append(out, compareOpaqueOptions(path, bc, tc)...)
	}
	return out
}

// compareBuiltinFields は組み込み type の checks.<key> 直下のキーを比較する。
// checkFieldRules にあるキーは意味を判定して比較し、それ以外（未知のキー）は
// 等しいかどうかだけを見る。
func compareBuiltinFields(path string, bc, tc map[string]any) []Loosening {
	var out []Loosening
	for _, k := range unionKeys(bc, tc) {
		if checkCommonFields[k] {
			continue
		}
		kind, known := checkFieldRules[k]
		if !known {
			if !reflect.DeepEqual(bc[k], tc[k]) {
				out = append(out, chg(KindChanged, path+"."+k, render(bc[k]), render(tc[k])))
			}
			continue
		}
		out = append(out, compareKnownField(path+"."+k, kind, bc[k], tc[k])...)
	}
	return out
}

// compareOpaqueOptions は command 型の checks.<key> を比較する。type・exempt 以外の
// 全キーを、意味を判定せず等しいかどうかだけで比較する
// （command・args・schema・オプションを変えたら理由が要る、という決定に基づく）。
func compareOpaqueOptions(path string, bc, tc map[string]any) []Loosening {
	var out []Loosening
	for _, k := range unionKeys(bc, tc) {
		if checkCommonFields[k] {
			continue
		}
		if !reflect.DeepEqual(bc[k], tc[k]) {
			out = append(out, chg(KindChanged, path+"."+k, render(bc[k]), render(tc[k])))
		}
	}
	return out
}

func compareKnownField(path string, kind ruleKind, bv, tv any) []Loosening {
	switch kind {
	case ruleLimit:
		return compareLimit(path, bv, tv)
	case ruleArray:
		return compareRuleArray(path, bv, tv)
	case ruleAllowSet:
		return compareAllowSet(path, bv, tv)
	case ruleTargetSet:
		return compareTargetSet(path, bv, tv)
	case ruleBoolTurnedOff:
		return compareBoolTurnedOff(path, bv, tv)
	default:
		return nil
	}
}

// compareLimit は省略・0 を無限大として扱い、after が before より大きければ
// KindLimitRaised にする。
func compareLimit(path string, bv, tv any) []Loosening {
	bn, bInf := effectiveLimit(bv)
	tn, tInf := effectiveLimit(tv)

	var raised bool
	switch {
	case bInf:
		raised = false
	case tInf:
		raised = true
	default:
		raised = tn > bn
	}
	if !raised {
		return nil
	}
	return []Loosening{chg(KindLimitRaised, path, render(bv), render(tv))}
}

func effectiveLimit(v any) (n int64, isInf bool) {
	n, ok := toInt64(v)
	if !ok || n == 0 {
		return 0, true
	}
	return n, false
}

// toInt64 は正規化後の Snapshot に現れる数値表現（Parse が normalizeValue で int64 に
// 揃える整数と、YAML の小数リテラルがそのまま残る float64）を受け付ける。
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// compareRuleArray はルール配列の要素を reason を除いて正規化した JSON で数え、base より
// target で出現回数が減った要素を KindRuleRemoved として報告する（中身の変更は
// 削除＋追加として扱うため、追加側は報告しない）。
func compareRuleArray(path string, bv, tv any) []Loosening {
	bCounts := counts(canonicalElements(bv))
	tCounts := counts(canonicalElements(tv))

	var removed []string
	for k, c := range bCounts {
		if tCounts[k] < c {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)

	out := make([]Loosening, 0, len(removed))
	for _, k := range removed {
		out = append(out, Loosening{Kind: KindRuleRemoved, Path: path, Before: k})
	}
	return out
}

func canonicalElements(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = canonicalElement(e)
	}
	return out
}

// canonicalElement は要素から reason を除いて JSON にする。reason は違反の説明であって
// 検査の強さそのものではないため、比較から外す。
func canonicalElement(e any) string {
	m, ok := e.(map[string]any)
	if !ok {
		return render(e)
	}
	stripped := make(map[string]any, len(m))
	for k, v := range m {
		if k == "reason" {
			continue
		}
		stripped[k] = v
	}
	return render(stripped)
}

func counts(list []string) map[string]int {
	m := make(map[string]int, len(list))
	for _, s := range list {
		m[s]++
	}
	return m
}

// compareAllowSet は target にだけある要素を KindAllowAdded として報告する
// （exclude / ignore / allowed_types は要素が増えるほど対象が減る＝緩くなる）。
func compareAllowSet(path string, bv, tv any) []Loosening {
	bSet := stringSet(bv)
	tSet := stringSet(tv)

	var added []string
	for e := range tSet {
		if !bSet[e] {
			added = append(added, e)
		}
	}
	sort.Strings(added)

	out := make([]Loosening, 0, len(added))
	for _, e := range added {
		out = append(out, Loosening{Kind: KindAllowAdded, Path: path, After: e})
	}
	return out
}

// compareTargetSet は base にだけある要素を KindTargetRemoved として報告する
// （docs / path_prefixes は要素が減るほど見る対象が減る＝緩くなる）。
func compareTargetSet(path string, bv, tv any) []Loosening {
	bSet := stringSet(bv)
	tSet := stringSet(tv)

	var removed []string
	for e := range bSet {
		if !tSet[e] {
			removed = append(removed, e)
		}
	}
	sort.Strings(removed)

	out := make([]Loosening, 0, len(removed))
	for _, e := range removed {
		out = append(out, Loosening{Kind: KindTargetRemoved, Path: path, Before: e})
	}
	return out
}

func stringSet(v any) map[string]bool {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	set := make(map[string]bool, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			set[s] = true
		}
	}
	return set
}

// compareBoolTurnedOff は true から false・省略になったときだけ KindTurnedOff にする
// （check_anchors は既定 false のオプトインなので、逆方向は緩和ではない）。
func compareBoolTurnedOff(path string, bv, tv any) []Loosening {
	b, _ := bv.(bool)
	t, _ := tv.(bool)
	if b && !t {
		return []Loosening{chg(KindTurnedOff, path, "true", render(tv))}
	}
	return nil
}

// diffTypes は base・target のどちらかの checks から参照されている types.<name> だけを
// 比較する（どの検査からも参照されていない type の変更は見ない、という決定に基づく）。
func diffTypes(base, target *Snapshot) []Loosening {
	var out []Loosening
	for _, name := range referencedTypeNames(base, target) {
		bt := mapAt(mapAt(base.root, "types"), name)
		tt := mapAt(mapAt(target.root, "types"), name)
		out = append(out, diffTypeConfig(name, bt, tt)...)
	}
	return out
}

func referencedTypeNames(base, target *Snapshot) []string {
	set := map[string]bool{}
	for _, snap := range [2]*Snapshot{base, target} {
		for _, v := range mapAt(snap.root, "checks") {
			cm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if t := stringAt(cm, "type"); t != "" {
				set[t] = true
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// typeConfigTopFields は types.<name> 直下で default 以外に等価比較するキー。
var typeConfigTopFields = []string{"command", "args", "transport", "schema"}

// diffTypeConfig は types.<name> を比較する。command・args・transport・schema・
// default.granularity・未知のキーは全て等しいかどうかだけで比較する
// （command 型のオプションと同じく、変えたら理由が要るという決定に基づく）。
// default.exempt は checks.k の行（exempt.enable / exempt.trailer）が見るのでここでは見ない。
func diffTypeConfig(name string, bt, tt map[string]any) []Loosening {
	path := "types." + name
	var out []Loosening

	for _, f := range typeConfigTopFields {
		if !reflect.DeepEqual(bt[f], tt[f]) {
			out = append(out, chg(KindChanged, path+"."+f, render(bt[f]), render(tt[f])))
		}
	}

	bd, td := mapAt(bt, "default"), mapAt(tt, "default")
	if !reflect.DeepEqual(bd["granularity"], td["granularity"]) {
		out = append(out, chg(KindChanged, path+".default.granularity", render(bd["granularity"]), render(td["granularity"])))
	}

	for _, k := range unionKeys(bt, tt) {
		if k == "default" || contains(typeConfigTopFields, k) {
			continue
		}
		if !reflect.DeepEqual(bt[k], tt[k]) {
			out = append(out, chg(KindChanged, path+"."+k, render(bt[k]), render(tt[k])))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
