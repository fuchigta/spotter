# doc-sync

コードとドキュメントの対応表を持たせ、片方だけが変更されていたら失敗させる検査です。
「実装は直したのに README を直し忘れる」を防ぎます。

起動粒度は [squashed](../granularity.md) 固定です。範囲全体をまとめて 1 回見るので、
後からドキュメントを直すコミットを追加すれば通ります。

## 設定

```yaml
checks:
  doc-sync:
    type: doc-sync
    pairs:
      - paths: 'internal/cli/*.go'
        doc: README.md
      - paths: 'internal/check/*/*.go'
        doc: README.md
        when: '^\+.*Granularity'
    exclude:
      - '*_mock.go'
```

### `pairs[].paths`（必須）

対応させたいコード側のパスパターン。[glob-patterns.md](../glob-patterns.md) の意味論（`*` は
`/` にもマッチする）でマッチします。

### `pairs[].doc`（必須）

対応するドキュメントのパス（リポジトリルートからの相対パス、1 ファイル）。この検査は
「`paths` に一致する変更があったのに `doc` が変更ファイル一覧に無い」場合だけ違反にします。
つまり `doc` 側が少しでも変更されていれば、その `paths` に対する条件は満たされたとみなします
（内容の対応まではチェックしません）。

### `pairs[].when`（省略可）

指定すると、`paths` に一致したファイルの差分行（`git diff -U0` 相当、コンテキスト無し）に
この正規表現がマッチした場合だけを対象にします。例えば「シグネチャが変わったときだけ」
「特定の識別子が増減したときだけ」のように条件を絞りたい場合に使います。省略時は `paths`
に一致した変更を無条件に対象にします。

### `exclude`（省略可、トップレベル）

すべての `pairs` に共通で適用される除外パターン（glob）。

テストファイルは自動では除外されません。以前は `_test.go` サフィックスを Go 決め打ちで
常に除外していましたが、JavaScript の `*.test.js` や Python の `test_*.py` のような
他言語のテストファイル慣習には対応できないため撤廃しました
（[fuchigta/spotter#2](https://github.com/fuchigta/spotter/issues/2)）。Go プロジェクトで
テストファイルを除外したい場合は、次のように明示してください。

```yaml
checks:
  doc-sync:
    type: doc-sync
    pairs: [...]
    exclude:
      - '*_test.go'
```

## 挙動の細部

- 1 回の起動で複数の `pairs` を独立に評価します。違反は `pairs` ごとに個別のブロックとして
  表示されます。
- `doc` 自体が変更されていれば、その `pairs` エントリはスキップされます（`paths` 側の
  マッチ有無を見るまでもなく合格）。
- 変更ファイルが 0 件（差分が無い）なら検査自体をスキップします。

## 免除

[免除トレーラ](../exemptions.md)が使えます。既定のトレーラ名はキー名から生成されるので、
`doc-sync` というキーなら `Doc-Sync: skip <理由>` です。
