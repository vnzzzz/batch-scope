# エージェントスキル

BatchScopeのエージェントスキルは、スナップショットの作成と検索APIの利用を支援します。
サービス内部の処理を代替せず、入力形式とAPIの使い方をエージェントへ伝えます。

## 管理する内容

| 対象 | 正本 | Skillでの扱い |
|---|---|---|
| 取込ファイルの制約 | `schema/`配下のJSON Schema | 作成時に参照する |
| 変換と検索の手順 | `skills/batchscope/SKILL.md` | エージェントへ直接指示する |
| 変換時の要点 | `skills/batchscope/references/` | Skillだけで判断しやすい範囲へ要約する |
| API仕様 | HumaのGo実装 | 実装後にOpenAPIを生成し、必要な配布物へ自動で含める |
| 設計理由 | `docs/design/`配下 | Skillへコピーせず、開発時に参照する |

## ディレクトリ構成

```text
skills/batchscope/
├── SKILL.md
└── references/
    ├── canonical-snapshot.md
    └── normalization-rules.md
```

`skills/batchscope`を正本とし、CodexとClaude Codeはシンボリックリンクから同じ内容を参照します。

```text
.agents/skills/batchscope -> ../../skills/batchscope
.claude/skills/batchscope -> ../../skills/batchscope
```

## 対応する操作

| 操作 | Skillの役割 | BatchScopeの役割 |
|---|---|---|
| スナップショット作成 | 元資料からノード、依存関係、リミット、確実性を共通形式へ変換する | JSON Schemaと参照関係を再検査し、SQLiteを作成する |
| 対象検索 | 完全一致検索を呼び、複数候補がある場合は利用者へ確認する | 候補と親子関係を返す |
| 後続リミット検索 | APIの返却順、確実性、循環、未確認範囲を保って説明する | 後続を探索し、リミットと経路を返す |

MVPでは専用のBatchScope CLIを用意しません。
Skillは標準コマンドでファイルを作成、梱包、送信しますが、完全な検査は取込APIでも必ず行います。

## 更新規則

- APIまたは取込形式を変更した場合は、実装、JSON Schema、設計文書、Skillの参照資料、必要なデモデータを同じ変更で更新する。
- JSON Schemaの制約をSkillへ全文転記せず、変換時に必要な判断だけを記載する。
- OpenAPIは手書きで同梱しない。
- Skillの具体的な実行手順は`SKILL.md`だけに記載する。
