---
name: batchscope
description: ジョブマネージャーの定義をBatchScopeの入力形式へ変換して取り込み、指定したジョブまたはジョブネットの後続リミットと依存経路を調べる。
---

# BatchScope

このスキルは、スナップショットの作成、取込、検索に使用する。

## APIから検索する手順

1. `GET /readyz`で検索可能な状態か確認する。
2. 対象IDが不明な場合は、完全な名前または完全パスを指定して`GET /v1/targets`を呼び出す。
3. 複数件が一致した場合は、ID、種別、完全パス、上位の管理単位を提示する。
4. 利用者の確認なしに対象を選ばない。
5. 確定した対象IDを`targetId`へ指定し、必要な場合だけ`includeEvidence=true`を加えて`GET /v1/downstream-limit-analysis`を呼び出す。
6. ジョブ指定時は対象自身のリミットを全件説明し、ジョブネット指定時は配下のリミット設定済みジョブを全件説明する。
7. 経路を説明するときは、`tree[].viaRelations`の種類、生成元、確実性を省略しない。
8. 後続リミットはAPIの返却順を維持し、返却順が緊急度を表すとは説明しない。
9. `declared`と`confirmed`を、`inferred`と`candidate`から区別する。
10. 圧縮区間は`hiddenConnections`の各接続を順にたどり、省略されたジョブ数だけで経路を説明しない。
11. 循環は`cycles[].nodes`の強連結成分と、`cycles[].route`の一周分の表示経路を区別して説明する。
12. `uncoveredRoutes`は境界ノード単体ではなく、その境界へ至るリミット未通過経路の判定として説明する。
13. 成功レスポンスは全件解析済みとして扱い、Problem Detailsが返った場合は部分結果を補わない。
    `analysis-timeout`の場合は、再試行するかを利用者に判断してもらう。
14. BatchScopeは現在時刻、実行状態、業務日を使った期限計算を行わないため、実際の対応判断は利用者がジョブマネージャー上の状態と照合して行う。

## スナップショットを作る手順

1. ジョブマネージャーの出力、実行スクリプト、ジョブ定義、補助資料を確認する。
2. 管理単位、ジョブネット、ジョブの単一親階層を抽出する。
3. ジョブマネージャーが定義する先行後続関係を抽出する。
4. ファイル、ファイルパターン、ジョブ状態、外部イベントを特定する。
5. 推定した依存関係へ`origin`と`certainty`を付ける。
6. リミットは元の設定として抽出し、検索時の優先順位を付けない。
7. 取得できる場合は、元の表記を`sourceText`、判定根拠を`evidence`へ保存する。
8. 根拠がないノード、依存関係、リミットを`confirmed`として生成しない。
9. JSON Schemaに従って`nodes.ndjson`と`relations.ndjson`を生成する。
10. JSON形式と参照関係を検査し、`manifest.json`を生成してアーカイブへまとめる。
11. `POST /v1/snapshot-imports`でアーカイブを送信する。
12. 取込状況を確認し、成功後に`GET /v1/snapshots/current`で`snapshotId`を確認する。

専用のBatchScope CLIは前提にしない。
検査、梱包、送信には、利用環境の`jq`、`tar`、`curl`などを使う。
取込API側でも、JSON Schemaと参照関係を必ず再検査する。

## 検索結果の読み方

BatchScopeが返す値は、定義に保存されたリミットである。
現在の障害に対する復旧期限ではない。

経路ツリーは、返却したリミットとリミットが見つからなかった経路の到達地点までを表す。
後続全体を表すものではない。

後続リミット取得のパラメーター、圧縮、参照、循環、リミット未通過経路の読み方は[`references/downstream-limit-analysis.md`](references/downstream-limit-analysis.md)に従う。

`evidence`がないことは、その依存関係が誤りであることを意味しない。
新しい根拠がない限り、`inferred`または`candidate`を確認済みとして扱わない。

## 参照資料

- `references/canonical-snapshot.md`
- `references/downstream-limit-analysis.md`
- `references/normalization-rules.md`
