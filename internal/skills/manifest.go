package skills

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter は SKILL.md 冒頭の YAML frontmatter。Agent Skills 標準
// （https://agentskills.io/specification）が定義するフィールドのみを扱う。
// Claude Code 固有の追加フィールド（disable-model-invocation 等）は、
// このリポジトリが Codex など他のエージェントにも同じ SKILL.md を配ることを
// 前提にしているため意図的に持たない（標準以外の環境では「Unexpected key」に
// なりうるため）。
type Frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
}

const frontmatterDelim = "---"

// nameRe は Agent Skills 標準の name 制約（小文字英数字とハイフンのみ、先頭・末尾は
// ハイフン不可、ハイフンの連続不可）を表す。
var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ParseFrontmatter は "---\n...yaml...\n---\n" で始まる SKILL.md から
// frontmatter と本文を分離し、標準仕様の必須フィールド・制約を検証する。
func ParseFrontmatter(data []byte) (Frontmatter, string, error) {
	s := string(data)
	if !strings.HasPrefix(s, frontmatterDelim+"\n") {
		return Frontmatter{}, "", fmt.Errorf(`skills: SKILL.md は "---" で始まる frontmatter が必要です`)
	}
	rest := s[len(frontmatterDelim)+1:]

	end := strings.Index(rest, "\n"+frontmatterDelim)
	if end == -1 {
		return Frontmatter{}, "", fmt.Errorf(`skills: frontmatter の終端 "---" が見つかりません`)
	}
	yamlPart := rest[:end]
	body := strings.TrimPrefix(rest[end+len(frontmatterDelim)+1:], "\n")

	// KnownFields(true) で Frontmatter に無いキーをエラーにする。Claude Code 固有の
	// 追加フィールド（disable-model-invocation 等）を紛れ込ませないためのガード
	// （このリポジトリは同じ SKILL.md を Codex 等の他エージェントにも配る前提のため、
	// 標準外のキーは他環境で「Unexpected key」になりうる）。
	var fm Frontmatter
	dec := yaml.NewDecoder(bytes.NewReader([]byte(yamlPart)))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return Frontmatter{}, "", fmt.Errorf("skills: frontmatter の YAML を解析できません（標準仕様に無いキーがある可能性があります）: %w", err)
	}

	if err := fm.Validate(); err != nil {
		return Frontmatter{}, "", err
	}

	return fm, body, nil
}

// Validate は Agent Skills 標準が定める制約を確認する
// （https://agentskills.io/specification の name/description の項）。
// name とディレクトリ名の一致は、ディレクトリ名を知っている呼び出し側
// （catalog.go）が別途 ValidateNameMatchesDir で確認する。
func (fm Frontmatter) Validate() error {
	if fm.Name == "" {
		return fmt.Errorf("skills: frontmatter に name が必要です")
	}
	if len(fm.Name) > 64 {
		return fmt.Errorf("skills: name は64文字以内である必要があります: %q", fm.Name)
	}
	if !nameRe.MatchString(fm.Name) {
		return fmt.Errorf("skills: name は小文字英数字とハイフンのみ使え、先頭・末尾のハイフンや連続するハイフンは使えません: %q", fm.Name)
	}
	if fm.Description == "" {
		return fmt.Errorf("skills: frontmatter に description が必要です（name=%q）", fm.Name)
	}
	if len(fm.Description) > 1024 {
		return fmt.Errorf("skills: description は1024文字以内である必要があります（name=%q）", fm.Name)
	}
	if len(fm.Compatibility) > 500 {
		return fmt.Errorf("skills: compatibility は500文字以内である必要があります（name=%q）", fm.Name)
	}
	return nil
}

// ValidateNameMatchesDir は frontmatter の name が、そのスキルを配置している
// ディレクトリ名と一致することを確認する（Agent Skills 標準の要件）。
func (fm Frontmatter) ValidateNameMatchesDir(dirName string) error {
	if fm.Name != dirName {
		return fmt.Errorf("skills: frontmatter の name (%q) がディレクトリ名 (%q) と一致しません", fm.Name, dirName)
	}
	return nil
}
