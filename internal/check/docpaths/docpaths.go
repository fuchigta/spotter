// Package docpaths はドキュメントが名指ししているコードのパスが実在するかを調べる検査
// （scripts/check-doc-paths.sh 相当）を実装する。
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

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

// backtickRe はバッククォートで囲まれた中身を拾う。地の文のそれらしい文字列まで拾うと
// 誤検知だらけになるため、これだけを候補とする。
var backtickRe = regexp.MustCompile("`([^`]+)`")

// fenceOpenRe はコードフェンス（``` / ~~~、3 つ以上）の開始行を検出する。
var fenceOpenRe = regexp.MustCompile("^(`{3,}|~{3,})")

// Check は doc-paths 検査の 1 インスタンス。
type Check struct {
	docs   []string
	ignore map[string]bool
	// pathLikeRe は path_prefixes から組み立てた正規表現。path_prefixes 未設定なら nil で、
	// その場合は候補が 1 つも見つからない。
	pathLikeRe *regexp.Regexp
}

// New は config.CheckConfig から Check を組み立てる。
func New(cc config.CheckConfig) (*Check, error) {
	ignore := make(map[string]bool, len(cc.Ignore))
	for _, p := range cc.Ignore {
		if p == "" {
			continue
		}
		ignore[p] = true
	}

	var pathLikeRe *regexp.Regexp
	if len(cc.PathPrefixes) > 0 {
		re, err := compilePathLikeRe(cc.PathPrefixes)
		if err != nil {
			return nil, fmt.Errorf("docpaths: path_prefixes のコンパイルに失敗しました: %w", err)
		}
		pathLikeRe = re
	}

	return &Check{docs: cc.Docs, ignore: ignore, pathLikeRe: pathLikeRe}, nil
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

	docs, err := c.resolveDocs(fsys)
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
			// 不正な glob 表記（地の文にたまたま "[" 等が混ざった場合）は、他の検査まで
			// 巻き込んで実行を止めないよう「存在しない」として扱う。
			if !existsOrGlob(fsys, p) {
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

// resolveDocs は対象ドキュメントの一覧を返す。docs は doublestar（"**" 対応）のパターン列
// として展開する。省略時は "**/*.md" 1 件を使う（".git" 配下は除く）。
func (c *Check) resolveDocs(fsys fs.FS) ([]string, error) {
	patterns := c.docs
	if len(patterns) == 0 {
		patterns = []string{"**/*.md"}
	}

	seen := map[string]bool{}
	for _, pattern := range patterns {
		matches, err := doublestar.Glob(fsys, pattern)
		if err != nil {
			return nil, fmt.Errorf("パターン %q が不正です: %w", pattern, err)
		}
		for _, m := range matches {
			if m == ".git" || strings.HasPrefix(m, ".git/") {
				continue
			}
			seen[m] = true
		}
	}

	docs := make([]string, 0, len(seen))
	for m := range seen {
		docs = append(docs, m)
	}
	sort.Strings(docs)
	return docs, nil
}

// extractCandidates はバッククォート内のパスらしき文字列を重複無く昇順で返す。
// path_prefixes が未設定（pathLikeRe が nil）なら常に空を返す。
func (c *Check) extractCandidates(content string) []string {
	if c.pathLikeRe == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(stripCodeFences(content), -1) {
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

// stripCodeFences はフェンスドコードブロック（``` / ~~~）の中身を丸ごと取り除く。
// コードブロック内のバッククォート（例: シェルスクリプト例に含まれるバッククォート）を
// 数えてしまうと、それ以降のインラインスパンの対応が丸ごとずれて誤抽出・抽出漏れの
// 原因になるため、抽出前に必ず除去する。
func stripCodeFences(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	var fenceChar byte

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !inFence {
			if m := fenceOpenRe.FindString(trimmed); m != "" {
				inFence = true
				fenceChar = m[0]
				continue
			}
			out = append(out, line)
			continue
		}

		if len(trimmed) > 0 && trimmed[0] == fenceChar {
			run := 0
			for run < len(trimmed) && trimmed[run] == fenceChar {
				run++
			}
			if run >= 3 && strings.TrimSpace(trimmed[run:]) == "" {
				inFence = false
			}
		}
		// フェンス内の行は開始・終了行を含めて丸ごと捨てる。
	}
	return strings.Join(out, "\n")
}

// existsOrGlob は p がリポジトリ内に実在するかを調べる。"*" を含む場合は doublestar
// パターン（"**" 対応）として扱い、1 つ以上に一致すればよい（グロブ表記の例示）。
// 不正な glob 表記は「存在しない」として扱う（地の文にたまたま "[" 等が混ざっただけで
// 検査全体が異常終了しないようにするため）。
func existsOrGlob(fsys fs.FS, p string) bool {
	if strings.Contains(p, "*") {
		matches, err := doublestar.Glob(fsys, p)
		if err != nil {
			return false
		}
		return len(matches) > 0
	}
	_, err := fs.Stat(fsys, p)
	return err == nil
}
