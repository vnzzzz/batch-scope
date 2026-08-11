# BatchScope

BatchScopeは、バッチジョブの定義から、指定したジョブまたはジョブネットの後続にあるリミット設定と、そこまでの依存経路を洗い出す静的解析サービスです。

各ジョブマネージャーの定義を共通スナップショットに変換して取り込みます。ジョブマネージャーへ直接接続したり、現在の実行状態や業務日から対応期限を計算したりすることはありません。

## Quick Start

リポジトリに含まれるデモスナップショットを使うと、取込から後続リミット解析まで一通り試せます。
Go、`curl`、`jq`、`tar`が必要です。

```bash
git clone https://github.com/vnzzzz/batch-scope.git
cd batch-scope
make smoke
```

`make smoke`は一時的にBatchScopeを起動し、デモスナップショットの取込、`JOB-A`の検索、後続リミット解析までを実行して結果を要約します。

APIを手動で試す場合は、まず別のターミナルでBatchScopeを起動します。

```bash
go run ./cmd/batchscope serve
```

次にデモスナップショットを取り込み、検索できる状態になるまで待ってからAPIを呼び出します。

```bash
tar -C examples/demo/snapshot -czf /tmp/batchscope-demo.tar.gz \
  manifest.json nodes.ndjson relations.ndjson

curl -fsS -X POST \
  -H 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
  --data-binary @/tmp/batchscope-demo.tar.gz \
  http://127.0.0.1:8080/v1/snapshot-imports

until curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; do sleep 1; done

curl -fsS 'http://127.0.0.1:8080/v1/targets?query=JOB-A' | jq
curl -fsS 'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' | jq
```

GitHub Releaseの単体バイナリは、アーカイブを展開して `./batchscope serve` で起動できます。起動後は `http://127.0.0.1:8080/docs` でもAPIドキュメントを確認できます。

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
