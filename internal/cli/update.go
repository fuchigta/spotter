package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fuchigta/spotter/internal/update"
	"github.com/fuchigta/spotter/internal/version"
)

// httpTimeout は GitHub Releases への通信のタイムアウト。
const httpTimeout = 30 * time.Second

// executable は os.Executable のラッパー。テストで実行中のテストバイナリ自体を
// 書き換えてしまわないよう、差し替え可能な変数にしている。
var executable = os.Executable

// updateRepoURL はテストで httptest.Server に差し替えられるよう変数にしている
// （既定は spotter 自身のリポジトリ）。
var updateRepoURL = update.DefaultRepoURL

func newUpdateCommand() *cobra.Command {
	var (
		checkOnly     bool
		targetVersion string
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "spotter バイナリを GitHub Releases の版に更新する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd.OutOrStdout(), checkOnly, targetVersion)
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "更新の有無を確認するだけで、実際には更新しない")
	cmd.Flags().StringVar(&targetVersion, "version", "", "指定したバージョン（例: v1.2.3）に切り替える（既定は最新版）")

	return cmd
}

func runUpdate(stdout io.Writer, checkOnly bool, targetVersion string) error {
	assetName, err := update.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	// pinned は DownloadURL にそのまま渡すタグ。空なら「latest」を意味し、
	// タグ名を経由しない固定 URL（/releases/latest/download/<asset>）を使う。
	pinned := normalizeTag(targetVersion)

	client := &http.Client{Timeout: httpTimeout}

	tag := pinned
	if tag == "" {
		tag, err = update.LatestTag(client, updateRepoURL)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(stdout, "現在のバージョン: %s\n", buildVersion)
	fmt.Fprintf(stdout, "対象のバージョン: %s\n", tag)

	// pinned が空（＝latest を求めた）ときだけ「既に満たしていれば何もしない」を
	// 適用する。特定バージョンへの固定・巻き戻しを頼まれたときは、現在と
	// 同じ表示バージョンでも常に取得し直す。
	if pinned == "" {
		if cmp, cmpErr := version.Compare(buildVersion, tag); cmpErr == nil && cmp >= 0 {
			fmt.Fprintln(stdout, "既に最新です")
			return nil
		}
	}

	if checkOnly {
		fmt.Fprintln(stdout, "更新があります（`spotter update` で適用できます）")
		return nil
	}

	binURL := update.DownloadURL(updateRepoURL, pinned, assetName)
	shaURL := binURL + ".sha256"

	fmt.Fprintf(stdout, "%s を取得しています...\n", assetName)
	binary, err := update.Fetch(client, binURL)
	if err != nil {
		return err
	}
	shaFile, err := update.Fetch(client, shaURL)
	if err != nil {
		return err
	}
	if err := update.VerifyChecksum(binary, shaFile); err != nil {
		return err
	}

	exe, err := executable()
	if err != nil {
		return fmt.Errorf("update: 実行中のバイナリのパス取得に失敗しました: %w", err)
	}

	if err := update.Install(exe, binary); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "%s に更新しました: %s\n", tag, exe)
	return nil
}

// normalizeTag は --version に渡された値の先頭に "v" を補う（git のタグは
// 常に "v" 始まりのため、"1.2.3" のような入力もそのまま使えるようにする）。
// 空文字は「未指定（latest を使う）」として素通しする。
func normalizeTag(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
