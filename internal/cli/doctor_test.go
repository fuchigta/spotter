package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/hooks"
)

// TestRunDoctorNoChecksNoHooks は、検査を 1 つも設定していない・フックも未設置の
// リポジトリで runDoctor がその旨を出力し、成功終了することを確認する。
func TestRunDoctorNoChecksNoHooks(t *testing.T) {
	dir := newCheckTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte("checks: {}\n"), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}

	t.Chdir(dir)

	var stdout bytes.Buffer
	if err := runDoctor(&stdout, ".spotter.yml"); err != nil {
		t.Fatalf("検査 0 件・フック未設置は成功のはず: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"検査は 1 つも設定されていません",
		"core.hooksPath: (未設定。既定の hooks ディレクトリを使用)",
		"commit-msg",
		"pre-push",
		"無し（`spotter hooks install` で作成できます）",
		"スキル:",
		"設置されていません（`spotter skills install` で追加できます）",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていません: %s", want, out)
		}
	}
}

// TestRunDoctorHookFileStates は、commit-msg フックファイルの中身のパターンごとに
// doctor の表示が出し分けられることを確認する（spotter を呼び出している / spotter 以外の既存フックで未設定）。
func TestRunDoctorHookFileStates(t *testing.T) {
	block := installedManagedBlockForDoctorTest(t)

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "spotter を呼び出している",
			content: "#!/bin/sh\n" + block,
			want:    "あり（spotter を呼び出しています）",
		},
		{
			name:    "spotter 以外の既存フックは未設定",
			content: "#!/bin/sh\necho existing\n",
			want:    "あり（spotter は未設定。呼び出し行をフックランナーの設定に組み込むか、手で追記してください。`spotter hooks install --print` で確認できます）",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCheckTestRepo(t)
			if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte("checks: {}\n"), 0o644); err != nil {
				t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
			}
			hooksDir := filepath.Join(dir, ".githooks")
			if err := os.MkdirAll(hooksDir, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(filepath.Join(hooksDir, "commit-msg"), []byte(tt.content), 0o755); err != nil {
				t.Fatalf("commit-msg フックの作成に失敗しました: %v", err)
			}
			runGitCLIForCheckTest(t, dir, "config", "core.hooksPath", ".githooks")

			t.Chdir(dir)

			var stdout bytes.Buffer
			if err := runDoctor(&stdout, ".spotter.yml"); err != nil {
				t.Fatalf("runDoctor: %v", err)
			}
			assertOutputContains(t, stdout.String(), tt.want)
		})
	}
}

// installedManagedBlockForDoctorTest は、Install が新規作成したフックが含む管理ブロック
// 部分（先頭の begin マーカーから末尾まで）を返す。マーカーの正確な文字列は実装の詳細
// なので、テストデータの組み立てにも実際の Install の出力をそのまま使う。
func installedManagedBlockForDoctorTest(t *testing.T) string {
	t.Helper()
	dir := newCheckTestRepo(t)
	t.Chdir(dir)

	var stdout bytes.Buffer
	if err := runHooksInstall(&stdout, ".githooks", hooks.DefaultHooks()); err != nil {
		t.Fatalf("runHooksInstall: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".githooks", "commit-msg"))
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}
	content := string(data)
	idx := strings.Index(content, "# --- spotter (managed) begin ---")
	if idx < 0 {
		t.Fatalf("生成されたフックに管理ブロックが見つからない: %q", content)
	}
	return content[idx:]
}
