# AGENTS.md

このリポジトリで作業するコーディングエージェント向けの指針です。個々の仕様は
[README.md](README.md) と [docs/](docs/README.md) が正です。用語は
[CONTEXT.md](CONTEXT.md) に揃えてください。

## 守るべき約束

spotter が利用者に約束していることは [docs/principles.md](docs/principles.md) に
まとめています。作業の前に必ず読んでください。

- 約束に反する変更（手元と CI で結果が変わりうる要素を持ち込む、警告レベルを足す、
  理由なしの免除を通す、利用者のフック設定を上書きする、など）は**独断で進めず、
  メンテナーに確認する**
- 合意の上で約束を変える場合は破壊的変更として扱い（`feat!:` / `BREAKING CHANGE`）、
  docs/principles.md も同じコミットで直す
- 新しい機能や検査を設計するときは、まず約束のどれに関わるかを確かめる。例えば新しい
  検査なら「後から直せば通るか」を決めてから起動粒度を選ぶ（[docs/granularity.md](docs/granularity.md)）

## 設計の考え方

以下は約束を実現するための現在の設計判断です。約束と違い、理由があれば変えて構いません
が、変えるときはその理由をコミットメッセージに残してください。

### 検査の構造

- 検査は `check.Runner`（`internal/check/check.go`）を実装し、違反を `[]Violation` で
  返す。`Violation` に重大度は持たせない
- 検査本体は git を直接呼ばない。staged と range の違いは `check.Source` が吸収し、
  検査は差分・ファイル一覧だけを見る。git とのやりとりは `internal/gitutil` に閉じる
- 検査本体は OS のファイルシステムも直接開かない。worktree 粒度の検査は
  `check.Context.FS`（`fs.FS`）越しに作業ツリーを読む。テストでは `fstest.MapFS` や
  読み取りに失敗する `fs.FS` を渡す
- 検査自体が実行できない（設定不正など）ことは `error`、検査の結果としての失敗は
  `[]Violation`。この 2 つを混ぜない
- 1 つの検査に責務を 1 つだけ持たせ、同じ問題を 2 回報告しない（例: subject が
  Conventional Commits でなければ `commit-intent` は黙り、`commit-subject` に任せる）
- 言語やプロジェクトに依存する知識は組み込み検査に入れず、`command` 型か
  ドキュメントのレシピに逃がす

### 設定

- 組み込み type は構造体で、`command` 型のオプションだけを `internal/schema`
  （simple / JSON Schema）で検証する
- 「未指定」と「false」を区別する（例: `exempt.enable` はポインタ）。解決順は
  システム既定 → `types.<type>.default` → `checks.<key>`
- `config lint` は静的な走査にとどめ、「一致しないことが正常」なフィールド
  （`unwanted-files.deny` など）は誤検知しないよう対象から外す

### 対応範囲と依存

- 直接依存は最小限にし、安易に増やさない。許可しているのは `github.com/spf13/cobra`・
  `gopkg.in/yaml.v3`・`github.com/bmatcuk/doublestar/v4`・`github.com/santhosh-tekuri/jsonschema/v6` で、
  `go.mod` の直接依存と一致するかを `direct-deps-consistency` が突き合わせる
- 開発用のツール（lint など）は本体の `go.mod` に入れず、ツールごとの `tools/<ツール名>/go.mod` の
  `tool` ディレクティブでバージョンを固定して `bash scripts/tool.sh <ツール名>` で実行する。手元に
  入っているかで結果が変わらないよう、Go で書かれたツールに限る。1 つの go.mod にまとめると依存の
  解決がツール間で混ざってビルドできなくなるので、ツールごとに分ける
- 実機でしか検証できない分岐は増やさない。`spotter range` の自動検出を GitHub Actions と
  GitLab CI に絞っているのはこのため。それ以外の CI は利用者が範囲を組み立てる
- `update` は GitHub API を使わず、リリースのリダイレクトだけで最新版を解決する
  （レート制限を避けるため）

### エージェント向けの同梱スキル

- 同梱スキル（`skills/`）は場面ごとに役割を分ける（一覧は `spotter skills list`）
- 設置先の所有権は `SKILL.md` の frontmatter の `managed-by: spotter` で判定する
- 設定キーなどは、散文より `spotter checks --json` のような実装から機械生成された出力を
  信じるよう案内する
- 「生成して終わり」にせず、`spotter doctor` や `--range` での空振り実行で検証してから
  提示させる。履歴の書き換えやシークレットの扱いなど取り返しのつかない操作は、
  利用者の確認を挟ませる

## このリポジトリで作業するときの決まり

このリポジトリの [.spotter.yml](.spotter.yml) は spotter を自身に適用したものです
（`.githooks/commit-msg` は `go run ./cmd/spotter` で手元のソースを検査します）。

### 検査が見ている決まり

以下は commit-msg フックと CI の `spotter check` が止めるので、落ちたら指摘に従って直してください。
免除トレーラは、検査の意図に照らして免除が妥当な理由を書けるときだけ使います。

- コードを変えたら対応するドキュメントも同じコミットで直す（`doc-sync`。対応表は `pairs`）
- `internal/check/*/` の各 `.go` には `_test.go` を併設し、`feat:`/`fix:` はテストを伴う
  （`companion-files` / `commit-intent`）
- コミットは Conventional Commits で、件名・本文は日本語（`commit-subject` / `commit-lang`）。
  type は `allowed_types` と `cliff.toml` で揃え（`commit-types-consistency`）、`docs:` は
  ドキュメントだけを変える（`commit-intent`）
- issue 番号への参照（`#` と数字）を書かない（`check-issue-refs`）
- ドキュメントのパスとリンクは実在させる（`doc-paths` / `doc-links`）。`docs/` のページを
  増減したら目次も揃える（`docs-index-consistency`）
- 検査 type の一覧は README.md・docs/README.md・`skills/spotter-docs/SKILL.md` で揃える
  （`check-types-consistency`）。検査やスキルの個数はドキュメントに書かず、一覧は表や
  `spotter checks` / `spotter skills list` に任せる（`diff-content`）
- 1 コミットは小さく保つ（`diff-size`）
- 抑制コメント（`nolint`）やテストの skip・削除で検査を黙らせない（`diff-content`）
- `internal/check` のテストでは実 git や実ファイルを使わず、fake の `Source` や `fstest.MapFS` を
  渡す（`diff-content`）
- 外部から入れた `.claude/skills/` のスキルは `skills-lock.json` と一緒に変える（`doc-sync`）。
  spotter が設置する `spotter-*` のスキルは `.gitignore` で除外済み

### 検査では見きれない決まり

- テストはテーブル駆動で、決定論的に書く
- 新しい識別子には [CONTEXT.md](CONTEXT.md) の用語に対応する英語を使い、用語を新しく
  作ったら CONTEXT.md に括弧で識別子を添える。添えた識別子がコードで宣言されているかは
  `context-identifiers` が見る。`_Avoid_` のうち
  別の意味で使われない表記は `diff-content` が禁止語として止めるので、足したら `.spotter.yml` にも足す
- エラーは `fmt.Errorf("<パッケージ名など>: <文脈>: %w", err)` のように、どこで何が起きたかを
  前に付けて日本語でラップする。呼び出し側が接頭辞を付けてまとめる内側のエラーや、
  利用者にそのまま見せる文言はこの限りでない
- コメント（`.spotter.yml` を含む）には、今のコードや設定を読んでも分からない「なぜ」だけを
  書く。変更の経緯や作業中のやりとり（レビュー指摘・検討した代案）はコミットメッセージに
  残す。典型的な言い回しは `diff-content` が止めるが、言い回しを変えれば済むわけではない
- 同梱スキルの動作確認は `go run ./cmd/spotter skills install <target> --dir <一時ディレクトリ>` で外に出すか、
  `go run ./cmd/spotter skills show <name>` で内容だけ見る
