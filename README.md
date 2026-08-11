# BatchScope

BatchScopeは、バッチジョブの定義から、指定したジョブまたはジョブネットの後続にあるリミット設定と、そこまでの依存経路を洗い出す静的解析サービスです。

各ジョブマネージャーの定義を共通スナップショットに変換して取り込み、HTTP APIで検索・解析します。ジョブマネージャーへ直接接続したり、現在の実行状態や業務日から対応期限を計算したりすることはありません。

## Quick Start

まず「1. Releaseバイナリを展開する」「2. BatchScopeを起動する」を順に実行します。
起動後は、デモで動作を確認する **A** と、Public Skillで実際のジョブ定義を使う **B** のどちらかへ進みます。

### 1. Releaseバイナリを展開する

[GitHub Releases](https://github.com/vnzzzz/batch-scope/releases)から利用環境に合うOS別アーカイブを取得します。Linuxでは`amd64`と`arm64`を配布しています。

```bash
# 例: Linux amd64
tar -xzf batchscope_*_linux_amd64.tar.gz
```

展開すると、`batchscope_*_linux_amd64/`配下に`batchscope`、`README.md`、`LICENSE`、Public Skillが配置されます。

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

`status`が`ok`なら準備完了です。

```json
{
  "status": "ok"
}
```

起動後は、次のどちらかへ進みます。

- **A. デモデータで試す** — まずBatchScopeの動作を確認したい場合
- **B. Public Skillで実データを使う** — 手元のジョブ定義を解析したい場合

### A. デモデータで試す

同じReleaseから`batchscope_demo_snapshot.tar.gz`を取得します。デモでは`curl`と`jq`を使用します。

#### A-1. スナップショットを取り込む

```bash
curl -i -X POST \
  -H 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
  --data-binary @batchscope_demo_snapshot.tar.gz \
  http://127.0.0.1:8080/v1/snapshot-imports
```

取込を受け付けると`202 Accepted`になり、`Location`に取込状況の確認先が返ります。

```text
HTTP/1.1 202 Accepted
Location: /v1/snapshot-imports/...
Retry-After: ...
```

#### A-2. 検索可能になったことを確認する

```bash
curl -sS http://127.0.0.1:8080/readyz | jq
```

取込中は`not_ready`です。数秒おいて再実行し、次の状態になれば取込完了です。

```json
{
  "status": "ready",
  "reason": "snapshot_loaded"
}
```

#### A-3. ジョブを検索する

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/targets?query=JOB-A&type=job' \
  | jq '.items[] | {id, name, path, type}'
```

デモの`JOB-A`が1件見つかります。

```json
{
  "id": "JOB-A",
  "name": "売上抽出",
  "path": "/DAILY/SALES/JOB-A",
  "type": "job"
}
```

#### A-4. 後続リミットを解析する

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' \
  | jq
```

`target.id`が`JOB-A`となり、後続のリミット、依存経路、循環、リミット未通過経路を含むJSONが返ればデモ完了です。

### B. Public Skillで実データを使う

OS別アーカイブの`skills/public/batchscope/`は、ジョブマネージャーの出力やジョブ定義から共通スナップショットを作り、BatchScopeへ取り込んで解析するためのエージェント向けSkillです。このルートではデモデータは使用しません。

1. Claude CodeやCodexなど、利用するエージェントの方法に従って`skills/public/batchscope/`を読み込ませます。
2. ジョブ定義、実行スクリプト、関連資料をエージェントから参照できるようにします。
3. 例えば次のように依頼します。

```text
BatchScope Public Skillを使って、<ジョブ定義のパス> からBatchScope用スナップショットを作成してください。
起動中の http://127.0.0.1:8080 に取り込み、<調べたいジョブ名またはジョブネット名> の後続リミットと依存経路を調べてください。
```

スナップショットの生成・検査・取込後、指定した対象の後続リミットと依存経路が得られれば完了です。JSON Schemaと変換手順の詳細はPublic Skillを参照してください。

## ドキュメント

- **使い方**
  - [デモ](docs/development/demo.md) — デモスナップショットとAPI利用例
  - [Public Skill](skills/public/batchscope/SKILL.md) — 実際のジョブ定義をスナップショットへ変換して利用する手順
- **公開仕様**
  - [API仕様](docs/design/api.md) — APIの意味と利用者へ保証する動作
    - [OpenAPI](docs/api/openapi.yaml) — HTTPのパス、パラメーター、JSON形式
  - [スナップショット仕様](docs/design/canonical-snapshot.md) — 取込データの意味とレコード間制約
- **設計・開発**
  - [設計文書](docs/index.md) — 設計文書全体への入口
  - [開発環境](docs/development/development.md) — ソースコードからの開発と実行
  - [ビルドと公開](docs/development/build-and-release.md) — Release成果物とコンテナイメージの扱い
  - [コントリビューションガイド](CONTRIBUTING.md) — 開発への参加方法とブランチ運用

## ライセンス

[MIT License](LICENSE)で公開します。
