// Package diffcontent は差分の追加行・削除行そのものを検査する。
//
// unwanted-files がファイル単位の deny なのに対し、こちらは行単位の deny になる。
// 抑制コメントの追加、テストの skip/only、コンフリクトマーカーの混入など、
// 「ファイル全体を見る linter では新規混入だけを拾えない」種類のミスを狙う。
package diffcontent

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/diffutil"
	"github.com/fuchigta/spotter/internal/config"
)

type rule struct {
	pattern *regexp.Regexp
	reason  string
	on      string
	paths   string
	// net は on: removed のルールにだけ許可される。true の場合、ファイルごとに
	// pattern に一致する削除行の数が同じ pattern に一致する追加行の数より多いときだけ
	// 違反にする（詳細は Run のコメント参照）。
	net bool
}

// Check は diff-content 検査の 1 インスタンス。
type Check struct {
	rules []rule
	// needsDeleted は rules に on: removed のルールが 1 件でもあるかどうか。無ければ
	// 削除ファイルの差分には出番が無い（削除ファイルは追加行を持たない）ため、
	// Run は DeletedFiles を呼ばずに済ませる。
	needsDeleted bool
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
			on = diffutil.OnAdded
		}
		if err := diffutil.ValidateOn(on); err != nil {
			return nil, fmt.Errorf("diffcontent: deny: on: %w", err)
		}

		if d.Net && on != diffutil.OnRemoved {
			return nil, fmt.Errorf("diffcontent: deny: net は on: removed のときだけ指定できます")
		}

		if d.Paths != "" && !doublestar.ValidatePattern(d.Paths) {
			return nil, fmt.Errorf("diffcontent: deny: パターン %q が不正です", d.Paths)
		}

		c.rules = append(c.rules, rule{pattern: re, reason: d.Reason, on: on, paths: d.Paths, net: d.Net})
		if on == diffutil.OnRemoved {
			c.needsDeleted = true
		}
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

	// on: removed のルールが 1 件も無ければ、削除ファイルの差分（追加行を持たず、
	// removed 行しか出ない）を見ても発火し得ないため、DeletedFiles を呼ばずに済ませる。
	files := changed
	if c.needsDeleted {
		deleted, err := src.DeletedFiles()
		if err != nil {
			return nil, fmt.Errorf("diffcontent: 削除ファイルの取得に失敗しました: %w", err)
		}
		// changed は Source が返したスライスなので、その余剰容量に書き込まないよう切り詰めてから足す。
		files = append(slices.Clip(changed), deleted...)
	}

	var order []string
	hits := map[string][]string{}
	recordHit := func(reason, f string, ln diffutil.Line) {
		if _, seen := hits[reason]; !seen {
			order = append(order, reason)
		}
		hits[reason] = append(hits[reason], diffutil.FormatHit(f, ln.Num, ln.Text))
	}

	// changed と deleted（削除・改名元）を同じループで扱う。削除ファイルの差分には
	// 追加行が無いため、added 側の判定をそのまま流しても誤って発火することはない。
	for _, f := range files {
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
		added, removed := diffutil.ParseLines(diff)

		for _, ln := range added {
			if idx, ok := firstMatch(applicable, diffutil.OnAdded, ln.Text); ok {
				recordHit(applicable[idx].reason, f, ln)
			}
		}

		// on: removed のルールのうち net なものは、このファイルの削除行数が追加行数を
		// 上回る場合だけ違反にするため、いったん保留して集計してから判定する
		// （net でないルールは即時に記録する）。
		netLines := make([][]diffutil.Line, len(applicable))
		for _, ln := range removed {
			idx, ok := firstMatch(applicable, diffutil.OnRemoved, ln.Text)
			if !ok {
				continue
			}
			r := applicable[idx]
			if r.net {
				netLines[idx] = append(netLines[idx], ln)
			} else {
				recordHit(r.reason, f, ln)
			}
		}
		for idx, lines := range netLines {
			if len(lines) == 0 {
				continue
			}
			r := applicable[idx]
			// 追加行の数え方は「その net ルールの pattern に一致する追加行」の生の
			// マッチ数で良い（firstMatch による on: added 側の帰属とは無関係）。
			addedCount := 0
			for _, ln := range added {
				if r.pattern.MatchString(ln.Text) {
					addedCount++
				}
			}
			if len(lines) <= addedCount {
				continue
			}
			// どの削除行が「本当に消えた」ものかは区別できないため、一致した削除行を全部報告する。
			for _, ln := range lines {
				recordHit(r.reason, f, ln)
			}
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

// firstMatch は on 方向が一致するルールを宣言順に見て、最初に pattern が一致したルールの
// rules 内でのインデックスを返す。呼び出し側が net 判定などでルール自体（rules[idx]）を
// 参照できるよう、reason 文字列ではなくインデックスを返す。
func firstMatch(rules []rule, on, text string) (int, bool) {
	for i, r := range rules {
		if r.on != on {
			continue
		}
		if r.pattern.MatchString(text) {
			return i, true
		}
	}
	return -1, false
}
