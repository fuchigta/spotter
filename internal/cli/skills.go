package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	spotter "github.com/fuchigta/spotter"
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
