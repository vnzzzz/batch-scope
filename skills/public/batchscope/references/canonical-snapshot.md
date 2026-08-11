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
同じ内容を再送する場合は、同じ`snapshotId`を使う。

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

ジョブIDは入力値を変更せず、全環境を通じて重複しない値として使う。
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

| 種類 | 代表的な使い方 |
|---|---|
| `precedes` | ジョブまたはジョブネットの直接的な先行後続関係 |
| `produces` | ジョブがファイル、状態、イベントを作る |
| `triggers` | ファイル、状態、イベントがジョブの起動条件になる |
| `consumed_by` | ファイルなどをジョブが入力として使う |
| `observed_by` | 状態またはイベントをジョブが監視する |

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
- `parentId`、`fromId`、`toId`が存在するノードを参照している。
- 親ノードの種別が許可されている。
- 親子関係に循環がない。
- 内容が同じ依存関係を重複して出力していない。
- マニフェストの件数が実データと一致している。
