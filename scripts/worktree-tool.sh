#!/usr/bin/env bash
# tools/ で固定した開発用ツールを、spotter の command 型検査（worktree 粒度）として
# 実行する。
#
# spotter は .spotter.yml の args の後ろに --mode などを付け足して呼ぶが、ツールは
# それを知らない。そこで args の "--" までをツールの引数とし、それより後ろは捨てる。
# spotter は違反の説明に stderr だけを使うため、ツールの stdout も stderr に回す。
#
# 使い方（.spotter.yml の args）: [scripts/worktree-tool.sh, <ツール名>, <引数>..., --]
set -eu

tool_args=()
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
  tool_args+=("$1")
  shift
done

if [ ${#tool_args[@]} -eq 0 ]; then
  echo "scripts/worktree-tool.sh: ツール名がありません" >&2
  exit 2
fi

exec bash "$(dirname "$0")/tool.sh" "${tool_args[@]}" 1>&2
