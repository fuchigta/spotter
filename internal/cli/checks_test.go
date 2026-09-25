package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/config"
)

// checkCatalog の Type 集合が config.BuiltinTypeNames() と過不足なく一致することを確認する。
// 組み込み検査を追加・削除したときに checkCatalog の更新漏れを検知するためのガード。
func TestCheckCatalogMatchesBuiltinTypes(t *testing.T) {
	want := config.BuiltinTypeNames()

	got := make([]string, 0, len(checkCatalog))
	for _, c := range checkCatalog {
		got = append(got, c.Type)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(want, got) {
		t.Errorf("checkCatalog の type 一覧が builtinTypes とズレています\n  builtinTypes = %v\n  checkCatalog = %v", want, got)
	}
}

// checkCatalog に書いた granularity が各検査パッケージの実装（Runner.Granularity()）と
// 一致することを、実際に buildRunner でインスタンス化して確認する。
func TestCheckCatalogGranularityMatchesRunner(t *testing.T) {
	// 各組み込み type が New() を通すための最小限の有効な設定。
	fixtures := map[string]config.CheckConfig{
		config.TypeDocSync: {
			Type:  config.TypeDocSync,
			Pairs: []config.DocSyncPair{{Paths: "**/*.go", Doc: "README.md"}},
		},
		config.TypeUnwantedFiles: {
			Type:     config.TypeUnwantedFiles,
			MaxBytes: 1,
		},
		config.TypeDocPaths: {
			Type:         config.TypeDocPaths,
			PathPrefixes: []string{"internal"},
		},
		config.TypeCommitSubject: {
			Type:         config.TypeCommitSubject,
			AllowedTypes: []string{"feat"},
		},
		config.TypeConsistency: {
			Type: config.TypeConsistency,
			Sources: []config.ConsistencySource{
				{File: "a.txt", Extract: "(a)"},
				{File: "b.txt", Extract: "(b)"},
			},
		},
		config.TypeDiffContent: {
			Type: config.TypeDiffContent,
			Deny: []config.DenyRule{{Pattern: "x", Reason: "y"}},
		},
		config.TypeCommitIntent: {
			Type: config.TypeCommitIntent,
			Rules: []config.CommitIntentRule{
				{Types: []string{"feat"}, Allow: []string{"**"}},
			},
		},
		config.TypeCompanionFiles: {
			Type: config.TypeCompanionFiles,
			Companions: []config.CompanionRule{
				{Paths: "**/*.go", Companion: []string{"{dir}/{name}_test.go"}, Reason: "テストが無い"},
			},
		},
		config.TypeDocLinks: {
			Type: config.TypeDocLinks,
		},
		config.TypeDiffSize: {
			Type:     config.TypeDiffSize,
			MaxFiles: 10,
		},
		config.TypeConfigGuard: {
			Type: config.TypeConfigGuard,
		},
	}

	cfg := &config.Config{}

	for _, entry := range checkCatalog {
		cc, ok := fixtures[entry.Type]
		if !ok {
			t.Errorf("%s: fixture がありません（checkCatalog に type を足したら fixtures にも追加してください）", entry.Type)
			continue
		}

		runner, err := buildRunner(cfg, entry.Type, cc)
		if err != nil {
			t.Errorf("%s: buildRunner に失敗しました: %v", entry.Type, err)
			continue
		}

		if got := string(runner.Granularity()); got != entry.Granularity {
			t.Errorf("%s: granularity が実装とズレています（catalog=%s, runner=%s）", entry.Type, entry.Granularity, got)
		}

		wantExempt := entry.Granularity != "worktree"
		if entry.ExemptSupported != wantExempt {
			t.Errorf("%s: ExemptSupported=%t だが granularity=%s（worktree なら false のはず）", entry.Type, entry.ExemptSupported, entry.Granularity)
		}
		if entry.ExemptSupported && entry.ExemptDefaultEnabled == nil {
			t.Errorf("%s: ExemptSupported=true なのに ExemptDefaultEnabled が nil です", entry.Type)
		}
		if !entry.ExemptSupported && entry.ExemptDefaultEnabled != nil {
			t.Errorf("%s: ExemptSupported=false なのに ExemptDefaultEnabled が nil ではありません", entry.Type)
		}
		if entry.ExemptScopedSupported && !entry.ExemptSupported {
			t.Errorf("%s: ExemptScopedSupported=true なら ExemptSupported も true のはず", entry.Type)
		}
		if entry.ExemptScopedSupported != (entry.ExemptScopedTargetKind != "") {
			t.Errorf("%s: ExemptScopedSupported と ExemptScopedTargetKind の有無が一致していません", entry.Type)
		}
	}
}

// TestCheckCatalogScopedExemptMatchesRunner は、checkCatalog で
// ExemptScopedSupported=true とした検査が実際に check.ScopedExemptable を実装しているかを
// 確認する（カタログと実装がズレて機械可読な情報が嘘になるのを防ぐ）。
func TestCheckCatalogScopedExemptMatchesRunner(t *testing.T) {
	fixtures := map[string]config.CheckConfig{
		config.TypeDocSync: {
			Type:  config.TypeDocSync,
			Pairs: []config.DocSyncPair{{Paths: "**/*.go", Doc: "README.md"}},
		},
	}

	cfg := &config.Config{}
	for _, entry := range checkCatalog {
		if !entry.ExemptScopedSupported {
			continue
		}
		cc, ok := fixtures[entry.Type]
		if !ok {
			t.Errorf("%s: ExemptScopedSupported=true だが fixture がありません", entry.Type)
			continue
		}
		runner, err := buildRunner(cfg, entry.Type, cc)
		if err != nil {
			t.Errorf("%s: buildRunner に失敗しました: %v", entry.Type, err)
			continue
		}
		if _, ok := runner.(interface{ ExemptTargets() []string }); !ok {
			t.Errorf("%s: ExemptScopedSupported=true だが ExemptTargets() を実装していません", entry.Type)
		}
	}
}

// yamlTags は構造体の yaml タグ名の集合を返す（",inline" や空タグは除く）。
func yamlTags(t reflect.Type) map[string]bool {
	tags := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		tags[name] = true
	}
	return tags
}

// checkCatalog / commonFields に書いたキー名が config.CheckConfig（トップレベル）や
// 各検査の入れ子設定構造体（DocSyncPair 等）に実在する yaml タグかどうかを確認する。
// 「幻の設定キー」をカタログ自身に紛れ込ませないためのガード。
func TestCheckCatalogFieldKeysExistInConfig(t *testing.T) {
	checkConfigTags := yamlTags(reflect.TypeOf(config.CheckConfig{}))
	exemptConfigTags := yamlTags(reflect.TypeOf(config.ExemptConfig{}))

	// 配列フィールドの要素がオブジェクトのとき、その子フィールドの照合先となる構造体。
	nestedTags := map[string]map[string]bool{
		"pairs":      yamlTags(reflect.TypeOf(config.DocSyncPair{})),
		"deny":       yamlTags(reflect.TypeOf(config.DenyRule{})),
		"sources":    yamlTags(reflect.TypeOf(config.ConsistencySource{})),
		"rules":      yamlTags(reflect.TypeOf(config.CommitIntentRule{})),
		"companions": yamlTags(reflect.TypeOf(config.CompanionRule{})),
		"exempt":     exemptConfigTags,
	}

	checkFields := func(t *testing.T, label string, tags map[string]bool, fields []FieldInfo) {
		t.Helper()
		for _, f := range fields {
			if !tags[f.Key] {
				t.Errorf("%s: フィールド %q は対応する構造体の yaml タグに存在しません", label, f.Key)
				continue
			}
			if len(f.Fields) == 0 {
				continue
			}
			childTags, ok := nestedTags[f.Key]
			if !ok {
				t.Errorf("%s.%s: 入れ子構造体が nestedTags に未登録です（checks.go で追加したら nestedTags にも追加してください）", label, f.Key)
				continue
			}
			for _, child := range f.Fields {
				if !childTags[child.Key] {
					t.Errorf("%s.%s: 子フィールド %q が対応する構造体の yaml タグに存在しません", label, f.Key, child.Key)
				}
			}
		}
	}

	checkFields(t, "commonFields", checkConfigTags, commonFields)
	for _, entry := range checkCatalog {
		checkFields(t, entry.Type, checkConfigTags, entry.Fields)
	}
}

// nestedStructFields は checkCatalog の子フィールドのうち、1 つの組み込み type からしか
// 使われない構造体を持つものについて、対応する Go 構造体の型を紐づける。config.Load は
// これらを yaml.Decoder.KnownFields(true) で直接デコードするため、要素の未知キーは
// デコードの時点でエラーになり、config.BuiltinNestedKeys() 側に型ごとの許可キー一覧を
// 別途持つ必要が無い（重複管理を避けるため、ここでは Go 構造体の yaml タグ自体を正とする）。
// deny（unwanted-files と diff-content が共用する DenyRule。構造体としては型ごとに
// 使わないキーも正規のフィールドとして持つため KnownFields では検知できない）だけは
// config.BuiltinNestedKeys() 側に type ごとの許可キー一覧があるので、ここには含めない。
var nestedStructFields = map[string]reflect.Type{
	"pairs":      reflect.TypeOf(config.DocSyncPair{}),
	"sources":    reflect.TypeOf(config.ConsistencySource{}),
	"rules":      reflect.TypeOf(config.CommitIntentRule{}),
	"companions": reflect.TypeOf(config.CompanionRule{}),
}

// checkCatalog のトップレベルフィールドのキー集合が config.BuiltinTypeKeys()（Load が
// checks.<key> のキー検証に使う正の情報源）と type ごとに過不足なく一致することを確認する。
// 片方だけキーを追加・削除するとここでズレを検知する。あわせて、要素がオブジェクトの配列に
// なっているフィールドについては、その子フィールドのキー集合も正の情報源
// （nestedStructFields の対象なら Go 構造体の yaml タグ、deny なら
// config.BuiltinNestedKeys()）と一致することを確認する。
func TestCheckCatalogKeysMatchConfigBuiltinTypeKeys(t *testing.T) {
	for _, entry := range checkCatalog {
		got := make([]string, 0, len(entry.Fields))
		for _, f := range entry.Fields {
			got = append(got, f.Key)
		}
		sort.Strings(got)

		want := config.BuiltinTypeKeys(entry.Type)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: checkCatalog のキー = %v, config.BuiltinTypeKeys() = %v", entry.Type, got, want)
		}

		for _, f := range entry.Fields {
			if len(f.Fields) == 0 {
				continue
			}
			gotNested := make([]string, 0, len(f.Fields))
			for _, child := range f.Fields {
				gotNested = append(gotNested, child.Key)
			}
			sort.Strings(gotNested)

			var wantNested []string
			if structType, ok := nestedStructFields[f.Key]; ok {
				tags := yamlTags(structType)
				wantNested = make([]string, 0, len(tags))
				for k := range tags {
					wantNested = append(wantNested, k)
				}
				sort.Strings(wantNested)
			} else {
				wantNested = config.BuiltinNestedKeys(entry.Type, f.Key)
			}

			if !reflect.DeepEqual(gotNested, wantNested) {
				t.Errorf("%s.%s: checkCatalog の子フィールドのキー = %v, want = %v", entry.Type, f.Key, gotNested, wantNested)
			}
		}
	}
}

func TestRunChecksJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := runChecks(&buf, true); err != nil {
		t.Fatalf("runChecks: %v", err)
	}

	var out ChecksOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal: %v（出力: %s）", err, buf.String())
	}

	if len(out.Types) != len(checkCatalog) {
		t.Errorf("出力された type の件数 = %d, want %d", len(out.Types), len(checkCatalog))
	}
	if out.SchemaVersion != checksSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", out.SchemaVersion, checksSchemaVersion)
	}
	if out.SpotterVersion == "" {
		t.Error("SpotterVersion が空です")
	}
	if len(out.CommonFields) == 0 {
		t.Error("CommonFields が空です")
	}
}

func TestRunChecksText(t *testing.T) {
	var buf bytes.Buffer
	if err := runChecks(&buf, false); err != nil {
		t.Fatalf("runChecks: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"doc-sync", "granularity=squashed", "max_files", "exempt=非対応（worktree）", "exempt_scoped=doc単位"} {
		if !strings.Contains(out, want) {
			t.Errorf("テキスト出力に %q が含まれていません:\n%s", want, out)
		}
	}
}
