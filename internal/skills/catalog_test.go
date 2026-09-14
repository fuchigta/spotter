package skills_test

import (
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
		})
	}
}
