package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/gitutil"
	"github.com/fuchigta/spotter/internal/prepush"
)

// runCheckPrePush は spotter check --pre-push <remote> の実装。pre-push フックの標準入力
// （ref ごとの "<local ref> <local sha> <remote ref> <remote sha>"）から push しようとしている
// 内容を読み、ref ごとに CI（spotter range + --range）と同じ range 検査を push する前に
// 走らせる。remote は失敗時の案内にだけ使い、範囲の計算には使わない（upstream の推測はしない）。
func runCheckPrePush(stdin io.Reader, stdout, stderr io.Writer, configPath, remote, only string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := checkRequiredVersion(cfg); err != nil {
		return fmt.Errorf("check: %w", err)
	}

	keys := selectKeys(cfg, only)

	updates, err := prepush.Parse(stdin)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	repo := gitutil.New(repoRoot)
	plans, err := prepush.PlanAll(updates, prePushDeps(repo))
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	for _, p := range plans {
		if !p.Checked {
			fmt.Fprintln(stderr, p.SkipReason)
		}
	}

	rangeExprs := prepush.UniqueRangeExprs(plans)
	if len(rangeExprs) == 0 {
		// 範囲が無いまま runCheckKey に渡すと staged モードとして扱われ、push と無関係な
		// インデックスを検査してしまう。worktree 粒度も、再現する CI の実行が無いので走らせない。
		return nil
	}

	failed := false
	for _, key := range keys {
		keyFailed, err := runCheckKey(cfg, key, repo, rangeExprs, "", stdout, stderr)
		if err != nil {
			return err
		}
		if keyFailed {
			failed = true
		}
	}

	guardFailed, guardRan, err := runConfigGuard(cfg, repo, configPath, rangeExprs, "", only, stdout, stderr)
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
		if err := printPrePushFailureNotice(stdout, repo, configPath, remote, plans); err != nil {
			return fmt.Errorf("check: %w", err)
		}
		return ErrCheckFailed
	}
	return nil
}

// prePushDeps は repo（実 git）を prepush.Deps に適合させる。
func prePushDeps(repo *gitutil.Repo) prepush.Deps {
	return prepush.Deps{
		ResolveCommit: repo.ResolveCommit,
		CommitExists:  repo.CommitExists,
		Head:          repo.HeadCommit,
	}
}

// prePushRangeGroup は同じ範囲式で検査した ref をまとめたもの（squashed push で複数 ref が
// 同じコミットを指す場合、UniqueRangeExprs 側では 1 回にまとまるため、失敗時の表示では
// 逆にどの ref のための検査だったかを示せるようにまとめ直す）。
type prePushRangeGroup struct {
	rangeExpr string
	refs      []string
}

// groupPlansByRangeExpr は plans のうち検査した（Checked な）ものだけを、範囲式ごとに
// 最初に出現した順でまとめる。
func groupPlansByRangeExpr(plans []prepush.Plan) []prePushRangeGroup {
	var groups []prePushRangeGroup
	index := make(map[string]int)
	for _, p := range plans {
		if !p.Checked {
			continue
		}
		i, ok := index[p.RangeExpr]
		if !ok {
			index[p.RangeExpr] = len(groups)
			groups = append(groups, prePushRangeGroup{rangeExpr: p.RangeExpr})
			i = len(groups) - 1
		}
		groups[i].refs = append(groups[i].refs, p.Update.LocalRef)
	}
	return groups
}

// printPrePushFailureNotice は range 検査に失敗したとき、違反表示（各検査が既に出している）に
// 続けて、CI でも同じ結果になること・push しようとした ref とその範囲式・その範囲で設定ファイル
// を変更したコミットの一覧（検査を足したコミットを特定しやすくするため）・対処の案内を出す。
func printPrePushFailureNotice(w io.Writer, repo *gitutil.Repo, configPath, remote string, plans []prepush.Plan) error {
	fmt.Fprintf(w, "%s への push 前の range 検査に失敗しました。CI（spotter range + --range）でも同じ結果になります。\n", remote)
	for _, g := range groupPlansByRangeExpr(plans) {
		fmt.Fprintf(w, "  - %s（範囲: %s）\n", strings.Join(g.refs, ", "), g.rangeExpr)

		changes, err := repo.ConfigChangeLog(g.rangeExpr, configPath)
		if err != nil {
			return err
		}
		if len(changes) > 0 {
			fmt.Fprintf(w, "    この範囲で %s を変更したコミット:\n", configPath)
			for _, c := range changes {
				fmt.Fprintf(w, "      %s\n", c)
			}
		}
	}
	fmt.Fprintln(w, "対処は docs/ci-integration.md の「対処」を参照してください。")
	return nil
}
