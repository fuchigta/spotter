package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"無し（`spotter hooks install` で作成できます）",
		"スキル:",
		"設置されていません（`spotter skills install` で追加できます）",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていません: %s", want, out)
		}
	}
}
