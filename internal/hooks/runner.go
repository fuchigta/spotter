package hooks

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Runner は spotter 以外のフックランナー。フックファイルが生成物で spotter の管理
// ブロックを持てないため、設定側に spotter の呼び出しがあるかを別に確かめる。
type Runner string

const (
	RunnerLefthook  Runner = "lefthook"
	RunnerHusky     Runner = "husky"
	RunnerPreCommit Runner = "pre-commit"
)

// RunnerConfig は Runner の設定に spotter の呼び出しがあるかの調査結果。
type RunnerConfig struct {
	Runner Runner
	// Found は設定に `spotter check` を呼ぶ記述が見つかったか。
	Found bool
	// Location は Found のときの場所（例: "lefthook.yml: pre-push.commands.spotter"）。
	Location string
	// Problem は Found でも動かない原因があるときの説明。無ければ空。
	Problem string
}

const spotterCheckCall = "spotter check"

// DetectRunner はフックファイルのパスと中身から、既知のフックランナーのものかを判定する。
// husky はフックファイルが `.husky/_/` の下に生成され中身から判別しにくいためパスで見る。
func DetectRunner(hookFile, content string) (Runner, bool) {
	slashed := filepath.ToSlash(hookFile)
	switch {
	case strings.Contains(slashed, "/.husky/") || strings.HasPrefix(slashed, ".husky/"):
		return RunnerHusky, true
	case strings.Contains(content, "lefthook"):
		return RunnerLefthook, true
	case strings.Contains(content, "pre-commit") || strings.Contains(content, "pre_commit"):
		return RunnerPreCommit, true
	}
	return "", false
}

// InspectRunnerConfig は fsys（リポジトリのルート）の下にあるランナーの設定を読み、
// フック h から spotter が呼ばれる設定かを調べる。
func InspectRunnerConfig(fsys fs.FS, r Runner, h Hook) (RunnerConfig, error) {
	switch r {
	case RunnerLefthook:
		return inspectLefthook(fsys, h)
	case RunnerHusky:
		return inspectHusky(fsys, h)
	case RunnerPreCommit:
		return inspectPreCommit(fsys, h)
	}
	return RunnerConfig{}, fmt.Errorf("hooks: 未知のフックランナーです: %q", r)
}

func readOptional(fsys fs.FS, name string) ([]byte, bool, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("hooks: %s の読み込みに失敗しました: %w", name, err)
	}
	return data, true, nil
}

func inspectHusky(fsys fs.FS, h Hook) (RunnerConfig, error) {
	name := ".husky/" + string(h)
	cfg := RunnerConfig{Runner: RunnerHusky}
	data, ok, err := readOptional(fsys, name)
	if err != nil {
		return cfg, err
	}
	if ok && strings.Contains(string(data), spotterCheckCall) {
		cfg.Found = true
		cfg.Location = name
	}
	return cfg, nil
}

var lefthookFiles = []string{
	"lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml",
	"lefthook-local.yml", "lefthook-local.yaml", ".lefthook-local.yml", ".lefthook-local.yaml",
}

func inspectLefthook(fsys fs.FS, h Hook) (RunnerConfig, error) {
	cfg := RunnerConfig{Runner: RunnerLefthook}
	for _, name := range lefthookFiles {
		data, ok, err := readOptional(fsys, name)
		if err != nil {
			return cfg, err
		}
		if !ok {
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return cfg, fmt.Errorf("hooks: %s の解析に失敗しました: %w", name, err)
		}
		section, _ := doc[string(h)].(map[string]any)
		loc, problem, found := findLefthookCall(section, string(h), h)
		if !found {
			continue
		}
		// 問題のある記述より、問題の無い記述を別ファイルで見つけたほうを採る
		// （lefthook-local.yml が use_stdin を補う構成がありうる）。
		if !cfg.Found || problem == "" {
			cfg = RunnerConfig{Runner: RunnerLefthook, Found: true, Location: name + ": " + loc, Problem: problem}
		}
		if problem == "" {
			break
		}
	}
	return cfg, nil
}

func findLefthookCall(section map[string]any, prefix string, h Hook) (loc, problem string, found bool) {
	if commands, ok := section["commands"].(map[string]any); ok {
		keys := make([]string, 0, len(commands))
		for k := range commands {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			cmd, _ := commands[key].(map[string]any)
			if l, p, ok := lefthookEntry(cmd, prefix+".commands."+key, h); ok {
				return l, p, true
			}
		}
	}
	return findLefthookJobs(section["jobs"], prefix+".jobs", h)
}

func findLefthookJobs(v any, prefix string, h Hook) (loc, problem string, found bool) {
	jobs, _ := v.([]any)
	for i, j := range jobs {
		job, _ := j.(map[string]any)
		label := fmt.Sprintf("%s[%d]", prefix, i)
		if n, ok := job["name"].(string); ok && n != "" {
			label = prefix + "." + n
		}
		if l, p, ok := lefthookEntry(job, label, h); ok {
			return l, p, true
		}
		group, _ := job["group"].(map[string]any)
		if l, p, ok := findLefthookJobs(group["jobs"], label+".group.jobs", h); ok {
			return l, p, true
		}
	}
	return "", "", false
}

func lefthookEntry(entry map[string]any, loc string, h Hook) (string, string, bool) {
	run, _ := entry["run"].(string)
	if !strings.Contains(run, spotterCheckCall) {
		return "", "", false
	}
	if h == HookPrePush && strings.Contains(run, "--pre-push") {
		if stdin, _ := entry["use_stdin"].(bool); !stdin {
			return loc, "use_stdin: true が付いていないため、push する ref の並びが届かず検査されません", true
		}
	}
	return loc, "", true
}

func inspectPreCommit(fsys fs.FS, h Hook) (RunnerConfig, error) {
	cfg := RunnerConfig{Runner: RunnerPreCommit}
	const name = ".pre-commit-config.yaml"
	data, ok, err := readOptional(fsys, name)
	if err != nil || !ok {
		return cfg, err
	}
	var doc struct {
		Repos []struct {
			Hooks []struct {
				ID     string   `yaml:"id"`
				Entry  string   `yaml:"entry"`
				Stages []string `yaml:"stages"`
			} `yaml:"hooks"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return cfg, fmt.Errorf("hooks: %s の解析に失敗しました: %w", name, err)
	}
	for _, repo := range doc.Repos {
		for _, hk := range repo.Hooks {
			if !strings.Contains(hk.Entry, spotterCheckCall) || !stageMatches(hk.Stages, h) {
				continue
			}
			cfg.Found = true
			cfg.Location = name + ": " + hk.ID
			return cfg, nil
		}
	}
	return cfg, nil
}

// stageMatches は stages 未指定なら絞り込まれていないとみなす。pre-commit は pre-push を
// 旧名の push とも呼ぶ。
func stageMatches(stages []string, h Hook) bool {
	if len(stages) == 0 {
		return true
	}
	for _, s := range stages {
		if s == string(h) || (h == HookPrePush && s == "push") {
			return true
		}
	}
	return false
}
