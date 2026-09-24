package command_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/command"
	"github.com/fuchigta/spotter/internal/config"
)

// TestMain は、go test でビルドされたテストバイナリ自身を「検査コマンド」として
// 再起動できるようにする（docs/development.md にある claude/codex の偽装と同じ手法）。
// SPOTTER_FAKE_CHECK=1 のときは検査コマンドとして振る舞い、実際のテストは走らせない。
func TestMain(m *testing.M) {
	if os.Getenv("SPOTTER_FAKE_CHECK") == "1" {
		os.Exit(fakeCheckMain())
	}
	os.Exit(m.Run())
}

type record struct {
	Args        []string `json:"args"`
	Env         []string `json:"env"`
	MessageFile string   `json:"message_file"`
	OptionsFile string   `json:"options_file"`
}

func fakeCheckMain() int {
	if path := os.Getenv("SPOTTER_FAKE_CHECK_RECORD"); path != "" {
		args := os.Args[1:]
		rec := record{
			Args:        args,
			Env:         filteredEnv(),
			MessageFile: readFileArg(args, "--message-file"),
			OptionsFile: readFileArg(args, "--options-file"),
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return 90
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return 91
		}
	}
	if msg := os.Getenv("SPOTTER_FAKE_CHECK_STDERR"); msg != "" {
		// 偽装コマンドの標準エラー出力で、書き込み失敗はテストの成否に関わらない。
		_, _ = os.Stderr.WriteString(msg)
	}
	if v := os.Getenv("SPOTTER_FAKE_CHECK_EXIT"); v != "" {
		code, err := strconv.Atoi(v)
		if err != nil {
			return 92
		}
		return code
	}
	return 0
}

// readFileArg は args から flag の次の値をファイルパスとして読み、その中身を返す
// （見つからない・読めない場合は空文字）。--message-file / --options-file は
// 検査コマンドの実行中しか存在しない一時ファイルなので、host（テストの親プロセス）
// 側が削除する前にここで中身を確定させておく。
func readFileArg(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				return ""
			}
			return string(data)
		}
	}
	return ""
}

func filteredEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "SPOTTER_OPT_") {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

func fakeCommandPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error: %v", err)
	}
	return exe
}

func setupFakeCheck(t *testing.T) (recordPath string) {
	t.Helper()
	t.Setenv("SPOTTER_FAKE_CHECK", "1")
	recordPath = filepath.Join(t.TempDir(), "record.json")
	t.Setenv("SPOTTER_FAKE_CHECK_RECORD", recordPath)
	t.Setenv("SPOTTER_FAKE_CHECK_EXIT", "0")
	t.Setenv("SPOTTER_FAKE_CHECK_STDERR", "")
	return recordPath
}

func readRecord(t *testing.T, path string) record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("record ファイルの読み込みに失敗しました: %v", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("record の解析に失敗しました: %v", err)
	}
	return rec
}

func mustNew(t *testing.T, cc config.CheckConfig, tc config.TypeConfig) *command.Check {
	t.Helper()
	c, err := command.New("my-check", cc, tc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

// TestRunModes は、staged/range/worktree それぞれの粒度で 1 回ずつだけ検査コマンドを
// 起動し、(1) args が command 直後・--mode より前に来ること、(2) モードに応じた
// --mode/--from/--to の付与、(3) file transport（既定）で options-file と
// message-file が渡ること、をまとめて確認する。モードごとに個別の Run 呼び出しに
// 分けると同じ内容を何度も起動することになるため、1 モード 1 起動にまとめている。
func TestRunModes(t *testing.T) {
	cases := []struct {
		name        string
		granularity string
		ctx         check.Context
		wantMode    []string
		forbidFlags []string
	}{
		{
			name:        "staged",
			granularity: "squashed",
			ctx:         check.Context{Message: "何かのメッセージ"},
			wantMode:    []string{"--mode", "staged"},
			forbidFlags: []string{"--from", "--to"},
		},
		{
			name:        "range",
			granularity: "per-commit",
			ctx:         check.Context{Message: "msg", Range: &check.RangeRef{From: "aaa", To: "bbb"}},
			wantMode:    []string{"--mode", "range", "--from", "aaa", "--to", "bbb"},
		},
		{
			// worktree 粒度の Context は staged と同じく Range が nil（cli/check.go の
			// planInvocations 参照）。それでも c.granularity を見て worktree と判定できる
			// ことを確認する。
			name:        "worktree",
			granularity: "worktree",
			ctx:         check.Context{},
			wantMode:    []string{"--mode", "worktree"},
			forbidFlags: []string{"--from", "--to"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recordPath := setupFakeCheck(t)

			c := mustNew(t, config.CheckConfig{
				Options: map[string]any{"threshold": 10},
			}, config.TypeConfig{
				Command: fakeCommandPath(t),
				Args:    []string{"run", "./cmd/example"},
				Default: &config.TypeDefault{Granularity: tc.granularity},
			})

			violations, err := c.Run(tc.ctx)
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if violations != nil {
				t.Errorf("成功時は violations が無いはず, got %v", violations)
			}

			rec := readRecord(t, recordPath)

			want := append([]string{"run", "./cmd/example"}, tc.wantMode...)
			if len(rec.Args) < len(want) {
				t.Fatalf("Args = %v, 先頭が %v であるはず", rec.Args, want)
			}
			for i, w := range want {
				if rec.Args[i] != w {
					t.Errorf("Args[%d] = %q, want %q（args は command の直後、--mode より前に来るはず）", i, rec.Args[i], w)
				}
			}
			for _, f := range tc.forbidFlags {
				if containsFlag(rec.Args, f) {
					t.Errorf("%s を渡さないはず: %v", f, rec.Args)
				}
			}

			if rec.OptionsFile == "" {
				t.Fatalf("--options-file が渡っていない: %v", rec.Args)
			}
			var options map[string]any
			if err := json.Unmarshal([]byte(rec.OptionsFile), &options); err != nil {
				t.Fatalf("options の解析に失敗しました: %v", err)
			}
			if options["threshold"] != float64(10) {
				t.Errorf("options.threshold = %v, want 10", options["threshold"])
			}
			if rec.MessageFile != tc.ctx.Message {
				t.Errorf("MessageFile = %q, want %q", rec.MessageFile, tc.ctx.Message)
			}
		})
	}
}

// TestNewNonexistentCommandIsError は、検査コマンド自体を起動できない（実行ファイルが
// 見つからない）場合、New が返す *Check の Run が [] check.Violation ではなく error を
// 返すことを確認する。非 0 終了（コマンドは起動できたが失敗した）とは区別されるべき経路。
func TestNewNonexistentCommandIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{}, config.TypeConfig{
		Command: "spotter-command-check-does-not-exist-xyzzy",
		Default: &config.TypeDefault{Granularity: "squashed"},
	})

	violations, err := c.Run(check.Context{})
	if err == nil {
		t.Fatalf("存在しないコマンドなら Run() は error を返すはず, violations = %v", violations)
	}
}

func TestRunFailureReportsStderrAsViolation(t *testing.T) {
	setupFakeCheck(t)
	t.Setenv("SPOTTER_FAKE_CHECK_EXIT", "1")
	t.Setenv("SPOTTER_FAKE_CHECK_STDERR", "違反の内容")

	c := mustNew(t, config.CheckConfig{}, config.TypeConfig{
		Command: fakeCommandPath(t),
		Default: &config.TypeDefault{Granularity: "squashed"},
	})

	violations, err := c.Run(check.Context{})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 || violations[0].Summary != "違反の内容" {
		t.Errorf("violations = %v, want [{違反の内容}]", violations)
	}
}

// TestRunFailureEmptyStderrUsesDefaultSummary は、非 0 終了かつ stderr が空の場合、
// Summary が「(コマンド) が失敗しました（終了コード N）」という代わりの文言になることを
// 確認する（stderr が空のまま Violation を空文字で報告しないため）。
func TestRunFailureEmptyStderrUsesDefaultSummary(t *testing.T) {
	setupFakeCheck(t)
	t.Setenv("SPOTTER_FAKE_CHECK_EXIT", "3")
	// stderr は setupFakeCheck が空文字にしている。

	commandPath := fakeCommandPath(t)
	c := mustNew(t, config.CheckConfig{}, config.TypeConfig{
		Command: commandPath,
		Default: &config.TypeDefault{Granularity: "squashed"},
	})

	violations, err := c.Run(check.Context{})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	want := fmt.Sprintf("%s が失敗しました（終了コード %d）", commandPath, 3)
	if len(violations) != 1 || violations[0].Summary != want {
		t.Errorf("violations = %v, want [{%s}]", violations, want)
	}
}

func TestTransportArgs(t *testing.T) {
	recordPath := setupFakeCheck(t)

	c := mustNew(t, config.CheckConfig{
		Options: map[string]any{"threshold": 10, "names": []any{"a", "b"}},
	}, config.TypeConfig{
		Command:   fakeCommandPath(t),
		Transport: "args",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})

	if _, err := c.Run(check.Context{}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	rec := readRecord(t, recordPath)
	if !containsPair(rec.Args, "--threshold", "10") {
		t.Errorf("--threshold 10 が渡っていない: %v", rec.Args)
	}
	if !containsPair(rec.Args, "--names", "a") || !containsPair(rec.Args, "--names", "b") {
		t.Errorf("--names の繰り返し引数が渡っていない: %v", rec.Args)
	}
}

// TestTransportArgsBoolValue は、bool 値のオプションが args transport で "true"/"false"
// という文字列に展開されることを確認する。
func TestTransportArgsBoolValue(t *testing.T) {
	recordPath := setupFakeCheck(t)

	c := mustNew(t, config.CheckConfig{
		Options: map[string]any{"strict": true},
	}, config.TypeConfig{
		Command:   fakeCommandPath(t),
		Transport: "args",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})

	if _, err := c.Run(check.Context{}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	rec := readRecord(t, recordPath)
	if !containsPair(rec.Args, "--strict", "true") {
		t.Errorf("--strict true が渡っていない: %v", rec.Args)
	}
}

func TestTransportEnv(t *testing.T) {
	recordPath := setupFakeCheck(t)

	c := mustNew(t, config.CheckConfig{
		Options: map[string]any{"threshold": 10},
	}, config.TypeConfig{
		Command:   fakeCommandPath(t),
		Transport: "env",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})

	if _, err := c.Run(check.Context{}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	rec := readRecord(t, recordPath)
	found := false
	for _, e := range rec.Env {
		if e == "SPOTTER_OPT_THRESHOLD=10" {
			found = true
		}
	}
	if !found {
		t.Errorf("SPOTTER_OPT_THRESHOLD=10 が環境変数に無い: %v", rec.Env)
	}
}

// TestTransportEnvBoolValue は、bool 値のオプションが env transport で "true"/"false"
// という文字列に展開されることを確認する。
func TestTransportEnvBoolValue(t *testing.T) {
	recordPath := setupFakeCheck(t)

	c := mustNew(t, config.CheckConfig{
		Options: map[string]any{"strict": false},
	}, config.TypeConfig{
		Command:   fakeCommandPath(t),
		Transport: "env",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})

	if _, err := c.Run(check.Context{}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	rec := readRecord(t, recordPath)
	found := false
	for _, e := range rec.Env {
		if e == "SPOTTER_OPT_STRICT=false" {
			found = true
		}
	}
	if !found {
		t.Errorf("SPOTTER_OPT_STRICT=false が環境変数に無い: %v", rec.Env)
	}
}

func TestTransportEnvRejectsArray(t *testing.T) {
	_, err := command.New("my-check", config.CheckConfig{
		Options: map[string]any{"names": []any{"a", "b"}},
	}, config.TypeConfig{
		Command:   "irrelevant",
		Transport: "env",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})
	if err == nil {
		t.Fatal("env transport は配列を拒否するはず")
	}
}

// TestNewTransportArgsRejectsNonScalar は、args transport がスカラーでない値（map）を
// 拒否することを確認する（配列は繰り返し引数として許容されるが、map は表現方法が無い）。
func TestNewTransportArgsRejectsNonScalar(t *testing.T) {
	_, err := command.New("my-check", config.CheckConfig{
		Options: map[string]any{"nested": map[string]any{"a": 1}},
	}, config.TypeConfig{
		Command:   "irrelevant",
		Transport: "args",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})
	if err == nil {
		t.Fatal("args transport はスカラーでない値（map）を拒否するはず")
	}
}

// TestNewTransportEnvRejectsNonScalarMap は、env transport がスカラーでない値（map）を
// 拒否することを確認する（TestTransportEnvRejectsArray の配列とは別の非スカラー型）。
func TestNewTransportEnvRejectsNonScalarMap(t *testing.T) {
	_, err := command.New("my-check", config.CheckConfig{
		Options: map[string]any{"nested": map[string]any{"a": 1}},
	}, config.TypeConfig{
		Command:   "irrelevant",
		Transport: "env",
		Default:   &config.TypeDefault{Granularity: "squashed"},
	})
	if err == nil {
		t.Fatal("env transport はスカラーでない値（map）を拒否するはず")
	}
}

func TestNewValidatesSchema(t *testing.T) {
	_, err := command.New("my-check", config.CheckConfig{
		Options: map[string]any{},
	}, config.TypeConfig{
		Command: "irrelevant",
		Schema: &config.SchemaConfig{
			Simple: map[string]config.FieldSpec{
				"threshold": {Type: "integer", Required: true},
			},
		},
		Default: &config.TypeDefault{Granularity: "squashed"},
	})
	if err == nil {
		t.Fatal("必須オプションが無いのに New() がエラーになりませんでした")
	}
}

func TestNewRequiresGranularity(t *testing.T) {
	_, err := command.New("my-check", config.CheckConfig{}, config.TypeConfig{
		Command: "irrelevant",
	})
	if err == nil {
		t.Fatal("granularity が無いのに New() がエラーになりませんでした")
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{}, config.TypeConfig{
		Command: "irrelevant",
		Default: &config.TypeDefault{Granularity: "per-commit"},
	})
	if c.Granularity() != check.GranularityPerCommit {
		t.Errorf("Granularity() = %v, want per-commit", c.Granularity())
	}
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
