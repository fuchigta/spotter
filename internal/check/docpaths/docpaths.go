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
	"os"
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
	ignore     map[string]bool
	pathLikeRe *regexp.Regexp
}

// New は config.CheckConfig から Check を組み立てる。
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

	return &Check{docs: cc.Docs, ignore: ignore, pathLikeRe: re}, nil
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
	return regexp.Compile(`^(` + strings.Join(parts, "|") + `)/`)
}

// Granularity は現在の作業ツリーを 1 回だけ見る。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityWorktree
}

// Run は対象ドキュメントからバッククォート内のパス候補を抜き出し、実在を確認する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	fsys := os.DirFS(ctx.Root)

	docs, err := docutil.ResolveDocs(fsys, c.docs)
	if err != nil {
		return nil, fmt.Errorf("docpaths: 対象ドキュメントの解決に失敗しました: %w", err)
	}

	var violations []check.Violation
	for _, doc := range docs {
		// doc は resolveDocs（doublestar.Glob）が返した実在確認済みのパスなので、
		// ここでの読み込み失敗は無視してよい欠落ではなく異常系として扱う。
		data, err := fs.ReadFile(fsys, doc)
		if err != nil {
			return nil, fmt.Errorf("docpaths: %s の読み込みに失敗しました: %w", doc, err)
		}

		var missing []string
		for _, p := range c.extractCandidates(string(data)) {
			if c.ignore[p] {
				continue
			}
			if !docutil.ExistsOrGlob(fsys, p) {
				missing = append(missing, p)
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

// extractCandidates はバッククォート内のパスらしき文字列を重複無く昇順で返す。
func (c *Check) extractCandidates(content string) []string {
	seen := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(docutil.StripCodeFences(content), -1) {
		p := m[1]
		if c.pathLikeRe.MatchString(p) {
			seen[p] = true
		}
	}
	candidates := make([]string, 0, len(seen))
	for p := range seen {
		candidates = append(candidates, p)
	}
	sort.Strings(candidates)
	return candidates
}
