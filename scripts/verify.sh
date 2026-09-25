#!/usr/bin/env bash
# push する前に .github/workflows/ci.yml のジョブのうち手元で再現できるものをまとめて
# 走らせる（race detector は cgo を要し遅いため対象外）。CI で落ちて気づくのではなく、
# push する前に同じ検査を手元で再現するためのもの。
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

echo "==> gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt で整形されていないファイルがあります:"
  echo "$unformatted"
  exit 1
fi

echo "==> go mod tidy"
go mod tidy
for d in tools/*/; do GOTOOLCHAIN=auto go -C "$d" mod tidy; done
if ! git diff --exit-code -- go.mod go.sum tools; then
  echo "go mod tidy による差分があります。ローカルで go mod tidy と tools/ 以下の各ディレクトリで go mod tidy を実行してコミットしてください。"
  exit 1
fi

echo "==> go vet"
go vet ./...

echo "==> go build"
go build ./...

echo "==> go test"
go test ./...

echo "==> golangci-lint"
bash scripts/tool.sh golangci-lint run ./...

echo "==> coverage"
bash scripts/coverage.sh

echo "==> verify.sh: OK"
