# BatchScope

BatchScopeは、障害発生時に巨大なジョブ定義を人間が手作業でたどることなく、指定したジョブまたはジョブネットから影響を受けるリミット設定箇所を全件洗い出す静的解析サービスです。

ジョブマネージャーへ直接接続せず、製品固有の定義を共通のスナップショットへ変換して取り込みます。
ファイル、ジョブ状態、外部イベントを介した依存関係も扱えます。

BatchScopeが返すリミットは定義に保存された設定です。
現在時刻、実行状態、業務日を使った対応期限の計算は行いません。

## 利用方法

| 方法 | 用途 |
|---|---|
| GitHub Releasesのバイナリ | Dockerを使わずにBatchScopeを起動する |
| ソースコードから実行 | 開発版の確認、変更を加えた利用 |
| ソースコードからイメージを作成 | DockerまたはCloud Runでの利用 |

GitHub Releaseのアーカイブを展開し、単体バイナリを起動します。

```bash
./batchscope serve
```

ソースコードからの実行方法は[開発環境](https://github.com/vnzzzz/batch-scope/blob/main/docs/development/development.md)、コンテナイメージの作成と対応OSは[ビルドと公開](https://github.com/vnzzzz/batch-scope/blob/main/docs/development/build-and-release.md)を参照してください。

## HTTP API

APIの意味と保証は[API仕様](https://github.com/vnzzzz/batch-scope/blob/main/docs/design/api.md)、パス、パラメーター、JSON項目の機械的な形式は[生成OpenAPI](https://github.com/vnzzzz/batch-scope/blob/main/docs/api/openapi.yaml)を正本とします。

| Method | Path | 用途 |
|---|---|---|
| `GET` | `/healthz` | プロセスの生存確認 |
| `GET` | `/readyz` | 検索可能な状態の確認 |
| `GET` | `/v1/status` | サービス状態の取得 |
| `POST` | `/v1/snapshot-imports` | スナップショットの送信 |
| `GET` | `/v1/snapshot-imports/{importId}` | 取込状況の取得 |
| `GET` | `/v1/snapshots/current` | 現在使用中のスナップショット情報の取得 |
| `GET` | `/v1/targets` | ジョブまたはジョブネットの完全一致検索 |
| `GET` | `/v1/downstream-limit-analysis` | 後続リミットと依存経路の取得 |

## 最小利用フロー

1. [取込データの形式](https://github.com/vnzzzz/batch-scope/blob/main/docs/design/canonical-snapshot.md)に従ってスナップショットを作成する。
2. `POST /v1/snapshot-imports`で送信し、`Location`が示すURIを取込成功まで確認する。
3. 対象IDが不明な場合は、`GET /v1/targets`で完全一致検索する。
4. `GET /v1/downstream-limit-analysis`へ対象IDを指定し、後続リミットと依存経路を取得する。

## Public Skill

GitHub Releaseの各アーカイブには、スナップショットの作成、取込、検索を支援する`skills/public/batchscope`を同梱します。
Public Skillには参照資料とJSON Schemaが含まれるため、このディレクトリだけを取り出して利用できます。

## ドキュメント

- [設計文書](https://github.com/vnzzzz/batch-scope/blob/main/docs/index.md)
- [開発環境](https://github.com/vnzzzz/batch-scope/blob/main/docs/development/development.md)
- [デモ](https://github.com/vnzzzz/batch-scope/blob/main/docs/development/demo.md)
- [コントリビューションガイド](https://github.com/vnzzzz/batch-scope/blob/main/CONTRIBUTING.md)
- [ビルドと公開](https://github.com/vnzzzz/batch-scope/blob/main/docs/development/build-and-release.md)

## リポジトリ構成

```text
batchscope/
├── cmd/                       # Goのエントリーポイント
├── internal/                  # API、取込、依存関係の検索、SQLite
├── schema/                    # 取込データのJSON Schema
├── docs/                      # 設計文書と開発手順
├── examples/demo/             # デモ用の取込データとレスポンス例
├── scripts/                   # 開発、表示、公開用の補助スクリプト
├── skills/                    # PublicとInternalのエージェントスキル
└── Dockerfile                 # 本番用イメージ
```

## ライセンス

[MIT License](LICENSE)で公開します。
