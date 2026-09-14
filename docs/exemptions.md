# 免除トレーラ

検査ごとに、コミットメッセージ本文の**トレーラ**で免除できます。

```
Doc-Sync: skip 対応するドキュメントは無い
```

## なぜ環境変数ではなくコミットメッセージなのか

免除の判断根拠をコミットメッセージに置いているのは、**手元のフックと CI が同じコミット
メッセージを見る**ようにするためです。もし環境変数（例: `SKIP_DOC_SYNC=1`）で免除できる
仕組みにすると、ローカルでは通ったのに、その環境変数を設定していない CI では落ちる
（あるいはその逆）という状態になり得ます。コミットメッセージなら、手元で書いた免除の
判断がそのまま git 履歴に乗って CI にも届くので、判断がずれません。

## トレーラ名

既定では `checks.<キー>` のキー名をタイトルケースに変換したものです。

```
doc-sync            → Doc-Sync
commit-types-consistency → Commit-Types-Consistency
```

`checks.<key>.exempt.trailer` で明示的に上書きできます。

```yaml
checks:
  doc-sync:
    type: doc-sync
    exempt:
      trailer: Custom-Skip
```

## 有効/無効

```yaml
checks:
  <key>:
    exempt:
      enable: false
```

- 既定値は**全ての組み込み検査で `true`**、ただし [`commit-subject`](checks/commit-subject.md)
  だけ `false`（メッセージの体裁そのものを検証する検査なので、免除トレーラで体裁検証を
  回避できてしまうと矛盾するため）。
- [`worktree` 粒度](granularity.md)の検査（`doc-paths`, `consistency`, `doc-links`）は、
  免除トレーラの仕組み自体を持ちません（`enable` を書いても意味を持ちません）。

解決順は次の通りです（後段が優先）。

1. システム既定（検査の type ごとの `DefaultExemptEnable`）
2. `types.<type>.default.exempt`（type 全体の既定値の上書き）
3. `checks.<key>.exempt`（そのインスタンス個別の上書き）

## 書式のルール

```
<Trailer>: skip <理由>
```

- トレーラ名の大文字小文字は区別しません
- `skip` の後に**1 文字以上の理由が必須**です。理由が空の免除は拒否されます
  （「なぜ免除したか」を必ず残す運用を前提にしています）
- 複数行のコミットメッセージのうち、この形式に合う行が 1 行でもあれば免除が成立します

## 粒度による違い

免除トレーラを「どのコミットのメッセージに書けば効くか」は、その検査の
[granularity](granularity.md) によって変わります。

- `squashed`: 範囲内のどのコミットに書いても良い（範囲内の全メッセージを連結して判定）
- `per-commit`: 違反したそのコミット自身に書く必要がある
- `worktree`: 免除トレーラの仕組みが無い
