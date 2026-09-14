// Package diffcontent は差分の追加行・削除行そのものを検査する。
//
// unwanted-files がファイル単位の deny なのに対し、こちらは行単位の deny になる。
// 抑制コメントの追加、テストの skip/only、コンフリクトマーカーの混入など、
// 「ファイル全体を見る linter では新規混入だけを拾えない」種類のミスを狙う。
package diffcontent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

const (
	onAdded   = "added"
	onRemoved = "removed"
)

type rule struct {
	pattern *regexp.Regexp
	reason  string
	on      string
	paths   string
}

// Check は diff-content 検査の 1 インスタンス。
type Check struct {
	rules []rule
}

// New は config.CheckConfig から Check を組み立てる。deny の各項目はここで検証し、
// 不正な設定は起動時に検出する。
func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.Deny) == 0 {
		return nil, fmt.Errorf("diffcontent: deny には少なくとも 1 件のルールが必要です")
	}

	c := &Check{}
	for _, d := range cc.Deny {
		if d.Reason == "" {
			return nil, fmt.Errorf("diffcontent: deny には reason が必要です")
		}
		if d.Pattern == "" {
			return nil, fmt.Errorf("diffcontent: deny には pattern が必要です")
		}
		re, err := regexp.Compile(d.Pattern)
		if err != nil {
			return nil, fmt.Errorf("diffcontent: deny: pattern %q のコンパイルに失敗しました: %w", d.Pattern, err)
		}

		on := d.On
		if on == "" {
			on = onAdded
		}
		if on != onAdded && on != onRemoved {
			return nil, fmt.Errorf("diffcontent: deny: on %q は未対応です（added | removed）", d.On)
		}

		if d.Paths != "" && !doublestar.ValidatePattern(d.Paths) {
			return nil, fmt.Errorf("diffcontent: deny: パターン %q が不正です", d.Paths)
		}

		c.rules = append(c.rules, rule{pattern: re, reason: d.Reason, on: on, paths: d.Paths})
	}
	return c, nil
}

// Granularity はコミットごとに 1 回ずつ見る（一度履歴に入った行は後から消しても直らない
// ため、unwanted-files と同じ意味論にする）。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityPerCommit
}

// Run は ctx.Source の差分行を deny ルールと突き合わせる。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	src := ctx.Source
	changed, err := src.ChangedFiles()
	if err != nil {
		return nil, fmt.Errorf("diffcontent: 変更ファイルの取得に失敗しました: %w", err)
	}

	var order []string
	hits := map[string][]string{}

	for _, f := range changed {
		applicable, err := rulesFor(c.rules, f)
		if err != nil {
			return nil, err
		}
		if len(applicable) == 0 {
			continue
		}

		diff, err := src.DiffLines(f)
		if err != nil {
			return nil, fmt.Errorf("diffcontent: %s の差分取得に失敗しました: %w", f, err)
		}
		added, removed := parseDiffLines(diff)

		for _, ln := range added {
			reason, ok := firstMatch(applicable, onAdded, ln.text)
			if !ok {
				continue
			}
			if _, seen := hits[reason]; !seen {
				order = append(order, reason)
			}
			hits[reason] = append(hits[reason], formatHit(f, ln.num, ln.text))
		}
		for _, ln := range removed {
			reason, ok := firstMatch(applicable, onRemoved, ln.text)
			if !ok {
				continue
			}
			if _, seen := hits[reason]; !seen {
				order = append(order, reason)
			}
			hits[reason] = append(hits[reason], formatHit(f, ln.num, ln.text))
		}
	}

	if len(order) == 0 {
		return nil, nil
	}

	violations := make([]check.Violation, 0, len(order))
	for _, reason := range order {
		violations = append(violations, check.Violation{
			Summary: reason + ":",
			Files:   hits[reason],
		})
	}
	return violations, nil
}

// rulesFor は f にパスが一致する（または paths 未指定の）ルールだけを返す。
// 一致するルールが 1 つも無いファイルは DiffLines を呼ばずに素通りするための絞り込み。
func rulesFor(rules []rule, f string) ([]rule, error) {
	var out []rule
	for _, r := range rules {
		if r.paths == "" {
			out = append(out, r)
			continue
		}
		ok, err := doublestar.Match(r.paths, f)
		if err != nil {
			return nil, fmt.Errorf("diffcontent: %s の評価に失敗しました: %w", r.paths, err)
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// firstMatch は on 方向が一致するルールを宣言順に見て、最初に pattern が一致した reason を返す。
func firstMatch(rules []rule, on, text string) (string, bool) {
	for _, r := range rules {
		if r.on != on {
			continue
		}
		if r.pattern.MatchString(text) {
			return r.reason, true
		}
	}
	return "", false
}

const maxLineDisplayLen = 120

func formatHit(path string, line int, text string) string {
	if len(text) > maxLineDisplayLen {
		text = text[:maxLineDisplayLen] + "..."
	}
	return fmt.Sprintf("%s:%d: %s", path, line, text)
}

// lineEntry は差分中の 1 行（+/- を落とした後の中身）と、その行の行番号。
type lineEntry struct {
	num  int
	text string
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseDiffLines は `git diff -U0` の出力を追加行・削除行に分ける。
//
//   - "+++ "/"--- " のファイルヘッダ行は -U0 でも出力されるため先に除外する
//   - "@@ -a,b +c,d @@" ハンクヘッダから行番号の起点を読み取る（",b"/",d" が
//     省略される "@@ -1 +1 @@" の形もある）
//   - 残りの "+"/"-" で始まる行が追加行/削除行。先頭の記号を落とした文字列を
//     pattern の対象にする
func parseDiffLines(diff string) (added, removed []lineEntry) {
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@ "):
			m := hunkHeaderPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			// ファイルヘッダ行。中身の対象にも行番号のカウントにも含めない。
		case strings.HasPrefix(line, "+"):
			added = append(added, lineEntry{num: newLine, text: line[1:]})
			newLine++
		case strings.HasPrefix(line, "-"):
			removed = append(removed, lineEntry{num: oldLine, text: line[1:]})
			oldLine++
		}
	}
	return added, removed
}
