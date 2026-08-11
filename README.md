# BatchScope

BatchScopeは、バッチジョブの定義から、指定したジョブまたはジョブネットの後続にあるリミット設定と、そこまでの依存経路を洗い出す静的解析サービスです。

各ジョブマネージャーの定義を共通スナップショットに変換して取り込み、HTTP APIで検索・解析します。ジョブマネージャーへ直接接続したり、現在の実行状態や業務日から対応期限を計算したりすることはありません。

## Quick Start

### 1. Releaseバイナリを起動する

[GitHub Releases](https://github.com/vnzzzz/batch-scope/releases)から、利用環境に合うOS別アーカイブと`batchscope_demo_snapshot.tar.gz`を取得します。
Linuxでは`amd64`と`arm64`のtar.gzを配布しています。

まずOS別アーカイブを展開します。

```bash
# 例: Linux amd64
tar -xzf batchscope_*_linux_amd64.tar.gz
```

バイナリを確認します。

```bash
./batchscope_*_linux_amd64/batchscope version
```

試用データの保存先を明示的に作成します。

```bash
mkdir batchscope-demo-data
```

作成したディレクトリを指定してBatchScopeを起動します。

```bash
./batchscope_*_linux_amd64/batchscope serve -data-dir ./batchscope-demo-data
```

既定では`0.0.0.0:8080`で待ち受けます。起動後は`http://127.0.0.1:8080/docs`でAPIドキュメントを確認できます。

### 2. デモデータで試す

BatchScopeを起動したターミナルはそのままにして、Releaseから取得した`batchscope_demo_snapshot.tar.gz`があるディレクトリを別のターミナルで開きます。`curl`と`jq`が必要です。

まず、デモスナップショットをBatchScopeへ送信します。

```bash
curl -i -X POST \
  -H 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
  --data-binary @batchscope_demo_snapshot.tar.gz \
  http://127.0.0.1:8080/v1/snapshot-imports
```

取込が完了して検索可能になるまで待ちます。

```bash
until curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; do
  sleep 1
done
```

デモの`JOB-A`を検索します。

```bash
curl -fsS 'http://127.0.0.1:8080/v1/targets?query=JOB-A' | jq
```

`JOB-A`から後続のリミット設定と依存経路を解析します。

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' \
  | jq
```

### 3. 実際のジョブ定義をPublic Skillで変換する

OS別のReleaseアーカイブには`skills/public/batchscope/`を同梱しています。
Public Skillは、既存のジョブマネージャーの出力やジョブ定義を読み取り、BatchScopeが受け取る共通スナップショットへ変換して、取込・検索するためのエージェント向け手順です。

Claude CodeやCodexなど、利用するエージェントのSkill配置方法に従って`skills/public/batchscope/`を読み込ませます。ジョブマネージャーから取得した定義ファイルや実行スクリプトを参照できる状態にしたうえで、例えば次のように依頼します。

```text
BatchScope Public Skillを使って、<ジョブ定義のパス> からBatchScope用スナップショットを作成してください。
起動中の http://127.0.0.1:8080 に取り込み、<調べたいジョブ名またはジョブネット名> の後続リミットと依存経路を調べてください。
```

スナップショットのJSON SchemaはPublic Skill内に同梱されています。変換規則や取込・検索の詳細はPublic Skillを参照し、READMEには重複して記載しません。

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
