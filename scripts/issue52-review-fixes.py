from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    target = Path(path)
    text = target.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one match, got {count}: {old[:100]!r}")
    target.write_text(text.replace(old, new, 1))


replace_once(
    "docs/operations.md",
    "初期運用では4 vCPU、BatchScopeプロセスが利用可能な4 GiB以上のメモリ、`data-dir`に6 GiB以上の空きを目安とします。",
    "初期運用では4 vCPU、8 GiB以上のメモリ、`data-dir`に6 GiB以上の空きを推奨します。",
)
replace_once(
    "docs/operations.md",
    "代表targetの公開HTTP測定では、並行度4のp95がcold 12.24 ms、warm 10.51 msでした。\n高密度な100,000ノード / 300,000 relation形状では、メモリ削減後の内部解析p95が約25.9 s、RSS増分が約588 MiBでした。",
    "代表targetの公開HTTP測定では、並行度4のp95がcold 12.24 ms、warm 10.51 msでした。\n全40万ノードへ到達するtargetもDTO組立てとJSON書込みまで完走し、並行度1のp95はcold 16.04 s、warm 15.23 sでした。\n高密度な100,000ノード / 300,000 relation形状では、メモリ削減後の内部解析p95が約25.9 s、RSS増分が約588 MiBでした。",
)
replace_once(
    "docs/operations.md",
    "これは取込アーカイブ500 MiB、展開後4 GiBの安全上限、世代別SQLiteと測定した検索メモリへ余裕を持たせるための運用目安であり、ホスト全体の厳密な必要量を固定する契約ではありません。",
    "これは取込アーカイブ500 MiB、展開後4 GiBの安全上限、世代別SQLite、全件HTTPのDTO/JSON化と測定した検索メモリへ余裕を持たせるための運用推奨であり、ホスト全体の厳密な必要量を固定する契約ではありません。",
)

old_history = """現在のコードベースでは、採用済みの5,000リミットと3,000ノードSCCで全パイプラインが完遂し、SCC上限超過を取込時に拒否することを次の回帰検査で再現できます。

```bash
GOCACHE=/tmp/batchscope-go-cache \\
  go test \\
  -run 'TestCapacityBoundaryPipelineCompletes|TestSCCCapacityBoundaryPipeline' \\
  -count=1 -v ./internal/importer
```

このコマンドは採用済み受入境界の正しさを検査するものであり、下表の時間、`alloc`、`heapInUse`を再測定するコマンドではありません。
`MaxSnapshotLimits`または`MaxSCCNodes`を性能根拠で変更する場合は、候補値を測定できる専用の再現可能な性能測定手順を先に用意し、測定revision、generator引数、実行コマンド、指標を新しい判断根拠と同じ変更で記録します。
現在の製品validationは採用済み受入上限を超える入力を拒否するため、通常の取込経路をそのまま候補上限の再測定手順として使用しません。"""
new_history = """現在の通常suiteでは、Smallの全パイプラインと3,000ノードSCCの完遂、SCC上限超過の取込拒否を軽量な回帰境界として確認します。
5,000リミットと40万ノード級の全パイプラインは、`go test -race ./...`へ重複して持ち込まず、Issue #52で追加した`operational` profileの専用測定で確認します。

```bash
GOCACHE=/tmp/batchscope-go-cache \\
  go test \\
  -run 'TestScalePipelineSmall|TestSCCCapacityBoundaryPipeline' \\
  -count=1 -v ./internal/importer

go run ./cmd/perf-measure -profile operational -runs 2
```

これらは採用済み境界の完遂可否を確認する入口であり、下表に残す過去の時間、`alloc`、`heapInUse`を再生成するコマンドではありません。
`MaxSnapshotLimits`または`MaxSCCNodes`を性能根拠で変更する場合は、候補値を測定できる専用の再現可能な性能測定手順を先に用意し、測定revision、generator引数、実行コマンド、指標を新しい判断根拠と同じ変更で記録します。"""
replace_once("docs/development/performance-measurement.md", old_history, new_history)
replace_once(
    "docs/development/performance-measurement.md",
    "この測定結果に基づき、初期対応規模を10,000ノード、25,000 relation、5,000リミット設定、SCCサイズ3,000ノード、ジョブネット階層深さ64、想定同時検索数4と確定しました。",
    "Issue #32では、この当時の測定結果に基づき、初期対応規模を10,000ノード、25,000 relation、5,000リミット設定、SCCサイズ3,000ノード、ジョブネット階層深さ64、想定同時検索数4と確定しました。現在の対応規模は下記Issue #52の再測定で更新しています。",
)
replace_once(
    "docs/development/performance-measurement.md",
    """同じ入力・条件の応答digestは一致し、決定性を維持しました。

### `pathtree`メモリ削減の比較""",
    """同じ入力・条件の応答digestは一致し、決定性を維持しました。

### 公開HTTPの全件到達target

`OPS-ROOT`も同じ製品`http.Handler`で並行度1、cold/warmを2反復測定しました。
400,000到達node、5,000リミット、700,000 tree node、95,949 `uncoveredRoutes`をDTO化し、JSON書込みまで完了しています。

| 状態 | p95 | 最大 |
|---|---:|---:|
| cold | 16.04 s | 16.04 s |
| warm | 15.23 s | 15.23 s |

応答digest `934c2f9aac01ff12c797b4a33cc20ff21977e190412324e54a785a7a5e7dfd64`は全要求で一致しました。
60秒deadlineに対して十分な余裕を持ち、件数によるpartial successはありません。

### `pathtree`メモリ削減の比較""",
)

replace_once(
    "docs/development/limit-analysis-performance.md",
    "40万ノード全体へ到達する`OPS-ROOT`は、内部`Traverse -> Scan -> Build`のp95が15.12秒でした。\nこれは完全解析保証のstress caseであり、1秒以内に切り捨てる対象にはしません。",
    "40万ノード全体へ到達する`OPS-ROOT`は、内部`Traverse -> Scan -> Build`のp95が15.12秒でした。\n公開HTTPでも700,000 tree nodeを含む応答をJSONまで返し、並行度1のp95はcold 16.04秒、warm 15.23秒でした。\nこれは完全解析保証のstress caseであり、1秒以内に切り捨てる対象にはしません。",
)
