# BatchScope設計文書

- 状態：MVP実装済み
- 対象範囲：MVP

BatchScopeは、障害発生時に巨大なジョブ定義を手作業でたどることなく、指定したジョブまたはジョブネットから影響を受けるリミット設定箇所を静的解析によって全件抽出するサービスです。

## 設計

| 文書 | 内容 |
|---|---|
| [全体構成](design/architecture.md) | 構成要素、責務、親子関係と依存関係 |
| [取込データの形式](design/canonical-snapshot.md) | スナップショットの意味とレコード間の制約 |
| [SQLiteの構成](design/storage.md) | 保存責務、検索方式、世代切替 |
| [後続リミットの検索](design/dependency-analysis.md) | リミットの抽出範囲、経路、循環の扱い |
| [API仕様](design/api.md) | APIの意味、利用者への保証、エラー |
| [生成OpenAPI](api/openapi.yaml) | GoとHumaの実装から生成するHTTPの機械的schema |
| [エージェントスキル](design/agent-skill.md) | Skillの責務と管理方法 |
| [設計判断](design/decisions.md) | MVPの方針、採用した方式、見送った方式 |

## 運用

| 文書 | 内容 |
|---|---|
| [運用](operations.md) | 起動状態、取込、監視、障害時の動作 |

## 開発

| 文書 | 内容 |
|---|---|
| [開発環境](development/development.md) | ホスト、Dev Container、CIの役割と開発コマンド |
| [デモ](development/demo.md) | デモデータとAPIレスポンスの確認方法 |
| [コントリビューションガイド](../CONTRIBUTING.md) | ブランチ運用とPull Requestの作成手順 |
| [ビルドと公開](development/build-and-release.md) | バイナリ、コンテナ、CI、GitHub Releases |
| [テスト](development/testing.md) | テスト範囲と評価データ |
| [性能測定結果](development/performance-measurement.md) | 取込、静的解析、SQLite接続方式の測定環境、条件、結果 |
| [完全一致検索のHTTP性能測定結果](development/target-search-performance.md) | 完全一致検索のHTTP性能の測定環境、条件、結果 |
| [後続リミット取得のHTTP性能測定結果](development/limit-analysis-performance.md) | 後続リミット取得のHTTP性能の測定環境、条件、結果 |
| [技術文書の書き方](development/writing-style.md) | 日本語表記、図表、重複を避ける規則 |
