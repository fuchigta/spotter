package consistency

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
)

// Explain は source ごとの抽出結果を、比較せずにそのまま返す。Run と同じ extractSource を
// 通すので、表示した要素がそのまま比較に使われる。
func (c *Check) Explain(ctx check.Context) (check.Explanation, error) {
	var out check.Explanation
	for i, s := range c.sources {
		ex, err := extractSource(ctx.FS, i, s)
		if err != nil {
			return check.Explanation{}, err
		}
		out.Sections = append(out.Sections, explainSource(i, s, ex))
	}
	return out, nil
}

func explainSource(idx int, s source, ex extraction) check.ExplanationSection {
	kind, details := "file", fileDetails(s, ex)
	if s.isGlob {
		kind, details = "glob", globDetails(s)
	}
	items := explainItems(ex.items)

	switch {
	case ex.unterminatedAt > 0:
		details = append(details, fmt.Sprintf(
			"L%d から始まるブロックの終端（until に一致する行）が見つかりませんでした（spotter check では実行エラーになります）",
			ex.unterminatedAt))
	case len(items) == 0:
		details = append(details, "1 つも抽出できませんでした（spotter check では実行エラーになります）")
	}

	return check.ExplanationSection{
		Title:   fmt.Sprintf("sources[%d] %s: %s", idx, kind, s.label()),
		Details: details,
		Items:   items,
	}
}

// fileDetails は file source の設定値と、line/until が見たブロック・行を並べる。
func fileDetails(s source, ex extraction) []string {
	var details []string
	if s.line != nil {
		details = append(details, "line: "+s.line.String())
	}
	if s.until != nil {
		details = append(details, "until: "+s.until.String())
	}
	details = append(details, "extract: "+s.extract.String())
	if s.split != "" {
		details = append(details, "split: "+s.split)
	}
	if s.subset {
		details = append(details, "subset: true")
	}
	for _, b := range ex.blocks {
		details = append(details, fmt.Sprintf("block: L%d-L%d", b.start, b.end))
	}
	if s.line != nil && s.until == nil {
		details = append(details, "line に一致した行: "+lineList(ex.lineMatches))
	}
	return details
}

func globDetails(s source) []string {
	var details []string
	if s.base != "" {
		details = append(details, "base: "+s.base)
	}
	if len(s.exclude) > 0 {
		details = append(details, "exclude: "+strings.Join(s.exclude, ", "))
	}
	if s.extract != nil {
		details = append(details, "extract: "+s.extract.String())
	}
	if s.subset {
		details = append(details, "subset: true")
	}
	return details
}

func lineList(lines []int) string {
	if len(lines) == 0 {
		return "なし"
	}
	parts := make([]string, len(lines))
	for i, n := range lines {
		parts[i] = "L" + strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// explainItems は同じ値をまとめ、値の昇順に並べる。出現位置は出現順のまま重複だけ除く。
func explainItems(items []item) []check.ExplanationItem {
	byValue := map[string]*check.ExplanationItem{}
	for _, it := range items {
		loc := it.path
		if loc == "" {
			loc = "L" + strconv.Itoa(it.line)
		}
		e, ok := byValue[it.value]
		if !ok {
			e = &check.ExplanationItem{Value: it.value}
			byValue[it.value] = e
		}
		if len(e.Locations) == 0 || e.Locations[len(e.Locations)-1] != loc {
			e.Locations = append(e.Locations, loc)
		}
	}

	values := make([]string, 0, len(byValue))
	for v := range byValue {
		values = append(values, v)
	}
	sort.Strings(values)

	out := make([]check.ExplanationItem, 0, len(values))
	for _, v := range values {
		out = append(out, *byValue[v])
	}
	return out
}
