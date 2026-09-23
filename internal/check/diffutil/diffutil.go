// Package diffutil は `git diff -U0` の出力を検査から扱いやすい形に変換する処理をまとめる。
// diffcontent のように、追加行・削除行そのものを見る検査が共通で使う。
package diffutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Line は差分中の 1 行（先頭の +/- を落とした後の中身）と、その行の行番号。
type Line struct {
	Num  int
	Text string
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// ParseLines は `git diff -U0` の出力を追加行・削除行に分ける。
//
//   - "+++ "/"--- " のファイルヘッダ行は -U0 でも出力されるため先に除外する
//   - "@@ -a,b +c,d @@" ハンクヘッダから行番号の起点を読み取る（",b"/",d" が
//     省略される "@@ -1 +1 @@" の形もある）
//   - ハンクヘッダの形式が崩れていて起点を読み取れない場合、直前のハンクの行番号を
//     引きずって以降の行に誤った番号を付け続けるより、そのハンクの行番号を 0 として
//     扱う方が「何かおかしい」と見て分かり、決定論的でもあるため、oldLine/newLine を
//     0 にリセットする（0 という行番号自体が異常を示すサインになる）
//   - 残りの "+"/"-" で始まる行が追加行/削除行。先頭の記号を落とした文字列を
//     呼び出し側の照合対象にする
func ParseLines(diff string) (added, removed []Line) {
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@ "):
			m := hunkHeaderPattern.FindStringSubmatch(line)
			if m == nil {
				oldLine, newLine = 0, 0
				continue
			}
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			// ファイルヘッダ行。中身の対象にも行番号のカウントにも含めない。
		case strings.HasPrefix(line, "+"):
			added = append(added, Line{Num: newLine, Text: line[1:]})
			newLine++
		case strings.HasPrefix(line, "-"):
			removed = append(removed, Line{Num: oldLine, Text: line[1:]})
			oldLine++
		}
	}
	return added, removed
}

// maxLineDisplayLen は FormatHit が 1 行を表示する際の上限（rune 数）。
const maxLineDisplayLen = 120

// Truncate は s が max rune を超えていれば max rune で切り詰めて "..." を付ける。
// バイト単位ではなく rune 単位で切ることで、マルチバイト文字（日本語など）の
// 途中で分割して不正な UTF-8 列を作らないようにする。
func Truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

// FormatHit は違反 1 件の表示行（"path:line: text"）を作る。text が 120 rune を
// 超える場合は Truncate で切り詰める。
func FormatHit(path string, line int, text string) string {
	return fmt.Sprintf("%s:%d: %s", path, line, Truncate(text, maxLineDisplayLen))
}
