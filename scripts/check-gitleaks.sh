#!/usr/bin/env bash
# 追加された変更にシークレットが含まれていないかを gitleaks で調べる。
# spotter の command 型検査（.spotter.yml の checks.gitleaks、per-commit 粒度）から
# 呼ばれる。後から消しても履歴に残るため、コミットごとに見る。
#
# range モードでは --to のコミット 1 つだけを見る（-1 <to>）。--from は親だが、
# 最初のコミットでは空の tree になり git log の範囲に使えないため使わない。
set -eu

mode=""
to=""

while [ $# -gt 0 ]; do
  case "$1" in
    --mode)
      mode="$2"
      shift 2
      ;;
    --to)
      to="$2"
      shift 2
      ;;
    --from | --message-file | --options-file) shift 2 ;;
    *) shift ;;
  esac
done

case "$mode" in
  staged)
    set -- git --pre-commit --staged
    ;;
  range)
    set -- git --log-opts="-1 $to"
    ;;
  *)
    echo "check-gitleaks: 未対応の mode です: $mode" >&2
    exit 1
    ;;
esac

exec bash "$(dirname "$0")/tool.sh" gitleaks "$@" --no-banner --no-color --redact --verbose --log-level warn . 1>&2
