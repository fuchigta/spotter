package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withBuildVersion は buildVersion（required_version の判定に使うグローバル変数）を
// 差し替え、テスト終了時に元に戻す。
func withBuildVersion(t *testing.T, v string) {
	t.Helper()
	orig := buildVersion
	buildVersion = v
	t.Cleanup(func() { buildVersion = orig })
}

// writeRequiredVersionConfig は required_version（省略可）に加えて、違反を必ず
// 出す unwanted-files を持つ設定を書く。required_version を満たさないときに
// この検査自体が実行されないことを、違反が出ないことで確認するために使う。
func writeRequiredVersionConfig(t *testing.T, dir, requiredVersion string) {
	t.Helper()
	content := "checks:\n  no-big-files:\n    type: unwanted-files\n    max_bytes: 1\n"
	if requiredVersion != "" {
		content = "required_version: " + requiredVersion + "\n" + content
	}
	if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
	}
}

func TestRunCheckRequiredVersion(t *testing.T) {
	tests := []struct {
		name            string
		requiredVersion string
		buildVersion    string
		satisfied       bool
	}{
		{"未指定なら常に満たす", "", "v1.0.0", true},
		{"満たすバージョン", "v0.1.0", "v1.0.0", true},
		{"満たさないバージョン", "v999.0.0", "v1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCheckTestRepo(t)
			writeRequiredVersionConfig(t, dir, tt.requiredVersion)

			if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("0123456789"), 0o644); err != nil {
				t.Fatalf("ファイル作成に失敗しました: %v", err)
			}
			runGitCLIForCheckTest(t, dir, "add", "big.txt")

			t.Chdir(dir)
			withBuildVersion(t, tt.buildVersion)

			var stdout, stderr bytes.Buffer
			err := runCheck(&stdout, &stderr, ".spotter.yml", "", "", "")

			if tt.satisfied {
				if err != ErrCheckFailed {
					t.Fatalf("required_version を満たすので違反を検出して ErrCheckFailed のはず, got %v (stderr=%s)", err, stderr.String())
				}
				if !strings.Contains(stderr.String(), "no-big-files の検査に失敗しました") {
					t.Errorf("検査が実行されて違反が出るはず, got stderr=%q", stderr.String())
				}
				return
			}

			if err == nil || err == ErrCheckFailed {
				t.Fatalf("required_version を満たさないなら、検査すら実行せず ErrCheckFailed ではない error を返すはず, got %v", err)
			}
			if !strings.Contains(err.Error(), "required_version") {
				t.Errorf("エラーに required_version を満たさない旨が含まれるはず, got %v", err)
			}
			if stderr.Len() != 0 {
				t.Errorf("検査自体を実行していないので stderr に違反表示は出ないはず, got %q", stderr.String())
			}
		})
	}
}

func TestRunDoctorRequiredVersion(t *testing.T) {
	tests := []struct {
		name            string
		requiredVersion string
		buildVersion    string
		wantErr         bool
		wantOutput      string
	}{
		{"未指定なら required_version の行を出力しない", "", "v1.0.0", false, ""},
		{"満たすバージョン", "v0.1.0", "v1.0.0", false, "required_version v0.1.0 を満たしています"},
		{"満たさないバージョン", "v999.0.0", "v1.0.0", true, "required_version v999.0.0 を満たしていません"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCheckTestRepo(t)
			content := "checks: {}\n"
			if tt.requiredVersion != "" {
				content = "required_version: " + tt.requiredVersion + "\nchecks: {}\n"
			}
			if err := os.WriteFile(filepath.Join(dir, ".spotter.yml"), []byte(content), 0o644); err != nil {
				t.Fatalf(".spotter.yml の作成に失敗しました: %v", err)
			}

			t.Chdir(dir)
			withBuildVersion(t, tt.buildVersion)

			var stdout bytes.Buffer
			err := runDoctor(&stdout, ".spotter.yml")

			if tt.wantErr {
				if err != ErrCheckFailed {
					t.Fatalf("required_version を満たさないなら ErrCheckFailed のはず, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("runDoctor: %v", err)
			}

			out := stdout.String()
			if tt.wantOutput != "" && !strings.Contains(out, tt.wantOutput) {
				t.Errorf("出力に %q が含まれるはず, got %q", tt.wantOutput, out)
			}
			if tt.requiredVersion == "" && strings.Contains(out, "required_version") {
				t.Errorf("required_version 未指定なら required_version の行が出ないはず, got %q", out)
			}
		})
	}
}
