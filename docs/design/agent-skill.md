# エージェントスキル

BatchScopeでは、製品利用者へ配布するPublic Skillと、複数repositoryで再利用するshared Pluginの責務を分離します。
BatchScope固有の開発運用はSkillとして持たず、`AGENTS.md`、`CLAUDE.md`、`CONTRIBUTING.md`、`docs/development/`で管理します。

## 責務

| 区分 | 配布単位 | 正本 | 利用者 | 役割 |
|---|---|---|---|---|
| Public | `batchscope` Skill | `skills/public/batchscope` | Codex、Claude Code | スナップショットの作成、取込、検索APIの利用 |
| Shared | `agent-skills` Plugin | `vnzzzz/agent-skills` | Codex、Claude Code | repository非依存の開発用Agent Skill群 |

Public SkillはBatchScopeという製品を利用するための成果物です。
shared Pluginは開発環境で利用する汎用規則であり、BatchScopeの製品Releaseには含めません。

## 情報の正本

| 対象 | 正本 |
|---|---|
| 取込JSONの機械的制約 | `schema/`配下のJSON Schema |
| 取込データの意味とレコード間制約 | `docs/design/canonical-snapshot.md` |
| BatchScope利用手順 | `skills/public/batchscope/SKILL.md` |
| repository非依存の汎用Agent Skill | `vnzzzz/agent-skills` Plugin |
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
shared SkillはDev Container作成時にPluginとして導入するため、`.agents/skills`または`.claude/skills`へ個別linkを置きません。

## shared Pluginの導入

Dev ContainerではCodex / Claude Code CLIのインストール後にpublic GitHub repository `vnzzzz/agent-skills`をmarketplace sourceとして登録し、`agent-skills` Pluginを導入します。

- public HTTPS取得にGitHub認証情報を要求しない。
- 特定revisionへpinしない。
- Plugin内の個別Skill名やprovider内部pathをBatchScope側で管理しない。
- Universal Plugin DirectoryやAnthropic公式marketplaceへの公開を前提にしない。

Plugin bootstrapは`.devcontainer/scripts/install-agent-skills-plugin.sh`が担当します。
Plugin packageの構造とCodex / Claude Code横断検証は`vnzzzz/agent-skills`と`vnzzzz/agent-skills-development`の責務です。

## 配布

Public SkillはBatchScope repository内を正本とし、GitHub Releaseで自己完結した成果物として配布します。
Release archiveにはPublic Skillを含めますが、開発環境用の`agent-skills` Pluginは含めません。
production imageにはPublic Skillもshared Pluginも含めません。
アーカイブ構成と生成方法は[ビルドと公開](../development/build-and-release.md#対応環境とアーカイブ構成)を正本とします。

Public Skill `batchscope`自体をPlugin化することは現在の対象ではありません。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータを同じ変更で更新する。
- repository非依存のshared Skillを変更する場合は`vnzzzz/agent-skills`を更新し、BatchScopeへコピーしない。
- BatchScope固有の開発運用は`AGENTS.md`、`CLAUDE.md`、`CONTRIBUTING.md`、`docs/development/`の責務に応じて更新する。
- Plugin内のSkill追加を理由にBatchScopeのdiscovery設定や文書を変更しない。
