// Package companionfiles は「触ったファイルに対する相方ファイルの存在」を検査する。
//
// doc-sync が「A を変更したなら B も変更されていること」を見るのに対し、こちらは
// 「A を触ったなら B が存在すること」を見る。まだ B が 1 度も無い（doc-sync の言う
// 「片方だけ変更」にすらならない）状態を捕まえるための検査。あわせて、A が削除・改名
// された後も B だけが残っている「孤児」も検出する。
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

// nameLikeTemplateVars は本体ファイルと相方ファイルが 1:1 に対応することを示す変数。
// このいずれも含まないテンプレート（{dir} だけで組み立てる共有型、例 {dir}/README.md）は、
// 複数の本体ファイルが同じ相方を指しうるため、孤児検出の対象から外す
// （本体が 1 つ削除されても、他の本体がまだ相方を必要としている可能性がある）。
var nameLikeTemplateVars = []string{"{name}", "{stem}", "{path}"}

type rule struct {
	paths      string
	companions []string
	reason     string
	exclude    []string
}

// matches は f が r.paths に一致し、かつ r.exclude のいずれにも一致しないかを判定する。
// runMissing/runOrphans はどちらも「paths に一致 → exclude に一致しなければ対象」という
// 同じ形の絞り込みを行うため、ここにまとめている。
func (r rule) matches(f string) (bool, error) {
	matched, err := doublestar.Match(r.paths, f)
	if err != nil {
		return false, fmt.Errorf("%s の評価に失敗しました: %w", r.paths, err)
	}
	if !matched {
		return false, nil
	}

	excluded, err := matchesAny(r.exclude, f)
	if err != nil {
		return false, fmt.Errorf("exclude の評価に失敗しました: %w", err)
	}
	return !excluded, nil
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
		if rc.Paths == "" || len(rc.Companion) == 0 || rc.Reason == "" {
			return nil, fmt.Errorf("companionfiles: companions には paths / companion / reason の全てが必要です")
		}
		if !doublestar.ValidatePattern(rc.Paths) {
			return nil, fmt.Errorf("companionfiles: companions: パターン %q が不正です", rc.Paths)
		}
		for _, tmpl := range rc.Companion {
			if tmpl == "" {
				return nil, fmt.Errorf("companionfiles: companions: companion に空文字は指定できません")
			}
			if err := validateTemplate(tmpl); err != nil {
				return nil, fmt.Errorf("companionfiles: companions: companion %q が不正です: %w", tmpl, err)
			}
		}
		for _, p := range rc.Exclude {
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("companionfiles: companions: exclude のパターン %q が不正です", p)
			}
		}

		c.rules = append(c.rules, rule{
			paths:      rc.Paths,
			companions: []string(rc.Companion),
			reason:     rc.Reason,
			exclude:    rc.Exclude,
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

// Granularity は範囲全体をまとめて 1 回で見る（後から相方ファイルを足す・孤児を消す
// コミットを積めば通るようにするため。doc-sync と同じ意味論）。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularitySquashed
}

// Run は 2 種類の違反を見る。
//  1. 変更ファイルのうち paths に一致するものについて、相方が比較の終点に存在しない
//     （テストを作り忘れた、など）
//  2. 削除・改名されたファイルのうち paths に一致するものについて、相方が比較の終点に
//     まだ存在する（本体だけ消して相方を消し忘れた、リネームで追従し忘れた、など）
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	var violations []check.Violation

	missing, err := c.runMissing(ctx)
	if err != nil {
		return nil, err
	}
	violations = append(violations, missing...)

	orphaned, err := c.runOrphans(ctx)
	if err != nil {
		return nil, err
	}
	violations = append(violations, orphaned...)

	return violations, nil
}

// runMissing は ctx.Source の変更ファイルのうち paths に一致するものについて、テンプレートから
// 組み立てた相方ファイルの候補が 1 つも ctx.Source.Exists（比較の終点）に無ければ違反にする。
func (c *Check) runMissing(ctx check.Context) ([]check.Violation, error) {
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
			match, err := r.matches(f)
			if err != nil {
				return nil, fmt.Errorf("companionfiles: %w", err)
			}
			if !match {
				continue
			}

			candidates := renderTemplates(r.companions, f)
			found, err := existsAny(ctx.Source, candidates)
			if err != nil {
				return nil, fmt.Errorf("companionfiles: %s の存在確認に失敗しました: %w", f, err)
			}
			if !found {
				missing = append(missing, fmt.Sprintf("%s → %s", f, strings.Join(candidates, ", ")))
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

// runOrphans は ctx.Source.DeletedFiles()（削除・改名元）のうち paths に一致するものについて、
// 「本体と相方が 1:1 に対応する」テンプレート（{name}/{stem}/{path} のいずれかを含むもの）
// だけを使って相方候補を組み立て、比較の終点にまだ存在するものを違反にする。
// {dir} だけの共有型テンプレートはここでは扱わない（対象外の理由は nameLikeTemplateVars を参照）。
func (c *Check) runOrphans(ctx check.Context) ([]check.Violation, error) {
	deleted, err := ctx.Source.DeletedFiles()
	if err != nil {
		return nil, fmt.Errorf("companionfiles: 削除ファイルの取得に失敗しました: %w", err)
	}
	if len(deleted) == 0 {
		return nil, nil
	}

	var violations []check.Violation
	for _, r := range c.rules {
		nameLike := nameLikeCandidates(r.companions)
		if len(nameLike) == 0 {
			continue
		}

		var orphaned []string
		for _, f := range deleted {
			match, err := r.matches(f)
			if err != nil {
				return nil, fmt.Errorf("companionfiles: %w", err)
			}
			if !match {
				continue
			}

			for _, companion := range renderTemplates(nameLike, f) {
				exists, err := ctx.Source.Exists(companion)
				if err != nil {
					return nil, fmt.Errorf("companionfiles: %s の存在確認に失敗しました: %w", companion, err)
				}
				if exists {
					orphaned = append(orphaned, fmt.Sprintf("%s → %s", f, companion))
				}
			}
		}

		if len(orphaned) > 0 {
			violations = append(violations, check.Violation{
				Summary: r.reason + "（本体が削除・改名されたのに相方が残っています）:",
				Files:   orphaned,
			})
		}
	}

	return violations, nil
}

// nameLikeCandidates は companions のうち、本体ファイルと相方ファイルが 1:1 に対応する
// テンプレート（{name}/{stem}/{path} のいずれかを含むもの）だけを返す。
func nameLikeCandidates(companions []string) []string {
	var out []string
	for _, tmpl := range companions {
		for _, v := range nameLikeTemplateVars {
			if strings.Contains(tmpl, v) {
				out = append(out, tmpl)
				break
			}
		}
	}
	return out
}

// existsAny は candidates のいずれかが src.Exists を満たすかを返す。
func existsAny(src check.Source, candidates []string) (bool, error) {
	for _, cand := range candidates {
		ok, err := src.Exists(cand)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// renderTemplates は tmpls の各テンプレートを p から renderTemplate で展開する。
func renderTemplates(tmpls []string, p string) []string {
	out := make([]string, len(tmpls))
	for i, tmpl := range tmpls {
		out[i] = renderTemplate(tmpl, p)
	}
	return out
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
			return false, fmt.Errorf("パターン %q が不正です: %w", pat, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
