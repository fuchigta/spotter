package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/gitutil"
)

// TestRepoRelativeConfigPath は --config の解決（repoRelativeConfigPath）を、相対パス・
// 絶対パス・リポジトリの外を指すケースについて確かめる。相対パスの分岐は
// git の prefix だけで決まりファイルシステムに触れないため、TopLevel の文字列表記
// （symlink や短縮名の違い）に依存しないことがポイント。絶対パスの分岐は
// filepath.EvalSymlinks で symlink 越しでも同じ場所と判定できることを確かめる。
func TestRepoRelativeConfigPath(t *testing.T) {
	type testCase struct {
		name  string
		setup func(t *testing.T) (repo *gitutil.Repo, configPath string)
		// want が空でも wantOutside が false のケースは無い（空文字列を期待するケースが
		// 無いため、want=="" は「未設定」の目印として使える）。
		want        string
		wantOutside bool
	}

	cases := []testCase{
		{
			name: "相対パス（リポジトリ直下から）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				t.Chdir(dir)
				return gitutil.New("."), ".spotter.yml"
			},
			want: ".spotter.yml",
		},
		{
			name: "相対パス（サブディレクトリから、親を辿って指す）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				sub := filepath.Join(dir, "sub", "dir")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatalf("サブディレクトリの作成に失敗しました: %v", err)
				}
				t.Chdir(sub)
				return gitutil.New("."), "../../.spotter.yml"
			},
			want: ".spotter.yml",
		},
		{
			name: "相対パス（サブディレクトリから、その中のファイルを指す）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				sub := filepath.Join(dir, "sub")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatalf("サブディレクトリの作成に失敗しました: %v", err)
				}
				t.Chdir(sub)
				return gitutil.New("."), "config.yml"
			},
			want: "sub/config.yml",
		},
		{
			name: "相対パス（../outside.yml はリポジトリの外）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				t.Chdir(dir)
				return gitutil.New("."), "../outside.yml"
			},
			wantOutside: true,
		},
		{
			name: "絶対パス（リポジトリの中）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				t.Chdir(dir)
				return gitutil.New("."), filepath.Join(dir, ".spotter.yml")
			},
			want: ".spotter.yml",
		},
		{
			name: "絶対パス（リポジトリの外）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				t.Chdir(dir)
				return gitutil.New("."), filepath.Join(t.TempDir(), ".spotter.yml")
			},
			wantOutside: true,
		},
	}

	if symlinkSupported(t) {
		cases = append(cases, testCase{
			name: "絶対パス（シンボリックリンク越しでもリポジトリの中と判定される）",
			setup: func(t *testing.T) (*gitutil.Repo, string) {
				t.Helper()
				dir := newCheckTestRepo(t)
				t.Chdir(dir)

				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatalf("シンボリックリンクの作成に失敗しました: %v", err)
				}
				return gitutil.New("."), filepath.Join(link, ".spotter.yml")
			},
			want: ".spotter.yml",
		})
	} else {
		t.Log("この環境ではシンボリックリンクを作成できないため、symlink 越しのケースは省略します")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, configPath := tc.setup(t)

			got, err := repoRelativeConfigPath(repo, configPath)

			if tc.wantOutside {
				if err == nil {
					t.Fatalf("リポジトリの外のはずがエラーになりませんでした, got %q", got)
				}
				if !strings.Contains(err.Error(), "リポジトリの外") {
					t.Errorf("エラーメッセージにリポジトリの外を指す旨が無い, got %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("repoRelativeConfigPath() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("repoRelativeConfigPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// symlinkSupported は、この環境でシンボリックリンクを作成できるかどうかを確かめる
// （Windows の CI ランナーは権限が無いと失敗しうる）。t.Skip は禁じられているため、
// このケース自体を実行するかどうかを呼び出し側の case 一覧の組み立てで分岐させる。
func symlinkSupported(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("シンボリックリンク対応確認用ディレクトリの作成に失敗しました: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		return false
	}
	return true
}
