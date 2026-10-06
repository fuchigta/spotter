package docpaths

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docutil"
)

// Explain は対象ドキュメントごとのパス候補と出現行を、実在確認をせずに返す。実在するかは
// 判定そのものなので表示しない。Run と同じ extractCandidates を通す。
func (c *Check) Explain(ctx check.Context) (check.Explanation, error) {
	docs, err := docutil.ResolveDocs(ctx.FS, c.docs)
	if err != nil {
		return check.Explanation{}, fmt.Errorf("docpaths: 対象ドキュメントの解決に失敗しました: %w", err)
	}

	docPatterns := "**/*.md（省略時）"
	if len(c.docs) > 0 {
		docPatterns = strings.Join(c.docs, ", ")
	}
	out := check.Explanation{
		Details: []string{"docs: " + docPatterns, "path_prefixes: " + strings.Join(c.prefixes, ", ")},
	}

	for _, doc := range docs {
		data, err := fs.ReadFile(ctx.FS, doc)
		if err != nil {
			return check.Explanation{}, fmt.Errorf("docpaths: %s の読み込みに失敗しました: %w", doc, err)
		}

		section := check.ExplanationSection{Title: doc, Items: []check.ExplanationItem{}}
		for _, cand := range c.extractCandidates(string(data)) {
			item := check.ExplanationItem{Value: cand.path}
			for _, n := range cand.lines {
				item.Locations = append(item.Locations, "L"+strconv.Itoa(n))
			}
			if c.ignore[cand.path] {
				item.Note = "ignore で除外"
			}
			section.Items = append(section.Items, item)
		}
		out.Sections = append(out.Sections, section)
	}
	return out, nil
}
