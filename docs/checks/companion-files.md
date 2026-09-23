# companion-files

触ったファイルに対して、必ず伴うはずの**相方ファイルが存在するか**を検査します。

[`doc-sync`](doc-sync.md) は「A を**変更**したなら B も**変更**されていること」を見ますが、
これは「A を触ったなら B が**存在**すること」であって別の関心事です。まだ B が 1 度も
無い（doc-sync の言う「片方だけ変更」にすらならない）状態は doc-sync では捕まりません。
実装だけ作ってテストを作らない、コンポーネントだけ作って型定義を作らない、マイグレーションの
up だけ作って down を作らない、といったミスがこの検査の対象です。

起動粒度は [squashed](../granularity.md) 固定です。後から相方ファイルを足すコミットを
積めば通ります（doc-sync と同じ意味論）。

## 設定

```yaml
checks:
  companion-files:
    type: companion-files
    companions:
      - paths: 'src/**/*.ts'
        companion: '{dir}/{name}.test.ts'
        reason: 'テストが無い'
        exclude: ['**/*.d.ts', 'src/types/**']

      - paths: 'db/migrations/**/*.up.sql'
        companion: '{dir}/{name}.down.sql'
        reason: 'ロールバック用のマイグレーションが無い'
```

`commit-intent` も同じ「ルールの一覧」という形の設定を持ちますが、フィールドの形が
まったく違う（commit-intent は commit type ごとの条件、こちらはファイルパターンごとの
相方指定）ため、YAML キーはあえて別にしています（`rules` ではなく `companions`）。

**既定ルールはありません。** テストファイルの命名規約は言語ごとに全く違うため、Go 側に
ハードコードされた既定値は持ちません。下部の「レシピ」を参考にしてください。
`companions` が 0 件だと起動時エラーになります。

### `companions[].paths` / `companions[].companion` / `companions[].reason`（全て必須）

- `paths`: 対象にするファイルの [doublestar](https://github.com/bmatcuk/doublestar) パターン
- `companion`: 相方ファイルのパスを組み立てるテンプレート（下記の変数が使えます）
- `reason`: 違反表示に出す理由

### `companions[].exclude`（省略可）

このルールから外す doublestar パターンの一覧。

### テンプレート変数

| 変数 | 値（`src/api/client.ts` の場合） |
|---|---|
| `{dir}` | `src/api` |
| `{name}` | `client`（**最後の**拡張子を除いたベース名） |
| `{ext}` | `.ts` |
| `{path}` | `src/api/client.ts` |

この 4 変数だけのシンプルな置換です。正規表現キャプチャによる汎用的な変換は設定が読みにくく
なるため採用していません。これで足りない場合は [`command`](command.md) 型検査を使ってください。

`{dir}` はリポジトリ直下のファイル（ディレクトリ部分が無い）では空文字列になります。
`src/api/client.ts` → `src/api`、`client.ts` → `` （空）。テンプレート展開後に
先頭の `/` が残らないよう自動で正規化されるので、`'{dir}/{name}.test.ts'` は
`client.ts` に対して `/client.test.ts` ではなく `client.test.ts` になります。

**既知の制限**: `{ext}`/`{name}` は最後の `.` だけを区切りに使います。`001.up.sql` の
ような複合拡張子では `{ext}` は `.sql` のみ、`{name}` は `001.up` になります
（`.up` の部分は `{name}` に残ります）。上の設定例の 2 番目のルールはこの制限の影響を
受け、実際に探す相方は `db/migrations/001.up.down.sql` になります。`up`/`down` が
拡張子そのもの（例: ファイル名が `001.up`、`001.down` で `.sql` を持たない）であれば
問題なく機能します。この制限を超えた変換が必要な場合は `command` 型検査を使ってください。

## 判定のしかた

`Source.ChangedFiles()` は追加と変更を区別しません（`--diff-filter=ACMR` をまとめて返す）。
そこで、この検査は次のように判定します。

> 変更集合に含まれる `paths` 一致ファイルについて、その相方が**比較の終点に
> 存在しない**なら違反。

追加・変更を区別する必要が無く、既存のインターフェースだけで実装できます。相方の**中身**が
更新されているかまでは見ません（それは doc-sync の守備範囲で、こちらは存在だけを見ます）。

「比較の終点」は staged モードではインデックス、range モードでは `to` のツリーです
（`Source.Exists`）。手元で `git add` し忘れた相方も、range で過去のコミットを検査する
場合も、同じ基準で判定します。作業ツリーを直接見ることはありません。

## 出力

```
companion-files の検査に失敗しました。

  テストが無い:
    - src/api/client.ts → src/api/client.test.ts
```

## 免除

[免除トレーラ](../exemptions.md)が使えます。既定のトレーラ名は `Companion-Files` です
（キー名を変えていれば、そのキーから生成された名前になります）。squashed 粒度なので、
範囲内のどれか 1 コミットのメッセージに書けば効きます。

## レシピ（設定例。既定値ではありません）

以下は「よくある命名規約の例」であり、spotter が既定で持つ値ではありません。必要なものだけを
自分のプロジェクトの `.spotter.yml` にコピーして使ってください。

### テスト

```yaml
# Go（同じディレクトリに _test.go）
- paths: 'internal/**/*.go'
  companion: '{dir}/{name}_test.go'
  reason: 'Go のテストが無い'
  exclude: ['**/*_test.go']

# TypeScript（同じディレクトリに .test.ts）
- paths: 'src/**/*.ts'
  companion: '{dir}/{name}.test.ts'
  reason: 'TypeScript のテストが無い'
  exclude: ['**/*.d.ts', '**/*.test.ts']

# Python（同じディレクトリに test_ プレフィックス。{name} だけでは組み立てられないため
# ディレクトリ構成を test_ 側に揃える運用が前提）
- paths: 'app/**/*.py'
  companion: '{dir}/test_{name}.py'
  reason: 'Python のテストが無い'
  exclude: ['**/test_*.py', '**/__init__.py']
```

### コンポーネントに付随するファイル

```yaml
# React コンポーネントにスタイルシートを伴わせる
- paths: 'src/components/**/*.tsx'
  companion: '{dir}/{name}.module.css'
  reason: 'コンポーネントのスタイルシートが無い'
  exclude: ['**/*.stories.tsx']
```
