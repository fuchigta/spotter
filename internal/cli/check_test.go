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

// writeScopedDocSyncConfig は doc-sync に 2 つの独立した pairs（別々の doc）を持たせた
// 設定を書く。片方の doc だけをスコープ付き免除で免除しても、もう片方の doc の違反は
// 残ることを確認するために使う。
func writeScopedDocSyncConfig(t *testing.T, dir string) {
	t.Helper()
	content := "checks:\n" +
		"  doc-sync:\n" +
		"    type: doc-sync\n" +
		"    pairs:\n" +
		"      - paths: 'a/*.go'\n" +
		"        doc: DOCA.md\n" +
		"      - paths: 'b/*.go'\n" +
		"        doc: DOCB.md\n"
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}
}

func writeFileAndStage(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("ディレクトリ作成に失敗しました: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", rel)
}

// TestRunCheckScopedExemptionOnlyExemptsMatchingDoc は、doc-sync のスコープ付き免除
// （"Doc-Sync: skip[DOCA.md] 理由"）が DOCA.md 側の違反だけを免除し、免除していない
// DOCB.md 側の違反は残って検査全体が失敗することを確認する。
func TestRunCheckScopedExemptionOnlyExemptsMatchingDoc(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeScopedDocSyncConfig(t, dir)

	writeFileAndStage(t, dir, "a/foo.go", "package a\n")
	writeFileAndStage(t, dir, "b/bar.go", "package b\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("feat: 何か\n\nDoc-Sync: skip[DOCA.md] 内部の変更\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "")
	if err != ErrCheckFailed {
		t.Fatalf("DOCB.md 側は免除していないので ErrCheckFailed のはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doc-sync: DOCA.md を免除しました（内部の変更）") {
		t.Errorf("DOCA.md を免除した旨が stdout に出るはず, got %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "DOCA.md") {
		t.Errorf("DOCA.md は免除されているので stderr の違反表示に出ないはず, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "DOCB.md") {
		t.Errorf("DOCB.md は免除していないので違反として stderr に出るはず, got %q", stderr.String())
	}
}

// TestRunCheckScopedExemptionMultipleTargets は、スコープ付き免除で複数の doc を指定したとき
// （1 行にカンマ区切りで並べる書き方と、行ごとに理由を分ける書き方の両方）、指定した
// doc の違反がすべて免除され、検査全体が成功することを確認する。
func TestRunCheckScopedExemptionMultipleTargets(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    []string
	}{
		{
			name:    "カンマ区切りで 1 行に並べる",
			message: "feat: 何か\n\nDoc-Sync: skip[DOCA.md, DOCB.md] 内部の変更\n",
			want: []string{
				"doc-sync: DOCA.md を免除しました（内部の変更）",
				"doc-sync: DOCB.md を免除しました（内部の変更）",
			},
		},
		{
			name:    "行ごとに理由を分ける",
			message: "feat: 何か\n\nDoc-Sync: skip[DOCA.md] 理由A\nDoc-Sync: skip[DOCB.md] 理由B\n",
			want: []string{
				"doc-sync: DOCA.md を免除しました（理由A）",
				"doc-sync: DOCB.md を免除しました（理由B）",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCheckTestRepo(t)
			writeScopedDocSyncConfig(t, dir)

			writeFileAndStage(t, dir, "a/foo.go", "package a\n")
			writeFileAndStage(t, dir, "b/bar.go", "package b\n")

			msgPath := filepath.Join(dir, "MSG")
			if err := os.WriteFile(msgPath, []byte(tt.message), 0o644); err != nil {
				t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
			}

			t.Chdir(dir)

			var stdout, stderr bytes.Buffer
			if err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", ""); err != nil {
				t.Fatalf("両方の doc を免除したので成功するはず, got %v (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
			}
			for _, w := range tt.want {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout に %q が出るはず, got %q", w, stdout.String())
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("違反は残らないので stderr は空のはず, got %q", stderr.String())
			}
		})
	}
}

// TestRunCheckScopedExemptionErrorsOnUnsupportedCheck は、スコープ付き免除
// （check.ScopedExemptable 未実装）の検査に "skip[対象] 理由" を書いたら、黙って
// 検査全体を免除にせず error になることを確認する（docs/principles.md 約束 7）。
func TestRunCheckScopedExemptionErrorsOnUnsupportedCheck(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeUnwantedFilesConfig(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("ファイル作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "add", "big.txt")

	msgPath := filepath.Join(dir, "MSG")
	// no-big-files のトレーラ名は defaultTrailer により "No-Big-Files"。
	if err := os.WriteFile(msgPath, []byte("feat: 何か\n\nNo-Big-Files: skip[big.txt] 理由\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "")
	if err == nil || err == ErrCheckFailed {
		t.Fatalf("スコープ付き免除に対応していない検査への skip[...] は error になるはず, got %v", err)
	}
}

// TestRunCheckScopedExemptionErrorsOnUnknownTarget は、doc-sync の ExemptTargets() に
// 無い対象を指定したら（書き間違い）error になることを確認する。
func TestRunCheckScopedExemptionErrorsOnUnknownTarget(t *testing.T) {
	dir := newCheckTestRepo(t)
	writeScopedDocSyncConfig(t, dir)

	writeFileAndStage(t, dir, "a/foo.go", "package a\n")
	writeFileAndStage(t, dir, "b/bar.go", "package b\n")

	msgPath := filepath.Join(dir, "MSG")
	if err := os.WriteFile(msgPath, []byte("feat: 何か\n\nDoc-Sync: skip[DOCA.md,DOCZ.md] 理由\n"), 0o644); err != nil {
		t.Fatalf("メッセージファイルの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	err := runCheck(&stdout, &stderr, ".spotter.yml", msgPath, "", "")
	if err == nil || err == ErrCheckFailed {
		t.Fatalf("存在しない対象を指定したら error になるはず, got %v", err)
	}
}
