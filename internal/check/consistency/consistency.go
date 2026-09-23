// Package consistency は「複数ファイルから抽出した集合が一致するか」を検証する汎用検査を
// 実装する。
//
// コミット type の一覧を複数箇所（例: cliff.toml の commit_parsers と .spotter.yml の
// allowed_types）でそれぞれ別の書式のまま二重管理していると、片方だけ更新して食い違う
// 事故が起きる。「ファイル＋抽出規則の集合を突き合わせる」という仕組み自体はコミット
// type に限らず汎用なので、抽出規則を設定（sources）に外出しした型として実装する。
//
// 現在の worktree の中身を見るだけで、git の差分にも commit-msg にも関わらないため
// check.GranularityWorktree を使う（doc-paths と同じ理由）。
package consistency

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

type source struct {
	file    string
	line    *regexp.Regexp // nil なら全行を対象にする
	until   *regexp.Regexp // nil ならブロック化しない
	extract *regexp.Regexp
	split   string
}

// Check は consistency 検査の 1 インスタンス。
type Check struct {
	sources []source
}

// New は config.CheckConfig から Check を組み立てる。
func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.Sources) < 2 {
		return nil, fmt.Errorf("consistency: sources は 2 つ以上必要です")
	}

	sources := make([]source, 0, len(cc.Sources))
	for _, s := range cc.Sources {
		if s.File == "" {
			return nil, fmt.Errorf("consistency: sources には file が必要です")
		}
		if s.Extract == "" {
			return nil, fmt.Errorf("consistency: %s: extract が必要です", s.File)
		}

		var lineRe *regexp.Regexp
		if s.Line != "" {
			re, err := regexp.Compile(s.Line)
			if err != nil {
				return nil, fmt.Errorf("consistency: %s: line のコンパイルに失敗しました: %w", s.File, err)
			}
			lineRe = re
		}

		var untilRe *regexp.Regexp
		if s.Until != "" {
			if s.Line == "" {
				return nil, fmt.Errorf("consistency: %s: until は line とセットでのみ指定できます", s.File)
			}
			re, err := regexp.Compile(s.Until)
			if err != nil {
				return nil, fmt.Errorf("consistency: %s: until のコンパイルに失敗しました: %w", s.File, err)
			}
			untilRe = re
		}

		extractRe, err := regexp.Compile(s.Extract)
		if err != nil {
			return nil, fmt.Errorf("consistency: %s: extract のコンパイルに失敗しました: %w", s.File, err)
		}
		if extractRe.NumSubexp() != 1 {
			return nil, fmt.Errorf(
				"consistency: %s: extract にはキャプチャグループがちょうど 1 つ必要です（%d 個あります）。"+
					"値として取り出さないグループには (?:...) を使ってください",
				s.File, extractRe.NumSubexp(),
			)
		}

		sources = append(sources, source{file: s.File, line: lineRe, until: untilRe, extract: extractRe, split: s.Split})
	}

	return &Check{sources: sources}, nil
}

// Granularity は現在の作業ツリーを 1 回だけ見る。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityWorktree
}

// Run は各 source から集合を抜き出し、全ての組み合わせで一致するかを確認する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	sets := make([]map[string]bool, len(c.sources))
	for i, s := range c.sources {
		set, err := extractSet(ctx.Root, i, s)
		if err != nil {
			return nil, err
		}
		if len(set) == 0 {
			return nil, fmt.Errorf("consistency: %s から 1 つも抽出できませんでした。記法が変わっていないか確認してください", s.file)
		}
		sets[i] = set
	}

	var violations []check.Violation
	for i := 0; i < len(c.sources); i++ {
		for j := i + 1; j < len(c.sources); j++ {
			onlyA := diff(sets[i], sets[j])
			onlyB := diff(sets[j], sets[i])
			if len(onlyA) == 0 && len(onlyB) == 0 {
				continue
			}

			fileA, fileB := c.sources[i].file, c.sources[j].file
			var detail []string
			if len(onlyA) > 0 {
				detail = append(detail, fmt.Sprintf("%s にあって %s に無い: %s", fileA, fileB, strings.Join(onlyA, " ")))
			}
			if len(onlyB) > 0 {
				detail = append(detail, fmt.Sprintf("%s にあって %s に無い: %s", fileB, fileA, strings.Join(onlyB, " ")))
			}

			violations = append(violations, check.Violation{
				Summary: fmt.Sprintf("%s と %s で抽出結果が一致しません:", fileA, fileB),
				Files:   detail,
			})
		}
	}

	return violations, nil
}

func extractSet(root string, idx int, s source) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(root, s.file))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("consistency: sources[%d]（file: %s）が見つかりません: %w", idx, s.file, err)
		}
		return nil, fmt.Errorf("consistency: sources[%d]（file: %s）の読み込みに失敗しました: %w", idx, s.file, err)
	}

	lines := strings.Split(string(data), "\n")
	set := map[string]bool{}

	if s.until == nil {
		for _, line := range lines {
			if s.line != nil && !s.line.MatchString(line) {
				continue
			}
			applyExtract(line, s, set)
		}
		return set, nil
	}

	// until 指定時は line にマッチした行から until にマッチする行まで（両端含む）を
	// 1 ブロックとし、ブロック内の各行に extract を当てる。複数ブロックがあれば全て対象。
	for i := 0; i < len(lines); i++ {
		if !s.line.MatchString(lines[i]) {
			continue
		}

		// until の探索はブロック開始行の次の行から始める。line と until が同じ行に
		// マッチしうる書き方（例: 両方とも「行頭が非空白」系）でも、開始行だけの
		// 0 行ブロックに縮退しないようにするため。
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if s.until.MatchString(lines[j]) {
				end = j
				break
			}
		}
		if end == -1 {
			return nil, fmt.Errorf(
				"consistency: sources[%d]（file: %s）: %d 行目から始まるブロックの終端（until にマッチする行）が見つかりませんでした",
				idx, s.file, i+1,
			)
		}

		for k := i; k <= end; k++ {
			applyExtract(lines[k], s, set)
		}
		i = end
	}

	return set, nil
}

// applyExtract は 1 行に extract（・split）を適用し、set に加える。
func applyExtract(line string, s source, set map[string]bool) {
	for _, m := range s.extract.FindAllStringSubmatch(line, -1) {
		val := m[1]
		if s.split == "" {
			set[val] = true
			continue
		}
		for _, tok := range strings.Split(val, s.split) {
			tok = strings.TrimSpace(tok)
			if tok != "" {
				set[tok] = true
			}
		}
	}
}

// diff は a にあって b に無い要素を昇順で返す。
func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
