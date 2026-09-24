package hooks_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/gitutil"
	"github.com/fuchigta/spotter/internal/hooks"
)

func newTestRepo(t *testing.T) *gitutil.Repo {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.name", "spotter test")
	run("config", "user.email", "spotter@example.invalid")

	return gitutil.New(dir)
}

func TestParseHook(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    hooks.Hook
		wantErr bool
	}{
		{name: "commit-msg", in: "commit-msg", want: hooks.HookCommitMsg},
		{name: "pre-push", in: "pre-push", want: hooks.HookPrePush},
		{name: "未知の名前はエラー", in: "pre-commit", wantErr: true},
		{name: "空文字はエラー", in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hooks.ParseHook(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseHook(%q) はエラーになるはず", tt.in)
				}
				if !strings.Contains(err.Error(), "commit-msg") || !strings.Contains(err.Error(), "pre-push") {
					t.Errorf("エラーに選べる名前が含まれていない: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseHook(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseHook(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInvocationLine(t *testing.T) {
	tests := []struct {
		hook hooks.Hook
		want string
	}{
		{hook: hooks.HookCommitMsg, want: `spotter check --message "$1"`},
		{hook: hooks.HookPrePush, want: `spotter check --pre-push "$1"`},
	}
	for _, tt := range tests {
		if got := hooks.InvocationLine(tt.hook); got != tt.want {
			t.Errorf("InvocationLine(%q) = %q, want %q", tt.hook, got, tt.want)
		}
	}
}

func TestDefaultHooksIncludesBoth(t *testing.T) {
	got := hooks.DefaultHooks()
	if len(got) != 2 || got[0] != hooks.HookCommitMsg || got[1] != hooks.HookPrePush {
		t.Errorf("DefaultHooks() = %v, want [commit-msg pre-push]（この順）", got)
	}
}

func TestInspectFreshRepo(t *testing.T) {
	repo := newTestRepo(t)

	status, err := hooks.Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect() error: %v", err)
	}
	if status.HooksPath != "" {
		t.Errorf("HooksPath = %q, want 空", status.HooksPath)
	}
	if len(status.Hooks) != 2 {
		t.Fatalf("Hooks の件数 = %d, want 2（引数省略で DefaultHooks() を使うはず）", len(status.Hooks))
	}
	for _, hs := range status.Hooks {
		if hs.HookFileExists {
			t.Errorf("%s: HookFileExists = true, want false", hs.Hook)
		}
		if hs.Managed {
			t.Errorf("%s: Managed = true, want false", hs.Hook)
		}
	}
}

func TestInspectSelectsGivenHooksOnly(t *testing.T) {
	repo := newTestRepo(t)

	status, err := hooks.Inspect(repo, hooks.HookPrePush)
	if err != nil {
		t.Fatalf("Inspect() error: %v", err)
	}
	if len(status.Hooks) != 1 || status.Hooks[0].Hook != hooks.HookPrePush {
		t.Fatalf("Hooks = %v, want [pre-push] だけ", status.Hooks)
	}
}

func TestInstallFreshRepoInstallsBothByDefault(t *testing.T) {
	repo := newTestRepo(t)

	result, err := hooks.Install(repo, ".githooks", nil)
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if !result.HooksPathChanged {
		t.Errorf("HooksPathChanged = false, want true")
	}
	if result.HooksPath != ".githooks" {
		t.Errorf("HooksPath = %q, want .githooks", result.HooksPath)
	}
	if len(result.Hooks) != 2 {
		t.Fatalf("Hooks の件数 = %d, want 2", len(result.Hooks))
	}

	for _, hr := range result.Hooks {
		if hr.Outcome != hooks.OutcomeCreated {
			t.Errorf("%s: Outcome = %q, want created", hr.Hook, hr.Outcome)
		}
		if !hr.HookFileExists || !hr.Managed {
			t.Errorf("%s: HookFileExists/Managed = %v/%v, want true/true", hr.Hook, hr.HookFileExists, hr.Managed)
		}

		data, err := os.ReadFile(hr.HookFile)
		if err != nil {
			t.Fatalf("%s: 生成されたフックの読み込みに失敗しました: %v", hr.Hook, err)
		}
		if !strings.HasPrefix(string(data), "#!/bin/sh") {
			t.Errorf("%s: 新規作成したフックに shebang が無い: %q", hr.Hook, data)
		}
		if !strings.Contains(string(data), hooks.InvocationLine(hr.Hook)) {
			t.Errorf("%s: 生成されたフックが spotter を呼び出していない: %q", hr.Hook, data)
		}
	}
}

func TestInstallSelectsGivenHooksOnly(t *testing.T) {
	repo := newTestRepo(t)

	result, err := hooks.Install(repo, ".githooks", []hooks.Hook{hooks.HookPrePush})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if len(result.Hooks) != 1 || result.Hooks[0].Hook != hooks.HookPrePush {
		t.Fatalf("Hooks = %v, want [pre-push] だけ", result.Hooks)
	}

	if _, err := os.Stat(filepath.Join(repo.Dir, ".githooks", "commit-msg")); !os.IsNotExist(err) {
		t.Errorf("commit-msg を選んでいないので作られていないはず: err=%v", err)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	repo := newTestRepo(t)

	if _, err := hooks.Install(repo, ".githooks", nil); err != nil {
		t.Fatalf("1 回目の Install() error: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(repo.Dir, ".githooks", "commit-msg"))
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}

	result, err := hooks.Install(repo, ".githooks", nil)
	if err != nil {
		t.Fatalf("2 回目の Install() error: %v", err)
	}
	if result.HooksPathChanged {
		t.Errorf("2 回目は HooksPathChanged = false のはず")
	}
	for _, hr := range result.Hooks {
		if hr.Outcome != hooks.OutcomeAlready {
			t.Errorf("2 回目の %s: Outcome = %q, want already", hr.Hook, hr.Outcome)
		}
	}

	after, err := os.ReadFile(filepath.Join(repo.Dir, ".githooks", "commit-msg"))
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("べき等であるべきなのにフックの内容が変わった:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestInstallRespectsExistingHooksPath(t *testing.T) {
	repo := newTestRepo(t)

	cmd := exec.Command("git", "config", "core.hooksPath", "custom-hooks")
	cmd.Dir = repo.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	result, err := hooks.Install(repo, ".githooks", nil)
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if result.HooksPathChanged {
		t.Errorf("既に core.hooksPath が設定済みなら変更しないはず")
	}
	if result.HooksPath != "custom-hooks" {
		t.Errorf("HooksPath = %q, want custom-hooks", result.HooksPath)
	}

	for _, hr := range result.Hooks {
		want := filepath.Join(repo.Dir, "custom-hooks", string(hr.Hook))
		if hr.HookFile != want {
			t.Errorf("%s: HookFile = %q, want %q", hr.Hook, hr.HookFile, want)
		}
		if hr.Outcome != hooks.OutcomeCreated {
			t.Errorf("%s: Outcome = %q, want created（custom-hooks に無いので新規作成のはず）", hr.Hook, hr.Outcome)
		}

		data, err := os.ReadFile(hr.HookFile)
		if err != nil {
			t.Fatalf("%s: 生成されたフックの読み込みに失敗しました: %v", hr.Hook, err)
		}
		if !strings.HasPrefix(string(data), "#!/bin/sh") {
			t.Errorf("%s: 新規作成したフックに shebang が無い: %q", hr.Hook, data)
		}
		if !strings.Contains(string(data), hooks.InvocationLine(hr.Hook)) {
			t.Errorf("%s: 生成されたフックが spotter を呼び出していない: %q", hr.Hook, data)
		}
	}
}

// TestInstallRespectsAbsoluteHooksPath は、core.hooksPath が絶対パスで設定済みの場合も
// （相対パスのときと同様に）それを尊重し、repo.Dir とは結合せずそのまま使うことを確認する。
func TestInstallRespectsAbsoluteHooksPath(t *testing.T) {
	repo := newTestRepo(t)
	absHooksDir := filepath.Join(t.TempDir(), "abs-hooks")

	cmd := exec.Command("git", "config", "core.hooksPath", absHooksDir)
	cmd.Dir = repo.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	result, err := hooks.Install(repo, ".githooks", []hooks.Hook{hooks.HookCommitMsg})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if result.HooksPathChanged {
		t.Errorf("既に core.hooksPath が絶対パスで設定済みなら変更しないはず")
	}
	if result.HooksPath != absHooksDir {
		t.Errorf("HooksPath = %q, want %q", result.HooksPath, absHooksDir)
	}
	if want := filepath.Join(absHooksDir, "commit-msg"); result.Hooks[0].HookFile != want {
		t.Errorf("HookFile = %q, want %q（絶対パスは repo.Dir と結合せずそのまま使うはず）", result.Hooks[0].HookFile, want)
	}
	if result.Hooks[0].Outcome != hooks.OutcomeCreated {
		t.Errorf("Outcome = %q, want created", result.Hooks[0].Outcome)
	}

	data, err := os.ReadFile(result.Hooks[0].HookFile)
	if err != nil {
		t.Fatalf("生成されたフックの読み込みに失敗しました: %v", err)
	}
	if !strings.Contains(string(data), `spotter check --message "$1"`) {
		t.Errorf("生成されたフックが spotter を呼び出していない: %q", data)
	}
}

func TestInstallAppendsToExistingHook(t *testing.T) {
	repo := newTestRepo(t)

	hookDir := filepath.Join(repo.Dir, "custom-hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	existing := "#!/bin/sh\necho 既存のフック\n"
	hookFile := filepath.Join(hookDir, "pre-push")
	if err := os.WriteFile(hookFile, []byte(existing), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := exec.Command("git", "config", "core.hooksPath", "custom-hooks")
	cmd.Dir = repo.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	result, err := hooks.Install(repo, ".githooks", []hooks.Hook{hooks.HookPrePush})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if result.Hooks[0].Outcome != hooks.OutcomeAppended {
		t.Errorf("Outcome = %q, want appended", result.Hooks[0].Outcome)
	}

	data, err := os.ReadFile(hookFile)
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "既存のフック") {
		t.Errorf("既存の内容が失われた: %q", content)
	}
	if !strings.Contains(content, `spotter check --pre-push "$1"`) {
		t.Errorf("spotter の呼び出しが追記されていない: %q", content)
	}
	if strings.Index(content, "既存のフック") > strings.Index(content, `spotter check`) {
		t.Errorf("追記は既存内容の後ろに来るはず: %q", content)
	}
}

// TestInstallHooksIndependentOutcome は、一方が既に設置済み・もう一方は未設置という
// 状態から Install(nil) を呼んだとき、フックごとに正しい Outcome が別々に返ることを
// 確認する（例: 既存利用者が pre-push だけ追加で入れる再実行）。
func TestInstallHooksIndependentOutcome(t *testing.T) {
	repo := newTestRepo(t)

	if _, err := hooks.Install(repo, ".githooks", []hooks.Hook{hooks.HookCommitMsg}); err != nil {
		t.Fatalf("1 回目の Install() error: %v", err)
	}

	result, err := hooks.Install(repo, ".githooks", nil)
	if err != nil {
		t.Fatalf("2 回目の Install() error: %v", err)
	}

	outcomes := map[hooks.Hook]hooks.Outcome{}
	for _, hr := range result.Hooks {
		outcomes[hr.Hook] = hr.Outcome
	}
	if outcomes[hooks.HookCommitMsg] != hooks.OutcomeAlready {
		t.Errorf("commit-msg の Outcome = %q, want already", outcomes[hooks.HookCommitMsg])
	}
	if outcomes[hooks.HookPrePush] != hooks.OutcomeCreated {
		t.Errorf("pre-push の Outcome = %q, want created", outcomes[hooks.HookPrePush])
	}
}
