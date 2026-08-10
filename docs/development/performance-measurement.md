# Issue #14の性能測定結果

- 測定日：2026-08-10
- 測定対象：Issue #14の作業ツリー
- 基準コミット：`163d3ceafb462c9cd192b61df5572a9128a08331`

測定手順と各指標の定義は[テストと受入条件](testing.md#性能測定)を正本とします。
この文書は、Issue #32で対応規模と検索方式を判断するための測定条件と結果を記録します。

## 測定環境

| 項目 | 値 |
|---|---|
| CPU | Apple arm64、10コア、1コア当たり1スレッド |
| 総メモリ | 8,216,862,720 bytes（7.65 GiB） |
| 利用可能メモリ | 中間規模測定の開始前は4,561,743,872 bytes（4.25 GiB）。Medium解析の試行時は約3.3 GiB |
| Swap | 1,073,737,728 bytes中、測定開始前の空きは40,730,624 bytes（38.8 MiB） |
| OS | Debian GNU/Linux 12（bookworm）、Linux 6.12.76-linuxkit、arm64 |
| Go | `go1.26.5 linux/arm64` |
| `GOMAXPROCS` | 10 |
| SQLiteドライバ | `modernc.org/sqlite v1.56.0` |

利用可能メモリは、ほかのプロセスとページキャッシュの状態で変化します。
Medium解析の失敗時と中間規模測定の開始時で値が異なるため、両方を記録しています。

## 測定コマンド

実行環境：Dev Container

```bash
GOCACHE=/tmp/batchscope-go-cache \
  go run ./cmd/perf-measure -profile small -runs 3 \
  > /tmp/batchscope-perf-small-clean.json

GOCACHE=/tmp/batchscope-go-cache \
  make perf-growth PERF_GROWTH_RUNS=2

GOCACHE=/tmp/batchscope-go-cache \
  go run ./cmd/perf-measure -mode import -profile medium -runs 2 \
  > /tmp/batchscope-perf-medium-import.json

GOCACHE=/tmp/batchscope-go-cache \
  go run ./cmd/perf-measure -mode concurrent -profile small \
  -runs 2 -concurrencies 1,2,4,8 \
  > /tmp/batchscope-perf-concurrent-final.json

GOCACHE=/tmp/batchscope-go-cache \
  go test -run TestSearchKeepsOneSnapshotGenerationAcrossConcurrentSwitch \
  -count=1 -v ./internal/importer
```

Smallは3反復、中間規模とMedium取込は2反復です。
並行負荷は各並行度を2ラウンド実行しました。

`cold`は`PRAGMA shrink_memory`でSQLite接続のページキャッシュを解放した状態、`warm`は直前の`cold`から同じ接続を再利用した状態です。
OSのページキャッシュはどちらも保持しており、ストレージを含む完全なcold startではありません。

## Small

Smallの入力は10,000ノード、25,000 relationで、アーカイブのSHA-256は`a0f8c293eb2db965f79c759dac09ee692bfa38b93619a3c69f7d98b6200b9359`です。

取込の3反復における中央値と、メモリおよびディスクの最大値は次のとおりです。

| 指標 | 値 |
|---|---:|
| 取込全体の中央値 | 1.957 s |
| 受信の中央値 | 0.118 ms |
| 展開の中央値 | 348.733 ms |
| 検査の中央値 | 614.095 ms |
| SQLite登録の中央値 | 692.384 ms |
| 完了処理の中央値 | 286.247 ms |
| Heap増分の最大 | 16,074,912 bytes（15.33 MiB） |
| RSS増分の最大 | 14,667,776 bytes（13.99 MiB） |
| 一時ディスクの最大 | 9,809,500 bytes（9.35 MiB） |
| SQLiteサイズ | 7,864,320 bytes（7.50 MiB） |

静的解析の段階別時間は各3反復の中央値です。
ダイジェスト生成時間は解析全体に含めていません。

| 対象 | 状態 | `Traverse` | `Scan` | `Build` | 解析全体 | ダイジェスト生成 |
|---|---|---:|---:|---:|---:|---:|
| `NET-TARGET` | cold | 166.402 ms | 14.283 ms | 245.870 ms | 437.476 ms | 57.385 ms |
| `NET-TARGET` | warm | 170.189 ms | 14.760 ms | 233.040 ms | 421.870 ms | 50.562 ms |
| `JOB-TARGET` | cold | 170.150 ms | 14.878 ms | 215.355 ms | 403.186 ms | 49.281 ms |
| `JOB-TARGET` | warm | 168.176 ms | 14.271 ms | 224.560 ms | 407.048 ms | 51.965 ms |

`NET-TARGET`の各反復でStatsと結果件数は一致しました。

| 指標 | 値 |
|---|---:|
| `ExpandedNodes`、到達ノード | 9,998 |
| `RelationQueries` | 225 |
| `RelationRows`、relation接続 | 25,000 |
| `ScopeQueries` | 4 |
| `ScopeRows`、scope接続 | 5 |
| SCC | 3件、最大3ノード |
| リミット | 49件 |
| 経路ツリーノード | 24,906 |
| `HiddenConnections` | 101 |
| `UncoveredRoutes` | 1 |

Small検索の最大Heap増分は123,381,512 bytes（117.67 MiB）、最大RSS増分は146,800,640 bytes（140.00 MiB）でした。
すべての反復、対象、coldとwarmで解析結果のSHA-256は一致しました。

## 中間規模

中間規模はrelation数をノード数の2.5倍とし、各規模を別プロセスで2反復しました。
段階別時間は`NET-TARGET`のcoldにおける中央値、HeapとRSSは二対象のcoldとwarmを含む全実行の最大値です。

| ノード | relation | 取込全体 | `Traverse` | `Scan` | `Build` | 解析全体 | 最大Heap | 最大RSS | SQLite |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 25,000 | 1.919 s | 171.733 ms | 14.648 ms | 263.771 ms | 450.236 ms | 120.45 MiB | 189.42 MiB | 7.50 MiB |
| 20,000 | 50,000 | 3.903 s | 373.953 ms | 32.519 ms | 769.535 ms | 1.176 s | 349.86 MiB | 472.54 MiB | 15.06 MiB |
| 40,000 | 100,000 | 8.319 s | 942.723 ms | 69.996 ms | 3.115 s | 4.128 s | 1.020 GiB | 1.217 GiB | 30.12 MiB |
| 80,000 | 200,000 | 15.082 s | 1.809 s | 133.634 ms | 11.759 s | 13.702 s | 3.425 GiB | 3.906 GiB | 60.36 MiB |

各入力アーカイブのSHA-256は次のとおりです。

| ノード、relation | SHA-256 |
|---|---|
| 10,000、25,000 | `7b8c0f585633a2761fa0f586cd67ba17ce63f8d4dcf1b6dd521b48ffcc82e33a` |
| 20,000、50,000 | `d212a4551094a379bcce5f85413006ed9014ab69f636e11816d28f60d0370f6d` |
| 40,000、100,000 | `84152920f29d9136b83512a967275d249f1de8dc87ba724f29446dd12ec0d3ab` |
| 80,000、200,000 | `af395a073d6b6ff562e2516e35fa4a6040dc1e09e6bfaad5c99ae2cc7be70aa1` |

relation数を倍にしたとき、最大Heapは2.90倍、2.99倍、3.36倍と増えました。
最大RSSは2.49倍、2.64倍、3.21倍と増えました。
入力規模に対して線形ではなく、規模が大きくなるほど`Build`の時間が解析全体を占めました。

## Medium

Mediumの入力は100,000ノード、300,000 relationで、アーカイブのSHA-256は`789922fd1b30bcd547eb91931bfafd98213ef3f9415c99405cb74d269684c055`です。
取込は2反復とも完走しました。

| 指標 | 値 |
|---|---:|
| 取込全体の中央値 | 21.310 s |
| 受信の中央値 | 0.890 ms |
| 展開の中央値 | 3.933 s |
| 検査の中央値 | 4.665 s |
| SQLite登録の中央値 | 8.861 s |
| 完了処理の中央値 | 3.834 s |
| Heap増分の最大 | 85,809,528 bytes（81.83 MiB） |
| RSS増分の最大 | 81,801,216 bytes（78.01 MiB） |
| 一時ディスクの最大 | 114,029,672 bytes（108.75 MiB） |
| SQLiteサイズ | 91,832,320 bytes（87.58 MiB） |

静的解析は同じDev Containerで二回試行し、どちらもOOM killerに終了され、JSONを出力するまで完走しませんでした。
一回は委任担当、もう一回は主担当が独立に再実行して同じ結果を確認しました。

Mediumの必要メモリは、中間規模のrelation数と最大Heapまたは最大RSSを両対数へ変換し、4点の最小二乗直線を300,000 relationまで外挿すると、Heap 6.34 GiB、RSS 6.58 GiBです。
増加が最も大きかった100,000 relationから200,000 relationの二点だけを使って外挿すると、Heap 6.96 GiB、RSS 7.73 GiBです。
これらは未完走のMediumを直接測った値ではなく、実測した中間規模から求めた**推定値**です。

推定した最大RSSは6.6 GiBから7.7 GiBであり、失敗時の利用可能メモリ約3.3 GiBを3.3 GiBから4.4 GiB上回ります。
総メモリ7.65 GiBに対してもOSとほかのプロセスの分を残せないため、OOMで完走しない結果と整合します。
Mediumの完全な解析測定には、少なくとも8 GiBの利用可能メモリを持つ環境を目安とします。

## Scale

Scaleの取込と静的解析は測定していません。
Mediumの解析が完走しない環境で1,000,000ノード、3,000,000 relationを開始すると、結果を得ずに資源を消費するためです。
MediumからScaleまでを一つの外挿で推定するには規模差が大きいため、Scaleの必要メモリも推定していません。

## 並行負荷

Smallの`NET-TARGET`を同じSQLiteへ接続して測定しました。
レイテンシは全検索の分布、スループット、接続待ち回数、接続待ち時間は2ラウンドの分布です。

| 並行度 | レイテンシ最小 | 中央値 | p95 | 最大 | スループット中央値 | 接続待ち回数中央値 | 接続待ち時間中央値 |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 411.075 ms | 417.178 ms | 423.280 ms | 423.280 ms | 2.40 search/s | 0 | 0 s |
| 2 | 634.089 ms | 647.541 ms | 689.784 ms | 689.784 ms | 3.01 search/s | 498 | 325.932 ms |
| 4 | 997.067 ms | 1.056 s | 1.118 s | 1.118 s | 3.69 search/s | 979 | 2.015 s |
| 8 | 1.828 s | 2.072 s | 2.251 s | 2.251 s | 3.69 search/s | 1,972 | 9.947 s |

全ラウンドで`MaxOpenConnections`は1でした。
並行度2から接続待ちが発生し、並行度4から8ではスループットが約3.69 search/sのまま、中央値レイテンシと接続待ち時間が増えました。
単一SQLite接続がSQL実行を直列化している観測結果です。
接続方式を変更するかは、この結果だけでは決めません。

## 世代切替

`TestSearchKeepsOneSnapshotGenerationAcrossConcurrentSwitch`は成功し、実行時間は70.032 msでした。
切替前に開始した検索は旧世代のノード、リミット、経路ツリーだけを返し、切替後に開始した検索は新世代だけを返しました。
一つの検索結果に旧世代と新世代は混在しませんでした。

## 判断先

この測定では、正式な対応規模、SQLiteの接続方式、SQLite方式を継続するかを決めません。
対応規模と保存または索引方式はIssue #32で判断します。

公開HTTPのp95は測定していません。
完全一致検索はIssue #10、後続リミット解析はIssue #13で公開HTTPを実装し、それぞれの条件で確認します。
