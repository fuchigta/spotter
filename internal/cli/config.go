package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/confighygiene"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: ".spotter.yml の衛生検査",
	}

	cmd.AddCommand(newConfigLintCommand())

	return cmd
}

func newConfigLintCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "現在のワークツリーと噛み合わなくなった設定（死んだパターン・未参照の type）を検出する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigLint(cmd.OutOrStdout(), configPath)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "設定ファイルのパス")

	return cmd
}

// runConfigLint は config.Load が見る「構文として正しいか」とは別に、
// .spotter.yml がリポジトリの実情と噛み合っているかを検証する。
// リポジトリルートの解決には常に repoRoot（カレントディレクトリ）を使う。
// spotter checks --json 同様、config.Load でエラーになる場合はそちらを優先して
// 報告する（衛生検査は構文が正しい設定にしか意味を持たないため）。
func runConfigLint(stdout io.Writer, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	findings := confighygiene.Lint(cfg, os.DirFS(repoRoot))

	if len(findings) == 0 {
		fmt.Fprintln(stdout, "陳腐化した設定は見つかりませんでした。")
		return nil
	}

	for _, f := range findings {
		fmt.Fprintf(stdout, "%s.%s: %s\n", f.Check, f.Field, f.Message)
	}
	fmt.Fprintf(stdout, "\n%d 件見つかりました。\n", len(findings))

	return ErrCheckFailed
}
