// Package diffsize は 1 コミットの変更量（ファイル数・行数）に上限を設ける検査を実装する。
//
// 個々の変更が正しく見えても、頼まれた範囲を超えて広がった書き換えは内容ベースの検査では
// 捕まらない。量でしか捕まえられない。この検査の目的は変更量を機械的に禁止することでは
// なく、免除トレーラを通じて「なぜこの量になったか」を人間に一度意識させることにある。
package diffsize

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

// topOffendersLimit は超過時に個別のファイル名を表示する上限件数。
const topOffendersLimit = 5

// Check は diff-size 検査の 1 インスタンス。
type Check struct {
	maxFiles int
	maxLines int
	exclude  []string
}

// New は config.CheckConfig から Check を組み立てる。max_files / max_lines の両方が 0
// （既定）だと「常に成功する無意味な検査」になるため起動時エラーにする。
func New(cc config.CheckConfig) (*Check, error) {
	if cc.MaxFiles <= 0 && cc.MaxLines <= 0 {
		return nil, fmt.Errorf("diffsize: max_files と max_lines のどちらか一方は指定してください")
	}
	for _, p := range cc.Exclude {
		if !doublestar.ValidatePattern(p) {
			return nil, fmt.Errorf("diffsize: exclude: パターン %q が不正です", p)
		}
	}
	return &Check{maxFiles: cc.MaxFiles, maxLines: cc.MaxLines, exclude: cc.Exclude}, nil
}

// Granularity はコミットごとに 1 回ずつ見る。1 コミットの中で起きた暴走を捕まえたいので、
// 範囲全体の合計にする squashed は選ばない（「PR を分割せよ」という別の主張になってしまう）。
// checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityPerCommit
}

// Run は ctx.Source.Stats() を集計し、ファイル数・行数のいずれかが上限を超えていれば違反にする。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	stats, err := ctx.Source.Stats()
	if err != nil {
		return nil, fmt.Errorf("diffsize: 変更量の取得に失敗しました: %w", err)
	}

	var filtered []check.FileStat
	for _, s := range stats {
		excluded, err := matchesAny(c.exclude, s.Path)
		if err != nil {
			return nil, fmt.Errorf("diffsize: exclude の評価に失敗しました: %w", err)
		}
		if excluded {
			continue
		}
		filtered = append(filtered, s)
	}

	var violations []check.Violation

	if c.maxFiles > 0 && len(filtered) > c.maxFiles {
		violations = append(violations, check.Violation{
			Summary: fmt.Sprintf("変更ファイル数が上限を超えています: %s 件（上限 %s 件）", commafy(len(filtered)), commafy(c.maxFiles)),
			Files:   topOffenders(filtered),
		})
	}

	totalAdded, totalDeleted := 0, 0
	for _, s := range filtered {
		totalAdded += s.Added
		totalDeleted += s.Deleted
	}
	totalLines := totalAdded + totalDeleted

	if c.maxLines > 0 && totalLines > c.maxLines {
		violations = append(violations, check.Violation{
			Summary: fmt.Sprintf("変更行数が上限を超えています: %s 行（追加 %s / 削除 %s、上限 %s 行）",
				commafy(totalLines), commafy(totalAdded), commafy(totalDeleted), commafy(c.maxLines)),
			Files: topOffenders(filtered),
		})
	}

	return violations, nil
}

// topOffenders は変更行数の多い順に上位 topOffendersLimit 件を "path (+added/-deleted)" の
// 形で返し、残りは「ほか N 件」の 1 行にまとめる。超過したファイルを全部並べても読めないため。
// 同じ行数の場合はパス昇順で決定論的に並ぶ。
func topOffenders(stats []check.FileStat) []string {
	sorted := make([]check.FileStat, len(stats))
	copy(sorted, stats)
	sort.SliceStable(sorted, func(i, j int) bool {
		linesI := sorted[i].Added + sorted[i].Deleted
		linesJ := sorted[j].Added + sorted[j].Deleted
		if linesI != linesJ {
			return linesI > linesJ
		}
		return sorted[i].Path < sorted[j].Path
	})

	limit := topOffendersLimit
	if len(sorted) < limit {
		limit = len(sorted)
	}

	files := make([]string, 0, limit+1)
	for _, s := range sorted[:limit] {
		files = append(files, fmt.Sprintf("%s (+%d/-%d)", s.Path, s.Added, s.Deleted))
	}
	if rest := len(sorted) - limit; rest > 0 {
		files = append(files, fmt.Sprintf("ほか %d 件", rest))
	}
	return files
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

// commafy は整数を 3 桁区切りの文字列にする（例: 1203 → "1,203"）。
func commafy(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}

	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)

	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}
