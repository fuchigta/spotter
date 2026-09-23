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

// writeDocSyncConfig は 1 つの pair だけを持つ doc-sync 設定を書く。
func writeDocSyncConfig(t *testing.T, dir string) {
	t.Helper()
	content := "checks:\n" +
		"  doc-sync:\n" +
		"    type: doc-sync\n" +
		"    pairs:\n" +
		"      - paths: 'a/*.go'\n" +
		"        doc: DOC.md\n"
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}
}

func writeFileAndCommit(t *testing.T, dir, rel, content, commitMessage string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("ディレクトリ作成に失敗しました: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", rel)
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", commitMessage)
}

// TestRunCheckSquashedExemptLooksAtEachCommitsTrailerParagraph は、squashed 粒度
// （doc-sync）の免除判定が「範囲内のどれか 1 コミットのトレーラ段落」を見ることを、
// 実際の複数コミットの範囲で確認する。
//
//   - 1 コミット目は本文の途中（最後の段落ではない）に skip を書いており、効かない
//   - 2 コミット目は最後の段落に skip を書いており、効く
//
// squashed は範囲全体をまとめて 1 回見るため、どちらのコミットの skip も範囲全体の
// 免除判定に候補として渡るが、トレーラ段落の形をしている 2 コミット目の分だけが
// 実際に免除として成立する。
func TestRunCheckSquashedExemptLooksAtEachCommitsTrailerParagraph(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeDocSyncConfig(t, dir)
	runGitCLIForCheckTest(t, dir, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "add config")

	writeFileAndCommit(t, dir, "a/foo.go", "package a\n",
		"feat: 1st\n\nDoc-Sync: skip 本文途中の理由\n\n続きの説明文")
	writeFileAndCommit(t, dir, "a/bar.go", "package a\n",
		"feat: 2nd\n\n説明\n\nDoc-Sync: skip 最後の段落の理由")

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", "", "HEAD~2..HEAD", "")
	if err != nil {
		t.Fatalf("2コミット目の末尾段落の skip で範囲全体が免除されるはずが: %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "免除されました（Doc-Sync: skip 最後の段落の理由）") {
		t.Errorf("2コミット目のトレーラ段落の理由で免除された旨が出るはず, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "本文途中の理由") {
		t.Errorf("1コミット目の本文途中の skip は免除として使われないはず, got %q", stdout.String())
	}
}
