# BatchScopeの実装指示

## 作業前に読む文書

次の順に確認する。

1. `docs/index.md`
2. `docs/development/development.md`
3. `CONTRIBUTING.md`
4. `docs/development/writing-style.md`
5. `docs/design/api.md`
6. `schema/`配下のJSON Schema

## 実装方針

- 現在のMVPで必要な処理だけを実装する。
- 実例が一つしかない段階では、プラグイン機構、汎用グラフ基盤、設定用DSL、過度なインターフェース分割を追加しない。
- 入力境界のID、親子関係、依存関係、リミットは検査する。
- 任意の補足情報は`attributes`、`locator`、`raw`、`evidence`で受け入れる。
- 曖昧検索や、サービス内部での依存関係の推測は行わない。
- 管理単位の親子関係と、ジョブの実行順を同じデータ構造で表さない。
- 新しいSQLiteの準備が終わるまでは、現在使用中のSQLiteを検索に使う。
- 同じ入力と検索条件には、同じ順序の結果を返す。
- 同じ仕様を複数の文書へコピーしない。

## 実行環境

CodexとClaude CodeはDev Container内で作業する。
Dev ContainerにはDocker CLIとDockerソケットを追加していない。

作業完了前に、Dev Container内で次を実行する。

```bash
make verify
```

Dockerfileまたはコンテナのビルド設定を変更した場合は、ホストまたはCIで次の確認が必要であることを報告する。

```bash
make image
```

エージェントは、Dockerを利用できないDev Container内で`make image`を実行しない。

## 公開仕様を変更する場合

APIまたは取込データの形式を変更する場合は、実装と同じ変更で次を更新する。

- 関連する`docs/`配下の文書
- `schema/`配下のJSON Schema
- `skills/batchscope/references/`配下の参照資料
- 必要に応じて`examples/demo/`配下の例

OpenAPIは手書きしない。
HumaによるAPI実装後にGoコードから生成し、生成手順と差分検査を追加する。

## 公開設定を変更する場合

バイナリ公開はGitHub Releasesだけを対象とする。
GHCR、Docker Hub、外部レジストリの認証設定を追加しない。

`.github/workflows/release.yml`または`scripts/build-release-artifacts.sh`を変更した場合は、Dev Container内で次を実行する。

```bash
make verify
```

公開設定を変更した場合は、MITライセンスを含む公開用成果物も確認する。

```bash
make release-artifacts VERSION=0.1.0
```
