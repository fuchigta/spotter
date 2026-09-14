package cli

import (
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
	cmd := &cobra.Command{
		Use:   "list",
		Short: "同梱スキルの一覧を表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsList(cmd.OutOrStdout())
		},
	}

	return cmd
}

func runSkillsList(stdout io.Writer) error {
	metas, err := skillsCatalog().List()
	if err != nil {
		return err
	}
	for _, m := range metas {
		fmt.Fprintf(stdout, "%s: %s\n", m.Name, m.Description)
	}
	return nil
}

func newSkillsShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "指定したスキルの中身（SKILL.md と合成される references）を表示する",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsShow(cmd.OutOrStdout(), args[0])
		},
	}

	return cmd
}

func runSkillsShow(stdout io.Writer, name string) error {
	files, err := skillsCatalog().Compose(name)
	if err != nil {
		return err
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		fmt.Fprintf(stdout, "=== %s ===\n", p)
		content := files[p]
		stdout.Write(content)
		if len(content) == 0 || content[len(content)-1] != '\n' {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}
