package consistency_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/consistency"
	"github.com/fuchigta/spotter/internal/config"
)

func TestExplain(t *testing.T) {
	other := config.ConsistencySource{File: "b.txt", Extract: `(b)`}
	otherFile := map[string]string{"b.txt": "b\n"}

	tests := []struct {
		name         string
		first        config.ConsistencySource
		files        map[string]string
		want         check.ExplanationSection
		runErrSubstr string
	}{
		{
			name:  "line だけを指定するとline に一致した行を示す",
			first: config.ConsistencySource{File: "a.txt", Line: `^x=`, Extract: `x=(\d)`},
			files: map[string]string{"a.txt": "x=1\nfoo\nx=2\n"},
			want: check.ExplanationSection{
				Title:   "sources[0] file: a.txt",
				Details: []string{"line: ^x=", `extract: x=(\d)`, "line に一致した行: L1, L3"},
				Items: []check.ExplanationItem{
					{Value: "1", Locations: []string{"L1"}},
					{Value: "2", Locations: []string{"L3"}},
				},
			},
		},
		{
			name:  "line に一致する行が無ければなしと示す",
			first: config.ConsistencySource{File: "a.txt", Line: `^y=`, Extract: `x=(\d)`},
			files: map[string]string{"a.txt": "x=1\n"},
			want: check.ExplanationSection{
				Title:   "sources[0] file: a.txt",
				Details: []string{"line: ^y=", `extract: x=(\d)`, "line に一致した行: なし", "1 つも抽出できませんでした（spotter check では実行エラーになります）"},
				Items:   []check.ExplanationItem{},
			},
			runErrSubstr: "1 つも抽出できませんでした",
		},
		{
			name:  "line と until は複数のブロックの範囲を示す",
			first: config.ConsistencySource{File: "a.txt", Line: `^A$`, Until: `^end$`, Extract: `\s(a\d)`},
			files: map[string]string{"a.txt": "A\n a1\nend\nzz\nA\n a2\nend\n"},
			want: check.ExplanationSection{
				Title: "sources[0] file: a.txt",
				Details: []string{
					"line: ^A$", "until: ^end$", `extract: \s(a\d)`,
					"block: L1-L3", "block: L5-L7",
				},
				Items: []check.ExplanationItem{
					{Value: "a1", Locations: []string{"L2"}},
					{Value: "a2", Locations: []string{"L6"}},
				},
			},
		},
		{
			name:  "split は分割した要素ごとに行を示し同じ行の重複は 1 つにする",
			first: config.ConsistencySource{File: "a.txt", Extract: `list = (.*)`, Split: ",", Subset: true},
			files: map[string]string{"a.txt": "head\nlist = b, a, b\nlist = a\n"},
			want: check.ExplanationSection{
				Title:   "sources[0] file: a.txt",
				Details: []string{`extract: list = (.*)`, "split: ,", "subset: true"},
				Items: []check.ExplanationItem{
					{Value: "a", Locations: []string{"L2", "L3"}},
					{Value: "b", Locations: []string{"L2"}},
				},
			},
		},
		{
			name:  "until の終端が無くても説明は返し注記する",
			first: config.ConsistencySource{File: "a.txt", Line: `^A$`, Until: `^end$`, Extract: `\s(a\d)`},
			files: map[string]string{"a.txt": "x\nA\n a1\n"},
			want: check.ExplanationSection{
				Title: "sources[0] file: a.txt",
				Details: []string{
					"line: ^A$", "until: ^end$", `extract: \s(a\d)`,
					"L2 から始まるブロックの終端（until に一致する行）が見つかりませんでした（spotter check では実行エラーになります）",
				},
				Items: []check.ExplanationItem{},
			},
			runErrSubstr: "終端",
		},
		{
			name:  "1 つも抽出できなくても説明は返し注記する",
			first: config.ConsistencySource{File: "a.txt", Extract: `x=(\d)`},
			files: map[string]string{"a.txt": "nothing\n"},
			want: check.ExplanationSection{
				Title:   "sources[0] file: a.txt",
				Details: []string{`extract: x=(\d)`, "1 つも抽出できませんでした（spotter check では実行エラーになります）"},
				Items:   []check.ExplanationItem{},
			},
			runErrSubstr: "1 つも抽出できませんでした",
		},
		{
			name:  "glob は base と exclude を適用した結果とパスを示す",
			first: config.ConsistencySource{Glob: "docs/**/*.md", Base: "docs", Exclude: []string{"docs/skip.md"}, Extract: `(.*)\.md`},
			files: map[string]string{"docs/a.md": "", "docs/sub/b.md": "", "docs/skip.md": "", "b.txt": "b\n"},
			want: check.ExplanationSection{
				Title:   "sources[0] glob: docs/**/*.md",
				Details: []string{"base: docs", "exclude: docs/skip.md", `extract: (.*)\.md`},
				Items: []check.ExplanationItem{
					{Value: "a", Locations: []string{"docs/a.md"}},
					{Value: "sub/b", Locations: []string{"docs/sub/b.md"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range otherFile {
				files[k] = v
			}
			for k, v := range tt.files {
				files[k] = v
			}

			c, err := consistency.New(config.CheckConfig{
				Type:    config.TypeConsistency,
				Sources: []config.ConsistencySource{tt.first, other},
			})
			if err != nil {
				t.Fatalf("New に失敗しました: %v", err)
			}

			got, err := c.Explain(check.Context{FS: mapFS(files)})
			if err != nil {
				t.Fatalf("Explain がエラーを返しました: %v", err)
			}
			if len(got.Sections) != 2 {
				t.Fatalf("Sections が 2 件ではありません: %d", len(got.Sections))
			}
			if !reflect.DeepEqual(got.Sections[0], tt.want) {
				t.Errorf("Sections[0] が一致しません\n got: %#v\nwant: %#v", got.Sections[0], tt.want)
			}

			_, runErr := c.Run(check.Context{FS: mapFS(files)})
			if tt.runErrSubstr == "" {
				if runErr != nil {
					t.Errorf("Run が予期せずエラーを返しました: %v", runErr)
				}
				return
			}
			if runErr == nil || !strings.Contains(runErr.Error(), tt.runErrSubstr) {
				t.Errorf("Run のエラーに %q が含まれる想定でしたが %v でした", tt.runErrSubstr, runErr)
			}
		})
	}
}

func TestExplainReadFailure(t *testing.T) {
	tests := []struct {
		name string
		fs   check.Context
	}{
		{name: "ファイルが無い", fs: check.Context{FS: mapFS(map[string]string{"b.txt": "b\n"})}},
		{name: "読み取りに失敗する", fs: check.Context{FS: newFailReadFS(map[string]string{"a.txt": "a\n", "b.txt": "b\n"}, "a.txt")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := consistency.New(config.CheckConfig{
				Type: config.TypeConsistency,
				Sources: []config.ConsistencySource{
					{File: "a.txt", Extract: `(a)`},
					{File: "b.txt", Extract: `(b)`},
				},
			})
			if err != nil {
				t.Fatalf("New に失敗しました: %v", err)
			}
			if _, err := c.Explain(tt.fs); err == nil {
				t.Error("Explain がエラーを返す想定でしたが nil でした")
			}
			if _, err := c.Run(tt.fs); err == nil {
				t.Error("Run がエラーを返す想定でしたが nil でした")
			}
		})
	}
}
