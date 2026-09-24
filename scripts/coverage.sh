#!/usr/bin/env bash
# パッケージごとのカバレッジを測り、.testcoverage.yml の基準値を下回っていないかを
# go-test-coverage で確かめる。CI の coverage ジョブと手元で同じものを実行する。
#
# カバレッジの文の数え方は Go のバージョンで変わるため、go.mod の toolchain の行
# （無ければ go の行）と同じバージョンに固定して測る。CI とリリースも同じ版を使うので、
# 手元の Go が違っても CI と同じ値になる。
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

toolchain=$(sed -n 's/^toolchain \(go[0-9.]*\)$/\1/p' go.mod)
if [ -z "$toolchain" ]; then
  toolchain=$(sed -n 's/^go \([0-9.]*\)$/go\1/p' go.mod)
fi
if [ -z "$toolchain" ]; then
  echo "scripts/coverage.sh: go.mod から Go のバージョンを読み取れません" >&2
  exit 2
fi

GOTOOLCHAIN="$toolchain" go test -coverprofile=cover.out ./...
bash scripts/tool.sh go-test-coverage --config .testcoverage.yml
