package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSkillsList(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsList(&buf); err != nil {
		t.Fatalf("runSkillsList: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "spotter-docs:") {
		t.Errorf("出力に spotter-docs が含まれていません:\n%s", out)
	}
}

func TestRunSkillsShow(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "spotter-docs"); err != nil {
		t.Fatalf("runSkillsShow: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"=== SKILL.md ===", "=== references/config-reference.md ===", "name: spotter-docs"} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていません", want)
		}
	}
}

func TestRunSkillsShowUnknown(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsShow(&buf, "no-such-skill"); err == nil {
		t.Fatal("未知のスキル名はエラーになるはず")
	}
}
