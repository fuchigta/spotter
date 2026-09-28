package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/hooks"
)

// TestResolveHooks は --hook の値からフックの一覧を解決する resolveHooks の
// 出し分け（省略時は両方・カンマ区切り・重複排除・未知の名前はエラー）を確認する。
func TestResolveHooks(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []hooks.Hook
		wantErr bool
	}{
		{name: "省略時は両方", in: "", want: []hooks.Hook{hooks.HookCommitMsg, hooks.HookPrePush}},
		{name: "1つに絞る", in: "pre-push", want: []hooks.Hook{hooks.HookPrePush}},
		{name: "カンマ区切りで両方", in: "pre-push,commit-msg", want: []hooks.Hook{hooks.HookPrePush, hooks.HookCommitMsg}},
		{name: "空白付きカンマ区切り", in: " pre-push , commit-msg ", want: []hooks.Hook{hooks.HookPrePush, hooks.HookCommitMsg}},
		{name: "重複は1つにまとめる", in: "commit-msg,commit-msg", want: []hooks.Hook{hooks.HookCommitMsg}},
		{name: "未知の名前はエラー", in: "pre-commit", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveHooks(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveHooks(%q) はエラーになるはず", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveHooks(%q) error: %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("resolveHooks(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("resolveHooks(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestPrintHooksInvocation は --print の出力の出し分け（1つに絞ったときは呼び出し行
// だけ、複数のときは `<hook>: <行>` の形）を確認する。
func TestPrintHooksInvocation(t *testing.T) {
	t.Run("1つに絞ったとき", func(t *testing.T) {
		var stdout bytes.Buffer
		printHooksInvocation(&stdout, []hooks.Hook{hooks.HookPrePush})

		want := hooks.InvocationLine(hooks.HookPrePush) + "\n"
		if stdout.String() != want {
			t.Errorf("printHooksInvocation() = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("複数（既定）のとき", func(t *testing.T) {
		var stdout bytes.Buffer
		printHooksInvocation(&stdout, hooks.DefaultHooks())

		out := stdout.String()
		for _, h := range hooks.DefaultHooks() {
			want := string(h) + ": " + hooks.InvocationLine(h)
			if !strings.Contains(out, want) {
				t.Errorf("出力に %q が含まれていません: %s", want, out)
			}
		}
	})
}

// TestRunHooksInstall は runHooksInstall の出力の出し分け（新規作成・spotter 以外の
// 既存フックには触れない・2 回目は何もしない・--hook で絞る）を確認する。フックの設置
// ロジック自体は internal/hooks で単体テスト済みのため、ここでは runHooksInstall が
// 結果に応じてどう出力するかだけを見る。
// assertOutputContains は out が wants を全て含むことを確認する。
func assertOutputContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていません: %s", want, out)
		}
	}
}

func TestRunHooksInstall(t *testing.T) {
	t.Run("新規作成（既定は両方）", testRunHooksInstallCreatesBoth)
	t.Run("--hook で1つに絞る", testRunHooksInstallLimitedByHookFlag)
	t.Run("spotter 以外の既存フックには触れない", testRunHooksInstallLeavesForeignExisting)
	t.Run("既定の hooks ディレクトリに他のフックランナーの設定がある", testRunHooksInstallLeavesHooksPathUnsetForForeignHooks)
	t.Run("2回目は何もしない", testRunHooksInstallIsIdempotent)
}

func testRunHooksInstallCreatesBoth(t *testing.T) {
	dir := newCheckTestRepo(t)
	t.Chdir(dir)

	var stdout bytes.Buffer
	if err := runHooksInstall(&stdout, ".githooks", hooks.DefaultHooks()); err != nil {
		t.Fatalf("runHooksInstall: %v", err)
	}

	assertOutputContains(t, stdout.String(),
		"commit-msg: フックを新規作成しました",
		"pre-push: フックを新規作成しました",
		"core.hooksPath を .githooks に設定しました",
	)
	if _, err := os.Stat(filepath.Join(dir, ".githooks", "commit-msg")); err != nil {
		t.Errorf("commit-msg フックが作成されていない: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".githooks", "pre-push")); err != nil {
		t.Errorf("pre-push フックが作成されていない: %v", err)
	}
}

func testRunHooksInstallLimitedByHookFlag(t *testing.T) {
	dir := newCheckTestRepo(t)
	t.Chdir(dir)

	var stdout bytes.Buffer
	if err := runHooksInstall(&stdout, ".githooks", []hooks.Hook{hooks.HookPrePush}); err != nil {
		t.Fatalf("runHooksInstall: %v", err)
	}

	assertOutputContains(t, stdout.String(), "pre-push: フックを新規作成しました")
	if _, err := os.Stat(filepath.Join(dir, ".githooks", "commit-msg")); !os.IsNotExist(err) {
		t.Errorf("commit-msg は選んでいないので作られないはず: err=%v", err)
	}
}

// testRunHooksInstallLeavesForeignExisting は、spotter 以外が作った既存の commit-msg
// フックには触れずに案内だけ出す一方、pre-push は未設置なので新規作成し、案内があるので
// 非 0 終了する（ErrCheckFailed）ことを確認する。
func testRunHooksInstallLeavesForeignExisting(t *testing.T) {
	dir := newCheckTestRepo(t)

	hooksDir := filepath.Join(dir, ".githooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	hookFile := filepath.Join(hooksDir, "commit-msg")
	existing := "#!/bin/sh\necho existing\n"
	if err := os.WriteFile(hookFile, []byte(existing), 0o755); err != nil {
		t.Fatalf("既存フックの作成に失敗しました: %v", err)
	}
	runGitCLIForCheckTest(t, dir, "config", "core.hooksPath", ".githooks")

	t.Chdir(dir)

	var stdout bytes.Buffer
	err := runHooksInstall(&stdout, ".githooks", hooks.DefaultHooks())
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("触れなかったフックがあるので ErrCheckFailed のはず, got %v", err)
	}

	assertOutputContains(t, stdout.String(),
		"commit-msg: 既存のフックに spotter 以外の内容があるため触れませんでした",
		hooks.InvocationLine(hooks.HookCommitMsg),
		"pre-push: フックを新規作成しました",
		"core.hooksPath は .githooks のまま変更していません",
	)

	data, err := os.ReadFile(hookFile)
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}
	if string(data) != existing {
		t.Errorf("既存のフックファイルは一切変更されないはず:\nbefore=%q\nafter=%q", existing, data)
	}
}

// testRunHooksInstallLeavesHooksPathUnsetForForeignHooks は、core.hooksPath が未設定
// でも .git/hooks に他のフックランナー（lefthook 等）が直接書いた既存フックがある場合は、
// core.hooksPath を変えずにそのディレクトリへ設置し、その旨を出力することを確認する。
func testRunHooksInstallLeavesHooksPathUnsetForForeignHooks(t *testing.T) {
	dir := newCheckTestRepo(t)

	hookFile := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookFile, []byte("#!/bin/sh\necho lefthook\n"), 0o755); err != nil {
		t.Fatalf("既存フックの作成に失敗しました: %v", err)
	}

	t.Chdir(dir)

	var stdout bytes.Buffer
	if err := runHooksInstall(&stdout, ".githooks", hooks.DefaultHooks()); err != nil {
		t.Fatalf("runHooksInstall: %v", err)
	}

	assertOutputContains(t, stdout.String(),
		"core.hooksPath は設定しませんでした",
	)
	if strings.Contains(stdout.String(), "core.hooksPath を .githooks に設定しました") {
		t.Errorf("既存フックがあるので .githooks へ切り替えてはいけない: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".githooks")); !os.IsNotExist(err) {
		t.Errorf(".githooks は作られないはず: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "commit-msg")); err != nil {
		t.Errorf(".git/hooks/commit-msg が作成されていない: %v", err)
	}
}

func testRunHooksInstallIsIdempotent(t *testing.T) {
	dir := newCheckTestRepo(t)
	t.Chdir(dir)

	var first bytes.Buffer
	if err := runHooksInstall(&first, ".githooks", hooks.DefaultHooks()); err != nil {
		t.Fatalf("1 回目の runHooksInstall: %v", err)
	}

	var second bytes.Buffer
	if err := runHooksInstall(&second, ".githooks", hooks.DefaultHooks()); err != nil {
		t.Fatalf("2 回目の runHooksInstall: %v", err)
	}
	assertOutputContains(t, second.String(),
		"commit-msg: 既に設置済みです",
		"pre-push: 既に設置済みです",
	)
}
