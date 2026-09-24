package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/gitutil"
	"github.com/fuchigta/spotter/internal/hooks"
)

func newHooksCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "git フックの設置・管理",
	}

	cmd.AddCommand(newHooksInstallCommand())

	return cmd
}

func newHooksInstallCommand() *cobra.Command {
	var (
		printOnly bool
		hooksDir  string
		hookNames string
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "commit-msg / pre-push フックを設置する（core.hooksPath の設定、または既存フックへの追記）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := resolveHooks(hookNames)
			if err != nil {
				return err
			}
			if printOnly {
				printHooksInvocation(cmd.OutOrStdout(), selected)
				return nil
			}
			return runHooksInstall(cmd.OutOrStdout(), hooksDir, selected)
		},
	}

	cmd.Flags().BoolVar(&printOnly, "print", false,
		"呼び出し行だけを出力する（何も変更しない。lefthook などの既存フックランナーに貼る用）")
	cmd.Flags().StringVar(&hooksDir, "hooks-dir", ".githooks",
		"core.hooksPath が未設定のときに新規作成するディレクトリ")
	cmd.Flags().StringVar(&hookNames, "hook", "",
		"設置するフックをカンマ区切りで絞る（commit-msg, pre-push。省略時は両方）")

	return cmd
}

// resolveHooks は --hook の値をフックの一覧に変換する。空なら hooks.DefaultHooks()
// （commit-msg と pre-push の両方）を使う。同じ名前が重複していても 1 つにまとめる。
func resolveHooks(names string) ([]hooks.Hook, error) {
	if names == "" {
		return hooks.DefaultHooks(), nil
	}

	seen := make(map[hooks.Hook]bool)
	var selected []hooks.Hook
	for _, n := range strings.Split(names, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		h, err := hooks.ParseHook(n)
		if err != nil {
			return nil, err
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		selected = append(selected, h)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("hooks: --hook に有効なフック名を指定してください")
	}
	return selected, nil
}

// printHooksInvocation は --print の出力を書く。フックを 1 つに絞ったときは呼び出し行
// 1 行だけ、複数（既定）のときはどのフック向けか分かるよう `<hook>: <行>` の形にする。
func printHooksInvocation(stdout io.Writer, selected []hooks.Hook) {
	if len(selected) == 1 {
		fmt.Fprintln(stdout, hooks.InvocationLine(selected[0]))
		return
	}
	for _, h := range selected {
		fmt.Fprintf(stdout, "%s: %s\n", h, hooks.InvocationLine(h))
	}
}

func runHooksInstall(stdout io.Writer, hooksDir string, selected []hooks.Hook) error {
	repo := gitutil.New(repoRoot)

	result, err := hooks.Install(repo, hooksDir, selected)
	if err != nil {
		return fmt.Errorf("hooks install: %w", err)
	}

	for _, hr := range result.Hooks {
		switch hr.Outcome {
		case hooks.OutcomeAlready:
			fmt.Fprintf(stdout, "%s: 既に設置済みです: %s\n", hr.Hook, hr.HookFile)
		case hooks.OutcomeCreated:
			fmt.Fprintf(stdout, "%s: フックを新規作成しました: %s\n", hr.Hook, hr.HookFile)
		case hooks.OutcomeAppended:
			fmt.Fprintf(stdout, "%s: 既存のフックに追記しました: %s\n", hr.Hook, hr.HookFile)
		}
	}

	if result.HooksPathChanged {
		fmt.Fprintf(stdout, "core.hooksPath を %s に設定しました\n", result.HooksPath)
	} else if result.HooksPath != "" {
		fmt.Fprintf(stdout, "core.hooksPath は %s のまま変更していません\n", result.HooksPath)
	}

	return nil
}
