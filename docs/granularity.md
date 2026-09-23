# 検査の粒度（granularity）

`spotter check --range` で範囲を指定したとき、検査は「範囲全体をまとめて 1 回見る」のか
「コミットごとに 1 回ずつ見る」のかで意味が変わります。この違いを **granularity** と呼び、
検査ごとに固定です（`checks` 側からは上書きできません）。

staged モード（`--message` を使う commit-msg フック向け）では、この違いは意識する必要が
ありません。ステージ済みの変更を 1 回見るだけです。range モードで初めて効いてきます。

どちらのモードも**マージコミットは対象になりません**。range モードは対象コミットの
一覧を作るときにマージコミットそのものを除外し、staged モードは `MERGE_HEAD` が
残っている（＝マージの途中である）ときに検査自体をスキップします
（[hooks.md](hooks.md)）。取り込まれる側の各コミットは、
マージされる前に手元・CI どちらでもそれぞれ検査済みという前提です。

## 3 種類

### `squashed`（例: `doc-sync`, `companion-files`）

範囲全体を、最古のコミットの親から最新のコミットまでの 1 回の比較としてまとめて見ます。

これは「後からドキュメントを直すコミットを足せば通る」ことを意味します。1 コミット目で
コードだけ変更し、2 コミット目で対応するドキュメントを直せば、範囲全体で見たときには
両方変更されているので通ります。人間が実際に開発するときの「まずコード、後でまとめて
ドキュメント」という進め方を妨げません。

免除トレーラも、範囲内の**どれか 1 つ**のコミットメッセージにあれば効きます
（`RangeMessages` が範囲内の全コミットのメッセージをコミットごとに分けて返し、
免除判定はコミットごとにトレーラ段落を取り出して行うため。[exemptions.md](exemptions.md)
参照）。

### `per-commit`（例: `unwanted-files`, `commit-subject`, `diff-content`, `commit-intent`, `diff-size`）

範囲内のコミットごとに、そのコミット単体の親からの差分を 1 回ずつ見ます。

これは「後から消しても履歴には残る」性質を持つ検査に使います。一度コミットしてしまった
シークレットファイルや不正な commit-subject は、次のコミットで削除・修正しても git の
履歴上は残り続けます。squashed のように「範囲全体で見て最終的に無事ならOK」にはできません。

免除トレーラは、違反した**そのコミット自身**のメッセージに書く必要があります。

### `worktree`（例: `doc-paths`, `consistency`, `doc-links`）

staged/range の指定に関わらず、**現在の作業ツリーの中身を 1 回だけ**見ます。git の差分にも
コミットメッセージにも一切依存しません。

そのため worktree 粒度の検査には**免除トレーラの仕組みがありません**（何を免除するかを
コミット単位で判断する余地が無いため）。誤検知を抑えたい場合は、各検査が個別に持つ
`ignore` 相当のオプションを使ってください。

## まとめ

| granularity | 単位 | 免除トレーラ | 該当する組み込み検査 |
|---|---|---|---|
| `squashed` | 範囲全体で 1 回 | 範囲内のどれか 1 コミット | `doc-sync`, `companion-files` |
| `per-commit` | コミットごとに 1 回 | そのコミット自身 | `unwanted-files`, `commit-subject`, `diff-content`, `commit-intent`, `diff-size` |
| `worktree` | 作業ツリーを 1 回 | 無し | `doc-paths`, `consistency`, `doc-links` |

組み込み検査の granularity は固定ですが、[command 型](checks/command.md)（外部コマンド
検査）では `types.<name>.default.granularity` に `squashed` / `per-commit` / `worktree`
のいずれも指定できます。`worktree` を選んだ場合、外部コマンドには `--mode worktree` が
渡されます（[command 型の入出力契約](checks/command.md)参照）。

## 「終点」の参照

`Source.Exists()` は、比較の**終点**（staged ならインデックス、range なら `to` のツリー）に
そのパスのファイルが存在するかを返します。この「終点」は起動 1 回分の比較が指す終わりのことで、
squashed なら範囲全体の最新コミット、per-commit ならそのコミット単体を指します（worktree
粒度の検査には `Source` 自体が渡らないため関係ありません）。`Source.DeletedFiles()`
（削除されたファイルの一覧。改名元のパスも含む）とあわせて、検査が「削除されたか」
「終点で見て残っているか」を判定するために使えます。

## 免除の対象を検査の一部に絞る（ScopedExemptable）

`Runner` は任意で `ScopedExemptable`（`ExemptTargets() []string` を持つ）を実装できます。
実装した検査は、免除トレーラを検査全体ではなく `Violation.Target` 単位に絞れます
（[exemptions.md](exemptions.md) の範囲付き免除を参照）。実装していない検査に範囲付き
免除のトレーラを書くと、cli 側が黙って無視せず error にします。
