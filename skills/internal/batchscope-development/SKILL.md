---
name: batchscope-development
description: GitHub Issueを起点にBatchScopeを自律的に開発し、Claude CodeがIssue単位でCodex CLIへ実装を委任して、CI成功済みのReady for review Pull Requestを作成するときに使用する。製品APIの利用やスナップショット変換には使用しない。
---

# BatchScope development

Claude Codeが主担当として設計判断、差分レビュー、最終検証、GitHub操作を担う。
コードベースの一次調査、実装、テスト、文書更新は、原則としてIssue単位でCodexへ委任する。

## 開始条件

1. Dev Container内で作業する。
2. Issue番号が指定されている場合は、そのIssueを対象として手順8へ進む。
3. Issue番号が指定されていない場合は、Milestone `v0.1.0`のOpen Issueを一覧し、ラベルを取得する。

   ```bash
   gh issue list --state open --milestone v0.1.0 --limit 200 --json number,title,labels
   ```

4. 一覧した各Issueのネイティブ依存関係、Openの関連Pull Request、作業ブランチの有無を確認する。

   ```bash
   gh issue view <番号> --json number,state,labels,blockedBy,blocking
   gh issue develop --list <番号>
   ```

5. `blockedBy`にOpenのIssueが一つでもあるIssue、`blocked`ラベルが付いたIssue、または対応中のPull RequestがあるIssueを候補から除外し、Issueごとに除外理由を記録する。
   Openの関連Pull Request、または`gh issue develop --list <番号>`が返す作業ブランチが存在する場合は、対応中のPull Requestがあるものとして扱う。
6. 候補が一つの場合は、そのIssueを対象とする。
   候補が複数の場合は、`blocking`を再帰的にたどって到達できるOpen Issueを重複なく数え、その数が多いIssueを対象とする。
   同数の場合は直接ブロックしているOpen Issueの数が多いIssueを優先し、それも同数の場合はIssue番号が小さいIssueを選ぶ。
   候補が一つもない場合は着手せず、除外したIssueと除外理由を人間へ報告する。
7. 自動選択したIssueと選択理由を人間へ報告する。
   選択理由には、除外したIssueと除外理由、および候補ごとの推移的にブロックしているOpen Issueの総数と直接ブロックしているOpen Issueの数を含める。
   報告後は承認を待たず、Issueの読み取りとブランチ作成へ進む。
8. `gh issue view <番号> --json number,title,body,labels,url,blockedBy,blocking`で対象Issueとネイティブ依存関係を読む。
9. `blockedBy`にOpenのIssueが一つでもある場合は、ブランチを作成せず作業を止めて人間へ報告する。
   `blockedBy`が空の場合、またはClosedのIssueだけの場合は着手してよい。
10. `git status --short`で、開始前から存在する変更を確認する。
11. `AGENTS.md`、`docs/index.md`、`docs/design/agent-skill.md`とIssueに関連する設計文書を読む。
12. Issueに目的、対象範囲、対象外、受入条件がそろっていることを確認する。
13. 変更する事実ごとに正本と同期対象を特定し、Issueの範囲内で一貫して更新できることを確認する。

## 正本と整合性

変更するファイルを正本とみなして作業を始めない。
先に`docs/index.md`と`docs/design/agent-skill.md`の責務分担を確認し、変更する事実ごとに正本を特定する。

| 情報 | 正本 | 同期時の扱い |
|---|---|---|
| HTTPのパス、パラメーター、ヘッダー、JSON項目の型と必須条件 | GoとHumaの実装から生成する`docs/api/openapi.yaml` | OpenAPIを手書きせず、実装変更後に生成して差分を確認する |
| snapshot入力の機械的制約 | `schema/`配下のJSON Schema | Markdownへフィールド一覧や境界値を複製しない |
| APIの意味と利用者への保証 | `docs/design/api.md` | 公開動作の意味が変わる場合に更新する |
| 複数packageを跨ぐ責務、不変条件、設計理由 | `docs/design/`配下 | 局所的な関数やlock位置の実況を置かない |
| 運用時の判断と資源上限 | `docs/operations.md` | 実装値や測定根拠との整合を確認する |
| 開発、テスト、ビルドの再現手順 | `docs/development/`配下 | 現在のコードベースで実行できる手順だけを現行手順として記載する |
| 局所的な実装理由、制約、不変条件 | owner codeのコメント | `readable-code` Skillに従い、設計文書へ同じ詳細を複製しない |
| 製品利用者向けの操作要点 | `skills/public/batchscope` | 正本の代わりにせず、利用に必要な要点だけを同期する |

実装前に、変更が次のどこへ波及するかを確認する。

- APIの機械的schema
- snapshotのJSON Schema
- APIの意味と保証
- システム全体の不変条件
- 運用上限と判断
- 開発または測定手順
- Public Skillとデモ
- owner codeのコメント

すべてを毎回変更するのではなく、正本と実際に影響を受ける表現だけを更新する。
同じ仕様を複数箇所へ詳しく複製して整合を取ろうとしない。
局所的な実装方法だけが変わる場合は、設計文書を実装実況へ戻さず、必要な理由をowner codeのコメントへ残す。
複数packageを跨ぐ保証が変わる場合は、コードコメントだけへ閉じず、該当する設計文書を更新する。

APIまたは取込形式を変更する場合は、`AGENTS.md`の「公開仕様を変更する場合」に従い、実装と同じ変更で必要なdocs、Schema、Public Skill、デモを同期する。
性能値や対応規模を変更する場合は、判断に使った測定revision、入力条件、実行コマンド、指標を再現できる形で残す。
再現できない過去の測定手順を推測で補わない。

Issueが十分に定義され、停止条件に該当しない場合は、計画の承認待ちなどの途中確認を行わず、Ready for review Pull Requestの作成まで進める。

作業ブランチはClaude Codeが作成する。
接頭辞は`CONTRIBUTING.md`のブランチ名規則に従う。

```bash
gh issue develop <番号> --name <接頭辞>/<要約> --base main --checkout
```

## 自律実行の停止条件

次のいずれかに該当する場合だけ作業を止め、人間へ確認する。

- 着手可能なIssueが一つもない。
- Openのブロッカーを持つIssueである。
- Issueに目的、対象範囲、対象外、受入条件が不足している。
- 新しい公開API、Schema、データ形式の決定が必要になる。
- Issueと設計文書、または正本同士が矛盾している。
- 変更に必要な正本または同期対象を特定できない。
- Secret、認証、権限、破壊的操作の追加が必要になる。
- Issue外の大規模な設計変更が必要になる。
- 外部環境の問題により受入条件を検証できない。
- 受入条件の変更が必要になる。

着手可能なIssueがないことを理由に停止する場合は、報告へ除外したIssueと除外理由を含める。
Openのブロッカーを理由に停止する場合は、報告へブロッカーのIssue番号と状態を含める。

内部の関数分割、パッケージ構成、テスト方法などは、既存方針の範囲内でエージェントが判断する。

## 責務

Codexは次を行う。

- コードベースを調査する。
- 変更する事実の正本と同期対象を調査する。
- Issueの対象範囲を実装する。
- テストを追加または修正する。
- 必要な`docs/`、`schema/`、Public/Internal Skill、デモを正本の責務に従って更新する。
- 実装と一致するようにowner codeのコメントを更新する。
- 変更後に正本と同期対象を再照合する。
- `make verify`を実行する。

Claude Codeは次を行う。

- Issue、正本、設計文書の整合を確認する。
- Codexへ委任する範囲と同期対象を決める。
- Codex実行後に`git diff`、影響を受ける正本と同期対象、検査結果を確認する。
- 必要な修正をCodexへ再委任する。
- 最後に`make verify`を実行する。
- 目的別にコミットし、pushする。
- Draft Pull Requestを作成し、CIを確認する。
- CI成功後にPull RequestをReady for reviewへ移行する。

Claude CodeはCodexの最終報告だけで完了を判断しない。
必ず実際の差分、変更が依存する正本、影響を受ける同期対象、検査結果を確認する。

通常のIssueでは一次調査をCodexへ委任し、Claude Codeは差分と受入条件を中心に確認する。
公開契約、Schema、対応規模、世代整合性、複数文書の責務整理など複数の正本へ波及する変更では、差分だけに限定せず、関連する正本と未変更の同期対象も必要な範囲で直接確認する。

## Codexへ委任する単位

原則としてIssue全体を一度の委任でCodexへ渡す。
一度の委任には大きすぎる場合だけ、Claude Codeが受入条件に沿って安全な単位へ分割し、順次委任する。

複数のCodex実行に同じ作業ツリーを同時編集させない。
委任されたCodexは対象外のリファクタリングを行わず、明示された範囲を広げない。

## 委任プロンプト

Codexへ渡すプロンプトには次を含める。

- Issue番号
- 目的
- 受入条件
- 今回の委任範囲
- 対象外
- 参照すべき正本、文書、コード
- 編集前に変更する事実ごとの正本と同期対象を特定すること
- 同じ仕様を複数文書へ詳しく複製せず、正本以外では要点とリンクにすること
- コードコメントを追加または変更する場合は`readable-code` Skillに従うこと
- 日本語技術文書を追加または変更する場合は`japanese-technical-writing` Skillに従うこと
- 実行する検査
- GitHub操作を行わないこと
- 最後に変更ファイル、正本との整合確認、検査結果、未解決事項を簡潔に報告すること

一時ファイルはリポジトリ外へ作成する。

`codex exec --ephemeral`は利用者設定の`model`を読み込まないため、`BATCHSCOPE_CODEX_MODEL`に利用者が利用できるモデルを指定する必要がある。
この動作はCodex CLI 0.147.0で確認している。
未設定の場合はCodexの既定モデルが使われ、アカウントによっては実行に失敗する。

`--sandbox workspace-write`では、既定のGoビルドキャッシュがサンドボックスの書き込み可能範囲外にあるため使用できない。
そのため、委任プロンプトでは`GOCACHE=/tmp/batchscope-go-cache make verify`を検査に指定する。

```bash
prompt_file="$(mktemp)"
result_file="$(mktemp)"

cat >"$prompt_file" <<'PROMPT'
ここにIssueと委任範囲に沿った実装指示を記載する。
検査では`GOCACHE=/tmp/batchscope-go-cache make verify`を実行する。
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
Codexへブランチ作成、コミット、push、IssueやPull Requestの作成または更新を任せない。

## 差分レビューと検証

Codex実行後、Claude Codeは次を確認する。

```bash
git status --short
git diff --check
git diff
make verify
```

差分を受入条件と照合し、変更する事実ごとに正本と同期対象を再確認する。
設計、実装、テスト、文書、Schema、生成OpenAPI、Public/Internal Skill、デモ、コード内コメントのうち、影響を受けるものが一致していることを確認する。
コードコメントが説明する判断理由、制約、不変条件と実装が一致していることを確認する。
正本以外の文書が詳細な仕様を再び複製していないこと、現行手順として記載したコマンドが現在のコードベースで実行可能であることも確認する。
問題がある場合は、修正範囲と検査を指定してCodexへ再委任する。

Dockerfileまたはコンテナのビルド設定を変更した場合は、Dev Container内で`make image`を実行しない。
CIの`Build production image`で確認することをPull Requestへ記載する。

## コミットとPull Request

「一つのIssue、一つのPull Request、一つの`main`コミット」を基本とする。
Claude CodeがIssueの目的に沿って変更をコミットし、作業ブランチをpushする。

```bash
git push -u origin <ブランチ>
```

Pull RequestはDraftとして作成する。
Pull RequestのタイトルはSquash merge後にそのまま`main`のコミットタイトルになる。
作成前に、タイトルが`feat:`、`fix:`、`chore:`などのConventional Commits形式であり、`main`のコミット履歴としてIssueの目的を適切に表していることを確認する。
Pull Requestの本文はSquash merge後にそのまま`main`のコミット本文になる。
本文へ`Closes #<Issue番号>`とレビューガイドを含める。

```bash
gh pr create --draft --title "<Conventional Commits形式のタイトル>"
```

レビューガイドには次を含める。

- 最初に確認するファイル
- 主な設計判断
- 維持する不変条件
- 生成ファイル
- 既知の制約
- 受入条件ごとの確認結果
- 実行した検査
- 互換性への影響
- 未対応事項

CIの完了まで検査結果を監視する。

```bash
gh pr checks <番号> --watch
```

CIが失敗した場合は、失敗したrunのログから原因を調べる。

```bash
gh run view <ID> --log-failed
```

必要な修正をCodexへ再委任し、Claude Codeが差分と検査結果を確認して再pushした後、CIを再確認する。
CIが成功したら、Pull RequestをReady for reviewへ移行する。

```bash
gh pr ready <番号>
```

Pull Requestはマージしない。
人間による最終レビューを省略しない。
人間がSquash mergeした後、GitHubは不要になったリモートのheadブランチを自動削除する。
ローカルブランチは自動削除せず、不要であることを確認したうえで`git branch -d <ブランチ名>`を使用する。
`git branch -D`による強制削除は行わない。
