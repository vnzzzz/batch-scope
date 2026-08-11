package graphgen

import "fmt"

// CapacityBoundary は、ノード数、relation数、リミット数を同時に受入上限へ寄せたデータを返す。
// 個々の病理形状ではなく、宣言した対応規模そのもので完遂できるかを確認するために使う。
// relationは前方向の辺だけで構成するため循環はなく、SCC上限の確認はDenseSCCが担当する。
func CapacityBoundary(nodeCount, relationCount, limitCount int) Dataset {
	if nodeCount < 2 || relationCount < nodeCount-1 || limitCount > nodeCount {
		panic(fmt.Sprintf("capacity boundary needs a connected chain: nodes=%d relations=%d limits=%d",
			nodeCount, relationCount, limitCount))
	}
	nodes := make([]Node, 0, nodeCount)
	for index := range nodeCount {
		id := capacityNodeID(index)
		var limits []Limit
		if index < limitCount {
			limits = []Limit{rawLimit("LIMIT-" + id)}
		}
		nodes = append(nodes, newCapacityJob(id, limits))
	}

	// まず一本の鎖で全ノードを対象から到達可能にし、残りをより長い前方向の辺で埋める。
	// 前方向だけに限ることで、追加の辺が意図しないSCCを作らない。
	relations := make([]Relation, 0, relationCount)
	for index := range nodeCount - 1 {
		relations = append(relations, capacityRelation(index, index+1))
	}
	for span := 2; len(relations) < relationCount; span++ {
		for index := 0; index+span < nodeCount && len(relations) < relationCount; index++ {
			relations = append(relations, capacityRelation(index, index+span))
		}
	}

	return Dataset{
		Name: fmt.Sprintf("capacity-boundary-%d-%d-%d", nodeCount, relationCount, limitCount),
		Nodes: nodes, Relations: relations,
		Expectations: []Expectation{{
			InputNodeCount: nodeCount, InputRelationCount: len(relations), InputLimitCount: limitCount,
			TargetID: capacityNodeID(0),
		}},
	}
}

// DenseSCC は、対象から到達する一つの大きな強連結成分を持つデータを返す。
// ringだけでは経路の分岐が起きないため、SCC内へ弦を加えて循環経路の生成量を増やす。
// リミットはSCCの内側と外側の両方へ置き、循環説明とUncoveredRoutesの生成を実際に走らせる。
func DenseSCC(sccSize, chordCount int) Dataset {
	if sccSize < 3 {
		panic(fmt.Sprintf("dense SCC needs at least three nodes: %d", sccSize))
	}
	targetID := "DENSE-TARGET"
	nodes := []Node{newCapacityJob(targetID, nil)}
	relations := make([]Relation, 0, sccSize+chordCount+2)

	for index := range sccSize {
		id := denseSCCNodeID(index)
		// SCC内のリミットは、循環内で見つけたリミットの扱いを一定間隔で確認するために置く。
		var limits []Limit
		if index%128 == 0 {
			limits = []Limit{rawLimit("LIMIT-" + id)}
		}
		nodes = append(nodes, newCapacityJob(id, limits))
		relations = append(relations, Relation{
			FromID: denseSCCNodeID(index), ToID: denseSCCNodeID((index + 1) % sccSize),
			Kind: "precedes", Origin: defaultOrigin, Certainty: defaultCertainty,
		})
	}

	// 弦は互いに素な歩幅で張り、SCCを一周する経路以外の分岐を作る。
	// 歩幅がsccSizeの約数だと同じ部分循環へ偏るため、奇数の歩幅から選ぶ。
	for index := range chordCount {
		span := 2*(index%((sccSize-1)/2)) + 3
		from := (index * 7) % sccSize
		relations = append(relations, Relation{
			FromID: denseSCCNodeID(from), ToID: denseSCCNodeID((from + span) % sccSize),
			Kind: "precedes", Origin: defaultOrigin, Certainty: defaultCertainty,
		})
	}

	// SCCの外側にも出口を作り、循環を抜けた先のリミットと未通過経路を同時に評価させる。
	exitID := "DENSE-EXIT"
	nodes = append(nodes, newCapacityJob(exitID, []Limit{rawLimit("LIMIT-" + exitID)}))
	relations = append(relations,
		Relation{FromID: targetID, ToID: denseSCCNodeID(0), Kind: "precedes", Origin: defaultOrigin, Certainty: defaultCertainty},
		Relation{FromID: denseSCCNodeID(sccSize / 2), ToID: exitID, Kind: "precedes", Origin: defaultOrigin, Certainty: defaultCertainty},
	)

	limitCount := 0
	for _, node := range nodes {
		limitCount += len(node.LimitFacts)
	}
	return Dataset{
		Name: fmt.Sprintf("dense-scc-%d-%d", sccSize, chordCount),
		Nodes: nodes, Relations: relations,
		Expectations: []Expectation{{
			InputNodeCount: len(nodes), InputRelationCount: len(relations), InputLimitCount: limitCount,
			TargetID: targetID,
		}},
	}
}

func newCapacityJob(id string, limits []Limit) Node {
	if limits == nil {
		limits = []Limit{}
	}
	return Node{Type: "job", ID: id, Name: id, LimitFacts: limits}
}

func capacityRelation(from, to int) Relation {
	return Relation{
		FromID: capacityNodeID(from), ToID: capacityNodeID(to),
		Kind: "precedes", Origin: defaultOrigin, Certainty: defaultCertainty,
	}
}

func capacityNodeID(index int) string { return fmt.Sprintf("CAP-%06d", index) }

func denseSCCNodeID(index int) string { return fmt.Sprintf("DENSE-%06d", index) }
