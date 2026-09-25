// Package gitutil は spotter が必要とする git 操作をまとめる。
//
// ファイルリストや diff はホスト（spotter 本体）が git で取得する。検査本体は
// check.Source インターフェース越しにこれを参照するだけで、staged/range の違いを
// 知らない。
package gitutil

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/fuchigta/spotter/internal/check"
)

// EmptyTree は git の空ツリーの SHA。根コミットを含む範囲の起点に使う。
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Repo はリポジトリのルートディレクトリに対する git 操作をまとめる。
//
// runCache / fileSets は check.Source 系（ChangedFiles/DeletedFiles/DiffLines/Stats/
// BlobSize/Exists）の読み取り専用呼び出しだけをメモ化する。spotter の 1 回の実行の中では
// インデックスや参照（staged の内容、range の from/to が指す commit）は変わらない前提
// なので、同じ引数の呼び出しは同じ結果になる。config の set のような書き込み系や、
// hooks 等が使う run はここに絡めない（run 自体は変えない）。
// mutex は複数 goroutine から同時に使われても runCache/fileSets を壊さないように守る。
type Repo struct {
	Dir string

	mu       sync.Mutex
	runCache map[string]runResult
	fileSets map[string]map[string]struct{}
}

// runResult は cachedRun の結果（出力とエラー）を 1 組にしたもの。
type runResult struct {
	out string
	err error
}

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

// cachedRun は run と同じだが、args をキーに結果（出力・エラーの両方）をメモ化する。
// check.Source 実装の読み取り専用の git 呼び出しからだけ使うこと。
func (r *Repo) cachedRun(args ...string) (string, error) {
	key := strings.Join(args, "\x00")

	r.mu.Lock()
	if cached, ok := r.runCache[key]; ok {
		r.mu.Unlock()
		return cached.out, cached.err
	}
	r.mu.Unlock()

	out, err := r.run(args...)

	r.mu.Lock()
	if r.runCache == nil {
		r.runCache = make(map[string]runResult)
	}
	r.runCache[key] = runResult{out: out, err: err}
	r.mu.Unlock()

	return out, err
}

// fileSet は key に対応するファイル集合を返す。無ければ load で取得してキャッシュする。
func (r *Repo) fileSet(key string, load func() (map[string]struct{}, error)) (map[string]struct{}, error) {
	r.mu.Lock()
	if set, ok := r.fileSets[key]; ok {
		r.mu.Unlock()
		return set, nil
	}
	r.mu.Unlock()

	set, err := load()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.fileSets == nil {
		r.fileSets = make(map[string]map[string]struct{})
	}
	r.fileSets[key] = set
	r.mu.Unlock()

	return set, nil
}

// fileSets のキー。インデックスと tree で名前空間を分け、"index" という名前の ref の
// tree 集合とインデックスの集合が衝突しないようにする。
const (
	fileSetKeyIndex      = "index:"
	fileSetKeyTreePrefix = "tree:"
)

// indexFileSet はステージ済みインデックスに存在するファイル（blob）のパス集合を返す。
// staged モードの Exists の判定対象を、呼び出しごとの `git ls-files` 起動 1 回ではなく
// リポジトリ全体で 1 回の起動にまとめるために使う。
func (r *Repo) indexFileSet() (map[string]struct{}, error) {
	return r.fileSet(fileSetKeyIndex, func() (map[string]struct{}, error) {
		out, err := r.cachedRun("ls-files", "-z", "--cached")
		if err != nil {
			return nil, err
		}
		set := make(map[string]struct{})
		for _, path := range splitNonEmptyTokensZ(out) {
			set[path] = struct{}{}
		}
		return set, nil
	})
}

// treeFileSet は tree（コミットやツリーの参照）に存在するファイル（blob）のパス集合を返す。
// `git ls-tree -r` はサブディレクトリを再帰的に辿った上でエントリ自体（ディレクトリの
// tree エントリ）は返さないが、submodule は commit エントリとして残るため、種別が
// "blob" のものだけを拾ってディレクトリ・submodule を除外する（呼び出し側が問うのは
// 「そのパスにファイルがあるか」だけのため）。
func (r *Repo) treeFileSet(tree string) (map[string]struct{}, error) {
	return r.fileSet(fileSetKeyTreePrefix+tree, func() (map[string]struct{}, error) {
		out, err := r.cachedRun("ls-tree", "-r", "-z", tree)
		if err != nil {
			return nil, err
		}
		set := make(map[string]struct{})
		for _, entry := range splitNonEmptyTokensZ(out) {
			meta, path, ok := strings.Cut(entry, "\t")
			if !ok {
				continue
			}
			fields := strings.Fields(meta)
			if len(fields) >= 2 && fields[1] == "blob" {
				set[path] = struct{}{}
			}
		}
		return set, nil
	})
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

// splitNonEmptyTokensZ は NUL 区切り（`git ... -z`）の出力をトークンに分ける。
// 末尾の区切り文字が作る空トークンは捨てる。
func splitNonEmptyTokensZ(s string) []string {
	var out []string
	for _, tok := range strings.Split(s, "\x00") {
		if tok != "" {
			out = append(out, tok)
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

// InMerge は MERGE_HEAD の有無でマージの途中（`git merge --no-ff`/`git pull` が残す）
// かどうかを返す。`-q --verify` は無いときだけ終了コード 1 を返すため false/nil にし、
// それ以外の失敗（リポジトリ破損等）は区別して error にする。
func (r *Repo) InMerge() (bool, error) {
	cmd := exec.Command("git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
	cmd.Dir = r.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("gitutil: git rev-parse --verify MERGE_HEAD の実行に失敗しました: %w\n%s", err, stderr.String())
	}
	return true, nil
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
// 絶対パスを返す（r.Dir がリポジトリのサブディレクトリでも解決できる）。project スコープの
// スキル設置先（.claude/skills, .agents/skills）をカレントディレクトリに依存せず求めるために使う。
//
// git は Windows でもスラッシュ区切りで返すため、呼び出し元に矯正を要求しないよう
// filepath.FromSlash でこの OS のセパレータに正規化してから返す。
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
// git コマンド自体の失敗（非 0 終了）はエラーにせず false として返す。
func (r *Repo) CommitExists(sha string) (bool, error) {
	if sha == "" {
		return false, nil
	}
	if _, err := r.run("cat-file", "-e", sha+"^{commit}"); err != nil {
		return false, nil
	}
	return true, nil
}

// ResolveCommit は ref（sha を含む）を `^{commit}` に peel して返す。実在しない、または
// tree だけを指す軽量でない tag のように commit に peel できない場合は ok=false
// （pre-push フックが渡す sha は tag オブジェクトのこともあるため、検査対象かどうかの
// 判定にこの区別が要る）。
func (r *Repo) ResolveCommit(ref string) (sha string, ok bool, err error) {
	out, runErr := r.run("rev-parse", "-q", "--verify", ref+"^{commit}")
	if runErr != nil {
		return "", false, nil
	}
	return strings.TrimSpace(out), true, nil
}

// HeadCommit は HEAD が指すコミットの SHA を返す。
func (r *Repo) HeadCommit() (string, error) {
	out, err := r.run("rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitutil: HEAD の解決に失敗しました: %w", err)
	}
	return strings.TrimSpace(out), nil
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

// RangeMessages は range 式に含まれる各コミットのメッセージ本文を、コミットごとに
// 分けたスライスで返す（新しい順。squashed 粒度の免除判定用で、コミットごとに
// トレーラ段落を取り出して判定できるようにするため、連結した 1 本の文字列ではなく
// スライスにしている。範囲内のどれか 1 つのコミットのトレーラ段落に免除トレーラが
// あれば免除が効く、という仕様自体はこの型では表現せず、呼び出し側で判定する）。
//
// `%B%x00` で各コミットのメッセージを NUL 区切りにする。git は --format 出力の
// エントリ間に改行を1つ挟むため、2 件目以降の要素の先頭に付く改行を取り除く。
func (r *Repo) RangeMessages(rangeExpr string) ([]string, error) {
	args := append([]string{"log", "--format=%B%x00"}, strings.Fields(rangeExpr)...)
	out, err := r.run(args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	tokens := strings.Split(out, "\x00")
	messages := make([]string, 0, len(tokens))
	for i, tok := range tokens {
		if i > 0 {
			tok = strings.TrimPrefix(tok, "\n")
		}
		if tok == "" {
			// 末尾のトークンは、最後のコミットの後に git が挟む改行だけが残った
			// 空文字列になる（実在のコミットメッセージが完全に空にはならないため、
			// 実質的にこの末尾ケースだけを捨てることになる）。
			continue
		}
		messages = append(messages, tok)
	}
	return messages, nil
}

// ConfigChangeLog は range 式（"a..b" や複数語の rev-list 引数のどちらも可）に含まれる
// コミットのうち、configPath を変更したものを新しい順で "<短い sha> <件名>" の一覧として
// 返す。pre-push が失敗したとき、検査を足したコミットがどれかを利用者が特定しやすくする
// ために使う。該当コミットが無ければ空スライスを返す。
func (r *Repo) ConfigChangeLog(rangeExpr, configPath string) ([]string, error) {
	args := append([]string{"log", "--format=%h %s"}, strings.Fields(rangeExpr)...)
	args = append(args, "--", configPath)
	out, err := r.run(args...)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
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
	out, err := s.r.cachedRun("diff", "--cached", "--name-only", "--diff-filter=ACMR")
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (s stagedSource) DiffLines(path string) (string, error) {
	return s.r.cachedRun("diff", "--cached", "-U0", "--", path)
}

func (s stagedSource) BlobSize(path string) (int64, error) {
	return s.r.blobSize(":" + path)
}

func (s stagedSource) Stats() ([]check.FileStat, error) {
	out, err := s.r.cachedRun("diff", "--cached", "--numstat", "-z")
	if err != nil {
		return nil, err
	}
	return parseNumstatZ(out)
}

// DeletedFiles は `--no-renames` を付け、改名を「旧パスの削除 + 新パスの追加」として
// 扱う（ChangedFiles の A 側に新パスが入るのと対になる）。`-z` により、空白や改行を
// 含むパスも安全に分割できる。
func (s stagedSource) DeletedFiles() ([]string, error) {
	out, err := s.r.cachedRun("diff", "--cached", "--name-only", "-z", "--no-renames", "--diff-filter=D")
	if err != nil {
		return nil, err
	}
	return splitNonEmptyTokensZ(out), nil
}

func (s stagedSource) Exists(path string) (bool, error) {
	set, err := s.r.indexFileSet()
	if err != nil {
		return false, err
	}
	_, ok := set[path]
	return ok, nil
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
	out, err := s.r.cachedRun("diff", "--name-only", "--diff-filter=ACMR", s.from, s.to)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (s rangeSource) DiffLines(path string) (string, error) {
	return s.r.cachedRun("diff", "-U0", s.from, s.to, "--", path)
}

func (s rangeSource) BlobSize(path string) (int64, error) {
	return s.r.blobSize(s.to + ":" + path)
}

func (s rangeSource) Stats() ([]check.FileStat, error) {
	out, err := s.r.cachedRun("diff", "--numstat", "-z", s.from, s.to)
	if err != nil {
		return nil, err
	}
	return parseNumstatZ(out)
}

// DeletedFiles は stagedSource.DeletedFiles と同じ理由で `--no-renames` と `-z` を使う。
func (s rangeSource) DeletedFiles() ([]string, error) {
	out, err := s.r.cachedRun("diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", s.from, s.to)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyTokensZ(out), nil
}

func (s rangeSource) Exists(path string) (bool, error) {
	set, err := s.r.treeFileSet(s.to)
	if err != nil {
		return false, err
	}
	_, ok := set[path]
	return ok, nil
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
// （削除されたファイルには大きさの上限を当てる対象が無いため）。
func (r *Repo) blobSize(object string) (int64, error) {
	out, err := r.cachedRun("cat-file", "-s", object)
	if err != nil {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gitutil: cat-file -s %s の出力を解析できません: %w", object, err)
	}
	return n, nil
}
