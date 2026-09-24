---
name: spotter-setup
description: まだ .spotter.yml が無いリポジトリに spotter を新規導入する。技術スタック（言語・依存関係）、利用中の静的解析ツール、ドキュメント構造、テスト規約、コミット規約を検出し、そのリポジトリに合った検査構成の .spotter.yml を生成する。既存の .spotter.yml がある場合はこのスキルの対象外（リファクタ用の別スキルを使う）。
license: MIT
metadata:
  managed-by: spotter
---

# spotter の新規セットアップ

`.spotter.yml` を新規に作るときの手順です。**生成して終わりにしないでください。** 既存の
コミット履歴に対して試し打ちし、誤検知が多い検査は緩めるか外してから導入するところまでが
このスキルの範囲です。生成した設定をそのまま置くと、既存リポジトリで大量に落ちて
「免除トレーラの濫用」を誘発します。

## 前提確認

1. `.spotter.yml` が既に存在するなら**このスキルの対象外**です。新規作成ではなく既存設定の
   見直しになるので、リファクタ用のスキル（`spotter skills list` で同梱されているか確認。
   無ければ利用者に相談してください）に委ねてください。
2. `spotter --version` で手元のバイナリを確認し、`spotter checks --json` で組み込み検査
   type・起動粒度・受け付ける設定キーの正を取得してください。**このスキルの本文やレシピの
   散文より `spotter checks --json` の出力を優先してください**（実装から機械的に生成されて
   いるため、こちらの記述が古い可能性があります）。

## 手順

1. **技術スタック検出** — `go.mod` / `package.json` / `pyproject.toml` / `Cargo.toml` /
   `pom.xml` / `Gemfile` / `*.csproj` の有無から主要言語を特定する
2. **静的解析ツール検出** — `.golangci.yml` / ESLint・Prettier 設定 / `ruff`・`black` 設定 /
   `.pre-commit-config.yaml` / CI ワークフローの中身から、既に使っている lint/format
   ツールを特定する
3. **コミット規約検出** — `cliff.toml` / `.commitlintrc*` / `CONTRIBUTING.md` / 直近の
   `git log --oneline -50` から、実際に使われている commit type の語彙を特定する
4. **ドキュメント構造検出** — `docs/` の有無、README の構成、トップレベルのソース
   ディレクトリ名を確認する
5. **テスト規約検出** — 既存のテストファイルの配置（同じディレクトリか別ディレクトリか、
   命名規則）を確認する
6. **混入リスク検出** — `.gitignore` の内容、`git log --diff-filter=A --name-only` で過去に
   追加されたファイルの傾向から、混入させたくないものを特定する

各ステップで具体的に「何を見て」「どう判断するか」は `references/detection.md` を参照して
ください（言語別の詳細な検出手がかり表があります）。`assets/` には言語別の最小構成
テンプレートがあります（`spotter.minimal.yml` / `spotter.go.yml` / `spotter.node.yml` /
`spotter.python.yml`）。ゼロから書くのではなく、近い言語のテンプレートをコピーして
リポジトリの実情に合わせて調整してください。

7. **生成と構文検証** — リポジトリ外の一時ファイル（例 `spotter.yml.candidate`）に書き、
   `spotter doctor --config <一時パス>` で構文・型エラーを潰す
8. **空振りテスト（最重要）** — `spotter check --config <一時パス> --range HEAD~10..HEAD`
   のように、既存のコミット履歴に対して実際に走らせる（コミット数が足りない新しい
   リポジトリでは範囲を縮める）。**新規リポジトリで書いた設定を
   検証せずに置いてはいけません。** 既存履歴にどれだけ引っかかるかを見てから次に進みます
9. **段階導入** — 誤検知が多い検査は、パターンを緩めるか、いったん設定から外す。最初は
   誤検知ゼロの検査だけで導入し、`required_version` を現在の `spotter --version` で
   固定する。段階導入の考え方の詳細は `references/rollout.md` を参照してください
10. **仕上げ** — `spotter hooks install` でフックを設置し、CI 側にも `spotter range` +
    `spotter check --range` を組み込む（`references/rollout.md` に GitHub Actions /
    GitLab CI の例がある。同梱されていれば `spotter-docs` スキルの `ci-integration.md`
    も参照）

## 検査を選ぶときの指針

- `worktree` 粒度の検査（`doc-paths` / `consistency` / `doc-links`）は免除トレーラが
  無く、誤検知があると手直しするまで検査が通りません。**空振りテスト（手順8）を
  必ず通してから導入してください。** 裏を返せば、空振りテストさえ通っていれば
  導入後に免除トレーラで誤魔化す逃げ道が無いぶん、運用中の検査の信頼性は最も
  高くなります
- `consistency` は「同じ情報を複数ファイルに手で転記していて片方だけ更新し忘れる」
  具体的な二重管理箇所を見つけたときだけ追加してください（コミット type の一覧と
  リリースノート生成ツールの設定など）。`assets/` のテンプレートには含めていません。
  `sources` は2件以上・各 `extract` はキャプチャグループ1個が必須で、該当箇所が
  無いまま無理に書くと起動時エラーになります
- `unwanted-files` はほぼどのリポジトリでも有効です（ログ・`.env`・秘密鍵・ビルド成果物の
  混入防止）。`assets/spotter.minimal.yml` の `deny` をベースに、`.gitignore` の内容を
  参考にして増やしてください
- `commit-subject` / `commit-intent` / `diff-content` はチームの合意が要る検査です。
  コミット規約が既に統一されていない（手順3で語彙がバラバラだった）リポジトリにいきなり
  `commit-subject` を強制すると、既存の開発フローと衝突します。空振りテストで違反が
  多ければ、まず `allowed_types` を実態に合わせて緩めるか、導入を見送って利用者に
  相談してください
- `doc-sync` / `companion-files` はコード側とドキュメント/テスト側の対応表が要ります。
  最初から全コードを網羅しようとせず、メインのソースディレクトリ1つだけに絞って
  始めるのが安全です

## 仕上げの前に

生成した `.spotter.yml` を提示・コミットする前に、必ず手順7・8（`spotter doctor` と
空振りテスト）をやり直してください。設定を調整するたびに結果が変わります。
