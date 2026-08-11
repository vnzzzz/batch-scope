# デモ

GitHub Releaseで配布する実行バイナリとデモスナップショットを使い、スナップショット取込から後続リミット解析までを確認します。

## Releaseで試す

[GitHub Releases](https://github.com/vnzzzz/batch-scope/releases)から、利用環境に合うOS別アーカイブと`batchscope_demo_snapshot.tar.gz`を取得します。
以下ではLinux amd64を例にします。`curl`と`jq`を使用します。

### 1. バイナリを展開する

```bash
tar -xzf batchscope_*_linux_amd64.tar.gz
```

`batchscope_*_linux_amd64/`配下に`batchscope`、`README.md`、`LICENSE`、Public Skillが展開されます。

### 2. BatchScopeを起動する

データ保存先を作成します。

```bash
mkdir batchscope-data
```

BatchScopeを起動します。このターミナルは起動したままにします。

```bash
./batchscope_*_linux_amd64/batchscope serve -data-dir ./batchscope-data
```

`BatchScope listening`を含むログが出れば起動しています。既定の待受ポートは`8080`です。

別のターミナルから応答を確認します。

```bash
curl -fsS http://127.0.0.1:8080/healthz | jq
```

`status`が`ok`なら次へ進めます。

```json
{
  "status": "ok"
}
```

### 3. デモスナップショットを取り込む

`batchscope_demo_snapshot.tar.gz`があるディレクトリで実行します。

```bash
curl -i -X POST \
  -H 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
  --data-binary @batchscope_demo_snapshot.tar.gz \
  http://127.0.0.1:8080/v1/snapshot-imports
```

取込を受け付けると`202 Accepted`になり、`Location`と`Retry-After`が返ります。

```text
HTTP/1.1 202 Accepted
Location: /v1/snapshot-imports/...
Retry-After: ...
```

取込は非同期です。数秒おいて準備状態を確認します。

```bash
curl -sS http://127.0.0.1:8080/readyz | jq
```

次の状態になれば検索できます。

```json
{
  "status": "ready",
  "reason": "snapshot_loaded"
}
```

### 4. ジョブを検索する

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/targets?query=JOB-A&type=job' \
  | jq '.items[] | {id, name, path, type}'
```

デモの`JOB-A`が1件返ります。

```json
{
  "id": "JOB-A",
  "name": "売上抽出",
  "path": "/DAILY/SALES/JOB-A",
  "type": "job"
}
```

### 5. 後続リミットを解析する

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' \
  | jq
```

`target.id`が`JOB-A`となり、後続のリミット、依存経路、循環、リミット未通過経路を含むJSONが返れば確認完了です。

APIの意味やレスポンス項目の詳細は[API仕様](../design/api.md)を参照してください。

## デモデータ

sourceでは次のファイルを正本として管理します。

```text
examples/demo/
├── snapshot/
│   ├── manifest.json
│   ├── nodes.ndjson
│   └── relations.ndjson
└── responses/
    └── downstream-limit-analysis.json
```

Releaseの`batchscope_demo_snapshot.tar.gz`は`examples/demo/snapshot/`から機械的に生成します。
データの内容は[`../../examples/demo/README.md`](../../examples/demo/README.md)を参照してください。

## 開発時の確認

Dev Containerでは、固定したレスポンス例を整形して表示できます。

```bash
make demo-view
```

出力では、検索対象、各種リミット、依存経路、`hiddenConnections`、循環、リミット未通過経路などを確認できます。

起動中のBatchScopeから取得したレスポンスを整形する場合は、次を実行します。

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' \
  | ./scripts/show-limit-analysis.sh
```

このスクリプトは開発時の確認にだけ使用し、本番用コンテナイメージには含めません。
