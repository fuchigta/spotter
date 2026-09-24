#!/usr/bin/env bash
# issue 番号への参照（"#<番号>" 形式。マークダウンの見出しは "#" の直後に空白が
# 入るため誤検知しない）が追加された変更に含まれていないかを調べる。
# spotter の command 型検査（.spotter.yml の checks.check-issue-refs、
# command: bash で明示的に bash 上で実行される）から呼ばれる。
set -eu

mode=""
from=""
to=""

while [ $# -gt 0 ]; do
  case "$1" in
    --mode)
      mode="$2"
      shift 2
      ;;
    --from)
      from="$2"
      shift 2
      ;;
    --to)
      to="$2"
      shift 2
      ;;
    --message-file) shift 2 ;;
    --options-file) shift 2 ;;
    *) shift ;;
  esac
done

case "$mode" in
  staged)
    diff_cmd=(git diff --cached)
    ;;
  range)
    diff_cmd=(git diff "$from" "$to")
    ;;
  *)
    echo "check-issue-refs: 未対応の mode です: $mode" >&2
    exit 1
    ;;
esac

found=""

# -z（NUL 区切り）で読むことで、空白を含むファイル名でも安全に処理する。
# プロセス置換（bash 限定）で while ループを現在のシェルで実行し、found の
# 更新がサブシェルに閉じ込められて消えないようにする。
while IFS= read -r -d '' f; do
  hits=$("${diff_cmd[@]}" -U0 -- "$f" \
    | grep -E '^\+' \
    | grep -vE '^\+\+\+ ' \
    | grep -oE '#[0-9]+' \
    | sort -u \
    | tr '\n' ' ' || true)
  if [ -n "$hits" ]; then
    found="$found
  $f: $hits"
  fi
done < <("${diff_cmd[@]}" --name-only --diff-filter=ACMR -z)

if [ -n "$found" ]; then
  echo "issue 番号への参照が追加されています（コード・ドキュメントには残さない方針）:" >&2
  echo "$found" >&2
  exit 1
fi
exit 0
