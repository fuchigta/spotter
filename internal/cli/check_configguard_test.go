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
// トレーラ名（Guard-Skip）での対象付き免除は効くが、終点だけで変えたトレーラ名
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
		// exempt.trailer 自体（Guard-Skip → Other-Skip）も checks.config-guard の緩和として
		// 報告されるため、この対象も一緒に免除する（このテストで確かめたいのはトレーラ名の
		// 解決元であって、その緩和自体の是非ではない）。
		msg := writeMessageFile(t, dir, "feat: 何か\n\nGuard-Skip: skip[checks.diff-size,checks.config-guard] テストのため\n")

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
		msg := writeMessageFile(t, dir, "feat: 何か\n\nOther-Skip: skip[checks.diff-size] テストのため\n")

		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard")
		if !errors.Is(err, ErrCheckFailed) {
			t.Fatalf("終点だけのトレーラ名は比較元の免除設定に無いので効かないはず, got %v (stderr=%s)", err, stderr.String())
		}
	})
}

// TestRunConfigGuardUnscopedExemptionDoesNotExempt は、対象を絞らない全体免除
// （skip <理由>）では config-guard の違反は免除されず、案内が表示されることを確認する。
func TestRunConfigGuardUnscopedExemptionDoesNotExempt(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 10\n",
		"add config-guard")
	stageSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 100\n")
	msg := writeMessageFile(t, dir, "feat: 何か\n\nConfig-Guard: skip 対象を絞らない免除のため\n")

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("対象を絞らない免除では免除されないはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "max_lines") {
		t.Errorf("stderr に max_lines の緩和が出るはず, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "config-guard の免除には対象の指定（skip[対象]）が要ります") {
		t.Errorf("stdout に案内が出るはず, got %q", stdout.String())
	}
}

// TestRunConfigGuardScopedExemptionUnknownTargetIsError は、免除できる対象の一覧に無い
// 対象（書き間違い）を指定したら error になることを確認する。
func TestRunConfigGuardScopedExemptionUnknownTargetIsError(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 10\n",
		"add config-guard")
	stageSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  diff-size:\n    type: diff-size\n    max_lines: 100\n")
	msg := writeMessageFile(t, dir, "feat: 何か\n\nConfig-Guard: skip[checks.no-such-key] 書き間違い\n")

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard")
	if err == nil || errors.Is(err, ErrCheckFailed) {
		t.Fatalf("免除できる対象の一覧に無い対象を指定したら error になるはず, got %v", err)
	}
	if !strings.Contains(err.Error(), "checks.no-such-key") {
		t.Errorf("エラーに対象名が含まれるはず, got %v", err)
	}
}

// TestRunConfigGuardScopedExemptionTargetOnlyInBaseIsAccepted は、比較元にだけある
// checks.<key>（削除された検査）も免除の対象として指定できることを確認する
// （configdiff.ExemptTargets は base・target の和を対象にするため）。
func TestRunConfigGuardScopedExemptionTargetOnlyInBaseIsAccepted(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n  config-guard:\n    type: config-guard\n  old-check:\n    type: doc-links\n",
		"add old-check")
	stageSpotterYML(t, dir, "checks:\n  config-guard:\n    type: config-guard\n")
	msg := writeMessageFile(t, dir, "feat: 何か\n\nConfig-Guard: skip[checks.old-check] もう使わないため\n")

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard"); err != nil {
		t.Fatalf("比較元にだけある検査キーへの対象付き免除は効くはず, got %v (stderr=%s)", err, stderr.String())
	}
}

// TestRunConfigGuardRequiredVersionAndTypeAndConfigPathTargets は、
// required_version・types.<t>・設定ファイル全体（未知のトップレベルキー）の
// それぞれの緩和を、その対象名で個別に免除できることを確認する。
func TestRunConfigGuardRequiredVersionAndTypeAndConfigPathTargets(t *testing.T) {
	t.Run("required_version", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		writeAndCommitSpotterYML(t, dir,
			"required_version: v2.0.0\nchecks:\n  config-guard:\n    type: config-guard\n",
			"add required_version")
		stageSpotterYML(t, dir,
			"required_version: v1.0.0\nchecks:\n  config-guard:\n    type: config-guard\n")
		msg := writeMessageFile(t, dir, "feat: 何か\n\nConfig-Guard: skip[required_version] バイナリの更新が間に合わないため\n")

		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard"); err != nil {
			t.Fatalf("required_version を対象にした免除は効くはず, got %v (stderr=%s)", err, stderr.String())
		}
	})

	t.Run("types.<t>", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		writeAndCommitSpotterYML(t, dir,
			"types:\n  my-cmd:\n    command: bash\n    args: [old.sh]\n    default: {granularity: worktree}\n"+
				"checks:\n  config-guard:\n    type: config-guard\n  my-check:\n    type: my-cmd\n",
			"add my-cmd")
		stageSpotterYML(t, dir,
			"types:\n  my-cmd:\n    command: bash\n    args: [new.sh]\n    default: {granularity: worktree}\n"+
				"checks:\n  config-guard:\n    type: config-guard\n  my-check:\n    type: my-cmd\n")
		msg := writeMessageFile(t, dir, "feat: 何か\n\nConfig-Guard: skip[types.my-cmd] スクリプトの置き場所を変えたため\n")

		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard"); err != nil {
			t.Fatalf("types.<t> を対象にした免除は効くはず, got %v (stderr=%s)", err, stderr.String())
		}
	})

	t.Run("設定ファイル全体（未知のトップレベルキー）", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		// target（実行時の設定として config.Load にも読まれる）は未知のキーを持てない
		// （持つと config.Load 自体が起動時エラーになる）ため、base 側にだけ未知のキーを
		// 置き、target でそのキーごと消す（値が変わったことには変わりない）。
		writeAndCommitSpotterYML(t, dir,
			"foo: 1\nchecks:\n  config-guard:\n    type: config-guard\n",
			"add foo")
		stageSpotterYML(t, dir, "checks:\n  config-guard:\n    type: config-guard\n")
		msg := writeMessageFile(t, dir, "feat: 何か\n\nConfig-Guard: skip[.spotter.yml] foo は検査に関係しないため\n")

		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		if err := runCheck(&stdout, &stderr, ".spotter.yml", msg, "", "config-guard"); err != nil {
			t.Fatalf("設定ファイル全体を対象にした免除は効くはず, got %v (stderr=%s)", err, stderr.String())
		}
	})
}

// TestRunConfigGuardRangeSquashedExemptionHoleIsClosed は、issue が挙げた 2 コミットの穴
// （1 コミット目が対象付き免除で A を緩め、2 コミット目が免除無しで B を緩める）が、
// range 検査では B だけ不合格になることを確認する。対象を絞らない免除であれば squashed の
// 「範囲内のどれか 1 コミット」の免除が範囲全体に効いてしまうが、config-guard は対象付き
// 免除しか受け付けないため、コミット 1 の免除は A（diff-size）にしか効かない。
func TestRunConfigGuardRangeSquashedExemptionHoleIsClosed(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n"+
			"  config-guard:\n    type: config-guard\n"+
			"  diff-size:\n    type: diff-size\n    max_lines: 10\n"+
			"  doc-links:\n    type: doc-links\n    check_anchors: true\n",
		"add config-guard")

	// コミット 1: diff-size（A）を対象付き免除で緩める。
	stageSpotterYML(t, dir,
		"checks:\n"+
			"  config-guard:\n    type: config-guard\n"+
			"  diff-size:\n    type: diff-size\n    max_lines: 100\n"+
			"  doc-links:\n    type: doc-links\n    check_anchors: true\n")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m",
		"fix: diff-size の上限を見直す\n\nConfig-Guard: skip[checks.diff-size] 生成コードの取り込みのため一時的に上げる")

	// コミット 2: doc-links（B）をトレーラ無しで緩める。
	stageSpotterYML(t, dir,
		"checks:\n"+
			"  config-guard:\n    type: config-guard\n"+
			"  diff-size:\n    type: diff-size\n    max_lines: 100\n"+
			"  doc-links:\n    type: doc-links\n",
	)
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "fix: 関係ない修正")

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", "", "HEAD~2..HEAD", "config-guard")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("対象を絞らない緩和（doc-links）が残っているので ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "checks.diff-size") {
		t.Errorf("diff-size は対象付き免除で免除されているはず, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "checks.doc-links") {
		t.Errorf("doc-links の緩和は免除されず残るはず, got %q", stderr.String())
	}
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
