# BatchScope

BatchScopeは、バッチジョブの定義から、指定したジョブまたはジョブネットの後続にあるリミット設定と、そこまでの依存経路を洗い出す静的解析サービスです。

各ジョブマネージャーの定義を共通スナップショットに変換して取り込み、HTTP APIで検索・解析します。ジョブマネージャーへ直接接続したり、現在の実行状態や業務日から対応期限を計算したりすることはありません。

## Quick Start

[GitHub Releases](https://github.com/vnzzzz/batch-scope/releases)では、Linux / macOS向けの実行バイナリとデモスナップショットを配布しています。OS別アーカイブを展開してBatchScopeを起動し、`batchscope_demo_snapshot.tar.gz`を取り込めば、ジョブ検索と後続リミット解析をすぐに試せます。

具体的なコマンドと想定結果は[デモ手順](docs/development/demo.md)を参照してください。

実際のジョブ定義を解析する場合は、OS別アーカイブに同梱する[Public Skill](skills/public/batchscope/SKILL.md)を使ってジョブマネージャーの定義から共通スナップショットを作成し、BatchScopeへ取り込みます。

## ドキュメント

- **使い方**
  - [デモ](docs/development/demo.md) — Releaseバイナリとデモスナップショットで一連の操作を試す
  - [Public Skill](skills/public/batchscope/SKILL.md) — 実際のジョブ定義をスナップショットへ変換して利用する
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
