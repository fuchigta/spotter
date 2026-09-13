# `.spotter.yml` 全体リファレンス

既定のパスは `.spotter.yml`（`--config` で変更可）です。トップレベルは 3 つのキーだけを
持ちます。

```yaml
required_version: v0.1.0

types:
  <type 名>: { ... }

checks:
  <キー>: { ... }
```

## `checks`

実際に実行される検査のインスタンス一覧です。キー名は自由に付けられます（表示名・
デフォルトの免除トレーラ名・`spotter check <キー>` での個別実行に使われます）。

```yaml
checks:
  <キー>:
    type: <組み込み type 名 | types に登録した名前>
    exempt:
      enable: true    # 省略時 true（commit-subject のみ false）
      trailer: Custom  # 省略時はキーから自動生成（例: doc-sync → Doc-Sync）
    # 以下は type ごとのフィールド
```

`type` は必須です。組み込み type（`doc-sync` / `unwanted-files` / `doc-paths` /
`commit-subject` / `consistency`）ならそのまま使えます。それ以外の名前を指定する場合は、
`types.<type>` に `command` を登録しておく必要があります（無ければ設定エラー）。

`type` ごとのフィールドは各検査のページを参照してください。

- [doc-sync](checks/doc-sync.md): `pairs`, `exclude`
- [unwanted-files](checks/unwanted-files.md): `max_bytes`, `deny`
- [doc-paths](checks/doc-paths.md): `docs`, `ignore`, `path_prefixes`
- [commit-subject](checks/commit-subject.md): `allowed_types`
- [consistency](checks/consistency.md): `sources`
- [command 型](checks/command.md): `types.<type>.schema` で宣言したオプション

同じ組み込み type を複数のキーでインスタンス化することもできます（例:
`doc-sync-frontend` と `doc-sync-backend` を別々の `pairs` で）。免除トレーラの既定名は
type ではなく**キー名**から生成されるため、この場合でもトレーラ名は衝突しません。

## `types`

次の**どちらか一方**の意味を持ちます（両方を同時に満たすことはできません）。

### 1. 組み込み type の `default` 上書き

```yaml
types:
  commit-subject:
    default:
      exempt:
        enable: false
```

組み込み type と同名のキーを置くと、その type 全体の既定値（現状は `exempt` のみ）を
上書きできます。`command` / `schema` / `transport` / `default.granularity` はここでは
指定できません（組み込み type は Go 側で挙動が固定されているため）。

### 2. command 型（外部コマンド検査）の登録

```yaml
types:
  my-check:
    command: ./scripts/my-check.sh
    transport: file          # file（既定） | args | env
    schema:
      simple: { ... }        # または json-schema
    default:
      granularity: squashed  # squashed | per-commit | worktree（必須）
```

組み込み type と衝突しない名前を使う場合、`command` の登録として扱われます。
`default.granularity` は必須です。詳細は [checks/command.md](checks/command.md) を
参照してください。

## `required_version`

```yaml
required_version: v0.1.0
```

このバージョン未満の `spotter` バイナリには検査を実行させません。詳細は
[versioning.md](versioning.md) を参照してください。省略可能です。

## `exempt` の解決順

`checks.<key>.exempt` の `enable`/`trailer` は、それぞれ次の優先順位でフォールバックします
（後段が指定されていれば優先）。

1. システム既定（`commit-subject` のみ無効、他は有効。トレーラ名はキーから自動生成）
2. `types.<type>.default.exempt`
3. `checks.<key>.exempt`

詳しくは [exemptions.md](exemptions.md) を参照してください。
