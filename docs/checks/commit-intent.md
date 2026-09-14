# commit-intent

コミットメッセージの **type/scope** と、実際に変更されたファイル・差分の**内容**が
噛み合っているかを検証する検査です。

[`commit-subject`](commit-subject.md) が subject の**体裁**（Conventional Commits の形として
正しいか）だけを見るのに対し、こちらは**実態**（申告した type と実際の差分が一致しているか）
を見ます。

- `docs:` と名乗りながらコードを書き換えている
- `fix:` なのにテストが 1 行も増えていない
- `refactor:`（振る舞いを変えない）と言いながら新しい定義を生やしている

こうした「メッセージは体裁として正しいのに実態と噛み合わない」ミスは、commit-subject
（体裁のみ）でも一般的な linter（差分を見ない）でも検知できません。spotter は commit-msg
フックとして**メッセージと差分の両方を同時に持っている**ため、ここを検証できます。

起動粒度は [per-commit](../granularity.md) 固定です。メッセージと差分が 1:1 で対応していないと
意味を持たないため、`squashed`（範囲内の全メッセージを連結する）は選べません。

## 設定

```yaml
checks:
  commit-intent:
    type: commit-intent
    rules:
      # docs を名乗るならドキュメントだけを触る
      - types: [docs]
        allow: ['**/*.md', 'docs/**']
        reason: 'docs はドキュメントだけを変更する'

      # 振る舞いを変えるならテストが一緒に来る
      - types: [feat, fix]
        require: ['**/*_test.*', '**/test_*', '**/*.test.*']
        reason: '振る舞いの変更にはテストを伴う'

      # refactor を名乗って新しい定義を生やしていないか
      - types: [refactor]
        deny_diff: '^\+\s*(func|def|class) '
        reason: 'refactor で新しい定義が増えている'
```

**既定ルールはありません。** 特に `require` に書くテストファイルの命名は言語ごとに違うため、
Go 側にハードコードされた既定値は持ちません。下部の「レシピ」を参考に、自分のプロジェクトの
言語・命名規則に合わせて書いてください。`rules` が 0 件だと起動時エラーになります。

### `rules[].types`（必須）

このルールを適用する commit type の一覧。コミットメッセージの type がここに含まれない
場合、このルールはそもそも評価されません。

### `rules[].scopes`（省略可）

指定すると、その scope のときだけこのルールを適用します（省略時は scope を問いません）。

### `rules[].allow` / `rules[].require` / `rules[].deny_diff`（少なくとも 1 つ必須）

| キー | 判定 | 違反になる条件 |
|---|---|---|
| `allow` | 変更ファイルが**全て**いずれかの doublestar パターンに一致する | 一致しないファイルが 1 つでもある |
| `require` | 変更ファイルの**少なくとも 1 つ**がいずれかの doublestar パターンに一致する | どれにも一致するファイルが無い |
| `deny_diff` | 差分（追加・削除行を含む全体）に正規表現が一致しない | 一致してしまう |

`deny_diff` は差分テキスト全体に当てます。`doc-sync` の `when` と同じく `(?m)` を自動付与する
ため、`^`/`$` はそのまま行頭・行末に効きます（例の `^\+\s*(func|def|class) ` は「行頭が `+`
（追加行）で始まり、その後に `func`/`def`/`class` が続く行」に一致します）。単一の追加/削除行
だけを見て良いなら、より柔軟な [diff-content](diff-content.md) の利用も検討してください。

1 つのルールに `allow`/`require`/`deny_diff` を複数指定すると、それぞれ独立して評価され、
違反ごとに別の結果として表示されます。

### `rules[].reason`（省略可）

違反表示に出す説明。省略すると `allow`/`require`/`deny_diff` の内容から既定文言を組み立てます。

## 判定の細部

- 1 つのコミットに複数のルールが一致する場合、**全て**評価します。
- コミットメッセージの subject が Conventional Commits の一般形になっていない場合、
  この検査は**何も報告しません**（体裁の検証は `commit-subject` の責務であり、両方で
  落とすと同じ問題を 2 回報告することになるため）。
- subject が空、または `Merge `/`Revert ` で始まる場合（git が自動生成するメッセージ）も
  対象外です。
- 変更ファイルが 0 件のコミットはスキップします。

## 免除は既定で有効

`commit-intent` は差分側の実態を見る検査なので、`commit-subject`（体裁の検証を体裁で
回避する矛盾を避けるため既定で免除無効）とは異なり、**既定で免除トレーラが有効**です。
「テスト不要な軽微な修正」のような正当な例外はあり得るため、免除トレーラに理由を書かせる
ことに価値があります。

```
Commit-Intent: skip 型定義のみの変更でテストの追加は不要なため
```

## レシピ（設定例。既定値ではありません）

以下は「よくある使い方の例」であり、spotter が既定で持つ値ではありません。必要なものだけを
自分のプロジェクトの `.spotter.yml` にコピーして使ってください。

### ドキュメントのみを名乗るなら、コードを触らせない

```yaml
- types: [docs]
  allow: ['**/*.md', 'docs/**']
  reason: 'docs はドキュメントだけを変更する'
```

### 振る舞いの変更にはテストを伴わせる

```yaml
# Go
- types: [feat, fix]
  require: ['**/*_test.go']
  reason: '振る舞いの変更には Go のテストを伴う'

# Python
- types: [feat, fix]
  require: ['**/test_*.py', '**/*_test.py']
  reason: '振る舞いの変更には pytest のテストを伴う'

# JS/TS
- types: [feat, fix]
  require: ['**/*.test.{js,ts}', '**/*.spec.{js,ts}']
  reason: '振る舞いの変更には JS/TS のテストを伴う'
```

### refactor で新しい公開 API を生やしていないか

```yaml
# Go（新しい関数定義の追加行を検知）
- types: [refactor]
  deny_diff: '^\+\s*func [A-Z]'
  reason: 'refactor で新しいエクスポート関数が増えている'

# Python
- types: [refactor]
  deny_diff: '^\+\s*(def|class) '
  reason: 'refactor で新しい定義が増えている'
```

### chore/build はソースコードを触らせない

```yaml
- types: [chore, build]
  allow: ['**/*.yml', '**/*.yaml', '**/*.json', 'Makefile', 'Dockerfile']
  reason: 'chore/build は設定ファイルだけを変更する'
```
