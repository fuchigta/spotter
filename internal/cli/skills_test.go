package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestRunSkillsInstallWithDir(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runSkillsInstall(&buf, "claude", "project", dir, "", false, false); err != nil {
		t.Fatalf("runSkillsInstall: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "spotter-docs", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md が書き込まれていません: %v", err)
	}
	if !strings.Contains(buf.String(), "created") {
		t.Errorf("出力に created が含まれていません: %s", buf.String())
	}
}

func TestRunSkillsInstallDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runSkillsInstall(&buf, "claude", "project", dir, "", false, true); err != nil {
		t.Fatalf("runSkillsInstall: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "spotter-docs")); !os.IsNotExist(err) {
		t.Error("--dry-run なのにファイルが書き込まれています")
	}
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Errorf("出力に [dry-run] が含まれていません: %s", buf.String())
	}
}

func TestRunSkillsInstallAllWithDirIsRejected(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runSkillsInstall(&buf, "all", "project", dir, "", false, false); err == nil {
		t.Fatal("target=all と --dir の併用はエラーになるはず")
	}
}

func TestRunSkillsInstallUnknownTarget(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsInstall(&buf, "bogus", "project", t.TempDir(), "", false, false); err == nil {
		t.Fatal("未知の target はエラーになるはず")
	}
}

func TestRunSkillsInstallInvalidScope(t *testing.T) {
	var buf bytes.Buffer
	if err := runSkillsInstall(&buf, "claude", "bogus-scope", t.TempDir(), "", false, false); err == nil {
		t.Fatal("未知の scope はエラーになるはず")
	}
}

func TestRunSkillsInstallOnlyFiltersNames(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runSkillsInstall(&buf, "claude", "project", dir, "spotter-docs", false, false); err != nil {
		t.Fatalf("runSkillsInstall: %v", err)
	}
	if !strings.Contains(buf.String(), "spotter-docs") {
		t.Errorf("--only で絞ったスキルが出力されていません: %s", buf.String())
	}
}

func TestRunSkillsUninstallWithDir(t *testing.T) {
	dir := t.TempDir()
	var installBuf bytes.Buffer
	if err := runSkillsInstall(&installBuf, "claude", "project", dir, "", false, false); err != nil {
		t.Fatalf("runSkillsInstall: %v", err)
	}

	var buf bytes.Buffer
	if err := runSkillsUninstall(&buf, "claude", "project", dir, "", false, false); err != nil {
		t.Fatalf("runSkillsUninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spotter-docs")); !os.IsNotExist(err) {
		t.Error("uninstall 後もディレクトリが残っています")
	}
	if !strings.Contains(buf.String(), "削除しました") {
		t.Errorf("出力に削除の報告が含まれていません: %s", buf.String())
	}
}

func TestRunSkillsUninstallNotInstalled(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	// names を空にすると「dir 直下にあるものを消す」動作になり、何も無ければ
	// 対象自体が0件になる。「未設置」の報告を見るには対象スキル名を明示する。
	if err := runSkillsUninstall(&buf, "claude", "project", dir, "spotter-docs", false, false); err != nil {
		t.Fatalf("runSkillsUninstall: %v", err)
	}
	if !strings.Contains(buf.String(), "設置されていません") {
		t.Errorf("出力に未設置の報告が含まれていません: %s", buf.String())
	}
}

func TestRunSkillsUninstallDryRunRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	var installBuf bytes.Buffer
	if err := runSkillsInstall(&installBuf, "claude", "project", dir, "", false, false); err != nil {
		t.Fatalf("runSkillsInstall: %v", err)
	}

	var buf bytes.Buffer
	if err := runSkillsUninstall(&buf, "claude", "project", dir, "", false, true); err != nil {
		t.Fatalf("runSkillsUninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spotter-docs", "SKILL.md")); err != nil {
		t.Errorf("--dry-run なのに削除されています: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("出力に [dry-run] が含まれていません: %s", out)
	}
	if !strings.Contains(out, "削除される予定") {
		t.Errorf("出力に削除予定の報告が含まれていません: %s", out)
	}
}

func TestRunSkillsUninstallRejectsUnmanagedWithoutForce(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: spotter-docs\ndescription: 手動\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var buf bytes.Buffer
	if err := runSkillsUninstall(&buf, "claude", "project", dir, "", false, false); err == nil {
		t.Fatal("spotter 管理外のディレクトリの削除は force なしだとエラーになるはず")
	}
}

func TestRunSkillsStatusInThisRepo(t *testing.T) {
	// このリポジトリ自身をカレントディレクトリとして動く（repoRoot = "."）。
	// .claude/skills, .agents/skills は .gitignore 済みで、通常は存在しない
	// ので「未設置」が返るはず。書き込みは一切行わない。
	var buf bytes.Buffer
	if err := runSkillsStatus(&buf, "project"); err != nil {
		t.Fatalf("runSkillsStatus: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "spotter-docs") {
		t.Errorf("出力に spotter-docs が含まれていません: %s", out)
	}
}
