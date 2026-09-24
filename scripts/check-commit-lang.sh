#!/usr/bin/env bash
# コミットメッセージの件名（type(scope): の後ろ）と本文が日本語で書かれているかを調べる。
# spotter の command 型検査（.spotter.yml の checks.commit-lang）から呼ばれる。
#
# 件名が Conventional Commits の形でなければ何も言わない（体裁は commit-subject が
# 報告するため、同じ問題を 2 回報告しない）。本文は、トレーラ（"Key: value" の行）を
# 除いた残りに日本語が 1 文字も無いときだけ違反にする。英語の識別子やパスが混ざるのは
# 構わないため、行ごとには見ない。
#
# 日本語かどうかは UTF-8 の先頭バイトで判定する（ひらがな・カタカナ・全角記号・漢字は
# 0xE3〜0xE9 で始まる）。grep -P（PCRE）は BSD grep に無く、手元の OS で結果が変わるため
# 使わない。
set -eu

message_file=""

while [ $# -gt 0 ]; do
  case "$1" in
    --message-file) message_file="$2"; shift 2 ;;
    --mode|--from|--to|--options-file) shift 2 ;;
    *) shift ;;
  esac
done

if [ -z "$message_file" ] || [ ! -s "$message_file" ]; then
  exit 0
fi

# git commit -v の差分プレビュー（scissors 行以降）とコメント行を落とす。
message=$(sed -e '/^# -\{24\} >8 -\{24\}$/,$d' -e '/^#/d' "$message_file")

subject=$(printf '%s\n' "$message" | sed -n '/[^[:space:]]/{p;q;}')
description=$(printf '%s\n' "$subject" | sed -n -E 's/^[a-z]+(\([^)]*\))?!?: (.+)$/\2/p')
if [ -z "$description" ]; then
  exit 0
fi

has_japanese() {
  LC_ALL=C grep -q $'[\xe3-\xe9]'
}

found=""

if ! printf '%s\n' "$description" | has_japanese; then
  found="$found
  件名: $subject"
fi

body=$(printf '%s\n' "$message" \
  | sed -n '/[^[:space:]]/,$p' \
  | sed '1d' \
  | grep -vE '^[A-Za-z][A-Za-z0-9-]*: ' \
  | grep -vE '^[[:space:]]*$' || true)
if [ -n "$body" ] && ! printf '%s\n' "$body" | has_japanese; then
  found="$found
  本文に日本語がありません"
fi

if [ -n "$found" ]; then
  echo "コミットメッセージの件名・本文は日本語で書いてください:$found" >&2
  exit 1
fi
exit 0
