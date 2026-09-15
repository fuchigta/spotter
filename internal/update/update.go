// Package update は spotter バイナリ自身を GitHub Releases の配布物に置き換える
// （`spotter update` の実体）。
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultRepoURL は spotter 自身のリポジトリ。GitHub Releases の固定 URL
// （/releases/latest/download/<asset>）を組み立てる基点になる。
const DefaultRepoURL = "https://github.com/fuchigta/spotter"

// supportedPlatforms は .github/workflows/release.yml のビルドマトリクスと同じ
// 組み合わせだけを許す。ここに無い GOOS/GOARCH は配布バイナリが存在しない。
var supportedPlatforms = map[string]bool{
	"linux/amd64":   true,
	"linux/arm64":   true,
	"darwin/amd64":  true,
	"darwin/arm64":  true,
	"windows/amd64": true,
}

// AssetName は goos/goarch から release.yml と同じ命名規則
// （spotter-<goos>-<goarch>[.exe]）でアセット名を組み立てる。
// 対応していない組み合わせにはエラーを返す。
func AssetName(goos, goarch string) (string, error) {
	if !supportedPlatforms[goos+"/"+goarch] {
		return "", fmt.Errorf("update: %s/%s 向けの配布バイナリはありません", goos, goarch)
	}
	name := fmt.Sprintf("spotter-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

var tagPattern = regexp.MustCompile(`/releases/tag/([^/?#]+)`)

// LatestTag は repoURL + "/releases/latest" へのリダイレクト先から最新タグ名を
// 取得する。
//
// GitHub API（/repos/.../releases/latest）を使わないのは、認証無しだと
// レート制限（60 req/h）にすぐ達するため。代わりに GitHub が
// `/releases/latest` を実タグの `/releases/tag/<tag>` へ 302 リダイレクトする
// 挙動を利用し、リダイレクトを追わず Location ヘッダだけを読む。
func LatestTag(client *http.Client, repoURL string) (string, error) {
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := c.Get(repoURL + "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("update: 最新バージョンの取得に失敗しました: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("update: 最新バージョンの取得に失敗しました（%s: status %d）", repoURL, resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	m := tagPattern.FindStringSubmatch(loc)
	if m == nil {
		return "", fmt.Errorf("update: リダイレクト先からバージョンを特定できませんでした（Location: %q）", loc)
	}
	return m[1], nil
}

// DownloadURL は tag（空文字なら latest）とアセット名から配布 URL を組み立てる。
// tag が空のときはタグ名を経由しない固定 URL
// （/releases/latest/download/<asset>）を使う（release.yml 参照。アセット名に
// バージョンを含めていないのはこの URL を使うため）。
func DownloadURL(repoURL, tag, assetName string) string {
	if tag == "" {
		return repoURL + "/releases/latest/download/" + assetName
	}
	return repoURL + "/releases/download/" + tag + "/" + assetName
}

// Fetch は url の中身を丸ごと取得する。バイナリ本体・sha256 ファイルの両方に使う。
func Fetch(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("update: %s の取得に失敗しました: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %s の取得に失敗しました（status %d）", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("update: %s の読み込みに失敗しました: %w", url, err)
	}
	return data, nil
}

// VerifyChecksum は binary の SHA-256 が shaFile（release.yml が `sha256sum` で
// 生成する "<hex>  <filename>" 形式）の内容と一致するか確認する。
func VerifyChecksum(binary, shaFile []byte) error {
	fields := strings.Fields(string(shaFile))
	if len(fields) == 0 {
		return fmt.Errorf("update: sha256 ファイルが空です")
	}
	want := strings.ToLower(fields[0])

	sum := sha256.Sum256(binary)
	got := hex.EncodeToString(sum[:])

	if got != want {
		return fmt.Errorf("update: チェックサムが一致しません（期待: %s, 実際: %s）", want, got)
	}
	return nil
}

// Install は execPath（実行中の spotter バイナリ）を binary の内容に置き換える。
//
// 実行中の自分自身を直接上書きすると、書き込み中の不完全な内容を OS が
// 実行してしまう。そこで同じディレクトリに書いた一時ファイルへ 2 段階の
// rename（旧バイナリを退避 → 新バイナリを配置）で差し替える。rename は
// 実行中のファイルに対しても許される操作（Unix は元々問題無く、Windows も
// 実行イメージは delete 共有付きで開かれているため rename/delete できる）
// なので、自分自身の実行を止めずに済む。
func Install(execPath string, binary []byte) (err error) {
	dir := filepath.Dir(execPath)

	tmp, err := os.CreateTemp(dir, ".spotter-update-*")
	if err != nil {
		return fmt.Errorf("update: 一時ファイルの作成に失敗しました: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	if _, err = tmp.Write(binary); err != nil {
		tmp.Close()
		return fmt.Errorf("update: 一時ファイルへの書き込みに失敗しました: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("update: 一時ファイルのクローズに失敗しました: %w", err)
	}
	if err = os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("update: 実行権限の付与に失敗しました: %w", err)
	}

	old := execPath + ".old"
	os.Remove(old) // 前回の更新の残骸があっても無視して上書きする

	if err = os.Rename(execPath, old); err != nil {
		return fmt.Errorf("update: 既存バイナリの退避に失敗しました: %w", err)
	}

	if err = os.Rename(tmpPath, execPath); err != nil {
		// 差し替えに失敗したら退避したものを戻す（可能な範囲でのロールバック）。
		os.Rename(old, execPath)
		return fmt.Errorf("update: 新しいバイナリの配置に失敗しました: %w", err)
	}

	// 実行中のプロセスがまだ握っている可能性がある（特に Windows）ため、
	// 削除に失敗しても無視する。残った場合は次回の update 実行時に上書きされる。
	os.Remove(old)
	return nil
}
