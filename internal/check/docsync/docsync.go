package docsync

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/diffutil"
	"github.com/fuchigta/spotter/internal/config"
)

type pair struct {
	paths   string
	doc     string
	when    *regexp.Regexp
	on      string
	docWhen *regexp.Regexp
	exclude []string
}

// Check は doc-sync 検査の 1 インスタンス。
type Check struct {
	pairs   []pair
	exclude []string
}

// New はパターンを検証してから Check を組み立てる。不正な設定は起動時に検出する。
func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.Pairs) == 0 {
		return nil, fmt.Errorf("docsync: pairs には少なくとも 1 件の対応が必要です")
	}

	exclude, err := validatePatterns("exclude", cc.Exclude)
	if err != nil {
		return nil, err
	}

	c := &Check{exclude: exclude}
	for _, p := range cc.Pairs {
		built, err := buildPair(p)
		if err != nil {
			return nil, err
		}
		c.pairs = append(c.pairs, built)
	}
	return c, nil
}

// buildPair は対応表（pairs）の 1 行分を検証し、pair を組み立てる。
func buildPair(p config.DocSyncPair) (pair, error) {
	if p.Paths == "" || p.Doc == "" {
		return pair{}, fmt.Errorf("docsync: pairs には paths と doc の両方が必要です")
	}
	if !doublestar.ValidatePattern(p.Paths) {
		return pair{}, fmt.Errorf("docsync: pairs: パターン %q が不正です", p.Paths)
	}

	when, err := compileDiffPattern("when", p.When)
	if err != nil {
		return pair{}, err
	}
	if p.On != "" {
		if p.When == "" {
			return pair{}, fmt.Errorf("docsync: pairs: on は when と併用してください")
		}
		if err := diffutil.ValidateOn(p.On); err != nil {
			return pair{}, fmt.Errorf("docsync: pairs: on: %w", err)
		}
	}

	docWhen, err := compileDiffPattern("doc_when", p.DocWhen)
	if err != nil {
		return pair{}, err
	}

	pairExclude, err := validatePatterns("pairs: exclude", p.Exclude)
	if err != nil {
		return pair{}, err
	}

	return pair{paths: p.Paths, doc: p.Doc, when: when, on: p.On, docWhen: docWhen, exclude: pairExclude}, nil
}

// compileDiffPattern は when/doc_when を正規表現としてコンパイルする。pattern が空なら
// 未指定として nil を返す。差分は複数行（diff --git / @@ ヘッダ等を含む）なので、
// "^"/"$" が行頭・行末に効くよう (?m) を自動で付与する。
func compileDiffPattern(field, pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(`(?m)` + pattern)
	if err != nil {
		return nil, fmt.Errorf("docsync: %s %q のコンパイルに失敗しました: %w", field, pattern, err)
	}
	return re, nil
}

// validatePatterns は exclude 系のパターン一覧を検証する。kind はエラーメッセージに
// 出すフィールド名（"exclude" または "pairs: exclude"）。
func validatePatterns(kind string, patterns []string) ([]string, error) {
	var out []string
	for _, pat := range patterns {
		if !doublestar.ValidatePattern(pat) {
			return nil, fmt.Errorf("docsync: %s: パターン %q が不正です", kind, pat)
		}
		out = append(out, pat)
	}
	return out, nil
}

// Granularity は範囲全体をまとめて 1 回で見る（後からドキュメントを直すコミットを
// 足せば通るようにするため）。
func (c *Check) Granularity() check.Granularity {
	return check.GranularitySquashed
}

// docGroup は同じ doc を対応先に持つ pairs をまとめて 1 件の Violation にするための
// 集計状態。
type docGroup struct {
	patterns    []string
	seenPattern map[string]bool
	files       map[string]bool
}

// Run は ctx.Source の変更内容を対応表と突き合わせ、コード側だけが変更（追加・変更・
// 削除）されドキュメントが一緒に変更されていない組を違反として返す。機能のコードを
// 消したのにドキュメントを直していない、という抜け穴を塞ぐため、削除も対象にする。
// 同じ doc を対応先に持つ複数の pairs は、1 件の Violation にまとめて報告する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	src := ctx.Source
	changed, err := src.ChangedFiles()
	if err != nil {
		return nil, fmt.Errorf("docsync: 変更ファイルの取得に失敗しました: %w", err)
	}
	deleted, err := src.DeletedFiles()
	if err != nil {
		return nil, fmt.Errorf("docsync: 削除ファイルの取得に失敗しました: %w", err)
	}
	if len(changed) == 0 && len(deleted) == 0 {
		return nil, nil
	}
	changedSet := make(map[string]bool, len(changed))
	for _, f := range changed {
		changedSet[f] = true
	}
	deletedSet := make(map[string]bool, len(deleted))
	for _, f := range deleted {
		deletedSet[f] = true
	}

	groups := map[string]*docGroup{}
	var docOrder []string

	for _, p := range c.pairs {
		satisfied, err := docSatisfied(src, p, changedSet, deletedSet)
		if err != nil {
			return nil, err
		}
		if satisfied {
			continue
		}

		hits, err := c.collectHits(src, p, changed, deleted)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			continue
		}

		g, ok := groups[p.doc]
		if !ok {
			g = &docGroup{seenPattern: map[string]bool{}, files: map[string]bool{}}
			groups[p.doc] = g
			docOrder = append(docOrder, p.doc)
		}
		if !g.seenPattern[p.paths] {
			g.seenPattern[p.paths] = true
			g.patterns = append(g.patterns, p.paths)
		}
		for _, h := range hits {
			g.files[h] = true
		}
	}

	if len(docOrder) == 0 {
		return nil, nil
	}

	violations := make([]check.Violation, 0, len(docOrder))
	for _, doc := range docOrder {
		g := groups[doc]

		files := make([]string, 0, len(g.files))
		for f := range g.files {
			files = append(files, f)
		}
		sort.Strings(files)

		violations = append(violations, check.Violation{
			Summary: fmt.Sprintf("%s を変更していますが、%s が一緒に入っていません:", strings.Join(g.patterns, ", "), doc),
			Files:   files,
			Target:  doc,
		})
	}

	return violations, nil
}

// ExemptTargets はスコープ付き免除（例: "Doc-Sync: skip[docs/foo.md] 理由"）で指定できる
// 対象の一覧を返す。pairs の doc を重複排除して集めたもの。check.ScopedExemptable の実装。
func (c *Check) ExemptTargets() []string {
	seen := make(map[string]bool, len(c.pairs))
	docs := make([]string, 0, len(c.pairs))
	for _, p := range c.pairs {
		if seen[p.doc] {
			continue
		}
		seen[p.doc] = true
		docs = append(docs, p.doc)
	}
	return docs
}

// collectHits は pair p の paths に一致する変更ファイル・削除ファイルのうち、トップレベルの
// exclude・pair 自身の exclude・when/on の条件をくぐり抜けたものを違反候補として返す。
// トップレベルの exclude は全 pairs に共通で効き、pair の exclude はこの pair だけに効く
// （どちらかに一致すれば対象から外れる）。削除されたファイルは check.DeletedLabel を
// 付けて区別する。
func (c *Check) collectHits(src check.Source, p pair, changed, deleted []string) ([]string, error) {
	var hits []string

	for _, f := range changed {
		hit, err := c.matchPairFile(src, p, f, false)
		if err != nil {
			return nil, err
		}
		if hit != "" {
			hits = append(hits, hit)
		}
	}
	for _, f := range deleted {
		hit, err := c.matchPairFile(src, p, f, true)
		if err != nil {
			return nil, err
		}
		if hit != "" {
			hits = append(hits, hit)
		}
	}

	return hits, nil
}

// matchPairFile は 1 ファイル f が pair p の違反候補かどうかを判定する。トップレベルの
// exclude・pair 自身の exclude・paths・when の条件を順に確かめ、全て通れば label
// （削除なら check.DeletedLabel を付けたもの）を返す。対象外なら空文字を返す。
func (c *Check) matchPairFile(src check.Source, p pair, f string, isDeleted bool) (string, error) {
	excluded, err := matchesAny(c.exclude, f)
	if err != nil {
		return "", fmt.Errorf("docsync: exclude の評価に失敗しました: %w", err)
	}
	if excluded {
		return "", nil
	}

	pairExcluded, err := matchesAny(p.exclude, f)
	if err != nil {
		return "", fmt.Errorf("docsync: pairs: exclude の評価に失敗しました: %w", err)
	}
	if pairExcluded {
		return "", nil
	}

	matched, err := doublestar.Match(p.paths, f)
	if err != nil {
		return "", fmt.Errorf("docsync: %s の評価に失敗しました: %w", p.paths, err)
	}
	if !matched {
		return "", nil
	}

	if p.when != nil {
		diff, err := src.DiffLines(f)
		if err != nil {
			return "", fmt.Errorf("docsync: %s の差分取得に失敗しました: %w", f, err)
		}
		if !whenMatches(p, diff) {
			return "", nil
		}
	}

	if isDeleted {
		return check.DeletedLabel(f), nil
	}
	return f, nil
}

// docSatisfied は pair p の doc 側の条件が既に満たされているかを判定する。
//   - doc が削除されていれば、ドキュメント側も変更されたとみなして常に満たす
//   - doc が変更されていれば、docWhen が無ければ満たす。docWhen があれば doc の差分に
//     一致した場合だけ満たす（形だけの更新を捕まえるオプトイン）
//   - doc が変更も削除もされていなければ満たさない
func docSatisfied(src check.Source, p pair, changedSet, deletedSet map[string]bool) (bool, error) {
	if deletedSet[p.doc] {
		return true, nil
	}
	if !changedSet[p.doc] {
		return false, nil
	}
	if p.docWhen == nil {
		return true, nil
	}
	diff, err := src.DiffLines(p.doc)
	if err != nil {
		return false, fmt.Errorf("docsync: %s の差分取得に失敗しました: %w", p.doc, err)
	}
	return p.docWhen.MatchString(diff), nil
}

// whenMatches は p.when を diff に当てる。p.on が指定されていれば、diffutil.ParseLines で
// 分けた追加行/削除行の中身（先頭の +/- を落としたもの、ファイルヘッダ行は除外済み）に
// 1 行ずつ当てる。省略時は差分全体（diff --git/@@ ヘッダを含む）に当てる。
func whenMatches(p pair, diff string) bool {
	if p.on == "" {
		return p.when.MatchString(diff)
	}

	for _, ln := range diffutil.LinesOn(diff, p.on) {
		if p.when.MatchString(ln.Text) {
			return true
		}
	}
	return false
}

// matchesAny は f が patterns のいずれかに一致するかを判定する。
// テストファイルは自動では除外されない。Go プロジェクトでテストファイルを除外したい
// 場合は、checks.<key>.exclude に明示的に "**/*_test.go" を指定すること。
func matchesAny(patterns []string, f string) (bool, error) {
	for _, pat := range patterns {
		ok, err := doublestar.Match(pat, f)
		if err != nil {
			return false, fmt.Errorf("パターン %q が不正です: %w", pat, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
