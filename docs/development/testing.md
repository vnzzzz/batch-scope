# テスト戦略

BatchScopeのテストは、テスト数やcoverage率ではなく、公開する入出力形式と処理中に維持する不変条件を守るために配置します。
同じ値を複数レイヤーで確認する場合は、各レイヤーで検出するfailure modeが異なることを条件とします。

## 守る対象

### 受入済みスナップショットの完全な検索

受入済みスナップショットの後続リミット取得は、検索時のdepth、state、relation、limit、tree nodeの件数を理由に部分結果へ打ち切りません。
対象、配下、後続の全リミット、経路の接続、循環、リミット未通過経路を、[後続リミットの検索](../design/dependency-analysis.md)の規則に従って返します。

キャンセル、deadline超過、SQLiteエラー、内部不整合では、蓄積済みの結果を`200 OK`として返しません。
HTTP層は[API仕様](../design/api.md#エラー)に定めたProblem Detailsへ変換します。

### 検索世代の一貫性

一回の検索は、SQLiteと検索世代メタデータを同じ世代、同じ参照寿命で扱います。
世代切替前に開始した検索は、処理を終えて参照を解放するまで旧SQLiteを保持します。
新しい世代の準備または切替に失敗した場合は、現在世代を検索可能な状態で維持します。

### スナップショット取込の資源管理

取込予約はrequest bodyを読む前に確保し、競合する取込のbodyを消費せずに拒否します。
受信失敗と受信deadlineでは、socket readを中断し、取込予約と一時ファイルを解放します。
実際のsocket readの中断は、`httptest`の直接呼出しではなく実TCPで検査します。

### 完全一致検索

ID、名前、パスの正規化規則と検索結果の順序は、[SQLiteの完全一致検索の前処理](../design/storage.md#完全一致検索の前処理)と[API仕様](../design/api.md#対象の検索)を正本とします。
空文字、空白のみの値、最大返却件数、`truncated`、同一ノードの重複排除を境界値として検査します。

### 入出力形式

取込データは`schema/`のJSON Schemaと[取込データの形式](../design/canonical-snapshot.md)に定めた単体制約とレコード間制約を検査します。
HTTPテストはstatus code、Problem Details、header、公開DTOの必須項目と境界値を検査します。

生成したOpenAPIは実装と一致させます。
デモレスポンスは、デモスナップショットを実際に取り込んだHTTPレスポンス全体と一致させます。

### 情報の非公開

既定ログには、検索query、ジョブ名、完全パス、evidence、入力内容を出しません。
正常系と入力エラーの両方で、構造化ログに必要な識別子と件数だけが残ることを検査します。

## テストレイヤー

| レイヤー | 主なfailure mode |
|---|---|
| pure functionまたはunit test | 正規化、変換規則、同じ入力に対する並び順が変わる |
| storeまたはintegration test | SQLiteの制約、transaction、世代の参照寿命、並行切替でデータが混在する |
| HTTP integration test | status code、Problem Details、DTO、header、cancelの写像が公開仕様と異なる |
| 実TCP test | upload deadlineがsocket readを中断せず、取込資源を保持し続ける |
| demo response test | 取込から公開DTOまでの代表フローで、個別レイヤーの組合せが崩れる |
| local smoke test | 実プロセスの起動、公開ポート、公開フローの接続が成立しない |

同じ仕様を下位レイヤーとHTTPレイヤーで検査する場合、下位レイヤーは規則そのもの、HTTPレイヤーは公開形式への写像を担当します。
同じ入力、同じfixture、同じassertionで同じ不具合を検出するテストは統合します。

private helperの配置、内部の呼出順、複数の不正入力がある場合の未規定なエラー優先順は固定しません。
過去の実装を削除した事実だけを守るテストも置きません。

## 通常検査

実行環境：Dev ContainerまたはCI

```bash
make verify
```

`make verify`はGoのformat、shell scriptの構文、`go vet`、race detector付きのテスト、生成OpenAPIと実装の差分を検査します。
通常のテストは、初期対応規模の受入境界と軽量な病理グラフを件数の合否に使いますが、実行時間やメモリ量の性能閾値を合否に使いません。

Issue #40で追加するlocal smoke testは、実プロセスを起動した公開フローを確認する役割を持ちます。
実装後もunit testやintegration testの代替にはせず、`make verify`との役割を分けます。

## 性能測定用データ

性能用データは`internal/testsupport/graphgen`が決まった規則で生成します。
同じプロファイルからは、同じノード、relation、期待値、アーカイブを生成します。

| プロファイル | 規模またはケース | 解析対象 | 実行環境と用途 |
|---|---:|---|---|
| Small | 10,000ノード、25,000 relation | `NET-TARGET`、`JOB-TARGET` | Dev ContainerとCIで取込から経路ツリーまでを検査する |
| Medium | 100,000ノード、300,000 relation | `NET-TARGET`、`JOB-TARGET` | 8 GiB以上の利用可能メモリを目安とする専用環境で測定する |
| Scale | 1,000,000ノード、3,000,000 relation | `NET-TARGET`、`JOB-TARGET` | Mediumより大きい専用環境で上限値の判断材料を測定する |
| Pathological | 個別の軽量ケース | ケースごとの一対象 | Dev ContainerとCIで規模以外の形状を検査する |

`CapacityBoundary`はノード数、relation数、リミット設定数を受入上限へ合わせた有向非巡回グラフを生成します。
`DenseSCC`はringへ弦を加え、指定したサイズのSCCとその内外のリミット設定を生成します。

公開上限の1,000万ノードと5,000万依存関係は、受入条件ではなく入力拒否の上限です。
実際に受け入れる初期対応規模は[運用](../operations.md#初期対応規模)を正本とします。

## Issue #32の対応規模境界

Issue #32では、Dev Containerで`go test`を介して取込後の`Traverse`、`Scan`、`Build`を実行しました。
表の内部処理合計は三処理の合計であり、HTTP層のDTO組立てとJSON化を含みません。
`alloc`は解析前後の`runtime.MemStats.TotalAlloc`の増分であり、解析中の累積割当量を示します。
`heapInUse`は同じ前後の`runtime.MemStats.HeapInuse`の増分であり、最大RSSと最大HeapをサンプリングするIssue #14の指標とは異なります。

リミット設定数の測定では、`CapacityBoundary(10_000, 25_000, limitCount)`を使い、ノード数とrelation数を受入上限に固定しました。

| リミット設定数 | 内部処理合計 | `Build` | alloc | heapInUse | 経路ツリー |
|---:|---:|---:|---:|---:|---:|
| 53 | 1.036 s | 742 ms | 1,113.2 MiB | 836.3 MiB | 25,001 |
| 500 | 1.087 s | 792 ms | 1,114.2 MiB | 727.4 MiB | 25,001 |
| 1,000 | 1.021 s | 719 ms | 1,115.2 MiB | 833.6 MiB | 25,001 |
| 2,000 | 1.025 s | 723 ms | 1,117.6 MiB | 759.4 MiB | 25,001 |
| 5,000 | 1.128 s | 799 ms | 1,125.3 MiB | 844.0 MiB | 25,001 |

すべての条件で欠落、メモリ不足、異常終了なく完了し、宣言したリミット設定を全件返しました。
リミット設定数を53件から5,000件へ約94倍にしても内部処理合計の増加は約9%であり、リミット設定数は処理時間の支配要因ではありませんでした。
この結果に基づき、リミット設定数の受入上限は5,000件を維持します。

SCCサイズの測定では、`DenseSCC(size, size)`を使いました。
ノード数は`size + 2`、relation数は`2 × size + 2`です。

| SCCサイズ | 内部処理合計 | `Build` | alloc | 経路ツリー |
|---:|---:|---:|---:|---:|
| 1,000 | 85 ms | 71 ms | 19.5 MiB | 2,003 |
| 2,000 | 323 ms | 293 ms | 55.8 MiB | 4,003 |
| 2,500 | 535 ms | 491 ms | 78.6 MiB | 5,003 |
| 3,000 | 717 ms | 670 ms | 105.7 MiB | 6,003 |
| 4,000 | 1.305 s | 1.245 s | 178.8 MiB | 8,003 |
| 6,000 | 2.914 s | 2.805 s | 356.2 MiB | 12,003 |
| 8,000 | 5.475 s | 5.343 s | 609.3 MiB | 16,003 |
| 9,990 | 8.589 s | 8.420 s | 906.7 MiB | 19,983 |

最大の9,990ノードでも完了しましたが、`Build`の時間はSCCサイズに対して超線形に増えました。
内部処理の中央値1秒以下を判定基準とし、測定点のうちこの基準を満たす最大の3,000ノードをSCCサイズの受入上限に採用します。
SCCサイズは取込時に検査し、受入済みスナップショットの検索は処理量によって打ち切りません。

## 性能測定

性能測定は通常検査から分離し、専用コマンドがJSONの測定結果を出力します。
SmallとPathologicalは開発環境で形状別の傾向を確認し、MediumとScaleは必要なメモリを確保した専用環境で実行します。

`cmd/perf-measure`は取込と静的解析を同じプロセスで実行し、JSONを標準出力へ出します。
出力は設定、実行環境、測定方法、入力件数とアーカイブのSHA-256、各実行の値、`min`、`median`、`p95`、`max`の要約を含みます。
`p95`はnearest-rank方式で求めます。

実行環境：Dev Containerまたは専用の測定環境

| コマンド | 測定内容 |
|---|---|
| `PERF_RUNS=3 make perf-small` | Smallの取込、二対象のcoldとwarmの静的解析 |
| `PERF_RUNS=2 make perf-medium` | Mediumの取込と静的解析。利用可能メモリは8 GiB以上が目安 |
| `PERF_RUNS=2 make perf-scale` | Scaleの取込と静的解析。必要メモリは未測定 |
| `PERF_PATHOLOGICAL_RUNS=3 make perf-pathological` | 全Pathologicalケースの取込と解析 |
| `PERF_CONCURRENT_RUNS=2 make perf-concurrent` | Smallの`NET-TARGET`を並行度1、2、4、8で解析 |
| `PERF_CONNECTION_COMPARISON_RUNS=5 make perf-connection-comparison` | Smallの`NET-TARGET`について単一接続と複数読み取り接続を並行度1、2、4、8で比較 |
| `PERF_TARGET_SEARCH_RUNS=20 make perf-target-search` | Smallを使い、公開HTTPの完全一致検索を並行度1、4、cold、warm、検索ケース別に測定 |
| `PERF_LIMIT_ANALYSIS_RUNS=20 make perf-limit-analysis` | Smallを使い、公開HTTPの後続リミット取得を並行度1、4、cold、warmで測定 |
| `PERF_GROWTH_RUNS=2 make perf-growth` | 10k、20k、40k、80kノードを別プロセスで測定し、規模別のJSONを`/tmp/batchscope-perf-growth`へ保存 |

任意規模は`custom`プロファイルへノード数とrelation数を指定します。
relation数はノード数の2.5倍から3倍までとし、Small、Medium、Scaleと同じ増加傾向を保ちます。

```bash
go run ./cmd/perf-measure \
  -profile custom \
  -nodes 40000 \
  -relations 100000 \
  -runs 2
```

取込だけを測定する場合は`-mode import`を指定します。
反復数は`-runs`で指定し、分布を作るため2以上を必要とします。

```bash
go run ./cmd/perf-measure -mode import -profile medium -runs 2
```

取込後の静的解析における`cold`は、各実行前に`PRAGMA shrink_memory`でSQLite接続のページキャッシュを解放した状態です。
同じ測定の`warm`は、直前の`cold`と同じSQLite接続を使い、接続のページキャッシュを解放せずに続けた状態です。
どちらもOSのページキャッシュを保持するため、ストレージからの完全な初回読込は測っていません。

公開HTTPの完全一致検索では、各coldラウンドの前に検索用接続プールの全接続を同時に保持し、それぞれへ`PRAGMA shrink_memory`を実行します。
warmラウンドは、直前のcoldラウンドと同じ`http.Handler`と接続プールを使い、ページキャッシュを解放せずに続けます。

検索の`total_ns`は`Traverse`の開始から`Build`の終了までです。
結果の決まった順序でのJSON化とSHA-256計算は`serialize_digest_ns`へ分けます。
HeapとLinuxのRSSは5 ms間隔と段階の境界で取得するため、サンプル間の短いピークを捉えない可能性があります。

公開HTTPの完全一致検索の`latency_ns`は、製品の`http.Handler`へ対する`ServeHTTP`呼出しの直前から、レスポンスの書き込みを終えて戻るまでです。
ルーティング、パラメーター解析、検索、DTO組立て、JSON化、構造化ログ、`httptest.ResponseRecorder`への書き込みを含みます。
要求とrecorderの準備、応答内容の検査は含みません。

並行負荷測定は、各ラウンドの開始前にSQLite接続のページキャッシュを解放し、すべてのworkerを同時に開始します。
出力は検索ごとのレイテンシ、ラウンド全体のスループット、`database/sql`の接続待ち回数と待ち時間、HeapとRSSを含みます。

公開HTTPの完全一致検索の並行負荷も、すべてのworkerが開始線へ到達してから同時に`ServeHTTP`を呼び出します。
出力はケース、coldまたはwarm、並行度ごとに全要求のレイテンシと`min`、`median`、`p95`、`max`を保持し、返却件数と`truncated`も記録します。

公開HTTPの後続リミット取得も、完全一致検索と同じ測定境界と開始線を使います。
測定区間には検索世代の取得、`Traverse`、`Scan`、`Build`、公開DTOへの写像、JSON化、構造化ログ、レスポンス書き込みを含めます。
プロファイルを変更する場合は`PERF_LIMIT_ANALYSIS_PROFILE`、並行度を変更する場合は`PERF_LIMIT_ANALYSIS_CONCURRENCIES`を指定します。

接続方式の比較測定は、一回の取込で作成したSQLiteを世代固有のファイルパスへ複製し、製品の`store`を閉じてから測定専用の接続を開きます。
単一接続と複数読み取り接続は同じSQLiteファイルを使い、最大接続数だけを1または並行度と同じ値へ変更します。
どちらも読み取り専用かつ不変ファイルとして開き、DSNにより`foreign_keys`と`query_only`をすべての接続へ適用します。
各ラウンドの前に接続プールの全接続を同時に確保して`PRAGMA shrink_memory`を実行し、方式を先に測る順序はラウンドと並行度ごとに交互にします。
OSのページキャッシュは既存の並行負荷測定と同様に保持します。

世代切替中の検索は次の検査で確認します。
旧SQLiteで`Traverse`を終えた検索を一時停止し、新SQLiteへ切り替えて新世代を検索した後、旧世代の`Scan`と`Build`を再開します。
旧検索が旧世代だけ、新検索が新世代だけを返し、ノード、リミット、経路ツリーに世代が混在しないことを検査します。

```bash
go test -run TestSearchKeepsOneSnapshotGenerationAcrossConcurrentSwitch -count=1 -v ./internal/importer
```

測定条件と採用済みの性能値は[性能測定結果](performance-measurement.md)、[完全一致検索のHTTP性能測定結果](target-search-performance.md)、[後続リミット取得のHTTP性能測定結果](limit-analysis-performance.md)を参照します。
通常テストへ性能閾値を追加せず、性能判断は再現可能な測定結果にもとづいて行います。

## テストデータの正本

取込形式の境界値は`schema/`と[取込データの形式](../design/canonical-snapshot.md)を正本とします。
JSON Schemaの制約をテスト専用定数へ複製しません。

利用者へ示す代表データは`examples/demo/snapshot/`、代表レスポンスは`examples/demo/responses/`を正本とします。
デモレスポンスは手で組み立てず、実HTTP DTOとの比較で維持します。

規模とグラフ形状の共通fixtureは`internal/testsupport/graphgen`を正本とします。
Small、Medium、Scale、Pathological、受入境界の生成規則と期待値を、利用側のテストへ複製しません。

個別failure modeに必要な最小データは、その不具合を最も低いレイヤーで再現するテストの近くへ置きます。
共通化によって入力と期待値の関係が読めなくなる場合は、無理に共通fixtureへ移しません。
