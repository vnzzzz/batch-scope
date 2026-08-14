# エージェントスキル

BatchScopeでは、製品利用に必要なPublic Skill、BatchScope固有の開発用Skill、複数repositoryで再利用する汎用Skillの責務を分離します。
サービス内部の処理や設計文書をSkillへ重複して実装しません。

## BatchScope repositoryが管理するSkill

| 区分 | Skill | 正本 | 役割 |
|---|---|---|---|
| Public | `batchscope` | `skills/public/batchscope` | スナップショットの作成、取込、検索APIの利用 |
| Internal | `batchscope-backlog` | `skills/internal/batchscope-backlog` | 設計、実装、既存Issueの監査とIssue候補の作成 |
| Internal | `batchscope-development` | `skills/internal/batchscope-development` | Issue単位の委任、差分レビュー、検証、GitHub操作 |

repository非依存の汎用Skillは`vnzzzz/agent-skills`のPluginとして利用します。
BatchScopeはPlugin内の個別Skill名、Skill一覧、内部pathを管理しません。

日本語技術文書では、Pluginから提供される汎用規則に`docs/development/writing-style.md`のBatchScope固有overlayを追加します。
競合時はBatchScope固有規則を優先します。

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
汎用SkillはPlugin経由で導入するため、`.agents/skills`または`.claude/skills`へ個別linkを置きません。

## shared Pluginの導入

Dev ContainerではCodex / Claude Code CLIのインストール後にpublic GitHub repository `https://github.com/vnzzzz/agent-skills.git`をmarketplace sourceとして登録し、`agent-skills` Pluginをuser scopeへ導入します。

- public HTTPS Git URLを使用し、GitHub tokenやSSH鍵を前提にしない。
- 特定revisionへpinしない。
- Dev Container作成時にmarketplaceを更新し、最新Pluginを利用する。
- Plugin内のSkill追加を理由にBatchScope側の設定を変更しない。
- Universal Plugin DirectoryやAnthropic公式marketplaceへの公開を前提にしない。

Plugin bootstrapは`scripts/install-shared-plugin.sh`が担当します。
Plugin packageの構造とCodex / Claude Code横断検証は`vnzzzz/agent-skills`と`vnzzzz/agent-skills-development`の責務です。

## 配布境界

Public SkillはBatchScope repository内を正本とし、GitHub Releaseで自己完結したPublic Skillとして配布します。
BatchScope Internal Skillと開発環境用`agent-skills` Pluginはproduction imageおよびRelease archiveへ含めません。
アーカイブ構成と生成方法は[ビルドと公開](../development/build-and-release.md#対応環境とアーカイブ構成)を正本とします。

Public Skill `batchscope`自体をPlugin化することは本変更の対象ではありません。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータを同じ変更で更新する。
- 汎用Skillの変更は`vnzzzz/agent-skills`で行い、BatchScopeへコピーしない。
- BatchScope固有の日本語文書規則は`docs/development/writing-style.md`で更新する。
- Plugin内のSkill追加を理由にBatchScopeのdiscovery設定を変更しない。
- Public Skillと開発用Skillの配布責務を混在させない。
