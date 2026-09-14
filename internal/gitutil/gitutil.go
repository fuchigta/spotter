// Package gitutil は spotter が必要とする git 操作をまとめる。
//
// ファイルリストや diff はホスト（spotter 本体）が git で取得する。検査本体は
// check.Source インターフェース越しにこれを参照するだけで、staged/range の違いを
// 知らない。
package gitutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
)

// EmptyTree は git の空ツリーの SHA。根コミットを含む範囲の起点に使う。
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Repo はリポジトリのルートディレクトリに対する git 操作をまとめる。
type Repo struct {
	Dir string
}

// New は dir をルートとする Repo を返す。
func New(dir string) *Repo {
	return &Repo{Dir: dir}
}

func (r *Repo) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitutil: git %s の実行に失敗しました: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// RevListNoMerges は range 式（"a..b" や "-1 HEAD" のような複数語も許す）に一致する
// マージコミットを除くコミットの一覧を新しい順で返す。
func (r *Repo) RevListNoMerges(rangeExpr string) ([]string, error) {
	args := append([]string{"rev-list", "--no-merges"}, strings.Fields(rangeExpr)...)
	out, err := r.run(args...)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

// ConfigGet は git config の値を読む。未設定なら ok=false（値の有無と空文字の区別が
// 要らない呼び出し側の便宜のため、エラーではなく ok で表す）。
func (r *Repo) ConfigGet(key string) (value string, ok bool, err error) {
	out, runErr := r.run("config", "--get", key)
	if runErr != nil {
		return "", false, nil
	}
	return strings.TrimSpace(out), true, nil
}

// ConfigSet は git config の値を設定する。
func (r *Repo) ConfigSet(key, value string) error {
	_, err := r.run("config", key, value)
	return err
}

// GitPath は git rev-parse --git-path で、.git ディレクトリ配下の実際のパスを解決する
// （worktree・submodule で .git が単純なディレクトリではない場合も正しく解決するため）。
func (r *Repo) GitPath(rel string) (string, error) {
	out, err := r.run("rev-parse", "--git-path", rel)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// TopLevel は git rev-parse --show-toplevel で、リポジトリのルートディレクトリの
// 絶対パスを返す（r.Dir がリポジトリのサブディレクトリでも解決できる）。
// project スコープのスキル設置先（.claude/skills, .agents/skills）を、
// カレントディレクトリに依存せず求めるために使う。
//
// git は Windows でもスラッシュ区切り（"C:/Users/..."）で返すため、
// filepath.FromSlash でこの OS のセパレータに正規化してから返す
// （filepath.Join 等では無害だが、呼び出し側が文字列としてそのまま
// 表示・比較する可能性があるため呼び出し元に矯正を要求しない）。
func (r *Repo) TopLevel() (string, error) {
	out, err := r.run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(strings.TrimSpace(out)), nil
}

// CommitExists は sha がこのリポジトリに実在するコミットかどうかを返す。
// 新しいブランチの最初の push や force push 直後は CI が渡す「比較元」の SHA が
// 全ゼロ（0000...）になったり、そもそも取得されていなかったりする。そうした
// 「実在しない」は呼び出し側のフォールバック処理に委ねるための正常系なので、
// git コマンド自体の失敗（非 0 終了）はエラーにせず false として返す
// （元のシェルスクリプトの `git cat-file -e ... 2>/dev/null` と同じ割り切り）。
func (r *Repo) CommitExists(sha string) (bool, error) {
	if sha == "" {
		return false, nil
	}
	if _, err := r.run("cat-file", "-e", sha+"^{commit}"); err != nil {
		return false, nil
	}
	return true, nil
}

// ParentOrEmptyTree は sha の親コミットを返す。根コミットなら EmptyTree を返す。
func (r *Repo) ParentOrEmptyTree(sha string) (string, error) {
	out, err := r.run("rev-parse", "-q", "--verify", sha+"^")
	if err != nil {
		return EmptyTree, nil
	}
	return strings.TrimSpace(out), nil
}

// CommitMessageBody はそのコミット単体のメッセージ本文を返す。
func (r *Repo) CommitMessageBody(sha string) (string, error) {
	return r.run("log", "-1", "--format=%B", sha)
}

// RangeMessagesBody は range 式に含まれる全コミットのメッセージを連結して返す
// （squashed 粒度の免除判定用。範囲内のどれか 1 つにトレーラがあれば免除が効く）。
func (r *Repo) RangeMessagesBody(rangeExpr string) (string, error) {
	args := append([]string{"log", "--format=%B"}, strings.Fields(rangeExpr)...)
	return r.run(args...)
}

// CommitLabel は "<短い sha> <件名>" というラベルを返す。
func (r *Repo) CommitLabel(sha string) (string, error) {
	out, err := r.run("log", "-1", "--format=%h %s", sha)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// stagedSource はステージ済みの変更（commit-msg フック）を見る check.Source。
type stagedSource struct{ r *Repo }

// StagedSource はステージ済みの変更を見る check.Source を返す。
func (r *Repo) StagedSource() check.Source {
	return stagedSource{r: r}
}

func (s stagedSource) ChangedFiles() ([]string, error) {
	out, err := s.r.run("diff", "--cached", "--name-only", "--diff-filter=ACMR")
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (s stagedSource) DiffLines(path string) (string, error) {
	return s.r.run("diff", "--cached", "-U0", "--", path)
}

func (s stagedSource) BlobSize(path string) (int64, error) {
	return s.r.blobSize(":" + path)
}

func (s stagedSource) Stats() ([]check.FileStat, error) {
	out, err := s.r.run("diff", "--cached", "--numstat", "-z")
	if err != nil {
		return nil, err
	}
	return parseNumstatZ(out)
}

// rangeSource は from..to の比較を見る check.Source。
type rangeSource struct {
	r        *Repo
	from, to string
}

// RangeSource は from から to までの比較を見る check.Source を返す。
func (r *Repo) RangeSource(from, to string) check.Source {
	return rangeSource{r: r, from: from, to: to}
}

func (s rangeSource) ChangedFiles() ([]string, error) {
	out, err := s.r.run("diff", "--name-only", "--diff-filter=ACMR", s.from, s.to)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (s rangeSource) DiffLines(path string) (string, error) {
	return s.r.run("diff", "-U0", s.from, s.to, "--", path)
}

func (s rangeSource) BlobSize(path string) (int64, error) {
	return s.r.blobSize(s.to + ":" + path)
}

func (s rangeSource) Stats() ([]check.FileStat, error) {
	out, err := s.r.run("diff", "--numstat", "-z", s.from, s.to)
	if err != nil {
		return nil, err
	}
	return parseNumstatZ(out)
}

// parseNumstatZ は `git diff --numstat -z` の出力を解析する。
//
// -z 付きの numstat は NUL 区切りで、通常の 1 行は "<added>\t<deleted>\t<path>\0" という
// 1 トークン。バイナリファイルは added/deleted が "-" になる。リネームされたファイルは
// パス欄が空になり、代わりに "<added>\t<deleted>\t\0<旧パス>\0<新パス>\0" という 3 トークンに
// 分かれる（-z を使わない通常表示は "src/{old.go => new.go}" のような共通接頭辞の畳み込み
// 表記になり、任意のパスに対して機械的に分解できないため -z を使う）。
// リネームの場合は新パス側を FileStat.Path として採用する（削除・大量変更の検出という
// この関数の用途では、変更後にどのパスに存在するかの方が意味を持つため）。
func parseNumstatZ(out string) ([]check.FileStat, error) {
	tokens := strings.Split(out, "\x00")
	if len(tokens) > 0 && tokens[len(tokens)-1] == "" {
		tokens = tokens[:len(tokens)-1]
	}

	var stats []check.FileStat
	for i := 0; i < len(tokens); i++ {
		parts := strings.SplitN(tokens[i], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitutil: numstat の行を解析できません: %q", tokens[i])
		}
		addedStr, deletedStr, pathField := parts[0], parts[1], parts[2]

		path := pathField
		if pathField == "" {
			if i+2 >= len(tokens) {
				return nil, fmt.Errorf("gitutil: numstat のリネーム表記を解析できません: %q", tokens[i])
			}
			path = tokens[i+2] // 新パス側。旧パス（tokens[i+1]）は使わない。
			i += 2
		}

		binary := addedStr == "-" || deletedStr == "-"
		var added, deleted int
		if !binary {
			var err error
			added, err = strconv.Atoi(addedStr)
			if err != nil {
				return nil, fmt.Errorf("gitutil: numstat の追加行数を解析できません: %q", addedStr)
			}
			deleted, err = strconv.Atoi(deletedStr)
			if err != nil {
				return nil, fmt.Errorf("gitutil: numstat の削除行数を解析できません: %q", deletedStr)
			}
		}

		stats = append(stats, check.FileStat{Path: path, Added: added, Deleted: deleted, Binary: binary})
	}
	return stats, nil
}

// blobSize はそのオブジェクトのバイト数を返す。存在しない（削除された等）場合は 0 を返す
// （移行元のシェルスクリプトと同じ挙動）。
func (r *Repo) blobSize(object string) (int64, error) {
	out, err := r.run("cat-file", "-s", object)
	if err != nil {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gitutil: cat-file -s %s の出力を解析できません: %w", object, err)
	}
	return n, nil
}
