# 他のフックランナーとの共存（lefthook / husky / pre-commit）

[hooks.md](hooks.md) の「`core.hooksPath` が既に設定されている場合」「既定の hooks
ディレクトリに spotter 以外の既存フックがある場合」のとおり、`spotter hooks install` は
他のフックランナーが管理するフックファイルを尊重します。ただし、フックランナーによっては
**追記した管理ブロックが実行されない**、または**追記が既存ランナーの結果を覆い隠す**ことが
あります。ここでは lefthook・husky v9・pre-commit（Python 版）それぞれについて、実機で
確かめた上でのレシピと注意点をまとめます。

いずれのツールでも、呼び出し行そのものは `spotter hooks install --print` で確認できます。

```bash
spotter hooks install --print
# commit-msg: spotter check --message "$1"
# pre-push: spotter check --pre-push "$1"
```

## lefthook

`pre-push` フックそのものは lefthook が管理し、`spotter hooks install` は使わずに
`--print` の出力を lefthook の設定にそのまま貼り付けます。lefthook の `run:` はフック引数を
`{1}`（`"$1"` ではありません）で受け取ります。

```yaml
commit-msg:
  commands:
    spotter:
      run: spotter check --message {1}

pre-push:
  commands:
    spotter:
      run: spotter check --pre-push {1}
      use_stdin: true
```

**`use_stdin: true` が必須です。** lefthook は既定では pre-push フックの標準入力をコマンドに
渡しません。付け忘れると `spotter check --pre-push` に ref の並びが 1 行も届かず、
`pre-push が検査する範囲`（[hooks.md](hooks.md)）に書いたとおり検査されずに理由だけが
stderr に出ます。

### `spotter hooks install` を重ねて使わない

lefthook がインストールした `.git/hooks/commit-msg` や `.git/hooks/pre-push` は
`call_lefthook run "<hook>" "$@"` の呼び出しで終わっており、`exec` ではないため
**`spotter hooks install` で追記した管理ブロックも実行はされます。** しかし、この構成には
2 つの問題があります。

- lefthook の設定に既に spotter を組み込んでいる場合、二重に検査が走ります。
- 追記した管理ブロックの結果が、lefthook 自身の結果を**上書き**します。lefthook の呼び出し
  行はスクリプトの途中にあるだけで、その後に続く行（追記した管理ブロック）の終了コードが
  スクリプト全体の終了コードになるためです。lefthook 側の検査が失敗していても、追記した
  spotter の検査だけが通れば push 自体は成功してしまいます（実機で、lefthook 側の検査を
  意図的に失敗させ、追記ブロック側の検査だけを合格させて確認済みです）。

このため lefthook を使う場合は、`spotter hooks install`（管理ブロックの追記・新規作成）は
使わず、上記のように lefthook の設定側に組み込んでください。また `lefthook install` を
再実行するとフックファイルが作り直され、追記した管理ブロックは失われます（lefthook の設定に
組み込んでいれば影響しません）。

## husky v9

husky v9 のフックファイル（`.husky/<hook名>`）は、シェバンや `husky.sh` の読み込みを必要と
しないプレーンなシェルスクリプトです。`--print` の出力をそのままファイルの中身にします。

```bash
echo 'spotter check --message "$1"' > .husky/commit-msg
echo 'spotter check --pre-push "$1"' > .husky/pre-push
```

標準入力・引数は husky の内部スクリプト（`.husky/_/h`）がそのまま引き継ぐため、追加の設定は
不要です。

### `spotter hooks install` を使わない

husky は `core.hooksPath` を `.husky/_` に設定します。`spotter hooks install` はこの値を
尊重してフックファイルを解決しますが、解決先は `.husky/_/<hook名>`（husky が生成する内部の
中継スクリプト）であり、利用者が編集する `.husky/<hook名>` ではありません。`.husky/_/<hook名>`
は中継先のスクリプト（`.husky/<hook名>`）を `sh -e` で実行した後 `exit` するため、
追記した管理ブロックは実行されずに残ります（実機で、`.husky/_/commit-msg` に管理ブロックを
追記したまま不正なコミットメッセージを試し、`.husky/commit-msg` 側の検査結果だけが反映されて
追記ブロックの実行痕跡が無いことを確認済みです）。husky を使う場合は `spotter hooks install`
を使わず、上記のように `.husky/<hook名>` を直接編集してください。

## pre-commit（Python 版）

### commit-msg

commit-msg はメッセージファイルのパスを引数で受け取るだけなので、そのまま local hook に
組み込めます。

```yaml
repos:
  - repo: local
    hooks:
      - id: spotter-commit-msg
        name: spotter (commit-msg)
        language: system
        entry: spotter check --message
        stages: [commit-msg]
```

### pre-push の限界

pre-commit の `hook_impl` は pre-push フックの標準入力を自分で読み切り、各フックには渡さず
`PRE_COMMIT_FROM_REF`（remote sha）・`PRE_COMMIT_TO_REF`（local sha）・
`PRE_COMMIT_LOCAL_BRANCH`・`PRE_COMMIT_REMOTE_BRANCH`・`PRE_COMMIT_REMOTE_NAME` という環境変数に
変換します。これらから `<local ref> <local sha> <remote ref> <remote sha>` の 1 行を組み立てて
`spotter check --pre-push` に渡すことはでき、単一 ref の通常の push（既存ブランチの更新・
共有履歴のある新規ブランチ）では実機で正しく動作することを確認しています。

```yaml
      - id: spotter-pre-push
        name: spotter (pre-push)
        language: system
        entry: sh -c 'printf "%s %s %s %s\n" "$PRE_COMMIT_LOCAL_BRANCH" "$PRE_COMMIT_TO_REF" "$PRE_COMMIT_REMOTE_BRANCH" "$PRE_COMMIT_FROM_REF" | spotter check --pre-push "$PRE_COMMIT_REMOTE_NAME"'
        stages: [pre-push]
        pass_filenames: false
        always_run: true
```

ただし実機で次の 2 つの限界を確認しており、この組み方は推奨しません。

- **複数 ref を同時に push すると、検査されるのは 1 ref だけです。** `git push origin
  main feature` のように複数 ref を同時に push しても、pre-commit は pre-push フックを
  1 回しか呼ばず、環境変数は最後に処理された 1 ref 分の値のままです。他の ref は検査されずに
  push されます（`spotter check --pre-push` 自身は複数 ref を渡されれば ref ごとに検査します
  が、そもそも pre-commit からその ref の情報が渡ってきません）。
- **共有履歴の無い新規ブランチ（orphan ブランチ）では `PRE_COMMIT_TO_REF` /
  `PRE_COMMIT_FROM_REF` が空になります。** 組み立てた行が `<local ref>  <remote ref> `
  のような不正な形になり、`spotter check --pre-push` はこれを解釈できずにエラーで
  push を止めます（黙って素通りするわけではありませんが、意図と異なるエラーで止まります）。

### 推奨: pre-push は pre-commit に持たせない

pre-commit は `core.hooksPath` を設定せず、`--hook-type` で指定したフックだけを
`.git/hooks/` に書きます。commit-msg だけを pre-commit にインストールし（`pre-commit
install --hook-type commit-msg`）、pre-push は `spotter hooks install --hook pre-push` で
別に設置すると、既定の hooks ディレクトリに pre-commit 以外の既存フックが無い状態のまま
`.git/hooks/pre-push` に spotter 用のフックが新規作成されます。この構成なら pre-push フックは
git が渡す標準入力をそのまま受け取るため、複数 ref の同時 push も orphan ブランチも
`spotter check --pre-push` 自身の規則（[hooks.md](hooks.md) の「pre-push が検査する範囲」）
どおりに扱われることを実機で確認しています。

**pre-commit で `pre-push` フックを既にインストールしている場合はこの手順を使わないで
ください。** pre-commit が生成する `.git/hooks/pre-push`・`.git/hooks/commit-msg` は
`exec "$INSTALL_PYTHON" -mpre_commit ...`（または `exec pre-commit ...`）で終わっており、
`spotter hooks install` が追記する管理ブロックはこの `exec` より後ろに置かれるため
**実行されません**（実機で、pre-commit 側に pre-push の設定を残さずビッグファイルの
コミットを push し、追記した管理ブロックの検査結果が一切出ないまま push が成功することを
確認済みです）。
