# 運用

## サービスの状態

```mermaid
stateDiagram-v2
    [*] --> empty
    empty --> importing: 初回取込を開始
    importing --> ready: 初回取込に成功
    importing --> empty: 初回取込に失敗
    ready --> importing: 更新取込を開始
    importing --> ready: 更新取込に成功
    importing --> ready: 更新取込に失敗、既存データを継続
```

| 状態 | 検索 | `/readyz` | 説明 |
|---|---|---:|---|
| `empty` | 不可 | 503 | 検索に使えるスナップショットがない |
| `importing` | 既存データがあれば可 | 既存データの有無による | 新しいスナップショットを検査し、SQLiteを作成している |
| `ready` | 可 | 200 | 検査済みのスナップショットを使用している |

サービス状態の`empty`、`importing`、`ready`は、検索できる世代と進行中の取込の有無を表します。
取込リソースの状態は、個別の取込処理の進行と失敗を表す別の概念です。
状態の意味と公開項目は[API仕様の取込状況](design/api.md#取込状況)を参照してください。

更新取込中も検索できる世代がある場合は、切替前の世代を継続して利用できます。

## SQLiteの切替

取込中は、現在世代とは別に新しいSQLiteを準備します。
検査に成功した場合だけ検索先を切り替え、失敗した場合は現在世代を維持します。

切替中の検索と旧世代の寿命を含む保証は、[SQLiteの世代切替](design/storage.md#sqliteの世代切替)を参照してください。

データディレクトリの既定値は`/tmp/batchscope`です。
`BATCHSCOPE_DATA_DIR`で変更できます。

## 一時データ

コンテナ内のSQLiteは、再起動やインスタンスの置換で失われる場合があります。
サービスは、起動時にSQLiteが残っていることを前提にしません。

外部ローダーは`/readyz`または`/v1/status`を確認し、必要に応じて最新のスナップショットを再投入します。
永続化、共有状態、複数インスタンス間の同期はMVPに含めません。

この構成は、ローカルのDockerやPodmanと、マネージドコンテナ基盤で使用できます。
基盤固有の設定は、別のデプロイガイドで扱います。

Cloud Runの書き込み可能な組み込みファイルシステムは一時領域です。
Cloud Runへ32 MiBを超えるアーカイブを直接送る場合は、通信経路全体でHTTP/2を使う必要があります。

## 取込時の判断

`202 Accepted`は、リクエストボディの保存が完了し、非同期処理を開始したことを示します。
取込の成功を示すものではないため、運用者は`Location`の取込リソースが終端状態になるまで確認します。

取込が`failed`になった場合は、`error`の種別、理由、入力位置を確認します。
更新取込の失敗では現在世代を維持するため、検索可否はサービス状態で別に確認します。

検索先の切替が完了した後に旧世代の後始末だけが失敗した場合は、警告ログを調査します。
すでに公開した新世代を取込失敗へ戻しません。

## 取込履歴

取込リソースはプロセスメモリ内で有界に保持します。
進行中の取込に加えて、完了した取込は`succeeded`と`failed`を合わせて新しいものから16件まで保持し、17件目の完了時に最も古い履歴を破棄します。

取込履歴は再起動をまたいで永続化しません。
破棄済みまたは再起動前の`importId`を`GET /v1/snapshot-imports/{importId}`へ指定した場合は、`import-not-found`を返します。

## 検索と取込の同時実行

同時に実行できる取込は一件です。
検索は取込と並行して実行できます。
検索と世代切替の保証は[SQLiteの世代切替](design/storage.md#sqliteの世代切替)を参照してください。

## 環境変数

| 環境変数 | 既定値 | 意味 |
|---|---|---|
| `PORT` | `8080` | 待受ポート |
| `BATCHSCOPE_DATA_DIR` | `/tmp/batchscope` | SQLiteと一時ファイルの保存先 |

不正な`PORT`を指定した場合は起動に失敗します。
入力サイズと表示上の上限は、MVPではコード内の定数として管理します。
環境ごとに変更する必要が生じた項目だけを、後から設定項目へ追加します。

## サービス側の上限

入力ファイルには、次の上限を適用します。
上限はコード内の定数で管理し、API利用者からは変更できません。

| 項目 | 初期上限 |
|---|---:|
| 圧縮アーカイブ | 500 MiB |
| リクエストボディの受信 | 10分 |
| 展開後合計 | 4 GiB |
| `manifest.json` | 1 MiB |
| `nodes.ndjson`と`relations.ndjson`の一行 | 16 MiB |

受信上限は、一件だけの取込枠を送信途中の要求が保持し続けることを防ぎます。
10分を超えた要求は`invalid-request`の`400 Bad Request`となり、取込予約と受信中の一時ファイルを破棄します。
値の根拠と検索SLOとの違いは[API仕様の大容量リクエストの制御](design/api.md#大容量リクエストの制御)を参照してください。

## 初期対応規模

現在の対応規模は、実運用で確認された40万ノード級のsnapshotと、形状の異なる高密度relationデータを再測定して決めています。
この値は形式上のJSON Schema上限ではなく、受入済みsnapshotを検索時の件数上限で打ち切らず完全解析するための製品上限です。

| 項目 | 対応規模 |
|---|---:|
| ノード数 | 400,000 |
| relation数 | 300,000 |
| リミット設定数 | 5,000 |
| 一つのSCCに含まれるノード数 | 3,000 |
| ジョブネット階層の深さ | 64 |
| 想定同時検索数 | 4 |

400,000ノード / 300,000 relation / 5,000リミットの決定的なoperational profileでは、snapshot全体から一つのtargetで全件へ到達する場合も`Traverse -> Scan -> Build`を完遂しました。
一方、100,000ノード / 300,000 relationの高密度形状は、ノード数が少なくても経路ツリー計算の負荷が高くなります。
そのため対応規模は件数だけを性能SLOとはみなしません。受入条件は全件解析を保証する容量境界、性能目標は代表的な検索負荷に対する目標として分けます。

正常な検索では、深さ、探索状態数、relation読込数、リミット件数、tree node件数による打切りを行いません。
解析を完了できない場合は部分結果を成功として返さず、時間上限や内部エラーとして失敗させます。

測定条件と詳細値は[性能測定結果](development/performance-measurement.md#issue-52-実運用40万ノード級の再測定)を参照してください。

## 初期の資源見積り

Issue #52の再測定はGitHub ActionsのUbuntu 24.04、linux/amd64、Go 1.26.5、4 CPU（`GOMAXPROCS=4`）で行いました。
40万ノード級のoperational profileにおける主要な測定値は次のとおりです。

| 処理 | p95または最大値 |
|---|---:|
| snapshot取込全体 | 48.3 s |
| 取込時Heap増分 | 272 MiB |
| 取込時RSS増分 | 280 MiB |
| SQLite | 147 MiB |
| 一時ディスク | 174 MiB |
| 代表targetの内部解析p95 | 2.93 ms |
| 全40万ノード到達targetの内部解析p95 | 15.2 s |
| 全40万ノード到達targetのcold Heap増分 | 1.47 GiB |
| 全40万ノード到達targetのcold RSS増分 | 1.56 GiB |

代表targetの公開HTTP測定では、並行度4のp95がcold 12.24 ms、warm 10.51 msでした。
全40万ノードへ到達する`OPS-ROOT`もDTO組立てとJSON書込みまで完走し、4 vCPU上の並行度4でp95はcold 43.93 s、warm 42.81 sでした。
このstress測定プロセスの最大RSSは9,402,744 KiB（約8.97 GiB）でした。測定ハーネスは`httptest.ResponseRecorder`で完全な応答bodyを保持するため、この値をそのまま本番HTTPサーバーの必要量とは扱いませんが、8 GiBを最悪target 4件同時実行の十分条件とはみなしません。
高密度な100,000ノード / 300,000 relation形状では、メモリ削減後の内部解析p95が約25.9 s、RSS増分が約588 MiBでした。

代表的な検索負荷を想定する初期運用では4 vCPU、8 GiB以上のメモリ、`data-dir`に6 GiB以上の空きを推奨します。
受入上限に近い全件到達targetを4件同時に完遂させる必要がある場合は、今回のstress測定を基準に12 GiB以上のメモリを確保してください。
これらは取込アーカイブ500 MiB、展開後4 GiBの安全上限、世代別SQLite、全件HTTPのDTO/JSON化と測定した検索メモリへ余裕を持たせるための運用推奨であり、ホスト全体の厳密な必要量を固定する契約ではありません。

後続リミット取得のp95 1秒は、キャッシュに乗った代表的な対象を想定する性能目標です。
snapshot全体へ到達するような最悪形状を1秒以内に切り捨てる契約ではありません。
受入済みsnapshotの完全解析保証を優先し、異常時の解析deadlineは60秒とします。

## graceful shutdown

BatchScopeは`SIGTERM`などによる停止時、処理中のHTTP要求を最大60秒待ってから終了します。
この猶予を有効にするには、コンテナランタイムやオーケストレータ側のtermination graceも60秒以上必要です。

Cloud Run serviceはインスタンス停止時に`SIGTERM`を送った後、10秒後に`SIGKILL`を送る場合があります。そのためCloud Run側が処理中の要求に対して停止を開始したケースでは、BatchScope内部の60秒graceだけで長時間検索の完遂を保証できません。
これは通常の要求処理に適用する60秒の解析deadlineとは別の制約です。停止時にも全件解析の完遂が必要な実行基盤では、60秒以上のtermination graceを設定してください。

## ログ

MVPの観測情報は、Goの`log/slog`による構造化ログを正本とします。
共通フィールドは`internal/observability`で定義し、値が設定された項目だけを出力します。

| 用途 | 項目 |
|---|---|
| 要求の識別 | `request_id`、`operation`、`duration_ms` |
| プロセスとデータ | `boot_id`、`snapshot_id`、`import_id` |
| 検索対象 | `target_id` |
| 処理量 | `reached_nodes`、`returned_tree_nodes`、`returned_limits`、`returned_targets`、`uncovered_routes`、`node_count`、`relation_count`、`limit_count` |
| 完了状態 | `cycles_detected`、`import_state`、`error_type` |

`duration_ms`は正の処理時間がある場合だけ出力します。
件数フィールドは0の場合に省略するため、ログ集計では省略を0として扱います。

ジョブ名、完全パス、検索の`query`、`evidence`、入力資料の内容は、既定ログへ出力しません。
スナップショット取込では`operation=snapshot_import`に固定し、受信開始から非同期処理の完了までを一件の完了ログとして記録します。
このログは、上表の要求識別、プロセスとデータ、取込件数、完了状態の項目を使用し、失敗時だけ`error_type`を記録します。
完全一致検索では`operation`を`target_search`に固定し、`returned_targets`へ返却件数を記録します。
後続リミット取得では`operation=downstream_limit_analysis`に固定し、到達ノード、返却ツリーノード、返却リミット、循環、リミット未通過経路の件数を記録します。
`error_type=analysis-timeout`は、後続リミット取得が時間上限を超えた場合または要求がキャンセルされた場合を表し、部分結果が返されたことを意味しません。

## メトリクス

MVPは専用のメトリクスexporterと`/metrics`エンドポイントを実装していません。
次の名前は自動公開するメトリクスではなく、構造化ログを集計する場合の指標名です。

| 指標名 | 構造化ログからの導出方法 |
|---|---|
| `snapshot_import_duration_seconds` | `operation=snapshot_import`の`duration_ms`を1,000で割る |
| `snapshot_import_failures_total` | `operation=snapshot_import`かつ`import_state=failed`の完了ログを数える |
| `target_lookup_duration_seconds` | `operation=target_search`の`duration_ms`を1,000で割る |
| `limit_analysis_duration_seconds` | `operation=downstream_limit_analysis`の`duration_ms`を1,000で割る |
| `limit_analysis_reached_nodes` | `operation=downstream_limit_analysis`の`reached_nodes`を集計する |
| `limit_analysis_returned_tree_nodes` | `operation=downstream_limit_analysis`の`returned_tree_nodes`を集計する |
| `limit_analysis_returned_limits` | `operation=downstream_limit_analysis`の`returned_limits`を集計する |
| `limit_analysis_cycles_total` | `operation=downstream_limit_analysis`の`cycles_detected`を集計する |
| `limit_analysis_uncovered_routes` | `operation=downstream_limit_analysis`の`uncovered_routes`を集計する |

各APIの実装では、`operation`の値を処理ごとに固定してからログ集計へ使用します。
専用メトリクスの公開が必要になった場合は、別Issueでexporterまたはエンドポイントを設計します。

## 性能目標

次の値は、データがキャッシュへ読み込まれた状態で、ローカルSQLiteを使う場合の初期目標です。

| 処理 | p95目標 |
|---|---:|
| 完全一致検索 | 200 ms |
| 後続リミットの取得 | 1秒 |
| 状態確認 | 100 ms |

500 MiBは入力サイズの上限であり、取込時間の保証値ではありません。

対応規模と同時検索数の根拠は[性能測定結果](development/performance-measurement.md)を参照してください。
公開HTTPを含む条件と結果は、[完全一致検索のHTTP性能測定結果](development/target-search-performance.md)と[後続リミット取得のHTTP性能測定結果](development/limit-analysis-performance.md)を参照してください。
