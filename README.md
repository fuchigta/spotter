# spotter

**コーディングエージェントが自律的に作業する（＝コミットする）のを、致命的な失敗の直前で支える**
ための CLI です。ジムのスポッター（補助者）の比喩から名付けました。

`git commit` の直前（`commit-msg` フック）と CI の両方から同じ検査を同じ引数で呼べるので、
「手元で通ったものは CI でも通る」が保証されます。組み込みで次の 5 種類の検査を持ちます。

| type | 検査内容 |
|---|---|
| [`doc-sync`](docs/checks/doc-sync.md) | コードとドキュメントの対応。片方だけ変更されていたら失敗する |
| [`unwanted-files`](docs/checks/unwanted-files.md) | コミットしてはいけないもの（データベース・ログ・巨大ファイルなど）の混入 |
| [`doc-paths`](docs/checks/doc-paths.md) | ドキュメントが名指ししているコードのパスの実在確認 |
| [`commit-subject`](docs/checks/commit-subject.md) | [Conventional Commits](https://www.conventionalcommits.org/) 形式の検証 |
| [`consistency`](docs/checks/consistency.md) | 複数ファイルから抽出した集合が一致するかの検証（type 一覧の突き合わせなど） |

固有性の高い検査は [`command`](docs/checks/command.md) で外部コマンドとして登録することもできます（後述）。

## インストール

[リリース](https://github.com/fuchigta/spotter/releases/latest)から、環境に合わせたバイナリを取得します。
Go は不要です。

**Linux / macOS**（`linux-amd64` の部分を `linux-arm64` / `darwin-amd64` / `darwin-arm64` に置き換えてください）

```bash
curl -fsSLo spotter https://github.com/fuchigta/spotter/releases/latest/download/spotter-linux-amd64
chmod +x spotter
sudo mv spotter /usr/local/bin/
```

**Windows**（PowerShell）

```powershell
Invoke-WebRequest -Uri https://github.com/fuchigta/spotter/releases/latest/download/spotter-windows-amd64.exe -OutFile spotter.exe
```

各バイナリには `.sha256` を添付しています。検証する場合は同じ URL に `.sha256` を付けて取得し、
`sha256sum -c`（Windows なら `Get-FileHash`）で照合してください。

Go の開発環境がある場合は、ソースからでも入れられます。

```bash
go install github.com/fuchigta/spotter/cmd/spotter@latest
```

## クイックスタート

リポジトリ直下に `.spotter.yml` を置きます。

```yaml
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: 'internal/cli/*.go'
        doc: README.md

  unwanted-files:
    type: unwanted-files
    max_bytes: 1048576
    deny:
      - { paths: '*.db', reason: 'ローカルのデータベース' }

  commit-subject:
    type: commit-subject
    allowed_types: [feat, fix, perf, refactor, docs, test, build, ci, chore, revert]
```

フックを設置します。

```bash
spotter install
```

`core.hooksPath` が未設定ならディレクトリを作って設定し、設定済みなら既存の `commit-msg` に
追記します（他のフックランナーと共存できます）。`--print` で呼び出し行だけを出力し、
lefthook などの既存ランナーに貼ることもできます。

```bash
spotter install --print
# spotter check --message "$1"
```

設定と設置状況の確認は `spotter doctor` でできます。フックの詳しい挙動（他のフックランナーとの
共存など）は [docs/hooks.md](docs/hooks.md) を参照してください。

## コマンド

```
spotter check [検査名] --message <ファイル>   # ステージ済みの変更（commit-msg フック向け）
spotter check [検査名] --range <git の範囲>    # 範囲（CI 向け）
spotter range [--provider github-actions|gitlab-ci]  # CI 用の範囲自動検出
spotter install [--print] [--hooks-dir <dir>]        # フックの設置
spotter doctor                                       # 検査一覧・フック設置状況の表示
```

`check` は**1 つ失敗しても残りを走らせ、終了コードだけを集約**します。検査名を指定すると
その検査だけを実行します。

CI では、範囲の算出まで `spotter` に任せられます。

```yaml
- run: echo "RANGE=$(spotter range)" >> "$GITHUB_ENV"
- run: spotter check --config .spotter.yml --range "$RANGE"
```

GitHub Actions と GitLab CI（セルフホスト含む）を環境変数から自動検出します。それ以外の CI では
`--range` に自分で組み立てた範囲式（`<from>..<to>` の形）を渡してください。自動検出の詳しい
ロジックとフォールバック条件は [docs/ci-integration.md](docs/ci-integration.md) を参照してください。

## 免除トレーラ

検査ごとに、コミットメッセージ本文のトレーラで免除できます（既定でトレーラ名は
`checks` のキーをタイトルケースにしたもの、例: `doc-sync` → `Doc-Sync`）。

```
Doc-Sync: skip 対応するドキュメントは無い
```

理由の記載は必須です。空の免除は拒否されます。ローカルのフックと CI が同じコミットメッセージを
見るため、**手元で通した判断がそのまま CI でも通ります**（環境変数による免除は用意していません。
CI に届かず、手元では通ったのに CI だけ落ちる、という状態になるためです）。

`commit-subject` はメッセージの体裁そのものを検証する検査なので、既定で免除が無効です。

トレーラ名の解決順や粒度ごとの効き方など、詳しい仕組みは [docs/exemptions.md](docs/exemptions.md)
を参照してください。

## 検査の粒度

検査ごとに範囲モードでの起動粒度が異なります。

- `squashed`（`doc-sync`）: 範囲全体を 1 回の比較としてまとめて見る。後からドキュメントを
  直すコミットを足せば通る
- `per-commit`（`unwanted-files` / `commit-subject`）: 範囲内のコミットごとに 1 回ずつ見る。
  後から消しても履歴に残るため直らない
- `worktree`（`doc-paths` / `consistency`）: staged/range を問わず、現在の作業ツリーを
  1 回だけ見る。コミットメッセージに依存しないため免除トレーラを持たない

それぞれの粒度がなぜこの単位になっているかは [docs/granularity.md](docs/granularity.md) を
参照してください。

## 外部コマンドで検査を追加する（`command`）

組み込みに寄せられない固有の検査は、外部コマンドとして登録できます。

```yaml
types:
  my-check:
    command: ./scripts/my-check.sh
    transport: file  # file（既定） | args | env
    schema:
      simple:
        threshold: { type: integer, required: true }
    default:
      granularity: squashed  # squashed | per-commit

checks:
  my-check:
    type: my-check
    threshold: 10
```

検査コマンドの入出力契約:

```
<command> --mode staged --message-file <path> [--options-file <path> | --<field> <value> ...]
<command> --mode range  --from <sha> --to <sha> --message-file <path> [...]
```

終了コード 0 が成功、非 0 が失敗です。標準エラー出力の内容が違反として表示されます。

`transport`/`schema` の詳細や、検査コマンドを実際に 1 つ作るチュートリアルは
[docs/checks/command.md](docs/checks/command.md) を参照してください。

## 設定ファイルのリファレンス

`.spotter.yml`（既定のパス。`--config` で変更可）は `checks` / `types` / `required_version`
の 3 つのトップレベルキーを持ちます。`checks` に検査インスタンスを列挙し、`types` は
組み込み type の default 上書き、または外部コマンド type の登録に使います。全体構造と
各フィールドの詳細は [docs/config-reference.md](docs/config-reference.md) を参照してください。

## もっと詳しく

各検査の設定オプションや、粒度・免除トレーラ・CI 連携の仕組みなど、詳細は
[docs/](docs/README.md) を参照してください。

## ライセンス

[MIT](LICENSE)
