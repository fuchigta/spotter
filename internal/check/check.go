// Package check は個々の検査（doc-sync, unwanted-files など）が共通で使う型を定義する。
package check

// Granularity は範囲モードでの起動粒度。検査ごとに違う範囲の意味論を表す。
type Granularity string

const (
	// GranularitySquashed は範囲全体を 1 回の比較としてまとめて見る。
	// 後からドキュメントを直すコミットを足せば通る（doc-sync 向け）。
	GranularitySquashed Granularity = "squashed"
	// GranularityPerCommit は範囲内のコミットごとに 1 回ずつ見る。
	// 後から消しても履歴に残るため直らない（unwanted-files, commit-subject 向け）。
	GranularityPerCommit Granularity = "per-commit"
	// GranularityWorktree は staged/range を問わず、現在の作業ツリーを 1 回だけ見る
	// （doc-paths 向け）。コミットメッセージにも依存しないため、免除トレーラも存在しない。
	GranularityWorktree Granularity = "worktree"
)

// Source は 1 回の比較（ステージ済み、またはある範囲）で検査が参照できるものを抽象化する。
// 検査本体は staged/range の違いを知る必要がない。GranularityWorktree の検査では使われない。
type Source interface {
	// ChangedFiles は追加・変更・コピー・改名されたファイルの一覧を返す（削除は含まない）。
	ChangedFiles() ([]string, error)
	// DiffLines はそのファイルの差分行（コンテキスト無し）を返す。
	DiffLines(path string) (string, error)
	// BlobSize はそのファイルの中身のバイト数を返す。
	BlobSize(path string) (int64, error)
	// Stats は変更量（git diff --numstat 相当）をファイルごとに返す。ChangedFiles と違い
	// 削除されたファイルも含む（diff-size が「大量削除」を捕まえるために必要）。
	Stats() ([]FileStat, error)
	// DeletedFiles は削除されたファイルの一覧を返す。改名元のパスも含む
	// （改名は旧パスの削除でもあるため。ChangedFiles 側には改名先が入る）。
	DeletedFiles() ([]string, error)
	// Exists は比較の終点にそのパスのファイルが存在するかを返す。staged モードでは
	// インデックス、range モードでは to のツリーを見る（作業ツリーは見ない）。
	Exists(path string) (bool, error)
}

// FileStat は 1 ファイルぶんの変更量。
type FileStat struct {
	// Path はファイルパス。リネームの場合は新パス側を使う
	// （gitutil の numstat 解析を参照）。
	Path string
	// Added はそのファイルへの追加行数。
	Added int
	// Deleted はそのファイルからの削除行数。
	Deleted int
	// Binary はバイナリファイルかどうか。numstat がバイナリに対して "-" を返すため、
	// その場合 Added/Deleted は 0 のまま、この値だけを true にする。
	Binary bool
}

// Context は 1 回の検査起動で検査本体に渡す入力をまとめる。
type Context struct {
	// Root はリポジトリのルート（カレントディレクトリからの相対、または絶対パス）。
	// GranularityWorktree の検査（doc-paths など）が現在のツリーを直接読むために使う。
	Root string
	// Source は staged/range の差分。GranularityWorktree の検査では nil。
	Source Source
	// Message はそのコミット（またはこれからコミットされる内容）のメッセージ本文。
	// 免除トレーラの判定はホスト（cli パッケージ）が済ませてから Run を呼ぶが、
	// commit-subject のようにメッセージの中身自体を検証したい検査はここから参照する。
	Message string
	// Range は staged/range どちらのモードかと、range モードでの生の比較両端。
	// command 型検査（外部プロセスに --from/--to を渡す必要がある）向け。
	// staged モードでは nil。
	Range *RangeRef
}

// RangeRef は range モードでの比較両端の生の git 参照。
type RangeRef struct {
	From string
	To   string
}

// Violation は 1 件の検査結果。
type Violation struct {
	// Summary は違反の見出し（例: "internal/cli/*.go を変更していますが、README.md が
	// 一緒に入っていません:"）。
	Summary string
	// Files はその違反に関係するファイル一覧。
	Files []string
	// Target はスコープ付き免除（例: "Doc-Sync: skip[docs/foo.md] 理由"）と照合するキー。
	// 検査が ScopedExemptable を実装している場合、ExemptTargets() が返す一覧の要素と
	// 一致する値をここに入れる。空文字列は「スコープ付き免除では対象にならない」ことを表し、
	// スコープ付き免除があってもこの Violation は免除されない。
	Target string
}

// Runner は 1 つの検査インスタンスを表す。
type Runner interface {
	// Granularity はこの検査を範囲モードでどう起動するか。
	Granularity() Granularity
	// Run は 1 回の比較を検査し、違反があれば返す（無ければ空スライス）。
	Run(ctx Context) ([]Violation, error)
}

// ScopedExemptable は、免除トレーラの対象を検査の一部に絞れる（スコープ付き免除に対応する）
// ことを表す任意インターフェイス。Runner がこれを実装していない場合、その検査の免除トレーラは
// 従来どおり検査全体にしか効かない（skip[対象] を書くと cli 側がエラーにする）。
type ScopedExemptable interface {
	// ExemptTargets はスコープ付き免除で指定できる対象の一覧を返す。Violation.Target の
	// 取り得る値と一致する（doc-sync なら pairs の doc パスの一覧）。
	ExemptTargets() []string
}
