// Package confighygiene は .spotter.yml が現在のリポジトリの実情と噛み合っているかを
// 検証する（spotter config lint）。config.Load が見る「構文として正しいか」とは別に、
// 「今のワークツリーに対して意味を持つか」を見る。
//
// ここでの判定は静的な走査であり、doublestar パターンの意味（doc-sync なら「対応する
// コード側」、diff-content なら「対象を絞るファイル」）までは区別しない。全て
// 「現在のワークツリーに 1 件も一致しない」という同じ形の問題として報告する。ただし
// フィールドごとに「一致しないことが異常かどうか」の性質は異なるため、対象にする
// フィールドは個別に選んでいる（各 case のコメントを参照）。
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
// docutil.ExistsOrGlob を .spotter.yml のパターン検証に転用しない理由:
//   - ExistsOrGlob は "*" を含むかどうかで Glob/Stat を切り替えるため、
//     "{Makefile,Dockerfile}" のような "*" を含まない doublestar 構文
//     （brace/bracket/"?"）を Glob に回さず、実在しても誤って「無い」と
//     判定する
//   - 常に doublestar.Glob を使えば、"*" を含まないリテラルパスも含めて
//     doublestar の構文全体を正しく解釈できる
//   - 不正な構文（ValidatePattern が弾くもの）と「実在しない」は原因が違うため
//     別のメッセージにする
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

func lintCheck(key string, cc config.CheckConfig, fsys fs.FS) []Finding {
	var findings []Finding

	report := func(field, message string) {
		findings = append(findings, Finding{Check: key, Field: field, Message: message})
	}

	globPattern := func(field, pattern string) {
		if pattern == "" {
			return
		}
		matched, invalid := patternMatches(fsys, pattern)
		switch {
		case invalid:
			report(field, fmt.Sprintf("%q は不正な doublestar パターンです", pattern))
		case !matched:
			report(field, fmt.Sprintf("%q に一致するファイルが現在のワークツリーにありません", pattern))
		}
	}
	fileRef := func(field, path string) {
		if path == "" {
			return
		}
		info, err := fs.Stat(fsys, path)
		if err != nil {
			report(field, fmt.Sprintf("%q が存在しません", path))
			return
		}
		if info.IsDir() {
			report(field, fmt.Sprintf("%q はディレクトリです（ファイルを指定してください）", path))
		}
	}
	dirRef := func(field, path string) {
		if path == "" {
			return
		}
		info, err := fs.Stat(fsys, path)
		if err != nil || !info.IsDir() {
			report(field, fmt.Sprintf("%q というディレクトリがありません", path))
		}
	}

	switch cc.Type {
	case config.TypeDocSync:
		// pairs[].paths は「対応するはずのコード側」、pairs[].doc は「対応する
		// ドキュメント」。どちらも実在すべきものなので対象にする。
		for i, p := range cc.Pairs {
			globPattern(fmt.Sprintf("pairs[%d].paths", i), p.Paths)
			fileRef(fmt.Sprintf("pairs[%d].doc", i), p.Doc)
		}
	case config.TypeCompanionFiles:
		// companions[].paths は doc-sync.pairs[].paths と同じ性質（対応する
		// はずの実在物）。
		for i, c := range cc.Companions {
			globPattern(fmt.Sprintf("companions[%d].paths", i), c.Paths)
		}
	case config.TypeConsistency:
		// sources[].file は「突き合わせ元のファイル」なので実在すべき。
		for i, s := range cc.Sources {
			fileRef(fmt.Sprintf("sources[%d].file", i), s.File)
		}
	case config.TypeDocPaths:
		// docs は対象ドキュメントの一覧。"./README.md" のような doublestar 上
		// 一致しない書き方をすると「黙って対象から外れる」（docs/checks/doc-paths.md
		// に明記された既知の落とし穴）ため、検査が静かに無力化される代表例。
		// path_prefixes はディレクトリ接頭辞の一覧で、リネームで無くなった
		// ディレクトリを書いたままだと候補が減って検査が弱まる。
		for i, d := range cc.Docs {
			globPattern(fmt.Sprintf("docs[%d]", i), d)
		}
		for i, p := range cc.PathPrefixes {
			dirRef(fmt.Sprintf("path_prefixes[%d]", i), p)
		}
	case config.TypeDocLinks:
		for i, d := range cc.Docs {
			globPattern(fmt.Sprintf("docs[%d]", i), d)
		}
	// unwanted-files.deny[].paths と diff-content.deny[].paths は意図的に対象外。
	// unwanted-files.deny は「禁止パターン」で、現在のワークツリーに一致する
	// ファイルが無いことこそが正常な状態（存在したら検査自体がそれを違反として
	// 拾う）。diff-content.deny[].paths は対象を絞り込むスコープで、一致 0 件は
	// 「そのルールが不活性」を意味し意味的には陳腐化の一種だが、unwanted-files
	// と同様「まだ発生していない違反」を先回りして書く運用もあるため、要素単位の
	// 機械判定は誤検知が多いと判断し対象外にしている。
	case config.TypeCommitIntent:
		for i, r := range cc.Rules {
			// require は OR 集合（「変更ファイルの少なくとも1つがいずれかに
			// 一致すべき」、docs/checks/commit-intent.md）。要素単位で判定すると、
			// 複数言語のレシピをまとめて書いている構成（このリポジトリの
			// docs/checks/commit-intent.md のレシピ自体がそう）で、まだ使って
			// いない言語向けの要素を誤って陳腐化と報告してしまう。ルール全体
			// （全要素が空振り）のときだけ 1 件報告する。
			if len(r.Require) > 0 && !anyPatternMatches(fsys, r.Require) {
				report(fmt.Sprintf("rules[%d].require", i), fmt.Sprintf("%v のどの要素にも一致するファイルが現在のワークツリーにありません", r.Require))
			}
			// allow は「このルールが変更を許すパス」という将来のコミットへの
			// 制約で、unwanted-files.deny と同じく「今のワークツリーに実在物が
			// 無い」ことが異常とは限らない（対応するコミットがまだ発生して
			// いないだけ）ため対象外にしている。
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
