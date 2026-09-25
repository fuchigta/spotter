package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// ErrCheckFailed は「実行はできたが 1 件以上の検査に違反があった」ことを表すセンチネル。
// main はこれと他のエラー（設定不備・git 実行失敗など）を区別して終了コードとメッセージを
// 出し分ける。
var ErrCheckFailed = errors.New("1 件以上の検査に失敗しました")

// version は cmd/spotter/main.go が internal/version.Resolve で解決したビルドバージョン
// （-ldflags -X による埋め込みが無く、ビルド情報からも解決できない場合は "dev"）。
func NewRootCommand(version string) *cobra.Command {
	buildVersion = version

	root := &cobra.Command{
		Use:           "spotter",
		Short:         "コミット前後の検査を実行するツール",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newCheckCommand())
	root.AddCommand(newChecksCommand())
	root.AddCommand(newConfigCommand())
	root.AddCommand(newRangeCommand())
	root.AddCommand(newHooksCommand())
	root.AddCommand(newSkillsCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newUpdateCommand())
	return root
}

func Execute(v string) error {
	return NewRootCommand(v).Execute()
}
