package doclinks_test

import (
	"reflect"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/doclinks"
	"github.com/fuchigta/spotter/internal/config"
)

func TestExplain(t *testing.T) {
	doc := "[a](b.md#sec) [e](https://e.com)\n" +
		"[i](ignored.md) [t](<>)\n" +
		"[o](../../out.md)\n" +
		"```\n[f](fence.md)\n```\n" +
		"[a2](b.md#sec)\n" +
		"[s](#top)\n"

	tests := []struct {
		name string
		cc   config.CheckConfig
		docs map[string]string
		want check.Explanation
	}{
		{
			name: "リンクを種類ごとに注記し同じリンク先の出現行をまとめる",
			cc:   config.CheckConfig{Type: config.TypeDocLinks, Ignore: []string{"ignored.md"}, CheckAnchors: true},
			docs: map[string]string{"docs/a.md": doc, "docs/empty.md": "リンクなし\n"},
			want: check.Explanation{
				Details: []string{"docs: **/*.md（省略時）", "check_anchors: true"},
				Sections: []check.ExplanationSection{
					{
						Title: "docs/a.md",
						Items: []check.ExplanationItem{
							{Value: "b.md#sec", Locations: []string{"L1", "L7"}, Note: "→ docs/b.md #sec"},
							{Value: "https://e.com", Locations: []string{"L1"}, Note: "外部リンク（対象外）"},
							{Value: "ignored.md", Locations: []string{"L2"}, Note: "ignore で除外"},
							{Value: "<>", Locations: []string{"L2"}, Note: "空のリンク（対象外）"},
							{Value: "../../out.md", Locations: []string{"L3"}, Note: "リポジトリの外を指す"},
							{Value: "#top", Locations: []string{"L8"}, Note: "→ docs/a.md #top"},
						},
					},
					{Title: "docs/empty.md", Items: []check.ExplanationItem{}},
				},
			},
		},
		{
			name: "docs を指定すると対象を絞る",
			cc:   config.CheckConfig{Type: config.TypeDocLinks, Docs: []string{"docs/empty.md"}},
			docs: map[string]string{"docs/a.md": doc, "docs/empty.md": "[x](y.md)\n"},
			want: check.Explanation{
				Details: []string{"docs: docs/empty.md", "check_anchors: false"},
				Sections: []check.ExplanationSection{{
					Title: "docs/empty.md",
					Items: []check.ExplanationItem{{Value: "y.md", Locations: []string{"L1"}, Note: "→ docs/y.md"}},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := doclinks.New(tt.cc)
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
		ctx  check.Context
	}{
		{
			name: "不正な docs パターン",
			cc:   config.CheckConfig{Type: config.TypeDocLinks, Docs: []string{"["}},
			ctx:  check.Context{FS: mapFS(map[string]string{"a.md": ""})},
		},
		{
			name: "ドキュメントの読み込みに失敗する",
			cc:   config.CheckConfig{Type: config.TypeDocLinks},
			ctx:  check.Context{FS: newFailFS(map[string]string{"a.md": "[x](y.md)"}, "a.md")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := doclinks.New(tt.cc)
			if err != nil {
				t.Fatalf("New に失敗しました: %v", err)
			}
			if _, err := c.Explain(tt.ctx); err == nil {
				t.Error("Explain がエラーを返す想定でしたが nil でした")
			}
		})
	}
}
