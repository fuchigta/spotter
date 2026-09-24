// Package prepush は pre-push フックの標準入力から、push しようとしている ref ごとに
// range モードで検査すべき範囲を決める。git を直接呼ばず、必要な問い合わせは Deps として
// 注入することで、決定ロジック自体を純粋関数としてテストできるようにする。
package prepush

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Update は pre-push フックの標準入力 1 行分
// （"<local ref> <local sha> <remote ref> <remote sha>"）を表す。
type Update struct {
	LocalRef  string
	LocalSHA  string
	RemoteRef string
	RemoteSHA string
}

// Parse は pre-push フックの標準入力を読み、Update の一覧を返す。git は行末を LF で書くが、
// 利用者の環境（Windows の一部シェル経由など）で CRLF になることがあるため吸収する。
func Parse(r io.Reader) ([]Update, error) {
	var updates []Update
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("prepush: pre-push の標準入力の行を解釈できません: %q", line)
		}
		updates = append(updates, Update{
			LocalRef:  fields[0],
			LocalSHA:  fields[1],
			RemoteRef: fields[2],
			RemoteSHA: fields[3],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("prepush: 標準入力の読み込みに失敗しました: %w", err)
	}
	return updates, nil
}

// isZeroSHA は sha が全 0（削除 push、または比較対象の sha がまだ無いことを表す）かどうかを
// 判定する。SHA-256 リポジトリでは桁数が SHA-1 と異なるため、長さを決め打ちしない。
func isZeroSHA(sha string) bool {
	return strings.Trim(sha, "0") == ""
}

// Deps は範囲決定に必要な git への問い合わせ。テストでは fake を注入する。
type Deps struct {
	// ResolveCommit は ref を `^{commit}` に peel する（gitutil.Repo.ResolveCommit 相当）。
	ResolveCommit func(ref string) (sha string, ok bool, err error)
	// CommitExists は sha がローカルにコミットとして実在するかどうかを返す
	// （gitutil.Repo.CommitExists 相当）。
	CommitExists func(sha string) (bool, error)
	// Head は現在の HEAD が指すコミットの SHA を返す（gitutil.Repo.HeadCommit 相当）。
	Head func() (sha string, err error)
}

// Plan は 1 件の Update を検査するかどうかと、検査する場合の range 式を表す。
type Plan struct {
	Update Update
	// Checked が false のとき、この ref は検査対象外（不合格ではない）。
	// SkipReason はその理由で、利用者への案内にそのまま使える。
	Checked    bool
	SkipReason string
	// RangeExpr は Checked のときの範囲式（"<local> --not --remotes [<remote sha>]"）。
	// gitutil.Repo.RevListNoMerges / RangeMessages がそのまま strings.Fields で
	// git に渡せる形。
	RangeExpr string
}

// PlanRef は 1 件の Update について、検査するかどうかと範囲式を決める。
//
//   - local sha が全 0（削除 push）なら検査しない
//   - local sha が commit に peel できない（tree だけを指す tag など）なら検査しない
//   - peel した local が現在の HEAD と異なるなら検査しない（設定・worktree 粒度の検査や
//     command 型のスクリプトは作業ツリー由来で、HEAD 以外の状態では再現できないため）
//   - 範囲式は "<local> --not --remotes" を基本とし、remote sha が全 0 でなくローカルに
//     実在するとき（＝まだ公開されていないコミット）だけ追加の除外として付け足す
func PlanRef(u Update, deps Deps) (Plan, error) {
	if isZeroSHA(u.LocalSHA) {
		return Plan{
			Update:     u,
			SkipReason: fmt.Sprintf("%s は削除 push のため検査しません", u.LocalRef),
		}, nil
	}

	local, ok, err := deps.ResolveCommit(u.LocalSHA)
	if err != nil {
		return Plan{}, fmt.Errorf("prepush: %s (%s) の解決に失敗しました: %w", u.LocalRef, u.LocalSHA, err)
	}
	if !ok {
		return Plan{
			Update:     u,
			SkipReason: fmt.Sprintf("%s はコミットを指していないため検査しません", u.LocalRef),
		}, nil
	}

	head, err := deps.Head()
	if err != nil {
		return Plan{}, fmt.Errorf("prepush: HEAD の解決に失敗しました: %w", err)
	}
	if local != head {
		return Plan{
			Update:     u,
			SkipReason: fmt.Sprintf("%s は HEAD ではないため検査しません（CI で検査されます）", u.LocalRef),
		}, nil
	}

	args := []string{local, "--not", "--remotes"}
	if !isZeroSHA(u.RemoteSHA) {
		exists, err := deps.CommitExists(u.RemoteSHA)
		if err != nil {
			return Plan{}, fmt.Errorf("prepush: %s の確認に失敗しました: %w", u.RemoteSHA, err)
		}
		if exists {
			args = append(args, u.RemoteSHA)
		}
	}

	return Plan{
		Update:    u,
		Checked:   true,
		RangeExpr: strings.Join(args, " "),
	}, nil
}

// PlanAll は updates すべてについて PlanRef を呼び、ref ごとの Plan を一覧で返す
// （updates と 1 対 1。範囲式の重複除去は UniqueRangeExprs で別に行う）。
func PlanAll(updates []Update, deps Deps) ([]Plan, error) {
	plans := make([]Plan, 0, len(updates))
	for _, u := range updates {
		p, err := PlanRef(u, deps)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// UniqueRangeExprs は plans のうち Checked な範囲式を、最初に出現した順で重複無く返す
// （squashed push でも複数 ref が同じコミットを指すことがあるため、同じ範囲式の検査を
// 二重に走らせない）。
func UniqueRangeExprs(plans []Plan) []string {
	var exprs []string
	seen := make(map[string]bool)
	for _, p := range plans {
		if !p.Checked || seen[p.RangeExpr] {
			continue
		}
		seen[p.RangeExpr] = true
		exprs = append(exprs, p.RangeExpr)
	}
	return exprs
}
