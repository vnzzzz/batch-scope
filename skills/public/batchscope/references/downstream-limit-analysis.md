# 後続リミット取得の読み方

## 要求

`GET /v1/downstream-limit-analysis`が受け取るクエリパラメーターは、必須の`targetId`と任意の`includeEvidence`だけである。
`includeEvidence`の既定値は`false`である。

成功レスポンスは、受入済みスナップショットに対する全件解析の結果である。
処理を完了できない場合はProblem Detailsが返るため、成功レスポンスに部分結果、処理上限到達、未解析範囲を表す項目はない。

`analysis-timeout`が返った場合は部分結果を推測で補わない。
再試行するかは利用者が判断する。

## リミット

`limits.target`、`limits.contained`、`limits.downstream`を区別する。
各区分では`finishByGroups`、`maxElapsed`、`raw`をAPIの返却順のまま読む。

各リミットの`limitOwner`は設定先ジョブ、`scopeRoot`は後続ジョブネットの配下を展開した場合の起点である。
`treeNodeId`は設定先を表す経路ツリーのノードを参照し、`alternatePathCount`は採用した経路以外の経路数を表す。

## 経路ツリー

圧縮していない通常接続は、`viaRelations`または`viaScope`で表す。
`viaScope=true`はジョブネットから直接の論理子ノードへの親子関係であり、依存relationではない。

圧縮区間は`hiddenConnections`で表す。
各要素の`fromId`、`toId`、`viaRelations`、`viaScope`を配列順にたどると、親ノードから表示ノードまでの接続を復元できる。
`hiddenJobCount`は省略したジョブ数、`hiddenNodeIds`は省略したノードIDの先頭1,000件を表す。
`hiddenNodeIdsTruncated=true`でも`hiddenConnections`は切り詰められない。

`referenceType=shared`は別経路との合流、`referenceType=cycle`は循環への回帰である。
どちらも`referenceTo`で既出の`treeNodeId`を参照し、参照ノード自身は子を持たない。
循環参照の`cycleId`は`cycles`の要素を参照する。

## 循環

`cycles[].nodes`は、循環として検出した強連結成分のノードを表す。
`cycles[].route`は、その強連結成分を説明するために同じ入力へ同じ順序で選ぶ一周分の表示経路である。
`route`は強連結成分に存在するすべての単純閉路の列挙ではない。

`route`の各接続は`fromId`、`toId`、`viaRelations`、`viaScope`を持つ。
`containsImplicitRelation`と`containsUncertainRelation`は、強連結成分内のrelation全体に対する性質である。

## リミット未通過経路

`uncoveredRoutes`は、対象から境界までリミット設定先を一件も通過しない経路を表す。
境界ノードにリミットがあるかだけで判定した一覧ではない。

`treeNodeId`は判定に使った経路上の境界出現を参照する。
`reason`は`terminal_without_limit`、`cycle_without_limit`、`non_traversable_node_type`のいずれかであり、循環の場合は`cycleId`も持つ。

## 根拠情報

`includeEvidence=true`が追加する`evidence`は、treeの通常接続、`hiddenConnections`、cycle `route`に含まれるrelationだけに適用する。
リミットの`fact`には`evidence`を追加しない。
