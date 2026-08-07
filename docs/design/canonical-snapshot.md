# 取込データの形式

ジョブマネージャー固有の定義は、BatchScopeが受け入れる共通形式へ変換します。
この全量データを、この文書では**スナップショット**と呼びます。
各ファイルの詳しい制約は[`../../schema`](../../schema/)配下のJSON Schemaで定義します。
JSON Schema間の参照には相対パスを使い、公開URLを表す`$id`は設定しません。

## 形式の考え方

検索に必要なID、種別、親子関係、依存関係、リミットは共通項目として定義します。
製品固有の補足情報は`locator`と`attributes`へ保存できます。

入力に不明な点がある場合は、値を推測して確定させず、`inferred`または`candidate`として保存します。
リミットを項目へ分解できない場合は、元の表記を`raw`として残します。

## アーカイブの内容

```text
batchscope-snapshot.tar.gz
├── manifest.json
├── nodes.ndjson
└── relations.ndjson
```

| 項目 | 値 |
|---|---|
| メディアタイプ | `application/vnd.batchscope.snapshot+gzip` |
| 圧縮後サイズ上限 | 500 MiB |
| 展開後サイズ上限 | 4 GiB |

アーカイブの直下には、この三つのファイルだけを配置します。
ディレクトリ、シンボリックリンク、ハードリンク、親ディレクトリを指すパス、同名の重複エントリは拒否します。

## マニフェスト

`manifest.json`には、スナップショットを識別する値と件数を記録します。

```json
{
  "schemaVersion": "0.5",
  "snapshotId": "2026-08-05T01:00:00Z",
  "generatedAt": "2026-08-05T01:00:00Z",
  "nodeCount": 8,
  "relationCount": 7,
  "producer": {
    "name": "batchscope-normalizer",
    "version": "0.5.0"
  }
}
```

## ノード

ノードは、管理単位の親子関係を構成する要素か、実行上の依存関係に現れる要素です。

| 種別 | 意味 | 許可する親 |
|---|---|---|
| `management_unit` | 最上位または入れ子の管理単位 | `management_unit` |
| `job_network` | ジョブまたは下位ジョブネットを含む単位 | `management_unit`、`job_network` |
| `job` | ジョブマネージャー上のジョブ | `job_network` |
| `file` | 具体的なファイル、オブジェクト、状態ファイル | なし |
| `file_pattern` | ワイルドカードまたはテンプレートで表すファイル集合 | なし |
| `job_status` | ジョブの完了状態または結果状態 | なし |
| `external_event` | 外部ベンダー、クラウド、サーバレスから届くイベント | なし |

ジョブIDは、全環境を通じて重複しない値を使います。
そのほかのノードIDも、一つのスナップショット内で重複してはいけません。

```json
{
  "type": "job",
  "id": "JOB-A",
  "name": "売上集計",
  "path": "/DAILY/SALES/JOB-A",
  "parentId": "NET-SALES",
  "limitFacts": []
}
```

## リミット

**リミット**は、ジョブ定義に記載された完了条件です。
ここには元の設定を記録し、検索時の優先順位は付けません。

| 種類 | 意味 | 比較方法 |
|---|---|---|
| `finish_by` | 業務日を基準にした完了時刻 | 同じタイムゾーン内で日オフセットと時刻を比較する |
| `max_elapsed` | 指定時間以内の完了 | ISO 8601 Durationを秒へ変換して比較する |
| `raw` | 安全に項目へ分解できなかった設定 | 数値として比較しない |

```json
{
  "id": "LIMIT-JOB-C-FINISH",
  "kind": "finish_by",
  "businessDayOffset": 1,
  "localTime": "06:00:00",
  "timeZone": "Asia/Tokyo",
  "sourceText": "翌日06:00までに終了",
  "origin": "scheduler",
  "certainty": "declared"
}
```

`limitFacts`を設定できるノードは`job`だけです。
ジョブネットを指定して検索する場合は、配下ジョブのリミットから代表値を検索時に求めます。
元の表記を取得できる場合は、`sourceText`へ保存します。
項目へ安全に分解できない設定は`raw`として保存します。
どのリミットを返すかは、検索時にBatchScopeが決めます。

## 依存関係の向き

依存関係は、`fromId`側の処理や状態が成立した後に、`toId`側の処理や状態が成立できる向きで記録します。

| 種類 | 代表的な使い方 |
|---|---|
| `precedes` | ジョブまたはジョブネットの直接的な先行後続関係 |
| `produces` | ジョブまたはジョブネットがファイル、イベント、状態を作る |
| `triggers` | ファイル、イベント、状態がジョブまたはジョブネットの起動条件になる |
| `consumed_by` | ファイルなどをジョブまたはジョブネットが入力として使う |
| `observed_by` | ファイル、状態、イベントをジョブまたはジョブネットが監視する |

```json
{
  "fromId": "JOB-A",
  "toId": "file:nfs-prod:sales.done",
  "kind": "produces",
  "origin": "ai_analysis",
  "certainty": "confirmed",
  "evidence": [
    {
      "source": "scripts/job_a.sh",
      "location": {"startLine": 84, "endLine": 84},
      "note": "トリガファイルを作成"
    }
  ]
}
```

## 生成元

`origin`には、依存関係やリミットをどの方法で取得したかを記録します。

| 値 | 意味 |
|---|---|
| `scheduler` | ジョブマネージャーの定義から取得した |
| `deterministic_analysis` | あらかじめ定めた解析規則で検出した |
| `ai_analysis` | LLMによる解析で検出した |
| `manual` | 人が追加した |

## 確実性

`certainty`には、入力資料からどこまで確認できたかを記録します。

| 値 | 意味 |
|---|---|
| `declared` | 元の設定に直接記載されている |
| `confirmed` | 入力資料から直接確認できる |
| `inferred` | 根拠はあるが、入力資料に直接は記載されていない |
| `candidate` | 人による確認が必要な候補である |

`evidence`は任意です。
`evidence`がないことだけを理由に、スナップショットを拒否しません。

## 取込を拒否する入力

| 分類 | 拒否する条件 |
|---|---|
| 形式 | JSON Schemaに違反している、マニフェストの件数が実データと一致しない |
| IDと参照 | ノードIDが重複している、存在しないノードまたは親を参照している |
| 親子関係 | 親の種別が許可されていない、複数の親を設定している、循環がある |
| 依存関係 | 内容がすべて同じ依存関係が重複している |
| アーカイブ | 不正なエントリがある、サイズ上限を超えている |

実行上の依存関係に含まれる循環、孤立ノード、同名ノードは受け入れます。
`evidence`を持たない`inferred`の依存関係も受け入れますが、検索結果でも`inferred`のまま返します。
