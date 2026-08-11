# BatchScope

BatchScopeは、指定したジョブまたはジョブネットから影響を受けるリミット設定箇所を全件洗い出す静的解析サービスです。

ジョブマネージャーへ直接接続せず、製品固有の定義を共通スナップショットへ変換して取り込みます。
返すのは定義に保存されたリミットであり、現在時刻、実行状態、業務日を使った対応期限の計算は行いません。

## 利用

GitHub Releaseのアーカイブを展開し、単体バイナリを起動します。

```bash
./batchscope serve
```

スナップショット取込から対象検索、後続リミット解析までの一連の利用方法は[デモ](docs/development/demo.md)を参照してください。
APIの意味と保証は[API仕様](docs/design/api.md)、機械的なHTTP形式は[生成OpenAPI](docs/api/openapi.yaml)、取込データの形式は[スナップショット仕様](docs/design/canonical-snapshot.md)を正本とします。

Release archiveには、スナップショットの作成、取込、検索を支援するPublic SkillをJSON Schemaとともに同梱します。
ソースコードからの実行は[開発環境](docs/development/development.md)、公開成果物とコンテナイメージは[ビルドと公開](docs/development/build-and-release.md)を参照してください。

## ドキュメント

- [設計文書](docs/index.md)
- [デモ](docs/development/demo.md)
- [開発環境](docs/development/development.md)
- [ビルドと公開](docs/development/build-and-release.md)
- [コントリビューションガイド](CONTRIBUTING.md)

## ライセンス

[MIT License](LICENSE)で公開します。
