package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/update"
)

// withUpdateTestServer は updateRepoURL・executable・buildVersion を差し替え、
// テスト終了時に元に戻す。assetBinary が空文字なら latest/pinned のダウンロード
// エンドポイントを登録しない（「既に最新」や --check だけを試すケース向け）。
func withUpdateTestServer(t *testing.T, latestTag, currentVersion, assetBinary string) (srv *httptest.Server, exePath string) {
	t.Helper()

	assetName, err := update.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("このプラットフォーム（%s/%s）向けの配布バイナリは無い: %v", runtime.GOOS, runtime.GOARCH, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/releases/tag/"+latestTag)
		w.WriteHeader(http.StatusFound)
	})
	if assetBinary != "" {
		sum := sha256.Sum256([]byte(assetBinary))
		shaLine := hex.EncodeToString(sum[:]) + "  " + assetName + "\n"

		serveAsset := func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, assetBinary)
		}
		serveSha := func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, shaLine)
		}

		mux.HandleFunc("/releases/latest/download/"+assetName, serveAsset)
		mux.HandleFunc("/releases/latest/download/"+assetName+".sha256", serveSha)
		mux.HandleFunc("/releases/download/"+latestTag+"/"+assetName, serveAsset)
		mux.HandleFunc("/releases/download/"+latestTag+"/"+assetName+".sha256", serveSha)
	}

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	origRepoURL, origExecutable, origVersion := updateRepoURL, executable, buildVersion
	updateRepoURL = srv.URL
	buildVersion = currentVersion
	t.Cleanup(func() {
		updateRepoURL = origRepoURL
		executable = origExecutable
		buildVersion = origVersion
	})

	dir := t.TempDir()
	exePath = filepath.Join(dir, "spotter")
	if err := os.WriteFile(exePath, []byte("old content"), 0o755); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	executable = func() (string, error) { return exePath, nil }

	return srv, exePath
}

func TestRunUpdateAlreadyUpToDate(t *testing.T) {
	withUpdateTestServer(t, "v1.0.0", "v1.0.0", "")

	var buf bytes.Buffer
	if err := runUpdate(&buf, false, ""); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !strings.Contains(buf.String(), "既に最新です") {
		t.Errorf("出力に「既に最新です」が含まれていない: %s", buf.String())
	}
}

func TestRunUpdateCheckOnlyReportsAvailable(t *testing.T) {
	withUpdateTestServer(t, "v2.0.0", "v1.0.0", "")

	var buf bytes.Buffer
	if err := runUpdate(&buf, true, ""); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "対象のバージョン: v2.0.0") {
		t.Errorf("出力に対象バージョンが含まれていない: %s", out)
	}
	if !strings.Contains(out, "更新があります") {
		t.Errorf("出力に更新ありの案内が含まれていない: %s", out)
	}
}

func TestRunUpdateInstallsNewBinary(t *testing.T) {
	_, exePath := withUpdateTestServer(t, "v2.0.0", "v1.0.0", "new binary content")

	var buf bytes.Buffer
	if err := runUpdate(&buf, false, ""); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !strings.Contains(buf.String(), "v2.0.0 に更新しました") {
		t.Errorf("出力に完了メッセージが含まれていない: %s", buf.String())
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(got) != "new binary content" {
		t.Errorf("バイナリが差し替わっていない: %q", got)
	}
}

func TestRunUpdatePinnedVersionAlwaysReinstalls(t *testing.T) {
	// pinned 指定時は、現在と同じバージョンでも常に取得し直す
	// （latest 経由のときだけ「既に最新」でスキップする）。
	_, exePath := withUpdateTestServer(t, "v1.0.0", "v1.0.0", "pinned binary content")

	var buf bytes.Buffer
	if err := runUpdate(&buf, false, "1.0.0"); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if strings.Contains(buf.String(), "既に最新です") {
		t.Errorf("--version 指定時は「既に最新」でスキップしてはいけない: %s", buf.String())
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(got) != "pinned binary content" {
		t.Errorf("バイナリが差し替わっていない: %q", got)
	}
}

func TestNormalizeTag(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"v1.2.3": "v1.2.3",
		"1.2.3":  "v1.2.3",
	}
	for in, want := range cases {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}
