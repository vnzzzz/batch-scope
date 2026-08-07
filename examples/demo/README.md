# デモデータ

このディレクトリには、取込データと後続リミット検索のレスポンス例を置きます。

デモには、次の要素を含めています。

- 管理単位、ジョブネット、ジョブの親子関係
- ジョブマネージャーに記載された先行後続関係
- トリガファイルを介した依存関係
- 外部イベントを介した循環
- `finish_by`と`max_elapsed`のリミット
- 循環と合流を含む経路ツリー

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

現在のサービス骨格は、スナップショット取込と後続リミット検索をまだ実装していません。
`responses/downstream-limit-analysis.json`は、API実装前にも表示方法とレスポンス形式を確認するための例です。
