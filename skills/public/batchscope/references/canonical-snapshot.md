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

新規生成ではnamespace対応のschema `0.6`を使う。

```json
{
  "schemaVersion": "0.6",
  "snapshotId": "2026-08-13T01:00:00Z",
  "generatedAt": "2026-08-13T01:00:00Z",
  "nodeCount": 8,
  "relationCount": 7,
  "producer": {
    "name": "batchscope-normalizer",
    "version": "0.6.0"
  }
}
```

`nodeCount`と`relationCount`は、NDJSONの件数と一致させる。
同じ`snapshotId`を再送できるのは、展開後の`manifest.json`、`nodes.ndjson`、`relations.ndjson`のbyte内容がすべて同じ場合だけである。
JSONとして意味が同じでも、空白、改行、objectのキー順などでbyte内容が変わる場合は同じ`snapshotId`を再利用しない。

schema `0.5`は既存の単一定義セットとの互換入力だけに使う。
`0.5`ではnamespaceは暗黙`default`、`localId=id`であり、`namespace`/`localId`を追加しない。

## namespaceとcanonical ID

namespaceは物理ファイルではなく、意味上の定義セットを識別する。
Main、DR、開発など、同じlocal IDが別定義として成立する境界へ安定したnamespaceを割り当てる。
同じ定義セットが複数ファイルに分かれていてもnamespaceを分割しない。
資料からnamespaceを確定できない場合は、名前やlocal IDの一致だけで既存namespaceへ統合しない。

schema `0.6`のすべてのnodeへ次を設定する。

- `namespace`: 定義セットID
- `localId`: 元定義内のnode ID
- `id`: snapshot内canonical ID

canonical IDは次の式で作る。

```text
bsid1:<namespaceのUTF-8 byte長>:<namespace>:<localId>
```

例:

```text
main + JOB-A -> bsid1:4:main:JOB-A
dr   + JOB-A -> bsid1:2:dr:JOB-A
```

node type、入力ファイル名、入力順をcanonical IDへ含めない。
同じnamespace内で同じlocal IDが別定義として現れた場合は競合とし、suffixを付けて別identityへしない。

`parentId`は同じnamespaceのcanonical IDだけを参照する。
relationの`fromId`/`toId`はcanonical IDを参照し、namespaceを跨げる。

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

| 種類 | 代表的な使い方 |
|---|---|
| `precedes` | ジョブまたはジョブネットの直接的な先行後続関係 |
| `produces` | ジョブまたはジョブネットがファイル、状態、イベントを作る |
| `triggers` | ファイル、状態、イベントがジョブまたはジョブネットの起動条件になる |
| `consumed_by` | ファイルなどをジョブまたはジョブネットが入力として使う |
| `observed_by` | 状態またはイベントをジョブまたはジョブネットが監視する |

namespaceはidentity境界であって依存解析境界ではない。
別namespace間の依存が資料から確認できる場合は明示的にrelationを作る。
例えばMainジョブが作った共有ファイルをDRジョブが監視する場合は、次のようにresource nodeを介して表現できる。

```text
[main] JOB-A --produces--> [shared] ready.flag --observed_by--> [dr] JOB-A
```

他ジョブの終了確認には`job_status`、共有ファイルには`file`/`file_pattern`、外部通知には`external_event`を使える。
namespaceが違うこと、local IDが同じことだけを根拠にrelationを生成してはならない。

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

- schema versionは新規生成なら`0.6`である。
- 全nodeに`namespace`と`localId`がある。
- canonical `id`がnamespace/localIdから同じ規則で再生成できる。
- 同一namespace内でlocalIdが競合していない。
- `parentId`が同じnamespaceの存在するnodeを参照している。
- `fromId`、`toId`が存在するcanonical node IDを参照している。
- cross-namespace relationにはnamespace一致以外の根拠がある。
- 親ノードの種別が許可されている。
- 親子関係に循環がない。
- 内容が同じ依存関係を重複して出力していない。
- マニフェストの件数が実データと一致している。
