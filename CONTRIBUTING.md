# コントリビューションガイド

## 基本方針

`main`は常にテストを通過した状態を保つ。
開発はGitHub Issueを起点に作業ブランチで行い、Pull Requestを通して`main`へ取り込む。
「一つのIssue、一つのPull Request、一つの`main`コミット」を基本とする。

Pull RequestはSquash mergeを使用する。
**mergeは人間が最終レビュー後に行う。エージェントはPull Requestをmergeしない。**

## ブランチ名

```text
feature/add-snapshot-import
fix/handle-cycle-path
refactor/simplify-storage
docs/update-contributing-guide
chore/update-dev-environment
```

| 接頭辞 | 用途 |
|---|---|
| `feature/` | 機能追加 |
| `fix/` | 不具合修正 |
| `refactor/` | 外部仕様を変えない構造改善 |
| `docs/` | 文書だけの変更 |
| `test/` | テストの追加または修正 |
| `chore/` | 開発環境、依存関係、CIの整備 |

エージェントが作成する一時的なIssueブランチでは`agent/`接頭辞を使用してよい。

## cloneとmainの更新

リポジトリへの書込権限がある開発者は元のリポジトリをcloneする。
外部コントリビューターはforkし、元のリポジトリを`upstream`として登録する。
Dev Container作成後の編集、Git操作、テストはDev Container内で行う。

既存checkoutで作業を始める場合は、branch切替、fetch、pull、reset相当の操作より先にworktreeを確認する。

```bash
git status --short
```

開始前から変更がある場合は、自動でstash、discard、commitせず、その変更の所有者と安全な扱いを確認するまでmain更新やbranch切替を行わない。

書込権限がある場合:

```bash
git switch main
git pull --ff-only origin main
```

forkの場合:

```bash
git fetch upstream
git switch -C main upstream/main
```

Issueに紐づく作業ブランチは、次節の確認後にGitHubのdevelopment branchとして作成する。

## Issueの確認

実装前にIssueの目的、対象範囲、対象外、受入条件を確認する。
実装順序上の依存関係はGitHubのネイティブな`blocked by` / `blocking`を正本とする。

```bash
gh issue view <番号> --json number,title,body,labels,url,blockedBy,blocking,closedByPullRequestsReferences
gh issue develop --list <番号>
git status --short
```

Openのブロッカーが一つでもあるIssueには着手しない。
単に参照するIssueはネイティブ依存関係へ登録せず、Issue本文の関連資料として扱う。
`closedByPullRequestsReferences`にOpenのPull Requestが一つでもある場合、または`gh issue develop --list <番号>`が作業ブランチを返す場合は、既に対応中として重複実装を開始しない。

`git status --short`で開始前から存在する変更がある場合は、その変更を自動でstash、discard、commitせず、作業ブランチを作成する前に安全な扱いを確認する。
エージェントは既存変更の所有者や意図を推測してlinked branchへ持ち込まない。

新しいIssue候補を作る場合は、設計文書、実装、テスト、Open / Closed Issueを確認し、既存Issueとの重複と依存関係を整理する。
利用者からIssue作成を明示的に依頼されていない場合は、候補を提示して確認を得てからGitHubへ登録する。

### Issue依存関係の登録

新しいIssueを登録したら、新規Issue側だけでなく既存Issue側も含めて実装上の依存関係を再確認する。
相手のIssueが完了するまで着手できない真のブロッカーだけをGitHubのネイティブ依存関係へ登録する。
単なる関連、同じ文書を扱う、テーマが近いだけの関係は登録しない。

```bash
gh issue edit <番号> --add-blocked-by <ブロッカー番号>
gh issue edit <番号> --add-blocking <ブロックされる番号>
```

依存理由はIssue本文にも記載するが、着手可否を機械判定する正本はGitHubのネイティブ依存関係とする。
登録後は対象Issueの`blockedBy` / `blocking`を取得し、意図した関係が登録されていること、不要な関係や循環がないことを確認する。

```bash
gh issue view <番号> --json blockedBy,blocking
```

## 作業ブランチの作成

Issueが着手可能で、対応中のlinked branchがなく、開始前のworktreeを安全に扱えることを確認してから、Issueに紐づくdevelopment branchを作成してcheckoutする。

```bash
gh issue develop <番号> --name <接頭辞>/<要約> --base main --checkout
```

fork等でこのコマンドをそのまま利用できない場合は、Issueとbranchの関連付けを失わない代替手順を確認してから作業を開始する。
単なるlocal branchだけを先に作って実装を開始しない。

## 変更と検証

変更する事実ごとに正本と同期対象を特定し、Issueの対象範囲内で一貫して更新する。
同じ仕様を複数文書へ詳しく複製しない。
公開APIまたは取込形式を変更した場合は、JSON Schema、設計文書、Public Skillの参照資料、必要なデモデータ、生成OpenAPIを実装と同期する。

```bash
git status
git diff
make verify
```

Dockerfileまたはコンテナのビルド設定を変更した場合は、Dockerを利用できるホストで`make image`を実行する。
実行できない場合はPull Requestへ未実施であることを記載し、CIの結果を確認する。

## AIエージェントによる実装

主担当エージェントはIssueの解釈、設計判断、差分レビュー、受入条件との照合、最終検証を担う。
実装、テスト、文書更新、一次調査は、原則としてIssue単位で実装担当エージェントへ委任できる。

Issueが十分に定義されている場合、主担当エージェントは途中の計画承認を必須とせず、Issueの範囲内で次まで進めてよい。

1. main更新より前に開始時worktreeを確認する。
2. Issue、Openのブロッカー、Openのclosing Pull Request、linked branch、branch作成直前のworktree状態を確認する。
3. Issueに紐づく作業ブランチを作成する。
4. 実装と検証を行う。
5. 実差分と受入条件を照合する。
6. コミットしてpushする。
7. Draft Pull Requestを作成する。
8. CIを確認し、Issue範囲内の失敗を修正する。
9. CI成功後にReady for reviewへ移行する。
10. 人間へレビュー対象、検証結果、未解決事項を報告して停止する。

次の場合は作業を止めて人間へ確認する。

- Issueの目的、対象範囲、対象外、受入条件が不足している。
- Openのブロッカーがある。
- 開始前から存在するworktree変更を安全に扱えない。
- 新しい公開API、Schema、データ形式の決定が必要になる。
- Issueと設計文書、または正本同士が矛盾している。
- 変更に必要な正本または同期対象を特定できない。
- Secret、認証、権限、破壊的操作の追加が必要になる。
- Issue外の大規模な設計変更が必要になる。
- 外部環境の問題により受入条件を検証できない。
- 受入条件の変更が必要になる。

内部の関数分割、パッケージ構成、テスト方法は既存方針の範囲内で判断してよい。

**エージェントはCI成功、review承認、merge可能状態のいずれを確認してもmerge操作を実行しない。**
mergeは人間の最終レビュー後に人間が実行する。

## コミット

コミットには一つの目的に関係する変更だけを含める。

```bash
git add <変更したファイル>
git diff --cached
git commit -m "feat: add snapshot import endpoint"
```

Pull RequestはSquash mergeするため、作業ブランチ上の途中コミット数は`main`の履歴には持ち込まれないが、レビューしやすい論理単位を保つ。

## 最新mainへのrebase

作業ブランチへ`main`をmergeせず、基準remoteの最新`main`へrebaseする。

```bash
git fetch origin
git rebase origin/main
make verify
```

forkの場合は`upstream/main`へrebaseする。
一度pushしたブランチをrebaseした場合は通常の`--force`ではなく`--force-with-lease`を使う。
共有ブランチでは合意なく履歴を書き換えない。

## Pull Requestの作成

Pull Request作成前に、タイトルがSquash merge後の`main`コミットタイトルとして適切か確認する。
タイトルは`feat:`、`fix:`、`chore:`等のConventional Commits形式を基本とする。

Pull Request本文には少なくとも次を記載する。

- `Closes #<Issue番号>`による実装Issueへのclosing reference
- 変更の目的と主な変更内容
- 最初に確認するファイル
- 主な設計判断と維持する不変条件
- 受入条件ごとの確認結果
- 実行した検査
- 互換性への影響
- 未対応事項

実装Issueは`Closes #<Issue番号>`等のGitHub closing keywordで参照し、単なる`#<Issue番号>`の関連付けだけで代替しない。

エージェントが作成するPull RequestはDraftで開始し、CI成功後にReady for reviewへ移行する。

```bash
gh pr create --draft
gh pr checks <Pull Request番号> --watch
gh pr ready <Pull Request番号>
```

Ready for reviewへの移行後は人間の最終レビューを待つ。

## レビュー後の更新

レビュー対応ではIssueの対象範囲を維持する。
必要な修正後に`make verify`を再実行し、最新`main`へのrebaseが必要な場合は`--force-with-lease`で更新する。

## マージと片付け

必須チェックと人間の最終レビューが完了した後、**人間が**GitHubのSquash mergeで`main`へ反映する。
GitHubはmerge後にリモートのheadブランチを自動削除する。

書込権限がある開発者はmerge後に次を実行する。

```bash
git switch main
git pull --ff-only origin main
git fetch --prune origin
```

forkの場合は`upstream/main`を取得して自分のforkを更新する。
ローカル作業ブランチは不要であることを確認した後、`git branch -d <ブランチ名>`で削除する。
`git branch -D`による強制削除を自動実行しない。
