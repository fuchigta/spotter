package cirange_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/cirange"
)

func envFromMap(m map[string]string) cirange.Env {
	return func(key string) string { return m[key] }
}

func writeEventJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("イベント JSON の書き込みに失敗しました: %v", err)
	}
	return path
}

func alwaysExists(sha string) (bool, error) { return sha != "", nil }
func neverExists(sha string) (bool, error)  { return false, nil }
func erroringExists(sha string) (bool, error) {
	return false, fmt.Errorf("commitExists: %s の確認に失敗しました（テスト用）", sha)
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want cirange.Provider
		ok   bool
	}{
		{"GitHub Actions", map[string]string{"GITHUB_ACTIONS": "true"}, cirange.ProviderGitHubActions, true},
		{"GitLab CI", map[string]string{"GITLAB_CI": "true"}, cirange.ProviderGitLabCI, true},
		{"どちらでもない", map[string]string{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cirange.Detect(envFromMap(tt.env))
			if got != tt.want || ok != tt.ok {
				t.Errorf("Detect() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestResolveGitHubActions は resolveGitHubActions（Resolve(ProviderGitHubActions, ...)）を
// イベントの形ごとにテーブル駆動で確認する。eventJSON == "" かつ noEventPath が false の
// ケースは無い（該当する行だけ noEventPath を使う）。
func TestResolveGitHubActions(t *testing.T) {
	tests := []struct {
		name         string
		eventName    string
		eventJSON    string
		noEventPath  bool
		githubSHA    string
		commitExists cirange.CommitExistsFunc
		want         string
		wantErr      bool
	}{
		{
			name:         "pull_request: base sha があれば base..HEAD",
			eventName:    "pull_request",
			eventJSON:    `{"pull_request": {"base": {"sha": "aaa111"}}}`,
			commitExists: alwaysExists,
			want:         "aaa111..HEAD",
		},
		{
			name:         "pull_request_target も同じ扱い",
			eventName:    "pull_request_target",
			eventJSON:    `{"pull_request": {"base": {"sha": "aaa111"}}}`,
			commitExists: alwaysExists,
			want:         "aaa111..HEAD",
		},
		{
			name:         "pull_request: base sha が空文字ならフォールバック",
			eventName:    "pull_request",
			eventJSON:    `{"pull_request": {"base": {"sha": ""}}}`,
			commitExists: alwaysExists,
			want:         "-1 HEAD",
		},
		{
			name:         "pull_request: base の途中のキー（base 自体）が無ければフォールバック",
			eventName:    "pull_request",
			eventJSON:    `{"pull_request": {}}`,
			commitExists: alwaysExists,
			want:         "-1 HEAD",
		},
		{
			name:         "pull_request: イベント JSON が壊れていればエラー",
			eventName:    "pull_request",
			eventJSON:    `{"pull_request": {`,
			commitExists: alwaysExists,
			wantErr:      true,
		},
		{
			name:         "push: before/GITHUB_SHA から before..after",
			eventName:    "push",
			eventJSON:    `{"before": "bbb222"}`,
			githubSHA:    "ccc333",
			commitExists: alwaysExists,
			want:         "bbb222..ccc333",
		},
		{
			// 新規ブランチの初回 push は before が全ゼロになり、実在しないコミットとして扱われる。
			name:         "push: 新規ブランチ相当（before が全ゼロで実在しない）はフォールバック",
			eventName:    "push",
			eventJSON:    `{"before": "0000000000000000000000000000000000000000"}`,
			githubSHA:    "ccc333",
			commitExists: neverExists,
			want:         "-1 HEAD",
		},
		{
			name:         "push: commitExists がエラーを返したら Resolve もエラー",
			eventName:    "push",
			eventJSON:    `{"before": "bbb222"}`,
			githubSHA:    "ccc333",
			commitExists: erroringExists,
			wantErr:      true,
		},
		{
			name:         "workflow_dispatch 等（GITHUB_EVENT_PATH 無し）はフォールバック",
			eventName:    "workflow_dispatch",
			noEventPath:  true,
			commitExists: alwaysExists,
			want:         "-1 HEAD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"GITHUB_EVENT_NAME": tt.eventName}
			if !tt.noEventPath {
				env["GITHUB_EVENT_PATH"] = writeEventJSON(t, tt.eventJSON)
			}
			if tt.githubSHA != "" {
				env["GITHUB_SHA"] = tt.githubSHA
			}

			got, err := cirange.Resolve(cirange.ProviderGitHubActions, envFromMap(env), tt.commitExists)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Resolve() はエラーになるはず")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveGitLabCI は resolveGitLabCI（Resolve(ProviderGitLabCI, ...)）をテーブル
// 駆動で確認する。
func TestResolveGitLabCI(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		commitExists cirange.CommitExistsFunc
		want         string
		wantErr      bool
	}{
		{
			name: "merge_request_event: base sha があれば base..HEAD",
			env: map[string]string{
				"CI_PIPELINE_SOURCE":             "merge_request_event",
				"CI_MERGE_REQUEST_DIFF_BASE_SHA": "aaa111",
			},
			commitExists: alwaysExists,
			want:         "aaa111..HEAD",
		},
		{
			name: "merge_request_event: CI_MERGE_REQUEST_DIFF_BASE_SHA が空文字ならフォールバック",
			env: map[string]string{
				"CI_PIPELINE_SOURCE":             "merge_request_event",
				"CI_MERGE_REQUEST_DIFF_BASE_SHA": "",
			},
			commitExists: alwaysExists,
			want:         "-1 HEAD",
		},
		{
			name: "push 相当: CI_COMMIT_BEFORE_SHA/CI_COMMIT_SHA から before..after",
			env: map[string]string{
				"CI_COMMIT_BEFORE_SHA": "bbb222",
				"CI_COMMIT_SHA":        "ccc333",
			},
			commitExists: alwaysExists,
			want:         "bbb222..ccc333",
		},
		{
			name:         "push 相当: CI_COMMIT_BEFORE_SHA が無ければフォールバック",
			env:          map[string]string{},
			commitExists: alwaysExists,
			want:         "-1 HEAD",
		},
		{
			name: "push 相当: commitExists がエラーを返したら Resolve もエラー",
			env: map[string]string{
				"CI_COMMIT_BEFORE_SHA": "bbb222",
				"CI_COMMIT_SHA":        "ccc333",
			},
			commitExists: erroringExists,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cirange.Resolve(cirange.ProviderGitLabCI, envFromMap(tt.env), tt.commitExists)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Resolve() はエラーになるはず")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	if _, err := cirange.Resolve("unknown", envFromMap(nil), alwaysExists); err == nil {
		t.Fatal("未対応の provider では Resolve() がエラーになるはず")
	}
}
