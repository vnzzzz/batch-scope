package app

import (
	"context"
	"database/sql"

	"batchscope/internal/identity"
	"batchscope/internal/traversal"
)

type analysisIdentityRef struct {
	Namespace string `json:"namespace"`
	LocalID   string `json:"localId"`
}

func loadAnalysisPublicIdentities(ctx context.Context, db *sql.DB, result traversal.Result) (map[string]identity.Public, error) {
	ids := make([]string, 0, len(result.Nodes)+len(result.Endpoints))
	seen := make(map[string]struct{}, cap(ids))
	appendID := func(id string) {
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, reached := range result.Nodes {
		appendID(reached.Node.ID)
	}
	for _, endpoint := range result.Endpoints {
		appendID(endpoint.Node.ID)
	}
	return identity.LoadPublic(ctx, db, ids)
}

func applyAnalysisPublicIdentities(response *analysisResponse, identities map[string]identity.Public) {
	publicIdentity := func(id string) identity.Public {
		value, ok := identities[id]
		if !ok {
			return identity.LegacyPublic(id)
		}
		return value
	}
	identityRef := func(id string) analysisIdentityRef {
		value := publicIdentity(id)
		return analysisIdentityRef{Namespace: value.Namespace, LocalID: value.LocalID}
	}
	applyNode := func(node *analysisNode) {
		value := publicIdentity(node.ID)
		node.Namespace = value.Namespace
		node.LocalID = value.LocalID
	}
	applyLimits := func(limits *analysisLimits) {
		for groupIndex := range limits.FinishByGroups {
			for itemIndex := range limits.FinishByGroups[groupIndex].Items {
				item := &limits.FinishByGroups[groupIndex].Items[itemIndex]
				applyNode(&item.LimitOwner)
				if item.ScopeRoot != nil {
					applyNode(item.ScopeRoot)
				}
			}
		}
		for itemIndex := range limits.MaxElapsed.Items {
			item := &limits.MaxElapsed.Items[itemIndex]
			applyNode(&item.LimitOwner)
			if item.ScopeRoot != nil {
				applyNode(item.ScopeRoot)
			}
		}
		for itemIndex := range limits.Raw.Items {
			item := &limits.Raw.Items[itemIndex]
			applyNode(&item.LimitOwner)
			if item.ScopeRoot != nil {
				applyNode(item.ScopeRoot)
			}
		}
	}
	applyLimits(&response.Limits.Target)
	applyLimits(&response.Limits.Contained)
	applyLimits(&response.Limits.Downstream)

	var applyTree func(*analysisTreeNode)
	applyTree = func(tree *analysisTreeNode) {
		applyNode(&tree.Node)
		for index := range tree.HiddenConnections {
			connection := &tree.HiddenConnections[index]
			connection.FromIdentity = identityRef(connection.FromID)
			connection.ToIdentity = identityRef(connection.ToID)
		}
		for index := range tree.Children {
			applyTree(&tree.Children[index])
		}
	}
	applyTree(&response.Tree)

	for index := range response.UncoveredRoutes {
		applyNode(&response.UncoveredRoutes[index].Boundary)
	}
	for cycleIndex := range response.Cycles {
		for nodeIndex := range response.Cycles[cycleIndex].Nodes {
			applyNode(&response.Cycles[cycleIndex].Nodes[nodeIndex])
		}
	}
}
