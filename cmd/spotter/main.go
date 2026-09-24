// Command spotter はコミット前後の検査を実行する CLI。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/fuchigta/spotter/internal/cli"
	spotterversion "github.com/fuchigta/spotter/internal/version"
)

// version はビルド時に -ldflags "-X main.version=..." で差し替える。
var version = "dev"

func main() {
	buildInfo, _ := debug.ReadBuildInfo()
	resolved := spotterversion.Resolve(version, buildInfo)
	if err := cli.Execute(resolved); err != nil {
		if !errors.Is(err, cli.ErrCheckFailed) {
			fmt.Fprintln(os.Stderr, "spotter:", err)
		}
		os.Exit(1)
	}
}
