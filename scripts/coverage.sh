#!/usr/bin/env bash
# パッケージごとのカバレッジを測り、.testcoverage.yml の基準値を下回っていないかを
# go-test-coverage で確かめる。CI の coverage ジョブと手元で同じものを実行する。
#
# カバレッジの文の数え方は Go のバージョンで変わるため、go.mod の go の行と同じ
# バージョンに固定して測る。手元の Go が新しくても、CI と同じ値になる。
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

go_version=$(sed -n 's/^go \([0-9.]*\)$/\1/p' go.mod)
if [ -z "$go_version" ]; then
  echo "scripts/coverage.sh: go.mod から go のバージョンを読み取れません" >&2
  exit 2
fi

GOTOOLCHAIN="go$go_version" go test -coverprofile=cover.out ./...
bash scripts/tool.sh go-test-coverage --config .testcoverage.yml
