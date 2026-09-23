// Package docsync はドキュメントの陳腐化を防ぐ検査を実装する。
package docsync

import (
	"fmt"
	"regexp"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

type pair struct {
	paths string
	doc   string
	when  *regexp.Regexp
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
		c.pairs = append(c.pairs, pair{paths: p.Paths, doc: p.Doc, when: when})
	}
	return c, nil
}

// Granularity は範囲全体をまとめて 1 回で見る（後からドキュメントを直すコミットを
// 足せば通るようにするため）。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularitySquashed
}

// Run は ctx.Source の変更内容を対応表と突き合わせ、コードだけが変更されドキュメントが
// 一緒に変更されていない組を違反として返す。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	src := ctx.Source
	changed, err := src.ChangedFiles()
	if err != nil {
		return nil, fmt.Errorf("docsync: 変更ファイルの取得に失敗しました: %w", err)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	changedSet := make(map[string]bool, len(changed))
	for _, f := range changed {
		changedSet[f] = true
	}

	var violations []check.Violation
	for _, p := range c.pairs {
		if changedSet[p.doc] {
			// ドキュメント側も一緒に入っているなら、この行は満たされている。
			continue
		}

		var hits []string
		for _, f := range changed {
			excluded, err := matchesAny(c.exclude, f)
			if err != nil {
				return nil, fmt.Errorf("docsync: exclude の評価に失敗しました: %w", err)
			}
			if excluded {
				continue
			}
			matched, err := doublestar.Match(p.paths, f)
			if err != nil {
				return nil, fmt.Errorf("docsync: %s の評価に失敗しました: %w", p.paths, err)
			}
			if !matched {
				continue
			}
			if p.when != nil {
				diff, err := src.DiffLines(f)
				if err != nil {
					return nil, fmt.Errorf("docsync: %s の差分取得に失敗しました: %w", f, err)
				}
				if !p.when.MatchString(diff) {
					continue
				}
			}
			hits = append(hits, f)
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
