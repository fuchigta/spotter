# diff-content

差分の**追加行・削除行そのもの**を deny パターンと突き合わせる検査です。
[`unwanted-files`](unwanted-files.md) が「ファイルの deny」なのに対して、こちらは「行の deny」です。

コーディングエージェントの最頻の失敗は「直さずに黙らせる」ことです。型/lint エラーを抑制
コメントで消す、落ちるテストを skip/only で回避する、コンフリクトマーカーを残したままコミット
する、といったミスは行として差分に現れますが、linter はファイル全体を見るため既存コードの
違反で大量に落ちてしまい、結局オフにされがちです。この検査は**差分の追加行だけ**を見るので、
「新規混入だけを止める」が成立します。

起動粒度は [per-commit](../granularity.md) 固定です。範囲内のコミットごとに 1 回ずつ見るため、
後から該当行を消しても履歴には残った状態になり、検査は失敗し続けます。

## 設定

```yaml
checks:
  diff-content:
    type: diff-content
    deny:
      - pattern: '@ts-ignore|//\s*nolint|#\s*noqa'
        reason: '型/lint エラーの抑制'
      - pattern: '^<{7}|^={7}|^>{7}'
        reason: 'コンフリクトマーカー'
      - pattern: '\.(only|skip)\('
        reason: 'テストの絞り込み・スキップ残し'
        paths: '**/*.spec.js'
      - pattern: '^\s*(func Test|def test_)'
        reason: 'テストの削除'
        on: removed
```

**既定の deny はありません。** どの記法を違反とみなすかは利用者が書きます（Go/TypeScript/Python
などの言語固有の記法をこちら側にハードコードすることはありません）。`deny` が 0 件だと
起動時エラーになります（常に成功する無意味な検査を作らせないため）。

### `deny[].pattern` / `deny[].reason`（両方必須）

行に当てる正規表現（Go の [`regexp`](https://pkg.go.dev/regexp) 構文）と、違反表示に出す理由。
**1 行ずつ**当てるため、`^`/`$` はそのまま行頭・行末を意味します（`doc-sync` の `when` は差分
テキスト全体に当てる仕様のため `^[+-]` のように書く必要がありますが、こちらは不要です）。

`deny` は上から順に評価し、同じ行に複数のルールが一致したら最初に一致したルールの `reason` を
使います。

### `deny[].on`（省略可、既定 `added`）

`added` なら追加行、`removed` なら削除行を見ます。追加行に対する既定の用途（抑制コメント・
デバッグ出力の追加など）と、削除行に対する用途（テストの削除など）を書き分けられます。

### `deny[].paths`（省略可、既定は全ファイル）

[doublestar](https://github.com/bmatcuk/doublestar) パターン。指定するとそのファイルだけを
対象にします。省略時は変更された全ファイルが対象です。

## 挙動の細部

- 対象は追加・変更・コピー・改名されたファイル（削除は対象外）です。
- `on: removed` は「**変更されたファイルの中の削除行**」だけを見ます。ファイルごと削除された
  場合は検知しません（`ChangedFiles` は削除を含まないため）。ファイルの削除そのものを検知
  したい場合は [`unwanted-files`](unwanted-files.md) など別の検査を使ってください。
- ファイルのリネームでは、git が差分をどう出すかによって全行が追加/削除に見える場合があります。
- バイナリファイルの差分は `Binary files ... differ` の 1 行になり、`+`/`-` で始まらないため
  自然に対象から外れます。
- 行番号は `@@ -a,b +c,d @@` ハンクヘッダから相対的に算出します。`on: added` なら新ファイル側
  （`+c` 起点）、`on: removed` なら旧ファイル側（`-a` 起点）の番号です。
- 120 文字を超える行は表示時に切り詰めます。

## 出力

```
diff-content の検査に失敗しました（a1b2c3d feat: ...）。

  型/lint エラーの抑制:
    - src/api/client.ts:42: // @ts-ignore
```

同じ理由の違反は 1 つにまとめて表示されます。

## 免除

[免除トレーラ](../exemptions.md)が使えます。既定のトレーラ名は `Diff-Content` です
（キー名を変えていれば、そのキーから生成された名前になります）。ただし per-commit 粒度
なので、免除トレーラは違反した**そのコミット自身**のメッセージに、理由を添えて書く必要が
あります（例: `Diff-Content: skip 外部 API の型定義が壊れているため一時的に抑制`）。

## レシピ（設定例。既定値ではありません）

以下は「よくある違反パターンの書き方の例」であり、spotter が既定で持つ値ではありません。
必要なものだけを自分のプロジェクトの `.spotter.yml` にコピーして使ってください。

### 型/lint エラーの抑制コメント

```yaml
- pattern: '@ts-ignore|@ts-expect-error'
  reason: 'TypeScript の型エラー抑制'
- pattern: '//\s*nolint'
  reason: 'Go の lint 抑制'
- pattern: '#\s*noqa'
  reason: 'Python の lint 抑制'
- pattern: '//\s*eslint-disable'
  reason: 'ESLint の抑制'
```

### テストの skip / only 残し

```yaml
- pattern: '\.(only|skip)\('
  reason: 'テストの絞り込み・スキップ残し'
  paths: '**/*.{test,spec}.{js,ts}'
- pattern: '^\s*(t\.Skip|t\.SkipNow)\('
  reason: 'Go テストのスキップ残し'
  paths: '**/*_test.go'
- pattern: '@pytest\.mark\.skip'
  reason: 'pytest のスキップ残し'
  paths: '**/test_*.py'
```

### デバッグ出力・コンフリクトマーカー

```yaml
- pattern: '^<{7}|^={7}|^>{7}'
  reason: 'コンフリクトマーカー'
- pattern: '\bconsole\.log\('
  reason: 'デバッグ出力の残留'
- pattern: '^\s*fmt\.Println\('
  reason: 'デバッグ出力の残留'
```

### テストの削除

```yaml
- pattern: '^\s*func Test\w+\('
  reason: 'Go テストの削除'
  on: removed
  paths: '**/*_test.go'
- pattern: '^\s*(it|test)\('
  reason: 'JS/TS テストの削除'
  on: removed
  paths: '**/*.{test,spec}.{js,ts}'
```
