# エージェントスキル

BatchScopeでは、Skillの利用目的をPublic / Internalで分けます。
この区分と、SkillがBatchScope固有か他リポジトリでも再利用できるかという区分は別の分類軸です。
サービス内部の処理や設計文書をSkillへ重複して実装しません。

## Skillの責務

| 利用区分 | 再利用性 | Skill | 利用者 | 役割 |
|---|---|---|---|---|
| Public | BatchScope固有 | `batchscope` | Codex、Claude Code | スナップショットの作成、取込、検索APIの利用 |
| Internal | BatchScope固有 | `batchscope-backlog` | Claude Code | 設計、実装、既存Issueの監査とIssue候補の作成 |
| Internal | BatchScope固有 | `batchscope-development` | Claude Code | Issue単位のCodexへの委任、差分レビュー、検証、GitHub操作による自律実行 |
| Internal | 汎用候補 | `readable-code` | Codex、Claude Code | 判断理由、制約、不変条件を残すコードコメントの作成とレビュー |
| Internal | 汎用core + BatchScope固有規則の候補 | `japanese-technical-writing` | Codex、Claude Code | 日本語技術文書の執筆と推敲 |

Public SkillはBatchScopeを利用するためのSkillであり、現在の`batchscope`はBatchScope固有です。
Internal SkillはBatchScopeの開発で使うSkillであり、Release成果物へは含めません。
Internal Skillの中にはBatchScope固有のものと、他リポジトリでも再利用できる汎用部分を持つものがあります。
汎用Internal Skillの共有方式や外部リポジトリへの分離はRelease配布とは別に設計します。

コードコメントと日本語技術文書のInternal Skillは、実装を担当するCodexと、レビューを担当するClaude Codeが共通して使用します。

## 管理する内容

| 対象 | 正本 | Skillでの扱い |
|---|---|---|
| 取込JSONの機械的制約 | `schema/`配下のJSON Schema | Public Skillへ詳細を転記せず、配布時も正本から機械的に同梱する |
| 取込データの意味とレコード間制約 | `docs/design/canonical-snapshot.md` | Public Skillにはスナップショット作成時に必要な要点だけを記載する |
| 変換と検索の手順 | `skills/public/batchscope/SKILL.md` | 製品利用時に直接指示する |
| 変換時の要点 | `skills/public/batchscope/references/` | リポジトリ外でも判断できる範囲へ要約する |
| バックログ監査 | `skills/internal/batchscope-backlog/SKILL.md` | Claude Codeの監査と登録手順を記載する |
| Issue実装 | `skills/internal/batchscope-development/SKILL.md` | Claude Codeの指揮手順を記載する |
| コードコメント | `skills/internal/readable-code/SKILL.md` | コメントの対象とレビュー規則を記載する |
| 日本語文書規則 | `docs/development/writing-style.md` | Skillへ全文転記せず、適用時の判断だけを記載する |
| 日本語文書の外部参考資料 | `skills/internal/japanese-technical-writing/references/sources.md` | 出典、確認日、採用範囲を記録する |
| HTTPの機械的schema | HumaのGo実装から生成する`docs/api/openapi.yaml` | Skillへ手書きで複製しない |
| APIの意味と保証 | `docs/design/api.md` | Skillには利用手順に必要な要点だけを記載する |
| システム全体の設計理由 | `docs/design/`配下 | Skillへコピーせず、開発時に参照する |
| 局所的な実装理由 | `internal/`配下のowner codeのコメント | Skillや設計文書へ複製しない |

## ディレクトリ構成

```text
skills/
├── README.md
├── public/
│   └── batchscope/
│       ├── SKILL.md
│       └── references/
│           ├── canonical-snapshot.md
│           ├── downstream-limit-analysis.md
│           └── normalization-rules.md
└── internal/
    ├── batchscope-backlog/
    │   └── SKILL.md
    ├── batchscope-development/
    │   └── SKILL.md
    ├── readable-code/
    │   └── SKILL.md
    └── japanese-technical-writing/
        ├── SKILL.md
        └── references/
            └── sources.md
```

Claude CodeとCodexは、シンボリックリンクから必要なSkillだけを参照します。

```text
.agents/skills/batchscope -> ../../skills/public/batchscope
.agents/skills/readable-code -> ../../skills/internal/readable-code
.agents/skills/japanese-technical-writing -> ../../skills/internal/japanese-technical-writing
.claude/skills/batchscope -> ../../skills/public/batchscope
.claude/skills/batchscope-backlog -> ../../skills/internal/batchscope-backlog
.claude/skills/batchscope-development -> ../../skills/internal/batchscope-development
.claude/skills/readable-code -> ../../skills/internal/readable-code
.claude/skills/japanese-technical-writing -> ../../skills/internal/japanese-technical-writing
```

CodexにはPublic Skillに加えて、コードコメントと日本語技術文書の共通規則だけを公開します。
Codex自身には、バックログ監査と実装指揮を担う`batchscope-backlog`と`batchscope-development`を公開しません。

## 配布

Public Skillはリポジトリ内を正本とし、リポジトリ外でも単独利用できる内容にします。
GitHub ReleaseではJSON Schemaを含む自己完結したPublic Skillとして配布し、Internal Skillは再利用性にかかわらず含めません。
アーカイブ構成と生成方法は[ビルドと公開](../development/build-and-release.md#対応環境とアーカイブ構成)を正本とします。

Plugin、Marketplace、Gist同期、Skill専用リポジトリは、利用上の必要性が確認されるまで追加しません。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータを同じ変更で更新する。
- JSON Schemaの制約をPublic Skillへ全文転記せず、利用時に必要な判断だけを記載する。
- JSON Schemaで表現しない取込データの意味制約は`docs/design/canonical-snapshot.md`で管理し、Public Skillを意味制約の正本にしない。
- OpenAPIは手書きで同梱しない。
- Public SkillとInternal Skillの責務を混在させない。
- 日本語文書の規則は`docs/development/writing-style.md`だけで更新し、Internal Skillへ全文転記しない。
- 外部参考資料の出典、確認日、採用範囲は`skills/internal/japanese-technical-writing/references/sources.md`だけで管理する。
- Issue候補は利用者の確認後に登録し、バックログ文書を別に作らない。
