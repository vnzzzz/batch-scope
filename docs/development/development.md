# 開発環境

clone、ブランチ作成、Issue / Pull Request運用は[コントリビューションガイド](../../CONTRIBUTING.md)を参照してください。
共通のエージェント実装原則は[`AGENTS.md`](../../AGENTS.md)、Claude Code固有の役割差分は[`CLAUDE.md`](../../CLAUDE.md)を正本とします。

## 実行環境の分担

| 実行環境 | 主な用途 | 利用できるもの |
|---|---|---|
| ホスト | clone、Dev Container作成、本番イメージ操作 | Docker、Dev Containers対応エディタ |
| Dev Container | 編集、Goテスト、サービス起動、Codex、Claude Code、デモ表示 | Go、Node.js、GitHub CLI、SQLite CLI、`jq` |
| GitHub Actions | Pull Request検査、Dev Container検査、イメージbuild確認、タグ付き公開成果物作成 | Go、Docker、GitHub Actions |

Dev ContainerにはDocker CLIとDockerソケットを追加しません。
本番イメージはホストまたはCIで作成します。

## Dev Containerの構成

BatchScopeは`vnzzzz/agentic-development-toolkit`が公開するDev Container Feature `agent-dev:1`を利用します。
Featureの提供範囲とconsumer責務は同repositoryの`docs/dev-container-feature.md`を正本とします。

責務は次のように分けます。

| 担当 | 管理するもの |
|---|---|
| `agent-dev` Feature | Node.js 22、GitHub CLI、Claude Code、Codex、共通CLI、Agent用VS Code extension、認証volume、`vnzzzz/agent-skills` Plugin bootstrap |
| BatchScope | Go 1.26.5、SQLite CLI、Go cache、Go module取得、ポート8080、BatchScope固有VS Code設定 |

`.devcontainer/devcontainer.json`では`agent-dev:1`のmajor versionを参照します。
`.devcontainer/devcontainer-lock.json`は、実際に解決したFeatureのexact versionとdigestを固定します。
Featureを更新するまで既存のbuildはlockfileのartifactを利用します。

BatchScope独自のAgent CLI installerや`agent-skills` installerは持ちません。
`agent-dev`のpost-create処理がpublic GitHub repository `vnzzzz/agent-skills`をmarketplace sourceとして登録し、CodexとClaude CodeへPluginを導入します。
BatchScopeの`.devcontainer/scripts/post-create.sh`はGo cacheの準備と`make bootstrap`だけを担当します。

## Dev Containerの作成

Dev Containers対応エディタからrepositoryを開き、Dev Containerを作成します。
作成後は必要に応じて各CLIへloginします。

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

Claude Code、Codex、GitHub CLIの認証情報は`agent-dev`が管理するDev Container単位のnamed volumeへ分離して保存します。
ホストのcredential directory、SSH鍵、Dockerソケットはbind mountしません。
認証済みcontainer内のcodeは同じuser権限で認証状態へアクセスできるため、未確認のrepositoryやscriptを実行しません。
詳細なtrust boundaryは`vnzzzz/agentic-development-toolkit`の`SECURITY.md`を参照してください。

### Featureを更新する場合

`agent-dev:1`はmajor versionだけを指定し、lockfileでexact artifactを固定します。
Dev Containers CLIを利用できるホストで更新候補を確認し、更新時はlockfile差分を通常のcode changeとしてreviewします。

```bash
devcontainer outdated
devcontainer upgrade
```

更新後は`.github/workflows/devcontainer.yml`で実際のDev Container作成とrepository検査を確認します。
major versionを変更する場合はFeatureのbreaking changeを別途確認します。

### shared Pluginを更新する場合

shared Skillの正本は`vnzzzz/agent-skills`です。
BatchScope側ではPlugin内の個別Skillを登録せず、独自install scriptも管理しません。
既存containerでPlugin bootstrapをやり直す必要がある場合はDev ContainerをRebuildし、`agent-dev`のpost-create処理を再実行します。
Pluginの直接操作が必要な場合は`vnzzzz/agent-skills`のREADMEに記載されたCodex / Claude Code向けコマンドを使用します。

### Agent CLIや認証volumeで問題が起きた場合

Agent CLIの既定version、認証volumeのmount先、環境変数は`agent-dev` Featureが管理します。
BatchScope側で`CODEX_HOME`やClaude Codeの設定directoryを上書きしません。

認証volumeの書込み権限やFeature bootstrapで問題が起きた場合は、まずDev ContainerをRebuildします。
解消しない場合は`vnzzzz/agentic-development-toolkit`のFeature contractと`SECURITY.md`を確認し、BatchScope固有設定ではなくFeature側の問題かを切り分けます。
認証状態を破棄する場合は対象Dev Containerと対応するnamed volumeを明示的に削除し、必要に応じてprovider側でもsessionをrevokeします。

### モデルとeffort

Claude CodeとCodexのモデルIDはrepositoryへ固定しません。
Claude Codeの既定effortは`.claude/settings.json`の`effortLevel`で管理し、このrepositoryでは`medium`を既定とします。
利用可能な値は`low`、`medium`、`high`、`xhigh`です。
権限設定は利用者ごとの`.claude/settings.local.json`に置き、repositoryでは管理しません。

Claude Codeのモデルは`ANTHROPIC_MODEL`でローカルに指定できます。
Claudeから`codex exec --ephemeral`へ委任する場合、利用者設定の`model`が読まれないため、必要に応じて利用可能なモデルを`BATCHSCOPE_CODEX_MODEL`で指定します。
未設定の場合はCodex側の既定モデルを使用します。

```bash
export ANTHROPIC_MODEL=<利用可能なClaudeモデル>
export BATCHSCOPE_CODEX_MODEL=<利用可能なCodexモデル>
```

モデル名や認証情報をrepositoryへコミットしません。

### Codexへ実装を委任する場合

`codex exec --ephemeral --sandbox workspace-write`では、既定のGo build cacheがsandboxの書込み可能範囲外になる場合があります。
委任した検査ではrepository外の一時cacheを明示します。

```bash
GOCACHE=/tmp/batchscope-go-cache make verify
```

Codexへ渡す指示には少なくとも次を含めます。

- Issue番号、目的、受入条件
- 今回の委任範囲と対象外
- 参照すべき正本、文書、コード
- 編集前に正本と同期対象を特定すること
- 実行する検査
- GitHub操作を行わないこと
- 変更ファイル、正本との整合、検査結果、未解決事項を最後に報告すること

モデルを指定する場合だけ`BATCHSCOPE_CODEX_MODEL`を`-m`へ渡します。
委任用promptと最後の応答はrepository外の一時ファイルへ作成し、成功・失敗にかかわらず削除します。

```bash
(
  set -e
  prompt_file="$(mktemp)"
  result_file="$(mktemp)"
  trap 'rm -f "$prompt_file" "$result_file"' EXIT

  cat >"$prompt_file" <<'PROMPT'
ここにIssueと委任範囲に沿った実装指示を記載する。
検査では`GOCACHE=/tmp/batchscope-go-cache make verify`を実行する。
PROMPT

  codex_args=(
    exec
    --ephemeral
    --sandbox workspace-write
    --output-last-message "$result_file"
  )

  if [[ -n "${BATCHSCOPE_CODEX_MODEL:-}" ]]; then
    codex_args+=(-m "$BATCHSCOPE_CODEX_MODEL")
  fi

  codex "${codex_args[@]}" - <"$prompt_file"
  cat "$result_file"
)
```

`--sandbox danger-full-access`、`--dangerously-bypass-approvals-and-sandbox`、`--skip-git-repo-check`は使用しません。
権限またはnetwork制限で処理できない場合は回避せず主担当へ報告します。

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

- 共通のAgent開発環境: `vnzzzz/agentic-development-toolkit`の`agent-dev` Feature
- repository非依存のAgent Skill: `vnzzzz/agent-skills` Plugin
- 共通の実装原則、停止条件、検証ルール: `AGENTS.md`
- Claude Code固有の責務: `CLAUDE.md`
- Issue、branch、Pull Request、review、mergeの実務フロー: `CONTRIBUTING.md`

実装作業はGitHub Issueを起点とし、OpenのブロッカーがあるIssueには着手しません。
主担当エージェントはIssueの解釈とレビューを担い、実装や一次調査をIssue単位でCodex等へ委任できます。
Draft Pull Request作成、CI確認、Ready for reviewまでは自律的に進められますが、**エージェントはmergeを行いません。** 人間の最終レビューを待ちます。

Issue候補の登録もGitHub Issueを正本とします。
利用者からIssue作成を明示的に依頼されていない場合は、既存Issueとの重複と依存関係を監査し、候補を提示して確認を得てから登録します。

Agent Skillの配布責務は[エージェントスキル](../design/agent-skill.md)を参照してください。
