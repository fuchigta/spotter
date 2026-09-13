# CI 連携（`spotter range`）

commit-msg フックはステージ済みの変更しか見られません。フックを回避してマージされた
コミット（`--no-verify`、フック未設置の環境、Web UI でのマージコミットなど）を最後に
拾うのが CI の役割です。CI では「どこからどこまでを見るか」という比較範囲そのものが
自明ではないため、`spotter range` がこれを自動検出します。

## 使い方

```yaml
- run: echo "RANGE=$(spotter range)" >> "$GITHUB_ENV"
- run: spotter check --config .spotter.yml --range "$RANGE"
```

`--provider` で自動検出をスキップして明示することもできます（`github-actions` |
`gitlab-ci`）。

## 自動検出のロジック

対応しているのは GitHub Actions と GitLab CI（セルフホスト含む）だけです。両者とも
本質的には次の 3 パターンに落ちます。

1. **MR/PR イベント** → `base..HEAD`
2. **push イベント** → `before..after`
3. **判定できない・新規ブランチ等** → フォールバック `-1 HEAD`（直近 1 コミットだけを見る）

### GitHub Actions

- 環境変数 `GITHUB_ACTIONS=true` で検出
- `GITHUB_EVENT_NAME` が `pull_request` / `pull_request_target` なら、`GITHUB_EVENT_PATH`
  の JSON から `pull_request.base.sha` を読み、`<base>..HEAD` にする
- それ以外（push 含む）は、同じ JSON の `before` フィールドと `GITHUB_SHA` から
  `<before>..<after>` を組み立てる

### GitLab CI

- 環境変数 `GITLAB_CI=true` で検出
- `CI_PIPELINE_SOURCE=merge_request_event` なら `CI_MERGE_REQUEST_DIFF_BASE_SHA` を使う
- それ以外は `CI_COMMIT_BEFORE_SHA`/`CI_COMMIT_SHA` を使う

### フォールバックが発動する条件

- base/before に相当する SHA が空
- その SHA がリポジトリ内に実在しない（`git cat-file -e` で確認）

後者は**新しいブランチの最初の push** や **force push 直後**で起こります。CI が渡す
「比較元」の SHA が全ゼロ（`0000...`）になったり、shallow clone でその SHA 自体を
取得できていなかったりするためです。この場合は直近 1 コミットだけを見る
`-1 HEAD` にフォールバックします（何も見ないよりは安全側に倒す設計です）。

## 対応外の CI

上記 2 つ以外の CI では自動検出しません（環境変数の組み合わせを増やすほど実機でしか
検証できない分岐が増えるため、意図的にここで線引きしています）。自分で組み立てた
`<from>..<to>` 形式の範囲式を `--range` に渡してください。

## 実践例（このリポジトリの `.github/workflows/ci.yml`）

このリポジトリ自身も spotter を dogfooding しており、`go run` で常に手元のソースを検証
しています（インストール済みバイナリではなく、リポジトリ内の `cmd/spotter` を毎回ビルドして
使う。詳しくは [hooks.md](hooks.md) 参照）。

```yaml
spotter:
  name: spotter check
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v7
      with:
        fetch-depth: 0   # base/before の SHA をたどるためフル履歴が必要

    - uses: actions/setup-go@v7
      with:
        go-version-file: go.mod
        cache: true

    - name: spotter range
      id: range
      run: echo "range=$(go run ./cmd/spotter range)" >> "$GITHUB_OUTPUT"

    - name: spotter check
      run: go run ./cmd/spotter check --range "${{ steps.range.outputs.range }}"
```

`fetch-depth: 0` が無いと、shallow clone によって base/before の SHA が
「実在しない」と判定され、意図せず `-1 HEAD` フォールバックに落ちてしまう点に注意して
ください。
