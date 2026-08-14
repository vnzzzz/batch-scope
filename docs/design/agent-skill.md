# エージェントスキル

BatchScopeでは、製品利用に必要なPublic Skill、BatchScope固有の開発用Skill、複数repositoryで再利用するshared Skillの責務を分離します。
サービス内部の処理や設計文書をSkillへ重複して実装しません。

## Skillの責務

| 区分 | Skill | 正本 | 利用者 | 役割 |
|---|---|---|---|---|
| Public | `batchscope` | `skills/public/batchscope` | Codex、Claude Code | スナップショットの作成、取込、検索APIの利用 |
| Internal | `batchscope-backlog` | `skills/internal/batchscope-backlog` | Claude Code | 設計、実装、既存Issueの監査とIssue候補の作成 |
| Internal | `batchscope-development` | `skills/internal/batchscope-development` | Claude Code | Issue単位の委任、差分レビュー、検証、GitHub操作 |
| Shared | `readable-code` | `vnzzzz/agent-skills` | Codex、Claude Code | 判断理由、制約、不変条件を残すコードコメント |
| Shared | `japanese-technical-writing` | `vnzzzz/agent-skills` | Codex、Claude Code | repository非依存の日本語技術文書原則 |

shared Skillは`agent-skills` Pluginとして開発環境へ導入します。
BatchScope repositoryはshared Skill本文、個別Skillへのsymlink、`agent-skills`内部pathを管理しません。

日本語技術文書にはshared `japanese-technical-writing`と`docs/development/writing-style.md`のBatchScope固有overlayを併用します。
競合時はBatchScope固有規則を優先します。

## 管理する内容

| 対象 | 正本 |
|---|---|
| 取込JSONの機械的制約 | `schema/`配下のJSON Schema |
| 取込データの意味とレコード間制約 | `docs/design/canonical-snapshot.md` |
| BatchScope利用手順 | `skills/public/batchscope/SKILL.md` |
| バックログ監査 | `skills/internal/batchscope-backlog/SKILL.md` |
| Issue実装 | `skills/internal/batchscope-development/SKILL.md` |
| 汎用コードコメント規則 | `vnzzzz/agent-skills`の`readable-code` |
| 汎用日本語文書規則 | `vnzzzz/agent-skills`の`japanese-technical-writing` |
| BatchScope固有日本語文書規則 | `docs/development/writing-style.md` |
| HTTPの機械的schema | HumaのGo実装から生成する`docs/api/openapi.yaml` |
| APIの意味と保証 | `docs/design/api.md` |
| システム全体の設計理由 | `docs/design/`配下 |

## repository内の構成

```text
skills/
├── README.md
├── public/
│   └── batchscope/
└── internal/
    ├── batchscope-backlog/
    └── batchscope-development/
```

repository内のdiscovery linkはBatchScope固有Skillだけを扱います。
shared SkillはDev Container作成時にPluginとして導入されるため、`.agents/skills`または`.claude/skills`へ個別linkを置きません。

## shared Pluginの導入

Dev ContainerではCodex / Claude Code CLIのインストール後にpublic GitHub repository `vnzzzz/agent-skills`をmarketplace sourceとして登録し、`agent-skills` Pluginを導入します。

- GitHub認証情報を前提にしない。
- 特定revisionへpinしない。
- Dev Container作成時にmarketplaceをrefreshし、最新Pluginを利用する。
- BatchScopeはPlugin内のSkill一覧を管理しない。
- Universal Plugin DirectoryやAnthropic公式marketplaceへの公開を前提にしない。

Plugin bootstrapは`.devcontainer/scripts/install-agent-skills-plugin.sh`が担当します。
Plugin package自体の構造とCodex / Claude Code横断検証は`vnzzzz/agent-skills`と`vnzzzz/agent-skills-development`の責務です。

## 配布

Public SkillはBatchScope repository内を正本とし、GitHub Releaseで自己完結したPublic Skillとして配布します。
Internal Skillと開発環境用`agent-skills` Pluginはproduction imageおよびRelease archiveへ含めません。
アーカイブ構成と生成方法は[ビルドと公開](../development/build-and-release.md#対応環境とアーカイブ構成)を正本とします。

Public Skill `batchscope`自体をPlugin化することは本変更の対象ではありません。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータを同じ変更で更新する。
- shared Skillの一般原則を変更する場合は`vnzzzz/agent-skills`を更新し、BatchScopeへコピーしない。
- BatchScope固有の日本語文書規則は`docs/development/writing-style.md`で更新する。
- Plugin内のSkill追加を理由にBatchScopeのdiscovery設定を変更しない。
- Public Skillと開発用Skillの配布責務を混在させない。
