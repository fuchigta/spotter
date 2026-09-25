package configdiff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/config"
)

// yamlTagName は構造体フィールドの yaml タグから、比較のキー名と inline map かどうかを
// 取り出す（omitempty 等の修飾子は無視する）。
func yamlTagName(tag reflect.StructTag) (name string, inline bool) {
	raw, ok := tag.Lookup("yaml")
	if !ok {
		return "", false
	}
	parts := strings.Split(raw, ",")
	for _, p := range parts[1:] {
		if p == "inline" {
			inline = true
		}
	}
	return parts[0], inline
}

// TestCheckFieldRulesCoversCheckConfigTags は config.CheckConfig の yaml タグ
// （type・exempt・Options の inline map を除く）が checkFieldRules ともれなく対応する
// ことを確かめる。分類表に無いフィールドを足すと、汎用の「等しいかどうか」比較に
// フォールバックしてしまい、意味を判定した比較が効かなくなる。
func TestCheckFieldRulesCoversCheckConfigTags(t *testing.T) {
	fields := make(map[string]reflect.StructField)
	rt := reflect.TypeOf(config.CheckConfig{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, inline := yamlTagName(f.Tag)
		if inline {
			continue
		}
		if name == "" {
			t.Fatalf("config.CheckConfig.%s に yaml タグがありません", f.Name)
		}
		fields[name] = f
	}

	for name := range fields {
		if checkCommonFields[name] {
			continue
		}
		if _, ok := checkFieldRules[name]; !ok {
			t.Errorf("checkFieldRules に checks.<key>.%s がありません", name)
		}
	}
	for name := range checkFieldRules {
		if _, ok := fields[name]; !ok {
			t.Errorf("checkFieldRules の %s に対応する config.CheckConfig のフィールドがありません", name)
		}
	}
}

// TestCheckFieldRuleKindMatchesFieldType は分類表の種類と、対応する Go の型が
// 矛盾していないかを確かめる（例: ruleLimit を文字列スライスのフィールドに付けていないか）。
func TestCheckFieldRuleKindMatchesFieldType(t *testing.T) {
	rt := reflect.TypeOf(config.CheckConfig{})
	fields := make(map[string]reflect.StructField)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, inline := yamlTagName(f.Tag)
		if inline || name == "" {
			continue
		}
		fields[name] = f
	}

	stringSliceType := reflect.TypeOf([]string{})

	for name, kind := range checkFieldRules {
		f, ok := fields[name]
		if !ok {
			t.Fatalf("%s に対応する config.CheckConfig のフィールドがありません", name)
		}
		switch kind {
		case ruleLimit:
			if k := f.Type.Kind(); k != reflect.Int64 && k != reflect.Int {
				t.Errorf("%s: ruleLimit は整数のフィールドを期待しますが %s でした", name, f.Type)
			}
		case ruleArray:
			if f.Type.Kind() != reflect.Slice || f.Type.Elem().Kind() != reflect.Struct {
				t.Errorf("%s: ruleArray は構造体スライスのフィールドを期待しますが %s でした", name, f.Type)
			}
		case ruleAllowSet, ruleTargetSet:
			if f.Type != stringSliceType {
				t.Errorf("%s: ruleAllowSet/ruleTargetSet は []string のフィールドを期待しますが %s でした", name, f.Type)
			}
		case ruleBoolTurnedOff:
			if f.Type.Kind() != reflect.Bool {
				t.Errorf("%s: ruleBoolTurnedOff は bool のフィールドを期待しますが %s でした", name, f.Type)
			}
		default:
			t.Errorf("%s: 未知の ruleKind %d", name, kind)
		}
	}
}

// TestBuiltinTypeKeysAllClassified は組み込み type ごとの type 固有キー
// （config.BuiltinTypeKeys）が全て checkFieldRules に分類されていることを確かめる。
// 分類漏れがあると、その組み込み type のそのキーだけ意味を判定できず、
// 常に「等しいかどうか」比較に落ちてしまう。
func TestBuiltinTypeKeysAllClassified(t *testing.T) {
	for _, name := range config.BuiltinTypeNames() {
		for _, key := range config.BuiltinTypeKeys(name) {
			if _, ok := checkFieldRules[key]; !ok {
				t.Errorf("types.%s の checks.<key>.%s が checkFieldRules に分類されていません", name, key)
			}
		}
	}
}

func topLevelYAMLTags(t *testing.T, v any) map[string]bool {
	t.Helper()
	rt := reflect.TypeOf(v)
	out := make(map[string]bool)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, inline := yamlTagName(f.Tag)
		if inline {
			continue
		}
		if name == "" {
			t.Fatalf("%s.%s に yaml タグがありません", rt.Name(), f.Name)
		}
		out[name] = true
	}
	return out
}

func assertSameKeys(t *testing.T, label string, got map[string]bool, want map[string]bool) {
	t.Helper()
	for k := range want {
		if !got[k] {
			t.Errorf("%s: %s が足りません", label, k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("%s: %s は分類表にありません", label, k)
		}
	}
}

func TestTopLevelFieldsCoverConfigTags(t *testing.T) {
	assertSameKeys(t, "config.Config", topLevelYAMLTags(t, config.Config{}), topLevelFields)
}

func TestTypeConfigFieldsCoverTypeConfigTags(t *testing.T) {
	assertSameKeys(t, "config.TypeConfig", topLevelYAMLTags(t, config.TypeConfig{}), typeConfigFields)
}

func TestTypeDefaultFieldsCoverTypeDefaultTags(t *testing.T) {
	assertSameKeys(t, "config.TypeDefault", topLevelYAMLTags(t, config.TypeDefault{}), typeDefaultFields)
}

func TestExemptConfigFieldsCoverExemptConfigTags(t *testing.T) {
	assertSameKeys(t, "config.ExemptConfig", topLevelYAMLTags(t, config.ExemptConfig{}), exemptConfigFields)
}

// TestCompanionStringOrListNormalized は companions[].companion をスカラーで書いても
// 配列で書いても、canonicalElement が同じ文字列になることを確かめる
// （config.StringOrList が両方の書き方を受け付けるのに合わせる）。
func TestCompanionStringOrListNormalized(t *testing.T) {
	scalar, err := Parse([]byte("checks:\n  a:\n    type: companion-files\n    companions:\n      - paths: 'x/*.go'\n        companion: '{dir}/{name}_test.go'\n        reason: r\n"))
	if err != nil {
		t.Fatalf("Parse(scalar) failed: %v", err)
	}
	list, err := Parse([]byte("checks:\n  a:\n    type: companion-files\n    companions:\n      - paths: 'x/*.go'\n        companion: ['{dir}/{name}_test.go']\n        reason: 別の理由\n"))
	if err != nil {
		t.Fatalf("Parse(list) failed: %v", err)
	}

	scalarChecksA, _ := mapAt(scalar.root, "checks")["a"].(map[string]any)
	listChecksA, _ := mapAt(list.root, "checks")["a"].(map[string]any)

	got := canonicalElements(scalarChecksA["companions"])
	want := canonicalElements(listChecksA["companions"])
	if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
		t.Fatalf("companion のスカラーと配列表記が正規化後に一致しません: %v vs %v", got, want)
	}
}

func TestSortedKeysAndUnionKeysDeterministic(t *testing.T) {
	m := map[string]any{"b": 1, "a": 2, "c": 3}
	got := sortedKeys(m)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedKeys = %v, want %v", got, want)
	}

	u := unionKeys(map[string]any{"a": 1}, map[string]any{"b": 2, "a": 3})
	wantUnion := []string{"a", "b"}
	if !reflect.DeepEqual(u, wantUnion) {
		t.Fatalf("unionKeys = %v, want %v", u, wantUnion)
	}
}

func TestCanonicalElementStripsReason(t *testing.T) {
	e := map[string]any{"paths": "x", "reason": "何か", "on": "added"}
	got := canonicalElement(e)
	if strings.Contains(got, "reason") {
		t.Fatalf("canonicalElement に reason が残っています: %s", got)
	}
	e2 := map[string]any{"paths": "x", "reason": "別の理由", "on": "added"}
	if canonicalElement(e) != canonicalElement(e2) {
		t.Fatalf("reason だけが違う要素は同じ正規化結果になるべきです: %s vs %s", canonicalElement(e), canonicalElement(e2))
	}
}
