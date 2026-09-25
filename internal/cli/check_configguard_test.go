package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAndCommitSpotterYML は .spotter.yml を書いてコミットする（比較元として使う
// HEAD 時点の設定を用意するため）。
func writeAndCommitSpotterYML(t *testing.T, dir, content, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", ".spotter.yml")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", subject)
}

// stageSpotterYML は .spotter.yml をディスクに書いてステージする（比較の終点として使う。
// config.Load はディスクから読むため、内容はステージした内容と一致させる）。
func stageSpotterYML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", ".spotter.yml")
}

func writeMessageFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "MSG")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}
	return p
}

// TestRunConfigGuardIntroductionPasses は、比較元（HEAD）に .spotter.yml 自体が
// 無い（これから spotter を導入する）場合、config-guard が合格することを確認する。
func TestRunConfigGuardIntroductionPasses(t *testing.T) {
	dir := newCheckTestRepo(t) // .spotter.yml を含まない初回コミットだけがある

	stageSpotterYML(t, dir, "checks:\n  config-guard:\n    type: config-guard\n")

	msg := writeMessageFile(t, dir, "feat: 何か\n")

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard"); err != nil {
		t.Fatalf("比較元に設定ファイルが無ければ合格のはず, got %v (stderr=%s)", err, stderr.String())
	}
}

// TestRunConfigGuardCatchesLimitRaisedInSameCommitThatAddsIt は、config-guard を
// 追加するのと同じコミットで別の検査の上限を緩めても、終点にしかない config-guard の
// キーで起動して検出することを確認する（比較元・終点の和での起動）。
func TestRunConfigGuardCatchesLimitRaisedInSameCommitThatAddsIt(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n  diff-size:\n    type: diff-size\n    max_lines: 10\n",
		"add diff-size")

	stageSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 100\n")

	msg := writeMessageFile(t, dir, "feat: 何か\n")

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("max_lines を上げているので ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "max_lines") {
		t.Errorf("stderr に max_lines の緩和が出るはず, got %q", stderr.String())
	}
}

// TestRunConfigGuardCatchesItsOwnRemoval は、config-guard 自身が checks から消えても、
// 比較元にキーが残っていれば（同じか厳しい別キーへの改名でもない限り）検出することを
// only を指定した経路・指定しない経路の両方で確認する（自己参照的な緩和の検出）。
func TestRunConfigGuardCatchesItsOwnRemoval(t *testing.T) {
	base := "checks:\n" +
		"  config-guard:\n    type: config-guard\n" +
		"  diff-size:\n    type: diff-size\n    max_lines: 1000\n"
	target := "checks:\n" +
		"  diff-size:\n    type: diff-size\n    max_lines: 1000\n"

	tests := []struct {
		name string
		only string
	}{
		{"only を指定", "config-guard"},
		{"only 無し（自動で起動）", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCheckTestRepo(t)
			writeAndCommitSpotterYML(t, dir, base, "add config-guard")
			stageSpotterYML(t, dir, target)
			msg := writeMessageFile(t, dir, "feat: 何か\n")

			t.Chdir(dir)
			var stdout, stderr bytes.Buffer
			err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", tt.only)
			if !errors.Is(err, ErrCheckFailed) {
				t.Fatalf("config-guard 自身の削除を検出して ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), "checks.config-guard") {
				t.Errorf("stderr に checks.config-guard の削除が出るはず, got %q", stderr.String())
			}
		})
	}
}

// TestRunConfigGuardExemptResolvedFromBaseConfig は、免除設定（トレーラ名）を
// 実行時の設定ではなく比較元（HEAD）の設定から解決することを確認する。比較元の
// トレーラ名（Guard-Skip）での免除は効くが、終点だけで変えたトレーラ名
// （Other-Skip）での免除は効かない。
func TestRunConfigGuardExemptResolvedFromBaseConfig(t *testing.T) {
	base := "checks:\n" +
		"  config-guard:\n    type: config-guard\n    exempt:\n      trailer: Guard-Skip\n" +
		"  diff-size:\n    type: diff-size\n    max_lines: 10\n"
	target := "checks:\n" +
		"  config-guard:\n    type: config-guard\n    exempt:\n      trailer: Other-Skip\n" +
		"  diff-size:\n    type: diff-size\n    max_lines: 100\n"

	t.Run("比較元のトレーラ名なら免除される", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		writeAndCommitSpotterYML(t, dir, base, "add config-guard")
		stageSpotterYML(t, dir, target)
		msg := writeMessageFile(t, dir, "feat: 何か\n\nGuard-Skip: skip テストのため\n")

		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard"); err != nil {
			t.Fatalf("比較元のトレーラ名での免除は効くはず, got %v (stderr=%s)", err, stderr.String())
		}
	})

	t.Run("終点だけのトレーラ名では免除されない", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		writeAndCommitSpotterYML(t, dir, base, "add config-guard")
		stageSpotterYML(t, dir, target)
		msg := writeMessageFile(t, dir, "feat: 何か\n\nOther-Skip: skip テストのため\n")

		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard")
		if !errors.Is(err, ErrCheckFailed) {
			t.Fatalf("終点だけのトレーラ名は比較元の免除設定に無いので効かないはず, got %v (stderr=%s)", err, stderr.String())
		}
	})
}

// TestRunConfigGuardSkippedForUnrelatedOnlyKey は、only に config-guard 以外の
// キーを指定したとき、config-guard は起動せず、指定した検査だけが評価されることを
// 確認する。
func TestRunConfigGuardSkippedForUnrelatedOnlyKey(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 1\n",
		"add config-guard")

	// diff-size の上限を大きく上げる（config-guard から見れば緩和）が、only=diff-size
	// なので config-guard は起動しないはず。diff-size 自身は違反しないよう十分に
	// 大きい上限にする。
	stageSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 100000\n")
	msg := writeMessageFile(t, dir, "feat: 何か\n")

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "diff-size"); err != nil {
		t.Fatalf("only=diff-size では diff-size 自身は違反しないはず, got %v (stderr=%s)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "config-guard") {
		t.Errorf("only=diff-size のときは config-guard が起動しないはず, got %q", stderr.String())
	}
}

// TestRunConfigGuardOnlyUnknownKeyIsError は、config-guard としても通常の検査としても
// 解決できない only を指定したら error になることを確認する（既存の「存在しない検査名は
// error」の挙動を config-guard 導入後も保つ）。
func TestRunConfigGuardOnlyUnknownKeyIsError(t *testing.T) {
	dir := newCheckTestRepo(t)
	stageSpotterYML(t, dir, "checks: {}\n")
	msg := writeMessageFile(t, dir, "feat: 何か\n")

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "no-such-check")
	if err == nil || errors.Is(err, ErrCheckFailed) {
		t.Fatalf("存在しない検査名を only に渡したら error になるはず, got %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-check") {
		t.Errorf("エラーに検査名が含まれるはず, got %v", err)
	}
}

// TestRunConfigGuardOutsideRepoConfigPath は、--config がリポジトリの外を指す場合、
// 実行時の設定に config-guard が無ければ何もせず成功し、あれば error になることを
// 確認する。
func TestRunConfigGuardOutsideRepoConfigPath(t *testing.T) {
	dir := newCheckTestRepo(t)
	outside := t.TempDir()
	outsideConfig := filepath.Join(outside, ".spotter.yml")

	t.Run("実行時の設定に config-guard が無ければ何もしない", func(t *testing.T) {
		if err := os.WriteFile(outsideConfig, []byte("checks: {}\n"), 0o644); err != nil {
			t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
		}
		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		if err := runCheck(&stdout, &stderr, outsideConfig, "", "", ""); err != nil {
			t.Fatalf("config-guard が無ければ --config がリポジトリ外でも成功するはず, got %v (stderr=%s)", err, stderr.String())
		}
	})

	t.Run("実行時の設定に config-guard があれば error", func(t *testing.T) {
		if err := os.WriteFile(outsideConfig, []byte("checks:\n  config-guard:\n    type: config-guard\n"), 0o644); err != nil {
			t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
		}
		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		err := runCheck(&stdout, &stderr, outsideConfig, "", "", "")
		if err == nil || errors.Is(err, ErrCheckFailed) {
			t.Fatalf("config-guard があるのに --config がリポジトリ外なら error になるはず, got %v", err)
		}
	})
}

// TestRunCheckPrePushCatchesConfigGuardLoosening は、pre-push 経路（runCheckPrePush）
// でも config-guard が push しようとしている範囲の比較元・終点で起動し、緩和を検出する
// ことを確認する（squashed 粒度の検査は runCheckKey と同じ経路で走るはず）。
func TestRunCheckPrePushCatchesConfigGuardLoosening(t *testing.T) {
	local, _ := newPrePushTestRepo(t)
	writeAndCommitSpotterYML(t, local,
		"checks:\n  diff-size:\n    type: diff-size\n    max_lines: 10\n",
		"add diff-size")
	runGitCLIForCheckTest(t, local, "push", "-q", "origin", "main")
	runGitCLIForCheckTest(t, local, "fetch", "-q", "origin")
	remoteSHA := runGitCLIForCheckTest(t, local, "rev-parse", "origin/main")

	stageSpotterYML(t, local,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 100\n")
	runGitCLIForCheckTest(t, local, "commit", "-q", "-m", "loosen diff-size")
	localSHA := runGitCLIForCheckTest(t, local, "rev-parse", "HEAD")

	stdin := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", localSHA, remoteSHA)

	t.Chdir(local)
	var stdout, stderr bytes.Buffer
	err := runCheckPrePush(strings.NewReader(stdin), &stdout, &stderr, ".spotter.yml", "origin", "")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("max_lines を上げているので ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "max_lines") {
		t.Errorf("stderr に max_lines の緩和が出るはず, got %q", stderr.String())
	}
}

// TestRunDoctorReportsConfigGuardActiveAtHeadOnly は、実行時の設定に config-guard が
// 無くても HEAD の設定にあれば、doctor がその旨を情報として表示し、それでも成功終了する
// ことを確認する。
func TestRunDoctorReportsConfigGuardActiveAtHeadOnly(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir, "checks:\n  config-guard:\n    type: config-guard\n", "add config-guard")
	// ディスク上（config.Load が読む実行時の設定）だけ config-guard を外す。ステージ・
	// コミットはしないため、HEAD にはまだ config-guard が残る。
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte("checks: {}\n"), 0o644); err != nil {
		t.Fatalf(".spotter.yml の書き換えに失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout bytes.Buffer
	if err := runDoctor(&stdout, ".spotter.yml"); err != nil {
		t.Fatalf("情報表示のみで成功終了するはず: %v (stdout=%s)", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "config-guard（実行時の設定には無いが、HEAD の .spotter.yml にあるため") {
		t.Errorf("HEAD にだけ config-guard がある旨が出るはず, got %q", stdout.String())
	}
}
