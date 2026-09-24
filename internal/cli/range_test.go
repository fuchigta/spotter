package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunRangeErrorsWhenProviderUndetectable は、--provider 未指定で CI 環境変数
// （GITHUB_ACTIONS・GITLAB_CI）も無いときに runRange が error を返すことを確認する。
// これらの環境変数は t.Setenv で明示的に空にし、このテスト自体が CI 上で実行されても
// 結果が変わらないようにする。
func TestRunRangeErrorsWhenProviderUndetectable(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GITLAB_CI", "")

	dir := newCheckTestRepo(t)
	t.Chdir(dir)

	var stdout bytes.Buffer
	err := runRange(&stdout, "")
	if err == nil {
		t.Fatal("CI 環境を検出できないなら error のはず")
	}
	if !strings.Contains(err.Error(), "CI 環境を自動検出できませんでした") {
		t.Errorf("エラーに自動検出できなかった旨が含まれるはず, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("失敗時は範囲式を出力しないはず, got %q", stdout.String())
	}
}
