// Package skills はコーディングエージェント（Claude Code / Codex など）向けの
// スキル（SKILL.md 一式）の設置先ディレクトリを解決する。
//
// スキル本体の埋め込み・コピー（`spotter skills install`）はまだこのパッケージには
// 無い。ここではまず「ターゲット名 + スコープ → 実際のディレクトリパス」という
// 解決ロジックだけを切り出す。
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope はスキルの設置範囲。
type Scope string

const (
	// ScopeProject はリポジトリ直下（.claude/skills, .agents/skills）。
	ScopeProject Scope = "project"
	// ScopeUser はユーザーのホームディレクトリ配下（~/.claude/skills, ~/.agents/skills）。
	ScopeUser Scope = "user"
)

// targetAliases はターゲット名（エイリアス含む）→ 正規名の対応表。
//
// "claude" は Claude Code、"agents" は Agent Skills の標準パス（.agents/skills）を
// 読む実装全般（Codex CLI, Gemini CLI, Cursor, GitHub Copilot など）を指す。
// エージェントごとに個別のターゲットを増やすのではなく、標準パスを読む実装は
// まとめて "agents" 1 つに寄せる（これらは同じ .agents/skills/* を読むため、
// 実体は 1 つで足りる）。
var targetAliases = map[string]string{
	"claude":      "claude",
	"claude-code": "claude",
	"agents":      "agents",
	"codex":       "agents",
	"gemini":      "agents",
	"cursor":      "agents",
	"copilot":     "agents",
}

// targetDirNames は正規名 → 設定ディレクトリ名（.claude / .agents）。
var targetDirNames = map[string]string{
	"claude": ".claude",
	"agents": ".agents",
}

// Targets は解決可能なターゲットの正規名一覧（ソート済み。エイリアスは含まない）。
func Targets() []string {
	names := make([]string, 0, len(targetDirNames))
	for name := range targetDirNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveTarget はターゲット名（エイリアス可）を正規名（"claude" | "agents"）に解決する。
func ResolveTarget(name string) (string, error) {
	canonical, ok := targetAliases[name]
	if !ok {
		return "", fmt.Errorf("skills: 未知のターゲットです: %q（有効な値: %s）", name, aliasesHint())
	}
	return canonical, nil
}

// aliasesHint はエラーメッセージ用に、有効なターゲット名（エイリアス含む）をソート済みで
// カンマ区切りにしたもの。targetAliases から動的に組み立てることで、エイリアスを追加した
// ときにエラーメッセージ側の更新漏れが起きないようにする。
func aliasesHint() string {
	names := make([]string, 0, len(targetAliases))
	for name := range targetAliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// ResolvePath はターゲット（エイリアス可）とスコープから、スキルを設置すべき
// ディレクトリの絶対パスを返す。
//
// scope が ScopeProject のときは repoRoot が必須（gitutil.Repo.TopLevel() の結果を
// 渡す想定。カレントディレクトリがリポジトリのサブディレクトリでも正しいルートを使うため）。
// scope が ScopeUser のときは repoRoot を無視する。
//
// "claude" の user スコープは環境変数 CLAUDE_CONFIG_DIR（Claude Code の設定ディレクトリ
// 全体を指す。未設定時の既定は ~/.claude）を尊重する。CLAUDE_CONFIG_DIR に相対パスが
// 設定されていた場合も、戻り値は必ず filepath.Abs で絶対パス化する（呼び出し側が
// この結果に os.MkdirAll するため、カレントディレクトリ依存で意図せぬ場所に書く事故を防ぐ）。
func ResolvePath(target string, scope Scope, repoRoot string) (string, error) {
	canonical, err := ResolveTarget(target)
	if err != nil {
		return "", err
	}

	var dir string
	switch scope {
	case ScopeProject:
		if repoRoot == "" {
			return "", fmt.Errorf("skills: scope=project には repoRoot が必要です")
		}
		dir = filepath.Join(repoRoot, targetDirNames[canonical], "skills")
	case ScopeUser:
		base, err := userConfigDir(canonical)
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "skills")
	default:
		return "", fmt.Errorf("skills: 未知の scope です: %q（project | user）", scope)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("skills: %q を絶対パスに変換できません: %w", dir, err)
	}
	return abs, nil
}

func userConfigDir(canonical string) (string, error) {
	if canonical == "claude" {
		if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
			return v, nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("skills: ホームディレクトリを取得できません: %w", err)
	}
	return filepath.Join(home, targetDirNames[canonical]), nil
}
