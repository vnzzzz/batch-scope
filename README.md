# BatchScope

BatchScopeは、障害発生時に巨大なジョブ定義を人間が手作業でたどることなく、指定したジョブまたはジョブネットから影響を受けるリミット設定箇所を全件洗い出す静的解析サービスです。

取り込んだ定義を解析するHTTPサービスであり、現在時刻や実行状態を使った対応期限の計算は行いません。

ジョブマネージャー固有の定義は、共通形式へ変換してから取り込みます。
ファイル、ジョブ状態、外部イベントを介した依存関係も扱えます。

> [!IMPORTANT]
> 現在は初期実装の段階です。
> ヘルスチェックと状態確認だけを実装しており、スナップショット取込、SQLiteへの格納、対象検索、後続リミット検索は未実装です。

## 利用方法

| 方法 | 用途 |
|---|---|
| GitHub Releasesのバイナリ | Dockerを使わずにBatchScopeを起動する |
| ソースコードから実行 | 開発版の確認、変更を加えた利用 |
| ソースコードからイメージを作成 | DockerまたはCloud Runでの利用 |

単体バイナリは、リリース後に次のコマンドで起動します。

```bash
./batchscope serve
```

ソースコードからコンテナイメージを作成する場合は、Dockerを利用できるホストで次を実行します。

```bash
make image
make image-run
```

対応OS、成果物、公開手順は[ビルドと公開](docs/development/build-and-release.md)を参照してください。

## ドキュメント

- [設計文書](docs/index.md)
- [開発環境](docs/development/development.md)
- [デモ](docs/development/demo.md)
- [コントリビューションガイド](CONTRIBUTING.md)
- [ビルドと公開](docs/development/build-and-release.md)

## 主なコマンド

| 実行環境 | コマンド | 用途 |
|---|---|---|
| Dev Container | `make verify` | 静的検査とテスト |
| Dev Container | `make run` | 開発中のサービス起動 |
| Dev Container | `make demo-view` | APIレスポンス例の見やすい表示 |
| Dev Container | `make release-artifacts VERSION=0.1.0` | 公開用バイナリの作成確認 |
| ホスト | `make image` | 本番用コンテナイメージの作成 |
| ホスト | `make image-run` | 作成済みイメージのローカル起動 |

Dev ContainerにはDocker CLIとDockerソケットを追加していません。
コンテナイメージの操作は、Dockerを利用できるホストで実行します。

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
