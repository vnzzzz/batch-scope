---
name: batchscope-backlog
description: BatchScopeの設計文書、実装、テスト、既存Issueを監査し、MVPのGitHub Issue候補を重複なく作成するときに使用する。利用者の確認前にIssueを書き込まない。
---

# BatchScope backlog

Claude Codeが、設計と実装の差分からGitHub Issue候補を作る。
Issueは作業単位と受入条件を管理し、設計仕様や調査ログの正本にはしない。

## 事前確認

Dev Container内で次を確認する。

```bash
gh auth status
gh repo view
gh issue list --state all --limit 200
gh label list
```

認証、対象リポジトリ、必要なラベルまたはMilestoneが不足している場合は、Issueを作らず不足を報告する。
Projectsは使用しない。

## 監査

次を順に確認する。

1. `docs/index.md`からMVPの設計文書を読む。
2. `schema/`、`cmd/`、`internal/`、`examples/`、`scripts/`、`.github/workflows/`を確認する。
3. テストと`make verify`が検査している範囲を確認する。
4. OpenとClosedの既存Issueを確認する。
5. 設計済み、実装済み、未実装、未決事項を対応付ける。
6. 既存Issueと重複する候補を除く。

Issue候補は、一つのPull Requestで完了判定できる単位にする。
関数単位の作業、実装中に決められる内部構造、必要性が未確認の将来拡張は独立したIssueにしない。

## 候補の提示

GitHubへ書き込む前に、次を含む候補一覧を提示する。

- 仮番号
- タイトル
- 目的
- 対象範囲
- 対象外
- 受入条件
- 関連文書
- 依存する候補またはIssue
- 推奨順序

候補の根拠は、確認したファイルまたは既存Issueを示す。
バックログ一覧を`docs/`へ追加しない。

## Issueの登録

利用者が候補、粒度、順序を確認した後だけ、`gh issue create`で登録する。
登録時は`.github/ISSUE_TEMPLATE/implementation.md`の項目を満たす。
指定されたMilestoneと既存ラベルだけを使用し、Skillが勝手に作成しない。

登録後は、作成したIssue番号、タイトル、依存関係、推奨する最初のIssueを報告する。
会話全文、エージェントの内部メモ、長い調査ログはIssueへ記載しない。
