# テストと受入条件

## APIと入力形式

- 正しいスナップショットをJSON Schemaが受け入れる。
- 各制約を一つずつ破った入力を、JSON Schemaまたは追加検査が拒否する。
- HumaによるAPI実装後は、OpenAPIをGoコードから生成できる。
- 同じ入力と検索条件に対する配列の順序が変わらない。
- すべてのエラーがRFC 9457のメディアタイプと必須項目を使う。
- 同じスナップショットの再送と`snapshotId`の競合を、仕様どおりに処理する。
- 完全一致検索が最大1,000件を返し、超過時に`truncated=true`となる。
- 後続検索の公開パラメーターが`targetId`と`includeEvidence`に限定される。

## 対象の完全一致検索

- ID、名前、完全パスの各項目との完全一致を返す。
- 名前ではUnicode NFKC、前後空白の除去、Unicodeケースフォールディングを適用する。
- パスではUnicode NFKCと前後空白の除去を適用し、大文字と小文字は区別する。
- IDでは表記幅と大文字小文字を含めて入力値を変更しない。
- `query`を省略した場合だけ`invalid-request`を返し、指定された空文字と空白のみの値を検索する。
- 空文字のパス、空白のみのID、名前、パスが、それぞれの正規化規則に従って一致する。
- `type`を省略した場合は`job`と`job_network`だけを対象とし、繰り返し指定と不正値を検査する。
- 同じノードが複数項目で一致した場合も一件だけ返し、`matchedBy`を`id`、`name`、`path`の順にする。
- 一致項目の優先度、完全パス、IDの順で同じ入力に同じ結果順を返す。
- `ancestorPath`を`parent_id`だけから最上位の祖先、直近の親の順に組み立て、依存relationを含めない。
- 1,001件目がある場合は先頭1,000件と`truncated=true`を返す。
- スナップショット未投入時は`snapshot-not-loaded`を返す。
- SQLite切替と複数の読み取り接続による並行検索でも、`snapshotId`と検索結果を同じ世代から返す。
- 構造化ログへ`snapshot_id`、`duration_ms`、`returned_targets`を記録し、ジョブ名、完全パス、`query`を記録しない。

## 親子関係

次の入力を確認します。

- ルートが一件の階層
- ルートが複数件の階層
- 入れ子になった管理単位
- 深いジョブネット階層
- 許可されない親種別
- 存在しない親
- 複数の親を指定した入力
- 親子関係の循環

複数親と親子関係の循環は、取込時に拒否します。

## 依存関係

次の経路を確認します。

- ジョブ定義に記載された直列の先行後続関係
- ファイルを介する依存関係
- ファイルパターンを介する依存関係
- 別ジョブの状態を確認するジョブ
- 外部ベンダーから届くイベント
- サーバレス処理の完了イベント
- 同じノード間にある複数の依存関係
- 複数経路から合流するノード
- ファイルなどを介する循環
- 循環に入らない後続枝
- 2,000本を超える直列経路の末端まで到達すること
- 高分岐ですべての枝へ到達すること
- 合流後に同じノードの外向きrelationを一度だけ読むこと
- 大きなSCC、複数の独立したSCC、自己ループを検出すること
- 依存relationとscope遷移をまたぐ循環をSCCとして検出すること
- 親子のscope遷移だけでは循環として扱わないこと
- 対象ジョブネットの全配下と、深い入れ子のジョブネットを探索すること
- 探索途中で到達したジョブネットの全配下と、配下から外へ出る依存関係を探索すること
- scope遷移を依存関係として結果へ混入させないこと
- 探索しないノード種別への到達を意味上の端点として扱うこと
- キャンセルとDBエラーで部分成功を返さないこと
- 入力の登録順を変えても結果順が変わらないこと

## リミットの抽出と閲覧順

- `target`では、指定したジョブ自身のリミットを全件返す。
- `contained`では、指定したジョブネットの配下ジョブに設定されたリミットを全件返す。
- `downstream`では、依存関係をたどって見つかったリミットを全件返す。
- ジョブネット自体にリミットを設定した入力を拒否する。
- 同じタイムゾーンの`finish_by`を、`businessDayOffset`、`localTime`、設定先ノードID、リミットIDの順に返す。
- 異なるタイムゾーンを別のグループへ分け、タイムゾーンの順に返す。
- `max_elapsed`を`duration`、設定先ノードID、リミットIDの順に返す。
- `raw`を設定先ノードID、リミットIDの順に返す。
- 同じリミットIDを一件にまとめる。
- 同じ入力と検索条件に対し、各区分内の閲覧順が変わらない。
- リミットがない終端と循環を`uncoveredRoutes`へ含める。

## 経路ツリー

- [後続リミットの検索](../design/dependency-analysis.md#経路ツリー)で定めた代表経路の選択
- `alternatePathCount`
- 各リミットの`treeNodeId`が設定先のツリーノードを参照すること
- ルート以外のツリーノードに`viaRelations`が含まれること
- `includeEvidence=false`でも`kind`、`origin`、`certainty`が返ること
- `includeEvidence=true`の場合だけ`evidence`が返ること
- 別経路との合流を循環として扱わないこと
- 循環に入らない枝を調べ続けること
- 循環の`nodes`と一周分の表示経路が入力順に依存せず、各接続がrelationまたはscope遷移を保つこと
- `uncoveredRoutes`が終端、循環、探索対象外の種別を区別し、その`treeNodeId`が判定に使ったリミット未通過経路の境界を参照すること
- 長い直列経路を省略しても、`hiddenConnections`が親から表示ノードまでの全接続を順番どおりに保持すること
- `hiddenNodeIds`が1,000件を上限とし、超過時に`hiddenNodeIdsTruncated=true`となること
- `hiddenConnections`が`hiddenNodeIds`の表示上限とは独立し、1,000接続を超えても切り詰められないこと
- 圧縮した接続を`hiddenConnections`と`viaRelations`またはscope遷移の両方で重ねて表さないこと

## 取込の安全性

- 圧縮後500 MiBの境界値
- 展開後4 GiBの超過
- NDJSONの一行サイズ超過
- 親ディレクトリを指すパス
- シンボリックリンクとハードリンク
- アーカイブ内の同名エントリ
- アップロードの中断
- 不正なUTF-8
- 不正なJSON
- `manifest.json`のノード数またはrelation数が初期対応規模を超えた場合に、NDJSON走査前に拒否すること
- `manifest.json`が実件数を少なく宣言した場合も、ノードまたはrelationの有効行が初期対応規模を超えた時点で走査を停止すること
- リミット設定の総数が初期対応規模を超えた最初の設定で走査を停止し、以後の設定を保持しないこと
- ジョブネット階層深さが初期対応規模を超えた場合に、検査工程で拒否すること
- 検索時に展開するrelationとscope遷移を含む探索グラフのSCCサイズが初期対応規模を超えた場合に、検査工程で拒否すること
- 取込失敗時に現在のスナップショットを維持すること
- 検索中にSQLiteを切り替えられること

## 性能測定用データ

性能用データは`internal/testsupport/graphgen`が決まった規則で生成します。
同じプロファイルからは、同じノード、relation、期待値、アーカイブを生成します。

| プロファイル | 規模またはケース | 解析対象 | 実行環境と用途 |
|---|---:|---|---|
| Small | 10,000ノード、25,000 relation | `NET-TARGET`、`JOB-TARGET` | Dev ContainerとCIで取込から経路ツリーまでを検査する |
| Medium | 100,000ノード、300,000 relation | `NET-TARGET`、`JOB-TARGET` | 8 GiB以上の利用可能メモリを目安とする専用環境で測定する |
| Scale | 1,000,000ノード、3,000,000 relation | `NET-TARGET`、`JOB-TARGET` | Mediumより大きい専用環境で上限値の判断材料を測定する |
| Pathological | 個別の軽量ケース | ケースごとの一対象 | Dev ContainerとCIで規模以外の形状を検査する |

Small、Medium、Scaleは、管理単位と入れ子のジョブネット、ジョブ、分岐、合流、循環、リミット、圧縮対象の直列経路を同じ生成規則で含みます。
`file`、`file_pattern`、`job_status`、`external_event`は、ジョブが`produces`した後に別のジョブへ到達する中間ノードとして使い、中間ノードの先にあるリミット設定済みジョブとその後続までを探索する経路を持ちます。
ジョブネットとジョブを起点とする二つの解析対象により、`target`、`contained`、`downstream`を検査します。

Pathologicalは、`long-chain`、`high-fan-out`、`high-fan-in`、`large-and-multiple-scc`、`cycle-with-exit`、`deep-nested-networks`、`reached-network-with-outbound`、`many-limits`、`parallel-relations`、`long-compression`、`covered-and-uncovered-merge`、`uncovered-cycle-and-endpoint`、`large-pathtree-scc`を個別に生成します。

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

## 対応規模と並行負荷

Issue #14で測定した単一検索の内部処理（`Traverse`、`Scan`、`Build`）の中央値に基づき、Smallをノード数とrelation数の受入上限に採用します。
規模別の中央値は[性能測定結果](performance-measurement.md#中間規模)を参照してください。

複数読み取り接続における内部処理のp95に基づき、検索用SQLiteの最大接続数と想定同時検索数を4にします。
並行度別のp95は[性能測定結果](performance-measurement.md#sqlite接続方式の比較)を参照してください。
単一検索の中央値と並行度4の内部処理のp95にはHTTP層を含まないため、DTO組立てとJSON化を含む後続リミット取得の最終p95はIssue #13で確認します。
リミット設定の総数5,000件、SCCサイズ3,000ノード、ジョブネット階層深さ64階層も取込時に検査し、測定した入力から一つの要素だけが無制限に増える条件を受け入れません。
リミット設定数とSCCサイズの測定結果は[Issue #32の対応規模境界](#issue-32の対応規模境界)を参照してください。

## 性能測定

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

## CIと専用測定環境

通常CIの`make verify`は、Smallと全Pathologicalケースに加え、リミット設定数とSCCサイズの受入境界について、取込から経路ツリー作成までの完了、上限超過の拒否、拒否時の世代維持を検査します。
`make verify`は性能値を合否判定に使わず、`perf-*`ターゲットも呼び出しません。

Mediumの完全な静的解析は、8 GiB以上の利用可能メモリを目安とする専用環境で実行します。
Scaleの完全な測定に必要なメモリは未測定であり、Mediumを完走できる環境で段階的に確認します。
測定済み環境と結果は[性能測定結果](performance-measurement.md)を参照してください。

## 受入条件

- 初期対応規模の入力で、内部の`Traverse`、`Scan`、`Build`を完了できることをIssue #32の性能測定で確認する。
- 初期対応規模を超えた入力を取込時に拒否し、受入済み入力の検索を処理量によって打ち切らない。
- 公開HTTPの完全一致検索がp95 200 ms以下であることを、[完全一致検索のHTTP性能測定結果](target-search-performance.md)で確認する。
- 公開HTTPの後続リミット取得がp95 1秒以下であることを、HTTP層を実装するIssue #13で確認する。
- 循環を含む入力で、処理が終わらなくなったりプロセスが停止したりしない。
- 各リミットの閲覧順を、レスポンス内の項目から説明できる。
- 取込失敗によって、現在使用中のSQLiteが破損または置換されない。

## 公開用バイナリ

- `scripts/build-release-artifacts.sh`の構文を`make verify`で確認する。
- Linux amd64、Linux arm64、macOS amd64、macOS arm64の成果物名が重複しない。
- バイナリへ指定したバージョンとコミットが埋め込まれる。
- 各アーカイブに`README.md`と`LICENSE`が含まれる。
- `checksums.txt`がすべてのアーカイブを含む。
- `LICENSE`がない状態では公開処理を開始しない。
- GitHub ReleasesのWorkflowは、`main`へ含まれないコミットのタグを拒否する。
