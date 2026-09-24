package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
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
		if err := enc.Encode(metas); err != nil {
			return fmt.Errorf("cli: skills list の出力に失敗しました: %w", err)
		}
		return nil
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
		return fmt.Errorf("cli: 出力の書き込みに失敗しました: %w", err)
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
		// skills.ResolvePath は常に絶対パスを返す（カレントディレクトリ依存の
		// 事故を防ぐため）。--dir 指定時もその挙動と揃える。
		abs, err := filepath.Abs(dirFlag)
		if err != nil {
			return "", fmt.Errorf("skills: --dir %q を絶対パスに変換できません: %w", dirFlag, err)
		}
		return abs, nil
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

// newSkillsScopedCommand は install/uninstall で共通のフラグ（scope/dir/only/force/
// dry-run）を持つサブコマンドを組み立てる。run は解決済みのフラグ値を受け取り本体を
// 実行する（runSkillsInstall / runSkillsUninstall のシグネチャに合わせている）。
func newSkillsScopedCommand(use, short, scopeHelp, dirHelp, onlyHelp, forceHelp, dryRunHelp string, run func(stdout io.Writer, target, scope, dir, only string, force, dryRun bool) error) *cobra.Command {
	var (
		scope  string
		dir    string
		only   string
		force  bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.OutOrStdout(), args[0], scope, dir, only, force, dryRun)
		},
	}

	cmd.Flags().StringVar(&scope, "scope", string(skills.ScopeProject), scopeHelp)
	cmd.Flags().StringVar(&dir, "dir", "", dirHelp)
	cmd.Flags().StringVar(&only, "only", "", onlyHelp)
	cmd.Flags().BoolVar(&force, "force", false, forceHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, dryRunHelp)

	return cmd
}

func newSkillsInstallCommand() *cobra.Command {
	return newSkillsScopedCommand(
		"install <target>",
		"スキルを設置する（target: claude | agents | all、またはそのエイリアス）",
		"設置範囲（project | user）",
		"出力先ディレクトリを直接指定する（--scope より優先。target に all は指定できない）",
		"設置するスキルをカンマ区切りで絞る（省略時は全部）",
		"spotter 管理外のディレクトリがあっても上書きする",
		"書き込まずに、何をどこへ書くかだけ表示する",
		runSkillsInstall,
	)
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
			if err := printInstallDryRun(stdout, installer, t, dir, names); err != nil {
				return err
			}
			continue
		}

		// results は err != nil でも途中まで完了した分を含む（Install は
		// 1件失敗した時点でそこまでの結果とエラーを返す）。エラーで打ち切る前に
		// 必ず出力する。そうしないと「実際には設置済みなのにエラー行しか
		// 見えない」状態になる。
		results, err := installer.Install(dir, names, force)
		for _, r := range results {
			fmt.Fprintf(stdout, "%s (%s): %s -> %s\n", r.Name, t, r.Outcome, r.Dir)
		}
		if err != nil {
			return fmt.Errorf("skills: %s: %w", t, err)
		}
	}

	return nil
}

// printInstallDryRun は --dry-run 時に、書き込みを行わず何が起きる予定かを表示する。
// names が空なら全同梱スキルが対象。names に未知のスキル名が含まれていれば
// エラーにする（--dry-run のときだけタイポに気付けないのでは、事前確認という
// dry-run の目的が果たせないため、通常実行と同じ検証をここでも行う）。
func printInstallDryRun(stdout io.Writer, installer skills.Installer, target, dir string, names []string) error {
	entries, err := installer.Status(dir)
	if err != nil {
		return err
	}

	byName := make(map[string]skills.StatusEntry, len(entries))
	var all []string
	for _, e := range entries {
		byName[e.Name] = e
		all = append(all, e.Name)
	}

	targetNames := names
	if len(targetNames) == 0 {
		targetNames = all
	} else {
		for _, n := range targetNames {
			if _, ok := byName[n]; !ok {
				return fmt.Errorf("skills: 未知のスキルです: %q", n)
			}
		}
	}

	fmt.Fprintf(stdout, "[dry-run] %s -> %s\n", target, dir)
	for _, n := range targetNames {
		fmt.Fprintf(stdout, "  %s: %s\n", n, dryRunPrediction(byName[n]))
	}
	return nil
}

// dryRunPrediction は Install を実行した場合に予想される Outcome を、
// 実際には書き込まずに Status の情報から推測する。force の有無は考慮しない
// （--force を付けたときの挙動まで正確に予測しようとすると、force が
// 「管理外を上書きしてよい」以上の意味を持たないことの前提が崩れたときに
// 追随漏れが起きやすいため、force 無しでの予測に統一している）。
func dryRunPrediction(e skills.StatusEntry) string {
	switch {
	case !e.Installed:
		return "created"
	case !e.Managed:
		return "spotter 管理外（--force が無いとエラーになります）"
	case e.UpToDate:
		return "already"
	default:
		return "updated"
	}
}

func newSkillsUninstallCommand() *cobra.Command {
	return newSkillsScopedCommand(
		"uninstall <target>",
		"spotter が設置したスキルを削除する（target: claude | agents | all、またはそのエイリアス）",
		"対象範囲（project | user）",
		"対象ディレクトリを直接指定する（--scope より優先。target に all は指定できない）",
		"削除するスキルをカンマ区切りで絞る（省略時は dir 直下の全ディレクトリ）",
		"spotter 管理外のディレクトリも削除する",
		"削除せずに、何を削除する予定かだけ表示する",
		runSkillsUninstall,
	)
}

func runSkillsUninstall(stdout io.Writer, target, scopeStr, dirFlag, only string, force, dryRun bool) error {
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

		// Uninstall は「対象を全部検証してから削除する」2パス方式なので、
		// 検証段階のエラーでは results は空（何も削除されていない）。
		// 削除の実行段階（RemoveAll）で失敗した場合は、それまでに削除できた
		// 分を含んだ results が返る。どちらのケースでも、エラーで打ち切る前に
		// 必ず出力する。
		results, err := installer.Uninstall(dir, names, force, dryRun)
		prefix := ""
		if dryRun {
			prefix = "[dry-run] "
		}
		for _, r := range results {
			status := "設置されていません"
			switch {
			case r.Removed:
				status = "削除しました"
			case dryRun && r.WouldRemove:
				status = "削除される予定"
			}
			fmt.Fprintf(stdout, "%s%s (%s): %s（%s）\n", prefix, r.Name, t, status, r.Dir)
		}
		if err != nil {
			return fmt.Errorf("skills: %s: %w", t, err)
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
