// Package doclinks は Markdown のリンク記法（[text](target) / ![alt](target) /
// 参照定義 [label]: target）が指すファイルの実在を確認する検査を実装する。
//
// doc-paths が「地の文に紛れたパスらしき言及」を拾う（誤検知を避けるため path_prefixes で
// 強く絞り込む必要がある）のに対し、こちらは Markdown のリンク記法という明確な構文から
// 拾うため絞り込みが要らない。ネットワークに出る外部 URL の到達性確認は行わない
// （遅く不安定で CI の偽陽性の原因になるため）。
package doclinks

import (
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docutil"
	"github.com/fuchigta/spotter/internal/config"
)

// inlineLinkPattern は "[text](target)" / "![alt](target)"（任意でタイトル付き）を拾う。
// target は "<...>" で囲まれた形も許す。
var inlineLinkPattern = regexp.MustCompile(`!?\[[^\]]*\]\(\s*(<[^>]*>|[^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)`)

// refDefPattern は参照定義 "[label]: target" を拾う（行頭、最大 3 個のインデントを許す）。
var refDefPattern = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(<[^>]*>|\S+)`)

// schemePattern は "scheme:" で始まる文字列（RFC3986 の scheme 相当）を検出する。
// mailto: / ftp: のような URL スキームを外部リンクとして除外するために使う。
var schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// headingPattern は ATX 見出し（"# foo"）を拾う。
var headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

type linkOccurrence struct {
	raw  string
	line int
}

// Check は doc-links 検査の 1 インスタンス。
type Check struct {
	docs         []string
	ignore       map[string]bool
	checkAnchors bool
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
	return &Check{docs: cc.Docs, ignore: ignore, checkAnchors: cc.CheckAnchors}, nil
}

// Granularity は現在の作業ツリーを 1 回だけ見る。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityWorktree
}

// Run は対象ドキュメントからリンク記法を抜き出し、リンク先の実在を確認する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	fsys := ctx.FS

	docs, err := docutil.ResolveDocs(fsys, c.docs)
	if err != nil {
		return nil, fmt.Errorf("doclinks: 対象ドキュメントの解決に失敗しました: %w", err)
	}

	headingCache := map[string]map[string]bool{}

	var violations []check.Violation
	for _, doc := range docs {
		data, err := fs.ReadFile(fsys, doc)
		if err != nil {
			return nil, fmt.Errorf("doclinks: %s の読み込みに失敗しました: %w", doc, err)
		}

		broken, err := c.checkDoc(fsys, doc, string(data), headingCache)
		if err != nil {
			return nil, err
		}

		if len(broken) > 0 {
			violations = append(violations, check.Violation{
				Summary: fmt.Sprintf("%s がリンク切れを含んでいます:", doc),
				Files:   broken,
			})
		}
	}

	return violations, nil
}

// checkDoc は 1 ドキュメント分のリンク切れを検出し、"raw:line → 詳細" の形の文字列一覧を返す
// （line 昇順）。同じリンク先（raw の完全一致）は最初に出現した行だけを報告する。
func (c *Check) checkDoc(fsys fs.FS, doc, content string, headingCache map[string]map[string]bool) ([]string, error) {
	occurrences := extractLinks(content)

	type broken struct {
		line   int
		raw    string
		detail string
	}
	seen := map[string]bool{}
	var brokenList []broken

	for _, occ := range occurrences {
		target, ok := unwrapTarget(occ.raw)
		if !ok || c.ignore[occ.raw] || isExternal(target) {
			continue
		}
		if seen[occ.raw] {
			continue
		}

		filePart, anchor := splitAnchor(target)

		resolved, escapesRoot := resolveFilePath(doc, filePart)
		if escapesRoot {
			seen[occ.raw] = true
			brokenList = append(brokenList, broken{line: occ.line, raw: occ.raw, detail: "はリポジトリの外を指しています"})
			continue
		}

		if filePart != "" && !docutil.ExistsOrGlob(fsys, resolved) {
			seen[occ.raw] = true
			brokenList = append(brokenList, broken{line: occ.line, raw: occ.raw, detail: fmt.Sprintf("%s が存在しません", resolved)})
			continue
		}

		if c.checkAnchors && anchor != "" {
			headings, err := c.headingsFor(fsys, resolved, headingCache)
			if err != nil {
				return nil, err
			}
			if !headings[anchor] {
				seen[occ.raw] = true
				brokenList = append(brokenList, broken{line: occ.line, raw: occ.raw, detail: fmt.Sprintf("見出し %q が %s に見つかりません", anchor, resolved)})
			}
		}
	}

	sort.Slice(brokenList, func(i, j int) bool { return brokenList[i].line < brokenList[j].line })

	files := make([]string, 0, len(brokenList))
	for _, b := range brokenList {
		files = append(files, fmt.Sprintf("%s:%d → %s", b.raw, b.line, b.detail))
	}
	return files, nil
}

func (c *Check) headingsFor(fsys fs.FS, doc string, cache map[string]map[string]bool) (map[string]bool, error) {
	if h, ok := cache[doc]; ok {
		return h, nil
	}
	data, err := fs.ReadFile(fsys, doc)
	if err != nil {
		// アンカー検証の対象ファイルが読めない（実在確認は別途済んでいるはずだが、
		// 念のため）場合は見出し無しとして扱い、アンカー不一致として報告させる。
		cache[doc] = map[string]bool{}
		return cache[doc], nil
	}
	h := extractHeadingAnchors(string(data))
	cache[doc] = h
	return h, nil
}

// inlineCodeSpanPattern はインラインコードスパン（バッククォートで囲まれた区間）を検出する。
// ドキュメントがリンク記法そのものを例示する際（例: このファイルの `[text](target)`）に
// 本物のリンクと誤認しないよう、リンク抽出前にこの中身を潰す。
var inlineCodeSpanPattern = regexp.MustCompile("`[^`\n]*`")

// maskInlineCode は line 中のインラインコードスパンを同じ長さの空白に置き換える
// （行番号・カラム位置をずらさないため、削除ではなく空白化する）。
func maskInlineCode(line string) string {
	return inlineCodeSpanPattern.ReplaceAllStringFunc(line, func(s string) string {
		return strings.Repeat(" ", len(s))
	})
}

// extractLinks は content（フェンス除去前の生のドキュメント）からリンク・画像・参照定義の
// 出現を行番号付きで抜き出す。コードフェンス内の行、およびインラインコードスパンの中身は
// 対象外にする。行番号は元のドキュメントとそろえる必要があるため、行を削らず空行に置き換える
// StripCodeFencesKeepLines を使う。
func extractLinks(content string) []linkOccurrence {
	stripped := docutil.StripCodeFencesKeepLines(content)
	lines := strings.Split(stripped, "\n")

	var occurrences []linkOccurrence
	for i, line := range lines {
		lineNo := i + 1
		masked := maskInlineCode(line)
		for _, m := range inlineLinkPattern.FindAllStringSubmatch(masked, -1) {
			occurrences = append(occurrences, linkOccurrence{raw: m[1], line: lineNo})
		}
		if m := refDefPattern.FindStringSubmatch(masked); m != nil {
			occurrences = append(occurrences, linkOccurrence{raw: m[1], line: lineNo})
		}
	}
	return occurrences
}

// unwrapTarget は "<...>" で囲まれた形を剥がす。空になった場合は ok=false（"[text]()" の
// ような空リンクは対象外）。
func unwrapTarget(raw string) (string, bool) {
	t := raw
	if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") && len(t) >= 2 {
		t = t[1 : len(t)-1]
	}
	if t == "" {
		return "", false
	}
	return t, true
}

// isExternal は target がスキーム付き（http:, mailto: 等）またはプロトコル相対（//host/...）
// かどうかを返す。これらは到達性確認をしない（この検査の対象外）。
func isExternal(target string) bool {
	if strings.HasPrefix(target, "//") {
		return true
	}
	return schemePattern.MatchString(target)
}

// splitAnchor は target を「ファイル部分」と「アンカー部分（# を除く）」に分ける。
func splitAnchor(target string) (filePart, anchor string) {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		return target[:i], target[i+1:]
	}
	return target, ""
}

// resolveFilePath は filePart を doc からの相対パスとして解決し、リポジトリルートからの
// 相対パスにする。filePart が空（同一ドキュメント内のアンカーのみ）なら doc 自身を返す。
// 先頭が "/" ならリポジトリルート相対として扱う。解決結果がリポジトリの外に出る場合は
// escapesRoot=true を返す。
func resolveFilePath(doc, filePart string) (resolved string, escapesRoot bool) {
	if filePart == "" {
		return doc, false
	}

	decoded, err := url.PathUnescape(filePart)
	if err != nil {
		decoded = filePart
	}

	var joined string
	if strings.HasPrefix(decoded, "/") {
		joined = path.Clean(strings.TrimPrefix(decoded, "/"))
	} else {
		joined = path.Join(path.Dir(doc), decoded)
	}

	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", true
	}
	return joined, false
}

// extractHeadingAnchors はドキュメントの ATX 見出しから、GitHub 準拠のスラグ集合を作る。
// コードフェンス内の "#" はコメント等であり見出しではないため除外する。
func extractHeadingAnchors(content string) map[string]bool {
	stripped := docutil.StripCodeFences(content)
	counts := map[string]int{}
	anchors := map[string]bool{}

	for _, line := range strings.Split(stripped, "\n") {
		m := headingPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimRight(strings.TrimSpace(m[2]), " \t#")
		base := slugify(text)
		n := counts[base]
		counts[base] = n + 1
		slug := base
		if n > 0 {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		anchors[slug] = true
	}
	return anchors
}

// slugify は見出しのテキストから GitHub 準拠（近似）のアンカースラグを作る：小文字化、
// 空白をハイフンに、英数字・非 ASCII 文字・ハイフン・アンダースコア以外を除去する
// （非 ASCII 文字、例えば日本語見出しはそのまま保持する）。同名の見出しに対する連番
// （-1, -2, ...）は呼び出し側（extractHeadingAnchors）が付与する。
func slugify(heading string) string {
	lower := strings.ToLower(heading)
	var b strings.Builder
	for _, r := range lower {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= utf8.RuneSelf:
			// 非 ASCII 文字は GitHub 同様に保持する。
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			// ASCII の記号（. , ! ? など）は除去する。
		}
	}
	return b.String()
}
