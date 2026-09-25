// Package configguard は .spotter.yml 自体の変更が検査を緩めていないかを検査する
// config-guard の判定を実装する。実際の判定は internal/configdiff の純粋関数に委ね、
// ここでは比較の両端（check.EndpointReader）を読んで Violation に変換するだけを担う。
package configguard

import (
	"fmt"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/configdiff"
)

// Check は config-guard 検査のインスタンス。checks.<key> 固有の設定を持たない
// （比較する対象は ctx.ConfigPath と ctx.Source から決まるため）。
type Check struct {
	// targets はスコープ付き免除で指定できる対象の一覧（ExemptTargets が返す値）。
	// ExemptTargets() は引数を取れないため、起動ごとに比較元・終点の設定から
	// configdiff.ExemptTargets で組み立てて CLI が渡す。
	targets []string
}

// New は targets（スコープ付き免除で指定できる対象の一覧）を持つ Check を返す。
// 検証すべき設定が無いため常に成功する。
func New(targets []string) *Check {
	return &Check{targets: targets}
}

// Granularity は範囲全体を比較元・終点の 2 端点だけで 1 回見る（配列の要素と同じく、
// 範囲の途中経過は見ない）。
func (c *Check) Granularity() check.Granularity {
	return check.GranularitySquashed
}

// Run は ctx.ConfigPath が指す設定ファイルを比較元・終点それぞれで読み、
// configdiff.Diff で緩和を判定する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	reader, ok := ctx.Source.(check.EndpointReader)
	if !ok {
		return nil, fmt.Errorf("configguard: この比較は比較元・終点のファイルを読めません（check.EndpointReader 非対応）")
	}
	if ctx.ConfigPath == "" {
		return nil, fmt.Errorf("configguard: ConfigPath が指定されていません")
	}

	base, baseOK, err := reader.BaseFile(ctx.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("configguard: 比較元の %s の読み込みに失敗しました: %w", ctx.ConfigPath, err)
	}
	if !baseOK {
		// 比較元に設定ファイルが無い（spotter をこれから導入する、または --config を
		// 変えた直後）場合、比較する緩和が無いので合格にする。
		return nil, nil
	}

	target, targetOK, err := reader.TargetFile(ctx.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("configguard: 終点の %s の読み込みに失敗しました: %w", ctx.ConfigPath, err)
	}
	if !targetOK {
		// 終点で設定ファイルごと消えている場合、個々の checks.<key> を突き合わせる意味が
		// 無い（全ての検査が同時に無くなる、あり得る中で最大の緩和）。設定ファイル全体の
		// 緩和なので対象は ctx.ConfigPath 自身にする。
		return []check.Violation{{
			Summary: fmt.Sprintf("%s が削除されました（全ての検査が無くなります）", ctx.ConfigPath),
			Target:  ctx.ConfigPath,
		}}, nil
	}

	loosenings := configdiff.Diff(base, target)
	violations := make([]check.Violation, len(loosenings))
	for i, l := range loosenings {
		violations[i] = check.Violation{Summary: l.String(), Target: l.Target(ctx.ConfigPath)}
	}
	return violations, nil
}

// ExemptTargets はスコープ付き免除（例: "Config-Guard: skip[checks.diff-size] 理由"）で
// 指定できる対象の一覧を返す。check.ScopedExemptable の実装。
func (c *Check) ExemptTargets() []string {
	return c.targets
}

// RequireScopedExemption は check.ScopedOnly の実装で、config-guard が対象を絞らない
// 全体免除を受け付けないことを表す（docs/exemptions.md 参照）。設定では変えられない
// 固定の挙動なので、値を持たない目印として実装する。
func (c *Check) RequireScopedExemption() {}
