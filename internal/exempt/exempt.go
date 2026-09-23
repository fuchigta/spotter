// Package exempt はコミットメッセージ本文のトレーラによる検査の免除判定を扱う。
//
// 環境変数ではなくコミットメッセージにしているのは、ローカルで通した判断が CI でも
// そのまま通る必要があるため。
package exempt

import (
	"fmt"
	"regexp"
	"strings"
)

// Config は 1 つの検査インスタンスの免除設定。
type Config struct {
	// Enable が false なら、メッセージの内容に関わらず免除されない。
	Enable bool
	// Trailer はトレーラ名（例: "Doc-Sync"）。
	Trailer string
}

// Check は message の**トレーラ段落**（末尾の、全行がトレーラ形式の段落）に
// Config.Trailer のスキップトレーラがあるかを調べる。理由が空のスキップは免除として
// 認めない（免除には理由を添えて書く運用を前提にしている）。
//
// トレーラ段落が本文の途中にあっても対象にならない。git のトレーラと同じく、
// メッセージ本文の最後の段落だけを見る（詳しくは trailerBlock を参照）。
func Check(cfg Config, message string) (skip bool, reason string, err error) {
	if !cfg.Enable {
		return false, "", nil
	}
	if cfg.Trailer == "" {
		return false, "", fmt.Errorf("exempt: trailer が空です")
	}

	re, err := regexp.Compile(`(?i)^` + regexp.QuoteMeta(cfg.Trailer) + `:\s*skip\s+(\S.*)$`)
	if err != nil {
		return false, "", fmt.Errorf("exempt: トレーラ名 %q の正規表現コンパイルに失敗しました: %w", cfg.Trailer, err)
	}

	block := trailerBlock(message)
	if block == "" {
		return false, "", nil
	}

	for _, line := range strings.Split(block, "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			return true, strings.TrimSpace(m[1]), nil
		}
	}
	return false, "", nil
}

// scissorsLineRe は `git commit -v` がテンプレートに挿入する区切り行
// （"# ------------------------ >8 ------------------------"）に当てる。
// コメント文字の後に空白、"-" の並び、"8" が続く前に ">" が挟まる、という形を
// 緩めに受け止める（正確な "-" の個数までは要求しない）。
var scissorsLineRe = regexp.MustCompile(`^#\s*-+\s*>8\s*-+`)

// trailerLineRe は 1 行がトレーラの形（"<キー>: <値>"）をしているかを判定する。
// git のトレーラ表記に合わせ、キーは英数字とハイフンのみ、コロンの直後に空白を要求する。
var trailerLineRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*:\s`)

// normalize はコミットメッセージ本文を、commit-msg フックが受け取る生のファイル内容
// （エディタのコメントや `git commit -v` の差分プレビューを含みうる）から、
// トレーラ判定に使える形に正規化する。
//   - CRLF・CR を LF に統一する
//   - `git commit -v` の区切り行（scissorsLineRe）以降を全て捨てる（その下にある
//     差分プレビューの中の文章が誤ってトレーラ扱いされないようにするため）
//   - コメント行（先頭が "#" の行）を捨てる。
//     NOTE: core.commentChar のカスタム設定は考慮しない。commit-msg フックが受け取る
//     ファイルは通常 core.commentChar の既定値 "#" を前提にしており、独自のコメント
//     文字を設定する利用者は稀と判断してこの単純化を許容している。
//   - 末尾の空行を取り除く
func normalize(message string) string {
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")

	lines := strings.Split(message, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if scissorsLineRe.MatchString(line) {
			break
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		kept = append(kept, line)
	}

	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}

	return strings.Join(kept, "\n")
}

// splitParagraphs は正規化済みメッセージを、空行で区切られた段落の列に分ける。
func splitParagraphs(message string) []string {
	var paragraphs []string
	var current []string
	for _, line := range strings.Split(message, "\n") {
		if line == "" {
			if len(current) > 0 {
				paragraphs = append(paragraphs, strings.Join(current, "\n"))
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, strings.Join(current, "\n"))
	}
	return paragraphs
}

// isTrailerLine は line がトレーラ行（"<キー>: <値>"）か、トレーラの値が複数行に
// またがる場合の継続行（空白で始まる行）かを判定する。
func isTrailerLine(line string) bool {
	if line == "" {
		return false
	}
	if line[0] == ' ' || line[0] == '\t' {
		return true
	}
	return trailerLineRe.MatchString(line)
}

// trailerBlock は正規化済みメッセージの**最後の段落**を取り出し、その段落の全ての行が
// トレーラ形式（isTrailerLine）である場合だけそれを返す。1 行でもトレーラ形式でない行が
// あれば ""（トレーラ無し）を返す。段落が 1 つしかないメッセージ（subject しか無い、
// または本文が subject と地続きで空行が無い）も、その 1 段落を subject とみなしトレーラ
// 無しとする（git interpret-trailers と同じ扱い）。
func trailerBlock(message string) string {
	paragraphs := splitParagraphs(normalize(message))
	if len(paragraphs) < 2 {
		return ""
	}

	last := paragraphs[len(paragraphs)-1]
	for _, line := range strings.Split(last, "\n") {
		if !isTrailerLine(line) {
			return ""
		}
	}
	return last
}
