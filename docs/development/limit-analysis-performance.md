# 後続リミット取得のHTTP性能測定結果

- 測定日：2026-08-11
- 測定対象コミット（基準）：`7f07c12`
- 測定対象：基準コミットへ後続リミット取得の実装とHTTP性能測定変更を適用した作業ツリー

指標の定義と測定コマンドの正本は[テストと受入条件](testing.md#性能測定)を参照してください。
この文書は、`GET /v1/downstream-limit-analysis`の測定条件と結果を記録します。

## 測定環境

| 項目 | 値 |
|---|---|
| CPU | Apple arm64、10コア、1コア当たり1スレッド |
| 総メモリ | 8,216,862,720 bytes（7.65 GiB） |
| 利用可能メモリ | 測定後は3,841,884,160 bytes（3.58 GiB） |
| Swap | 1,073,737,728 bytes中、測定後の空きは17,608,704 bytes（16.79 MiB） |
| OS | Debian GNU/Linux 12（bookworm）、Linux 6.12.76-linuxkit、arm64 |
| Go | `go1.26.5 linux/arm64` |
| `GOMAXPROCS` | 10 |
| SQLiteドライバ | `modernc.org/sqlite v1.56.0` |

利用可能メモリとSwapの空きは、ほかのプロセスとページキャッシュの状態で変化します。

## 測定コマンド

実行環境：Dev Container

```bash
GOCACHE=/tmp/batchscope-go-cache \
  PERF_LIMIT_ANALYSIS_RUNS=20 \
  make perf-limit-analysis \
  > /tmp/batchscope-perf-limit-small.json

GOCACHE=/tmp/batchscope-go-cache \
  PERF_LIMIT_ANALYSIS_PROFILE=pathological \
  PERF_LIMIT_ANALYSIS_RUNS=20 \
  make perf-limit-analysis \
  > /tmp/batchscope-perf-limit-pathological.json

GOCACHE=/tmp/batchscope-go-cache \
  PERF_LIMIT_ANALYSIS_PROFILE=medium \
  PERF_LIMIT_ANALYSIS_RUNS=2 \
  PERF_LIMIT_ANALYSIS_CONCURRENCIES=1 \
  make perf-limit-analysis \
  > /tmp/batchscope-perf-limit-medium.json
```

Smallの出力JSONの`generated_at`は`2026-08-11T05:13:09.318334013Z`、Pathologicalは`2026-08-11T05:14:29.025856383Z`です。
Mediumは取込検査で終了したため、出力JSONは生成されませんでした。

## 測定区間

製品の`internal/app`が組み立てた`http.Handler`へ、`net/http/httptest`の要求を渡しました。
`ServeHTTP`の直前から戻るまでを測り、ルーティング、パラメーター解析、検索世代の取得、`Traverse`、`Scan`、`Build`、公開DTOへの写像、JSON化、構造化ログ、レスポンス書き込みを含めました。
要求とrecorderの準備、応答内容の検査は含めていません。

要求には`targetId`だけを指定し、`includeEvidence=false`の既定値を使いました。
各データセットでは、生成時に定めた先頭の解析対象を測定しました。

`cold`は、各ラウンドの前に検索用接続プールの全接続へ`PRAGMA shrink_memory`を実行した状態です。
`warm`は、直前のcoldと同じHTTPハンドラーと接続プールを使い、接続のページキャッシュを解放せずに続けた状態です。
どちらもOSのページキャッシュを保持しています。

並行度1は各状態で20要求、並行度4は20ラウンドで80要求を測定しました。
並行度4では、すべてのworkerが開始線へ到達してから同時に`ServeHTTP`を呼び出しました。
p95は各条件の全要求にnearest-rank方式を適用しています。

## Small

Smallは2026-08-11時点の受入上限と同じ10,000ノード、25,000 relationです。
アーカイブは198,110 bytes、SHA-256は`60b332fc50901cacdf5f95eb5560bcc540f58f151871afab9ce53750be08cb71`です。
解析対象は`NET-TARGET`で、53件のリミット、24,156件の経路ツリーノード、1件の`uncoveredRoutes`、3件の循環を返しました。

時間の単位はmsです。

| 並行度 | 状態 | 要求数 | 最小 | 中央値 | p95 | 最大 |
|---:|---|---:|---:|---:|---:|---:|
| 1 | cold | 20 | 410.750 | 418.488 | 437.486 | 505.213 |
| 1 | warm | 20 | 408.518 | 420.128 | 437.587 | 457.527 |
| 4 | cold | 80 | 683.573 | 737.835 | 776.798 | 839.774 |
| 4 | warm | 80 | 666.788 | 720.741 | 753.289 | 756.289 |

## Pathological

Pathologicalは、規模以外の形状ごとに独立したデータセットを取り込みました。
表は初期目標の判定に使うwarmかつ並行度4の分布です。
各ケースは20ラウンド、80要求を測定しました。

時間の単位はmsです。

| ケース | ノード | relation | 中央値 | p95 | 最大 |
|---|---:|---:|---:|---:|---:|
| `long-chain` | 2,002 | 2,001 | 207.904 | 238.702 | 268.761 |
| `high-fan-out` | 257 | 256 | 12.010 | 13.263 | 13.793 |
| `high-fan-in` | 258 | 512 | 12.807 | 13.983 | 14.953 |
| `large-and-multiple-scc` | 137 | 139 | 11.722 | 14.760 | 16.647 |
| `cycle-with-exit` | 9 | 9 | 1.192 | 1.762 | 2.132 |
| `deep-nested-networks` | 65 | 0 | 11.130 | 13.845 | 14.338 |
| `reached-network-with-outbound` | 4 | 2 | 0.822 | 1.249 | 1.291 |
| `many-limits` | 301 | 300 | 18.791 | 20.540 | 21.628 |
| `parallel-relations` | 2 | 3 | 0.706 | 0.978 | 1.073 |
| `long-compression` | 1,103 | 1,102 | 97.535 | 113.741 | 140.114 |
| `covered-and-uncovered-merge` | 4 | 4 | 0.813 | 1.272 | 1.323 |
| `uncovered-cycle-and-endpoint` | 4 | 4 | 0.639 | 1.059 | 1.271 |
| `large-pathtree-scc` | 401 | 401 | 38.336 | 56.241 | 58.877 |

Pathologicalのwarmかつ並行度4で最大のp95は、`long-chain`の238.702 msでした。
coldを含む全Pathological条件でも最大のp95は同じケースの254.561 msでした。

## Medium

Mediumは100,000ノード、300,000 relationであり、この測定を行った2026-08-11時点の受入上限10,000ノード、25,000 relationを超えていました。
公開APIで解析できるデータは受入済みスナップショットに限られるため、測定用に取込検査を迂回しませんでした。

測定コマンドは`manifest.json/nodeCount: capacity_exceeded`で終了しました。
HTTPハンドラーを呼び出していないため、Mediumの中央値、p95、最大値はありません。
これは性能上の異常終了ではなく、当時公開していた入力条件による拒否です。
Issue #52で受入規模を400,000ノード / 300,000 relationへ更新した後のMedium再測定は、後述の[高密度Medium並行測定](#issue-52の高密度medium並行測定)に分けて記録します。

## 目標に対する判定

性能目標は、データがキャッシュへ読み込まれた状態におけるp95 1秒です。
当時の受入上限と同じSmallのwarmかつ並行度4のp95は753.289 msであり、目標を満たしました。
Pathologicalの全ケースも同じ目標を満たしました。

参考値として、Smallのcoldかつ並行度4のp95は776.798 msでした。
測定した受入可能データでは、coldを含むすべての条件がp95 1秒以下でした。

## 判定の限界

`httptest`による測定は製品のHTTPハンドラーを通りますが、TCP接続、OSのHTTPソケット処理、外部ロードバランサー、ネットワーク遅延を含みません。
coldでもOSのページキャッシュを保持するため、ストレージからの完全な初回読込を表しません。

結果は一つのDev Containerまたは各GitHub Actions runnerで測定した値です。
CPU数、利用可能メモリ、同居プロセスが異なる環境で同じp95を保証しません。

`includeEvidence=true`によるrelation evidenceの追加と、5,000件のリミットをすべて大きな文字列として返す最悪応答サイズは測定していません。

## Issue #52のoperational profile

2026-08-12に、400,000ノード / 300,000 relationのoperational profileで公開HTTPを再測定しました。
環境はGitHub ActionsのUbuntu 24.04、linux/amd64、Go 1.26.5、4 CPUです。
代表target `OPS-NET-0000`は100ノードへ到達し、99リミット、198 tree nodeを返します。
並行度4のp95はcold 12.24 ms、warm 10.51 msで、p95 1秒の代表負荷目標を満たしました。

40万ノード全体へ到達する`OPS-ROOT`は、内部`Traverse -> Scan -> Build`のp95が15.12秒でした。
公開HTTPでは700,000 tree nodeと95,949 `uncoveredRoutes`を含む応答をJSONまで返し、並行度1のp95はcold 19.66秒、warm 20.59秒、並行度4のp95はcold 43.93秒、warm 42.81秒でした。
全要求が60秒deadline内で完遂し、結果の決定性を維持しました。

並行度1/4の全件到達target測定はPR head `019eaa2c8d1f8435d13569bb171081664920d938`に対するGitHub Actions CI #178で実行しました。`pull_request`イベントで実際にcheckoutしたtest merge commitは`2cbe494f5e106ae4b7ccee7636217dad47daa6ee`です。
測定プロセスの最大RSSは9,402,744 KiB（約8.97 GiB）でした。
測定ハーネスは`httptest.ResponseRecorder`で完全な応答bodyを保持するため、本番HTTPサーバーの厳密な必要メモリ値とは扱いません。一方、8 GiBを全件到達target 4件同時実行の十分条件とも扱いません。

これは完全解析保証のstress caseであり、1秒以内に切り捨てる対象にはしません。
詳細な取込、メモリ、段階別測定と運用資源の判定は[Issue #52の再測定](performance-measurement.md#issue-52-実運用40万ノード級の再測定)を参照してください。

## Issue #52の高密度Medium並行測定

現在の受入規模内にある100,000ノード / 300,000 relationのMediumは、Operationalよりノード数が少ない一方、平方根幅の前向き辺による分岐・合流を多数含み、`pathtree`の高密度stress shapeとして使います。
Operationalは40万ノード級の入力規模と一検索の到達範囲、Mediumは高密度な分岐・合流、Pathologicalは長鎖、fan-in/out、SCC、深いscopeなどの独立した特殊形状を担当します。単一profileだけで実データの全トポロジを再現したとは扱いません。

2026-08-12に、Mediumの`NET-TARGET`を製品の`http.Handler`で並行度1と4、cold/warmを各2反復測定しました。
環境はGitHub ActionsのUbuntu 24.04、linux/amd64、Go 1.26.5、4 CPU（`GOMAXPROCS=4`）です。
入力アーカイブは2,223,686 bytes、SHA-256は`b14d436dafe9822cbec7f63c3ebd93d867fa4e4ff79a215f29e2b79f9b613ea3`です。
`NET-TARGET`は99,998ノードへ到達し、432リミット、291,506 tree node、1 `uncoveredRoutes`、3 cyclesを返しました。

```bash
go run ./cmd/perf-measure \
  -mode limit-analysis \
  -profile medium \
  -target NET-TARGET \
  -runs 2 \
  -concurrencies 1,4
```

| 並行度 | 状態 | p95 | 最大 |
|---:|---|---:|---:|
| 1 | cold | 23.18 s | 23.18 s |
| 1 | warm | 21.83 s | 21.83 s |
| 4 | cold | 40.03 s | 40.03 s |
| 4 | warm | 40.93 s | 40.93 s |

全条件で結果digestは一致し、`deterministic=true`でした。並行度4の全要求も60秒deadline内に完遂しました。
2反復のためnearest-rank p95は各条件の最大値と一致します。

この測定はPR head `b98d039440a03a7c1857d641d8818bd55513c75e`に対するGitHub Actions CI #185で実行しました。`pull_request`イベントで実際にcheckoutしたtest merge commitは`fd99f9f52dc74715387eec28d53d6c2904d863c6`です。
これにより、40万ノード全件到達のOperationalだけでなく、現在の受入規模内にある高密度Mediumでも想定同時検索数4の全要求が60秒以内に完遂することを確認しました。
