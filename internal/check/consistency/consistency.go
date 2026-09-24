// Package consistency は「複数ファイルから抽出した集合が一致するか」を検証する汎用検査を
// 実装する。
//
// コミット type の一覧を複数箇所（例: cliff.toml の commit_parsers と .spotter.yml の
// allowed_types）でそれぞれ別の書式のまま二重管理していると、片方だけ更新して食い違う
// 事故が起きる。「ファイル＋抽出規則の集合を突き合わせる」という仕組み自体はコミット
// type に限らず汎用なので、抽出規則を設定（sources）に外出しした型として実装する。
//
// sources は file（1 ファイルを正規表現で行単位に抽出）に加えて glob（doublestar
// パターンに一致するファイルパスの一覧をそのまま集合にする）も選べる。「ドキュメントの
// ページ集合」のように、抽出規則というより「ファイルの存在そのもの」を集合として扱いたい
// 場合に使う（例: docs/**/*.md のページ集合と、目次のリンク集合の突き合わせ）。
//
// 現在の worktree の中身を見るだけで、git の差分にも commit-msg にも関わらないため
// check.GranularityWorktree を使う（doc-paths と同じ理由）。
package consistency

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/docutil"
	"github.com/fuchigta/spotter/internal/config"
)

// driveLetterRe は Windows のドライブ文字で始まるパス（"C:/..." や "C:..."）に一致する。
var driveLetterRe = regexp.MustCompile(`^[A-Za-z]:`)

type source struct {
	file    string
	line    *regexp.Regexp // nil なら全行を対象にする
	until   *regexp.Regexp // nil ならブロック化しない
	extract *regexp.Regexp
	split   string
	subset  bool

	// glob 系フィールド。isGlob が true のとき file/line/until/extract/split は使わない。
	isGlob  bool
	glob    string
	base    string
	exclude []string
}

// label はエラーメッセージや違反表示で「どの source か」を示す文字列。file source なら
// ファイルパス、glob source なら glob パターンを使う。
func (s source) label() string {
	if s.isGlob {
		return s.glob
	}
	return s.file
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
		if (s.File == "") == (s.Glob == "") {
			return nil, fmt.Errorf("consistency: sources には file か glob のどちらか一方が必要です")
		}

		if s.Glob != "" {
			if s.Line != "" || s.Until != "" || s.Extract != "" || s.Split != "" {
				return nil, fmt.Errorf("consistency: %s: glob と line/until/extract/split は併用できません", s.Glob)
			}
			if !doublestar.ValidatePattern(s.Glob) {
				return nil, fmt.Errorf("consistency: glob %q が不正です", s.Glob)
			}
			for _, ex := range s.Exclude {
				if !doublestar.ValidatePattern(ex) {
					return nil, fmt.Errorf("consistency: glob %q: exclude %q が不正です", s.Glob, ex)
				}
			}
			sources = append(sources, source{
				isGlob:  true,
				glob:    s.Glob,
				base:    s.Base,
				exclude: append([]string(nil), s.Exclude...),
				subset:  s.Subset,
			})
			continue
		}

		if s.Base != "" || len(s.Exclude) > 0 {
			return nil, fmt.Errorf("consistency: %s: base/exclude は glob と併用する場合のみ指定できます", s.File)
		}
		// 作業ツリーは fs.FS 越しに読むため、"./" や "\" を含む書き方を fs.FS のパス表記に
		// 揃える。filepath.ToSlash は Linux では "\" を変換せず、fs.ValidPath は "C:" の
		// ようなドライブ文字を通すため、どちらも OS に依らず自前で扱い、手元と CI で
		// 同じ設定の解釈が変わらないようにする。リポジトリの外を指すパスは fs.FS では
		// 読めないので設定の誤りとして扱う。
		file := path.Clean(strings.ReplaceAll(s.File, `\`, "/"))
		if !fs.ValidPath(file) || driveLetterRe.MatchString(file) {
			return nil, fmt.Errorf("consistency: file %q はリポジトリのルートからの相対パスで、リポジトリの中を指す必要があります", s.File)
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

		sources = append(sources, source{
			file:    file,
			line:    lineRe,
			until:   untilRe,
			extract: extractRe,
			split:   s.Split,
			subset:  s.Subset,
		})
	}

	hasRequired := false
	for _, s := range sources {
		if !s.subset {
			hasRequired = true
			break
		}
	}
	if !hasRequired {
		return nil, fmt.Errorf("consistency: subset ではない sources が最低 1 つ必要です")
	}

	return &Check{sources: sources}, nil
}

// Granularity は現在の作業ツリーを 1 回だけ見る。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityWorktree
}

// Run は各 source から集合を抜き出し、subset ではない source どうしの完全一致と、
// subset な source が和集合からはみ出していないかを確認する。全ペアの差分を別々に
// 報告すると同じ食い違いが重複して出るため、食い違う要素ごとに1行にまとめた
// 1 つの Violation として報告する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	sets := make([]map[string]bool, len(c.sources))
	for i, s := range c.sources {
		set, err := extractSet(ctx.FS, i, s)
		if err != nil {
			return nil, err
		}
		if len(set) == 0 {
			return nil, fmt.Errorf("consistency: %s から 1 つも抽出できませんでした。記法が変わっていないか確認してください", s.label())
		}
		sets[i] = set
	}

	var requiredIdx []int
	for i, s := range c.sources {
		if !s.subset {
			requiredIdx = append(requiredIdx, i)
		}
	}

	elementSet := map[string]bool{}
	for _, set := range sets {
		for k := range set {
			elementSet[k] = true
		}
	}
	elements := make([]string, 0, len(elementSet))
	for k := range elementSet {
		elements = append(elements, k)
	}
	sort.Strings(elements)

	var lines []string
	for _, e := range elements {
		var missing []string
		for _, i := range requiredIdx {
			if !sets[i][e] {
				missing = append(missing, c.sources[i].label())
			}
		}
		if len(missing) == 0 {
			// subset ではない source 全てに存在する（subset 側は欠けていても良いので
			// 見なくてよい）。
			continue
		}

		var present []string
		for i, s := range c.sources {
			if sets[i][e] {
				present = append(present, s.label())
			}
		}

		sort.Strings(missing)
		sort.Strings(present)
		lines = append(lines, fmt.Sprintf("`%s`: %s に無い（%s にある）", e, strings.Join(missing, ", "), strings.Join(present, ", ")))
	}

	if len(lines) == 0 {
		return nil, nil
	}

	return []check.Violation{{
		Summary: "sources 間で抽出結果が一致しません:",
		Files:   lines,
	}}, nil
}

func extractSet(fsys fs.FS, idx int, s source) (map[string]bool, error) {
	if s.isGlob {
		return extractGlobSet(fsys, s)
	}

	data, err := fs.ReadFile(fsys, s.file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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

// extractGlobSet は s.glob に一致する現在の作業ツリーのファイルパスの集合を返す。
// fs.FS 経由（docutil.ResolveDocs）で解決するため、パス区切りは Windows でも "/" に
// 揃う。ディレクトリと .git 配下は対象から除く。
func extractGlobSet(fsys fs.FS, s source) (map[string]bool, error) {
	matches, err := docutil.ResolveDocs(fsys, []string{s.glob})
	if err != nil {
		return nil, fmt.Errorf("consistency: glob %q の評価に失敗しました: %w", s.glob, err)
	}

	set := map[string]bool{}
	for _, m := range matches {
		info, err := fs.Stat(fsys, m)
		if err != nil {
			return nil, fmt.Errorf("consistency: glob %q: %s の情報取得に失敗しました: %w", s.glob, m, err)
		}
		if info.IsDir() {
			continue
		}

		excluded := false
		for _, ex := range s.exclude {
			ok, err := doublestar.Match(ex, m)
			if err != nil {
				return nil, fmt.Errorf("consistency: glob %q: exclude %q の評価に失敗しました: %w", s.glob, ex, err)
			}
			if ok {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		elem := m
		if s.base != "" {
			prefix := s.base + "/"
			if !strings.HasPrefix(m, prefix) {
				return nil, fmt.Errorf("consistency: glob %q: %s は base %q 配下にありません", s.glob, m, s.base)
			}
			elem = strings.TrimPrefix(m, prefix)
		}
		set[elem] = true
	}

	return set, nil
}
