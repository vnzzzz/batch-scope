# デモ

## 目的

デモデータは、取込形式、後続リミット、暗黙的な依存関係、循環の表示を小さなデータで確認するために使います。
Web画面は作成せず、APIレスポンスを整形したテキストで確認します。

## 構成

```text
examples/demo/
├── snapshot/                              # 取込データ
│   ├── manifest.json
│   ├── nodes.ndjson
│   └── relations.ndjson
└── responses/
    └── downstream-limit-analysis.json     # APIレスポンス例
```

データの内容は[`../../examples/demo/README.md`](../../examples/demo/README.md)に記載します。

## レスポンス例の表示

実行環境：Dev Container

```bash
make demo-view
```

出力では、次の項目をJSONから抜き出します。

- 検索対象とスナップショットID
- `target`、`contained`、`downstream`の全リミット
- 通常接続のrelationとscope遷移
- 圧縮区間の`hiddenConnections`
- 合流参照と循環参照
- リミット未通過経路の理由と境界
- 循環の一周分の表示経路

## 実際のAPIレスポンスの表示

実際のAPIレスポンスは、標準入力から渡せます。

実行環境：Dev Container

```bash
curl -fsS \
  'http://127.0.0.1:8080/v1/downstream-limit-analysis?targetId=JOB-A' \
  | ./scripts/show-limit-analysis.sh
```

このスクリプトは開発時の確認にだけ使用します。
本番用コンテナイメージには含めません。
