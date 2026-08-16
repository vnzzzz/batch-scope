# BatchScopeの実装指示

## 作業前に読む文書

次の順に確認する。

1. `docs/index.md`
2. `docs/development/development.md`
3. `CONTRIBUTING.md`
4. `docs/development/writing-style.md`
5. Issueに関連する設計文書、Schema、コード

## 実装方針

- 現在のMVPで必要な処理だけを実装する。
- 実例が一つしかない段階では、製品機能としてのプラグイン機構、汎用グラフ基盤、設定用DSL、過度なインターフェース分割を追加しない。
- 入力境界のID、親子関係、依存関係、リミットは検査する。
- 任意の補足情報は`attributes`、`locator`、`raw`、`evidence`で受け入れる。
- 曖昧検索や、サービス内部での依存関係の推測は行わない。
- 管理単位の親子関係と、ジョブの実行順を同じデータ構造で表さない。
- 新しいSQLiteの準備が終わるまでは、現在使用中のSQLiteを検索に使う。
- 同じ入力と検索条件には、同じ順序の結果を返す。
- 同じ仕様を複数の文書へ詳しく複製しない。情報ごとの正本は`docs/index.md`と関連する設計文書で確認する。

## Agent Skillとrepository固有規則

- BatchScope repositoryが管理するSkillは、製品利用者向けの`skills/public/batchscope`だけとする。
- repository非依存の開発規則は`vnzzzz/agent-skills` Pluginを利用し、BatchScopeへ複製しない。
- shared Pluginは`vnzzzz/agentic-development-toolkit`のDev Container Feature `agent-dev`が導入する。
- 作業内容に応じて`agent-skills` Plugin内の該当Skillを適用する。
- 日本語技術文書には、shared Pluginの規則に加えて`docs/development/writing-style.md`のBatchScope固有規則を適用する。
- shared Pluginの規則とBatchScope固有規則が競合する場合は、BatchScope固有規則を優先する。

## IssueとPull Request

- 機能追加、不具合修正、設計変更、開発基盤変更はGitHub Issueを起点とする。
- Issueには目的、対象範囲、対象外、受入条件を記載し、調査ログや会話全文は残さない。
- 実装順序上のブロッカーはGitHubのネイティブな`blocked by`と`blocking`で管理する。Openのブロッカーが一つでもあるIssueには着手しない。
- 原則として一つのIssueを一つのPull Requestで完了させる。
- 作業開始時にIssue本文、依存関係、既存Pull Request、作業ツリーの状態を確認する。
- Issue候補を新規登録する場合は、既存Issueとの重複と依存関係を確認する。利用者からIssue作成を明示的に依頼されていない場合は、候補を提示して確認を得てからGitHubへ書き込む。

主担当エージェントはIssueの解釈、設計判断、差分レビュー、受入条件との照合、最終検証に責任を持つ。
実装、テスト、文書更新、一次調査は、原則としてIssue単位で実装担当エージェントへ委任できる。

委任されたエージェントは次を守る。

- 明示されたIssueと委任範囲だけを変更する。
- 編集前に、変更する事実の正本と同期対象を特定する。
- 明示的な指示がない限り、ブランチ作成、コミット、push、IssueやPull Requestの作成・更新を行わない。
- 複数のエージェントで同じ作業ツリーを同時編集しない。

主担当エージェントは、Issueの範囲内でブランチ作成、コミット、push、Draft Pull Request作成、CI失敗の修正、Ready for reviewへの移行まで行ってよい。
**エージェントはPull Requestをmergeしない。** CI成功やreview完了だけを理由にmergeへ進まず、人間の最終確認を待つ。

詳細なブランチ、Pull Request、レビューの手順は`CONTRIBUTING.md`を正本とする。

## 作業を止めて人間へ確認する条件

次のいずれかに該当する場合は、推測で対象範囲を広げず人間へ確認する。

- Issueに目的、対象範囲、対象外、受入条件が不足している。
- Openのブロッカーがある。
- 新しい公開API、Schema、データ形式の決定が必要になる。
- Issueと設計文書、または正本同士が矛盾している。
- 変更に必要な正本または同期対象を特定できない。
- Secret、認証、権限、破壊的操作の追加が必要になる。
- Issue外の大規模な設計変更が必要になる。
- 外部環境の問題により受入条件を検証できない。
- 受入条件の変更が必要になる。

既存方針の範囲内の関数分割、パッケージ構成、テスト方法等は、エージェントが判断してよい。

## 実行環境と検証

CodexとClaude CodeはDev Container内で作業する。
Dev Containerは`vnzzzz/agentic-development-toolkit`の`agent-dev:1`を利用し、Agent CLI、GitHub CLI、認証volume、`vnzzzz/agent-skills` Pluginのbootstrapを共通Featureへ委ねる。
BatchScope側はGo、SQLite、Go cache、port等のproject固有設定だけを管理する。
認証情報はFeatureが管理するDev Container単位のnamed volumeへ保存し、ホストのcredential directory、SSH鍵、Dockerソケットをmountしない。
Dev ContainerにはDocker CLIとDockerソケットを追加しない。

作業完了前にDev Container内で次を実行する。

```bash
make verify
```

Dockerfileまたはコンテナのビルド設定を変更した場合は、Dockerを利用できるホストまたはCIで次を確認する。

```bash
make image
```

Dev Container設定を変更した場合は、`.github/workflows/devcontainer.yml`によるDev Container作成と`make verify`の成功も確認する。

エージェントはDockerを利用できないDev Container内で`make image`を実行しない。
ホストで確認できない場合は、Pull Requestへ未実施であることを記載してCI結果を確認する。

## 公開仕様を変更する場合

APIまたは取込データの形式を変更する場合は、実装と同じ変更で影響する正本と配布物を同期する。

- 関連する`docs/`配下の設計文書
- `schema/`配下のJSON Schema
- `skills/public/batchscope/references/`配下の参照資料
- 必要に応じて`examples/demo/`配下の例
- Go実装から生成する`docs/api/openapi.yaml`

OpenAPIは手書きしない。

## 公開設定を変更する場合

バイナリ公開はGitHub Releasesだけを対象とし、GHCR、Docker Hub、外部レジストリの認証設定を追加しない。

`.github/workflows/release.yml`または`scripts/build-release-artifacts.sh`を変更した場合は、Dev Container内で次を実行する。

```bash
make verify
make release-artifacts VERSION=0.1.0
make release-artifacts-check VERSION=0.1.0
```

公開用成果物の構成、Public Skill、配布Schema、READMEリンク、チェックサムまで確認する。
