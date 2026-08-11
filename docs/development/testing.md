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

[SQLiteの世代切替](../design/storage.md#sqliteの世代切替)で定めた、検索中の世代整合性と切替失敗時の現在世代維持を検査します。

### スナップショット取込の資源管理

取込予約はrequest bodyを読む前に確保し、競合する取込のbodyを消費せずに拒否します。
受信失敗と受信deadlineでは、socket readを中断し、取込予約と一時ファイルを解放します。
実際のsocket readの中断は、`httptest`の直接呼出しではなく実TCPで検査します。

### 完全一致検索

ID、名前、パスの完全一致規則と検索結果の順序は、[API仕様](../design/api.md#対象の検索)を正本とします。
空文字、空白のみの値、最大返却件数、`truncated`、同一ノードの重複排除を境界値として検査します。

### 入出力形式

取込データは`schema/`のJSON Schemaと[取込データの形式](../design/canonical-snapshot.md)に定めた単体制約とレコード間制約を検査します。
HTTPテストはstatus code、Problem Details、header、公開DTOの必須項目と境界値を検査します。

生成したOpenAPIは実装と一致させます。
デモレスポンスは、デモスナップショットを実際に取り込んだHTTPレスポンス全体と一致させます。

### 情報の非公開

既定ログには、検索query、ジョブ名、完全パス、evidence、入力内容を出しません。
正常系と入力エラーの両方で、構造化ログに必要な識別子と件数だけが残ることを検査します。

### 公開成果物

Release archiveは[ビルドと公開](build-and-release.md#対応環境とアーカイブ構成)を正本とし、専用の成果物検査で実際に生成したアーカイブがその契約へ一致することを確認します。
Public Skillと配布JSON Schemaのsource一致、Internal Skill非同梱、READMEの版固定リンク、ターゲット間の構成一致、チェックサムを検査します。

## テストレイヤー

| レイヤー | 主なfailure mode |
|---|---|
| pure functionまたはunit test | 正規化、変換規則、同じ入力に対する並び順が変わる |
| DB integration test | SQLiteの制約、transaction、世代の参照寿命、並行切替でデータが混在する |
| HTTP integration test | status code、Problem Details、DTO、header、cancelの写像が公開仕様と異なる |
| 実TCP test | upload deadlineがsocket readを中断せず、取込資源を保持し続ける |
| demo response test | 取込から公開DTOまでの代表フローで、個別レイヤーの組合せが崩れる |
| local smoke test | 実プロセスの起動、公開ポート、公開フローの接続が成立しない |
| Release artifact check | 公開用archiveの構成、配布コピー、リンク、checksumがRelease契約と異なる |

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

local smoke testとRelease artifact checkは、実プロセスまたは生成済み成果物を検査するため、通常の`make verify`から分離します。
それぞれの実行方法は[開発環境](development.md)と[ビルドと公開](build-and-release.md#公開成果物の確認)を参照してください。

## 性能測定用データ

性能用データは`internal/testsupport/graphgen`が決まった規則で生成します。
同じプロファイルからは、同じノード、relation、期待値、アーカイブを生成します。

| プロファイル | 目的 | 現在の扱い |
|---|---|---|
| Small | 初期対応規模の取込と解析を確認する | Dev ContainerとCI、および現行の性能測定で使用する |
| Pathological | 規模以外のグラフ形状を確認する | Dev ContainerとCI、および現行の性能測定で使用する |
| Medium | 初期対応規模を超えた増加傾向を調べる | 過去の性能測定で使用した生成器として保持する |
| Scale | Mediumより大きい規模の判断材料を作る | 過去の測定設計で使用した生成器として保持する |

各プロファイルの件数、生成規則、期待値は`internal/testsupport/graphgen`を正本とします。
初期対応規模は[運用](../operations.md#初期対応規模)、測定済みの条件と結果は[性能測定結果](performance-measurement.md)を参照してください。

## 性能測定

性能測定は通常検査から分離し、専用コマンドがJSONの測定結果を出力します。
現行の標準コマンドは、製品が受け入れる規模または軽量な病理グラフを対象にします。

`cmd/perf-measure`は取込と静的解析を同じプロセスで実行し、JSONを標準出力へ出します。
出力は設定、実行環境、測定方法、入力件数とアーカイブのSHA-256、各実行の値、`min`、`median`、`p95`、`max`の要約を含みます。
`p95`はnearest-rank方式で求めます。

実行環境：Dev Container

| コマンド | 測定内容 |
|---|---|
| `PERF_RUNS=3 make perf-small` | Smallの取込、二対象のcoldとwarmの静的解析 |
| `PERF_PATHOLOGICAL_RUNS=3 make perf-pathological` | 受入範囲内のPathologicalケースの取込と解析 |
| `PERF_CONCURRENT_RUNS=2 make perf-concurrent` | Smallの`NET-TARGET`を並行度1、2、4、8で解析 |
| `PERF_CONNECTION_COMPARISON_RUNS=5 make perf-connection-comparison` | Smallの`NET-TARGET`について単一接続と複数読み取り接続を並行度1、2、4、8で比較 |
| `PERF_TARGET_SEARCH_RUNS=20 make perf-target-search` | Smallを使い、公開HTTPの完全一致検索を並行度1、4、cold、warm、検索ケース別に測定 |
| `PERF_LIMIT_ANALYSIS_RUNS=20 make perf-limit-analysis` | Smallを使い、公開HTTPの後続リミット取得を並行度1、4、cold、warmで測定 |

Medium、Scale、10,000ノードを超える中間規模の結果は、現在の受入上限を確定する前に行った測定履歴です。
現行の製品validationは受入上限を超える入力を拒否するため、それらを現行の取込経路で測定するコマンドは標準入口として提供しません。
過去の測定条件と結果は[性能測定結果](performance-measurement.md)に記録します。
対応規模を見直す場合は、候補上限を測定できる再現可能な専用手順を先に用意し、製品validationを一時的に弱めて測定しません。

取込後の静的解析における`cold`は、各実行前に`PRAGMA shrink_memory`でSQLite接続のページキャッシュを解放した状態です。
同じ測定の`warm`は、直前の`cold`と同じSQLite接続を使い、接続のページキャッシュを解放せずに続けた状態です。
どちらもOSのページキャッシュを保持するため、ストレージからの完全な初回読込は測っていません。

公開HTTPの完全一致検索では、各coldラウンドの前に検索用接続プールの全接続を同時に保持し、それぞれへ`PRAGMA shrink_memory`を実行します。
warmラウンドは、直前のcoldラウンドと同じ`http.Handler`と接続プールを使い、ページキャッシュを解放せずに続けます。

検索の`total_ns`は後続探索の開始から経路ツリー生成の終了までです。
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
測定区間には検索世代の取得、後続探索、リミット抽出、経路ツリー生成、公開DTOへの写像、JSON化、構造化ログ、レスポンス書き込みを含めます。
プロファイルを変更する場合は`PERF_LIMIT_ANALYSIS_PROFILE`、並行度を変更する場合は`PERF_LIMIT_ANALYSIS_CONCURRENCIES`を指定します。

接続方式の比較測定は、一回の取込で作成したSQLiteを世代固有のファイルパスへ複製し、製品の`store`を閉じてから測定専用の接続を開きます。
単一接続と複数読み取り接続は同じSQLiteファイルを使い、最大接続数だけを1または並行度と同じ値へ変更します。
どちらも読み取り専用かつ不変ファイルとして開き、DSNにより`foreign_keys`と`query_only`をすべての接続へ適用します。
各ラウンドの前に接続プールの全接続を同時に確保して`PRAGMA shrink_memory`を実行し、方式を先に測る順序はラウンドと並行度ごとに交互にします。
OSのページキャッシュは既存の並行負荷測定と同様に保持します。

世代切替の検査では、切替前に開始した検索と切替後に開始した検索を並行させ、各検索のSQLite、メタデータ、解析結果が一つの世代だけで構成されることを確認します。

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
