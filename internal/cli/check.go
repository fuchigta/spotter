package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

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

// repoRoot は常にカレントディレクトリ（git がフックや CI を実行する場所）。
const repoRoot = "."

func newCheckCommand() *cobra.Command {
	var (
		messageFile   string
		rangeExpr     string
		prePushRemote string
		configPath    string
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
			if cmd.Flags().Changed("pre-push") {
				return runCheckPrePush(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), configPath, prePushRemote, only)
			}
			return runCheck(cmd.OutOrStdout(), cmd.ErrOrStderr(), configPath, messageFile, rangeExpr, only)
		},
	}

	cmd.Flags().StringVar(&messageFile, "message", "", "ステージ済みの変更を見る（commit-msg フック向け。コミットメッセージのファイルを指定する）")
	cmd.Flags().StringVar(&rangeExpr, "range", "", "その範囲のコミットを見る（CI 向け。git の範囲式）")
	cmd.Flags().StringVar(&prePushRemote, "pre-push", "", "push する前に CI と同じ range 検査を走らせる（pre-push フック向け。git が渡す remote 名を指定する。標準入力から push 対象の ref を読み、remote 自体は範囲の計算に使わず失敗時の案内にだけ使う）")
	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "設定ファイルのパス")
	cmd.MarkFlagsMutuallyExclusive("message", "range", "pre-push")

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

	skip, err := skipForMerge(repo, rangeExpr, stderr)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	var rangeExprs []string
	if rangeExpr != "" {
		rangeExprs = []string{rangeExpr}
	}

	failed := false
	for _, key := range keys {
		keyFailed, err := runCheckKey(cfg, key, repo, rangeExprs, messageFile, stdout, stderr)
		if err != nil {
			return err
		}
		if keyFailed {
			failed = true
		}
	}

	if failed {
		return ErrCheckFailed
	}
	return nil
}

// skipForMerge は --range 指定が無いとき（staged/commit-msg フックを見る経路）だけ、
// マージ中かどうかを確かめる。CI の --range 側は RevListNoMerges でマージコミットを
// 除外しているので、ここでも同じ扱いに揃える。コンフリクト解消後の `git commit` でも
// MERGE_HEAD は残っているため、commit-msg フックの時点でここに来て検査せず成功終了する。
func skipForMerge(repo *gitutil.Repo, rangeExpr string, stderr io.Writer) (bool, error) {
	if rangeExpr != "" {
		return false, nil
	}
	inMerge, err := repo.InMerge()
	if err != nil {
		return false, fmt.Errorf("check: %w", err)
	}
	if !inMerge {
		return false, nil
	}
	fmt.Fprintln(stderr, "マージコミットのため検査しません（CI の範囲検査と同じ扱い）")
	return true, nil
}

// runCheckKey は checks.<key> を 1 つ組み立て、その粒度に応じた invocation ごとに
// 実行する。rangeExprs は range モードで検査する範囲式の一覧（staged/messageFile 側の
// 起動では空）で、pre-push が ref ごとに複数の範囲式を 1 つの key に対して評価できるよう
// 複数持てる形にしている。戻り値は、この key で違反を 1 件でも報告したかどうか。
func runCheckKey(cfg *config.Config, key string, repo *gitutil.Repo, rangeExprs []string, messageFile string, stdout, stderr io.Writer) (bool, error) {
	cc := cfg.Checks[key]

	runner, err := buildRunner(cfg, key, cc)
	if err != nil {
		return false, fmt.Errorf("check: checks.%s: %w", key, err)
	}

	granularity := runner.Granularity()

	invocations, err := planInvocations(repo, granularity, rangeExprs, messageFile)
	if err != nil {
		return false, fmt.Errorf("check: checks.%s: %w", key, err)
	}

	// GranularityWorktree（doc-paths など）はコミットメッセージに依存しないため、
	// 免除トレーラの仕組み自体を持たない。
	var exemptCfg exempt.Config
	if granularity != check.GranularityWorktree {
		enable, trailer := cfg.ResolveExempt(key, cc)
		exemptCfg = exempt.Config{Enable: enable, Trailer: trailer}
	}

	return runInvocations(key, runner, granularity, exemptCfg, invocations, stdout, stderr)
}

// runInvocations は invocations を順に実行する。戻り値は、そのうち 1 件でも違反を
// 報告したかどうか。pre-push が複数の range 式から作った invocations をまとめて渡す
// 経路と、runCheckKey からの経路の両方から使う。
func runInvocations(key string, runner check.Runner, granularity check.Granularity, exemptCfg exempt.Config, invocations []invocation, stdout, stderr io.Writer) (bool, error) {
	failed := false
	for _, inv := range invocations {
		invFailed, err := runInvocation(key, runner, granularity, exemptCfg, inv, stdout, stderr)
		if err != nil {
			return false, err
		}
		if invFailed {
			failed = true
		}
	}
	return failed, nil
}

// runInvocation は invocation を 1 回実行する。検査全体が免除された場合と、違反が無い
// 場合は false を返す（呼び出し側の failed には数えない）。
func runInvocation(key string, runner check.Runner, granularity check.Granularity, exemptCfg exempt.Config, inv invocation, stdout, stderr io.Writer) (bool, error) {
	exemptions, wholeExempt, err := resolveExemptions(key, granularity, exemptCfg, inv, stdout)
	if err != nil {
		return false, err
	}
	if wholeExempt {
		return false, nil
	}

	violations, err := runner.Run(inv.ctx)
	if err != nil {
		return false, fmt.Errorf("check: checks.%s: %w", key, err)
	}

	if scoped := scopedExemptionsFrom(exemptions); len(scoped) > 0 {
		violations, err = applyScopedExemptions(stdout, key, runner, scoped, violations)
		if err != nil {
			return false, fmt.Errorf("check: checks.%s: %w", key, err)
		}
	}

	if len(violations) == 0 {
		return false, nil
	}

	printViolations(stderr, key, inv.label, violations)
	return true, nil
}

// resolveExemptions は inv.messages から免除トレーラを集め、対象を絞らない全体免除が
// あればそれを表示して wholeExempt を true で返す（呼び出し側はその場で invocation を
// 打ち切る）。GranularityWorktree は免除トレーラの仕組み自体を持たないため、
// exemptions は空のまま返す。
func resolveExemptions(key string, granularity check.Granularity, exemptCfg exempt.Config, inv invocation, stdout io.Writer) ([]exempt.Exemption, bool, error) {
	if granularity == check.GranularityWorktree {
		return nil, false, nil
	}

	exemptions, err := collectExemptions(exemptCfg, inv.messages)
	if err != nil {
		return nil, false, fmt.Errorf("check: checks.%s: %w", key, err)
	}
	reasons := wholeExemptionReasons(exemptions)
	if len(reasons) == 0 {
		return exemptions, false, nil
	}

	for _, reason := range reasons {
		fmt.Fprintf(stdout, "%s: 免除されました（%s: skip %s）\n", key, exemptCfg.Trailer, reason)
	}
	return exemptions, true, nil
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

// planInvocations は granularity に応じて invocation 列を組み立てる。rangeExprs が
// 1 件以上あれば range モード（複数件なら pre-push が ref ごとに作った範囲式を
// まとめて評価する経路。GranularitySquashed/PerCommit では範囲式ごとの結果を単純に
// 連結する）、無ければ messageFile を読む staged モードになる。
func planInvocations(repo *gitutil.Repo, granularity check.Granularity, rangeExprs []string, messageFile string) ([]invocation, error) {
	if granularity == check.GranularityWorktree {
		// staged/range の指定・件数に関わらず、現在の作業ツリーを 1 回だけ見る。
		return []invocation{{ctx: check.Context{FS: os.DirFS(repoRoot)}}}, nil
	}

	if len(rangeExprs) > 0 {
		var invocations []invocation
		for _, rangeExpr := range rangeExprs {
			plans, err := rangespec.Plan(repo, rangeExpr, granularity)
			if err != nil {
				return nil, err
			}
			for _, p := range plans {
				invocations = append(invocations, invocation{
					ctx: check.Context{
						Source:  p.Source,
						Message: p.Message,
						Range:   &check.RangeRef{From: p.From, To: p.To},
					},
					label:    p.Label,
					messages: p.Messages,
				})
			}
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
		ctx:      check.Context{Source: repo.StagedSource(), Message: msg},
		messages: []string{msg},
	}}, nil
}

// collectExemptions は messages（squashed なら範囲内の全コミット、per-commit/staged なら
// 1 件）それぞれのトレーラ段落から exempt.Exemption を集めて返す。squashed 粒度の
// 「範囲内のどれか 1 コミットに書けば効く」という仕様は、複数メッセージのうちどれか 1 つに
// でもあれば良い、という形でここに現れる。
func collectExemptions(cfg exempt.Config, messages []string) ([]exempt.Exemption, error) {
	var all []exempt.Exemption
	for _, msg := range messages {
		found, err := exempt.Check(cfg, msg)
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	return all, nil
}

// wholeExemptionReasons は exemptions のうち、対象を絞らない（検査全体を免除する）ものの
// 理由だけを返す。1 つでもあれば検査全体を実行せずに免除する（角括弧付きのスコープ付き免除が
// 同時にあっても、全体免除が優先される）。
func wholeExemptionReasons(exemptions []exempt.Exemption) []string {
	var reasons []string
	for _, e := range exemptions {
		if len(e.Targets) == 0 {
			reasons = append(reasons, e.Reason)
		}
	}
	return reasons
}

// scopedExemption は 1 つのスコープ付き免除の対象と理由。
type scopedExemption struct {
	target string
	reason string
}

// scopedExemptionsFrom は exemptions からスコープ付き免除（Targets が空でないもの）を
// 対象ごとに展開する（"skip[a,b] 理由" は対象 a・b それぞれに同じ理由を持つ要素になる）。
func scopedExemptionsFrom(exemptions []exempt.Exemption) []scopedExemption {
	var scoped []scopedExemption
	for _, e := range exemptions {
		for _, target := range e.Targets {
			scoped = append(scoped, scopedExemption{target: target, reason: e.Reason})
		}
	}
	return scoped
}

// applyScopedExemptions はスコープ付き免除を violations に適用し、対象が一致した
// 違反だけを除いた残りを返す。ScopedExemptable 未実装や書き間違った対象はエラーにする
// （設定・トレーラの誤りを黙って無視しないため）。該当違反が無い対象はエラーにしない。
func applyScopedExemptions(w io.Writer, key string, runner check.Runner, scoped []scopedExemption, violations []check.Violation) ([]check.Violation, error) {
	scopable, ok := runner.(check.ScopedExemptable)
	if !ok {
		return nil, fmt.Errorf("この検査は範囲を絞った免除（skip[対象]）に対応していません")
	}

	targets := scopable.ExemptTargets()
	valid := make(map[string]bool, len(targets))
	for _, t := range targets {
		valid[t] = true
	}
	for _, s := range scoped {
		if !valid[s.target] {
			return nil, fmt.Errorf("対象 %q は免除できる対象の一覧にありません（%s）", s.target, strings.Join(targets, ", "))
		}
	}

	remaining := make([]check.Violation, 0, len(violations))
	for _, v := range violations {
		reason, exempted := "", false
		if v.Target != "" {
			for _, s := range scoped {
				if s.target == v.Target {
					reason, exempted = s.reason, true
					break
				}
			}
		}
		if exempted {
			fmt.Fprintf(w, "%s: %s を免除しました（%s）\n", key, v.Target, reason)
			continue
		}
		remaining = append(remaining, v)
	}
	return remaining, nil
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
