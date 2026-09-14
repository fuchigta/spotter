# フックの設置（`spotter hooks install`）

```
spotter hooks install [--print] [--hooks-dir <dir>]
```

`commit-msg` フックから `spotter check --message "$1"` を呼ぶように設置します。

## 挙動

### `core.hooksPath` が未設定の場合

`--hooks-dir`（既定 `.githooks`）にディレクトリを作り、`core.hooksPath` をそこに設定した
上で、新規に `commit-msg` フックを作成します。

### `core.hooksPath` が既に設定されている場合

**それを尊重します。** 新しいディレクトリに決め打ちで差し替えたりはしません。既存の
`commit-msg` フックが無ければ新規作成、既にあれば管理ブロックを追記します（lefthook や
husky 相当の既存フックランナーと共存させるため）。

### べき等性

一度設置した後にもう一度 `spotter hooks install` を実行しても、既に spotter の管理ブロックが
入っていれば何もしません（`already` として報告するだけ）。管理ブロックは次のマーカーで
識別されます。

```sh
# --- spotter (managed) begin ---
...
# --- spotter (managed) end ---
```

このマーカーさえ残っていれば、内側の中身を手で書き換えても「設置済み」として扱われ、
`spotter hooks install` の再実行で上書きされることはありません（このリポジトリ自身が
`go run ./cmd/spotter check ...` に書き換えて使っている実例です。後述）。

## `--print`: 呼び出し行だけを出力する

```bash
spotter hooks install --print
# spotter check --message "$1"
```

何も変更せず、呼び出し行を 1 行出力するだけです。lefthook などの既存フックランナーの
設定に、この 1 行だけ貼り付けたい場合に使います。

## 設置状況の確認（`spotter doctor`）

```
spotter doctor
```

設定済みの検査一覧（type・granularity）に加えて、`core.hooksPath` の値と `commit-msg`
フックの状態（無し / spotter を呼び出す設定あり / spotter 未設定の既存フックあり）を
表示します。`required_version` を設定していれば、手元のバイナリがそれを満たすかも
ここで分かります（[versioning.md](versioning.md) 参照）。

## 実例: インストール済みバイナリではなく `go run` を使う

`spotter` 自身の開発リポジトリのように、リポジトリの中身そのものが `cmd/spotter` を
持っている場合は、`go install` で取得した（≒ 別バージョンかもしれない）バイナリではなく、
**手元のソースを毎回 `go run` で検証**した方が「今のブランチの spotter」と「フックが
呼ぶ spotter」が常に一致します。`spotter hooks install` が生成した管理ブロックの中身を、
そのまま書き換えれば実現できます。

```sh
#!/bin/sh
# --- spotter (managed) begin ---
if command -v go >/dev/null 2>&1; then
  go run ./cmd/spotter check --message "$1" || exit 1
else
  echo "spotter: go コマンドが見つからないため検査をスキップします" >&2
fi
# --- spotter (managed) end ---
```

git の `commit-msg` フックは**リポジトリのトップレベルで実行される**ことが保証されて
いるため、`go run ./cmd/spotter` のような相対パス指定がそのまま機能します。
