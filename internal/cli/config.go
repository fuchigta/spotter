package cli

import (
	"encoding/json"
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
	var (
		configPath string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "現在のワークツリーと噛み合わなくなった設定（死んだパターン・未参照の type）を検出する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigLint(cmd.OutOrStdout(), configPath, jsonOutput)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "設定ファイルのパス")
	cmd.Flags().BoolVar(&jsonOutput, "json", false,
		"機械可読な JSON で出力する（スキルなど外部ツールからの利用を想定）")

	return cmd
}

// runConfigLint は config.Load が見る「構文として正しいか」とは別に、
// .spotter.yml がリポジトリの実情と噛み合っているかを検証する。
// リポジトリルートの解決には常に repoRoot（カレントディレクトリ）を使う。
// config.Load 自体がエラー（YAML 構文エラー・未対応 type 等）ならそちらを
// そのまま返す（構文が壊れた設定に衛生検査は意味を持たないため。
// spotter check や spotter doctor と同じ順序）。
func runConfigLint(stdout io.Writer, configPath string, jsonOutput bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	findings := confighygiene.Lint(cfg, os.DirFS(repoRoot))

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			return fmt.Errorf("cli: config lint の出力に失敗しました: %w", err)
		}
		if len(findings) > 0 {
			return ErrCheckFailed
		}
		return nil
	}

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
