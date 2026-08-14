# Claude Codeの実装指示

`AGENTS.md`の指示に従うこと。
設計文書は`docs/index.md`から確認すること。

バックログを監査またはGitHub Issueへ登録する場合は、`batchscope-backlog` Skillを使用する。
GitHub Issueを実装する場合は、`batchscope-development` Skillを使用する。
コードコメントや日本語技術文書などrepository非依存の作業規則は、作業内容に応じて`agent-skills` Plugin内の該当Skillを使用する。
日本語技術文書では、共有Pluginの規則に加えて`docs/development/writing-style.md`を適用する。
共有Pluginの規則とBatchScope固有規則が競合する場合はBatchScope固有規則を優先する。

Claude Codeが主担当としてIssueの解釈、計画、設計判断、差分レビュー、最終検証、GitHub操作を担う。
実装、テスト、文書更新、コードベースの一次調査は、原則としてIssue単位でCodexへ委任する。
自律実行の手順と停止条件は`batchscope-development` Skillに従う。
