# 開発環境

clone、ブランチ作成、Issue / Pull Request運用は[コントリビューションガイド](../../CONTRIBUTING.md)を参照してください。
共通のエージェント実装原則は[`AGENTS.md`](../../AGENTS.md)、Claude Code固有の役割差分は[`CLAUDE.md`](../../CLAUDE.md)を正本とします。

## 実行環境の分担

| 実行環境 | 主な用途 | 利用できるもの |
|---|---|---|
| ホスト | clone、Dev Container作成、本番イメージ操作 | Docker、Dev Containers対応エディタ |
| Dev Container | 編集、Goテスト、サービス起動、Codex、Claude Code、デモ表示 | Go、Node.js、GitHub CLI、SQLite CLI、`jq` |
| GitHub Actions | Pull Request検査、イメージbuild確認、タグ付き公開成果物作成 | Go、Docker、GitHub Actions |

Dev ContainerにはDocker CLIとDockerソケットを追加しません。
本番イメージはホストまたはCIで作成します。

## Dev Containerの作成

Dev Container作成時に`.devcontainer/scripts/post-create.sh`が次を行います。

1. Codex CLIとClaude Codeをインストールする。
2. public GitHub repository `vnzzzz/agent-skills`をPlugin marketplaceとして登録する。
3. `agent-skills` PluginをCodexとClaude Codeへ導入する。
4. Goモジュールを取得する。

Agent CLIと`agent-skills` Pluginは特定revisionへ固定せず、Dev Container作成時の取得対象を使用します。
Plugin取得はpublic GitHub repositoryへのHTTPSアクセスであり、GitHub認証情報を必要としません。
Agent自身の認証、GitHub CLIの認証とは別です。

実行環境：Dev Container

```bash
codex
claude
gh auth login
```

GitHub Issueを扱う前に対象アカウントとrepositoryを確認します。

```bash
gh auth status
gh repo view
```

認証情報と利用者設定はrepository専用の名前付きボリュームへ保存します。
ホストのSSH鍵、クラウド認証ファイル、Dockerソケットはマウントしません。

### shared Pluginを再取得する場合

```bash
bash .devcontainer/scripts/install-agent-skills-plugin.sh
```

Plugin単位で導入し、BatchScope側ではPlugin内の個別Skillを登録しません。

### Codexが起動できない場合

`/home/node/.codex`への書込み権限がない場合はDev ContainerをRebuildします。
既存コンテナをそのまま使う必要がある場合は、Dev Container内で次を実行します。

```bash
sudo chown -R "$(id -u):$(id -g)" "${CODEX_HOME:-$HOME/.codex}"
chmod 700 "${CODEX_HOME:-$HOME/.codex}"
mkdir -p "${CODEX_HOME:-$HOME/.codex}/tmp"
codex doctor
```

SQLite破損エラーが残る場合だけ、Codexを終了して`state_5.sqlite`と付随ファイルを退避します。
認証情報やセッションを含む`.codex`全体は削除しません。

### モデルとeffort

Claude CodeとCodexのモデルIDはrepositoryへ固定しません。
Claude Codeの既定effortは`.claude/settings.json`の`effortLevel`で管理し、このrepositoryでは`medium`を既定とします。
権限設定は利用者ごとの`.claude/settings.local.json`に置き、repositoryでは管理しません。

Claude Codeのモデルは`ANTHROPIC_MODEL`でローカルに指定できます。
Claudeから`codex exec --ephemeral`へ委任する場合、利用者設定の`model`が読まれないため、必要に応じて利用可能なモデルを`BATCHSCOPE_CODEX_MODEL`で指定します。
未設定の場合はCodex側の既定モデルを使用します。

```bash
export ANTHROPIC_MODEL=<利用可能なClaudeモデル>
export BATCHSCOPE_CODEX_MODEL=<利用可能なCodexモデル>
```

モデル名や認証情報をrepositoryへコミットしません。

## 開発コマンド

実行環境：Dev Container

| コマンド | 用途 |
|---|---|
| `make verify` | format、shell syntax、`go vet`、race test、OpenAPI差分を検査 |
| `make run` | ポート8080でサービスを起動 |
| `make smoke` | 一時サービスへデモデータを取り込み、公開APIをE2E確認 |
| `make openapi` | `docs/api/openapi.yaml`を生成 |
| `make openapi-check` | OpenAPI生成物と実装の差分を確認 |

ポート8080はDev Containerからホストへ転送します。
デモは[デモ](demo.md)、公開成果物は[ビルドと公開](build-and-release.md)、テスト方針は[テスト戦略](testing.md)を参照してください。

## AIエージェントによる開発

BatchScope固有の開発運用はInternal Skillとして持ちません。

- 共通の実装原則、停止条件、検証ルール: `AGENTS.md`
- Claude Code固有の責務: `CLAUDE.md`
- Issue、branch、Pull Request、review、mergeの実務フロー: `CONTRIBUTING.md`
- repository非依存のコード・文章等の規則: `agent-skills` Plugin

実装作業はGitHub Issueを起点とし、OpenのブロッカーがあるIssueには着手しません。
主担当エージェントはIssueの解釈とレビューを担い、実装や一次調査をIssue単位でCodex等へ委任できます。
Draft Pull Request作成、CI確認、Ready for reviewまでは自律的に進められますが、**エージェントはmergeを行いません。** 人間の最終レビューを待ちます。

Issue候補の登録もGitHub Issueを正本とします。
利用者からIssue作成を明示的に依頼されていない場合は、既存Issueとの重複と依存関係を監査し、候補を提示して確認を得てから登録します。

Agent Skillの配布責務は[エージェントスキル](../design/agent-skill.md)を参照してください。
