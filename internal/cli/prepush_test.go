package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// newPrePushTestRepo は、bare リポジトリを origin remote に持つ作業リポジトリを作る。
// main を 1 コミットで origin に push・fetch 済みの状態（pre-push フックの標準入力にある
// remote sha が実在する、通常の「更新」push を再現できる状態）で返す。
func newPrePushTestRepo(t *testing.T) (local, remote string) {
	t.Helper()
	local = newCheckTestRepo(t)

	remote = t.TempDir()
	initCmd := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	runGitCLIForCheckTest(t, local, "remote", "add", "origin", remote)
	runGitCLIForCheckTest(t, local, "push", "-q", "origin", "main")
	runGitCLIForCheckTest(t, local, "fetch", "-q", "origin")

	return local, remote
}

// newPrePushTestRepoWithConfig は newPrePushTestRepo に加え、unwanted-files（1 byte 超で
// 違反）の設定を origin まで push・fetch 済みの状態にする。以降のテストでは、この設定
// コミット自身（.spotter.yml は 1 byte を超える）が範囲に含まれず、意図したファイルだけが
// 評価対象になる。
func newPrePushTestRepoWithConfig(t *testing.T) (local, remote string) {
	t.Helper()
	local, remote = newPrePushTestRepo(t)

	writeUnwantedFilesConfig(t, local)
	runGitCLIForCheckTest(t, local, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add config")
	runGitCLIForCheckTest(t, local, "push", "-q", "origin", "main")
	runGitCLIForCheckTest(t, local, "fetch", "-q", "origin")

	return local, remote
}

func zeroSHA() string { return strings.Repeat("0", 40) }

// countNoBigFilesFailures は stderr に出た no-big-files（writeUnwantedFilesConfig が
// 使うキー名）の起動回数を数える。
func countNoBigFilesFailures(stderr string) int {
	return strings.Count(stderr, "no-big-files の検査に失敗しました（")
}

// TestRunCheckPrePushUpdatePushChecksOnlyNewCommits は、既存ブランチへの通常の push
// （remote sha が origin の現在地を指し、ローカルに実在する）で、既に origin にある
// コミットまで遡って評価しないことを確認する。
func TestRunCheckPrePushUpdatePushChecksOnlyNewCommits(t *testing.T) {
	local, _ := newPrePushTestRepoWithConfig(t)
	remoteSHA := runGitCLIForCheckTest(t, local, "rev-parse", "origin/main")

	writeFileAndStage(t, local, "big.txt", "0123456789")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add big")
	localSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")

	stdin := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", localSHA, remoteSHA)

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", "")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("big.txt が新規コミットに含まれるので ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "big.txt") {
		t.Errorf("big.txt が違反として出るはず, got %q", stderr.String())
	}
	if n := countNoBigFilesFailures(stderr.String()); n != 1 {
		t.Errorf("origin に既にある初回・設定コミットまでは遡らず、新規コミット 1 件だけ評価されるはず, got %d 回\n%s", n, stderr.String())
	}
}

// TestRunCheckPrePushNewBranchExcludesRemoteHistory は、新規ブランチの push（remote sha が
// 全 0）で、他の remote-tracking ref（origin/main）に既にあるコミットは検査対象に含めない
// （--not --remotes による除外）ことを確認する。
func TestRunCheckPrePushNewBranchExcludesRemoteHistory(t *testing.T) {
	local, _ := newPrePushTestRepoWithConfig(t)

	runGitCLIForCheckTest(t, local, "checkout", "-q", "-b", "feature")
	writeFileAndStage(t, local, "big.txt", "0123456789")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add big")
	localSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")

	stdin := fmt.Sprintf("refs/heads/feature %s refs/heads/feature %s\n", localSHA, zeroSHA())

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", "")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("feature 側の big.txt は違反のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if n := countNoBigFilesFailures(stderr.String()); n != 1 {
		t.Errorf("origin/main 側のコミットは除外され、feature 固有の 1 コミットだけ評価されるはず, got %d 回\n%s", n, stderr.String())
	}
}

// TestRunCheckPrePushForcePushUnknownRemoteSHAStillChecks は、force push 先の remote sha が
// ローカルに実在しない場合（fetch していない、または相手側で書き換えられた場合）でも、
// remote sha を範囲式に含めず `--not --remotes` だけで検査が実行できることを確認する。
func TestRunCheckPrePushForcePushUnknownRemoteSHAStillChecks(t *testing.T) {
	local, _ := newPrePushTestRepoWithConfig(t)

	writeFileAndStage(t, local, "big.txt", "0123456789")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add big")
	localSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")

	// ローカルに存在しない sha（force push 相手が書き換えた後、まだ fetch していない状態を
	// 想定した架空の値）。
	unknownRemoteSHA := "1111111111111111111111111111111111111111"
	stdin := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", localSHA, unknownRemoteSHA)

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", "")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("remote sha が未知でも検査自体は走って big.txt を検出するはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "big.txt") {
		t.Errorf("big.txt が違反として出るはず, got %q", stderr.String())
	}
}

// TestRunCheckPrePushDeletedRefIsSkippedNotFailed は、削除 push（local sha が全 0）は
// 検査せず、不合格にもしないことを確認する。stderr には理由が 1 行出る。
func TestRunCheckPrePushDeletedRefIsSkippedNotFailed(t *testing.T) {
	local, _ := newPrePushTestRepoWithConfig(t)
	remoteSHA := runGitCLIForCheckTest(t, local, "rev-parse", "origin/main")

	stdin := fmt.Sprintf("refs/heads/gone %s refs/heads/gone %s\n", zeroSHA(), remoteSHA)

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	if err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", ""); err != nil {
		t.Fatalf("削除 push は不合格にしないはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "refs/heads/gone は削除 push のため検査しません") {
		t.Errorf("削除 push を検査しない旨が stderr に出るはず, got %q", stderr.String())
	}
}

// TestRunCheckPrePushNonHeadRefIsSkippedNotFailed は、HEAD ではない位置を指す push
// （古い commit を指す軽量 tag の push など）を検査せず、不合格にもしないことを確認する。
func TestRunCheckPrePushNonHeadRefIsSkippedNotFailed(t *testing.T) {
	local, _ := newPrePushTestRepo(t)
	firstSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")

	writeUnwantedFilesConfig(t, local)
	runGitCLIForCheckTest(t, local, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add config") // HEAD をさらに進める

	stdin := fmt.Sprintf("refs/tags/v1 %s refs/tags/v1 %s\n", firstSHA, zeroSHA())

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	if err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", ""); err != nil {
		t.Fatalf("HEAD 以外の ref は不合格にしないはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "refs/tags/v1 は HEAD ではないため検査しません（CI で検査されます）") {
		t.Errorf("HEAD ではない旨が stderr に出るはず, got %q", stderr.String())
	}
}

// TestRunCheckPrePushUsesRuntimeConfigLikeRange は、pre-push が range モードと同じく
// 実行時（HEAD）の .spotter.yml を使うことを確認する。検査を足すコミットより前の
// コミット（足した時点ではまだ存在しなかった検査）も、HEAD の設定で遡って評価される。
func TestRunCheckPrePushUsesRuntimeConfigLikeRange(t *testing.T) {
	local, _ := newPrePushTestRepo(t)
	remoteSHA := runGitCLIForCheckTest(t, local, "rev-parse", "origin/main")

	// 1st: 検査を足す前に違反ファイルをコミットする。
	writeFileAndStage(t, local, "big.txt", "0123456789")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add big before rule")

	// 2nd: このコミットで初めて unwanted-files を足す（.spotter.yml 自体も 1 byte を
	// 超えるため、このコミット自身も同じ検査に違反する。それでも「足す前のコミットが
	// 遡って落ちる」という主眼は big.txt 側の違反で確認できる）。
	writeUnwantedFilesConfig(t, local)
	runGitCLIForCheckTest(t, local, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add config")
	localSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")

	stdin := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", localSHA, remoteSHA)

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", "")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("検査を足す前のコミットも HEAD の設定で遡って落ちるはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "big.txt") {
		t.Errorf("検査を足す前にコミットした big.txt も違反として出るはず, got %q", stderr.String())
	}
}

// TestRunCheckPrePushDedupesIdenticalRangeExprs は、複数 ref が同じコミットを指す
// （squashed push で複数ブランチを同時に更新するなど）場合、同じ範囲式の検査を
// 重複して走らせないことを確認する。
func TestRunCheckPrePushDedupesIdenticalRangeExprs(t *testing.T) {
	local, _ := newPrePushTestRepoWithConfig(t)

	writeFileAndStage(t, local, "big.txt", "0123456789")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "add big")
	localSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")
	runGitCLIForCheckTest(t, local, "branch", "feature", localSHA)

	stdin := fmt.Sprintf(
		"refs/heads/main %s refs/heads/main %s\nrefs/heads/feature %s refs/heads/feature %s\n",
		localSHA, zeroSHA(), localSHA, zeroSHA(),
	)

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", "")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("big.txt は違反のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if n := countNoBigFilesFailures(stderr.String()); n != 1 {
		t.Errorf("main と feature は同じコミットを指すので、範囲検査は 1 回だけのはず, got %d 回\n%s", n, stderr.String())
	}
	if !strings.Contains(stdout.String(), "refs/heads/main") || !strings.Contains(stdout.String(), "refs/heads/feature") {
		t.Errorf("失敗時の案内には検査した ref が両方出るはず, got %q", stdout.String())
	}
}

// TestRunCheckPrePushMutuallyExclusiveWithMessageAndRange は、newCheckCommand が
// --message / --range / --pre-push を排他フラグとして構成していることを確認する。
func TestRunCheckPrePushMutuallyExclusiveWithMessageAndRange(t *testing.T) {
	cmd := newCheckCommand()
	cmd.SetArgs([]string{"--message", "MSG", "--pre-push", "origin"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err == nil {
		t.Fatal("--message と --pre-push は排他のはずが成功しました")
	}
}
