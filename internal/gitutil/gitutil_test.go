package gitutil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/gitutil"
)

// runGit は git コマンドを実行し、TrimSpace した標準出力を返す。失敗したら t.Fatalf で
// テストを止める。セットアップ用の呼び出し専用で、失敗そのものを検証したいテスト
// （マージコンフリクトなど）では使わず、exec.Command を直接呼ぶ。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newTestRepo(t *testing.T) (*gitutil.Repo, string) {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "spotter test")
	runGit(t, dir, "config", "user.email", "spotter@example.invalid")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "1st")
	sha := runGit(t, dir, "rev-parse", "HEAD")

	return gitutil.New(dir), sha
}

func TestStagedSourceStats(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	// a.txt（既存）に 1 行追加。
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	// バイナリファイルを追加。
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	// リネーム対象のファイルをコミット済みにしてからリネーム + 追記する。
	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("x\ny\nz\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGit(t, dir, "add", "old.go")
	runGit(t, dir, "commit", "-q", "-m", "2nd")
	runGit(t, dir, "mv", "old.go", "new.go")
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("x\ny\nz\nw\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}

	runGit(t, dir, "add", "-A")

	stats, err := repo.StagedSource().Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}

	byPath := map[string]struct {
		added, deleted int
		binary         bool
	}{}
	for _, s := range stats {
		byPath[s.Path] = struct {
			added, deleted int
			binary         bool
		}{s.Added, s.Deleted, s.Binary}
	}

	if got, ok := byPath["a.txt"]; !ok || got.added != 1 || got.deleted != 0 || got.binary {
		t.Errorf("a.txt の統計が想定外です: %+v (ok=%v)", got, ok)
	}
	if got, ok := byPath["bin.dat"]; !ok || !got.binary || got.added != 0 || got.deleted != 0 {
		t.Errorf("bin.dat はバイナリとして 0/0 のはず: %+v (ok=%v)", got, ok)
	}
	if got, ok := byPath["new.go"]; !ok || got.added != 1 || got.deleted != 0 {
		t.Errorf("リネーム後は新パス new.go で、追記した 1 行だけが added のはず: %+v (ok=%v)", got, ok)
	}
	if _, ok := byPath["old.go"]; ok {
		t.Errorf("リネームの旧パス old.go は結果に含まれないはず: %v", stats)
	}
}

func TestRangeSourceStatsIncludesDeletedFiles(t *testing.T) {
	repo, from := newTestRepo(t)
	dir := repo.Dir

	runGit(t, dir, "rm", "-q", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "delete a.txt")
	to := runGit(t, dir, "rev-parse", "HEAD")

	stats, err := repo.RangeSource(from, to).Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}
	if len(stats) != 1 || stats[0].Path != "a.txt" || stats[0].Added != 0 || stats[0].Deleted != 1 {
		t.Fatalf("削除されたファイルも計上されるはず, got %+v", stats)
	}
}

func TestStagedSourceDeletedFilesIncludesRenameOldPath(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "old.go")
	runGit(t, dir, "commit", "-q", "-m", "add old.go")

	// a.txt をただ削除しつつ、old.go を new.go にリネームする。--no-renames を使う
	// DeletedFiles では、リネームも「旧パスの削除」として a.txt と一緒に出てくるはず。
	runGit(t, dir, "rm", "-q", "a.txt")
	runGit(t, dir, "mv", "old.go", "new.go")

	deleted, err := repo.StagedSource().DeletedFiles()
	if err != nil {
		t.Fatalf("DeletedFiles() error: %v", err)
	}

	got := map[string]bool{}
	for _, f := range deleted {
		got[f] = true
	}
	if !got["a.txt"] {
		t.Errorf("削除した a.txt が含まれるはず, got %v", deleted)
	}
	if !got["old.go"] {
		t.Errorf("リネーム元の old.go も削除として含まれるはず, got %v", deleted)
	}
	if got["new.go"] {
		t.Errorf("リネーム先の new.go は DeletedFiles に含まれないはず, got %v", deleted)
	}
}

func TestStagedSourceExists(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	runGit(t, dir, "rm", "-q", "a.txt")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("ディレクトリ作成に失敗しました: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "sub/b.txt")

	src := repo.StagedSource()

	if ok, err := src.Exists("sub/b.txt"); err != nil || !ok {
		t.Errorf("ステージ済みの sub/b.txt は存在するはず, ok=%v err=%v", ok, err)
	}
	if ok, err := src.Exists("a.txt"); err != nil || ok {
		t.Errorf("インデックスから削除済みの a.txt は存在しないはず, ok=%v err=%v", ok, err)
	}
	if ok, err := src.Exists("sub"); err != nil || ok {
		t.Errorf("ディレクトリは存在扱いにしないはず, ok=%v err=%v", ok, err)
	}
	if ok, err := src.Exists("nope.txt"); err != nil || ok {
		t.Errorf("存在しないパスは false のはず, ok=%v err=%v", ok, err)
	}
}

func TestRangeSourceDeletedFilesIncludesRenameOldPath(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "old.go")
	runGit(t, dir, "commit", "-q", "-m", "add old.go")
	from := runGit(t, dir, "rev-parse", "HEAD")

	runGit(t, dir, "rm", "-q", "a.txt")
	runGit(t, dir, "mv", "old.go", "new.go")
	runGit(t, dir, "commit", "-q", "-am", "delete a.txt, rename old.go to new.go")
	to := runGit(t, dir, "rev-parse", "HEAD")

	deleted, err := repo.RangeSource(from, to).DeletedFiles()
	if err != nil {
		t.Fatalf("DeletedFiles() error: %v", err)
	}

	got := map[string]bool{}
	for _, f := range deleted {
		got[f] = true
	}
	if !got["a.txt"] || !got["old.go"] {
		t.Errorf("a.txt と old.go（リネーム元）が両方含まれるはず, got %v", deleted)
	}
	if got["new.go"] {
		t.Errorf("リネーム先の new.go は含まれないはず, got %v", deleted)
	}
}

func TestRangeSourceExists(t *testing.T) {
	repo, from := newTestRepo(t)
	dir := repo.Dir

	runGit(t, dir, "rm", "-q", "a.txt")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("ディレクトリ作成に失敗しました: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "sub/b.txt")
	runGit(t, dir, "commit", "-q", "-am", "delete a.txt, add sub/b.txt")
	to := runGit(t, dir, "rev-parse", "HEAD")

	src := repo.RangeSource(from, to)

	if ok, err := src.Exists("sub/b.txt"); err != nil || !ok {
		t.Errorf("to では sub/b.txt が存在するはず, ok=%v err=%v", ok, err)
	}
	if ok, err := src.Exists("a.txt"); err != nil || ok {
		t.Errorf("to では削除済みの a.txt は存在しないはず, ok=%v err=%v", ok, err)
	}
	if ok, err := src.Exists("sub"); err != nil || ok {
		t.Errorf("ディレクトリは存在扱いにしないはず, ok=%v err=%v", ok, err)
	}
	if ok, err := src.Exists("nope.txt"); err != nil || ok {
		t.Errorf("存在しないパスは false のはず, ok=%v err=%v", ok, err)
	}
}

func TestRangeSourceExistsGitErrorIsNotNotFound(t *testing.T) {
	// 「存在しない」と「git の実行エラー」を区別すること。to に実在しない参照を
	// 渡した場合は false ではなく error を返すべき。
	repo, from := newTestRepo(t)

	src := repo.RangeSource(from, "0000000000000000000000000000000000000000")
	ok, err := src.Exists("a.txt")
	if err == nil {
		t.Fatalf("実在しない to を渡したら error になるはず, ok=%v", ok)
	}
	if ok {
		t.Errorf("error のときは ok も false のはず, got %v", ok)
	}
}

// TestStagedSourceChangedFilesIsMemoizedPerRepo は、同じ Repo から複数回 ChangedFiles() を
// 呼んでも git を再実行せず、最初に取得した結果を返し続けることを確かめる。spotter の
// 1 回の実行の中ではインデックスが変わらない前提のキャッシュなので、Go 側を経由せず
// 直接インデックスを変えても同じ Repo からは反映されない（別の Repo インスタンスなら
// 最新の状態が見える）。
func TestStagedSourceChangedFilesIsMemoizedPerRepo(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	if err := os.WriteFile(filepath.Join(dir, "first.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "first.txt")

	src := repo.StagedSource()
	before, err := src.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error: %v", err)
	}
	if len(before) != 1 || before[0] != "first.txt" {
		t.Fatalf("最初の ChangedFiles() は first.txt だけのはず, got %v", before)
	}

	// 同じ repo.run/cachedRun を経由しない形でインデックスへ 2 個目のファイルを足す。
	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("2\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "second.txt")

	after, err := src.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error (2 回目): %v", err)
	}
	if len(after) != 1 || after[0] != "first.txt" {
		t.Fatalf("同じ Repo からの 2 回目の ChangedFiles() はキャッシュされた結果のはず, got %v", after)
	}

	fresh := gitutil.New(dir).StagedSource()
	freshFiles, err := fresh.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles() error (別 Repo): %v", err)
	}
	got := map[string]bool{}
	for _, f := range freshFiles {
		got[f] = true
	}
	if !got["first.txt"] || !got["second.txt"] {
		t.Errorf("別の Repo インスタンスからは両方のファイルが見えるはず, got %v", freshFiles)
	}
}

// TestStagedSourceExistsIsMemoizedPerRepo は Exists() の元になるインデックスのファイル
// 集合が Repo ごとに 1 回だけ取得され、以後は使い回されることを確かめる。
func TestStagedSourceExistsIsMemoizedPerRepo(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	if err := os.WriteFile(filepath.Join(dir, "cache.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "cache.txt")

	src := repo.StagedSource()
	if ok, err := src.Exists("cache.txt"); err != nil || !ok {
		t.Fatalf("ステージ済みの cache.txt は存在するはず, ok=%v err=%v", ok, err)
	}

	// インデックスから直接（Go 側を経由せず）外す。同じ Repo なら、この変更後も
	// 最初に取得したファイル集合のキャッシュを返し続けるはず。
	runGit(t, dir, "rm", "-q", "--cached", "cache.txt")

	if ok, err := src.Exists("cache.txt"); err != nil || !ok {
		t.Errorf("同じ Repo からの 2 回目の Exists() はキャッシュされた結果 true のはず, ok=%v err=%v", ok, err)
	}

	fresh := gitutil.New(dir).StagedSource()
	if ok, err := fresh.Exists("cache.txt"); err != nil || ok {
		t.Errorf("別の Repo インスタンスでは最新のインデックス（削除済み）が見えるはず, ok=%v err=%v", ok, err)
	}
}

// TestRangeSourceExistsPerToIsIndependent は、同じ Repo から to の異なる RangeSource を
// 複数作っても、Exists() が参照するファイル集合のキャッシュが to ごとに独立していて
// 混ざらないことを確かめる。
func TestRangeSourceExistsPerToIsIndependent(t *testing.T) {
	repo, from := newTestRepo(t)
	dir := repo.Dir

	runGit(t, dir, "rm", "-q", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "delete a.txt")
	to1 := runGit(t, dir, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGit(t, dir, "add", "b.txt")
	runGit(t, dir, "commit", "-q", "-m", "add b.txt")
	to2 := runGit(t, dir, "rev-parse", "HEAD")

	if ok, err := repo.RangeSource(from, to1).Exists("a.txt"); err != nil || ok {
		t.Errorf("to1 の時点で a.txt は削除済みのはず, ok=%v err=%v", ok, err)
	}
	if ok, err := repo.RangeSource(from, to1).Exists("b.txt"); err != nil || ok {
		t.Errorf("to1 の時点で b.txt はまだ存在しないはず, ok=%v err=%v", ok, err)
	}
	if ok, err := repo.RangeSource(from, to2).Exists("b.txt"); err != nil || !ok {
		t.Errorf("to2 の時点で b.txt は存在するはず, ok=%v err=%v", ok, err)
	}
	if ok, err := repo.RangeSource(from, to1).Exists("a.txt"); err != nil || ok {
		t.Errorf("to1 を再度参照しても a.txt は存在しないままのはず, ok=%v err=%v", ok, err)
	}
}

func TestCommitExists(t *testing.T) {
	repo, sha := newTestRepo(t)

	ok, err := repo.CommitExists(sha)
	if err != nil {
		t.Fatalf("CommitExists() error: %v", err)
	}
	if !ok {
		t.Errorf("実在するコミットのはずが CommitExists() = false")
	}
}

func TestCommitExistsAllZero(t *testing.T) {
	repo, _ := newTestRepo(t)

	ok, err := repo.CommitExists("0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("CommitExists() error: %v", err)
	}
	if ok {
		t.Errorf("全ゼロの SHA は実在しないはずが CommitExists() = true")
	}
}

func TestCommitExistsEmpty(t *testing.T) {
	repo, _ := newTestRepo(t)

	ok, err := repo.CommitExists("")
	if err != nil {
		t.Fatalf("CommitExists() error: %v", err)
	}
	if ok {
		t.Errorf("空文字は実在しないはずが CommitExists() = true")
	}
}

func TestInMergeNormal(t *testing.T) {
	repo, _ := newTestRepo(t)

	inMerge, err := repo.InMerge()
	if err != nil {
		t.Fatalf("InMerge() error: %v", err)
	}
	if inMerge {
		t.Errorf("通常時は InMerge() = false のはずが true")
	}
}

func TestInMergeDuringConflict(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nfeature\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGit(t, dir, "commit", "-q", "-am", "feature change")

	runGit(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nmain\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGit(t, dir, "commit", "-q", "-am", "main change")

	// コンフリクトするマージなので、コミットされないまま MERGE_HEAD が残る。失敗そのものを
	// 検証したいので runGit ではなく exec.Command を直接呼ぶ。
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("コンフリクトするマージのはずが成功しました: %s", out)
	}

	inMerge, err := repo.InMerge()
	if err != nil {
		t.Fatalf("InMerge() error: %v", err)
	}
	if !inMerge {
		t.Errorf("コンフリクト中は InMerge() = true のはずが false")
	}

	runGit(t, dir, "merge", "--abort")

	inMerge, err = repo.InMerge()
	if err != nil {
		t.Fatalf("InMerge() error: %v", err)
	}
	if inMerge {
		t.Errorf("merge --abort 後は InMerge() = false のはずが true")
	}
}

func TestTopLevel(t *testing.T) {
	repo, _ := newTestRepo(t)

	top, err := repo.TopLevel()
	if err != nil {
		t.Fatalf("TopLevel() error: %v", err)
	}

	if !sameDir(t, top, repo.Dir) {
		t.Errorf("TopLevel() = %q, want 同じディレクトリを指す %q", top, repo.Dir)
	}
}

func TestTopLevelFromSubdirectory(t *testing.T) {
	repo, _ := newTestRepo(t)

	sub := filepath.Join(repo.Dir, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("サブディレクトリの作成に失敗しました: %v", err)
	}

	subRepo := gitutil.New(sub)

	top, err := subRepo.TopLevel()
	if err != nil {
		t.Fatalf("TopLevel() error: %v", err)
	}

	if !sameDir(t, top, repo.Dir) {
		t.Errorf("サブディレクトリからの TopLevel() = %q, want 同じディレクトリを指す %q", top, repo.Dir)
	}
}

// sameDir は 2 つのパスが同一ディレクトリを指すかどうかを os.SameFile で判定する
// （シンボリックリンクの解決やパス表記の揺れを吸収する。macOS の /tmp → /private/tmp
// のようなケースを filepath.EvalSymlinks + 文字列比較より確実に扱える）。
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatalf("os.Stat(%q): %v", a, err)
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatalf("os.Stat(%q): %v", b, err)
	}
	return os.SameFile(fa, fb)
}

func TestExistsIndexAndRefNamedIndexDoNotShareCache(t *testing.T) {
	repo, from := newTestRepo(t)
	dir := repo.Dir

	// "index" という名前のブランチは a.txt を含む初期コミットを指し、
	// インデックスからは a.txt を消しておく。
	runGit(t, dir, "branch", "index", from)
	runGit(t, dir, "rm", "-q", "--cached", "a.txt")

	if ok, err := repo.StagedSource().Exists("a.txt"); err != nil || ok {
		t.Errorf("インデックスでは a.txt は削除済みのはず, ok=%v err=%v", ok, err)
	}
	if ok, err := repo.RangeSource(from, "index").Exists("a.txt"); err != nil || !ok {
		t.Errorf("ブランチ index には a.txt があるはず, ok=%v err=%v", ok, err)
	}
}
