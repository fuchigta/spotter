package docpaths_test

import (
	"reflect"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docpaths"
	"github.com/fuchigta/spotter/internal/config"
)

func TestExplain(t *testing.T) {
	tests := []struct {
		name string
		cc   config.CheckConfig
		docs map[string]string
		want check.Explanation
	}{
		{
			name: "フェンスを除いた元の行番号で候補を出し ignore を注記する",
			cc:   config.CheckConfig{Type: config.TypeDocPaths, PathPrefixes: []string{"internal", "cmd"}, Ignore: []string{"cmd/x"}},
			docs: map[string]string{
				"README.md": "見出し\n`internal/a.go` と `internal/a.go`\n```sh\n`internal/in-fence.go`\n```\n`cmd/x` `internal/a.go`\n`other/y.go`\n",
			},
			want: check.Explanation{
				Details: []string{"docs: **/*.md（省略時）", "path_prefixes: internal, cmd"},
				Sections: []check.ExplanationSection{{
					Title: "README.md",
					Items: []check.ExplanationItem{
						{Value: "cmd/x", Locations: []string{"L6"}, Note: "ignore で除外"},
						{Value: "internal/a.go", Locations: []string{"L2", "L6"}},
					},
				}},
			},
		},
		{
			name: "候補が 0 件のドキュメントも出す",
			cc:   config.CheckConfig{Type: config.TypeDocPaths, PathPrefixes: []string{"internal"}, Docs: []string{"docs/*.md"}},
			docs: map[string]string{"docs/a.md": "なし\n", "README.md": "`internal/x.go`\n"},
			want: check.Explanation{
				Details: []string{"docs: docs/*.md", "path_prefixes: internal"},
				Sections: []check.ExplanationSection{{
					Title: "docs/a.md",
					Items: []check.ExplanationItem{},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := docpaths.New(tt.cc)
			if err != nil {
				t.Fatalf("New に失敗しました: %v", err)
			}
			got, err := c.Explain(check.Context{FS: mapFS(tt.docs)})
			if err != nil {
				t.Fatalf("Explain がエラーを返しました: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("説明が一致しません\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

func TestExplainErrors(t *testing.T) {
	tests := []struct {
		name string
		cc   config.CheckConfig
		fsys func() check.Context
	}{
		{
			name: "不正な docs パターン",
			cc:   config.CheckConfig{Type: config.TypeDocPaths, PathPrefixes: []string{"internal"}, Docs: []string{"["}},
			fsys: func() check.Context { return check.Context{FS: mapFS(map[string]string{"a.md": ""})} },
		},
		{
			name: "ドキュメントの読み込みに失敗する",
			cc:   config.CheckConfig{Type: config.TypeDocPaths, PathPrefixes: []string{"internal"}},
			fsys: func() check.Context {
				return check.Context{FS: newFailFS(map[string]string{"a.md": "`internal/x`"}, "a.md")}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := docpaths.New(tt.cc)
			if err != nil {
				t.Fatalf("New に失敗しました: %v", err)
			}
			if _, err := c.Explain(tt.fsys()); err == nil {
				t.Error("Explain がエラーを返す想定でしたが nil でした")
			}
		})
	}
}
