package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	spotter "github.com/fuchigta/spotter"
	"github.com/fuchigta/spotter/internal/gitutil"
	"github.com/fuchigta/spotter/internal/skills"
)

// skillsCatalog はバイナリに埋め込まれた同梱スキル（skills/, docs/）への
// アクセスを返す。
func skillsCatalog() skills.Catalog {
	return skills.NewCatalog(spotter.SkillsFS, spotter.DocsFS)
}

func newSkillsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "コーディングエージェント（Claude Code / Codex など）向けスキルの一覧・表示",
	}

	cmd.AddCommand(newSkillsListCommand())
	cmd.AddCommand(newSkillsShowCommand())
	cmd.AddCommand(newSkillsInstallCommand())
	cmd.AddCommand(newSkillsUninstallCommand())
	cmd.AddCommand(newSkillsStatusCommand())

	return cmd
}

func newSkillsListCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "同梱スキルの一覧を表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsList(cmd.OutOrStdout(), jsonOutput)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "機械可読な JSON で出力する")

	return cmd
}

func runSkillsList(stdout io.Writer, jsonOutput bool) error {
	metas, err := skillsCatalog().List()
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(metas)
	}

	for _, m := range metas {
		fmt.Fprintf(stdout, "%s: %s\n", m.Name, m.Description)
	}
	return nil
}

func newSkillsShowCommand() *cobra.Command {
	var (
		file     string
		listOnly bool
	)

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "指定したスキルの SKILL.md とファイル一覧を表示する（--file で個別ファイルの中身を見る）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsShow(cmd.OutOrStdout(), args[0], file, listOnly)
		},
	}

	cmd.Flags().StringVar(&file, "file", "",
		"指定した1ファイルの中身だけを表示する（Compose 結果の相対パス。例: references/hooks.md）")
	cmd.Flags().BoolVar(&listOnly, "list", false, "ファイルパス一覧だけを表示する（中身は表示しない）")

	return cmd
}

// runSkillsShow は既定では SKILL.md 本体とファイル一覧だけを出す。合成後の
// references/ は docs/ 全体（現状十数ファイル）を含みうるため、全文を無条件に
// ダンプすると progressive disclosure（SKILL.md 自身が「1〜2 ファイルだけ選んで
// 読め」と案内している設計）と矛盾する。個別ファイルの中身は --file で明示的に
// 選ばせる。
func runSkillsShow(stdout io.Writer, name, file string, listOnly bool) error {
	files, err := skillsCatalog().Compose(name)
	if err != nil {
		return err
	}

	if file != "" {
		content, ok := files[file]
		if !ok {
			return fmt.Errorf("skills: %s に %q というファイルはありません（--list で一覧を確認できます）", name, file)
		}
		return writeWithTrailingNewline(stdout, content)
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	if listOnly {
		for _, p := range paths {
			fmt.Fprintln(stdout, p)
		}
		return nil
	}

	if content, ok := files["SKILL.md"]; ok {
		if err := writeWithTrailingNewline(stdout, content); err != nil {
			return err
		}
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "--- ファイル一覧（`spotter skills show", name, "--file <path>` で個別に表示） ---")
	for _, p := range paths {
		if p == "SKILL.md" {
			continue
		}
		fmt.Fprintln(stdout, p)
	}

	return nil
}

func writeWithTrailingNewline(w io.Writer, content []byte) error {
	if _, err := w.Write(content); err != nil {
		return err
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		fmt.Fprintln(w)
	}
	return nil
}

// targetsForArg は install/uninstall/show のターゲット引数を解決する。
// "all" は skills.Targets()（現状 agents, claude）全部に展開する。
func targetsForArg(target string) ([]string, error) {
	if target == "all" {
		return skills.Targets(), nil
	}
	canonical, err := skills.ResolveTarget(target)
	if err != nil {
		return nil, err
	}
	return []string{canonical}, nil
}

func parseScope(s string) (skills.Scope, error) {
	switch skills.Scope(s) {
	case skills.ScopeProject, skills.ScopeUser:
		return skills.Scope(s), nil
	default:
		return "", fmt.Errorf("skills: --scope は project か user のいずれかです（got %q）", s)
	}
}

func splitOnly(only string) []string {
	if only == "" {
		return nil
	}
	parts := strings.Split(only, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			names = append(names, p)
		}
	}
	return names
}

// resolveSkillsDir はターゲット・スコープ・--dir 指定から実際の設置先を決める。
// dirFlag が指定されていればそれを最優先する。scope=project のときだけ
// リポジトリのルート（gitutil.Repo.TopLevel()）を解決する。
func resolveSkillsDir(repo *gitutil.Repo, target string, scope skills.Scope, dirFlag string) (string, error) {
	if dirFlag != "" {
		return dirFlag, nil
	}

	repoRootDir := ""
	if scope == skills.ScopeProject {
		top, err := repo.TopLevel()
		if err != nil {
			return "", fmt.Errorf("skills: リポジトリのルートを解決できません: %w", err)
		}
		repoRootDir = top
	}
	return skills.ResolvePath(target, scope, repoRootDir)
}

func newSkillsInstallCommand() *cobra.Command {
	var (
		scope  string
		dir    string
		only   string
		force  bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "install <target>",
		Short: "スキルを設置する（target: claude | agents | all、またはそのエイリアス）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsInstall(cmd.OutOrStdout(), args[0], scope, dir, only, force, dryRun)
		},
	}

	cmd.Flags().StringVar(&scope, "scope", string(skills.ScopeProject), "設置範囲（project | user）")
	cmd.Flags().StringVar(&dir, "dir", "", "出力先ディレクトリを直接指定する（--scope より優先。target に all は指定できない）")
	cmd.Flags().StringVar(&only, "only", "", "設置するスキルをカンマ区切りで絞る（省略時は全部）")
	cmd.Flags().BoolVar(&force, "force", false, "spotter 管理外のディレクトリがあっても上書きする")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "書き込まずに、何をどこへ書くかだけ表示する")

	return cmd
}

func runSkillsInstall(stdout io.Writer, target, scopeStr, dirFlag, only string, force, dryRun bool) error {
	targets, err := targetsForArg(target)
	if err != nil {
		return err
	}
	if dirFlag != "" && len(targets) > 1 {
		return fmt.Errorf("skills: --dir は単一のターゲットと併用してください（target に all は指定できません）")
	}
	scope, err := parseScope(scopeStr)
	if err != nil {
		return err
	}
	names := splitOnly(only)

	repo := gitutil.New(repoRoot)
	catalog := skillsCatalog()
	installer := skills.NewInstaller(catalog, buildVersion)

	for _, t := range targets {
		dir, err := resolveSkillsDir(repo, t, scope, dirFlag)
		if err != nil {
			return err
		}

		if dryRun {
			targetNames := names
			if len(targetNames) == 0 {
				metas, err := catalog.List()
				if err != nil {
					return err
				}
				for _, m := range metas {
					targetNames = append(targetNames, m.Name)
				}
			}
			fmt.Fprintf(stdout, "[dry-run] %s -> %s\n", t, dir)
			for _, n := range targetNames {
				fmt.Fprintf(stdout, "  %s\n", n)
			}
			continue
		}

		results, err := installer.Install(dir, names, force)
		if err != nil {
			return fmt.Errorf("skills: %s: %w", t, err)
		}
		for _, r := range results {
			fmt.Fprintf(stdout, "%s (%s): %s -> %s\n", r.Name, t, r.Outcome, r.Dir)
		}
	}

	return nil
}

func newSkillsUninstallCommand() *cobra.Command {
	var (
		scope string
		dir   string
		only  string
		force bool
	)

	cmd := &cobra.Command{
		Use:   "uninstall <target>",
		Short: "spotter が設置したスキルを削除する（target: claude | agents | all、またはそのエイリアス）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsUninstall(cmd.OutOrStdout(), args[0], scope, dir, only, force)
		},
	}

	cmd.Flags().StringVar(&scope, "scope", string(skills.ScopeProject), "対象範囲（project | user）")
	cmd.Flags().StringVar(&dir, "dir", "", "対象ディレクトリを直接指定する（--scope より優先。target に all は指定できない）")
	cmd.Flags().StringVar(&only, "only", "", "削除するスキルをカンマ区切りで絞る（省略時は全部）")
	cmd.Flags().BoolVar(&force, "force", false, "spotter 管理外のディレクトリも削除する")

	return cmd
}

func runSkillsUninstall(stdout io.Writer, target, scopeStr, dirFlag, only string, force bool) error {
	targets, err := targetsForArg(target)
	if err != nil {
		return err
	}
	if dirFlag != "" && len(targets) > 1 {
		return fmt.Errorf("skills: --dir は単一のターゲットと併用してください（target に all は指定できません）")
	}
	scope, err := parseScope(scopeStr)
	if err != nil {
		return err
	}
	names := splitOnly(only)

	repo := gitutil.New(repoRoot)
	installer := skills.NewInstaller(skillsCatalog(), buildVersion)

	for _, t := range targets {
		dir, err := resolveSkillsDir(repo, t, scope, dirFlag)
		if err != nil {
			return err
		}

		results, err := installer.Uninstall(dir, names, force)
		if err != nil {
			return fmt.Errorf("skills: %s: %w", t, err)
		}
		for _, r := range results {
			status := "削除しました"
			if !r.Removed {
				status = "設置されていません"
			}
			fmt.Fprintf(stdout, "%s (%s): %s（%s）\n", r.Name, t, status, r.Dir)
		}
	}

	return nil
}

func newSkillsStatusCommand() *cobra.Command {
	var scope string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "設置済みスキルの状況（設置有無・spotter 管理下か・最新版かどうか）を表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsStatus(cmd.OutOrStdout(), scope)
		},
	}

	cmd.Flags().StringVar(&scope, "scope", string(skills.ScopeProject), "確認する範囲（project | user）")

	return cmd
}

func runSkillsStatus(stdout io.Writer, scopeStr string) error {
	scope, err := parseScope(scopeStr)
	if err != nil {
		return err
	}

	repo := gitutil.New(repoRoot)
	installer := skills.NewInstaller(skillsCatalog(), buildVersion)

	for _, t := range skills.Targets() {
		dir, err := resolveSkillsDir(repo, t, scope, "")
		if err != nil {
			return err
		}
		entries, err := installer.Status(dir)
		if err != nil {
			return fmt.Errorf("skills: %s: %w", t, err)
		}

		fmt.Fprintf(stdout, "%s (%s):\n", t, dir)
		for _, e := range entries {
			fmt.Fprintf(stdout, "  %s: %s\n", e.Name, statusLabel(e))
		}
	}

	return nil
}

func statusLabel(e skills.StatusEntry) string {
	switch {
	case !e.Installed:
		return "未設置"
	case !e.Managed:
		return "spotter 管理外"
	case e.UpToDate:
		return fmt.Sprintf("最新（%s）", e.InstalledVersion)
	default:
		return fmt.Sprintf("更新あり（設置済み %s → 最新 %s）", e.InstalledVersion, e.CurrentVersion)
	}
}
