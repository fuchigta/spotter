package skills_test

import (
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/skills"
)

func TestParseFrontmatterValid(t *testing.T) {
	data := []byte(`---
name: my-skill
description: 何かをするスキル
license: MIT
metadata:
  managed-by: spotter
---
# 本文
ここに本文が入る。
`)

	fm, body, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", fm.Name)
	}
	if fm.Description != "何かをするスキル" {
		t.Errorf("Description = %q", fm.Description)
	}
	if fm.License != "MIT" {
		t.Errorf("License = %q, want MIT", fm.License)
	}
	if fm.Metadata["managed-by"] != "spotter" {
		t.Errorf("Metadata[managed-by] = %q, want spotter", fm.Metadata["managed-by"])
	}
	if !strings.Contains(body, "# 本文") {
		t.Errorf("body に見出しが含まれていません: %q", body)
	}
}

func TestParseFrontmatterMissingDelimiter(t *testing.T) {
	if _, _, err := skills.ParseFrontmatter([]byte("name: x\ndescription: y\n")); err == nil {
		t.Fatal("frontmatter の区切りが無ければエラーになるはず")
	}
}

func TestParseFrontmatterUnterminated(t *testing.T) {
	if _, _, err := skills.ParseFrontmatter([]byte("---\nname: x\ndescription: y\n")); err == nil {
		t.Fatal("frontmatter の終端が無ければエラーになるはず")
	}
}

func TestParseFrontmatterMissingName(t *testing.T) {
	data := []byte("---\ndescription: y\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("name が無ければエラーになるはず")
	}
}

func TestParseFrontmatterMissingDescription(t *testing.T) {
	data := []byte("---\nname: x\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("description が無ければエラーになるはず")
	}
}

func TestParseFrontmatterInvalidNameChars(t *testing.T) {
	cases := []string{"My-Skill", "my_skill", "-my-skill", "my-skill-", "my--skill", ""}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			data := []byte("---\nname: " + name + "\ndescription: y\n---\nbody\n")
			if _, _, err := skills.ParseFrontmatter(data); err == nil {
				t.Errorf("name=%q はエラーになるはず", name)
			}
		})
	}
}

func TestParseFrontmatterNameTooLong(t *testing.T) {
	name := strings.Repeat("a", 65)
	data := []byte("---\nname: " + name + "\ndescription: y\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("65文字の name はエラーになるはず")
	}
}

func TestParseFrontmatterDescriptionTooLong(t *testing.T) {
	desc := strings.Repeat("a", 1025)
	data := []byte("---\nname: my-skill\ndescription: " + desc + "\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("1025文字の description はエラーになるはず")
	}
}

func TestParseFrontmatterRejectsUnknownKeys(t *testing.T) {
	// disable-model-invocation は Claude Code 固有の拡張フィールドで、標準仕様には無い。
	// 同じ SKILL.md を他のエージェントにも配る前提なので、標準外キーはエラーにする。
	data := []byte("---\nname: my-skill\ndescription: y\ndisable-model-invocation: true\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("標準仕様に無いキーはエラーになるはず")
	}
}

func TestFrontmatterValidateNameMatchesDir(t *testing.T) {
	fm := skills.Frontmatter{Name: "my-skill", Description: "d"}

	if err := fm.ValidateNameMatchesDir("my-skill"); err != nil {
		t.Errorf("一致しているのにエラー: %v", err)
	}
	if err := fm.ValidateNameMatchesDir("other-skill"); err == nil {
		t.Error("不一致なのにエラーになりません")
	}
}
