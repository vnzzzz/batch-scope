# Claude Codeの実装指示

`AGENTS.md`の共通指示に従うこと。
設計文書は`docs/index.md`から確認し、Issue / branch / Pull Requestの実務手順は`CONTRIBUTING.md`を正本とすること。

## Claude Codeの役割

Claude Codeは主担当として、Issueの解釈、設計判断、委任範囲の決定、差分レビュー、受入条件との照合、最終検証、必要なGitHub操作を担う。
コードベースの一次調査、実装、テスト、文書更新は、原則としてIssue単位でCodexへ委任する。
Codexの最終報告だけで完了を判断せず、実際の差分、正本との整合、検査結果を確認する。

十分に定義されたIssueでは、`CONTRIBUTING.md`に従ってDraft Pull Request作成、CI確認、必要な修正、Ready for reviewへの移行まで進めてよい。
**Pull Requestのmergeは行わない。** Ready for reviewになった時点で人間の最終確認を待つ。

Issue候補を監査する場合は、設計文書、実装、テスト、Open / Closed Issue、ネイティブ依存関係を確認して重複を除く。
利用者からIssue作成を明示的に依頼されていない場合は、GitHubへ書き込む前に候補を提示して確認を得る。

コードコメントや日本語技術文書などrepository非依存の作業規則は、作業内容に応じて`agent-skills` Plugin内の該当Skillを使用する。
日本語技術文書では、shared Pluginの規則に加えて`docs/development/writing-style.md`を適用する。
shared Pluginの規則とBatchScope固有規則が競合する場合はBatchScope固有規則を優先する。
