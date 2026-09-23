package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCheck は internal package レベルの repoRoot（"."）を常に git リポジトリとして
// 扱うため、このテストでは t.Chdir で一時リポジトリに移動して隔離する。

func runGitCLIForCheckTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newCheckTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGitCLIForCheckTest(t, dir, "init", "-q", "-b", "main")
	runGitCLIForCheckTest(t, dir, "config", "user.name", "spotter test")
	runGitCLIForCheckTest(t, dir, "config", "user.email", "spotter@example.invalid")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", "a.txt")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "1st")

	return dir
}

// writeUnwantedFilesConfig は「1 byte を超えるファイルのステージを拒否する」だけの
// 最小構成を書く。ステージした大きいファイルが違反として検出されるかどうかで、
// 検査が実際に走ったかどうかを判定するために使う。
func writeUnwantedFilesConfig(t *testing.T, dir string) {
	t.Helper()
	content := "checks:\n  no-big-files:\n    type: unwanted-files\n    max_bytes: 1\n"
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}
}

func TestRunCheckFailsOnViolationWhenNotMerging(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeUnwantedFilesConfig(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", "big.txt")

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", "", "", "")
	if err != ErrCheckFailed {
		t.Fatalf("マージ中でなければ違反を検出して ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
	}
}

// TestRunCheckSkipsDuringMerge は、コンフリクト解消待ちで MERGE_HEAD が残っている
// 状態（`git merge --no-ff` の途中や `git commit` 前）では、本来なら検出されるはずの
// 違反があっても検査自体を走らせず、成功終了することを確認する
// （CI の --range が RevListNoMerges でマージコミットを除外しているのと揃える）。
func TestRunCheckSkipsDuringMerge(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeUnwantedFilesConfig(t, dir)
	runGitCLIForCheckTest(t, dir, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "add config")

	runGitCLIForCheckTest(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nfeature\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-am", "feature change")

	runGitCLIForCheckTest(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nmain\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-am", "main change")

	mergeCmd := exec.Command("git", "merge", "feature")
	mergeCmd.Dir = dir
	if out, err := mergeCmd.CombinedOutput(); err == nil {
		t.Fatalf("コンフリクトするマージのはずが成功しました: %s", out)
	}

	// 本来なら unwanted-files が検出するはずの違反を、コンフリクト解消と一緒に
	// ステージしておく（マージ中でなければ ErrCheckFailed になる内容）。
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", "big.txt")

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", "", "", ""); err != nil {
		t.Fatalf("マージ中は検査をスキップして成功終了するはずが: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "マージコミットのため検査しません") {
		t.Errorf("スキップした旨が stderr に出るはず, got %q", stderr.String())
	}
}

// TestRunCheckSkipsDuringMergeWithMessageFile は --message を渡す commit-msg フックの
// 経路でも、MERGE_HEAD が残っていれば同じくスキップされることを確認する
// （コンフリクト解消後の `git commit` はこの経路を通る）。
func TestRunCheckSkipsDuringMergeWithMessageFile(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeUnwantedFilesConfig(t, dir)
	runGitCLIForCheckTest(t, dir, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "add config")

	runGitCLIForCheckTest(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nfeature\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-am", "feature change")

	runGitCLIForCheckTest(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nmain\n"), 0o644); err != nil {
		t.Fatalf("ファイル書き込みに失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-am", "main change")

	mergeCmd := exec.Command("git", "merge", "feature")
	mergeCmd.Dir = dir
	if out, err := mergeCmd.CombinedOutput(); err == nil {
		t.Fatalf("コンフリクトするマージのはずが成功しました: %s", out)
	}

	msgPath := filepath.Join(dir, "MERGE_MSG_FOR_TEST")
	if err := os.WriteFile(msgPath, []byte("Merge branch 'feature'\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", ""); err != nil {
		t.Fatalf("マージ中は --message でもスキップして成功終了するはずが: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "マージコミットのため検査しません") {
		t.Errorf("スキップした旨が stderr に出るはず, got %q", stderr.String())
	}
}
