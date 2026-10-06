// Package docpaths はドキュメントが名指ししているコードのパスが実在するかを調べる検査を
// 実装する。
//
// doc-sync が守るのは「一緒に直したか」だけで、参照先の実在は守れない。この検査は
// git の差分ではなく現在の作業ツリーそのものを見るため、check.GranularityWorktree
// （staged/range を問わず 1 回だけ実行し、免除トレーラも持たない）を使う。
package docpaths

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docutil"
	"github.com/fuchigta/spotter/internal/config"
)

// backtickRe はバッククォートで囲まれた中身を拾う。地の文のそれらしい文字列まで拾うと
// 誤検知だらけになるため、これだけを候補とする。
var backtickRe = regexp.MustCompile("`([^`]+)`")

// Check は doc-paths 検査の 1 インスタンス。
type Check struct {
	docs       []string
	prefixes   []string
	ignore     map[string]bool
	pathLikeRe *regexp.Regexp
}

func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.PathPrefixes) == 0 {
		return nil, fmt.Errorf("docpaths: path_prefixes が必須です（指定されていないと候補が見つかりません）")
	}

	ignore := make(map[string]bool, len(cc.Ignore))
	for _, p := range cc.Ignore {
		if p == "" {
			continue
		}
		ignore[p] = true
	}

	re, err := compilePathLikeRe(cc.PathPrefixes)
	if err != nil {
		return nil, fmt.Errorf("docpaths: path_prefixes のコンパイルに失敗しました: %w", err)
	}

	return &Check{docs: cc.Docs, prefixes: cc.PathPrefixes, ignore: ignore, pathLikeRe: re}, nil
}

// compilePathLikeRe は接頭辞の一覧から「いずれかで始まる」正規表現を組み立てる。
func compilePathLikeRe(prefixes []string) (*regexp.Regexp, error) {
	parts := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		parts = append(parts, regexp.QuoteMeta(p))
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("path_prefixes に有効な値がありません")
	}
	re, err := regexp.Compile(`^(` + strings.Join(parts, "|") + `)/`)
	if err != nil {
		return nil, fmt.Errorf("正規表現にできません: %w", err)
	}
	return re, nil
}

func (c *Check) Granularity() check.Granularity {
	return check.GranularityWorktree
}

// Run は対象ドキュメントからバッククォート内のパス候補を抜き出し、実在を確認する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	fsys := ctx.FS

	docs, err := docutil.ResolveDocs(fsys, c.docs)
	if err != nil {
		return nil, fmt.Errorf("docpaths: 対象ドキュメントの解決に失敗しました: %w", err)
	}

	var violations []check.Violation
	for _, doc := range docs {
		// doc は ResolveDocs（doublestar.Glob）が返した実在確認済みのパスなので、
		// ここでの読み込み失敗は無視してよい欠落ではなく異常系として扱う。
		data, err := fs.ReadFile(fsys, doc)
		if err != nil {
			return nil, fmt.Errorf("docpaths: %s の読み込みに失敗しました: %w", doc, err)
		}

		var missing []string
		for _, cand := range c.extractCandidates(string(data)) {
			if c.ignore[cand.path] {
				continue
			}
			if !docutil.ExistsOrGlob(fsys, cand.path) {
				missing = append(missing, cand.path)
			}
		}

		if len(missing) > 0 {
			violations = append(violations, check.Violation{
				Summary: fmt.Sprintf("%s が存在しないパスを指しています:", doc),
				Files:   missing,
			})
		}
	}

	return violations, nil
}

// candidate はパス候補と、元のドキュメントでの出現行（1 始まり、出現順）。
type candidate struct {
	path  string
	lines []int
}

// extractCandidates はバッククォート内のパスらしき文字列を重複無く昇順で返す。除去後の
// 文字列は StripCodeFences と同じなので、判定に使う候補と出現位置の報告は同じ抽出から作られる。
func (c *Check) extractCandidates(content string) []candidate {
	stripped, origLines := docutil.StripCodeFencesLineMap(content)
	byPath := map[string]*candidate{}
	for _, m := range backtickRe.FindAllStringSubmatchIndex(stripped, -1) {
		p := stripped[m[2]:m[3]]
		if !c.pathLikeRe.MatchString(p) {
			continue
		}
		line := origLines[strings.Count(stripped[:m[2]], "\n")]
		cand, ok := byPath[p]
		if !ok {
			cand = &candidate{path: p}
			byPath[p] = cand
		}
		if n := len(cand.lines); n == 0 || cand.lines[n-1] != line {
			cand.lines = append(cand.lines, line)
		}
	}
	candidates := make([]candidate, 0, len(byPath))
	for _, cand := range byPath {
		candidates = append(candidates, *cand)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	return candidates
}
