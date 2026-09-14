package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Catalog は埋め込み FS 上の同梱スキルへのアクセスを提供する。
type Catalog struct {
	// SkillsFS はモジュールルートの embed.go が公開する SkillsFS
	// （skills/ ディレクトリ全体）を渡す想定。
	SkillsFS embed.FS
	// DocsFS は同じく DocsFS（docs/ ディレクトリ全体）。spotter-docs スキルの
	// references/ を合成するために使う。
	DocsFS embed.FS
}

// NewCatalog は skillsFS/docsFS から Catalog を作る。
func NewCatalog(skillsFS, docsFS embed.FS) Catalog {
	return Catalog{SkillsFS: skillsFS, DocsFS: docsFS}
}

// Meta は同梱スキル 1 つのメタデータ（SKILL.md の frontmatter から取れる範囲）。
type Meta struct {
	Name        string
	Description string
	License     string
}

// List は同梱スキルの一覧を Name でソートして返す。
func (c Catalog) List() ([]Meta, error) {
	entries, err := fs.ReadDir(c.SkillsFS, "skills")
	if err != nil {
		return nil, fmt.Errorf("skills: 同梱スキルの一覧取得に失敗しました: %w", err)
	}

	var metas []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fm, err := c.readFrontmatter(e.Name())
		if err != nil {
			return nil, err
		}
		metas = append(metas, Meta{Name: fm.Name, Description: fm.Description, License: fm.License})
	}

	sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
	return metas, nil
}

func (c Catalog) readFrontmatter(dirName string) (Frontmatter, error) {
	data, err := c.SkillsFS.ReadFile(path.Join("skills", dirName, "SKILL.md"))
	if err != nil {
		return Frontmatter{}, fmt.Errorf("skills: %s の SKILL.md を読み込めません: %w", dirName, err)
	}
	fm, _, err := ParseFrontmatter(data)
	if err != nil {
		return Frontmatter{}, fmt.Errorf("skills: %s: %w", dirName, err)
	}
	if err := fm.ValidateNameMatchesDir(dirName); err != nil {
		return Frontmatter{}, err
	}
	return fm, nil
}

// Compose はスキル name の完全なファイルツリー（スキルルートからの相対パス →
// 中身）を返す。SKILL.md 本体に加えて、docs/ 由来の references/ 合成分を含む
// （現時点では spotter-docs だけがこの合成を必要とする）。
func (c Catalog) Compose(name string) (map[string][]byte, error) {
	base := path.Join("skills", name)
	if _, err := fs.Stat(c.SkillsFS, base); err != nil {
		return nil, fmt.Errorf("skills: 未知のスキルです: %q", name)
	}

	files := map[string][]byte{}
	err := fs.WalkDir(c.SkillsFS, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := c.SkillsFS.ReadFile(p)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(p, base+"/")] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("skills: %s の読み込みに失敗しました: %w", name, err)
	}

	// spotter-docs は docs/ 全体を references/ 配下に合成する。現状これを必要と
	// するスキルは 1 つだけなので name で分岐している。2 つ目以降が増えたら、
	// Meta 側に「合成する埋め込みディレクトリ」の宣言を持たせて一般化すること。
	if name == "spotter-docs" {
		err := fs.WalkDir(c.DocsFS, "docs", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := c.DocsFS.ReadFile(p)
			if err != nil {
				return err
			}
			files["references/"+strings.TrimPrefix(p, "docs/")] = data
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("skills: spotter-docs の references 合成に失敗しました: %w", err)
		}
	}

	return files, nil
}
