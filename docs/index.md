# BatchScope設計文書

- 状態：設計レビュー中
- 対象範囲：MVP

BatchScopeは、障害発生時に巨大なジョブ定義を手作業でたどることなく、指定したジョブまたはジョブネットから影響を受けるリミット設定箇所を静的解析によって全件抽出するサービスです。

## 設計

| 文書 | 内容 |
|---|---|
| [全体構成](design/architecture.md) | 構成要素、責務、親子関係と依存関係 |
| [取込データの形式](design/canonical-snapshot.md) | スナップショットのファイル構成と項目 |
| [SQLiteの構成](design/storage.md) | テーブル、索引、DB切替 |
| [後続リミットの検索](design/dependency-analysis.md) | リミットの抽出範囲、経路、循環の扱い |
| [API仕様](design/api.md) | エンドポイント、入出力、エラー |
| [エージェントスキル](design/agent-skill.md) | Skillの責務と管理方法 |
| [設計判断](design/decisions.md) | MVPの方針、採用した方式、見送った方式 |

## 運用

| 文書 | 内容 |
|---|---|
| [運用](operations.md) | 起動状態、取込、監視、障害時の動作 |

## 開発

| 文書 | 内容 |
|---|---|
| [開発環境](development/development.md) | ホスト、Dev Container、CIの役割 |
| [デモ](development/demo.md) | デモデータとAPIレスポンスの確認方法 |
| [コントリビューションガイド](../CONTRIBUTING.md) | ブランチ運用とPull Requestの作成手順 |
| [ビルドと公開](development/build-and-release.md) | バイナリ、コンテナ、CI、GitHub Releases |
| [テスト](development/testing.md) | テスト範囲と評価データ |
| [技術文書の書き方](development/writing-style.md) | 日本語表記、図表、重複を避ける規則 |
