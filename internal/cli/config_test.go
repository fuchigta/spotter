package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/confighygiene"
)

// runConfigLint は internal package レベルの repoRoot（"."）を常に使うため、
// このテストパッケージの実行ディレクトリ（internal/cli）を基点にファイルシステムを
// 走査する。実際のリポジトリ構造（README.md 等）に依存するケースはここでは
// テストできないため、cwd に依存しないケース（設定ファイル自体のエラー、
// 実在しないパスを指す陳腐化）に絞る。

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spotter.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}

func TestRunConfigLintMissingConfigFile(t *testing.T) {
	var buf bytes.Buffer
	err := runConfigLint(&buf, "no-such-config.yml", false)
	if err == nil {
		t.Fatal("存在しない設定ファイルはエラーになるはず")
	}
	if err == ErrCheckFailed {
		t.Error("config.Load のエラーは ErrCheckFailed ではなく、そのままのエラーを返すはず")
	}
}

func TestRunConfigLintNoChecksIsSuccess(t *testing.T) {
	// checks が空の設定はどの cwd でも Finding 0 件になる（Lint はワークツリーを
	// 走査する前に checks が空なら何もしない）。
	configPath := writeConfigFile(t, "checks: {}\n")

	var buf bytes.Buffer
	if err := runConfigLint(&buf, configPath, false); err != nil {
		t.Fatalf("runConfigLint: %v", err)
	}
	if !strings.Contains(buf.String(), "陳腐化した設定は見つかりませんでした") {
		t.Errorf("出力に成功メッセージが含まれていません: %s", buf.String())
	}
}

func TestRunConfigLintJSONNoFindings(t *testing.T) {
	configPath := writeConfigFile(t, "checks: {}\n")

	var buf bytes.Buffer
	if err := runConfigLint(&buf, configPath, true); err != nil {
		t.Fatalf("runConfigLint: %v", err)
	}

	var findings []confighygiene.Finding
	if err := json.Unmarshal(buf.Bytes(), &findings); err != nil {
		t.Fatalf("json.Unmarshal: %v（出力: %s）", err, buf.String())
	}
	if len(findings) != 0 {
		t.Errorf("Finding が無いはずが出ました: %+v", findings)
	}
}

func TestRunConfigLintReportsErrCheckFailedWhenFindingsExist(t *testing.T) {
	configPath := writeConfigFile(t, `
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: 'no/such/dir/*.go'
        doc: README.md
`)

	var buf bytes.Buffer
	err := runConfigLint(&buf, configPath, false)
	if err != ErrCheckFailed {
		t.Errorf("Finding があるときは ErrCheckFailed を返すはず: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "pairs[0].paths") {
		t.Errorf("出力に Finding が含まれていません: %s", out)
	}
	// README.md も internal/cli 配下（このテストの cwd）には存在しないため
	// pairs[0].doc も検出され、2件になる。
	if !strings.Contains(out, "2 件見つかりました") {
		t.Errorf("出力に件数が含まれていません: %s", out)
	}
}

func TestRunConfigLintJSONReportsErrCheckFailedWhenFindingsExist(t *testing.T) {
	configPath := writeConfigFile(t, `
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: 'no/such/dir/*.go'
        doc: README.md
`)

	var buf bytes.Buffer
	err := runConfigLint(&buf, configPath, true)
	if err != ErrCheckFailed {
		t.Errorf("Finding があるときは JSON 出力でも ErrCheckFailed を返すはず: %v", err)
	}

	var findings []confighygiene.Finding
	if err := json.Unmarshal(buf.Bytes(), &findings); err != nil {
		t.Fatalf("json.Unmarshal: %v（出力: %s）", err, buf.String())
	}
	// README.md も internal/cli 配下（このテストの cwd）には存在しないため
	// pairs[0].doc も検出され、2件になる。
	if len(findings) != 2 {
		t.Errorf("Finding は2件のはず: %+v", findings)
	}
}
