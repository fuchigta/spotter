package update

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{"linux", "amd64", "spotter-linux-amd64", false},
		{"linux", "arm64", "spotter-linux-arm64", false},
		{"darwin", "amd64", "spotter-darwin-amd64", false},
		{"darwin", "arm64", "spotter-darwin-arm64", false},
		{"windows", "amd64", "spotter-windows-amd64.exe", false},
		{"windows", "arm64", "", true},
		{"freebsd", "amd64", "", true},
	}

	for _, c := range cases {
		got, err := AssetName(c.goos, c.goarch)
		if c.wantErr {
			if err == nil {
				t.Errorf("AssetName(%s, %s): エラーになるはず", c.goos, c.goarch)
			}
			continue
		}
		if err != nil {
			t.Errorf("AssetName(%s, %s): %v", c.goos, c.goarch, err)
			continue
		}
		if got != c.want {
			t.Errorf("AssetName(%s, %s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			t.Errorf("想定外のパス: %s", r.URL.Path)
		}
		w.Header().Set("Location", "https://example.invalid/x/y/releases/tag/v1.2.3")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	tag, err := LatestTag(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("LatestTag: %v", err)
	}
	if tag != "v1.2.3" {
		t.Errorf("tag = %q, want v1.2.3", tag)
	}
}

func TestLatestTagNonRedirectIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := LatestTag(srv.Client(), srv.URL); err == nil {
		t.Fatal("リダイレクトでないレスポンスはエラーになるはず")
	}
}

func TestLatestTagUnparsableLocationIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.invalid/not-a-tag-url")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	if _, err := LatestTag(srv.Client(), srv.URL); err == nil {
		t.Fatal("タグを含まない Location はエラーになるはず")
	}
}

func TestDownloadURL(t *testing.T) {
	if got, want := DownloadURL("https://example.invalid/r", "", "spotter-linux-amd64"),
		"https://example.invalid/r/releases/latest/download/spotter-linux-amd64"; got != want {
		t.Errorf("DownloadURL(latest) = %q, want %q", got, want)
	}
	if got, want := DownloadURL("https://example.invalid/r", "v1.2.3", "spotter-linux-amd64"),
		"https://example.invalid/r/releases/download/v1.2.3/spotter-linux-amd64"; got != want {
		t.Errorf("DownloadURL(tag) = %q, want %q", got, want)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	data, err := Fetch(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want %q", data, "hello")
	}
}

func TestFetchNotFoundIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Fetch(srv.Client(), srv.URL); err == nil {
		t.Fatal("404 はエラーになるはず")
	}
}

func TestVerifyChecksum(t *testing.T) {
	binary := []byte("dummy binary content")
	want := sha256Hex(binary)

	if err := VerifyChecksum(binary, []byte(want+"  spotter-linux-amd64\n")); err != nil {
		t.Errorf("一致するチェックサムでエラーになった: %v", err)
	}

	if err := VerifyChecksum(binary, []byte("0000000000000000000000000000000000000000000000000000000000000000  spotter-linux-amd64\n")); err == nil {
		t.Error("不一致のチェックサムがエラーにならなかった")
	}

	if err := VerifyChecksum(binary, []byte("")); err == nil {
		t.Error("空の sha256 ファイルがエラーにならなかった")
	}
}

func TestInstallReplacesExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "spotter")
	if err := os.WriteFile(exe, []byte("old content"), 0o755); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	if err := Install(exe, []byte("new content")); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(got) != "new content" {
		t.Errorf("content = %q, want %q", got, "new content")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("差し替え後にディレクトリへ残留物がある: %v", names)
	}
}

func TestInstallMissingDirIsError(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "no-such-dir", "spotter")
	if err := Install(exe, []byte("new content")); err == nil {
		t.Fatal("存在しないディレクトリへの Install はエラーになるはず")
	}
}
