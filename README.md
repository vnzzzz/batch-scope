# BatchScope

BatchScopeは、バッチジョブの定義から、指定したジョブまたはジョブネットの後続にあるリミット設定と、そこまでの依存経路を洗い出す静的解析サービスです。

各ジョブマネージャーの定義を共通スナップショットに変換して取り込み、HTTP APIで検索・解析します。ジョブマネージャーへ直接接続したり、現在の実行状態や業務日から対応期限を計算したりすることはありません。

## Quick Start

### 1. Releaseバイナリを起動する

[GitHub Releases](https://github.com/vnzzzz/batch-scope/releases)から利用環境に合うアーカイブを取得します。
Linuxでは`amd64`と`arm64`のtar.gzを配布しています。

```bash
# 例: Linux amd64
 tar -xzf batchscope_*_linux_amd64.tar.gz
cd batchscope_*_linux_amd64
./batchscope version
./batchscope serve
```

既定では`0.0.0.0:8080`で待ち受けます。
起動後は`http://127.0.0.1:8080/docs`でAPIドキュメントを確認できます。

### 2. デモデータで試す

デモでは、利用中のバイナリと同じversionのスナップショットを使います。
別のターミナルで次を実行してください。`git`、`curl`、`jq`、`tar`が必要です。

```bash
VERSION="v$(./batchscope version | awk '{print $2}')"
DEMO_DIR="$(mktemp -d)"

git clone --quiet --depth 1 --branch "$VERSION" \
  https://github.com/vnzzzz/batch-scope.git "$DEMO_DIR/source"

tar -C "$DEMO_DIR/source/examples/demo/snapshot" \
  -czf "$DEMO_DIR/snapshot.tar.gz" \
  manifest.json nodes.ndjson relations.ndjson

curl -fsS -X POST \
  -H 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
  --data-binary "@$DEMO_DIR/snapshot.tar.gz" \
  http://127.0.0.1:8080/v1/snapshot-imports

until curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; do sleep 1; done

curl -fsS 'http://127.0.0.1:8080/v1/targets?query=JOB-A' | jq
curl -fsS 'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' | jq
```

これで、デモスナップショットの取込、対象ジョブの検索、後続リミット解析まで確認できます。

### 3. 実際のジョブ定義をPublic Skillで変換する

Releaseアーカイブには`skills/public/batchscope/`を同梱しています。
このPublic Skillは、既存のジョブマネージャーの出力やジョブ定義を読み取り、BatchScopeが受け取るスナップショットへ変換して、取込・検索するためのエージェント向け手順です。

利用するエージェントのSkill配置方法に従って`skills/public/batchscope/`を読み込ませ、ジョブマネージャーから取得した定義ファイルや実行スクリプトを参照できる状態にします。
そのうえで、例えば次のように依頼します。

```text
BatchScope Public Skillを使って、<ジョブ定義のパス> からBatchScope用スナップショットを作成してください。
起動中の http://127.0.0.1:8080 に取り込み、<調べたいジョブ名またはジョブネット名> の後続リミットと依存経路を調べてください。
```

スナップショットのJSON SchemaはPublic Skill内に同梱されています。
製品固有の定義をどのように解釈・変換するかはPublic Skillが扱い、BatchScope本体は共通スナップショットの取込と解析に専念します。

## ドキュメント

- **使い方を確認する**
  - [デモ](docs/development/demo.md) — デモスナップショットとAPI利用例
- **公開仕様を確認する**
  - [API仕様](docs/design/api.md) — APIの意味と利用者へ保証する動作
    - [OpenAPI](docs/api/openapi.yaml) — HTTPのパス、パラメーター、JSON形式
  - [スナップショット仕様](docs/design/canonical-snapshot.md) — 取込データの意味とレコード間制約
- **設計・開発について確認する**
  - [設計文書](docs/index.md) — 設計文書全体への入口
  - [開発環境](docs/development/development.md) — ソースコードからの開発と実行
  - [ビルドと公開](docs/development/build-and-release.md) — Release成果物とコンテナイメージの扱い
  - [コントリビューションガイド](CONTRIBUTING.md) — 開発への参加方法とブランチ運用

## ライセンス

[MIT License](LICENSE)で公開します。
