# `required_version` によるバージョン固定

```yaml
required_version: v0.1.0
```

`.spotter.yml` に検査を追加・変更したのに、手元の `spotter` バイナリが古いままだと
「新しい検査が走っていないだけなのに、手元では通ってしまう」状態になり得ます。これは
「手元で通ったものは CI でも通る」という `spotter` の前提を壊します。`required_version`
はこれを防ぐための下限バージョン指定です。

## 挙動

- `spotter check` / `spotter doctor` は、設定を読み込んだ直後にバージョンを検証します
- 手元のバイナリのバージョンが `required_version` 未満なら、**検査を一切実行せず**
  エラーで終了します
- バージョン文字列は `v1.2.3` 形式（`v` は省略可、末尾に `-rc1` のようなプレリリース
  識別子が付いていても無視して比較します）
- 比較は major/minor/patch の数値比較です（`v0.3.0` は `v0.2.9` より新しい、等）

## dev ビルドは判定不能として素通りする

`go install github.com/fuchigta/spotter/cmd/spotter@latest` や `go run ./cmd/spotter`
のように、`-ldflags -X` でバージョンを埋め込まずにビルドした場合、バイナリのバージョンは
`dev` になります。`dev` は正式なバージョン文字列として解釈できないため、
`required_version` の判定は**「判定不能」として常に満たしているとみなされます**
（エラーにはなりません）。

これは「ソースからビルドしているなら、少なくともそのソース時点の実装で動いている」という
前提に立った割り切りです。つまり `required_version` は**リリースバイナリを使っている環境
（`go install .../spotter@latest` で特定バージョンを固定した場合や、GitHub Releases の
バイナリを配布している場合）でこそ効く**仕組みで、`go run` でソースを直接使っている環境
（このリポジトリ自身の commit-msg フックや CI がそうです。[hooks.md](hooks.md) 参照）
では実質的に意味を持ちません。

## 確認方法

```
spotter doctor
```

`required_version` を設定していれば、手元のバイナリがそれを満たしているかを表示します。
満たしていない場合は `doctor` 自体も失敗（非 0 終了）します。

満たしていない場合は `spotter update` でバイナリ自体を更新できます。詳しくは
[update.md](update.md) を参照してください（`go run` でソースを直接使っている環境では
`git pull` 等でソース自体を更新することになるため対象外です）。
