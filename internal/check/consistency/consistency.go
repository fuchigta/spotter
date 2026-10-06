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

	// glob 系フィールド。isGlob が true のとき file/line/until/split は使わない。extract は
	// base 除去後のパスに当てる。
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

func New(cc config.CheckConfig) (*Check, error) {
	if len(cc.Sources) < 2 {
		return nil, fmt.Errorf("consistency: sources は 2 つ以上必要です")
	}

	sources := make([]source, 0, len(cc.Sources))
	for _, s := range cc.Sources {
		src, err := buildSource(s)
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}

	if !hasRequiredSource(sources) {
		return nil, fmt.Errorf("consistency: subset ではない sources が最低 1 つ必要です")
	}

	return &Check{sources: sources}, nil
}

// buildSource は sources の 1 件分を file source と glob source のどちらかとして
// 組み立てる。file/glob のどちらを指定したかで以降の検証項目が分かれるため、
// ここで振り分ける。
func buildSource(s config.ConsistencySource) (source, error) {
	if (s.File == "") == (s.Glob == "") {
		return source{}, fmt.Errorf("consistency: sources には file か glob のどちらか一方が必要です")
	}
	if s.Glob != "" {
		return buildGlobSource(s)
	}
	return buildFileSource(s)
}

func buildGlobSource(s config.ConsistencySource) (source, error) {
	if s.Line != "" || s.Until != "" || s.Split != "" {
		return source{}, fmt.Errorf("consistency: %s: glob と line/until/split は併用できません", s.Glob)
	}
	if !doublestar.ValidatePattern(s.Glob) {
		return source{}, fmt.Errorf("consistency: glob %q が不正です", s.Glob)
	}
	for _, ex := range s.Exclude {
		if !doublestar.ValidatePattern(ex) {
			return source{}, fmt.Errorf("consistency: glob %q: exclude %q が不正です", s.Glob, ex)
		}
	}
	extractRe, err := compileExtract(s.Glob, s.Extract)
	if err != nil {
		return source{}, err
	}
	return source{
		isGlob:  true,
		glob:    s.Glob,
		extract: extractRe,
		base:    s.Base,
		exclude: append([]string(nil), s.Exclude...),
		subset:  s.Subset,
	}, nil
}

func buildFileSource(s config.ConsistencySource) (source, error) {
	if s.Base != "" || len(s.Exclude) > 0 {
		return source{}, fmt.Errorf("consistency: %s: base/exclude は glob と併用する場合のみ指定できます", s.File)
	}

	file, err := normalizeSourceFile(s.File)
	if err != nil {
		return source{}, err
	}
	if s.Extract == "" {
		return source{}, fmt.Errorf("consistency: %s: extract が必要です", s.File)
	}

	lineRe, err := compileSourceField(s.File, "line", s.Line)
	if err != nil {
		return source{}, err
	}
	if s.Until != "" && s.Line == "" {
		return source{}, fmt.Errorf("consistency: %s: until は line とセットでのみ指定できます", s.File)
	}
	untilRe, err := compileSourceField(s.File, "until", s.Until)
	if err != nil {
		return source{}, err
	}

	extractRe, err := compileExtract(s.File, s.Extract)
	if err != nil {
		return source{}, err
	}

	return source{
		file:    file,
		line:    lineRe,
		until:   untilRe,
		extract: extractRe,
		split:   s.Split,
		subset:  s.Subset,
	}, nil
}

// normalizeSourceFile は file source の File を fs.FS のパス表記に揃える。作業ツリーは
// fs.FS 越しに読むため、"./" や "\" を含む書き方を揃える。filepath.ToSlash は Linux では
// "\" を変換せず、fs.ValidPath は "C:" のようなドライブ文字を通すため、どちらも OS に
// 依らず自前で扱い、手元と CI で同じ設定の解釈が変わらないようにする。リポジトリの外を
// 指すパスは fs.FS では読めないので設定の誤りとして扱う。
func normalizeSourceFile(rawFile string) (string, error) {
	file := path.Clean(strings.ReplaceAll(rawFile, `\`, "/"))
	if !fs.ValidPath(file) || driveLetterRe.MatchString(file) {
		return "", fmt.Errorf("consistency: file %q はリポジトリのルートからの相対パスで、リポジトリの中を指す必要があります", rawFile)
	}
	return file, nil
}

// compileExtract は extract をコンパイルし、キャプチャグループがちょうど 1 つあることを
// 確かめる。pattern が空文字なら未指定として nil を返す。
func compileExtract(label, pattern string) (*regexp.Regexp, error) {
	re, err := compileSourceField(label, "extract", pattern)
	if err != nil || re == nil {
		return re, err
	}
	if re.NumSubexp() != 1 {
		return nil, fmt.Errorf(
			"consistency: %s: extract にはキャプチャグループがちょうど 1 つ必要です（%d 個あります）。"+
				"値として取り出さないグループには (?:...) を使ってください",
			label, re.NumSubexp(),
		)
	}
	return re, nil
}

// compileSourceField は line/until/extract のいずれか（field）を正規表現としてコンパイル
// する。pattern が空文字なら未指定として nil を返す。
func compileSourceField(file, field, pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("consistency: %s: %s のコンパイルに失敗しました: %w", file, field, err)
	}
	return re, nil
}

func hasRequiredSource(sources []source) bool {
	for _, s := range sources {
		if !s.subset {
			return true
		}
	}
	return false
}

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
		ex, err := extractSource(ctx.FS, i, s)
		if err != nil {
			return nil, err
		}
		if ex.unterminatedAt > 0 {
			return nil, errUnterminated(i, s, ex.unterminatedAt)
		}
		set := ex.set()
		if len(set) == 0 {
			return nil, errEmpty(s)
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

// item は抽出した 1 つの要素と、その出現位置。file source なら line が 1 始まりの行番号、
// glob source なら path が一致したファイルパス。
type item struct {
	value string
	line  int
	path  string
}

// blockRange は until で区切ったブロックの行範囲（1 始まり、両端含む）。
type blockRange struct{ start, end int }

// extraction は 1 つの source から抽出した結果。Run と Explain が同じ値を見るために、
// 集合に潰す前の形で持つ。
type extraction struct {
	items []item

	// blocks は until 指定時のブロック範囲。
	blocks []blockRange

	// lineMatches は line だけを指定したとき（until 無し）に line に一致した行。
	lineMatches []int

	// unterminatedAt は until に一致する行が見つからなかったブロックの開始行。0 なら無し。
	// Run は実行エラーにするが、Explain は説明に含めるため、ここでは error にしない。
	unterminatedAt int
}

func (ex extraction) set() map[string]bool {
	set := make(map[string]bool, len(ex.items))
	for _, it := range ex.items {
		set[it.value] = true
	}
	return set
}

func errUnterminated(idx int, s source, startLine int) error {
	return fmt.Errorf(
		"consistency: sources[%d]（file: %s）: %d 行目から始まるブロックの終端（until にマッチする行）が見つかりませんでした",
		idx, s.file, startLine,
	)
}

func errEmpty(s source) error {
	return fmt.Errorf("consistency: %s から 1 つも抽出できませんでした。記法が変わっていないか確認してください", s.label())
}

func extractSource(fsys fs.FS, idx int, s source) (extraction, error) {
	if s.isGlob {
		return extractGlob(fsys, s)
	}
	return extractFile(fsys, idx, s)
}

func extractFile(fsys fs.FS, idx int, s source) (extraction, error) {
	data, err := fs.ReadFile(fsys, s.file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return extraction{}, fmt.Errorf("consistency: sources[%d]（file: %s）が見つかりません: %w", idx, s.file, err)
		}
		return extraction{}, fmt.Errorf("consistency: sources[%d]（file: %s）の読み込みに失敗しました: %w", idx, s.file, err)
	}

	lines := strings.Split(string(data), "\n")
	var ex extraction

	if s.until == nil {
		for i, line := range lines {
			if s.line != nil {
				if !s.line.MatchString(line) {
					continue
				}
				ex.lineMatches = append(ex.lineMatches, i+1)
			}
			ex.items = append(ex.items, extractLine(line, i+1, s)...)
		}
		return ex, nil
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
			ex.unterminatedAt = i + 1
			return ex, nil
		}

		ex.blocks = append(ex.blocks, blockRange{start: i + 1, end: end + 1})
		for k := i; k <= end; k++ {
			ex.items = append(ex.items, extractLine(lines[k], k+1, s)...)
		}
		i = end
	}

	return ex, nil
}

// extractLine は 1 行に extract（・split）を適用し、見つかった要素を返す。
func extractLine(line string, lineNo int, s source) []item {
	var items []item
	for _, m := range s.extract.FindAllStringSubmatch(line, -1) {
		val := m[1]
		if s.split == "" {
			items = append(items, item{value: val, line: lineNo})
			continue
		}
		for _, tok := range strings.Split(val, s.split) {
			tok = strings.TrimSpace(tok)
			if tok != "" {
				items = append(items, item{value: tok, line: lineNo})
			}
		}
	}
	return items
}

// extractGlob は s.glob に一致する現在の作業ツリーのファイルパスを要素として返す。
// fs.FS 経由（docutil.ResolveDocs）で解決するため、パス区切りは Windows でも "/" に
// 揃う。ディレクトリと .git 配下は対象から除く。
func extractGlob(fsys fs.FS, s source) (extraction, error) {
	matches, err := docutil.ResolveDocs(fsys, []string{s.glob})
	if err != nil {
		return extraction{}, fmt.Errorf("consistency: glob %q の評価に失敗しました: %w", s.glob, err)
	}

	var ex extraction
	for _, m := range matches {
		info, err := fs.Stat(fsys, m)
		if err != nil {
			return extraction{}, fmt.Errorf("consistency: glob %q: %s の情報取得に失敗しました: %w", s.glob, m, err)
		}
		if info.IsDir() {
			continue
		}

		excluded := false
		for _, ex := range s.exclude {
			ok, err := doublestar.Match(ex, m)
			if err != nil {
				return extraction{}, fmt.Errorf("consistency: glob %q: exclude %q の評価に失敗しました: %w", s.glob, ex, err)
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
				return extraction{}, fmt.Errorf("consistency: glob %q: %s は base %q 配下にありません", s.glob, m, s.base)
			}
			elem = strings.TrimPrefix(m, prefix)
		}
		if s.extract != nil {
			sub := s.extract.FindStringSubmatch(elem)
			if sub == nil {
				return extraction{}, fmt.Errorf("consistency: glob %q: %s は extract %q に一致しません", s.glob, elem, s.extract)
			}
			elem = sub[1]
		}
		ex.items = append(ex.items, item{value: elem, path: m})
	}

	return ex, nil
}
