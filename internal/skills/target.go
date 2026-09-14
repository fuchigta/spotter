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
		return "", fmt.Errorf("skills: 未知のターゲットです: %q（claude | agents、またはそのエイリアス: claude-code, codex, gemini, cursor, copilot）", name)
	}
	return canonical, nil
}

// ResolvePath はターゲット（エイリアス可）とスコープから、スキルを設置すべき
// ディレクトリの絶対パスを返す。
//
// scope が ScopeProject のときは repoRoot が必須（gitutil.Repo.TopLevel() の結果を
// 渡す想定。カレントディレクトリがリポジトリのサブディレクトリでも正しいルートを使うため）。
// scope が ScopeUser のときは repoRoot を無視する。
//
// "claude" の user スコープは環境変数 CLAUDE_CONFIG_DIR（Claude Code の設定ディレクトリ
// 全体を指す。未設定時の既定は ~/.claude）を尊重する。
func ResolvePath(target string, scope Scope, repoRoot string) (string, error) {
	canonical, err := ResolveTarget(target)
	if err != nil {
		return "", err
	}

	switch scope {
	case ScopeProject:
		if repoRoot == "" {
			return "", fmt.Errorf("skills: scope=project には repoRoot が必要です")
		}
		return filepath.Join(repoRoot, targetDirNames[canonical], "skills"), nil
	case ScopeUser:
		base, err := userConfigDir(canonical)
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "skills"), nil
	default:
		return "", fmt.Errorf("skills: 未知の scope です: %q（project | user）", scope)
	}
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
