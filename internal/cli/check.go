package cli

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/command"
	"github.com/fuchigta/spotter/internal/check/commitintent"
	"github.com/fuchigta/spotter/internal/check/commitsubject"
	"github.com/fuchigta/spotter/internal/check/companionfiles"
	"github.com/fuchigta/spotter/internal/check/consistency"
	"github.com/fuchigta/spotter/internal/check/diffcontent"
	"github.com/fuchigta/spotter/internal/check/diffsize"
	"github.com/fuchigta/spotter/internal/check/doclinks"
	"github.com/fuchigta/spotter/internal/check/docpaths"
	"github.com/fuchigta/spotter/internal/check/docsync"
	"github.com/fuchigta/spotter/internal/check/unwantedfiles"
	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/exempt"
	"github.com/fuchigta/spotter/internal/gitutil"
	"github.com/fuchigta/spotter/internal/rangespec"
)

// repoRoot は今のところ常にカレントディレクトリ（git がフックや CI を実行する場所）。
const repoRoot = "."

func newCheckCommand() *cobra.Command {
	var (
		messageFile string
		rangeExpr   string
		configPath  string
	)

	cmd := &cobra.Command{
		Use:   "check [検査名]",
		Short: "設定済みの検査を実行する（検査名を指定すればそれだけを実行する）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				only = args[0]
			}
			return runCheck(cmd.OutOrStdout(), cmd.ErrOrStderr(), configPath, messageFile, rangeExpr, only)
		},
	}

	cmd.Flags().StringVar(&messageFile, "message", "", "ステージ済みの変更を見る（commit-msg フック向け。コミットメッセージのファイルを指定する）")
	cmd.Flags().StringVar(&rangeExpr, "range", "", "その範囲のコミットを見る（CI 向け。git の範囲式）")
	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "設定ファイルのパス")

	return cmd
}

// invocation は 1 回の検査起動に必要な情報（staged/range/worktree どのモードかを吸収済み）。
type invocation struct {
	ctx   check.Context
	label string
	// messages は免除判定に使う、コミットごとに分けたメッセージ本文
	// （GranularityWorktree では使わないため空のまま）。
	messages []string
}

func runCheck(stdout, stderr io.Writer, configPath, messageFile, rangeExpr, only string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := checkRequiredVersion(cfg); err != nil {
		return fmt.Errorf("check: %w", err)
	}

	keys, err := selectKeys(cfg, only)
	if err != nil {
		return err
	}

	repo := gitutil.New(repoRoot)

	// --range 指定が無いときは staged（commit-msg フック）を見る経路で、CI の --range
	// が RevListNoMerges でマージコミットを除外しているのと同じ扱いに揃える。
	// コンフリクト解消後の `git commit` でも MERGE_HEAD は残っているため、
	// commit-msg フックの時点でここに来て検査せず成功終了する。
	if rangeExpr == "" {
		inMerge, err := repo.InMerge()
		if err != nil {
			return fmt.Errorf("check: %w", err)
		}
		if inMerge {
			fmt.Fprintln(stderr, "マージコミットのため検査しません（CI の範囲検査と同じ扱い）")
			return nil
		}
	}

	failed := false

	for _, key := range keys {
		cc := cfg.Checks[key]

		runner, err := buildRunner(cfg, key, cc)
		if err != nil {
			return fmt.Errorf("check: checks.%s: %w", key, err)
		}

		granularity := runner.Granularity()

		invocations, err := planInvocations(repo, granularity, rangeExpr, messageFile)
		if err != nil {
			return fmt.Errorf("check: checks.%s: %w", key, err)
		}

		// GranularityWorktree（doc-paths など）はコミットメッセージに依存しないため、
		// 免除トレーラの仕組み自体を持たない。
		var exemptCfg exempt.Config
		if granularity != check.GranularityWorktree {
			enable, trailer := cfg.ResolveExempt(key, cc)
			exemptCfg = exempt.Config{Enable: enable, Trailer: trailer}
		}

		for _, inv := range invocations {
			if granularity != check.GranularityWorktree {
				skip, reason, err := exemptFromMessages(exemptCfg, inv.messages)
				if err != nil {
					return fmt.Errorf("check: checks.%s: %w", key, err)
				}
				if skip {
					fmt.Fprintf(stdout, "%s: 免除されました（%s: skip %s）\n", key, exemptCfg.Trailer, reason)
					continue
				}
			}

			violations, err := runner.Run(inv.ctx)
			if err != nil {
				return fmt.Errorf("check: checks.%s: %w", key, err)
			}
			if len(violations) == 0 {
				continue
			}

			failed = true
			printViolations(stderr, key, inv.label, violations)
		}
	}

	if failed {
		return ErrCheckFailed
	}
	return nil
}

func selectKeys(cfg *config.Config, only string) ([]string, error) {
	if only != "" {
		if _, ok := cfg.Checks[only]; !ok {
			return nil, fmt.Errorf("check: 設定に checks.%s がありません", only)
		}
		return []string{only}, nil
	}

	keys := make([]string, 0, len(cfg.Checks))
	for k := range cfg.Checks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func buildRunner(cfg *config.Config, key string, cc config.CheckConfig) (check.Runner, error) {
	switch cc.Type {
	case config.TypeDocSync:
		return docsync.New(cc)
	case config.TypeUnwantedFiles:
		return unwantedfiles.New(cc)
	case config.TypeDocPaths:
		return docpaths.New(cc)
	case config.TypeCommitSubject:
		return commitsubject.New(cc)
	case config.TypeConsistency:
		return consistency.New(cc)
	case config.TypeDiffContent:
		return diffcontent.New(cc)
	case config.TypeCommitIntent:
		return commitintent.New(cc)
	case config.TypeCompanionFiles:
		return companionfiles.New(cc)
	case config.TypeDocLinks:
		return doclinks.New(cc)
	case config.TypeDiffSize:
		return diffsize.New(cc)
	default:
		// config.Load が既に「types.<type> に command が登録されているか」を検証済み。
		tc := cfg.Types[cc.Type]
		return command.New(key, cc, tc)
	}
}

func planInvocations(repo *gitutil.Repo, granularity check.Granularity, rangeExpr, messageFile string) ([]invocation, error) {
	if granularity == check.GranularityWorktree {
		// staged/range の指定に関わらず、現在の作業ツリーを 1 回だけ見る。
		return []invocation{{ctx: check.Context{Root: repoRoot}}}, nil
	}

	if rangeExpr != "" {
		plans, err := rangespec.Plan(repo, rangeExpr, granularity)
		if err != nil {
			return nil, err
		}
		invocations := make([]invocation, 0, len(plans))
		for _, p := range plans {
			invocations = append(invocations, invocation{
				ctx: check.Context{
					Root:    repoRoot,
					Source:  p.Source,
					Message: p.Message,
					Range:   &check.RangeRef{From: p.From, To: p.To},
				},
				label:    p.Label,
				messages: p.Messages,
			})
		}
		return invocations, nil
	}

	msg := ""
	if messageFile != "" {
		data, err := os.ReadFile(messageFile)
		if err != nil {
			return nil, fmt.Errorf("メッセージファイル %s の読み込みに失敗しました: %w", messageFile, err)
		}
		msg = string(data)
	}
	return []invocation{{
		ctx:      check.Context{Root: repoRoot, Source: repo.StagedSource(), Message: msg},
		messages: []string{msg},
	}}, nil
}

// exemptFromMessages は messages（squashed なら範囲内の全コミット、per-commit/staged
// なら 1 件）を新しい順に見て、いずれかのトレーラ段落に免除トレーラがあれば免除する。
// squashed 粒度の「範囲内のどれか 1 コミットに書けば効く」という仕様はここに現れる
// （コミットごとにトレーラ段落を取り出して判定するため、1 コミット目の本文途中に
// 書いた skip は無視され、2 コミット目のトレーラ段落に書いた skip は拾われる）。
func exemptFromMessages(cfg exempt.Config, messages []string) (skip bool, reason string, err error) {
	for _, msg := range messages {
		skip, reason, err = exempt.Check(cfg, msg)
		if err != nil {
			return false, "", err
		}
		if skip {
			return true, reason, nil
		}
	}
	return false, "", nil
}

func printViolations(w io.Writer, key, label string, violations []check.Violation) {
	fmt.Fprintf(w, "%s の検査に失敗しました%s。\n\n", key, label)
	for _, v := range violations {
		fmt.Fprintf(w, "  %s\n", v.Summary)
		for _, f := range v.Files {
			fmt.Fprintf(w, "    - %s\n", f)
		}
	}
	fmt.Fprintln(w)
}
