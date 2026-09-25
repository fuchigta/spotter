package cli

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/command"
	"github.com/fuchigta/spotter/internal/check/commitintent"
	"github.com/fuchigta/spotter/internal/check/commitsubject"
	"github.com/fuchigta/spotter/internal/check/companionfiles"
	"github.com/fuchigta/spotter/internal/check/configguard"
	"github.com/fuchigta/spotter/internal/check/consistency"
	"github.com/fuchigta/spotter/internal/check/diffcontent"
	"github.com/fuchigta/spotter/internal/check/diffsize"
	"github.com/fuchigta/spotter/internal/check/doclinks"
	"github.com/fuchigta/spotter/internal/check/docpaths"
	"github.com/fuchigta/spotter/internal/check/docsync"
	"github.com/fuchigta/spotter/internal/check/unwantedfiles"
	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/configdiff"
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

	keys := selectKeys(cfg, only)

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

	guardFailed, guardRan, err := runConfigGuard(cfg, repo, configPath, rangeExprs, messageFile, only, stdout, stderr)
	if err != nil {
		return err
	}
	if guardFailed {
		failed = true
	}

	if only != "" && len(keys) == 0 && !guardRan {
		return fmt.Errorf("check: 設定に checks.%s がありません", only)
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
	exemptions, wholeExempt, err := resolveExemptions(key, runner, granularity, exemptCfg, inv, stdout)
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
// 打ち切る）。ただし runner が check.ScopedOnly を実装している場合、全体免除は一切
// 受け付けない。その場合は全体免除を無効な物として扱い（wholeExempt=false のまま
// exemptions は返す。スコープ付き免除は scopedExemptionsFrom がそのまま使う）、
// error にはせず案内だけを表示する（過去に push 済みのコミットのトレーラが原因で、
// その範囲の検査がずっと失敗し続けるのを避けるため）。GranularityWorktree は
// 免除トレーラの仕組み自体を持たないため、exemptions は空のまま返す。
func resolveExemptions(key string, runner check.Runner, granularity check.Granularity, exemptCfg exempt.Config, inv invocation, stdout io.Writer) ([]exempt.Exemption, bool, error) {
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

	if _, ok := runner.(check.ScopedOnly); ok {
		fmt.Fprintf(stdout, "%s の免除には対象の指定（skip[対象]）が要ります\n", key)
		return exemptions, false, nil
	}

	for _, reason := range reasons {
		fmt.Fprintf(stdout, "%s: 免除されました（%s: skip %s）\n", key, exemptCfg.Trailer, reason)
	}
	return exemptions, true, nil
}

// selectKeys は通常の起動経路（runCheckKey）で走らせるキーを選ぶ。config-guard 型は
// ここでは選ばない（比較元と終点の和で起動を決める必要があり、通常の 1 対 1 の
// キー選択とは意味が違うため。runConfigGuard が別に扱う）。only を指定していて、
// それが cfg.Checks に無い、または config-guard 型の場合は空スライスを返す
// （見つからないエラーの判定は runCheck/runCheckPrePush 側が runConfigGuard の結果と
// 合わせて行う。only がそのコミット範囲の比較元・終点にだけ存在する config-guard の
// キーである可能性があるため、ここではまだエラーにできない）。
func selectKeys(cfg *config.Config, only string) []string {
	if only != "" {
		if cc, ok := cfg.Checks[only]; ok && cc.Type != config.TypeConfigGuard {
			return []string{only}
		}
		return nil
	}

	keys := make([]string, 0, len(cfg.Checks))
	for k, cc := range cfg.Checks {
		if cc.Type == config.TypeConfigGuard {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// configGuardKey は cfg.Checks の中に config-guard 型のキーがあれば返す。
// config.Load が checks に置ける config-guard 型を 1 つまでに制限しているため、
// 複数見つかることは無い。
func configGuardKey(cfg *config.Config) (string, bool) {
	for k, cc := range cfg.Checks {
		if cc.Type == config.TypeConfigGuard {
			return k, true
		}
	}
	return "", false
}

// repoRelativeConfigPath は --config に指定されたパスを、check.EndpointReader が要求する
// リポジトリルート相対のスラッシュ区切りパスに変換する。リポジトリの外を指していれば
// エラーを返す。
//
// 相対パスと絶対パスで経路を分けているのは、比較の基準が違うため。相対パスは
// カレントディレクトリのリポジトリ内での位置（git の prefix）さえ分かれば
// ファイルシステムに触れずに判定できる一方、絶対パスは TopLevel（git rev-parse
// --show-toplevel）と実際に突き合わせる必要があり、両者が symlink 越しに同じ場所を
// 指していても文字列としては食い違う（macOS の一時ディレクトリが /var → /private/var
// を経由する、Windows の 8.3 短縮名が長い名前と食い違う、など）ため symlink 解決を挟む。
func repoRelativeConfigPath(repo *gitutil.Repo, configPath string) (string, error) {
	if filepath.IsAbs(configPath) {
		return repoRelativeConfigPathFromAbs(repo, configPath)
	}
	return repoRelativeConfigPathFromRelative(repo, configPath)
}

// repoRelativeConfigPathFromRelative は相対パスの --config を、git の prefix（カレント
// ディレクトリのリポジトリルートからの相対パス）と組み合わせて解決する。ファイルシステムには
// 触れないため、TopLevel の symlink 解決の有無やパスの表記（8.3 短縮名など）に依存しない。
func repoRelativeConfigPathFromRelative(repo *gitutil.Repo, configPath string) (string, error) {
	prefix, err := repo.Prefix()
	if err != nil {
		return "", fmt.Errorf("check: リポジトリ内の現在位置の解決に失敗しました: %w", err)
	}

	cleaned := filepath.ToSlash(filepath.Clean(configPath))
	rel := path.Clean(prefix + cleaned)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("check: --config（%s）がリポジトリの外を指しています", configPath)
	}
	return rel, nil
}

// repoRelativeConfigPathFromAbs は絶対パスの --config を、TopLevel（git rev-parse
// --show-toplevel）と突き合わせて解決する。両者を filepath.EvalSymlinks で解決してから
// 比較することで、symlink 越しに同じ場所を指しているのに文字列表記が食い違うケース
// （macOS の /var → /private/var、Windows の 8.3 短縮名）を吸収する。
func repoRelativeConfigPathFromAbs(repo *gitutil.Repo, configPath string) (string, error) {
	top, err := repo.TopLevel()
	if err != nil {
		return "", fmt.Errorf("check: リポジトリのルートの解決に失敗しました: %w", err)
	}
	topResolved, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", fmt.Errorf("check: リポジトリのルート %s のシンボリックリンク解決に失敗しました: %w", top, err)
	}

	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("check: %s の絶対パスへの変換に失敗しました: %w", configPath, err)
	}
	// 設定ファイル自体はまだ無いこともあるので、親ディレクトリを解決してから名前を付け直す。
	absResolved := abs
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		absResolved = filepath.Join(dir, filepath.Base(abs))
	}

	rel, err := filepath.Rel(topResolved, absResolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("check: --config（%s）がリポジトリの外を指しています", configPath)
	}
	return filepath.ToSlash(rel), nil
}

// runConfigGuard は config-guard を、比較元・終点それぞれの checks から見つかる
// config-guard のキー（比較元 → 実行時の設定 → 終点の優先順、いずれにも無ければ
// 起動しない）の和で起動する。squashed 粒度の invocation は planInvocations を
// そのまま再利用し、比較の両端の読み込みと免除設定の解決だけをここで行う。
// 戻り値は (違反を報告したか, この呼び出しで実際に 1 回以上走ったか)。
func runConfigGuard(cfg *config.Config, repo *gitutil.Repo, configPath string, rangeExprs []string, messageFile, only string, stdout, stderr io.Writer) (failed, ran bool, err error) {
	if only != "" {
		if cc, ok := cfg.Checks[only]; ok && cc.Type != config.TypeConfigGuard {
			// only は通常の検査を指している。config-guard の出番は無い。
			return false, false, nil
		}
	}

	runtimeKey, runtimeHasGuard := configGuardKey(cfg)

	relConfigPath, pathErr := repoRelativeConfigPath(repo, configPath)
	if pathErr != nil {
		if runtimeHasGuard {
			return false, false, pathErr
		}
		// 実行時の設定に config-guard が無ければ、比較元・終点を読めなくても
		// 起動しないだけで済ませる（--config がリポジトリの外を指す運用自体は
		// config-guard 以外の検査には影響しない）。
		return false, false, nil
	}

	invocations, err := planInvocations(repo, check.GranularitySquashed, rangeExprs, messageFile)
	if err != nil {
		return false, false, err
	}

	for _, inv := range invocations {
		key, exemptCfg, targets, run, err := resolveConfigGuardInvocation(inv, relConfigPath, runtimeKey, only)
		if err != nil {
			return false, false, fmt.Errorf("check: %w", err)
		}
		if !run {
			continue
		}
		ran = true

		inv.ctx.ConfigPath = relConfigPath
		runner := configguard.New(targets)
		invFailed, err := runInvocation(key, runner, check.GranularitySquashed, exemptCfg, inv, stdout, stderr)
		if err != nil {
			return false, ran, err
		}
		if invFailed {
			failed = true
		}
	}
	return failed, ran, nil
}

// resolveConfigGuardInvocation は 1 回の起動について、config-guard のキー（比較元 →
// 実行時の設定 → 終点の優先順）・免除設定・スコープ付き免除で指定できる対象の一覧を決める。
// 免除設定は比較元の設定から解決する（終点から読むと、緩めるコミット自身が免除設定も
// 緩めて自分を免除できてしまう）。比較元が無い・YAML として解析できない場合はシステム
// 既定にフォールバックする。キーが 1 つも見つからない、または only を指定していてそれと
// 一致しない場合は run=false を返す（呼び出し側はこの起動を静かにスキップする）。
func resolveConfigGuardInvocation(inv invocation, configPath, runtimeKey, only string) (key string, exemptCfg exempt.Config, targets []string, run bool, err error) {
	reader, ok := inv.ctx.Source.(check.EndpointReader)
	if !ok {
		return "", exempt.Config{}, nil, false, fmt.Errorf("config-guard: この比較は比較元・終点のファイルを読めません")
	}

	baseData, baseOK, err := reader.BaseFile(configPath)
	if err != nil {
		return "", exempt.Config{}, nil, false, fmt.Errorf("config-guard: 比較元の %s の読み込みに失敗しました: %w", configPath, err)
	}
	targetData, targetOK, err := reader.TargetFile(configPath)
	if err != nil {
		return "", exempt.Config{}, nil, false, fmt.Errorf("config-guard: 終点の %s の読み込みに失敗しました: %w", configPath, err)
	}

	var baseSnap *configdiff.Snapshot
	if baseOK {
		if snap, perr := configdiff.Parse(baseData); perr == nil {
			baseSnap = snap
		}
	}
	baseKey := ""
	if baseSnap != nil {
		if keys := baseSnap.CheckKeysOfType(config.TypeConfigGuard); len(keys) > 0 {
			baseKey = keys[0]
		}
	}

	var targetSnap *configdiff.Snapshot
	if targetOK {
		if snap, perr := configdiff.Parse(targetData); perr == nil {
			targetSnap = snap
		}
	}
	targetKey := ""
	if targetSnap != nil {
		if keys := targetSnap.CheckKeysOfType(config.TypeConfigGuard); len(keys) > 0 {
			targetKey = keys[0]
		}
	}

	key = firstNonEmpty(baseKey, runtimeKey, targetKey)
	if key == "" || (only != "" && key != only) {
		return "", exempt.Config{}, nil, false, nil
	}
	targets = configdiff.ExemptTargets(baseSnap, targetSnap, configPath)

	if baseSnap != nil {
		enable, trailer := baseSnap.ResolveExempt(key)
		return key, exempt.Config{Enable: enable, Trailer: trailer}, targets, true, nil
	}
	// 比較元が無い、または YAML として解析できない場合はシステム既定にフォールバックする
	// （config.Config のゼロ値には types/checks の上書きが無いため、
	// ResolveExempt がそのままシステム既定を返す）。
	enable, trailer := (&config.Config{}).ResolveExempt(key, config.CheckConfig{Type: config.TypeConfigGuard})
	return key, exempt.Config{Enable: enable, Trailer: trailer}, targets, true, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
	case config.TypeConfigGuard:
		return configguard.New(nil), nil
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
