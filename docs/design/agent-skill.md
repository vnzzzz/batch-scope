# エージェントスキル

BatchScopeでは、利用者へ配布するPublic Skillと、リポジトリ開発だけに使うInternal Skillを分けます。
サービス内部の処理や設計文書をSkillへ重複して実装しません。

## Skillの責務

| 区分 | Skill | 利用者 | 役割 |
|---|---|---|---|
| Public | `batchscope` | Codex、Claude Code | スナップショットの作成、取込、検索APIの利用 |
| Internal | `batchscope-backlog` | Claude Code | 設計、実装、既存Issueの監査とIssue候補の作成 |
| Internal | `batchscope-development` | Claude Code | Issue単位のCodexへの委任、差分レビュー、検証、GitHub操作による自律実行 |

Public Skillは、BatchScope利用者と開発者が同じ手順でサービスを利用するために共有します。
Internal Skillはリポジトリ固有の開発手順だけを扱い、公開用成果物へ含めません。

## 管理する内容

| 対象 | 正本 | Skillでの扱い |
|---|---|---|
| 取込ファイルの制約 | `schema/`配下のJSON Schema | Public Skillには作成時に必要な要点だけを記載する |
| 変換と検索の手順 | `skills/public/batchscope/SKILL.md` | 製品利用時に直接指示する |
| 変換時の要点 | `skills/public/batchscope/references/` | リポジトリ外でも判断できる範囲へ要約する |
| バックログ監査 | `skills/internal/batchscope-backlog/SKILL.md` | Claude Codeの監査と登録手順を記載する |
| Issue実装 | `skills/internal/batchscope-development/SKILL.md` | Claude Codeの指揮手順を記載する |
| API仕様 | HumaのGo実装 | 実装後にOpenAPIを生成する |
| 設計理由 | `docs/design/`配下 | Skillへコピーせず、開発時に参照する |

## ディレクトリ構成

```text
skills/
├── README.md
├── public/
│   └── batchscope/
│       ├── SKILL.md
│       └── references/
│           ├── canonical-snapshot.md
│           └── normalization-rules.md
└── internal/
    ├── batchscope-backlog/
    │   └── SKILL.md
    └── batchscope-development/
        └── SKILL.md
```

Claude CodeとCodexは、シンボリックリンクから必要なSkillだけを参照します。

```text
.agents/skills/batchscope -> ../../skills/public/batchscope
.claude/skills/batchscope -> ../../skills/public/batchscope
.claude/skills/batchscope-backlog -> ../../skills/internal/batchscope-backlog
.claude/skills/batchscope-development -> ../../skills/internal/batchscope-development
```

CodexにはPublic Skillだけを公開します。
Internal SkillからCodexを限定的に呼び出すため、Codex自身にはバックログ管理や指揮のSkillを持たせません。

## 配布

Public Skillはリポジトリ内を正本とし、リポジトリ外へコピーしても利用できる内容にします。
リポジトリ内部の開発手順やIssue運用へ依存させません。

初回リリースまでに、`skills/public/batchscope`だけをGitHub Releaseのアーカイブへ追加します。
Plugin、Marketplace、Gist同期、Skill専用リポジトリは、利用上の必要性が確認されるまで追加しません。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータを同じ変更で更新する。
- JSON Schemaの制約をPublic Skillへ全文転記せず、利用時に必要な判断だけを記載する。
- OpenAPIは手書きで同梱しない。
- Public SkillとInternal Skillの責務を混在させない。
- Issue候補は利用者の確認後に登録し、バックログ文書を別に作らない。
