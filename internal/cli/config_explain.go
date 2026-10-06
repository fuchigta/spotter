package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

// ExplainOutput は `spotter config explain --json` の出力。
type ExplainOutput struct {
	Check string `json:"check"`
	Type  string `json:"type"`
	check.Explanation
}

func newConfigExplainCommand() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "explain <検査名>",
		Short: "検査が判定に使う抽出結果を、合否を付けずに表示する",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigExplain(cmd.OutOrStdout(), configPath, args[0], jsonOutput)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "設定ファイルのパス")
	cmd.Flags().BoolVar(&jsonOutput, "json", false,
		"機械可読な JSON で出力する（スキルなど外部ツールからの利用を想定）")

	return cmd
}

// runConfigExplain は checks.<key> の説明を作業ツリーから作って出力する。合否は出さず、
// 終了コードにも反映しない。git は呼ばない（作業ツリーだけで決まる worktree 粒度の検査に
// 限るため）。
func runConfigExplain(stdout io.Writer, configPath, key string, jsonOutput bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := checkRequiredVersion(cfg); err != nil {
		return err
	}

	cc, ok := cfg.Checks[key]
	if !ok {
		return fmt.Errorf("config explain: 設定に checks.%s がありません", key)
	}

	runner, err := buildRunner(cfg, key, cc)
	if err != nil {
		return fmt.Errorf("config explain: checks.%s: %w", key, err)
	}

	explainer, ok := runner.(check.Explainer)
	if !ok {
		return fmt.Errorf("config explain: checks.%s（type: %s）は explain に対応していません（対応する type は spotter checks の explain 列で確認できます）", key, cc.Type)
	}

	explanation, err := explainer.Explain(check.Context{FS: os.DirFS(repoRoot)})
	if err != nil {
		return fmt.Errorf("config explain: checks.%s: %w", key, err)
	}

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(ExplainOutput{Check: key, Type: cc.Type, Explanation: explanation}); err != nil {
			return fmt.Errorf("cli: config explain の出力に失敗しました: %w", err)
		}
		return nil
	}

	writeExplanation(stdout, key, cc.Type, explanation)
	return nil
}

func writeExplanation(w io.Writer, key, checkType string, e check.Explanation) {
	fmt.Fprintf(w, "%s（%s）\n", key, checkType)
	for _, d := range e.Details {
		fmt.Fprintf(w, "  %s\n", d)
	}
	for _, s := range e.Sections {
		fmt.Fprintf(w, "  %s\n", s.Title)
		for _, d := range s.Details {
			fmt.Fprintf(w, "    %s\n", d)
		}
		fmt.Fprintf(w, "    要素（%d 件）:\n", len(s.Items))
		for _, it := range s.Items {
			parts := []string{it.Value}
			if len(it.Locations) > 0 {
				parts = append(parts, strings.Join(it.Locations, ", "))
			}
			if it.Note != "" {
				parts = append(parts, it.Note)
			}
			fmt.Fprintf(w, "      - %s\n", strings.Join(parts, "  "))
		}
	}
	fmt.Fprintf(w, "合否は spotter check %s で確認してください。\n", key)
}
