// Package docutil は Markdown を対象にする検査（doc-paths, doc-links）が共通で使う処理を
// まとめる：対象ドキュメントの解決、コードフェンス除去、パスの実在確認。
//
// 抽出戦略（doc-paths はバッククォート内の言及、doc-links はリンク記法）は検査ごとに
// 異なるため、ここには含めない。
package docutil

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// fenceOpenRe はコードフェンス（``` / ~~~、3 つ以上）の開始行を検出する。
var fenceOpenRe = regexp.MustCompile("^(`{3,}|~{3,})")

// ResolveDocs は patterns（doublestar、"**" 対応）に一致する Markdown ファイルの一覧を
// 昇順で返す。patterns が空なら "**/*.md" を既定にする。".git" 配下は常に除く。
func ResolveDocs(fsys fs.FS, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		patterns = []string{"**/*.md"}
	}

	seen := map[string]bool{}
	for _, pattern := range patterns {
		matches, err := doublestar.Glob(fsys, pattern)
		if err != nil {
			return nil, fmt.Errorf("パターン %q が不正です: %w", pattern, err)
		}
		for _, m := range matches {
			if m == ".git" || strings.HasPrefix(m, ".git/") {
				continue
			}
			seen[m] = true
		}
	}

	docs := make([]string, 0, len(seen))
	for m := range seen {
		docs = append(docs, m)
	}
	sort.Strings(docs)
	return docs, nil
}

// fenceState はコードフェンスの中にいるかどうかを行ごとに追跡する。
type fenceState struct {
	inFence   bool
	fenceChar byte
}

// consume は 1 行を読み進め、その行がフェンス関連（開始/終了行、またはフェンス内部）で
// 捨てるべきなら true を返す。
func (s *fenceState) consume(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !s.inFence {
		if m := fenceOpenRe.FindString(trimmed); m != "" {
			s.inFence = true
			s.fenceChar = m[0]
			return true
		}
		return false
	}

	if len(trimmed) > 0 && trimmed[0] == s.fenceChar {
		run := 0
		for run < len(trimmed) && trimmed[run] == s.fenceChar {
			run++
		}
		if run >= 3 && strings.TrimSpace(trimmed[run:]) == "" {
			s.inFence = false
		}
	}
	// フェンス内の行は開始・終了行を含めて丸ごと捨てる。
	return true
}

// StripCodeFences はフェンスドコードブロック（``` / ~~~）の中身を丸ごと取り除く。
// コードブロック内の記法（バッククォートやリンク記法に見える文字列）を地の文の抽出対象と
// 数えてしまうと誤抽出・抽出漏れの原因になるため、抽出前に必ず除去する。
//
// 行番号を保持する必要がある呼び出し側（doc-links）は StripCodeFencesKeepLines を使うこと。
// こちらはフェンス行そのものを削るため、返り値の行番号は元のドキュメントとずれる。
func StripCodeFences(content string) string {
	lines := strings.Split(content, "\n")
	var st fenceState
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if st.consume(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// StripCodeFencesKeepLines は StripCodeFences と同じ判定でフェンス内の行を無害化するが、
// 行そのものは削らず空行に置き換える。返り値の行番号（1 始まり）が元のドキュメントの
// 行番号とそろうため、行番号を報告する検査（doc-links）はこちらを使う。
func StripCodeFencesKeepLines(content string) string {
	lines := strings.Split(content, "\n")
	var st fenceState
	out := make([]string, len(lines))
	for i, line := range lines {
		if st.consume(line) {
			out[i] = ""
		} else {
			out[i] = line
		}
	}
	return strings.Join(out, "\n")
}

// ExistsOrGlob は p がリポジトリ内に実在するかを調べる。"*" を含む場合は doublestar
// パターン（"**" 対応）として扱い、1 つ以上に一致すればよい（グロブ表記の例示）。
// 不正な glob 表記は「存在しない」として扱う（地の文にたまたま "[" 等が混ざっただけで
// 検査全体が異常終了しないようにするため）。
func ExistsOrGlob(fsys fs.FS, p string) bool {
	if strings.Contains(p, "*") {
		matches, err := doublestar.Glob(fsys, p)
		if err != nil {
			return false
		}
		return len(matches) > 0
	}
	_, err := fs.Stat(fsys, p)
	return err == nil
}
