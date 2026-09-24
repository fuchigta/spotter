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

// TestInstallRejectsPathTraversalNames は TestUninstallRejectsPathTraversalNames と対で、
// --only 由来の name にパストラバーサルを含むものを Install に渡しても dir の外を
// 変更しないことを確認する（Catalog.Compose の nameRe 検証をすり抜けないこと）。
func TestInstallRejectsPathTraversalNames(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	installDir := filepath.Join(root, "install")
	in := testInstaller("v1.0.0")

	cases := []string{"../victim", "..\\victim", "spotter-docs/../../victim"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := in.Install(installDir, []string{name}, false); err == nil {
				t.Errorf("パストラバーサルを含む name %q はエラーになるはず", name)
			}
			if _, err := os.Stat(filepath.Join(victim, "keep.txt")); err != nil {
				t.Errorf("dir の外のファイルが変更されています（パストラバーサル）: %v", err)
			}
		})
	}
}

// TestInstallRejectsBrokenFrontmatterWithoutForce は、既存ディレクトリの SKILL.md の
// frontmatter が壊れている（終端の "---" が無く YAML として解析できない）場合、
// readManagedMeta が managed=false を返す（frontmatter が読めない = spotter が書いた
// ものではない、として扱う）ため、force なしの Install がエラーになることを確認する。
func TestInstallRejectsBrokenFrontmatterWithoutForce(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	broken := "---\nname: spotter-docs\ndescription: 終端が無い\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(broken), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	in := testInstaller("v1.0.0")
	if _, err := in.Install(dir, []string{"spotter-docs"}, false); err == nil {
		t.Fatal("frontmatter が壊れたディレクトリへの上書きは force なしだとエラーになるはず")
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

	results, err := in.Uninstall(dir, []string{"spotter-docs"}, false, false)
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
	if _, err := in.Uninstall(dir, []string{"spotter-docs"}, false, false); err == nil {
		t.Fatal("spotter 管理外のディレクトリの削除は force なしだとエラーになるはず")
	}
	if _, err := os.Stat(skillDir); err != nil {
		t.Error("エラーになったのにディレクトリが削除されています")
	}
}

func TestUninstallNotInstalledIsNoop(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")

	results, err := in.Uninstall(dir, []string{"spotter-docs"}, false, false)
	if err != nil {
		t.Fatalf("未設置のアンインストールはエラーにならないはず: %v", err)
	}
	if len(results) != 1 || results[0].Removed {
		t.Errorf("Removed=false のはず: %+v", results)
	}
}

// TestUninstallRejectsPathTraversalNames は、--only 由来の name にパス区切りや
// ".." を含むものを与えても、dir の外にあるディレクトリを削除できないことを
// 確認する（パストラバーサルで設置先の外を消させないため）。
func TestUninstallRejectsPathTraversalNames(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	installDir := filepath.Join(root, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	in := testInstaller("v1.0.0")
	cases := []string{"../victim", "..\\victim", "spotter-docs/../../victim"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := in.Uninstall(installDir, []string{name}, true, false); err == nil {
				t.Errorf("パストラバーサルを含む name %q はエラーになるはず", name)
			}
			if _, err := os.Stat(filepath.Join(victim, "keep.txt")); err != nil {
				t.Errorf("dir の外のファイルが削除されています（パストラバーサル）: %v", err)
			}
		})
	}
}

func TestUninstallOnlyDedupesNames(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")
	if _, err := in.Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("Install error: %v", err)
	}

	results, err := in.Uninstall(dir, []string{"spotter-docs", "spotter-docs"}, false, false)
	if err != nil {
		t.Fatalf("Uninstall error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("重複した name は1件にまとめられるはず: %+v", results)
	}
}

func TestUninstallForceRemovesUnmanagedDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: spotter-docs\ndescription: 手動\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	in := testInstaller("v1.0.0")
	results, err := in.Uninstall(dir, []string{"spotter-docs"}, true, false)
	if err != nil {
		t.Fatalf("force ありなのに Uninstall error: %v", err)
	}
	if len(results) != 1 || !results[0].Removed {
		t.Errorf("force ありなら spotter 管理外でも削除されるはず: %+v", results)
	}
}

// TestUninstallPartialFailureStopsBeforeRemoving は、複数 name のうち1つが
// force 無しで拒否される場合、それより前の対象も削除されない（検証を先に
// 全部済ませてから削除する2パス方式になっている）ことを確認する。
func TestUninstallPartialFailureStopsBeforeRemoving(t *testing.T) {
	dir := t.TempDir()
	in := testInstaller("v1.0.0")
	if _, err := in.Install(dir, []string{"spotter-docs"}, false); err != nil {
		t.Fatalf("Install error: %v", err)
	}

	unmanagedDir := filepath.Join(dir, "unmanaged")
	if err := os.MkdirAll(unmanagedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unmanagedDir, "SKILL.md"), []byte("---\nname: unmanaged\ndescription: 手動\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := in.Uninstall(dir, []string{"spotter-docs", "unmanaged"}, false, false); err == nil {
		t.Fatal("unmanaged が force 無しで拒否されるのでエラーになるはず")
	}

	if _, err := os.Stat(filepath.Join(dir, "spotter-docs")); err != nil {
		t.Error("検証で失敗する前に spotter-docs が削除されています（2パス方式が機能していない）")
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

// TestStatusReportsBrokenFrontmatterAsUnmanaged は、SKILL.md はあるが frontmatter が
// 壊れているディレクトリについて、Status() が Installed=true・Managed=false を返す
// ことを確認する（TestInstallRejectsBrokenFrontmatterWithoutForce と同じ壊れ方）。
func TestStatusReportsBrokenFrontmatterAsUnmanaged(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "spotter-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	broken := "---\nname: spotter-docs\ndescription: 終端が無い\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(broken), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
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
		if !e.Installed {
			t.Errorf("SKILL.md が存在するので Installed=true のはず: %+v", e)
		}
		if e.Managed {
			t.Errorf("frontmatter が壊れているので Managed=false のはず: %+v", e)
		}
	}
	if !found {
		t.Fatal("Status() に spotter-docs が含まれていません")
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

// TestInstalledSkillMDBytesAreStableAcrossRuns は、injectMeta が yaml.Marshal で
// frontmatter を再構築する際、map[string]string である Metadata の出力順が
// 実行ごとにぶれないこと（＝設置される SKILL.md のバイト列が毎回一致すること）
// を確認する。TestInstallHashIsStableAcrossRuns は Compose() の生データに対する
// ハッシュしか見ておらず injectMeta 後の出力までは保証しないため、これは別に
// 確認する必要がある。
func TestInstalledSkillMDBytesAreStableAcrossRuns(t *testing.T) {
	var first []byte
	for i := 0; i < 5; i++ {
		dir := t.TempDir()
		if _, err := testInstaller("v1.0.0").Install(dir, []string{"spotter-docs"}, false); err != nil {
			t.Fatalf("回 %d: Install error: %v", i, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "spotter-docs", "SKILL.md"))
		if err != nil {
			t.Fatalf("回 %d: ReadFile: %v", i, err)
		}
		if i == 0 {
			first = data
			continue
		}
		if string(data) != string(first) {
			t.Fatalf("回 %d: 設置された SKILL.md のバイト列が1回目と異なります（yaml.Marshal の出力が不安定）", i)
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
