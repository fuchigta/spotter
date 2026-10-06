package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirWithFiles は files（パス→内容）を一時ディレクトリに作り、そこをカレントディレクトリにする。
func chdirWithFiles(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("os.MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("os.WriteFile: %v", err)
		}
	}
	t.Chdir(dir)
}

const explainConsistencyConfig = `
checks:
  templates:
    type: consistency
    sources:
      - file: a.txt
        line: '^names ='
        extract: 'names = (.*)'
        split: ','
      - file: b.txt
        extract: '^- (\w+)$'
  doc-sync:
    type: doc-sync
    pairs:
      - paths: '**/*.go'
        doc: README.md
  config-guard:
    type: config-guard
`

func TestRunConfigExplainText(t *testing.T) {
	chdirWithFiles(t, map[string]string{
		"a.txt": "head\nnames = x, y\n",
		"b.txt": "- x\n- y\n",
	})
	configPath := writeConfigFile(t, explainConsistencyConfig)

	var buf bytes.Buffer
	if err := runConfigExplain(&buf, configPath, "templates", false); err != nil {
		t.Fatalf("runConfigExplain: %v", err)
	}

	want := `templates（consistency）
  sources[0] file: a.txt
    line: ^names =
    extract: names = (.*)
    split: ,
    line に一致した行: L2
    要素（2 件）:
      - x  L2
      - y  L2
  sources[1] file: b.txt
    extract: ^- (\w+)$
    要素（2 件）:
      - x  L1
      - y  L2
合否は spotter check templates で確認してください。
`
	if got := buf.String(); got != want {
		t.Errorf("出力が一致しません\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunConfigExplainJSON(t *testing.T) {
	chdirWithFiles(t, map[string]string{
		"a.txt": "names = x\n",
		"b.txt": "- x\n",
	})
	configPath := writeConfigFile(t, explainConsistencyConfig)

	var buf bytes.Buffer
	if err := runConfigExplain(&buf, configPath, "templates", true); err != nil {
		t.Fatalf("runConfigExplain: %v", err)
	}

	var out ExplainOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal: %v（出力: %s）", err, buf.String())
	}
	if out.Check != "templates" || out.Type != "consistency" || len(out.Sections) != 2 {
		t.Errorf("JSON の内容が想定と違います: %+v", out)
	}
	if got := out.Sections[0].Items; len(got) != 1 || got[0].Value != "x" || got[0].Locations[0] != "L1" {
		t.Errorf("Items が想定と違います: %+v", got)
	}
}

func TestRunConfigExplainReportsProblemsWithoutFailing(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "1 つも抽出できなくても実行エラーにしない",
			files: map[string]string{"a.txt": "head\n", "b.txt": "- x\n"},
			want:  "1 つも抽出できませんでした（spotter check では実行エラーになります）",
		},
	}

	configPath := writeConfigFile(t, explainConsistencyConfig)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chdirWithFiles(t, tt.files)

			var buf bytes.Buffer
			if err := runConfigExplain(&buf, configPath, "templates", false); err != nil {
				t.Fatalf("runConfigExplain がエラーを返しました: %v", err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Errorf("出力に %q が含まれていません:\n%s", tt.want, buf.String())
			}
		})
	}
}

func TestRunConfigExplainUntilWithoutTerminator(t *testing.T) {
	chdirWithFiles(t, map[string]string{"a.txt": "A\n a1\n", "b.txt": "- a1\n"})
	configPath := writeConfigFile(t, `
checks:
  blocks:
    type: consistency
    sources:
      - file: a.txt
        line: '^A$'
        until: '^end$'
        extract: '\s(a\d)'
      - file: b.txt
        extract: '^- (\w+)$'
`)

	var buf bytes.Buffer
	if err := runConfigExplain(&buf, configPath, "blocks", false); err != nil {
		t.Fatalf("runConfigExplain がエラーを返しました: %v", err)
	}
	if !strings.Contains(buf.String(), "L1 から始まるブロックの終端") {
		t.Errorf("終端が無い旨の注記がありません:\n%s", buf.String())
	}
}

func TestRunConfigExplainErrors(t *testing.T) {
	tests := []struct {
		name       string
		configBody string
		noConfig   bool
		key        string
		want       string
	}{
		{name: "存在しないキー", configBody: explainConsistencyConfig, key: "nope", want: "設定に checks.nope がありません"},
		{name: "対応していない type（doc-sync）", configBody: explainConsistencyConfig, key: "doc-sync", want: "explain に対応していません"},
		{name: "対応していない type（config-guard）", configBody: explainConsistencyConfig, key: "config-guard", want: "explain に対応していません"},
		{name: "抽出元のファイルが無い", configBody: explainConsistencyConfig, key: "templates", want: "見つかりません"},
		{name: "設定ファイルが無い", noConfig: true, key: "templates", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chdirWithFiles(t, nil)
			configPath := filepath.Join(t.TempDir(), "missing.yml")
			if !tt.noConfig {
				configPath = writeConfigFile(t, tt.configBody)
			}

			var buf bytes.Buffer
			err := runConfigExplain(&buf, configPath, tt.key, false)
			if err == nil {
				t.Fatal("エラーが返る想定でしたが nil でした")
			}
			if errors.Is(err, ErrCheckFailed) {
				t.Errorf("explain は ErrCheckFailed を返してはいけません: %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("エラーに %q が含まれていません: %v", tt.want, err)
			}
		})
	}
}
