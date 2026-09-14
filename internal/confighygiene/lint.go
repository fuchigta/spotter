// Package confighygiene は .spotter.yml が現在のリポジトリの実情と噛み合っているかを
// 検証する（spotter config lint）。config.Load が見る「構文として正しいか」とは別に、
// 「今のワークツリーに対して意味を持つか」を見る。
//
// ここでの判定は静的な走査であり、doublestar パターンの意味（doc-sync なら「対応する
// コード側」、diff-content なら「対象を絞るファイル」）までは区別しない。全て
// 「現在のワークツリーに 1 件も一致しない」という同じ形の問題として報告する。
package confighygiene

import (
	"fmt"
	"io/fs"
	"sort"

	"github.com/fuchigta/spotter/internal/check/docutil"
	"github.com/fuchigta/spotter/internal/config"
)

// Finding は 1 件の検出結果。
type Finding struct {
	// Check は checks.<key> のキー名。types 由来の Finding では "types" になる。
	Check string
	// Field は Check 内でのフィールドパス（例: "pairs[0].paths"）。
	Field string
	// Message は人間向けの説明。
	Message string
}

// Lint は cfg の中の陳腐化した設定を検出する。fsys にはリポジトリルートを渡す
// （呼び出し側が os.DirFS(repoRoot) で用意する）。checks キーの昇順、各 checks 内は
// フィールドの出現順で安定した順序を返す。
func Lint(cfg *config.Config, fsys fs.FS) []Finding {
	var findings []Finding

	keys := make([]string, 0, len(cfg.Checks))
	for k := range cfg.Checks {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		findings = append(findings, lintCheck(key, cfg.Checks[key], fsys)...)
	}

	findings = append(findings, lintUnusedTypes(cfg)...)

	return findings
}

func lintCheck(key string, cc config.CheckConfig, fsys fs.FS) []Finding {
	var findings []Finding

	globPattern := func(field, pattern string) {
		if pattern == "" {
			return
		}
		if !docutil.ExistsOrGlob(fsys, pattern) {
			findings = append(findings, Finding{
				Check: key, Field: field,
				Message: fmt.Sprintf("%q に一致するファイルが現在のワークツリーにありません", pattern),
			})
		}
	}
	fileRef := func(field, path string) {
		if path == "" {
			return
		}
		if _, err := fs.Stat(fsys, path); err != nil {
			findings = append(findings, Finding{
				Check: key, Field: field,
				Message: fmt.Sprintf("%q が存在しません", path),
			})
		}
	}

	switch cc.Type {
	case config.TypeDocSync:
		for i, p := range cc.Pairs {
			globPattern(fmt.Sprintf("pairs[%d].paths", i), p.Paths)
			fileRef(fmt.Sprintf("pairs[%d].doc", i), p.Doc)
		}
	case config.TypeCompanionFiles:
		for i, c := range cc.Companions {
			globPattern(fmt.Sprintf("companions[%d].paths", i), c.Paths)
		}
	case config.TypeConsistency:
		for i, s := range cc.Sources {
			fileRef(fmt.Sprintf("sources[%d].file", i), s.File)
		}
	// unwanted-files.deny[].paths と diff-content.deny[].paths は意図的に対象外。
	// どちらも「禁止パターン」であり、現在のワークツリーに一致するファイルが
	// 無いことこそが正常な状態（存在したら検査自体がそれを違反として拾う）。
	// doc-sync.pairs や companion-files.companions のような「対応するはずの
	// 実在物」とは性質が逆なので、同じ「死んだパターン」判定を適用できない。
	case config.TypeCommitIntent:
		for i, r := range cc.Rules {
			for j, p := range r.Allow {
				globPattern(fmt.Sprintf("rules[%d].allow[%d]", i, j), p)
			}
			for j, p := range r.Require {
				globPattern(fmt.Sprintf("rules[%d].require[%d]", i, j), p)
			}
		}
	}

	return findings
}

// lintUnusedTypes は types.<name> のうち、どの checks.<key>.type からも参照されて
// いないものを検出する（組み込み type の default 上書きも、command 型の登録も対象。
// 登録だけして使っていない設定は、リファクタの過程で checks 側を消し忘れた・
// 差し替えた可能性が高い）。
func lintUnusedTypes(cfg *config.Config) []Finding {
	used := make(map[string]bool, len(cfg.Checks))
	for _, cc := range cfg.Checks {
		used[cc.Type] = true
	}

	names := make([]string, 0, len(cfg.Types))
	for name := range cfg.Types {
		names = append(names, name)
	}
	sort.Strings(names)

	var findings []Finding
	for _, name := range names {
		if !used[name] {
			findings = append(findings, Finding{
				Check: "types", Field: name,
				Message: fmt.Sprintf("types.%s はどの checks からも参照されていません", name),
			})
		}
	}
	return findings
}
