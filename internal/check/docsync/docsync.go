// Package docsync はドキュメントの陳腐化を防ぐ検査を実装する。
package docsync

import (
	"fmt"
	"regexp"

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
	paths string
	doc   string
	when  *regexp.Regexp
	on    string
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
		c.pairs = append(c.pairs, pair{paths: p.Paths, doc: p.Doc, when: when, on: p.On})
	}
	return c, nil
}

// Granularity は範囲全体をまとめて 1 回で見る（後からドキュメントを直すコミットを
// 足せば通るようにするため）。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularitySquashed
}

// deletedMarker は違反表示上、削除されたファイルだと分かるように付けるラベル。
const deletedMarker = "（削除）"

// Run は ctx.Source の変更内容を対応表と突き合わせ、コード側だけが変更（追加・変更・
// 削除）されドキュメントが一緒に変更されていない組を違反として返す。機能のコードを
// 消したのにドキュメントを直していない、という抜け穴を塞ぐため、削除も対象にする。
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

	var violations []check.Violation
	for _, p := range c.pairs {
		if changedSet[p.doc] || deletedSet[p.doc] {
			// ドキュメント側も一緒に変更（削除も含む）されているなら、この行は満たされている。
			continue
		}

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
					label = f + deletedMarker
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

		if len(hits) > 0 {
			violations = append(violations, check.Violation{
				Summary: fmt.Sprintf("%s を変更していますが、%s が一緒に入っていません:", p.paths, p.doc),
				Files:   hits,
			})
		}
	}

	return violations, nil
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
