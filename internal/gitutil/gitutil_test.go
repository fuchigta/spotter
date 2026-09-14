package gitutil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/gitutil"
)

func newTestRepo(t *testing.T) (*gitutil.Repo, string) {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-q", "-b", "main")
	run("config", "user.name", "spotter test")
	run("config", "user.email", "spotter@example.invalid")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "1st")
	sha := run("rev-parse", "HEAD")

	return gitutil.New(dir), sha
}

func TestStagedSourceStats(t *testing.T) {
	repo, _ := newTestRepo(t)
	dir := repo.Dir

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

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
	run("add", "old.go")
	run("commit", "-q", "-m", "2nd")
	run("mv", "old.go", "new.go")
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("x\ny\nz\nw\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}

	run("add", "-A")

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

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("rm", "-q", "a.txt")
	run("commit", "-q", "-m", "delete a.txt")
	to := run("rev-parse", "HEAD")

	stats, err := repo.RangeSource(from, to).Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}
	if len(stats) != 1 || stats[0].Path != "a.txt" || stats[0].Added != 0 || stats[0].Deleted != 1 {
		t.Fatalf("削除されたファイルも計上されるはず, got %+v", stats)
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
