package skills_test

import (
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/skills"
)

func TestResolveTarget(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{name: "claude", want: "claude"},
		{name: "claude-code", want: "claude"},
		{name: "agents", want: "agents"},
		{name: "codex", want: "agents"},
		{name: "gemini", want: "agents"},
		{name: "cursor", want: "agents"},
		{name: "copilot", want: "agents"},
		{name: "unknown", wantErr: true},
		{name: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := skills.ResolveTarget(tc.name)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveTarget(%q) はエラーになるはず", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTarget(%q) error: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("ResolveTarget(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestTargets(t *testing.T) {
	got := skills.Targets()
	want := []string{"agents", "claude"}
	if len(got) != len(want) {
		t.Fatalf("Targets() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Targets()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolvePathProjectScope(t *testing.T) {
	// ResolvePath は結果を filepath.Abs で絶対パス化するため、repoRoot 自体も
	// 実在する絶対パス（t.TempDir()）にしておく（"/repo" のような Unix 風の
	// 相対もどきだと Windows で CWD のドライブが補完されてしまい、期待値がぶれる）。
	repoRoot := t.TempDir()

	cases := []struct {
		target string
		want   string
	}{
		{target: "claude", want: filepath.Join(repoRoot, ".claude", "skills")},
		{target: "claude-code", want: filepath.Join(repoRoot, ".claude", "skills")},
		{target: "agents", want: filepath.Join(repoRoot, ".agents", "skills")},
		{target: "codex", want: filepath.Join(repoRoot, ".agents", "skills")},
	}

	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			got, err := skills.ResolvePath(tc.target, skills.ScopeProject, repoRoot)
			if err != nil {
				t.Fatalf("ResolvePath error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolvePath(%q, project) = %q, want %q", tc.target, got, tc.want)
			}
		})
	}
}

func TestResolvePathProjectScopeRequiresRepoRoot(t *testing.T) {
	if _, err := skills.ResolvePath("claude", skills.ScopeProject, ""); err == nil {
		t.Fatal("repoRoot が空なら project スコープはエラーになるはず")
	}
}

func TestResolvePathUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows の os.UserHomeDir はこちらを見る
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	got, err := skills.ResolvePath("agents", skills.ScopeUser, "")
	if err != nil {
		t.Fatalf("ResolvePath error: %v", err)
	}
	want := filepath.Join(home, ".agents", "skills")
	if got != want {
		t.Errorf("ResolvePath(agents, user) = %q, want %q", got, want)
	}
}

func TestResolvePathUserScopeClaudeDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	got, err := skills.ResolvePath("claude", skills.ScopeUser, "")
	if err != nil {
		t.Fatalf("ResolvePath error: %v", err)
	}
	want := filepath.Join(home, ".claude", "skills")
	if got != want {
		t.Errorf("ResolvePath(claude, user) = %q, want %q", got, want)
	}
}

func TestResolvePathUserScopeRespectsClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	customDir := filepath.Join(t.TempDir(), "custom-claude-home")
	t.Setenv("CLAUDE_CONFIG_DIR", customDir)

	got, err := skills.ResolvePath("claude", skills.ScopeUser, "")
	if err != nil {
		t.Fatalf("ResolvePath error: %v", err)
	}
	want := filepath.Join(customDir, "skills")
	if got != want {
		t.Errorf("ResolvePath(claude, user) = %q, want %q", got, want)
	}
}

func TestResolvePathUserScopeClaudeConfigDirDoesNotAffectAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "custom-claude-home"))

	got, err := skills.ResolvePath("agents", skills.ScopeUser, "")
	if err != nil {
		t.Fatalf("ResolvePath error: %v", err)
	}
	want := filepath.Join(home, ".agents", "skills")
	if got != want {
		t.Errorf("CLAUDE_CONFIG_DIR は agents ターゲットに影響しないはず: ResolvePath(agents, user) = %q, want %q", got, want)
	}
}

func TestResolvePathUnknownScope(t *testing.T) {
	if _, err := skills.ResolvePath("claude", skills.Scope("bogus"), "/repo"); err == nil {
		t.Fatal("未知の scope はエラーになるはず")
	}
}

func TestResolvePathUnknownTarget(t *testing.T) {
	if _, err := skills.ResolvePath("bogus", skills.ScopeProject, "/repo"); err == nil {
		t.Fatal("未知の target はエラーになるはず")
	}
}
