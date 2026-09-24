package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunHooksInstall は runHooksInstall の出力の出し分け（新規作成・既存フックへの
// 追記・2 回目は何もしない）を確認する。フックの設置ロジック自体は internal/hooks で
// 単体テスト済みのため、ここでは runHooksInstall が結果に応じてどう出力するかだけを見る。
func TestRunHooksInstall(t *testing.T) {
	t.Run("新規作成", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		t.Chdir(dir)

		var stdout bytes.Buffer
		if err := runHooksInstall(&stdout, ".githooks"); err != nil {
			t.Fatalf("runHooksInstall: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "commit-msg フックを新規作成しました") {
			t.Errorf("新規作成の旨が出るはず, got %q", out)
		}
		if !strings.Contains(out, "core.hooksPath を .githooks に設定しました") {
			t.Errorf("core.hooksPath 設定の旨が出るはず, got %q", out)
		}
		if _, err := os.Stat(filepath.Join(dir, ".githooks", "commit-msg")); err != nil {
			t.Errorf("commit-msg フックが作成されていない: %v", err)
		}
	})

	t.Run("既存フックへの追記", func(t *testing.T) {
		dir := newCheckTestRepo(t)

		hooksDir := filepath.Join(dir, ".githooks")
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		hookFile := filepath.Join(hooksDir, "commit-msg")
		if err := os.WriteFile(hookFile, []byte("#!/bin/sh\necho existing\n"), 0o755); err != nil {
			t.Fatalf("既存フックの作成に失敗しました: %v", err)
		}
		runGitCLIForCheckTest(t, dir, "config", "core.hooksPath", ".githooks")

		t.Chdir(dir)

		var stdout bytes.Buffer
		if err := runHooksInstall(&stdout, ".githooks"); err != nil {
			t.Fatalf("runHooksInstall: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "既存の commit-msg フックに追記しました") {
			t.Errorf("追記の旨が出るはず, got %q", out)
		}
		if !strings.Contains(out, "core.hooksPath は .githooks のまま変更していません") {
			t.Errorf("core.hooksPath を変更していない旨が出るはず, got %q", out)
		}
	})

	t.Run("2回目は何もしない", func(t *testing.T) {
		dir := newCheckTestRepo(t)
		t.Chdir(dir)

		var first bytes.Buffer
		if err := runHooksInstall(&first, ".githooks"); err != nil {
			t.Fatalf("1 回目の runHooksInstall: %v", err)
		}

		var second bytes.Buffer
		if err := runHooksInstall(&second, ".githooks"); err != nil {
			t.Fatalf("2 回目の runHooksInstall: %v", err)
		}
		if !strings.Contains(second.String(), "既に設置済みです") {
			t.Errorf("2 回目は既に設置済みの旨が出るはず, got %q", second.String())
		}
	})
}
