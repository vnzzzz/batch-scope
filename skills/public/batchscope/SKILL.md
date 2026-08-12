---
name: batchscope
description: ジョブマネージャーの定義をBatchScopeの入力形式へ変換して取り込み、指定したジョブまたはジョブネットの後続リミットと依存経路を調べる。
---

# BatchScope

このスキルは、スナップショットの作成、取込、検索に使用する。

## APIから検索する手順

1. `GET /readyz`で検索可能な状態か確認する。
2. 利用者がジョブIDを指定した場合は、`GET /v1/targets?query=<local-id>`を呼び出す。namespaceも指定された場合は`namespace=<namespace>`を加える。
3. namespace未指定で同じlocal IDが複数namespaceに一致した場合は、一件を選ばせたり勝手に選んだりしない。`truncated=false`であれば返された**すべてのcandidate**について、各candidateのcanonical `id`を`targetId`へ指定して`GET /v1/downstream-limit-analysis`を呼び出す。
4. 複数namespaceを解析した回答はnamespaceごとに独立した見出しへ分け、`[namespace] localId`を主表示にする。canonical `id`はAPI参照や診断が必要な場合だけ補助表示する。
5. `GET /v1/targets?query=...`が`truncated=true`の場合は、全namespaceを解析済みとは扱わない。利用者へnamespace指定が必要であることを伝える。
6. `query`はlocal ID完全一致を最優先する。local ID一致がない場合にだけ、従来互換としてcanonical ID、完全な名前、完全パスの一致へfallbackする。このfallbackで意味の異なる複数候補が返った場合は、namespace、localId、種別、完全パス、上位の管理単位を提示し、利用者の確認なしに対象を選ばない。
7. 確定した各canonical target IDについて、必要な場合だけ`includeEvidence=true`を加えて後続解析を行う。
8. ジョブ指定時は対象自身のリミットを全件説明し、ジョブネット指定時は配下のリミット設定済みジョブを全件説明する。
9. 経路を説明するときは、`tree[].viaRelations`の種類、生成元、確実性を省略しない。
10. 経路がnamespaceを跨ぐ場合は、境界の前後を`[namespace] localId`で明示する。namespaceが変わったこと自体を依存の根拠として説明せず、ファイル、状態、イベント、relation evidenceなど実際の接続根拠を説明する。
11. 後続リミットはAPIの返却順を維持し、返却順が緊急度を表すとは説明しない。
12. `declared`と`confirmed`を、`inferred`と`candidate`から区別する。
13. 圧縮区間は`hiddenConnections`の各接続を順にたどり、省略されたジョブ数だけで経路を説明しない。
14. 循環は`cycles[].nodes`の強連結成分と、`cycles[].route`の一周分の表示経路を区別して説明する。
15. `uncoveredRoutes`は境界ノード単体ではなく、その境界へ至るリミット未通過経路の判定として説明する。
16. 成功レスポンスはそのcanonical targetについて全件解析済みとして扱い、Problem Detailsが返った場合は部分結果を補わない。複数namespaceの一部だけが失敗した場合は、成功したnamespaceと失敗したnamespaceを混同せず個別に示す。
17. BatchScopeは現在時刻、実行状態、業務日を使った期限計算を行わないため、実際の対応判断は利用者がジョブマネージャー上の状態と照合して行う。

## チャットでの表示

複数namespaceに同じlocal IDが存在する場合は、例えば次のように結果を分ける。

```text
JOB-A の検索結果: 2 namespace

[main] JOB-A
  後続リミット: ...
  代表経路: [main] JOB-A -> [shared] ready.flag -> [dr] JOB-B

[dr] JOB-A
  後続リミット: ...
  代表経路: [dr] JOB-A -> [dr] JOB-C
```

同じlocal IDをcanonical IDだけで区別させない。
pathやnameが必要な場合もnamespace/localIdを先に表示する。
legacy schema `0.5`の応答でnamespace/localIdが省略されている場合は、表示上だけ`[default] <id>`として扱う。

## スナップショットを作る手順

1. ジョブマネージャーの出力、実行スクリプト、ジョブ定義、補助資料を確認する。
2. **意味上の定義セット**を特定し、各セットへ安定したnamespaceを割り当てる。Main/DR/開発などの環境、顧客別定義セットなどが例であり、物理ファイル単位では分割しない。同じ定義セットが複数ファイルに分かれていれば同じnamespaceを使う。
3. namespaceを資料から確定できない定義を、名前やlocal IDの一致だけで既存namespaceへ統合しない。
4. schema version `0.6`を使い、全nodeへ`namespace`と元定義の`localId`を設定する。
5. canonical `id`は`bsid1:<namespaceのUTF-8 byte長>:<namespace>:<localId>`で生成する。node type、入力ファイル名、入力順をidentityへ含めない。生成したcanonical IDは既存のnode ID上限1024文字以内でなければならない。
6. 同一namespaceに同じlocalIdの別定義が存在する場合は競合として扱い、勝手にsuffix等を付けて別identityへしない。
7. 管理単位、ジョブネット、ジョブの単一親階層を抽出し、`parentId`は同じnamespaceのcanonical IDだけを参照する。
8. ジョブマネージャーが定義する先行後続関係を抽出する。
9. ファイル、ファイルパターン、ジョブ状態、外部イベントを特定する。共有resourceが意味上別の定義セットに属する場合は、そのresourceにも安定したnamespaceを割り当てる。
10. namespaceを跨ぐ依存も調べる。別環境のジョブ状態監視、共有ファイルの生成/監視、外部イベント等が確認できた場合は、resource nodeを介したrelationまたは根拠付きcross-namespace relationとして明示する。
11. **namespaceが異なること、localIdが同じことだけを理由にrelationを作らない。** relationの`fromId`/`toId`はそれぞれのcanonical IDを使う。
12. 推定した依存関係へ`origin`と`certainty`を付ける。
13. リミットは元の設定として抽出し、検索時の優先順位を付けない。
14. 取得できる場合は、元の表記を`sourceText`、判定根拠を`evidence`へ保存する。
15. 根拠がないノード、依存関係、リミットを`confirmed`として生成しない。
16. JSON Schemaに従って`nodes.ndjson`と`relations.ndjson`を生成する。
17. JSON形式、canonical identity、parent、relation参照を検査し、`manifest.json`を生成してアーカイブへまとめる。
18. `Content-Type: application/vnd.batchscope.snapshot+gzip`を指定し、`POST /v1/snapshot-imports`でアーカイブを送信する。
19. `202 Accepted`の`Location`が示す取込状況URIを、`state=succeeded`になるまで確認する。`state=failed`になった場合は取込リソースの`error`を確認して終了する。
20. `GET /v1/snapshots/current`で有効な世代の`snapshotId`、schema version、件数を確認する。

専用のBatchScope CLIは前提にしない。
検査、梱包、送信には、利用環境の`jq`、`tar`、`curl`などを使う。
取込API側でも、JSON Schema、canonical identity、参照関係を必ず再検査する。

## JSON Schemaの参照先

source checkoutではリポジトリルートの`schema/`、GitHub Releaseの配布物ではこのSkill内の`references/schema/`を参照する。
Schemaのフィールドと機械的制約は参照先を正本とし、このSkillの説明から推測して補わない。

## 検索結果の読み方

BatchScopeが返す値は、定義に保存されたリミットである。
現在の障害に対する復旧期限ではない。

経路ツリーは、返却したリミットとリミットが見つからなかった経路の到達地点までを表す。
後続全体を表すものではない。

namespaceはidentityの区別であり、依存解析の境界ではない。
`main`から`dr`へ経路が続く場合も、明示されたrelationを通常どおりたどった結果として読む。

後続リミット取得のパラメーター、圧縮、参照、循環、リミット未通過経路の読み方は[`references/downstream-limit-analysis.md`](references/downstream-limit-analysis.md)に従う。

`evidence`がないことは、その依存関係が誤りであることを意味しない。
新しい根拠がない限り、`inferred`または`candidate`を確認済みとして扱わない。

## 参照資料

- `references/canonical-snapshot.md`
- `references/downstream-limit-analysis.md`
- `references/normalization-rules.md`
