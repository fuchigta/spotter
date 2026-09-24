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

- 直接依存は最小限（cobra / yaml.v3 / doublestar / jsonschema）。安易に増やさない
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

- **コードを変えたら対応するドキュメントも同じコミットで直す。** 対応表は
  `.spotter.yml` の `doc-sync.pairs` にある
- **振る舞いを変えるならテストを伴う。** `internal/check/*/` の各 `.go` には
  `_test.go` を併設する。テストは fake の `Source` を使ったテーブル駆動で、決定論的に書く
- **コミットは Conventional Commits。** type は `.spotter.yml` の `allowed_types` と
  `cliff.toml` の両方に揃える（`consistency` 検査が突き合わせる）。`docs:` を名乗るなら
  ドキュメントだけを変える。件名・本文は日本語
- **コードやドキュメントに issue 番号への参照（`#` と数字）を書かない。**
  `scripts/check-issue-refs.sh` が弾く
- ドキュメントに書くパスとリンクは実在させる（`doc-paths` / `doc-links`）
- 検査やスキルの**個数をドキュメントに書かない**（「10 種類の検査」「4 本のスキル」など）。
  増減のたびに陳腐化するため、一覧は表や `spotter checks` / `spotter skills list` に任せる
- 検査 type を増減・改名したら README.md・docs/README.md・`skills/spotter-docs/SKILL.md` の
  一覧を揃える（`check-types-consistency` が突き合わせる）
- 1 コミットは小さく保つ（上限は `.spotter.yml` の `diff-size`）
- 抑制コメント（`nolint`）やテストの skip・削除で検査を黙らせない（`diff-content`）
- 新しい識別子には [CONTEXT.md](CONTEXT.md) の用語に対応する英語を使い、用語を新しく
  作ったら CONTEXT.md に括弧で識別子を添える（用語と識別子をずらさない）
- エラーは `fmt.Errorf("<パッケージ名など>: <文脈>: %w", err)` のように、どこで何が起きたかを
  前に付けて日本語でラップする
- コメント（`.spotter.yml` を含む）には、今のコードや設定を読んでも分からない「なぜ」だけを
  書く。変更の経緯（「〜から移した」「以前は〜だった」「免除が繰り返されていたため」）や
  作業中のやりとり（レビュー指摘・検討した代案）は書かず、コミットメッセージに残す
- spotter が設置する `.claude/skills/spotter-*/` と `.agents/skills/spotter-*/` はコミットしない
  （`skills/` から再生成できる）。外部から入れたスキルは `skills-lock.json` と一緒にコミットする
- 同梱スキルの動作確認は `go run ./cmd/spotter skills install <target> --dir <一時ディレクトリ>` で外に出すか、
  `go run ./cmd/spotter skills show <name>` で内容だけ見る
