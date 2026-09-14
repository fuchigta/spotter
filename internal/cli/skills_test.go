package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunSkillsList(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsList(&buf, false); err != nil {
		t.Fatalf("runSkillsList: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "spotter-docs:") {
		t.Errorf("出力に spotter-docs が含まれていません:\n%s", out)
	}
}

func TestRunSkillsListJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsList(&buf, true); err != nil {
		t.Fatalf("runSkillsList: %v", err)
	}

	var metas []struct {
		Name        string `json:"Name"`
		Description string `json:"Description"`
	}
	if err := json.Unmarshal(buf.Bytes(), &metas); err != nil {
		t.Fatalf("json.Unmarshal: %v（出力: %s）", err, buf.String())
	}
	if len(metas) == 0 {
		t.Fatal("JSON 出力が空です")
	}
}

func TestRunSkillsShowDefaultOmitsFullReferenceDump(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "spotter-docs", "", false); err != nil {
		t.Fatalf("runSkillsShow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "name: spotter-docs") {
		t.Error("既定の出力に SKILL.md 本体が含まれていません")
	}
	if !strings.Contains(out, "references/config-reference.md") {
		t.Error("既定の出力にファイル一覧が含まれていません")
	}
	// progressive disclosure: 個別ファイルの中身は --file を指定しない限り
	// ダンプしない。
	if strings.Contains(out, "## `checks`") {
		t.Error("--file を指定していないのに references/config-reference.md の中身がダンプされています")
	}
}

func TestRunSkillsShowListOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "spotter-docs", "", true); err != nil {
		t.Fatalf("runSkillsShow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "references/config-reference.md") {
		t.Error("--list の出力にファイルパスが含まれていません")
	}
	if strings.Contains(out, "name: spotter-docs") {
		t.Error("--list の出力に SKILL.md の中身が含まれています")
	}
}

func TestRunSkillsShowFile(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "spotter-docs", "references/hooks.md", false); err != nil {
		t.Fatalf("runSkillsShow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "spotter hooks install") {
		t.Errorf("--file references/hooks.md の出力が期待した中身を含んでいません:\n%s", out)
	}
}

func TestRunSkillsShowFileUnknown(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "spotter-docs", "no-such-file.md", false); err == nil {
		t.Fatal("存在しないファイルの指定はエラーになるはず")
	}
}

func TestRunSkillsShowUnknownSkill(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "no-such-skill", "", false); err == nil {
		t.Fatal("未知のスキル名はエラーになるはず")
	}
}
