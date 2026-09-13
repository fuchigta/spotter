# glob パターンの意味論

`pairs[].paths` (doc-sync)、`exclude` (doc-sync)、`deny[].paths` (unwanted-files) などの
「パスパターン」は、**POSIX シェルの `case` パターンと同じ意味論**の独自 glob です。
`.gitignore` の書式や Go 標準の `path.Match`/`filepath.Match` とは挙動が異なるので注意して
ください。

## 最大の違い: `*` は `/` にもマッチする

```
internal/cli/*.go
```

は `internal/cli/check.go` だけでなく、仮にサブディレクトリが挟まっていても、シェルの
`case "$f" in internal/cli/*.go)` と同じ感覚でマッチします。つまり `*` は「1 階層のみ」
ではなく「任意の文字列（`/` を含む）」に一致します。

Go 標準の `filepath.Match("internal/cli/*.go", "internal/cli/sub/check.go")` は `*` が
`/` に一致しないため false になりますが、`spotter` の glob では true になります。
シェルスクリプトからの移行を前提にしているための仕様です。

## 対応表

| 記法 | 意味 |
|---|---|
| `*` | 任意の文字列（`/` を含む、0 文字も可） |
| `?` | 任意の 1 文字 |
| `[abc]` | `a`/`b`/`c` のいずれか 1 文字 |
| `[a-z]` | 範囲指定 |
| `[!abc]` / `[^abc]` | 否定（`a`/`b`/`c` 以外の 1 文字。シェルの `!` と正規表現の `^` の
  両方の書き方に対応） |
| それ以外の文字 | リテラル一致（正規表現の特殊文字も含めてエスケープされる） |

パターン全体は暗黙に先頭 `^`・末尾 `$` で固定されます（部分一致ではなく完全一致）。

## 使われている箇所

- [doc-sync](checks/doc-sync.md): `pairs[].paths`, `exclude`
- [unwanted-files](checks/unwanted-files.md): `deny[].paths`

一方、[doc-paths](checks/doc-paths.md) の `docs` フィールドは Go 標準の `filepath.Glob`
（`*` は `/` に一致しない）を使っています。同じ「glob」という言葉でも検査によって意味論が
違う点に注意してください。迷ったら各検査のページで確認してください。

## `consistency` の `extract`/`line` は別物

[consistency](checks/consistency.md) の `line`/`extract` はこの glob ではなく、**Go の
正規表現（RE2）** です。混同しないよう注意してください。
