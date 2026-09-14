package skills_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuchigta/spotter/internal/skills"
)

func testInstaller(version string) skills.Installer {
	return skills.NewInstaller(testCatalog(), version)
}

func TestInstallCreatesNewDir(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")

	results, err := in.Install(dir, nil, false)
	if err != nil {
		t.Fatalf("Install error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("results が空です")
	}
	for _, r := range results {
		if r.Outcome != skills.OutcomeCreated {
			t.Errorf("%s: Outcome = %s, want created", r.Name, r.Outcome)
		}
	}

	skillMD := filepath.Join(dir, "spotter-docs", "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		t.Fatalf("SKILL.md が書き込まれていません: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spotter-docs", "references", "config-reference.md")); err != nil {
		t.Fatalf("references が書き込まれていません: %v", err)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")

	if _, err := in.Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("1回目の Install error: %v", err)
	}

	results, err := in.Install(dir, []string{"spotter-docs"}, false)
	if err != nil {
		t.Fatalf("2回目の Install error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != skills.OutcomeAlready {
		t.Errorf("2回目は already のはず: %+v", results)
	}
}

func TestInstallUpdatesOnVersionChange(t *testing.T) {
	dir := t.TempDir()

	if _, err := testInstaller("v1.0.0").Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("1回目の Install error: %v", err)
	}

	results, err := testInstaller("v2.0.0").Install(dir, []string{"spotter-docs"}, false)
	if err != nil {
		t.Fatalf("2回目の Install error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != skills.OutcomeUpdated {
		t.Errorf("バージョンが変わったら updated のはず: %+v", results)
	}
}

func TestInstallRejectsUnmanagedDirWithoutForce(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: spotter-docs\ndescription: 手動で置いたもの\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	in := testInstaller("v1.0.0")
	if _, err := in.Install(dir, []string{"spotter-docs"}, false); err == nil {
		t.Fatal("spotter 管理外のディレクトリへの上書きは force なしだとエラーになるはず")
	}
}

func TestInstallForceOverwritesUnmanagedDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: spotter-docs\ndescription: 手動で置いたもの\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// force なしでは削除されないはずの目印ファイル。writeTree は dir を
	// RemoveAll してから書き直すため、force での上書き成功時には消える。
	if err := os.WriteFile(filepath.Join(skillDir, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	in := testInstaller("v1.0.0")
	results, err := in.Install(dir, []string{"spotter-docs"}, true)
	if err != nil {
		t.Fatalf("force ありなのに Install error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != skills.OutcomeUpdated {
		t.Errorf("既存ディレクトリへの force 上書きは updated のはず: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "leftover.txt")); !os.IsNotExist(err) {
		t.Error("leftover.txt が残っています（writeTree が dir を置き換えていない）")
	}
}

func TestInstallUnknownSkillName(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")
	if _, err := in.Install(dir, []string{"no-such-skill"}, false); err == nil {
		t.Fatal("未知のスキル名はエラーになるはず")
	}
}

func TestUninstallRemovesManagedDir(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")
	if _, err := in.Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("Install error: %v", err)
	}

	results, err := in.Uninstall(dir, []string{"spotter-docs"}, false)
	if err != nil {
		t.Fatalf("Uninstall error: %v", err)
	}
	if len(results) != 1 || !results[0].Removed {
		t.Errorf("Removed=true のはず: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "spotter-docs")); !os.IsNotExist(err) {
		t.Error("ディレクトリが削除されていません")
	}
}

func TestUninstallRejectsUnmanagedDirWithoutForce(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: spotter-docs\ndescription: 手動で置いたもの\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	in := testInstaller("v1.0.0")
	if _, err := in.Uninstall(dir, []string{"spotter-docs"}, false); err == nil {
		t.Fatal("spotter 管理外のディレクトリの削除は force なしだとエラーになるはず")
	}
	if _, err := os.Stat(skillDir); err != nil {
		t.Error("エラーになったのにディレクトリが削除されています")
	}
}

func TestUninstallNotInstalledIsNoop(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")

	results, err := in.Uninstall(dir, []string{"spotter-docs"}, false)
	if err != nil {
		t.Fatalf("未設置のアンインストールはエラーにならないはず: %v", err)
	}
	if len(results) != 1 || results[0].Removed {
		t.Errorf("Removed=false のはず: %+v", results)
	}
}

func TestStatusReportsInstalledAndVersion(t *testing.T) {
	dir := t.TempDir()
	if _, err := testInstaller("v1.0.0").Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("Install error: %v", err)
	}

	entries, err := testInstaller("v1.0.0").Status(dir)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Name != "spotter-docs" {
			continue
		}
		found = true
		if !e.Installed || !e.Managed || !e.UpToDate {
			t.Errorf("spotter-docs の状態が期待と違います: %+v", e)
		}
	}
	if !found {
		t.Fatal("Status() に spotter-docs が含まれていません")
	}
}

func TestStatusReportsOutdatedVersion(t *testing.T) {
	dir := t.TempDir()
	if _, err := testInstaller("v1.0.0").Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("Install error: %v", err)
	}

	entries, err := testInstaller("v2.0.0").Status(dir)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}

	for _, e := range entries {
		if e.Name != "spotter-docs" {
			continue
		}
		if e.UpToDate {
			t.Errorf("バージョンが違うので UpToDate=false のはず: %+v", e)
		}
		if e.InstalledVersion != "v1.0.0" {
			t.Errorf("InstalledVersion = %q, want v1.0.0", e.InstalledVersion)
		}
	}
}

func TestStatusReportsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	entries, err := testInstaller("v1.0.0").Status(dir)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}

	for _, e := range entries {
		if e.Name != "spotter-docs" {
			continue
		}
		if e.Installed {
			t.Errorf("未設置のはず: %+v", e)
		}
	}
}

// hashTree・injectMeta は非公開なので Installer 経由の挙動で間接的に確認する。
// ここでは Compose() の元データが変わらない限りハッシュが安定していることを、
// Install を2回呼んで両方 already になることで確認する（安定していなければ
// 2回目が updated になってしまう）。
func TestInstallHashIsStableAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")

	for i := 0; i < 3; i++ {
		results, err := in.Install(dir, []string{"spotter-docs"}, false)
		if err != nil {
			t.Fatalf("Install (回 %d) error: %v", i, err)
		}
		want := skills.OutcomeCreated
		if i > 0 {
			want = skills.OutcomeAlready
		}
		if results[0].Outcome != want {
			t.Errorf("回 %d: Outcome = %s, want %s", i, results[0].Outcome, want)
		}
	}
}

func TestInstalledSkillMDIsStillValidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if _, err := testInstaller("v1.0.0").Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("Install error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "spotter-docs", "SKILL.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	fm, _, err := skills.ParseFrontmatter(data)
	if err != nil {
		t.Fatalf("設置後の SKILL.md が frontmatter として解析できません: %v", err)
	}
	if fm.Name != "spotter-docs" {
		t.Errorf("Name = %q, want spotter-docs", fm.Name)
	}
	if fm.Metadata["managed-by"] != "spotter" {
		t.Errorf("metadata.managed-by = %q, want spotter", fm.Metadata["managed-by"])
	}
	if fm.Metadata["spotter-version"] != "v1.0.0" {
		t.Errorf("metadata.spotter-version = %q, want v1.0.0", fm.Metadata["spotter-version"])
	}
}
