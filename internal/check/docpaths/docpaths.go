// Package docpaths はドキュメントが名指ししているコードのパスが実在するかを調べる検査
// （scripts/check-doc-paths.sh 相当）を実装する。
//
// doc-sync が守るのは「一緒に直したか」だけで、参照先の実在は守れない。この検査は
// git の差分ではなく現在の作業ツリーそのものを見るため、check.GranularityWorktree
// （staged/range を問わず 1 回だけ実行し、免除トレーラも持たない）を使う。
package docpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/config"
)

// defaultDocs は docs を省略したときに見るドキュメントの一覧（固定ファイル分）。
var defaultDocs = []string{"README.md", "CLAUDE.md"}

// defaultDocGlobs は defaultDocs に加えて追加で拾うグロブパターン。
// .github 配下（SECURITY.md など）もコードの場所を名指しするので対象に含める。
var defaultDocGlobs = []string{"docs/*.md", ".github/*.md"}

// backtickRe はバッククォートで囲まれた中身を拾う。地の文のそれらしい文字列まで拾うと
// 誤検知だらけになるため、これだけを候補とする。
var backtickRe = regexp.MustCompile("`([^`]+)`")

// defaultPathPrefixes は path_prefixes を省略したときに候補と認識するディレクトリ接頭辞。
// 元々は Go のモジュールレイアウト規約（internal/, cmd/）決め打ちだったが、他言語
// （src/, lib/, pkg/ など）でも使えるよう checks.<key>.path_prefixes で上書き可能にした
// （fuchigta/spotter#2）。
var defaultPathPrefixes = []string{"internal", "cmd", "scripts"}

// alwaysPathPrefixes は path_prefixes の指定に関わらず常に候補に含める接頭辞。
// 言語ではなく spotter/git 自身の慣習（フック・CI 設定）なので固定でよい。
var alwaysPathPrefixes = []string{".githooks", ".github"}

// Check は doc-paths 検査の 1 インスタンス。
type Check struct {
	docs       []string
	ignore     map[string]bool
	pathLikeRe *regexp.Regexp
}

// New は config.CheckConfig から Check を組み立てる。
func New(cc config.CheckConfig) (*Check, error) {
	ignore := make(map[string]bool, len(cc.Ignore))
	for _, p := range cc.Ignore {
		if p == "" {
			continue
		}
		ignore[p] = true
	}

	prefixes := cc.PathPrefixes
	if len(prefixes) == 0 {
		prefixes = defaultPathPrefixes
	}
	pathLikeRe, err := compilePathLikeRe(append(append([]string{}, prefixes...), alwaysPathPrefixes...))
	if err != nil {
		return nil, fmt.Errorf("docpaths: path_prefixes のコンパイルに失敗しました: %w", err)
	}

	return &Check{docs: cc.Docs, ignore: ignore, pathLikeRe: pathLikeRe}, nil
}

// compilePathLikeRe は接頭辞の一覧から「いずれかで始まる」正規表現を組み立てる。
func compilePathLikeRe(prefixes []string) (*regexp.Regexp, error) {
	parts := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		parts = append(parts, regexp.QuoteMeta(p))
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("path_prefixes が空です")
	}
	return regexp.Compile(`^(` + strings.Join(parts, "|") + `)/`)
}

// Granularity は現在の作業ツリーを 1 回だけ見る。checks 側からは上書きできない。
func (c *Check) Granularity() check.Granularity {
	return check.GranularityWorktree
}

// Run は対象ドキュメントからバッククォート内のパス候補を抜き出し、実在を確認する。
func (c *Check) Run(ctx check.Context) ([]check.Violation, error) {
	docs, err := c.resolveDocs(ctx.Root)
	if err != nil {
		return nil, fmt.Errorf("docpaths: 対象ドキュメントの解決に失敗しました: %w", err)
	}

	var violations []check.Violation
	for _, doc := range docs {
		data, err := os.ReadFile(filepath.Join(ctx.Root, doc))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("docpaths: %s の読み込みに失敗しました: %w", doc, err)
		}

		var missing []string
		for _, p := range c.extractCandidates(string(data)) {
			if c.ignore[p] {
				continue
			}
			ok, err := existsOrGlob(ctx.Root, p)
			if err != nil {
				return nil, fmt.Errorf("docpaths: %s の解決に失敗しました: %w", p, err)
			}
			if !ok {
				missing = append(missing, p)
			}
		}

		if len(missing) > 0 {
			violations = append(violations, check.Violation{
				Summary: fmt.Sprintf("%s が存在しないパスを指しています:", doc),
				Files:   missing,
			})
		}
	}

	return violations, nil
}

// resolveDocs は対象ドキュメントの一覧を返す。docs が指定されていればそれをそのまま使う。
func (c *Check) resolveDocs(root string) ([]string, error) {
	if len(c.docs) > 0 {
		return c.docs, nil
	}

	docs := append([]string{}, defaultDocs...)
	for _, pattern := range defaultDocGlobs {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			docs = append(docs, filepath.ToSlash(rel))
		}
	}
	return docs, nil
}

// extractCandidates はバッククォート内のパスらしき文字列を重複無く昇順で返す。
func (c *Check) extractCandidates(content string) []string {
	seen := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(content, -1) {
		p := m[1]
		if c.pathLikeRe.MatchString(p) {
			seen[p] = true
		}
	}
	candidates := make([]string, 0, len(seen))
	for p := range seen {
		candidates = append(candidates, p)
	}
	sort.Strings(candidates)
	return candidates
}

// existsOrGlob は p がリポジトリ内に実在するかを調べる。"*" を含む場合は
// 1 つ以上に一致すればよい（グロブ表記の例示）。
func existsOrGlob(root, p string) (bool, error) {
	full := filepath.Join(root, filepath.FromSlash(p))
	if strings.Contains(p, "*") {
		matches, err := filepath.Glob(full)
		if err != nil {
			return false, err
		}
		return len(matches) > 0, nil
	}
	if _, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
