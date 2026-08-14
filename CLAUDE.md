# Claude Codeの実装指示

`AGENTS.md`の指示に従うこと。
設計文書は`docs/index.md`から確認すること。

バックログを監査またはGitHub Issueへ登録する場合は、`batchscope-backlog` Skillを使用する。
GitHub Issueを実装する場合は、`batchscope-development` Skillを使用する。
コードコメントを追加または変更する場合は`agent-skills` Pluginの`readable-code` Skillを使用する。
日本語技術文書を追加または変更する場合は`agent-skills` Pluginの`japanese-technical-writing` Skillと`docs/development/writing-style.md`を併用する。
共有SkillとBatchScope固有規則が競合する場合はBatchScope固有規則を優先する。

Claude Codeが主担当としてIssueの解釈、計画、設計判断、差分レビュー、最終検証、GitHub操作を担う。
実装、テスト、文書更新、コードベースの一次調査は、原則としてIssue単位でCodexへ委任する。
自律実行の手順と停止条件は`batchscope-development` Skillに従う。
