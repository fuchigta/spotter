#!/usr/bin/env bash
# ミューテーションテスト（gremlins）を、比較元との差分に乗ったミュータントだけに対して
# 走らせ、生き残りの数でゲートする。CI の mutation ワークフローと手元で同じものを実行する。
#
# 使い方:
#   bash scripts/mutation.sh origin/main   # 差分実行（CI と同じ）
#   bash scripts/mutation.sh --all         # 対象パッケージ全体（ベースラインの計測用）
#
# gremlins v0.6.0 の --diff は、実際の挙動で次の癖があるため、それぞれ回避している。
# - 差分が空だと全行を変更扱いにして全件実行になる。対象パッケージの非テスト .go に差分が
#   無ければ gremlins を呼ばずに終える
# - ミュータントのファイル名は「gremlins に渡したパスからの相対パス」で git diff の
#   リポジトリルート相対パスと照合されるため、パッケージを直接渡すと全件 SKIPPED になる。
#   モジュールルート（.）を渡し、対象外のディレクトリを --exclude-files で外す
# - hunk の変更範囲を「先頭の追加行から追加行数ぶん」とみなすため、前後 3 行の既定の
#   コンテキストでは 1 つの hunk に変更の塊が 2 つあると 2 つ目を取りこぼす。diff.context=0 を
#   環境変数経由の git 設定で渡し、変更の塊ごとに hunk を分ける
# - 各ミュータントのタイムアウトは「最初のカバレッジ計測の所要時間 × 係数（既定 3）」で、
#   テストのキャッシュが効くと所要時間が 1 秒ほどに縮み、遅いテストを持つパッケージを
#   対象に足したときに正常なテストまで TIMED OUT になる。TIMED OUT は生き残りに数えられず
#   生き残りを隠すので、GOFLAGS=-count=1 でキャッシュを使わせない
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

# 純粋なロジックでテストが厚いパッケージに絞る。足すときは --all で測り直し、上限も見直す。
targets=(internal/check/diffutil internal/check/docutil)

# 生き残り（LIVED + NOT COVERED）の上限。NOT COVERED もテストが検出しないミュータントで、
# 数えないとテストの無いコードを足しても通ってしまう。
#
# ベースライン（gremlins v0.6.0・Go 1.27.1・4 コア、--all）:
#   internal/check/diffutil: KILLED 7 / LIVED 2 / NOT COVERED 0
#   internal/check/docutil:  KILLED 17 / LIVED 2 / NOT COVERED 0
#   合計 KILLED 24 / 生き残り 4 / TIMED OUT 0（efficacy 85.71%）
# 生き残り 4 件はどれも等価ではなく、テストが踏んでいない入力によるもの:
#   - diffutil.go ParseLines の newLine++ と oldLine++ の ++ → --（連続する追加行・削除行の
#     2 行目以降の行番号を確かめていない）
#   - docutil.go のフェンス内の len(trimmed) > 0 → >= 0（フェンス内の空行を通していない。
#     変異後は添字の範囲外で panic する）
#   - docutil.go ExistsOrGlob の len(matches) > 0 → >= 0（一致しない glob を通していない）
# gremlins の除外はファイル単位と変異の種類単位しかなく、どちらも検出力ごと落とすため使わない。
# 差分実行での最悪（対象の全行に差分がある）は上の 4 で、上限は 4 に置くラチェットとし、
# テストを足して実測が下がったら同じコミットで引き下げる。
max_surviving=4

if [ $# -ne 1 ]; then
  echo "使い方: bash scripts/mutation.sh <比較元の ref> | --all" >&2
  exit 2
fi
base="$1"

diff_args=()
if [ "$base" != "--all" ]; then
  changed=$(git diff --name-only --diff-filter=AMR --merge-base "$base" -- "${targets[@]}" \
    | grep -E '\.go$' | grep -v -E '_test\.go$' || true)
  if [ -z "$changed" ]; then
    echo "scripts/mutation.sh: 対象（${targets[*]}）の非テスト .go に $base からの差分が無いため省略します"
    exit 0
  fi
  echo "差分のあるファイル:"
  echo "$changed" | sed 's/^/  /'
  diff_args=(--diff "$base")
fi

exclude_args=()
while IFS= read -r dir; do
  for t in "${targets[@]}"; do
    if [ "$dir" = "$t" ]; then
      continue 2
    fi
  done
  exclude_args+=(--exclude-files "^${dir//./\\.}/[^/]+\$")
done < <(git ls-files '*.go' | grep -v -E '_test\.go$' | xargs -n1 dirname | sort -u)

report="$(mktemp)"
trap 'rm -f "$report"' EXIT

GOFLAGS=-count=1 \
  GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=diff.context GIT_CONFIG_VALUE_0=0 \
  bash scripts/tool.sh gremlins unleash \
  --output-statuses lct \
  --output "$report" \
  ${diff_args[@]+"${diff_args[@]}"} "${exclude_args[@]}" .

killed=$(jq '.mutants_killed' "$report")
lived=$(jq '.mutants_lived' "$report")
not_covered=$(jq '.mutants_not_covered' "$report")
timed_out=$(jq '[.files[].mutations[] | select(.status == "TIMED OUT")] | length' "$report")
surviving=$((lived + not_covered))

echo
echo "KILLED $killed / LIVED $lived / NOT COVERED $not_covered / TIMED OUT $timed_out"
echo "生き残り（LIVED + NOT COVERED）: $surviving（上限 $max_surviving）"
if [ "$timed_out" -gt 0 ]; then
  echo "TIMED OUT は生き残りに数えない。無限ループ以外で出ているならタイムアウトが短すぎるので --timeout-coefficient で係数を上げる"
fi
if [ "$surviving" -gt "$max_surviving" ]; then
  echo "scripts/mutation.sh: 生き残りが上限を超えています。上の LIVED / NOT COVERED を殺すテストを足してください" >&2
  exit 1
fi
