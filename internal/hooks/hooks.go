// Package hooks はフックの設置（spotter hooks install）と状態確認（spotter doctor）を
// 扱う。対応するフックは commit-msg（staged モードの検査）と pre-push（push 前の range
// 検査）の 2 つ。
//
// フックランナーそのものは作らない。core.hooksPath は 1 つしか持てないため、
// 既に lefthook 等で設定済みならそれを尊重し、そこにあるフックへ追記するだけに
// とどめる（新規に決め打ちのディレクトリへ差し替えたりはしない）。
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fuchigta/spotter/internal/gitutil"
)

// Hook は spotter が設置できる git フックの種類。値はそのまま git のフックファイル名
// （commit-msg / pre-push）として使う。
type Hook string

const (
	// HookCommitMsg はステージ済みの変更とメッセージを検査する staged モードの検査を呼ぶ。
	HookCommitMsg Hook = "commit-msg"
	// HookPrePush は push しようとしている範囲を検査する range モードの検査を呼ぶ。
	HookPrePush Hook = "pre-push"
)

const beginMarker = "# --- spotter (managed) begin ---"
const endMarker = "# --- spotter (managed) end ---"

// invocationLines はフックごとに spotter を呼び出す最小の 1 行。
// `spotter hooks install --print` はこれだけを出力する。
var invocationLines = map[Hook]string{
	HookCommitMsg: `spotter check --message "$1"`,
	HookPrePush:   `spotter check --pre-push "$1"`,
}

// DefaultHooks は `spotter hooks install` が --hook 未指定のときに設置するフックの
// 一覧（この順で処理する）。ParseHook が受け付ける名前の一覧でもある。
func DefaultHooks() []Hook {
	return []Hook{HookCommitMsg, HookPrePush}
}

// ParseHook は名前を Hook に変換する。未知の名前は日本語のエラーで、選べる名前を示す。
func ParseHook(name string) (Hook, error) {
	for _, h := range DefaultHooks() {
		if string(h) == name {
			return h, nil
		}
	}
	return "", fmt.Errorf("hooks: 未知のフック名です: %q（選べるのは %s）", name, hookNamesJoined())
}

func hookNamesJoined() string {
	all := DefaultHooks()
	names := make([]string, len(all))
	for i, h := range all {
		names[i] = string(h)
	}
	return strings.Join(names, ", ")
}

// InvocationLine はフックから他のフックランナー（lefthook / husky など）に貼り付ける
// ための、spotter を呼び出す最小の 1 行を返す。
func InvocationLine(h Hook) string {
	return invocationLines[h]
}

// managedBlock はフックファイルに追記する本体。spotter が手元に無い場合は警告して
// 素通りする。すり抜けは CI（`spotter check --range "$(spotter range)"`）が最後の
// 歯止めになる。
func managedBlock(h Hook) string {
	return beginMarker + "\n" +
		"if command -v spotter >/dev/null 2>&1; then\n" +
		"  " + InvocationLine(h) + " || exit 1\n" +
		"else\n" +
		"  echo \"spotter: コマンドが見つからないため検査をスキップします\" >&2\n" +
		"fi\n" +
		endMarker + "\n"
}

// freshHookTemplate はフックが存在しない場所に新規作成する内容。
func freshHookTemplate(h Hook) string {
	return "#!/bin/sh\n" +
		"# spotter hooks install が生成した " + string(h) + " フック。\n" +
		managedBlock(h)
}

// Outcome は Install が選んだフックごとに実際に何をしたか。
type Outcome string

const (
	OutcomeCreated  Outcome = "created"  // フックファイルを新規作成した
	OutcomeAppended Outcome = "appended" // 既存のフックファイルに追記した
	OutcomeAlready  Outcome = "already"  // 既に spotter を呼び出す設定になっていた
)

// HookStatus は 1 つのフックファイルの現在の状態（doctor 用）。
type HookStatus struct {
	Hook Hook
	// HookFile はそのフックの実際のパス（HooksPath が空なら既定の hooks ディレクトリ配下）。
	HookFile string
	// HookFileExists は HookFile が存在するか。
	HookFileExists bool
	// Managed は HookFile が spotter の管理ブロックを含むか。
	Managed bool
}

// Status は現在のフック設置状況（doctor 用）。
type Status struct {
	// HooksPath は git config core.hooksPath の現在値。未設定なら空文字。
	HooksPath string
	// Hooks は Inspect に渡したフックそれぞれの状態。渡した順を保つ。
	Hooks []HookStatus
}

// HookResult は Install が 1 つのフックに対して行った結果。
type HookResult struct {
	HookStatus
	Outcome Outcome
}

// Result は Install の結果。
type Result struct {
	// HooksPath は Install 後の core.hooksPath の値。
	HooksPath string
	// HooksPathChanged は core.hooksPath を新たに設定したか
	// （既に設定済みだった場合は false のまま尊重して変更しない）。
	HooksPathChanged bool
	// Hooks は選んだフックそれぞれの結果。渡した順を保つ。
	Hooks []HookResult
}

// Inspect は選んだフック（省略時は DefaultHooks()）の設置状況を調べる（変更は一切しない）。
func Inspect(repo *gitutil.Repo, selected ...Hook) (Status, error) {
	if len(selected) == 0 {
		selected = DefaultHooks()
	}

	hooksPath, hooksPathSet, err := repo.ConfigGet("core.hooksPath")
	if err != nil {
		return Status{}, err
	}

	statuses := make([]HookStatus, 0, len(selected))
	for _, h := range selected {
		hookFile, err := resolveHookFile(repo, hooksPath, hooksPathSet, h)
		if err != nil {
			return Status{}, err
		}
		managed, exists, err := inspectHookFile(hookFile)
		if err != nil {
			return Status{}, err
		}
		statuses = append(statuses, HookStatus{
			Hook:           h,
			HookFile:       hookFile,
			HookFileExists: exists,
			Managed:        managed,
		})
	}

	return Status{HooksPath: hooksPath, Hooks: statuses}, nil
}

// Install は選んだフック（省略時は DefaultHooks()）を設置する。
//
//   - core.hooksPath が未設定なら hooksDirDefault を設定してそこに新規作成する
//   - 既に設定済みならそれを尊重し、そこにあるフックへ追記する（無ければ新規作成する）
//   - フックごとに、既に spotter の管理ブロックが入っていれば何もしない（べき等）
func Install(repo *gitutil.Repo, hooksDirDefault string, selected []Hook) (Result, error) {
	if len(selected) == 0 {
		selected = DefaultHooks()
	}

	hooksPath, hooksPathSet, err := repo.ConfigGet("core.hooksPath")
	if err != nil {
		return Result{}, err
	}

	hooksPathChanged := false
	if !hooksPathSet {
		if err := os.MkdirAll(filepath.Join(repo.Dir, hooksDirDefault), 0o755); err != nil {
			return Result{}, fmt.Errorf("hooks: %s の作成に失敗しました: %w", hooksDirDefault, err)
		}
		if err := repo.ConfigSet("core.hooksPath", hooksDirDefault); err != nil {
			return Result{}, fmt.Errorf("hooks: core.hooksPath の設定に失敗しました: %w", err)
		}
		hooksPathChanged = true
		hooksPath = hooksDirDefault
		hooksPathSet = true
	}

	results := make([]HookResult, 0, len(selected))
	for _, h := range selected {
		result, err := installHook(repo, hooksPath, hooksPathSet, h)
		if err != nil {
			return Result{}, err
		}
		results = append(results, result)
	}

	return Result{HooksPath: hooksPath, HooksPathChanged: hooksPathChanged, Hooks: results}, nil
}

func installHook(repo *gitutil.Repo, hooksPath string, hooksPathSet bool, h Hook) (HookResult, error) {
	hookFile, err := resolveHookFile(repo, hooksPath, hooksPathSet, h)
	if err != nil {
		return HookResult{}, err
	}

	managed, exists, err := inspectHookFile(hookFile)
	if err != nil {
		return HookResult{}, err
	}

	if managed {
		return HookResult{
			HookStatus: HookStatus{Hook: h, HookFile: hookFile, HookFileExists: true, Managed: true},
			Outcome:    OutcomeAlready,
		}, nil
	}

	var outcome Outcome
	if exists {
		if err := appendManagedBlock(hookFile, h); err != nil {
			return HookResult{}, err
		}
		outcome = OutcomeAppended
	} else {
		if err := os.MkdirAll(filepath.Dir(hookFile), 0o755); err != nil {
			return HookResult{}, fmt.Errorf("hooks: %s の作成に失敗しました: %w", filepath.Dir(hookFile), err)
		}
		if err := os.WriteFile(hookFile, []byte(freshHookTemplate(h)), 0o755); err != nil {
			return HookResult{}, fmt.Errorf("hooks: %s の作成に失敗しました: %w", hookFile, err)
		}
		outcome = OutcomeCreated
	}

	return HookResult{
		HookStatus: HookStatus{Hook: h, HookFile: hookFile, HookFileExists: true, Managed: true},
		Outcome:    outcome,
	}, nil
}

func appendManagedBlock(hookFile string, h Hook) error {
	f, err := os.OpenFile(hookFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("hooks: %s への追記に失敗しました: %w", hookFile, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString("\n" + managedBlock(h)); err != nil {
		return fmt.Errorf("hooks: %s への追記に失敗しました: %w", hookFile, err)
	}
	return nil
}

// inspectHookFile は path の存在確認と、spotter の管理ブロックを含むかの判定をまとめて行う。
func inspectHookFile(path string) (managed, exists bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("hooks: %s の読み込みに失敗しました: %w", path, err)
	}
	return strings.Contains(string(data), beginMarker), true, nil
}

// resolveHookFile はフック h の実際のパスを求める。hooksPathSet が false
// （core.hooksPath 未設定）なら、git 自身が使う既定の hooks ディレクトリを
// `git rev-parse --git-path hooks` で解決する（worktree・submodule でも正しく
// 求まるようにするため、".git/hooks" と決め打ちにしない）。
func resolveHookFile(repo *gitutil.Repo, hooksPath string, hooksPathSet bool, h Hook) (string, error) {
	if !hooksPathSet {
		dir, err := repo.GitPath("hooks")
		if err != nil {
			return "", fmt.Errorf("hooks: 既定の hooks ディレクトリの解決に失敗しました: %w", err)
		}
		return filepath.Join(resolveDir(repo.Dir, dir), string(h)), nil
	}
	return filepath.Join(resolveDir(repo.Dir, hooksPath), string(h)), nil
}

// resolveDir は p が絶対パスならそのまま、相対パスなら root からの相対として解決する。
func resolveDir(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}
