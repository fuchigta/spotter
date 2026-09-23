// Package companionfiles は「触ったファイルに対する相方ファイルの存在」を検査する。
//
// doc-sync が「A を変更したなら B も変更されていること」を見るのに対し、こちらは
// 「A を触ったなら B が存在すること」を見る。まだ B が 1 度も無い（doc-sync の言う
// 「片方だけ変更」にすらならない）状態を捕まえるための検査。
package companionfiles

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

// templateVarPattern はテンプレート中の "{...}" を全て拾う。既知の変数以外が
// 混ざっていたら New で弾くための検出に使う。
var templateVarPattern = regexp.MustCompile(`\{[^{}]*\}`)

var knownTemplateVars = map[string]bool{
	"{dir}":  true,
	"{name}": true,
	"{stem}": true,
	"{ext}":  true,
	"{path}": true,
}

type rule struct {
	paths     string
	companion string
	reason    string
	exclude   []string
}

// Check は companion-files 検査の 1 インスタンス。
type Check struct {
	rules []rule
}

// New は config.CheckConfig から Check を組み立てる。rules の各項目はここで検証し、
// 不正な設定は起動時に検出する。
func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.Companions) == 0 {
		return nil, fmt.Errorf("companionfiles: companions には少なくとも 1 件のルールが必要です")
	}

	c := &Check{}
	for _, rc := range cc.Companions {
		if rc.Paths == "" || rc.Companion == "" || rc.Reason == "" {
			return nil, fmt.Errorf("companionfiles: companions には paths / companion / reason の全てが必要です")
		}
		if !doublestar.ValidatePattern(rc.Paths) {
			return nil, fmt.Errorf("companionfiles: companions: パターン %q が不正です", rc.Paths)
		}
		if err := validateTemplate(rc.Companion); err != nil {
			return nil, fmt.Errorf("companionfiles: companions: companion %q が不正です: %w", rc.Companion, err)
		}
		for _, p := range rc.Exclude {
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("companionfiles: companions: exclude のパターン %q が不正です", p)
			}
		}

		c.rules = append(c.rules, rule{
			paths:     rc.Paths,
			companion: rc.Companion,
			reason:    rc.Reason,
			exclude:   rc.Exclude,
		})
	}
	return c, nil
}

// validateTemplate はテンプレートの固定部分（変数展開前にそのまま書かれている部分）を
// 検証する。未知の変数、"/" 区切りで見て ".." そのものであるセグメント、先頭が "/"
// （絶対パス）はいずれも起動時エラーにする。展開後の値（例えば {dir} が実際のファイルパスに
// 由来する値）はここでは見ない。
func validateTemplate(tmpl string) error {
	for _, v := range templateVarPattern.FindAllString(tmpl, -1) {
		if !knownTemplateVars[v] {
			return fmt.Errorf("未知の変数 %q があります（使えるのは {dir}/{name}/{stem}/{ext}/{path}）", v)
		}
	}
	if strings.HasPrefix(tmpl, "/") {
		return fmt.Errorf("絶対パスは指定できません")
	}
	for _, seg := range strings.Split(tmpl, "/") {
		if seg == ".." {
			return fmt.Errorf("\"..\" セグメントは指定できません")
		}
	}
	return nil
}

// Granularity は範囲全体をまとめて 1 回で見る（後から相方ファイルを足すコミットを
// 積めば通るようにするため。doc-sync と同じ意味論）。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularitySquashed
}

// Run は ctx.Source の変更ファイルのうち paths に一致するものについて、テンプレートから
// 組み立てた相方ファイルが比較の終点（ctx.Source.Exists。staged はインデックス、range は
// to のツリー）に存在するかを確認する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	changed, err := ctx.Source.ChangedFiles()
	if err != nil {
		return nil, fmt.Errorf("companionfiles: 変更ファイルの取得に失敗しました: %w", err)
	}
	if len(changed) == 0 {
		return nil, nil
	}

	var violations []check.Violation
	for _, r := range c.rules {
		var missing []string
		for _, f := range changed {
			matched, err := doublestar.Match(r.paths, f)
			if err != nil {
				return nil, fmt.Errorf("companionfiles: %s の評価に失敗しました: %w", r.paths, err)
			}
			if !matched {
				continue
			}

			excluded, err := matchesAny(r.exclude, f)
			if err != nil {
				return nil, fmt.Errorf("companionfiles: exclude の評価に失敗しました: %w", err)
			}
			if excluded {
				continue
			}

			companion := renderTemplate(r.companion, f)
			exists, err := ctx.Source.Exists(companion)
			if err != nil {
				return nil, fmt.Errorf("companionfiles: %s の存在確認に失敗しました: %w", companion, err)
			}
			if !exists {
				missing = append(missing, fmt.Sprintf("%s → %s", f, companion))
			}
		}

		if len(missing) > 0 {
			violations = append(violations, check.Violation{
				Summary: r.reason + ":",
				Files:   missing,
			})
		}
	}

	return violations, nil
}

// renderTemplate は tmpl 中の {dir}/{name}/{stem}/{ext}/{path} を p から求めた値に展開する。
// {dir} がリポジトリ直下（p にディレクトリ部分が無い）の場合、そのまま埋めると
// 先頭に "/" が残ってしまう（"/foo.test.ts" のような不正な形）ため、展開後に
// path.Clean で正規化し、先頭の "/" を落とす。
func renderTemplate(tmpl, p string) string {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	base := path.Base(p)
	ext := path.Ext(p)
	name := strings.TrimSuffix(base, ext)
	stem := computeStem(base)

	replacer := strings.NewReplacer(
		"{dir}", dir,
		"{name}", name,
		"{stem}", stem,
		"{ext}", ext,
		"{path}", p,
	)
	rendered := replacer.Replace(tmpl)
	return strings.TrimPrefix(path.Clean(rendered), "/")
}

// computeStem はファイル名（ディレクトリを含まないベース名）から {stem} の値を求める。
//
//   - ドット始まりでなければ、最初の "." より前（無ければベース名そのまま）。
//     例: "001.up.sql" → "001"、"client.ts" → "client"
//   - ドット始まり（隠しファイル）なら、先頭のドットを除いた残りの中の最初の "." までを
//     先頭のドットごと含める（残りにドットが無ければベース名そのまま）。
//     例: ".env" → ".env"（残り "env" にドットが無い）、".env.local" → ".env"
//
// {name}/{ext}（path.Ext ベース、最後の "." で区切る）と違い、複合拡張子でも最初の意味の
// まとまりだけを取り出せる。
func computeStem(base string) string {
	if strings.HasPrefix(base, ".") {
		rest := base[1:]
		if i := strings.Index(rest, "."); i >= 0 {
			return base[:i+1]
		}
		return base
	}
	if i := strings.Index(base, "."); i >= 0 {
		return base[:i]
	}
	return base
}

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
