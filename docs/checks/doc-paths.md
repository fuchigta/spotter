# doc-paths

ドキュメントがバッククォートで名指ししているコードのパスが、実際にリポジトリに存在するかを
確認する検査です。`doc-sync` が守るのは「一緒に直したか」だけなので、その先の「参照先が
まだ実在するか」（リネーム・削除の取り残し）はこちらが担当します。

起動粒度は [worktree](../granularity.md) 固定です。staged/range を問わず現在の作業ツリーを
1 回だけ見るため、**免除トレーラを持ちません**。

## 設定

```yaml
checks:
  doc-paths:
    type: doc-paths
    docs:
      - README.md
      - docs/*.md
    ignore:
      - internal/foo/bar.go
    path_prefixes: [internal, cmd, scripts]
```

### `docs`（省略可）

対象ドキュメントの一覧。glob も使えます（`filepath.Glob` 相当。`**` は非対応です）。

**省略時の既定**は次の通りです。

- 固定ファイル: `README.md`, `CLAUDE.md`
- 追加のグロブ: `docs/*.md`, `.github/*.md`

対象ファイルが存在しなければ黙ってスキップします（無いこと自体はこの検査の対象外）。

### `ignore`（省略可）

誤検知として無視したい候補パスの完全一致リスト。後述の抽出結果がここに載っていれば
チェックしません。

## 候補の抽出方法

対象ドキュメントの中身から、バッククォートで囲まれた文字列（`` `...` ``）を全て拾い、
その中から次のいずれかで始まるものだけを実在確認の候補にします。

```
<path_prefixes の各値>/  .githooks/  .github/
```

それ以外（`spotter check` のようなコマンド例や、`.spotter.yml` のような単なるファイル名の
言及）は候補になりません。誤検知を避けるための意図的な絞り込みです。

### `path_prefixes`（省略可）

候補と認識するディレクトリ接頭辞の一覧。**省略時の既定値は `[internal, cmd, scripts]`**
（Go のモジュールレイアウト規約）です。以前はこの一覧がソースにハードコードされていて
上書きできず、`src/`・`lib/`・`pkg/` のような他言語で一般的なディレクトリを使うプロジェクト
では実質的に何も検知できませんでした
（[fuchigta/spotter#2](https://github.com/fuchigta/spotter/issues/2)）。他言語のリポジトリ
では、自分のディレクトリレイアウトに合わせて上書きしてください。

```yaml
checks:
  doc-paths:
    type: doc-paths
    path_prefixes: [src, lib, pkg]
```

`.githooks/`・`.github/` は言語ではなく spotter/git 自身の慣習なので、`path_prefixes` の
指定に関わらず常に候補に含まれます。

## 実在確認のルール

- 候補に `*` を含む場合は glob として扱い、1 件以上一致すれば OK とします（例示的なパスの
  書き方を許容するため）。
- それ以外は `os.Stat` でそのまま実在確認します。

## 免除

worktree 粒度のためコミットメッセージに依存せず、免除トレーラの仕組み自体がありません。
誤検知は `ignore` で個別に潰してください。
