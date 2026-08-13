# スナップショットの形式

JSON Schemaは、構造と1レコード単体の基本制約を定義する。
BatchScopeは、複数レコード間の整合性と意味上の制約を追加で検査する。
JSON Schemaに適合するだけでは、取込は成功しない。
この文書は、LLMがスナップショットを作るときに必要な規則をまとめる。

## アーカイブ

```text
batchscope-snapshot.tar.gz
├── manifest.json
├── nodes.ndjson
└── relations.ndjson
```

- メディアタイプ：`application/vnd.batchscope.snapshot+gzip`
- 圧縮後サイズ上限：500 MiB
- 展開後サイズ上限：4 GiB
- `manifest.json`サイズ上限：1 MiB

三つのファイル以外は含めない。
ディレクトリ、リンク、親ディレクトリを指すパスも含めない。

## マニフェスト

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

`nodeCount`と`relationCount`は、NDJSONの件数と一致させる。
同じ`snapshotId`を再送できるのは、展開後の`manifest.json`、`nodes.ndjson`、`relations.ndjson`のbyte内容がすべて同じ場合だけである。
JSONとして意味が同じでも、空白、改行、objectのキー順などでbyte内容が変わる場合は同じ`snapshotId`を再利用しない。

## namespaceとID

namespaceは、同じジョブID体系を共有する意味上の定義セットである。本番/DR、独立環境、管理上別の定義領域などを表す。
ファイルの分割単位だけをnamespaceにしない。資料からnamespaceを判断できない場合は推測しない。

新しくnamespace対応データを作る場合、各ノードに次を持たせる。

- `namespace`: 意味上の定義セット
- `localId`: 元システム内のID
- `id`: BatchScope内部で参照するcanonical ID

`namespace`と`localId`は必ず両方指定する。
canonical IDは次の式で生成する。

```text
id = "bs1." + base64url-no-padding(UTF-8(namespace))
             + "." + base64url-no-padding(UTF-8(localId))
```

base64urlはRFC 4648のURL-safe alphabetを使い、末尾の`=`paddingを付けない。
同じ`namespace + localId`から常に同じ`id`を生成し、ファイル名、行順、表示名、親階層をIDへ含めない。

同じnamespace内の同じ`localId`は同じcanonical IDになるため、二重に生成しない。
異なるnamespaceでは同じ`localId`を使用できる。

schema version 0.5の従来データは`namespace`と`localId`を省略できる。その場合、API表示上はnamespace=`default`、localId=`id`として扱われる。`default`はこの互換表示専用の予約namespaceであり、新しいnamespace対応ノードへ明示指定しない。複数namespaceを新しく生成するときは従来形式へ依存しない。

## ノード種別

| 種別 | 意味 | 許可する親 |
|---|---|---|
| `management_unit` | 最上位または入れ子の管理単位 | `management_unit` |
| `job_network` | ジョブまたは下位ジョブネットを含む単位 | `management_unit`、`job_network` |
| `job` | ジョブマネージャー上のジョブ | `job_network` |
| `file` | 具体的なファイル、オブジェクト、状態ファイル | なし |
| `file_pattern` | パターンで表すファイル集合 | なし |
| `job_status` | ジョブの完了状態または結果状態 | なし |
| `external_event` | 外部システムから届くイベント | なし |

`parentId`はcanonical IDを使う。namespace対応ノードの親は同じnamespace内に限る。
そのほかのノードIDも、一つのスナップショット内で重複させない。

## リミット

リミットには、元の定義から読み取った設定を保存する。
検索時の重要度や順位は付けない。

| 種類 | 用途 |
|---|---|
| `finish_by` | 業務日を基準にした完了時刻 |
| `max_elapsed` | 完了までの最大経過時間 |
| `raw` | 項目へ安全に分解できない元の設定 |

`limitFacts`は`job`にだけ設定する。
リミットIDは、一つのスナップショット内で重複させない。
BatchScopeは、ジョブネットの配下にあるリミット設定済みジョブを検索時に全件抽出する。
元の表記を取得できる場合は`sourceText`へ保存する。
項目へ安全に分解できない値は`raw`として保存する。

`max_elapsed`の`duration`には年と月を含めない。
週を使う場合は週だけで表し、日や時刻成分と組み合わせない。
日、時、分、秒は組み合わせて指定できる。
小数部は指定した構成要素のうち最も小さい単位にだけ指定し、点とコンマのどちらも小数記号として使える。
全体を秒へ換算した結果が整数になる値を使う。

## 依存関係の向き

`fromId`側の処理や状態が成立した後に、`toId`側の処理や状態が成立できる向きで記録する。
`fromId`と`toId`はcanonical IDを使う。

| 種類 | 代表的な使い方 |
|---|---|
| `precedes` | ジョブまたはジョブネットの直接的な先行後続関係 |
| `produces` | ジョブまたはジョブネットがファイル、状態、イベントを作る |
| `triggers` | ファイル、状態、イベントがジョブまたはジョブネットの起動条件になる |
| `consumed_by` | ファイルなどをジョブまたはジョブネットが入力として使う |
| `observed_by` | 状態またはイベントをジョブまたはジョブネットが監視する |

### namespaceを跨ぐ依存

namespaceはidentityと親子階層の境界であり、依存解析の境界ではない。relationは明示的に別namespaceのcanonical IDを参照できる。

例えばmainのジョブ終了をDRのジョブが状態チェックして起動するなら、資料に合わせて次のように表す。

```text
main / JOB-A --produces--> main / JOB-A.done
main / JOB-A.done --triggers--> dr / JOB-B
```

中間ノードには`file`、`file_pattern`、`job_status`、`external_event`など根拠に合う種別を使う。
直接の終了チェックが確認できる場合は、別namespaceのjobをendpointに持つrelationを使ってよい。

namespaceが違うという事実だけからrelationを生成しない。入力資料から検出した依存だけを出力し、`origin`と`certainty`を付ける。取得できる場合は`evidence`も残す。

## 生成元と確実性

`origin`には次のいずれかを指定する。

- `scheduler`
- `deterministic_analysis`
- `ai_analysis`
- `manual`

`certainty`には次のいずれかを指定する。

- `declared`：元の設定に直接記載されている。
- `confirmed`：入力資料から直接確認できる。
- `inferred`：根拠はあるが、入力資料に直接は記載されていない。
- `candidate`：人による確認が必要な候補である。

`evidence`は任意である。
`evidence`がないことだけを理由に、依存関係を削除しない。

## 生成前の確認

- ノードIDが重複していない。
- namespace対応ノードでは`id`が`namespace + localId`から決定規則どおり生成されている。
- `parentId`が同じnamespaceの存在するノードを参照している。
- `fromId`、`toId`が存在するcanonical node IDを参照している。
- 親ノードの種別が許可されている。
- 親子関係に循環がない。
- 内容が同じ依存関係を重複して出力していない。
- namespaceを跨ぐrelationには、入力資料に基づく生成元と確実性が付いている。
- マニフェストの件数が実データと一致している。
