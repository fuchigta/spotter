#!/usr/bin/env bash
# tools/go.mod の tool ディレクティブで固定した開発用ツールを、カレントディレクトリ
# （リポジトリのルート）のまま実行する。
#
# go tool は go.mod のあるディレクトリで動かす必要があるが、lint などは本体のモジュールを
# 対象にするため、カレントディレクトリは変えたくない。そこで go tool -n でビルド済みの
# バイナリのパスだけを受け取り、ここから実行する。バージョンは tools/go.mod で固定される
# ため、手元と CI で同じものが動く。
#
# 使い方: bash scripts/tool.sh <ツール名> [引数...]
set -eu

if [ $# -lt 1 ]; then
  echo "使い方: bash scripts/tool.sh <ツール名> [引数...]" >&2
  exit 2
fi

name="$1"
shift

tools_dir="$(cd "$(dirname "$0")/../tools" && pwd)"
bin=$(go -C "$tools_dir" tool -n "$name")
exec "$bin" "$@"
