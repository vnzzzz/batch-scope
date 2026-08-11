# BatchScope

BatchScopeは、バッチジョブの定義を静的に解析し、指定したジョブまたはジョブネットから後続の依存関係をたどって、リミット設定とそこまでの経路を確認するためのサービスです。

ジョブマネージャーへ直接接続するのではなく、製品固有の定義を共通スナップショットへ変換して取り込みます。現在時刻や実行状態、業務日から「いつまでに対応すべきか」を計算するサービスではありません。

## Quick Start

最短で一連の動作を確認するには、リポジトリをcloneしてデモスナップショットを使います。
Go、`curl`、`jq`、`tar`が必要です。

```bash
git clone https://github.com/vnzzzz/batch-scope.git
cd batch-scope
make smoke
```

`make smoke`は一時的にBatchScopeを起動し、`examples/demo/snapshot`を取り込んで、`JOB-A`の検索と後続リミット解析まで実行します。結果は人が確認しやすい形で表示されます。

GitHub Releaseの単体バイナリを使う場合は、アーカイブを展開して次のように起動します。

```bash
./batchscope serve
```

起動後は `http://127.0.0.1:8080/docs` でAPIドキュメントを確認できます。詳しい利用例や仕様は下のドキュメント一覧から参照してください。

## Public Skill

GitHub Releaseのアーカイブには、スナップショットの作成、取込、検索を支援するPublic SkillをJSON Schemaとともに同梱します。

## ドキュメント

| 文書 | 確認できること |
|---|---|
| [デモ](docs/development/demo.md) | デモスナップショットとAPI利用例 |
| [API仕様](docs/design/api.md) | APIの意味と利用者へ保証する動作 |
| [OpenAPI](docs/api/openapi.yaml) | HTTPのパス、パラメーター、JSON形式 |
| [スナップショット仕様](docs/design/canonical-snapshot.md) | 取込データの意味とレコード間制約 |
| [設計文書](docs/index.md) | システム全体の設計文書への入口 |
| [開発環境](docs/development/development.md) | ソースコードからの開発と実行 |
| [ビルドと公開](docs/development/build-and-release.md) | Release成果物とコンテナイメージの扱い |
| [コントリビューションガイド](CONTRIBUTING.md) | 開発への参加方法とブランチ運用 |

## ライセンス

[MIT License](LICENSE)で公開します。
