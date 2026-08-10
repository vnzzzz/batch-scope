# SQLiteの構成

SQLiteは、取り込んだスナップショットを検索しやすい形で保存するために使います。
外部へ公開するAPIや入力形式は、SQLiteのテーブル構成に依存させません。

## テーブル

```mermaid
erDiagram
    NODE ||--o{ NODE : parent
    NODE ||--o{ LIMIT_FACT : owns
    NODE ||--o{ RELATION : from
    NODE ||--o{ RELATION : to

    NODE {
      text node_id PK
      text node_type
      text name
      text name_normalized
      text path
      text path_normalized
      text parent_id FK
      text locator_json
      text attributes_json
    }

    LIMIT_FACT {
      text limit_id PK
      text node_id FK
      text kind
      int business_day_offset
      int local_time_seconds
      text time_zone
      int finish_sort_seconds
      int duration_seconds
      text source_text
      text origin
      text certainty
    }

    RELATION {
      text relation_id PK
      text from_id FK
      text to_id FK
      text relation_kind
      text origin
      text certainty
      text evidence_json
    }
```

## リミットの保存方法

リミットは、検索のたびに種類ごとの規則で並べ替えます。
ノードのJSONへ埋め込むと、検索のたびにJSONを解析する必要があり、SQLiteのインデックスも使いにくくなります。
このため、リミットは`limit_fact`テーブルへ分けて保存します。

`finish_by`は、同じタイムゾーン内で並べ替えられる秒数へ変換します。

```text
finish_sort_seconds = businessDayOffset × 86400 + localTimeSeconds
```

`finish_by`には、`kind`、`time_zone`、`finish_sort_seconds`の複合インデックスを作ります。
実行日が分からない状態では、異なるタイムゾーンの値を同じ時刻として比較しません。

`max_elapsed`は、固定秒数の`duration_seconds`へ変換します。
年や月を含む期間は長さが一定でないため、MVPでは`max_elapsed`として受け入れません。
小数部は、指定した期間の構成要素のうち最も小さい単位にだけ許可します。
各構成要素を秒へ換算した合計がちょうど整数になる値だけを受け入れます。

`raw`は元の表記を保存します。
数値へ変換しないため、設定値による並べ替えは行いません。

## インデックス

```sql
CREATE UNIQUE INDEX idx_node_id ON node(node_id);
CREATE INDEX idx_node_parent ON node(parent_id);
CREATE INDEX idx_node_name_exact ON node(node_type, name_normalized);
CREATE INDEX idx_node_path_exact ON node(node_type, path_normalized);
CREATE INDEX idx_relation_from ON relation(from_id);
CREATE INDEX idx_relation_to ON relation(to_id);
CREATE INDEX idx_limit_node ON limit_fact(node_id);
CREATE INDEX idx_limit_finish ON limit_fact(kind, time_zone, finish_sort_seconds);
CREATE INDEX idx_limit_elapsed ON limit_fact(kind, duration_seconds);
```

## 完全一致検索の前処理

検索対象ごとに、比較前の処理を次のように定めます。

- ID：入力された文字列を変更せずに比較する。
- 名前：Unicode NFKC、前後空白の除去、Unicodeケースフォールディングを適用する。
- パス：Unicode NFKCと前後空白の除去を適用し、大文字と小文字は区別する。

MVPでは、前方一致、部分一致、曖昧検索、ベクトル検索を行いません。

## 依存関係の重複判定

`relation_id`は、依存関係の内容を構成する次の項目から作ります。
同じ項目からは常に同じIDを作る必要があります。

```text
fromId + toId + kind + origin + certainty + canonicalized evidence
```

このIDにより、同じ二つのノード間でも、種類や判定根拠が異なる依存関係を別々に保存できます。
すべての項目が同じ依存関係は重複として拒否します。

`relation_id`は、項目を正規化したJSONからSHA-256で生成し、hex文字列として保存します。
JSON objectのキーは辞書順に並べ、配列の順序は入力どおりに保持します。
`evidence`の欠落と空配列は同じ値として扱います。
数値は整数として正規化するため、例えば`1`と`1.0`からは同じ値を生成します。

## 後続探索

後続探索には、索引付きSQLiteのバッチ探索を使います。
後続探索は、未展開の到達ノードをまとめ、`idx_relation_from`を使って外向きrelationをバッチ取得します。
到達ノードは一度だけ展開するため、各ノードの外向きrelationを高々一度だけ読みます。

到達ノードが`job_network`の場合は、`idx_node_parent`を使って直接の論理子ノードをバッチ取得します。
取得した子はscope遷移として扱い、依存関係と区別して保存します。

検索には索引付きSQLiteを継続して使用し、取込時に構築する不変オンメモリ検索構造は導入しません。
Issue #14で観測された制約はSQLiteの探索性能ではなく、単一接続における`database/sql`の接続待ちでした。
接続数だけを変更した比較で待ち時間とレイテンシが改善し、オンメモリ化の必要性を示す測定結果がないためです。

採否の根拠と測定値は[設計判断](decisions.md#対応規模と検索方式)を参照してください。

## SQLiteの接続

取込用SQLiteは最大1接続で開きます。
検索用SQLiteは読み取り専用で開き、最大接続数と最大アイドル接続数を4にします。
接続の有効期間とアイドル期間には上限を設けません。

接続ごとの設定は、`sql.DB.Exec`で一度だけPRAGMAを実行する方式ではなく、DSNのクエリパラメーターで指定します。
この方式により、接続プールが後から作る接続にも同じ設定を適用します。

| 用途 | 最大接続数 | DSNで指定する設定 |
|---|---:|---|
| 取込 | 1 | `_foreign_keys=on` |
| 検索 | 4 | `_foreign_keys=on`、`mode=ro`、`immutable=1`、`_query_only=1` |

`immutable=1`は、検索世代のファイルを参照が残る間は置換も削除もしないことを前提に使用します。

## SQLiteの作成と切替

スナップショットを取り込むたびに`importing.db`を新規作成します。
ノード、リミット、依存関係を登録した後で、検索用のインデックスを作ります。

検索先を切り替える前に、外部キー、SQLiteの整合性、親子関係の循環、代表的な検索結果を確認します。
すべての確認に通った場合だけ、新しいSQLiteを検索に使います。

検査後は`importing.db`を閉じ、`generation-`、20桁の連番、`.db`から成る世代別ファイル名へrenameします。
検索用SQLiteは、この世代別ファイルを読み取り専用で開きます。
固定した`current.db`のパスは再利用しません。

検索先を切り替えると、旧世代を退役状態にします。
旧世代を参照する検索が残っている場合は、その参照が0になるまでSQLiteを閉じず、ファイルも削除しません。
最後の参照を解放した後に、SQLiteを閉じて世代別ファイルを削除します。

検索中の切替と元のSQLiteを閉じる条件は[運用](../operations.md#検索と取込の同時実行)で定めます。

## 検索世代メタデータ

検索世代は、次のメタデータをSQLiteと組にして保持します。

| 項目 | 内容 |
|---|---|
| `SnapshotID` | スナップショットの識別子 |
| `GeneratedAt` | スナップショットの生成日時 |
| `SchemaVersion` | 取込形式のバージョン |
| `NodeCount` | ノード数 |
| `RelationCount` | relation数 |
| `LimitCount` | リミット設定の総数 |
| `MaxJobNetworkDepth` | ジョブネット階層の最大深さ |
| `Fingerprint` | 展開後の内容フィンガープリント |

`Store.Acquire`は、SQLite、同じ世代のメタデータ、解放関数をまとめて返します。
切替後も解放関数を呼ぶまでは、SQLiteとメタデータの世代が変わりません。

## 内容フィンガープリント

内容フィンガープリントは、展開後の`manifest.json`、`nodes.ndjson`、`relations.ndjson`をこの固定順序でSHA-256へ入力し、16進文字列として保持します。
各ファイルは、ファイル名のバイト長を64ビット符号なし整数のbig-endianで入力し、ファイル名、内容長を64ビット符号なし整数のbig-endian、内容のバイト列の順に続けます。
ファイル名と内容長を境界情報に含めるため、異なるファイル分割が同じ連結バイト列になることを防ぎます。

tarとgzipのヘッダー、アーカイブ内のファイル順、権限、更新日時、gzipの圧縮レベルは対象に含めません。
展開後の三ファイルが同じであれば、コンテナ表現だけが異なるアーカイブからも同じ値を生成します。
展開後ファイルの空白や改行を含む内容が変われば、内容フィンガープリントも変わります。
