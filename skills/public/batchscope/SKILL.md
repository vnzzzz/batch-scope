---
name: batchscope
description: ジョブマネージャーの定義をBatchScopeの入力形式へ変換して取り込み、指定したジョブまたはジョブネットの後続リミットと依存経路を調べる。
---

# BatchScope

このスキルは、スナップショットの作成、取込、検索に使用する。

## APIから検索する手順

1. `GET /readyz`で検索可能な状態か確認する。
2. 利用者がジョブIDまたはジョブネットIDを指定した場合は、`GET /v1/targets?jobId=<ID>`を呼び出す。利用者がnamespaceも指定した場合は`namespace=<namespace>`も加える。
3. `jobId`だけの検索で複数namespaceが返った場合は、一件へ推測して絞らない。**全候補のnamespace、localId、名前、完全パス、上位管理単位を提示する。**
4. 利用者が「このIDの影響を全部見たい」など全候補を対象にしている場合は、返された各itemのcanonical `id`について後続解析を実行する。namespaceごとの結果を混ぜずに表示する。
5. 名前または完全パスから探す必要がある場合だけ、従来の`GET /v1/targets?query=<value>`を使う。
6. 対象のcanonical `id`を`targetId`へ指定し、必要な場合だけ`includeEvidence=true`を加えて`GET /v1/downstream-limit-analysis`を呼び出す。
7. ジョブ指定時は対象自身のリミットを全件説明し、ジョブネット指定時は配下のリミット設定済みジョブを全件説明する。
8. 経路を説明するときは、`tree[].viaRelations`の種類、生成元、確実性を省略しない。
9. 後続リミットはAPIの返却順を維持し、返却順が緊急度を表すとは説明しない。
10. `declared`と`confirmed`を、`inferred`と`candidate`から区別する。
11. 圧縮区間は`hiddenConnections`の各接続を順にたどり、`fromIdentity` / `toIdentity`のnamespaceとlocalIdを使って表示する。省略されたジョブ数だけで経路を説明せず、canonical IDを表示用に推測復号しない。
12. 循環は`cycles[].nodes`の強連結成分と、`cycles[].route`の一周分の表示経路を区別して説明する。
13. `uncoveredRoutes`は境界ノード単体ではなく、その境界へ至るリミット未通過経路の判定として説明する。
14. 成功レスポンスは全件解析済みとして扱い、Problem Detailsが返った場合は部分結果を補わない。`analysis-timeout`の場合は、再試行するかを利用者に判断してもらう。
15. BatchScopeは現在時刻、実行状態、業務日を使った期限計算を行わないため、実際の対応判断は利用者がジョブマネージャー上の状態と照合して行う。

### チャットでの表示

canonical `id`はAPI呼出し用の値として保持し、利用者向けの主表示には使わない。ジョブまたはジョブネットを表示するときは、必ずnamespaceを併記する。

推奨表示:

```text
[main] JOB-A — 売上集計
[dr]   JOB-A — 売上集計（DR）
```

複数namespaceを解析した場合は、最初に対象一覧を示し、その後をnamespace単位の見出しで分ける。

```text
対象: JOB-A（2 namespace）

main / JOB-A
- 後続リミット: ...
- 主な経路: ...

dr / JOB-A
- 後続リミット: ...
- 主な経路: ...
```

経路途中でnamespaceが変わる場合も省略しない。例えば`main / JOB-A -> main / JOB-A.done -> dr / JOB-B`のように、cross-namespace境界が分かる形で表示する。

## スナップショットを作る手順

1. ジョブマネージャーの出力、実行スクリプト、ジョブ定義、補助資料を確認する。
2. **意味上の定義セットをnamespaceとして特定する。** 本番/DR、独立した環境、管理上別の定義領域などが該当する。ファイルの分割単位だけをnamespaceにしない。資料から判断できない場合は推測しない。`default`は従来snapshotの互換表示専用なので、新しいnamespaceとして割り当てない。
3. 各ノードに元システムの`localId`と`namespace`を付け、`references/canonical-snapshot.md`の規則でcanonical `id`を生成する。
4. 管理単位、ジョブネット、ジョブの単一親階層を抽出する。`parentId`は同じnamespace内のcanonical IDだけを参照する。
5. ジョブマネージャーが定義する先行後続関係を抽出する。
6. ファイル、ファイルパターン、ジョブ状態、外部イベントを特定する。
7. namespaceを跨ぐ依存を確認する。別環境のファイル監視、他ジョブの終了チェック、外部イベント待ちなどは、根拠に合う中間ノードとrelationで明示する。**namespaceが違うことだけを理由にrelationを生成しない。**
8. 推定した依存関係へ`origin`と`certainty`を付ける。
9. リミットは元の設定として抽出し、検索時の優先順位を付けない。
10. 取得できる場合は、元の表記を`sourceText`、判定根拠を`evidence`へ保存する。
11. 根拠がないノード、依存関係、リミットを`confirmed`として生成しない。
12. JSON Schemaに従って`nodes.ndjson`と`relations.ndjson`を生成する。
13. JSON形式、canonical identity、namespace内の親参照、relation参照を検査し、`manifest.json`を生成してアーカイブへまとめる。
14. `Content-Type: application/vnd.batchscope.snapshot+gzip`を指定し、`POST /v1/snapshot-imports`でアーカイブを送信する。
15. `202 Accepted`の`Location`が示す取込状況URIを、`state=succeeded`になるまで確認する。`state=failed`になった場合は、取込リソースの`error`を確認して取込を終了する。
16. `GET /v1/snapshots/current`で有効な世代の`snapshotId`と件数を確認する。

専用のBatchScope CLIは前提にしない。
検査、梱包、送信には、利用環境の`jq`、`tar`、`curl`などを使う。
取込API側でも、JSON Schemaと参照関係を必ず再検査する。

## JSON Schemaの参照先

source checkoutではリポジトリルートの`schema/`、GitHub Releaseの配布物ではこのSkill内の`references/schema/`を参照する。
Schemaのフィールドと機械的制約は参照先を正本とし、このSkillの説明から推測して補わない。

## 検索結果の読み方

BatchScopeが返す値は、定義に保存されたリミットである。
現在の障害に対する復旧期限ではない。

`namespace`はidentityと管理階層の境界であり、依存解析の境界ではない。別namespaceへ到達した後続は通常の解析結果として扱い、表示時にnamespace境界を明示する。

経路ツリーは、返却したリミットとリミットが見つからなかった経路の到達地点までを表す。
後続全体を表すものではない。

後続リミット取得のパラメーター、圧縮、参照、循環、リミット未通過経路の読み方は[`references/downstream-limit-analysis.md`](references/downstream-limit-analysis.md)に従う。

`evidence`がないことは、その依存関係が誤りであることを意味しない。
新しい根拠がない限り、`inferred`または`candidate`を確認済みとして扱わない。

## 参照資料

- `references/canonical-snapshot.md`
- `references/downstream-limit-analysis.md`
- `references/normalization-rules.md`
