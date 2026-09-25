// Package confighygiene は .spotter.yml が現在のリポジトリの実情と噛み合っているかを
// 検証する（spotter config lint）。config.Load の構文検証とは別に、静的な走査で
// 「今のワークツリーに対して意味を持つか」を見る。フィールドごとに一致しないことが
// 異常かどうかは違うため、対象フィールドは個別に選ぶ（各 case のコメント参照）。
package confighygiene

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fuchigta/spotter/internal/config"
)

// Finding は 1 件の検出結果。
type Finding struct {
	// Check は checks.<key> のキー名。types 由来の Finding では "types" になる。
	Check string `json:"check"`
	// Field は Check 内でのフィールドパス（例: "pairs[0].paths"）。
	Field string `json:"field"`
	// Message は人間向けの説明。
	Message string `json:"message"`
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

// patternMatches は pattern が現在のワークツリーに実在する「非ディレクトリの
// ファイル」に1件以上一致するかを判定する。".git" 配下は無視する
// （internal/check/docutil.ResolveDocs と同じ扱い。ワークツリー直下の .git は
// 検査本体が対象にすることが無いため、含めると誤って「生きている」と判定する）。
//
// "{Makefile,Dockerfile}" のような "*" を含まない doublestar 構文（brace/bracket/"?"）
// も含めて構文全体を正しく解釈するため、常に doublestar.Glob を使う。不正な構文
// （ValidatePattern が弾くもの）と「実在しない」は原因が違うため別のメッセージにする。
func patternMatches(fsys fs.FS, pattern string) (matched, invalidSyntax bool) {
	if !doublestar.ValidatePattern(pattern) {
		return false, true
	}
	matches, err := doublestar.Glob(fsys, pattern)
	if err != nil {
		return false, true
	}
	for _, m := range matches {
		if m == ".git" || strings.HasPrefix(m, ".git/") {
			continue
		}
		info, err := fs.Stat(fsys, m)
		if err != nil || info.IsDir() {
			continue
		}
		return true, false
	}
	return false, false
}

// anyPatternMatches は patterns のうち少なくとも 1 つが patternMatches を満たすか
// どうかを返す。commit-intent.rules[].require のような OR 集合の判定に使う。
func anyPatternMatches(fsys fs.FS, patterns []string) bool {
	for _, p := range patterns {
		if matched, invalid := patternMatches(fsys, p); matched && !invalid {
			return true
		}
	}
	return false
}

// checkLinter は checks.<key> 1 つ分の Finding を集める状態。lintCheck の switch の
// 各 case（フィールドの種類ごとの走査）を個別のメソッドに分けるために、report/
// globPattern/fileRef/dirRef をメソッドとして束ねている。
type checkLinter struct {
	key      string
	fsys     fs.FS
	findings []Finding
}

func (l *checkLinter) report(field, message string) {
	l.findings = append(l.findings, Finding{Check: l.key, Field: field, Message: message})
}

func (l *checkLinter) globPattern(field, pattern string) {
	if pattern == "" {
		return
	}
	matched, invalid := patternMatches(l.fsys, pattern)
	switch {
	case invalid:
		l.report(field, fmt.Sprintf("%q は不正な doublestar パターンです", pattern))
	case !matched:
		l.report(field, fmt.Sprintf("%q に一致するファイルが現在のワークツリーにありません", pattern))
	}
}

func (l *checkLinter) fileRef(field, path string) {
	if path == "" {
		return
	}
	info, err := fs.Stat(l.fsys, path)
	if err != nil {
		l.report(field, fmt.Sprintf("%q が存在しません", path))
		return
	}
	if info.IsDir() {
		l.report(field, fmt.Sprintf("%q はディレクトリです（ファイルを指定してください）", path))
	}
}

func (l *checkLinter) dirRef(field, path string) {
	if path == "" {
		return
	}
	info, err := fs.Stat(l.fsys, path)
	if err != nil || !info.IsDir() {
		l.report(field, fmt.Sprintf("%q というディレクトリがありません", path))
	}
}

// lintDocSyncPairs は pairs[].paths（対応するはずのコード側）と pairs[].doc
// （対応するドキュメント）を検証する。どちらも実在すべきものなので対象にする。
func (l *checkLinter) lintDocSyncPairs(pairs []config.DocSyncPair) {
	for i, p := range pairs {
		l.globPattern(fmt.Sprintf("pairs[%d].paths", i), p.Paths)
		l.fileRef(fmt.Sprintf("pairs[%d].doc", i), p.Doc)
	}
}

// lintCompanions は companions[].paths を検証する。doc-sync.pairs[].paths と
// 同じ性質（対応するはずの実在物）。
func (l *checkLinter) lintCompanions(companions []config.CompanionRule) {
	for i, c := range companions {
		l.globPattern(fmt.Sprintf("companions[%d].paths", i), c.Paths)
	}
}

// lintConsistencySources は sources[].file（突き合わせ元のファイルなので実在すべき）と
// sources[].glob（集合そのもの。一致 0 件は doc-paths.docs や doc-links.docs の glob
// フィールドと同じ意味で陳腐化。記法変更やディレクトリのリネームに追従できていない）を
// 検証する。
func (l *checkLinter) lintConsistencySources(sources []config.ConsistencySource) {
	for i, s := range sources {
		l.fileRef(fmt.Sprintf("sources[%d].file", i), s.File)
		l.globPattern(fmt.Sprintf("sources[%d].glob", i), s.Glob)
	}
}

// lintDocPaths は docs（"./README.md" のような書き方だと doublestar 上一致せず黙って
// 対象から外れる。docs/checks/doc-paths.md に明記された既知の落とし穴）と
// path_prefixes（リネームで無くなったディレクトリのままだと候補が減り検査が弱まる）を
// 検証する。
func (l *checkLinter) lintDocPaths(docs, pathPrefixes []string) {
	for i, d := range docs {
		l.globPattern(fmt.Sprintf("docs[%d]", i), d)
	}
	for i, p := range pathPrefixes {
		l.dirRef(fmt.Sprintf("path_prefixes[%d]", i), p)
	}
}

func (l *checkLinter) lintDocLinksDocs(docs []string) {
	for i, d := range docs {
		l.globPattern(fmt.Sprintf("docs[%d]", i), d)
	}
}

// lintCommitIntentRules は rules[].require（OR 集合）を検証する。複数言語のレシピを
// まとめて書く構成（docs/checks/commit-intent.md のレシピ自体がそう）では要素単位の
// 判定だと未使用言語向けの要素を誤って陳腐化と報告するため、ルール全体（全要素が
// 空振り）のときだけ 1 件報告する。
//
// allow/deny は対象外: どちらも「このルールが許す/禁止するパス」という制約で、
// 一致するファイルが今のワークツリーに無いことが異常とは限らない（allow は対応する
// コミットが未発生なだけ、deny はむしろ一致しないことこそ正常）。
func (l *checkLinter) lintCommitIntentRules(rules []config.CommitIntentRule) {
	for i, r := range rules {
		if len(r.Require) > 0 && !anyPatternMatches(l.fsys, r.Require) {
			l.report(fmt.Sprintf("rules[%d].require", i), fmt.Sprintf("%v のどの要素にも一致するファイルが現在のワークツリーにありません", r.Require))
		}
	}
}

func lintCheck(key string, cc config.CheckConfig, fsys fs.FS) []Finding {
	l := &checkLinter{key: key, fsys: fsys}

	switch cc.Type {
	case config.TypeDocSync:
		l.lintDocSyncPairs(cc.Pairs)
	case config.TypeCompanionFiles:
		l.lintCompanions(cc.Companions)
	case config.TypeConsistency:
		l.lintConsistencySources(cc.Sources)
	case config.TypeDocPaths:
		l.lintDocPaths(cc.Docs, cc.PathPrefixes)
	case config.TypeDocLinks:
		l.lintDocLinksDocs(cc.Docs)
	// unwanted-files.deny[].paths と diff-content.deny[].paths は意図的に対象外。
	// unwanted-files.deny は「禁止パターン」で、現在のワークツリーに一致する
	// ファイルが無いことこそが正常な状態（存在したら検査自体がそれを違反として
	// 拾う）。diff-content.deny[].paths は対象を絞り込むスコープで、一致 0 件は
	// 「そのルールが不活性」を意味し意味的には陳腐化の一種だが、unwanted-files
	// と同様「まだ発生していない違反」を先回りして書く運用もあるため、要素単位の
	// 機械判定は誤検知が多いと判断し対象外にしている。
	case config.TypeCommitIntent:
		l.lintCommitIntentRules(cc.Rules)
	}

	return l.findings
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
