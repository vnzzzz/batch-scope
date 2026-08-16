# エージェントスキル

BatchScopeでは、製品利用者へ配布するPublic Skill、複数repositoryで再利用するshared Plugin、共通Agent開発環境の責務を分離します。
BatchScope固有の開発運用はSkillとして持たず、`AGENTS.md`、`CLAUDE.md`、`CONTRIBUTING.md`、`docs/development/`で管理します。

## 責務

| 区分 | 配布単位 | 正本 | 利用者 | 役割 |
|---|---|---|---|---|
| Public | `batchscope` Skill | `skills/public/batchscope` | Codex、Claude Code | スナップショットの作成、取込、検索APIの利用 |
| Shared Skill | `agent-skills` Plugin | `vnzzzz/agent-skills` | Codex、Claude Code | repository非依存の開発用Agent Skill群 |
| Development environment | `agent-dev` Dev Container Feature | `vnzzzz/agentic-development-toolkit` | BatchScope開発者 | Agent CLI、GitHub CLI、認証volume、shared Plugin bootstrap |

Public SkillはBatchScopeという製品を利用するための成果物です。
shared Pluginと`agent-dev` Featureは開発環境で利用する共通基盤であり、BatchScopeの製品Releaseには含めません。

## 情報の正本

| 対象 | 正本 |
|---|---|
| 取込JSONの機械的制約 | `schema/`配下のJSON Schema |
| 取込データの意味とレコード間制約 | `docs/design/canonical-snapshot.md` |
| BatchScope利用手順 | `skills/public/batchscope/SKILL.md` |
| repository非依存の汎用Agent Skill | `vnzzzz/agent-skills` Plugin |
| 共通Agent開発環境 | `vnzzzz/agentic-development-toolkit`の`agent-dev` Feature |
| BatchScope共通のエージェント実装原則 | `AGENTS.md` |
| Claude Code固有の役割 | `CLAUDE.md` |
| Issue / Pull Request運用 | `CONTRIBUTING.md` |
| BatchScope固有日本語文書規則 | `docs/development/writing-style.md` |
| HTTPの機械的schema | HumaのGo実装から生成する`docs/api/openapi.yaml` |
| APIの意味と保証 | `docs/design/api.md` |
| システム全体の設計理由 | `docs/design/`配下 |

## repository内のSkill構成

```text
skills/
├── README.md
└── public/
    └── batchscope/
```

repository内のdiscovery linkはPublic Skill `batchscope`だけを扱います。
shared Skillは`agent-dev`がPluginとして導入するため、`.agents/skills`または`.claude/skills`へ個別linkを置きません。

## shared Pluginと開発環境の導入

Dev Containerでは`vnzzzz/agentic-development-toolkit`が公開する`agent-dev:1`を利用します。
BatchScope側はmajor versionを参照し、`.devcontainer/devcontainer-lock.json`で解決済みFeature versionとdigestを固定します。

`agent-dev`はClaude Code、Codex、GitHub CLI、認証volumeを提供し、post-create処理でpublic GitHub repository `vnzzzz/agent-skills`をmarketplace sourceとして登録して`agent-skills` Pluginを導入します。

責務境界は次のとおりです。

- Agent CLIのversion、認証volume、shared Plugin bootstrapは`vnzzzz/agentic-development-toolkit`で管理する。
- 汎用Skillの内容とPlugin packageは`vnzzzz/agent-skills`で管理する。
- BatchScopeはAgent CLIやshared Pluginのinstallerを複製しない。
- BatchScopeはPlugin内の個別Skill名やprovider内部pathを管理しない。
- BatchScope固有のGo、SQLite、port、開発規則だけをconsumer設定として管理する。

Featureのconsumer契約、versioning、credential trust boundaryは`vnzzzz/agentic-development-toolkit`の文書を正本とします。
Plugin packageの構造とSkill内容は`vnzzzz/agent-skills`を正本とします。

## 配布

Public SkillはBatchScope repository内を正本とし、GitHub Releaseで自己完結した成果物として配布します。
Release archiveにはPublic Skillを含めますが、開発環境用の`agent-skills` Pluginと`agent-dev` Featureは含めません。
production imageにはPublic Skill、shared Plugin、Agent CLIを含めません。
アーカイブ構成と生成方法は[ビルドと公開](../development/build-and-release.md#対応環境とアーカイブ構成)を正本とします。

Public Skill `batchscope`自体をPlugin化することは現在の対象ではありません。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータを同じ変更で更新する。
- repository非依存のshared Skillを変更する場合は`vnzzzz/agent-skills`を更新し、BatchScopeへコピーしない。
- Agent CLI、認証volume、shared Plugin bootstrap等の共通開発環境を変更する場合は`vnzzzz/agentic-development-toolkit`を更新し、BatchScopeへ複製しない。
- BatchScope固有の開発運用は`AGENTS.md`、`CLAUDE.md`、`CONTRIBUTING.md`、`docs/development/`の責務に応じて更新する。
- Plugin内のSkill追加を理由にBatchScopeのdiscovery設定や文書を変更しない。
