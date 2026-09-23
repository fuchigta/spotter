# spotter

**コーディングエージェントが自律的に作業する（＝コミットする）のを、致命的な失敗の直前で支える**
ための CLI です。ジムのスポッター（補助者）の比喩から名付けました。

`git commit` の直前（`commit-msg` フック）と CI の両方から同じ検査を同じ引数で呼べるので、
「手元で通ったものは CI でも通る」が保証されます。spotter が守る約束の一覧は
[docs/principles.md](docs/principles.md) にまとめています。組み込みで次の検査を持ちます。

| type | 検査内容 |
|---|---|
| [`doc-sync`](docs/checks/doc-sync.md) | コードとドキュメントの対応。片方だけ変更されていたら失敗する |
| [`unwanted-files`](docs/checks/unwanted-files.md) | コミットしてはいけないもの（データベース・ログ・巨大ファイルなど）の混入 |
| [`doc-paths`](docs/checks/doc-paths.md) | ドキュメントが名指ししているコードのパスの実在確認 |
| [`commit-subject`](docs/checks/commit-subject.md) | [Conventional Commits](https://www.conventionalcommits.org/) 形式の検証 |
| [`consistency`](docs/checks/consistency.md) | 複数ファイルから抽出した集合が一致するかの検証（type 一覧の突き合わせ、複数行にまたがる記法の抽出など） |
| [`diff-content`](docs/checks/diff-content.md) | 差分の追加/削除行に対する deny パターンの検証（抑制コメント・テストの skip 残しなど） |
| [`commit-intent`](docs/checks/commit-intent.md) | コミットの type/scope と実際の変更内容の整合の検証（申告と実態の乖離） |
| [`companion-files`](docs/checks/companion-files.md) | 触ったファイルに対する相方ファイル（テストなど）の存在確認 |
| [`doc-links`](docs/checks/doc-links.md) | Markdown のリンク記法が指すファイルの実在確認（リンク切れ検出） |
| [`diff-size`](docs/checks/diff-size.md) | 1 コミットの変更量（ファイル数・行数）に上限を設ける検証 |

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

導入後の更新は `spotter update` でできます（リリースバイナリを再ダウンロードして
自分自身を置き換えます）。詳しくは [docs/update.md](docs/update.md) を参照してください。

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
      - { paths: '**/*.db', reason: 'ローカルのデータベース' }

  commit-subject:
    type: commit-subject
    allowed_types: [feat, fix, perf, refactor, docs, test, build, ci, chore, revert]
```

フックを設置します。

```bash
spotter hooks install
```

`core.hooksPath` が未設定ならディレクトリを作って設定し、設定済みなら既存の `commit-msg` に
追記します（他のフックランナーと共存できます）。`--print` で呼び出し行だけを出力し、
lefthook などの既存ランナーに貼ることもできます。

```bash
spotter hooks install --print
# spotter check --message "$1"
```

設定と設置状況の確認は `spotter doctor` でできます。フックの詳しい挙動（他のフックランナーとの
共存など）は [docs/hooks.md](docs/hooks.md) を参照してください。

## コマンド

```
spotter check [検査名] --message <ファイル>   # ステージ済みの変更（commit-msg フック向け）
spotter check [検査名] --range <git の範囲>    # 範囲（CI 向け）
spotter checks [--json]                              # 組み込み検査 type と設定キーの一覧
spotter config lint [--config <path>] [--json]       # 陳腐化した設定（死んだパターン・未参照の type）の検出
spotter range [--provider github-actions|gitlab-ci]  # CI 用の範囲自動検出
spotter hooks install [--print] [--hooks-dir <dir>]  # フックの設置
spotter skills list [--json]                         # 同梱スキル（コーディングエージェント向け）の一覧
spotter skills show <name> [--file <path>] [--list]  # スキルの SKILL.md・ファイル一覧・個別ファイルを表示する
spotter skills install <target> [--scope project|user] [--only <names>] [--force]  # スキルの設置
spotter skills uninstall <target> [--scope project|user] [--only <names>] [--force] [--dry-run]  # スキルの削除
spotter skills status [--scope project|user]         # 設置済みスキルの状況
spotter doctor                                       # 検査一覧・フック設置状況の表示
spotter update [--check] [--version <バージョン>]     # バイナリを GitHub Releases の版に更新する
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

組み込みに寄せられない固有の検査は、`types` に `command`（実行ファイル）を登録して
外部コマンドとして追加できます。入出力契約・`args`/`transport`/`schema` の詳細、
検査コマンドを実際に 1 つ作るチュートリアルは [docs/checks/command.md](docs/checks/command.md)
を参照してください。

## 設定ファイルのリファレンス

`.spotter.yml`（既定のパス。`--config` で変更可）は `checks` / `types` / `required_version`
の 3 つのトップレベルキーを持ちます。`checks` に検査インスタンスを列挙し、`types` は
組み込み type の default 上書き、または外部コマンド type の登録に使います。全体構造と
各フィールドの詳細は [docs/config-reference.md](docs/config-reference.md) を参照してください。

## コーディングエージェント向けスキル

Claude Code / Codex CLI などコーディングエージェント向けに、`.spotter.yml` の書き方・
検査失敗時の対処・新規導入・既存設定の見直しを支援するスキルを同梱しています
（`spotter skills list` で確認できます）。`spotter skills install` で設置する以外に、
Claude Code なら `/plugin marketplace add fuchigta/spotter` でこのリポジトリを直接
マーケットプレイスとして追加することもできます。詳しくは
[docs/skills.md](docs/skills.md) を参照してください。

## もっと詳しく

各検査の設定オプションや、粒度・免除トレーラ・CI 連携の仕組みなど、詳細は
[docs/](docs/README.md) を参照してください。

## ライセンス

[MIT](LICENSE)
