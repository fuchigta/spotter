#!/usr/bin/env bash
# CONTEXT.md で用語に添えた識別子（**用語**（`Identifier`））が、Go のコードで宣言されて
# いるかを調べる。識別子を改名したのに CONTEXT.md が古いまま残るのを防ぐ。
# spotter の command 型検査（.spotter.yml の checks.context-identifiers、worktree 粒度）
# から呼ばれ、カレントディレクトリ（リポジトリのルート）以下を直接読む。
#
# 単語として現れるかではなく宣言を探す。"FS" のような短い識別子は fs.FS などにも
# 現れるため、出現だけでは改名に気付けない。対象は型・関数・メソッドの宣言と、
# 構造体のフィールドやインターフェースのメソッドの行。
set -eu

context_file="CONTEXT.md"

identifiers=$(grep -oE '\*\*[^*]+\*\*（`[A-Za-z_][A-Za-z0-9_]*`）' "$context_file" \
  | sed -E 's/.*（`([A-Za-z0-9_]+)`）/\1/' \
  | sort -u)

go_files=$(find internal cmd -name '*.go' ! -name '*_test.go' | sort)

missing=""
for id in $identifiers; do
  pattern="^type ${id}([^A-Za-z0-9_]|$)|^func (\([^)]*\) )?${id}\(|^[[:space:]]+${id}([[:space:]]|\()"
  if ! grep -qE "$pattern" $go_files; then
    missing="$missing
  $id"
  fi
done

if [ -n "$missing" ]; then
  echo "CONTEXT.md に書いた識別子が Go のコードで宣言されていません（改名したなら CONTEXT.md も直す）:$missing" >&2
  exit 1
fi
exit 0
