package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
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
			Type: config.TypeUnwantedFiles,
		},
		config.TypeDocPaths: {
			Type: config.TypeDocPaths,
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
				{Paths: "**/*.go", Companion: "{dir}/{name}_test.go", Reason: "テストが無い"},
			},
		},
		config.TypeDocLinks: {
			Type: config.TypeDocLinks,
		},
		config.TypeDiffSize: {
			Type:     config.TypeDiffSize,
			MaxFiles: 10,
		},
	}

	cfg := &config.Config{}

	for _, entry := range checkCatalog {
		cc, ok := fixtures[entry.Type]
		if !ok {
			t.Fatalf("%s: fixture がありません（checkCatalog に type を足したら fixtures にも追加してください）", entry.Type)
		}

		runner, err := buildRunner(cfg, entry.Type, cc)
		if err != nil {
			t.Fatalf("%s: buildRunner に失敗しました: %v", entry.Type, err)
		}

		if got := string(runner.Granularity()); got != entry.Granularity {
			t.Errorf("%s: granularity が実装とズレています（catalog=%s, runner=%s）", entry.Type, entry.Granularity, got)
		}
	}
}

func TestRunChecksJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := runChecks(&buf, true); err != nil {
		t.Fatalf("runChecks: %v", err)
	}

	var out checksOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal: %v（出力: %s）", err, buf.String())
	}

	if len(out.Types) != len(checkCatalog) {
		t.Errorf("出力された type の件数 = %d, want %d", len(out.Types), len(checkCatalog))
	}
}

func TestRunChecksText(t *testing.T) {
	var buf bytes.Buffer
	if err := runChecks(&buf, false); err != nil {
		t.Fatalf("runChecks: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("テキスト出力が空です")
	}
}
