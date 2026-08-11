# デモデータ

このディレクトリには、取込データと後続リミット検索のレスポンス例を置きます。

デモには、次の要素を含めています。

- 管理単位、ジョブネット、ジョブの親子関係
- ジョブマネージャーに記載された先行後続関係
- トリガファイルを介した依存関係
- 外部イベントを介した循環
- `finish_by`と`max_elapsed`のリミット
- `raw`のリミットまでの長い直列経路と`hiddenConnections`による圧縮表示
- 合流参照と循環参照を含む経路ツリー
- リミットなし終端、リミットなし循環、探索対象外の管理単位へ達する経路

## レスポンス例の表示

実行環境：Dev Container

```bash
make demo-view
```

JSONを直接読む代わりに、対象、リミット、経路、循環をテキストで確認できます。

任意のAPIレスポンスを表示する場合は、JSONファイルを指定します。

実行環境：Dev Container

```bash
./scripts/show-limit-analysis.sh result.json
```

APIから直接渡すこともできます。

実行環境：Dev Container

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' \
  | ./scripts/show-limit-analysis.sh
```

`responses/downstream-limit-analysis.json`は、このスナップショットを取り込み、`targetId=JOB-A`で後続リミット取得APIを呼び出して生成しています。
自動テストでは、固定した`bootId`を除くレスポンス全体が実装の出力と一致することを確認します。
