package cli

import (
	"fmt"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/version"
)

// buildVersion は NewRootCommand で渡されたバージョンを保持する。
// required_version（internal/config.Config.RequiredVersion）の判定に使う
// （check / doctor コマンドから参照する）。
var buildVersion = "dev"

// checkRequiredVersion は cfg.RequiredVersion を手元の spotter が満たしているかを
// 確認する。満たしていなければ検査を実行させないため、check / doctor コマンドが
// config.Load の直後に呼ぶ。
func checkRequiredVersion(cfg *config.Config) error {
	ok, err := version.Satisfies(buildVersion, cfg.RequiredVersion)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("spotter のバージョン %s は required_version %s を満たしていません。バイナリを更新してください", buildVersion, cfg.RequiredVersion)
	}
	return nil
}
