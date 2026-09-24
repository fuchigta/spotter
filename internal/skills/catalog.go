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
//
// 同梱スキルはバイナリのビルド時に埋め込まれる（TestBundledSkillsConformToSpec が
// CI で全スキルの妥当性を検証する）ため、実行時に 1 つでも frontmatter が壊れている
// ことは通常起き得ない。起きた場合は「壊れたスキルだけ無視して一覧を返す」のではなく、
// どのスキルがどう壊れているかを呼び出し元にそのまま伝える方を選んでいる。
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
//
// name は Agent Skills 標準の name 制約（nameRe）を満たさない限り拒否する。
// この検証が無いと、embed.FS 上で path.Join("skills", name) がそのまま
// fs.Stat に通ってしまうケース（name が "."、空文字、"spotter-docs/.." 等）で
// スキルの本体ディレクトリ全体を合成した意図しない結果を返してしまう
// （path.Join がこれらを正規化してしまうため）。
func (c Catalog) Compose(name string) (map[string][]byte, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("skills: 未知のスキルです: %q", name)
	}

	base := path.Join("skills", name)
	if info, err := fs.Stat(c.SkillsFS, base); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("skills: 未知のスキルです: %q", name)
	}

	files := map[string][]byte{}
	// setFile は合成元（skills/ 本体と docs/ 由来の references/）が同じキーへ
	// 黙って上書きし合うことを防ぐ。将来 spotter-docs 以外のスキルが独自の
	// references/ を持つと衝突しうるため、検知した時点でエラーにする。
	setFile := func(key string, data []byte) error {
		if _, exists := files[key]; exists {
			return fmt.Errorf("skills: %s: %q が複数のソースから合成されようとしています（衝突）", name, key)
		}
		files[key] = data
		return nil
	}

	err := fs.WalkDir(c.SkillsFS, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := c.SkillsFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s の読み込みに失敗しました: %w", p, err)
		}
		return setFile(strings.TrimPrefix(p, base+"/"), data)
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
				return fmt.Errorf("%s の読み込みに失敗しました: %w", p, err)
			}
			return setFile("references/"+strings.TrimPrefix(p, "docs/"), data)
		})
		if err != nil {
			return nil, fmt.Errorf("skills: spotter-docs の references 合成に失敗しました: %w", err)
		}
	}

	return files, nil
}
