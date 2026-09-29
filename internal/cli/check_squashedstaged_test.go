package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docSyncAppConfigYAML は 'app/*.go' の変更に DOC.md を対にした最小の doc-sync 設定。
const docSyncAppConfigYAML = "checks:\n" +
	"  doc-sync:\n" +
	"    type: doc-sync\n" +
	"    pairs:\n" +
	"      - paths: 'app/*.go'\n" +
	"        doc: DOC.md\n"

// addOriginRemoteAndPush は dir のリポジトリに origin という bare remote を追加し、main を
// push・fetch した状態にする（origin/main が実在するリモート追跡 ref になる。
// internal/gitutil のテストヘルパーと同じ手順を internal/cli 側の runGitCLIForCheckTest で
// 再現したもの）。
func addOriginRemoteAndPush(t *testing.T, dir string) {
	t.Helper()
	remote := t.TempDir()
	initCmd := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	runGitCLIForCheckTest(t, dir, "remote", "add", "origin", remote)
	runGitCLIForCheckTest(t, dir, "push", "-q", "origin", "main")
	runGitCLIForCheckTest(t, dir, "fetch", "-q", "origin")
}

// newSquashedStagedTestRepo は configYAML を .spotter.yml として追加コミットし、push・fetch
// 済み（＝これ以降の変更が「未 push 範囲」になる）の状態のリポジトリを作る。
func newSquashedStagedTestRepo(t *testing.T, configYAML string) string {
	t.Helper()
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir, configYAML, "add config")
	addOriginRemoteAndPush(t, dir)
	return dir
}

// TestRunCheckSquashedStagedUsesUnpushedRangeOriginForAmend は、squashed 粒度
// （doc-sync）の staged 起動の比較元が未 push 範囲の起点になることを確認する。
// 「元コミットで DOC.md だけを変更 → HEAD を動かさずにコードだけを追加でステージする
// （`git commit --amend` の直前と同じ状態）」でも、未 push 範囲の起点（push 済みの
// 設定コミット）からインデックスまでの累積差分では DOC.md とコードの両方が変更されて
// 見えるため、doc-sync は合格するはず。
func TestRunCheckSquashedStagedUsesUnpushedRangeOriginForAmend(t *testing.T) {
	dir := newSquashedStagedTestRepo(t, docSyncAppConfigYAML)

	writeFileAndStage(t, dir, "DOC.md", "doc\n")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "docs: DOC を追加")

	// amend 相当: HEAD はまだ動かさず、コードだけを追加でステージする。
	writeFileAndStage(t, dir, "app/foo.go", "package app\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("docs: DOC と foo\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "doc-sync"); err != nil {
		t.Fatalf("未 push 範囲の起点からの累積差分で DOC.md も変更されているので合格するはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
}

// TestRunCheckSquashedStagedAfterPushRequiresDocInSameUnpushedRange は、直前のコミットが
// 既に push 済み（＝未 push 範囲の起点として使えない）なら、比較元が HEAD にリセットされ、
// コードだけを変更する通常の次のコミットは不合格になることを確認する（amend で通る抜け穴が
// push 済みの履歴にまで及ばないことの確認）。
func TestRunCheckSquashedStagedAfterPushRequiresDocInSameUnpushedRange(t *testing.T) {
	dir := newSquashedStagedTestRepo(t, docSyncAppConfigYAML)

	writeFileAndStage(t, dir, "DOC.md", "doc\n")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "docs: DOC を追加")
	runGitCLIForCheckTest(t, dir, "push", "-q", "origin", "main")
	runGitCLIForCheckTest(t, dir, "fetch", "-q", "origin")

	// 通常の（amend ではない）次のコミット相当: コードだけを変更する。
	writeFileAndStage(t, dir, "app/bar.go", "package app\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("feat: bar\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "doc-sync")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("直前のコミットが push 済みで比較元は HEAD のはずなので、コードだけの変更は不合格のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "app/bar.go") {
		t.Errorf("app/bar.go が違反として出るはず, got %q", stderr.String())
	}
}

// TestRunCheckSquashedStagedExemptionOnUnpushedHeadMessageApplies は、squashed 粒度の
// staged 起動の免除判定が、未 push の HEAD 自身のコミットメッセージに書かれた
// トレーラも見ることを確認する（これからコミットする内容のメッセージだけでなく、
// HEAD を含む未 push のコミットのメッセージも集めて判定するため）。
func TestRunCheckSquashedStagedExemptionOnUnpushedHeadMessageApplies(t *testing.T) {
	dir := newSquashedStagedTestRepo(t, docSyncAppConfigYAML)

	// 元コミット（未 push）: DOC.md を変更しない代わりに、免除トレーラを書いておく。
	writeFileAndStage(t, dir, "random.txt", "random\n")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m",
		"chore: 準備\n\nDoc-Sync: skip[DOC.md] 後で DOC.md をまとめて直すため")

	// amend 相当: HEAD はまだ動かさず、コードだけを追加でステージする。
	writeFileAndStage(t, dir, "app/foo.go", "package app\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("feat: foo\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "doc-sync"); err != nil {
		t.Fatalf("未 push の HEAD のメッセージにある免除トレーラが効くはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doc-sync: DOC.md を免除しました") {
		t.Errorf("DOC.md を免除した旨が stdout に出るはず, got %q", stdout.String())
	}
}

// TestRunCheckSquashedStagedNoRemoteUsesHead は、リモート追跡 ref が 1 つも無い
// リポジトリでは squashed 粒度の staged 起動も HEAD を比較元にすることを確認する
// （履歴全体と比較する重さを避けるための割り切り。gitutil.UnpushedRangeOrigin 参照）。
// HEAD より前のコミットで DOC.md を変更していても、それだけでは合格しない。
func TestRunCheckSquashedStagedNoRemoteUsesHead(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir, docSyncAppConfigYAML, "add config")

	writeFileAndStage(t, dir, "DOC.md", "doc\n")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "docs: DOC を追加")

	// コードだけを追加でステージする（リモートが無いので比較元は常に HEAD）。
	writeFileAndStage(t, dir, "app/foo.go", "package app\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("feat: foo\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "doc-sync")
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("リモートが無ければ比較元は HEAD のはずなので、コードだけの変更は不合格のはず, got %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "app/foo.go") {
		t.Errorf("app/foo.go が違反として出るはず, got %q", stderr.String())
	}
}

// TestRunCheckSquashedStagedConfigGuardUsesUnpushedRangeOriginForAmend は、
// config-guard（runConfigGuard 経由）も doc-sync と同じ planInvocations の経路で
// 未 push 範囲の起点を比較元にすることを確認する。元コミット（未 push）で diff-size を
// 緩めて対象付き免除トレーラを書き、HEAD を動かさずに無関係なファイルを追加でステージ
// しても（amend 相当）、免除が効いて合格するはず。
func TestRunCheckSquashedStagedConfigGuardUsesUnpushedRangeOriginForAmend(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeAndCommitSpotterYML(t, dir,
		"checks:\n"+
			"  config-guard:\n    type: config-guard\n"+
			"  diff-size:\n    type: diff-size\n    max_lines: 10\n",
		"add config-guard")
	addOriginRemoteAndPush(t, dir)

	// 元コミット（未 push）: diff-size を緩め、対象付き免除トレーラも一緒に書く。
	stageSpotterYML(t, dir,
		"checks:\n"+
			"  config-guard:\n    type: config-guard\n"+
			"  diff-size:\n    type: diff-size\n    max_lines: 100\n")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m",
		"fix: diff-size の上限を見直す\n\nConfig-Guard: skip[checks.diff-size] 生成コードの取り込みのため")

	// amend 相当: HEAD はまだ動かさず、無関係なファイルを追加でステージする。
	writeFileAndStage(t, dir, "unrelated.txt", "x\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("fix: 関係ないファイルを追加\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "config-guard"); err != nil {
		t.Fatalf("config-guard も同じ経路で未 push 範囲の起点を比較元にするので、HEAD の免除トレーラが効いて合格するはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
}

// TestRunCheckPerCommitStagedIgnoresUnpushedHistory は、per-commit 粒度
// （unwanted-files）の staged 起動が squashed とは違って常に HEAD を比較元にすること
// （未 push の履歴を累積しないこと）を確認する。未 push の HEAD 自身がしきい値超えの
// ファイルを含んでいても、それは HEAD・インデックス間の差分には現れないため、ステージした
// 別の小さいファイルだけを見て合格するはず。
func TestRunCheckPerCommitStagedIgnoresUnpushedHistory(t *testing.T) {
	dir := newCheckTestRepo(t)
	addOriginRemoteAndPush(t, dir)

	// 未 push のコミットに、しきい値を超えるファイルを既にコミット済みにしておく。
	writeFileAndStage(t, dir, "big_committed.txt", "0123456789")
	runGitCLIForCheckTest(t, dir, "commit", "-q", "-m", "add big (unpushed)")

	// ステージするのはしきい値以下の別ファイルだけ。
	writeFileAndStage(t, dir, "small.txt", "x")
	writeUnwantedFilesConfig(t, dir)

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("feat: 何か\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "no-big-files"); err != nil {
		t.Fatalf("per-commit 粒度は HEAD 基準のままのはずなので、ステージした差分だけを見て合格するはず, got %v (stderr=%s)", err, stderr.String())
	}
}
