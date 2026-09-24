package docutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check/docutil"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestResolveDocsDefault(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "")
	writeFile(t, root, "docs/guide.md", "")
	writeFile(t, root, ".git/COMMIT_EDITMSG.md", "")

	docs, err := docutil.ResolveDocs(os.DirFS(root), nil)
	if err != nil {
		t.Fatalf("ResolveDocs() error: %v", err)
	}
	want := []string{"README.md", "docs/guide.md"}
	if len(docs) != len(want) {
		t.Fatalf("ResolveDocs() = %v, want %v", docs, want)
	}
	for i := range want {
		if docs[i] != want[i] {
			t.Errorf("ResolveDocs()[%d] = %q, want %q", i, docs[i], want[i])
		}
	}
}

func TestResolveDocsInvalidPattern(t *testing.T) {
	root := t.TempDir()
	if _, err := docutil.ResolveDocs(os.DirFS(root), []string{"["}); err == nil {
		t.Fatal("不正なパターンなら ResolveDocs() はエラーになるはず")
	}
}

func TestStripCodeFences(t *testing.T) {
	content := "本文\n```sh\necho `date`\n```\n続き\n"
	got := docutil.StripCodeFences(content)
	if got != "本文\n続き\n" {
		t.Errorf("StripCodeFences() = %q", got)
	}
}

func TestStripCodeFencesKeepLines(t *testing.T) {
	content := "本文\n```sh\necho `date`\n```\n続き\n"
	got := docutil.StripCodeFencesKeepLines(content)
	want := "本文\n\n\n\n続き\n"
	if got != want {
		t.Errorf("StripCodeFencesKeepLines() = %q, want %q", got, want)
	}
	if gotLines, wantLines := len(strings.Split(got, "\n")), len(strings.Split(content, "\n")); gotLines != wantLines {
		t.Errorf("行数が元のドキュメントとそろわない: got %d, want %d", gotLines, wantLines)
	}
}

func TestExistsOrGlob(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/cli/root.go", "")
	writeFile(t, root, "internal/cli/sub/deep.go", "")
	fsys := os.DirFS(root)

	if !docutil.ExistsOrGlob(fsys, "internal/cli/root.go") {
		t.Error("実在するファイルは true のはず")
	}
	if docutil.ExistsOrGlob(fsys, "internal/cli/missing.go") {
		t.Error("実在しないファイルは false のはず")
	}
	if !docutil.ExistsOrGlob(fsys, "internal/cli/*.go") {
		t.Error("1 つでも一致する glob は true のはず")
	}
	if !docutil.ExistsOrGlob(fsys, "internal/cli/**/*.go") {
		t.Error("\"**\" はネストした階層のファイルにも一致するはず（doublestar のため 0 階層以上）")
	}
	if docutil.ExistsOrGlob(fsys, "internal/cli/[abc*.go") {
		t.Error("不正な glob は false（存在しない扱い）のはず")
	}
}
