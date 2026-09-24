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

## マージコミットは検査しない

`git merge --no-ff` や `git pull` でマージが発生すると、コンフリクトが無くても
`commit-msg` フックは走ります。この時点で `MERGE_HEAD` が存在するコミットは、
（コンフリクト解消のためにその場で書き足した内容も含めて）検査せず成功終了します。
これは CI 側（[ci-integration.md](ci-integration.md)、`spotter range`）がマージコミット
自体を比較範囲から除外しているのと同じ扱いで、取り込む側のブランチに積まれていた各
コミットはマージされる前に手元・CI どちらでもそれぞれ検査済みだからです
（詳しくは [granularity.md](granularity.md) 参照）。

**限界**: コンフリクト解消のためにマージコミット上で書き足した内容自体は、手元でも
CI でも検査されません。マージがシークレットの混入や大きな不正な変更を持ち込む経路には
ならないという前提に立っています。

## push する前に CI と同じ range 検査を走らせる（`spotter check --pre-push`）

```
spotter check --pre-push <remote>
```

pre-push フックから `spotter check --pre-push "$1"` として呼びます。`$1` は git が
pre-push フックに渡す remote 名（URL のこともあります）で、範囲の計算には使わず、
検査に失敗したときの案内にだけ使います。`--message` / `--range` とは排他です。

push しようとしている内容は引数からは渡らないため、pre-push フックの標準入力
（`<local ref> <local sha> <remote ref> <remote sha>` の行が ref ごとに 1 行）を
`cmd.InOrStdin()` から読んで範囲を決めます。upstream の推測はせず、標準入力に現れた
ref だけを見ます。

### pre-push が検査する範囲（`internal/prepush`）

ref ごとに次の規則で範囲式を決めます（`internal/prepush` の `PlanRef`）。

- 範囲式は `<local> --not --remotes` を基本とし、remote sha が全 0 でなくローカルに
  コミットとして実在するとき（＝まだどのリモート追跡ブランチにも取り込まれていない
  コミット）だけ、追加の除外として remote sha を付け足します
  （`<local> --not --remotes <remote sha>`）。`--remotes` は既知の全リモート追跡ブランチを
  除外の起点にする git 標準のショートカットで、pre-push の時点ではこの push 個別の
  remote sha がまだ追跡ブランチに反映されていないことがあるため、確実に除外できるよう
  別立てで付け足します
- 削除 push（local sha が全 0）は検査しません
- local sha が commit に peel できない場合（tree だけを指す tag など）は検査しません
- peel した local が現在の HEAD と異なる場合は検査しません。設定・worktree 粒度の検査や
  command 型のスクリプトは作業ツリー由来で、HEAD 以外の状態を検査しても再現できない
  ためです。tag の push はここに該当することがあります
- 複数 ref を同時に push した場合、ref ごとに個別の範囲で検査します（squashed 粒度の
  検査も ref をまたいでまとめません）。同じ範囲式になる ref（同じコミットを複数の ref に
  push する場合など）は範囲検査を重複して走らせません。一方 worktree 粒度の検査は、
  検査対象の ref が 1 つも無い場合も含めて呼び出し 1 回につき 1 回だけ実行します
  （`--range` 単独実行時の挙動と揃えるためです）

検査しない ref があっても不合格にはせず、その理由を必ず stderr に 1 行出します
（[docs/principles.md](principles.md) 約束 3。警告という中間の段階を作らないための
割り切りです）。設定の読み込みと `required_version` の確認は、`--pre-push` の呼び出し
1 回につき 1 回だけ行います。

### 失敗したとき

各検査が違反を表示した後、続けて次を出します。

- CI（`spotter range` + `--range`）でも同じ結果になること
- 検査した ref とその範囲式
- その範囲で `--config` のファイル（既定 `.spotter.yml`）を変更したコミットの一覧
  （検査を足したコミットがどれかを特定しやすくするため）
- 対処の案内（[docs/ci-integration.md](ci-integration.md) の「対処」を参照）

### `--no-verify` で飛ばせる

commit-msg フックと同様、`git push --no-verify` で pre-push フックをスキップできます。
最後に検査するのは CI（`spotter range` + `--range`）なので、約束1「手元で通ったものは
CI でも通る」には影響しません。push する前に気づけるようにするための位置づけです。

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
