// Package docsync はドキュメントの陳腐化を防ぐ検査を実装する。
package docsync

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/diffutil"
	"github.com/fuchigta/spotter/internal/config"
)

const (
	onAdded   = "added"
	onRemoved = "removed"
)

type pair struct {
	paths   string
	doc     string
	when    *regexp.Regexp
	on      string
	docWhen *regexp.Regexp
}

// Check は doc-sync 検査の 1 インスタンス。
type Check struct {
	pairs   []pair
	exclude []string
}

// New は config.CheckConfig から Check を組み立てる。パターンはここで検証し、
// 不正な設定は起動時に検出する。
func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.Pairs) == 0 {
		return nil, fmt.Errorf("docsync: pairs には少なくとも 1 件の対応が必要です")
	}

	c := &Check{}

	for _, pat := range cc.Exclude {
		if !doublestar.ValidatePattern(pat) {
			return nil, fmt.Errorf("docsync: exclude: パターン %q が不正です", pat)
		}
		c.exclude = append(c.exclude, pat)
	}

	for _, p := range cc.Pairs {
		if p.Paths == "" || p.Doc == "" {
			return nil, fmt.Errorf("docsync: pairs には paths と doc の両方が必要です")
		}
		if !doublestar.ValidatePattern(p.Paths) {
			return nil, fmt.Errorf("docsync: pairs: パターン %q が不正です", p.Paths)
		}
		var when *regexp.Regexp
		if p.When != "" {
			// 差分は複数行（diff --git / @@ ヘッダ等を含む）なので、"^"/"$" が行頭・行末に
			// 効くよう (?m) を自動で付与する。
			re, err := regexp.Compile(`(?m)` + p.When)
			if err != nil {
				return nil, fmt.Errorf("docsync: when %q のコンパイルに失敗しました: %w", p.When, err)
			}
			when = re
		}
		if p.On != "" {
			if p.When == "" {
				return nil, fmt.Errorf("docsync: pairs: on は when と併用してください")
			}
			if p.On != onAdded && p.On != onRemoved {
				return nil, fmt.Errorf("docsync: pairs: on %q は未対応です（added | removed）", p.On)
			}
		}
		var docWhen *regexp.Regexp
		if p.DocWhen != "" {
			re, err := regexp.Compile(`(?m)` + p.DocWhen)
			if err != nil {
				return nil, fmt.Errorf("docsync: doc_when %q のコンパイルに失敗しました: %w", p.DocWhen, err)
			}
			docWhen = re
		}
		c.pairs = append(c.pairs, pair{paths: p.Paths, doc: p.Doc, when: when, on: p.On, docWhen: docWhen})
	}
	return c, nil
}

// Granularity は範囲全体をまとめて 1 回で見る（後からドキュメントを直すコミットを
// 足せば通るようにするため）。checks 側からは上書きできない。
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
			Summary: fmt.Sprintf("%s を変更していますが、%s が一緒に入っていません:", joinPatterns(g.patterns), doc),
			Files:   files,
		})
	}

	return violations, nil
}

// joinPatterns は patterns を ", " で連結する（patterns は 1 件以上ある前提）。
func joinPatterns(patterns []string) string {
	out := patterns[0]
	for _, p := range patterns[1:] {
		out += ", " + p
	}
	return out
}

// collectHits は pair p の paths に一致する変更ファイル・削除ファイルのうち、exclude と
// when/on の条件をくぐり抜けたものを違反候補として返す。削除されたファイルは
// check.DeletedLabel を付けて区別する。
func (c *Check) collectHits(src check.Source, p pair, changed, deleted []string) ([]string, error) {
	var hits []string

	collect := func(files []string, isDeleted bool) error {
		for _, f := range files {
			excluded, err := matchesAny(c.exclude, f)
			if err != nil {
				return fmt.Errorf("docsync: exclude の評価に失敗しました: %w", err)
			}
			if excluded {
				continue
			}
			matched, err := doublestar.Match(p.paths, f)
			if err != nil {
				return fmt.Errorf("docsync: %s の評価に失敗しました: %w", p.paths, err)
			}
			if !matched {
				continue
			}
			if p.when != nil {
				diff, err := src.DiffLines(f)
				if err != nil {
					return fmt.Errorf("docsync: %s の差分取得に失敗しました: %w", f, err)
				}
				if !whenMatches(p, diff) {
					continue
				}
			}
			label := f
			if isDeleted {
				label = check.DeletedLabel(f)
			}
			hits = append(hits, label)
		}
		return nil
	}

	if err := collect(changed, false); err != nil {
		return nil, err
	}
	if err := collect(deleted, true); err != nil {
		return nil, err
	}

	return hits, nil
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
// 1 行ずつ当てる。省略時は従来どおり差分全体（diff --git/@@ ヘッダを含む）に当てる
// （互換維持）。
func whenMatches(p pair, diff string) bool {
	if p.on == "" {
		return p.when.MatchString(diff)
	}

	added, removed := diffutil.ParseLines(diff)
	lines := added
	if p.on == onRemoved {
		lines = removed
	}
	for _, ln := range lines {
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
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
