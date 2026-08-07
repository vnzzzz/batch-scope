---
name: batchscope-development
description: GitHub Issueを起点にBatchScopeを開発し、Claude Codeが計画とレビューを担い、Codex CLIへ限定された実装やレビューを委任するときに使用する。製品APIの利用やスナップショット変換には使用しない。
---

# BatchScope development

Claude Codeが主担当として作業を指揮する。
Codexは、明示された範囲を実装またはレビューする作業担当として使用する。

## 開始条件

1. Dev Container内で作業する。
2. `main`ではなく、対象Issue用の作業ブランチにいることを確認する。
3. `git status --short`で、開始前から存在する変更を確認する。
4. `gh issue view <番号> --json number,title,body,labels,url`でIssueを読む。
5. `AGENTS.md`とIssueに関連する設計文書を読む。

Issueに目的、対象範囲、対象外、受入条件のいずれかが不足し、公開仕様や設計判断に影響する場合は、実装前に不足を報告する。
エージェントだけで新しい公開仕様を決めない。

## 主担当の責務

Claude Codeは次を行う。

- Issueと設計文書の整合を確認する。
- 受入条件と変更予定ファイルを対応付けた短い計画を作る。
- Codexへ委任する範囲と、Claude Code自身が判断する範囲を分ける。
- Codex実行後に`git diff`を読み、設計、実装、テスト、文書の整合を確認する。
- 必要な修正だけを追加で委任する。
- 最後に`make verify`を実行する。
- コミット、push、Issue更新、Pull Request作成は主担当が行う。

主担当はCodexの最終回答だけで完了を判断しない。
必ず実際の差分とテスト結果を確認する。

## Codexへ委任する単位

一度の委任は、一つの明確な成果物に限定する。

適切な例：

- 既存3エンドポイントをHumaへ移行し、対応するテストを追加する。
- 指定されたパッケージだけをレビューし、問題点をファイルと行単位で報告する。
- 失敗したテストの原因を特定し、最小の修正を行う。

避ける例：

- Issue全体の設計、実装、レビュー、GitHub操作をまとめて任せる。
- 複数のCodex実行に同じ作業ツリーを同時編集させる。
- Codexへ対象外のリファクタリングを許可する。

並列化する場合は、読み取り専用の調査またはレビューだけにする。

## 委任プロンプト

Codexへ渡すプロンプトには次を含める。

- Issue番号、目的、受入条件
- 今回の委任範囲
- 対象外
- 参照すべき文書とファイル
- 実行すべき検査
- GitHub操作を行わないこと
- 最後に変更ファイル、検査結果、未解決事項を簡潔に報告すること

一時ファイルはリポジトリ外へ作成する。

```bash
prompt_file="$(mktemp)"
result_file="$(mktemp)"

cat >"$prompt_file" <<'PROMPT'
ここに限定された実装指示を記載する。
PROMPT

codex_args=(
  exec
  --ephemeral
  --sandbox workspace-write
  --output-last-message "$result_file"
)

if [[ -n "${BATCHSCOPE_CODEX_MODEL:-}" ]]; then
  codex_args+=(-m "$BATCHSCOPE_CODEX_MODEL")
fi

codex "${codex_args[@]}" - <"$prompt_file"

cat "$result_file"
rm -f "$prompt_file" "$result_file"
```

`--sandbox danger-full-access`、`--dangerously-bypass-approvals-and-sandbox`、`--skip-git-repo-check`は使用しない。
権限またはネットワーク制限で処理できない場合は、回避せず主担当へ報告する。
Codexへコミット、push、IssueやPull Requestの操作を任せない。
`BATCHSCOPE_CODEX_MODEL`が未設定の場合は、Codexの利用者設定または既定モデルを使用する。

## レビューと完了

Codex実行後、主担当は次を確認する。

```bash
git status --short
git diff --check
git diff
make verify
```

Dockerfileまたはコンテナのビルド設定を変更した場合は、Dev Container内で`make image`を実行しない。
ホストでの確認またはCI確認が必要であることをPull Requestへ記載する。

Pull Requestには次を含める。

- `Closes #<Issue番号>`
- 変更の目的と主な変更
- 受入条件ごとの確認結果
- 実行した検査
- 互換性への影響
- 未対応事項
