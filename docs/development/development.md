# 開発環境

clone、ブランチ作成、rebase、Pull Requestの作成手順は[コントリビューションガイド](../../CONTRIBUTING.md)を参照してください。

## 実行環境の分担

| 実行環境 | 主な用途 | 利用できるもの |
|---|---|---|
| ホスト | リポジトリのclone、Dev Containerの作成、本番イメージの操作 | Docker、Dev Containers対応エディタ |
| Dev Container | 編集、Goのテスト、サービス起動、Codex、Claude Code、デモ表示 | Go、Node.js、GitHub CLI、SQLite CLI、`jq` |
| GitHub Actions | Pull Requestの検査、イメージのビルド確認、タグ付きバイナリの公開 | Go、Docker、GitHub CLI |

Dev ContainerにはDocker CLIとDockerソケットを追加しません。
本番イメージのビルドをDev Containerから行えるようにすると、ホストのDockerへ接続する権限と設定が増えるためです。

## Dev Containerと本番イメージ

```mermaid
flowchart LR
    Host[ホスト] -->|リポジトリを開く| Dev[Dev Container]
    Dev --> Verify[編集、make verify、make run]

    Host -->|make image| Builder[Dockerのビルド用ステージ]
    Builder --> Runtime[本番用イメージ]
    Runtime --> Service[BatchScope]
```

| 項目 | Dev Container | 本番イメージ |
|---|---|---|
| 目的 | 開発、テスト、エージェント利用 | BatchScopeの実行 |
| Go | ツールチェーンを含む | コンパイラを含まない |
| Node.js | Codex CLIとClaude Codeのために含む | 含まない |
| 開発ツール | GitHub CLI、SQLite CLI、`jq`を含む | 含まない |
| Docker操作 | 行わない | ホストまたはCIが作成する |
| ソースと文書 | リポジトリ全体をマウントする | アプリケーションバイナリだけを含む |
| 実行ユーザー | 開発用ユーザー | 非rootユーザー |

## Dev Containerの作成

実行環境：ホスト

Dev Containerに対応したエディタでリポジトリを開き、コンテナを作成します。
初回作成時に`.devcontainer/scripts/post-create.sh`を実行します。

このスクリプトは、Codex CLIとClaude Codeをnpmからインストールし、Goモジュールを取得します。
Codex CLIとClaude Codeは、利用時点の最新版をインストールします。

実行環境：Dev Container

各CLIとGitHub CLIの認証を完了します。

```bash
codex
claude
gh auth login
```

GitHub Issueを扱う前に、対象アカウントとリポジトリを確認します。

```bash
gh auth status
gh repo view
```

認証情報と利用者設定は、リポジトリ専用の名前付きボリュームへ保存します。
初回作成時に、名前付きボリュームの所有者をDev Containerの`node`ユーザーへ変更します。
ホストのSSH鍵、クラウド認証ファイル、Dockerソケットはマウントしません。

### Codexが起動できない場合

次のエラーは、`/home/node/.codex`へ書き込めない場合に発生します。

```text
unable to open database file
```

`.devcontainer`を更新した後は、エディタの`Rebuild Container`を実行してください。
既存コンテナをそのまま使う場合は、Dev Container内で次を実行します。

```bash
sudo chown -R "$(id -u):$(id -g)" "${CODEX_HOME:-$HOME/.codex}"
chmod 700 "${CODEX_HOME:-$HOME/.codex}"
mkdir -p "${CODEX_HOME:-$HOME/.codex}/tmp"
codex doctor
```

権限を修正してもSQLiteの破損エラーが残る場合だけ、Codexを終了してから`state_5.sqlite`と付随ファイルを退避します。
認証情報やセッションを含む`.codex`全体は削除しません。

### モデルの選択

CLIはDev Container作成時の最新版を使用します。
Claude CodeとCodexのモデルはリポジトリへ固定しません。

Claude Codeは`ANTHROPIC_MODEL`、ClaudeからCodexへ委任する場合は任意の`BATCHSCOPE_CODEX_MODEL`でローカルに指定できます。
未設定の場合は、それぞれのCLIの利用者設定または既定モデルを使用します。

```bash
export ANTHROPIC_MODEL=<利用可能なClaudeモデル>
export BATCHSCOPE_CODEX_MODEL=<利用可能なCodexモデル>
```

モデル名や認証情報をリポジトリへコミットしません。

## 開発コマンド

実行環境：Dev Container

| コマンド | 用途 |
|---|---|
| `make verify` | 静的検査とテスト |
| `make run` | ポート8080でサービスを起動 |
| `make openapi` | `docs/api/openapi.yaml`を生成 |
| `make openapi-check` | OpenAPI生成物と実装の差分を確認 |
| `make demo-view` | デモのAPIレスポンスをテキスト表示 |
| `make release-artifacts VERSION=0.1.0` | GitHub Releasesへ登録するバイナリを作成 |

ポート8080はDev Containerからホストへ転送します。
ホストのブラウザまたは`curl`から`http://localhost:8080`へ接続できます。

実行環境：ホスト

| コマンド | 用途 |
|---|---|
| `make image` | 本番用コンテナイメージの作成 |
| `make image-run` | 作成済みイメージの起動 |

## 現在のサービス骨格

現在の実装は次のAPIだけを公開します。

| API | 動作 |
|---|---|
| `GET /healthz` | プロセスの生存確認 |
| `GET /readyz` | スナップショット未投入時は503 |
| `GET /v1/status` | 起動状態とスナップショット状態 |

このほかに、Humaが`/docs`、`/openapi.json`、`/openapi.yaml`を提供します。

スナップショット取込と後続リミット検索は、設計済みで未実装です。
デモ用レスポンスの確認方法は[デモ](demo.md)を参照してください。

## AIエージェントによる開発

実装作業はGitHub Issueを起点とします。
Issueには目的、対象範囲、対象外、受入条件を記載し、Pull RequestからIssueを関連付けます。

Claude Codeを主担当とし、Issueの解釈、計画、設計判断、差分レビュー、最終検証を担当させます。
Codexは、一度に一つの限定された実装またはレビューだけを担当します。
同じ作業ツリーへの並列編集は行いません。

設計と実装からIssue候補を作る場合は`batchscope-backlog` Skillを使用します。
Issue候補は利用者の確認後にGitHubへ登録し、Projectsや別のバックログ文書は使用しません。

Issue実装の進行手順は`batchscope-development` Skillに記載します。
共通の実装規則は`AGENTS.md`、Claude Code固有の役割は`CLAUDE.md`を参照してください。
Skillの配置と更新規則は[エージェントスキル](../design/agent-skill.md)を参照してください。
