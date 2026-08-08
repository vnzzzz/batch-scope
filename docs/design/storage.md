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

## SQLiteの作成と切替

スナップショットを取り込むたびに`importing.db`を新規作成します。
ノード、リミット、依存関係を登録した後で、検索用のインデックスを作ります。

検索先を切り替える前に、外部キー、SQLiteの整合性、親子関係の循環、代表的な検索結果を確認します。
すべての確認に通った場合だけ、新しいSQLiteを検索に使います。

検索中の切替と元のSQLiteを閉じる条件は[運用](../operations.md#検索と取込の同時実行)で定めます。
