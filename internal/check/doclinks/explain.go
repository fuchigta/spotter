package doclinks

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docutil"
)

// Explain は対象ドキュメントごとのリンクと、対象外か・どこへ解決されるかを返す。リンク先の
// 実在やアンカーの一致は判定そのものなので表示しない。Run と同じ classifyLinks を通す。
func (c *Check) Explain(ctx check.Context) (check.Explanation, error) {
	docs, err := docutil.ResolveDocs(ctx.FS, c.docs)
	if err != nil {
		return check.Explanation{}, fmt.Errorf("doclinks: 対象ドキュメントの解決に失敗しました: %w", err)
	}

	docPatterns := "**/*.md（省略時）"
	if len(c.docs) > 0 {
		docPatterns = strings.Join(c.docs, ", ")
	}
	out := check.Explanation{
		Details: []string{"docs: " + docPatterns, "check_anchors: " + strconv.FormatBool(c.checkAnchors)},
	}

	for _, doc := range docs {
		data, err := fs.ReadFile(ctx.FS, doc)
		if err != nil {
			return check.Explanation{}, fmt.Errorf("doclinks: %s の読み込みに失敗しました: %w", doc, err)
		}
		out.Sections = append(out.Sections, check.ExplanationSection{
			Title: doc,
			Items: explainLinks(c.classifyLinks(doc, string(data))),
		})
	}
	return out, nil
}

// explainLinks は同じリンク先（raw）を最初に出現した順にまとめ、全ての出現行を添える。
func explainLinks(links []classifiedLink) []check.ExplanationItem {
	items := []check.ExplanationItem{}
	index := map[string]int{}
	for _, cl := range links {
		i, ok := index[cl.occ.raw]
		if !ok {
			i = len(items)
			index[cl.occ.raw] = i
			items = append(items, check.ExplanationItem{Value: cl.occ.raw, Note: linkNote(cl)})
		}
		items[i].Locations = append(items[i].Locations, "L"+strconv.Itoa(cl.occ.line))
	}
	return items
}

func linkNote(cl classifiedLink) string {
	switch cl.kind {
	case linkEmpty:
		return "空のリンク（対象外）"
	case linkIgnored:
		return "ignore で除外"
	case linkExternal:
		return "外部リンク（対象外）"
	case linkEscapesRoot:
		return "リポジトリの外を指す"
	default:
		note := "→ " + cl.resolved
		if cl.anchor != "" {
			note += " #" + cl.anchor
		}
		return note
	}
}
