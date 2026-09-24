#!/usr/bin/env bash
# tools/<ツール名>/go.mod の tool ディレクティブで固定した開発用ツールを、カレント
# ディレクトリ（リポジトリのルート）のまま実行する。
#
# ツールごとにモジュールを分けるのは、1 つの go.mod にまとめると依存の解決（MVS）が
# ツール間で混ざり、あるツールが上げた依存で別のツールがビルドできなくなるため。
#
# go tool は go.mod のあるディレクトリで動かす必要があるが、lint などは本体のモジュールを
# 対象にするため、カレントディレクトリは変えたくない。そこで go tool -n でビルド済みの
# バイナリのパスだけを受け取り、ここから実行する。バージョンは tools/ の go.mod で固定される
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

tool_dir="$(cd "$(dirname "$0")/../tools" && pwd)/$name"
if [ ! -f "$tool_dir/go.mod" ]; then
  echo "scripts/tool.sh: tools/$name/go.mod がありません" >&2
  exit 2
fi
bin=$(go -C "$tool_dir" tool -n "$name")
exec "$bin" "$@"
