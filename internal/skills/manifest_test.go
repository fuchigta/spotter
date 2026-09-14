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
	// 空文字は Validate の Name=="" 分岐（TestParseFrontmatterMissingName）で
	// 弾かれるため、ここでは「文字種として不正」なケースだけを扱う。
	cases := []string{"My-Skill", "my_skill", "-my-skill", "my-skill-", "my--skill"}
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

func TestParseFrontmatterCompatibilityTooLong(t *testing.T) {
	compat := strings.Repeat("a", 501)
	data := []byte("---\nname: my-skill\ndescription: y\ncompatibility: " + compat + "\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("501文字の compatibility はエラーになるはず")
	}
}

func TestParseFrontmatterOptionalFields(t *testing.T) {
	data := []byte(`---
name: my-skill
description: y
compatibility: bash が使える環境限定
allowed-tools: Read Bash
---
body
`)
	fm, _, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	if fm.Compatibility != "bash が使える環境限定" {
		t.Errorf("Compatibility = %q", fm.Compatibility)
	}
	want := []string{"Read", "Bash"}
	if len(fm.AllowedTools) != len(want) || fm.AllowedTools[0] != want[0] || fm.AllowedTools[1] != want[1] {
		t.Errorf("AllowedTools = %v, want %v（スペース区切り文字列形式）", fm.AllowedTools, want)
	}
}

func TestParseFrontmatterAllowedToolsAsSequence(t *testing.T) {
	data := []byte(`---
name: my-skill
description: y
allowed-tools:
  - Read
  - Bash
---
body
`)
	fm, _, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	want := []string{"Read", "Bash"}
	if len(fm.AllowedTools) != len(want) || fm.AllowedTools[0] != want[0] || fm.AllowedTools[1] != want[1] {
		t.Errorf("AllowedTools = %v, want %v（配列形式）", fm.AllowedTools, want)
	}
}

func TestParseFrontmatterRejectsUnknownKeys(t *testing.T) {
	// これらは Claude Code 固有の拡張フィールドで、標準仕様には無い。同じ SKILL.md を
	// 他のエージェントにも配る前提なので、標準外キーはエラーにする。
	cases := []string{
		"disable-model-invocation: true",
		"argument-hint: '<path>'",
		"model: opus",
		"context: fork",
	}
	for _, extra := range cases {
		t.Run(extra, func(t *testing.T) {
			data := []byte("---\nname: my-skill\ndescription: y\n" + extra + "\n---\nbody\n")
			if _, _, err := skills.ParseFrontmatter(data); err == nil {
				t.Errorf("%s は標準仕様に無いキーなのでエラーになるはず", extra)
			}
		})
	}
}

func TestParseFrontmatterCRLF(t *testing.T) {
	data := []byte("---\r\nname: my-skill\r\ndescription: y\r\n---\r\nbody line\r\n")
	fm, body, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("CRLF の SKILL.md を解析できません: %v", err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", fm.Name)
	}
	if !strings.Contains(body, "body line") {
		t.Errorf("body = %q", body)
	}
}

func TestParseFrontmatterBodyStartingWithDashesIsNotMistakenForDelimiter(t *testing.T) {
	// frontmatter の終端探索は最初に見つかった「行全体が正確に ---」の行で
	// 止まる。本文（frontmatter の外）側にたまたま "---" で始まる行があっても、
	// 既に終端は確定しているので影響しないことを確認する。
	data := []byte("---\nname: my-skill\ndescription: y\n---\n---not-a-delim\nbody\n")
	fm, body, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", fm.Name)
	}
	want := "---not-a-delim\nbody\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestParseFrontmatterAmbiguousDashesFailsLoudly(t *testing.T) {
	// frontmatter 内（YAML のブロックスカラー等）にインデントされていない
	// "---" で始まる行が来ると、それを終端行として扱わない代わりに次の本物の
	// "---" までを frontmatter として YAML に渡すことになる。この入力は
	// その結果 YAML として不正になる。これは意図した動作: サイレントに
	// 本文の一部が frontmatter に取り込まれたり frontmatter の一部が本文に
	// 漏れたりするより、明確なエラーになる方が安全。
	data := []byte("---\nname: my-skill\ndescription: |\n  a\n---not-a-delim\nmore: yes\n---\nbody\n")
	if _, _, err := skills.ParseFrontmatter(data); err == nil {
		t.Fatal("あいまいな --- を含む frontmatter は YAML エラーとして検出されるはず")
	}
}

func TestParseFrontmatterDelimiterLineWithTrailingSpace(t *testing.T) {
	data := []byte("---\nname: my-skill\ndescription: y\n--- \nbody\n")
	fm, body, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", fm.Name)
	}
	if body != "body\n" {
		t.Errorf("body = %q, want %q", body, "body\n")
	}
}

func TestParseFrontmatterEmptyBody(t *testing.T) {
	data := []byte("---\nname: my-skill\ndescription: y\n---\n")
	fm, body, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", fm.Name)
	}
	if body != "" {
		t.Errorf("body = %q, want 空文字", body)
	}
}

func TestParseFrontmatterNoTrailingNewlineAfterEnd(t *testing.T) {
	// frontmatter の終端行の後に改行が無い（ファイルが "---" で終わる）ケース。
	data := []byte("---\nname: my-skill\ndescription: y\n---")
	fm, body, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("ParseFrontmatter error: %v", err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", fm.Name)
	}
	if body != "" {
		t.Errorf("body = %q, want 空文字", body)
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
