# 検出手がかり（言語別）

`spotter-setup` の手順 1〜6 で、具体的に何を見て何を判断するかの表です。ここに無い言語・
フレームワークに遭遇したら、同じ考え方（マニフェストファイル → テスト規約 → lint 設定 →
無視すべきファイル）で類推してください。

## 1. 技術スタック検出

| マニフェスト | 言語/ランタイム | 典型的なビルド成果物・依存ディレクトリ |
|---|---|---|
| `go.mod` | Go | `*.exe`, ビルドしたバイナリ名, `vendor/` |
| `package.json` | Node.js / TypeScript | `node_modules/`, `dist/`, `build/`, `*.tsbuildinfo` |
| `pyproject.toml` / `setup.py` / `requirements.txt` | Python | `__pycache__/`, `*.pyc`, `.venv/`, `venv/`, `*.egg-info/` |
| `Cargo.toml` | Rust | `target/` |
| `pom.xml` / `build.gradle` | Java/Kotlin | `target/`（Maven）, `build/`（Gradle）, `*.class`, `*.jar` |
| `Gemfile` | Ruby | `vendor/bundle/` |
| `*.csproj` / `*.sln` | C#/.NET | `bin/`, `obj/` |

複数のマニフェストが見つかった場合（モノレポ、フロントエンド+バックエンド混在など）は、
`doc-sync` / `companion-files` のルールをディレクトリ単位で分けて書くことを検討してください
（例: `doc-sync-frontend` と `doc-sync-backend` を別キーで定義。同じ組み込み type を複数
インスタンス化できます）。

## 2. 静的解析ツール検出 → `diff-content` の deny 候補

既存の lint 設定ファイルが見つかったら、対応する抑制コメントの記法を `diff-content` の
`deny` に追加する候補にします。

| 設定ファイル | ツール | 抑制コメントの例 |
|---|---|---|
| `.golangci.yml` | golangci-lint | `//nolint` |
| `.eslintrc*` | ESLint | `// eslint-disable`, `// eslint-disable-next-line` |
| `tsconfig.json` | TypeScript | `// @ts-ignore`, `// @ts-expect-error` |
| `ruff.toml` / `pyproject.toml` の `[tool.ruff]` | Ruff | `# noqa` |
| `.flake8` | Flake8 | `# noqa` |
| `.rubocop.yml` | RuboCop | `# rubocop:disable` |

テストのスキップ記法も言語ごとに違うので、`companion-files`/テスト規約の検出と合わせて
`diff-content` の `deny`（`on: added`, テストファイルへの `paths` 絞り込み）候補にします。

| 言語/フレームワーク | スキップ記法 |
|---|---|
| Go（標準 testing） | `t.Skip(`, `t.SkipNow(` |
| JS/TS（Jest/Mocha 系） | `.skip(`, `.only(`, `xit(`, `xdescribe(` |
| Python（pytest） | `@pytest.mark.skip`, `@pytest.mark.skipif` |
| Ruby（RSpec） | `xit `, `skip ` |

## 3. コミット規約検出

- `cliff.toml`（[git-cliff](https://github.com/orhun/git-cliff)）があれば `[git.commit_parsers]`
  の `message = "^type"` から type 一覧を抽出できる。このリポジトリ自身の `.spotter.yml` が
  `consistency` 検査で `cliff.toml` と `commit-subject.allowed_types` を突き合わせている実例
- `.commitlintrc*` があれば `type-enum` ルールから type 一覧を抽出できる
- どちらも無ければ `git log --oneline -50` の 1 行目を集計し、実際に使われている type の
  語彙を数える。`feat`/`fix`/`docs`/`chore` あたりが多ければ Conventional Commits に近い
  運用が既にされている可能性が高い。バラバラなら `commit-subject`/`commit-intent` の導入は
  慎重に（`SKILL.md` の「検査を選ぶときの指針」を参照）

## 4. ドキュメント構造検出 → `doc-sync` / `doc-paths` / `doc-links`

- `docs/` ディレクトリがあるか、README 1 枚だけか
- トップレベルのソースディレクトリ名（src, lib, internal, pkg, app 等）を
  `doc-paths.path_prefixes` の候補にする
- README や `docs/` 内で、コード側のパスにバッククォートで言及している箇所がどれだけ
  あるか（少なければ `doc-paths` の価値は低い）
- ドキュメント同士の相互リンク（`[text](path)`）がどれだけあるか（`doc-links` の価値の目安）

## 5. テスト規約検出 → `companion-files`

既存のテストファイルを数件サンプルして、相方ファイルの配置パターンを特定します。

| パターン | 例 |
|---|---|
| 同じディレクトリに `_test.go` | `foo.go` → `foo_test.go` |
| 同じディレクトリに `.test.ts` | `src/api/client.ts` → `src/api/client.test.ts` |
| 別ディレクトリ + `test_` 接頭辞 | `app/models/user.py` → `tests/models/test_user.py` |
| `__tests__/` サブディレクトリ | `src/Button.tsx` → `src/__tests__/Button.test.tsx` |

`companion` テンプレートで使えるのは `{dir}`/`{name}`/`{ext}`/`{path}` の4変数だけです。
別ディレクトリ + 接頭辞の組み合わせ（`tests/models/test_user.py` のようにディレクトリ構造
ごと変わる）はこのテンプレートでは組み立てられません。その場合は `command` 型検査での
実装を検討するか、companion-files の導入を見送ってください。

## 6. 混入リスク検出 → `unwanted-files`

- `.gitignore` に列挙されているパターンは、そのままコミットされては困るものの一覧です。
  `**/*.log` / `**/.env` / `**/.env.*` / 秘密鍵拡張子（`*.pem`, `*.key`）は言語を問わず
  ほぼ常に該当します
- `git log --diff-filter=A --name-only | sort | uniq -c | sort -rn` で過去に追加された
  ファイルの拡張子を見て、ビルド成果物や大きなバイナリが過去に混入した形跡がないか確認
  する（あれば再発防止として `deny` に追加する強い理由になる）
