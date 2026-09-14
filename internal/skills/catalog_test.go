package skills_test

import (
	"regexp"
	"strings"
	"testing"

	spotter "github.com/fuchigta/spotter"
	"github.com/fuchigta/spotter/internal/skills"
)

func testCatalog() skills.Catalog {
	return skills.NewCatalog(spotter.SkillsFS, spotter.DocsFS)
}

func TestCatalogListIncludesSpotterDocs(t *testing.T) {
	metas, err := testCatalog().List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	var found bool
	for _, m := range metas {
		if m.Name == "spotter-docs" {
			found = true
			if m.Description == "" {
				t.Error("spotter-docs の Description が空です")
			}
		}
	}
	if !found {
		t.Fatalf("List() に spotter-docs が含まれていません: %+v", metas)
	}
}

func TestCatalogListIsSorted(t *testing.T) {
	metas, err := testCatalog().List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	for i := 1; i < len(metas); i++ {
		if metas[i-1].Name > metas[i].Name {
			t.Fatalf("List() がソートされていません: %+v", metas)
		}
	}
}

func TestCatalogComposeSpotterDocs(t *testing.T) {
	files, err := testCatalog().Compose("spotter-docs")
	if err != nil {
		t.Fatalf("Compose() error: %v", err)
	}

	if _, ok := files["SKILL.md"]; !ok {
		t.Error("Compose() の結果に SKILL.md がありません")
	}
	if _, ok := files["references/config-reference.md"]; !ok {
		t.Error("Compose() の結果に references/config-reference.md がありません（docs/ の合成漏れ）")
	}
	if _, ok := files["references/checks/doc-sync.md"]; !ok {
		t.Error("Compose() の結果に references/checks/doc-sync.md がありません（docs/checks/ の合成漏れ）")
	}

	// docs/README.md は references/README.md になる（サブディレクトリと衝突しない
	// 唯一のトップレベル README なので特別扱いは不要）。
	if _, ok := files["references/README.md"]; !ok {
		t.Error("Compose() の結果に references/README.md がありません")
	}
}

func TestCatalogComposeUnknownSkill(t *testing.T) {
	if _, err := testCatalog().Compose("no-such-skill"); err == nil {
		t.Fatal("未知のスキル名はエラーになるはず")
	}
}

// TestCatalogComposeRejectsPathLikeNames は、path.Join("skills", name) が正規化
// してしまう入力（"."、空文字、".." を含むもの等）を Compose がすり抜けて
// skills/ ツリー全体やディレクトリを誤って「1 つのスキル」として合成しないことを
// 確認する。
func TestCatalogComposeRejectsPathLikeNames(t *testing.T) {
	cases := []string{".", "", "spotter-docs/..", "spotter-docs/", "../skills", "spotter-docs/SKILL.md"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := testCatalog().Compose(name); err == nil {
				t.Errorf("Compose(%q) はエラーになるはず", name)
			}
		})
	}
}

// TestBundledSkillsConformToSpec は同梱スキル全てが Agent Skills 標準の frontmatter
// 制約（ParseFrontmatter が検証する範囲）と、SKILL.md 本文の行数上限（progressive
// disclosure のガイドライン: 500 行未満）を満たすことを確認する。
func TestBundledSkillsConformToSpec(t *testing.T) {
	metas, err := testCatalog().List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(metas) == 0 {
		t.Fatal("同梱スキルが 1 つもありません")
	}

	for _, m := range metas {
		t.Run(m.Name, func(t *testing.T) {
			files, err := testCatalog().Compose(m.Name)
			if err != nil {
				t.Fatalf("Compose(%q) error: %v", m.Name, err)
			}

			skillMD, ok := files["SKILL.md"]
			if !ok {
				t.Fatal("SKILL.md がありません")
			}

			if _, _, err := skills.ParseFrontmatter(skillMD); err != nil {
				t.Errorf("frontmatter が標準仕様を満たしません: %v", err)
			}

			lines := strings.Count(string(skillMD), "\n")
			if lines >= 500 {
				t.Errorf("SKILL.md が %d 行あります（500 行未満を推奨。progressive disclosure のため本文は references/ に逃がすこと）", lines)
			}

			for _, ref := range referencePathsIn(string(skillMD)) {
				if strings.HasSuffix(ref, "/") {
					// ディレクトリ参照（例: `references/checks/`）は、その接頭辞を
					// 持つファイルが 1 件以上あるかで存在確認する。
					if !hasPrefixedKey(files, ref) {
						t.Errorf("SKILL.md が参照しているディレクトリ %q 配下にファイルが 1 つもありません", ref)
					}
					continue
				}
				if _, ok := files[ref]; !ok {
					t.Errorf("SKILL.md が参照している %q が Compose() の結果に存在しません（リンク切れ、またはコマンド名/パスのリネーム漏れ）", ref)
				}
			}
		})
	}
}

// referencePathsIn は SKILL.md 本文のバッククォート内から "references/..." で
// 始まるパス表記を抜き出す。TestBundledSkillsConformToSpec が、SKILL.md が
// 案内しているファイルが実際に Compose() の結果に存在するかを検証するために使う
// （spotter install → spotter hooks install のリネームを SKILL.md 側が
// 追随し損ねていた、というレビュー指摘の再発防止）。
var referencePathRe = regexp.MustCompile("`(references/[\\w./-]+)`")

func referencePathsIn(body string) []string {
	var refs []string
	for _, m := range referencePathRe.FindAllStringSubmatch(body, -1) {
		refs = append(refs, m[1])
	}
	return refs
}

func hasPrefixedKey(files map[string][]byte, prefix string) bool {
	for k := range files {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}
