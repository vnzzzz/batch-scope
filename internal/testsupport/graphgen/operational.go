package graphgen

import "fmt"

const (
	OperationalNodeCount     = 400_000
	OperationalRelationCount = 300_000
	OperationalNetworkCount  = 4_000
	OperationalLimitCount    = 5_000
)

// Operational400Kは、実運用で確認されたsnapshot全体規模と、一検索の到達範囲が小さいケースを
// 同じdatasetで測定するための決定的なprofileを返す。通常のtest suiteでは生成しない。
func Operational400K() Dataset {
	return operationalDataset(
		OperationalNodeCount,
		OperationalRelationCount,
		OperationalNetworkCount,
		OperationalLimitCount,
	)
}

func operationalDataset(nodeCount, relationCount, networkCount, limitCount int) Dataset {
	if networkCount < 1 || nodeCount <= networkCount+1 {
		panic(fmt.Sprintf("operational dataset needs jobs below networks: nodes=%d networks=%d", nodeCount, networkCount))
	}
	jobCount := nodeCount - networkCount - 1
	if limitCount < 0 || limitCount > jobCount {
		panic(fmt.Sprintf("operational limit count out of range: limits=%d jobs=%d", limitCount, jobCount))
	}

	jobsPerNetwork := make([]int, networkCount)
	for index := range jobCount {
		jobsPerNetwork[index%networkCount]++
	}
	maxRelations := 0
	for _, count := range jobsPerNetwork {
		if count > 0 {
			maxRelations += count - 1
		}
	}
	if relationCount < 0 || relationCount > maxRelations {
		panic(fmt.Sprintf("operational relation count out of range: relations=%d max=%d", relationCount, maxRelations))
	}

	nodes := make([]Node, 0, nodeCount)
	rootID := "OPS-ROOT"
	nodes = append(nodes, Node{Type: "job_network", ID: rootID, Name: rootID, LimitFacts: []Limit{}})

	jobIndex := 0
	remainingLimits := limitCount
	remainingRelations := relationCount
	for networkIndex, count := range jobsPerNetwork {
		networkID := operationalNetworkID(networkIndex)
		root := rootID
		nodes = append(nodes, Node{
			Type: "job_network", ID: networkID, Name: networkID, ParentID: &root, LimitFacts: []Limit{},
		})
		for localIndex := 0; localIndex < count; localIndex++ {
			id := operationalJobID(jobIndex)
			parent := networkID
			facts := []Limit{}
			if remainingLimits > 0 {
				facts = []Limit{rawLimit("LIMIT-" + id)}
				remainingLimits--
			}
			nodes = append(nodes, Node{Type: "job", ID: id, Name: id, ParentID: &parent, LimitFacts: facts})
			jobIndex++
		}
	}

	relations := make([]Relation, 0, relationCount)
	jobIndex = 0
	for _, count := range jobsPerNetwork {
		available := count - 1
		if available < 0 {
			available = 0
		}
		use := min(available, remainingRelations)
		for localIndex := 0; localIndex < use; localIndex++ {
			relations = append(relations, Relation{
				FromID: operationalJobID(jobIndex + localIndex),
				ToID: operationalJobID(jobIndex + localIndex + 1),
				Kind: "precedes", Origin: defaultOrigin, Certainty: defaultCertainty,
			})
		}
		remainingRelations -= use
		jobIndex += count
	}
	if remainingRelations != 0 {
		panic(fmt.Sprintf("operational relation construction left %d relations", remainingRelations))
	}

	representativeTarget := operationalNetworkID(0)
	return Dataset{
		Name: "operational-400k",
		Nodes: nodes,
		Relations: relations,
		Expectations: []Expectation{
			{
				InputNodeCount: nodeCount, InputRelationCount: relationCount, InputLimitCount: limitCount,
				TargetID: rootID,
			},
			{
				InputNodeCount: nodeCount, InputRelationCount: relationCount, InputLimitCount: limitCount,
				TargetID: representativeTarget,
			},
		},
	}
}

func operationalNetworkID(index int) string { return fmt.Sprintf("OPS-NET-%04d", index) }
func operationalJobID(index int) string     { return fmt.Sprintf("OPS-JOB-%06d", index) }
