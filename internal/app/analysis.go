package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"batchscope/internal/limitscan"
	"batchscope/internal/observability"
	"batchscope/internal/pathtree"
	"batchscope/internal/store"
	"batchscope/internal/target"
	"batchscope/internal/traversal"

	"github.com/danielgtaylor/huma/v2"
)

const analysisOperation = "downstream_limit_analysis"

type analysisInput struct {
	TargetID        string `query:"targetId" doc:"Exact job or job network ID to analyze"`
	IncludeEvidence string `query:"includeEvidence" doc:"Include relation evidence in the response"`
	RequestID       string `header:"X-Request-Id" doc:"Request identifier used in structured logs"`

	targetIDProvided        bool
	targetIDRepeated        bool
	includeEvidenceRepeated bool
}

// ResolveはHumaの束縛後もパラメーターの有無と重複を保持し、不正値をこのAPIのProblem Detailsへ写像できるようにする。
func (input *analysisInput) Resolve(ctx huma.Context) []error {
	requestURL := ctx.URL()
	query := requestURL.Query()
	input.targetIDProvided = query.Has("targetId")
	input.targetIDRepeated = len(query["targetId"]) > 1
	input.includeEvidenceRepeated = len(query["includeEvidence"]) > 1
	return nil
}

type analysisResponse struct {
	BootID          string                   `json:"bootId"`
	SnapshotID      string                   `json:"snapshotId"`
	Target          target.Details           `json:"target"`
	Limits          analysisLimitSections    `json:"limits"`
	Tree            analysisTreeNode         `json:"tree"`
	UncoveredRoutes []analysisUncoveredRoute `json:"uncoveredRoutes" nullable:"false"`
	Cycles          []analysisCycle          `json:"cycles" nullable:"false"`
}

type analysisOutput struct {
	Body analysisResponse
}

type analysisNode struct {
	ID   string `json:"id"`
	Type string `json:"type" enum:"management_unit,job_network,job,file,file_pattern,job_status,external_event"`
	Name string `json:"name"`
}

type analysisLimitSections struct {
	Target     analysisLimits `json:"target"`
	Contained  analysisLimits `json:"contained"`
	Downstream analysisLimits `json:"downstream"`
}

type analysisLimits struct {
	FinishByGroups []analysisFinishByGroup `json:"finishByGroups" nullable:"false"`
	MaxElapsed     analysisLimitGroup      `json:"maxElapsed"`
	Raw            analysisLimitGroup      `json:"raw"`
}

type analysisFinishByGroup struct {
	TimeZone string              `json:"timeZone"`
	Total    int                 `json:"total"`
	Items    []analysisLimitItem `json:"items" nullable:"false"`
}

type analysisLimitGroup struct {
	Total int                 `json:"total"`
	Items []analysisLimitItem `json:"items" nullable:"false"`
}

type analysisLimitItem struct {
	LimitOwner         analysisNode  `json:"limitOwner"`
	ScopeRoot          *analysisNode `json:"scopeRoot,omitempty"`
	Fact               analysisFact  `json:"fact"`
	TreeNodeID         string        `json:"treeNodeId"`
	AlternatePathCount int           `json:"alternatePathCount"`
}

type analysisFact struct {
	ID                string  `json:"id"`
	Kind              string  `json:"kind" enum:"finish_by,max_elapsed,raw"`
	BusinessDayOffset *int64  `json:"businessDayOffset,omitempty"`
	LocalTime         *string `json:"localTime,omitempty"`
	TimeZone          *string `json:"timeZone,omitempty"`
	Duration          *string `json:"duration,omitempty"`
	SourceText        *string `json:"sourceText,omitempty"`
	Origin            string  `json:"origin" enum:"scheduler,deterministic_analysis,ai_analysis,manual"`
	Certainty         string  `json:"certainty" enum:"declared,confirmed,inferred,candidate"`
}

type analysisTreeNode struct {
	TreeNodeID             string                     `json:"treeNodeId"`
	Node                   analysisNode               `json:"node"`
	ViaRelations           []analysisRelation         `json:"viaRelations,omitempty" nullable:"false"`
	ViaScope               bool                       `json:"viaScope,omitempty"`
	HiddenConnections      []analysisHiddenConnection `json:"hiddenConnections,omitempty" nullable:"false"`
	HiddenJobCount         int                        `json:"hiddenJobCount,omitempty"`
	HiddenNodeIDs          []string                   `json:"hiddenNodeIds,omitempty" nullable:"false"`
	HiddenNodeIDsTruncated bool                       `json:"hiddenNodeIdsTruncated,omitempty"`
	ReferenceType          string                     `json:"referenceType,omitempty" enum:"shared,cycle"`
	ReferenceTo            string                     `json:"referenceTo,omitempty"`
	CycleID                string                     `json:"cycleId,omitempty"`
	Children               []analysisTreeNode         `json:"children" nullable:"false"`
}

type analysisHiddenConnection struct {
	FromID       string             `json:"fromId"`
	ToID         string             `json:"toId"`
	ViaRelations []analysisRelation `json:"viaRelations" nullable:"false"`
	ViaScope     bool               `json:"viaScope"`
}

type analysisRelation struct {
	Kind      string              `json:"kind" enum:"precedes,produces,triggers,consumed_by,observed_by"`
	Origin    string              `json:"origin" enum:"scheduler,deterministic_analysis,ai_analysis,manual"`
	Certainty string              `json:"certainty" enum:"declared,confirmed,inferred,candidate"`
	Evidence  *[]analysisEvidence `json:"evidence,omitempty"`
}

type analysisEvidence struct {
	Source   string                    `json:"source"`
	Location *analysisEvidenceLocation `json:"location,omitempty"`
	Note     *string                   `json:"note,omitempty"`
}

type analysisEvidenceLocation struct {
	StartLine   *int    `json:"startLine,omitempty"`
	EndLine     *int    `json:"endLine,omitempty"`
	JSONPointer *string `json:"jsonPointer,omitempty"`
}

type analysisUncoveredRoute struct {
	Boundary   analysisNode `json:"boundary"`
	Reason     string       `json:"reason" enum:"terminal_without_limit,cycle_without_limit,non_traversable_node_type"`
	TreeNodeID string       `json:"treeNodeId"`
	CycleID    string       `json:"cycleId,omitempty"`
}

type analysisCycle struct {
	CycleID                   string              `json:"cycleId"`
	Nodes                     []analysisNode      `json:"nodes" nullable:"false"`
	Route                     []analysisCycleStep `json:"route" nullable:"false"`
	ContainsImplicitRelation  bool                `json:"containsImplicitRelation"`
	ContainsUncertainRelation bool                `json:"containsUncertainRelation"`
}

type analysisCycleStep struct {
	FromID       string             `json:"fromId"`
	ToID         string             `json:"toId"`
	ViaRelations []analysisRelation `json:"viaRelations" nullable:"false"`
	ViaScope     bool               `json:"viaScope"`
}

func registerAnalysisRoute(api huma.API, a *App) {
	huma.Register(api, huma.Operation{
		OperationID: "analyze-downstream-limits",
		Method:      http.MethodGet,
		Path:        "/v1/downstream-limit-analysis",
		Summary:     "Analyze downstream configured limits and dependency routes",
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
		// パラメーターはハンドラーで検査し、不正値をHumaの422ではなくinvalid-requestへ写像する。
		SkipValidateParams: true,
	}, a.downstreamLimitAnalysis)

	operation := api.OpenAPI().Paths["/v1/downstream-limit-analysis"].Get
	for _, parameter := range operation.Parameters {
		switch parameter.Name {
		case "targetId":
			parameter.Required = true
		case "includeEvidence":
			// 文字列として束縛して解析失敗をハンドラーへ渡すが、公開契約はbooleanのままにする。
			parameter.Schema.Type = "boolean"
			parameter.Schema.Default = false
		}
	}
	delete(operation.Responses, "422")
}

func (a *App) downstreamLimitAnalysis(ctx context.Context, input *analysisInput) (*analysisOutput, error) {
	started := time.Now()
	requestID := input.RequestID
	targetID := input.TargetID
	var snapshotID, errorType string
	var reachedNodes, returnedTreeNodes, returnedLimits, cyclesDetected, uncoveredRoutes int
	defer func() {
		attrs := observability.Attrs(observability.Fields{
			RequestID: requestID, Operation: analysisOperation, Duration: time.Since(started),
			BootID: a.bootID, SnapshotID: snapshotID, TargetID: targetID,
			ReachedNodes: reachedNodes, ReturnedTreeNodes: returnedTreeNodes,
			ReturnedLimits: returnedLimits, CyclesDetected: cyclesDetected,
			UncoveredRoutes: uncoveredRoutes, ErrorType: errorType,
		})
		a.log().LogAttrs(ctx, slog.LevelInfo, "downstream limit analysis completed", attrs...)
	}()

	if requestID == "" {
		var err error
		requestID, err = newBootID()
		if err != nil {
			errorType = "internal-error"
			return nil, InternalErrorProblem("failed to generate request ID")
		}
	}
	includeEvidence, problem := validateAnalysisInput(input)
	if problem != nil {
		errorType = "invalid-request"
		return nil, problem
	}

	db, generation, release, err := a.store.Acquire()
	if errors.Is(err, store.ErrNoDatabase) {
		errorType = "snapshot-not-loaded"
		return nil, SnapshotNotLoadedProblem("searchable snapshot is not loaded")
	}
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("failed to acquire search snapshot")
	}
	// 全問い合わせとDTO組立てが終わるまで世代を保持し、並行切替でsnapshotIdと業務結果が別世代になることを防ぐ。
	defer release()
	snapshotID = generation.SnapshotID

	traversalResult, err := traversal.Traverse(ctx, db, targetID)
	if errors.Is(err, traversal.ErrTargetNotFound) || errors.Is(err, traversal.ErrInvalidTargetType) {
		errorType = "target-not-found"
		return nil, TargetNotFoundProblem("analysis target was not found")
	}
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("downstream traversal failed")
	}
	reachedNodes = len(traversalResult.Nodes)

	limitResult, err := limitscan.Scan(ctx, db, traversalResult)
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("limit scan failed")
	}
	pathResult, err := pathtree.Build(ctx, traversalResult, limitResult)
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("path tree construction failed")
	}
	targetDetails, err := target.LoadOne(ctx, db, targetID)
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("target details lookup failed")
	}

	response, err := buildAnalysisResponse(a.bootID, snapshotID, targetDetails, limitResult, pathResult, includeEvidence)
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("analysis response construction failed")
	}
	returnedTreeNodes = countAnalysisTreeNodes(response.Tree)
	returnedLimits = countAnalysisLimits(response.Limits)
	cyclesDetected = len(response.Cycles)
	uncoveredRoutes = len(response.UncoveredRoutes)
	return &analysisOutput{Body: response}, nil
}

func validateAnalysisInput(input *analysisInput) (bool, *huma.ErrorModel) {
	if !input.targetIDProvided || input.TargetID == "" {
		return false, InvalidRequestProblem("targetId is required")
	}
	if input.targetIDRepeated {
		return false, InvalidRequestProblem("targetId must be specified once")
	}
	if input.includeEvidenceRepeated {
		return false, InvalidRequestProblem("includeEvidence must be specified once")
	}
	if input.IncludeEvidence == "" {
		return false, nil
	}
	if input.IncludeEvidence != "true" && input.IncludeEvidence != "false" {
		return false, InvalidRequestProblem("includeEvidence must be true or false")
	}
	value, _ := strconv.ParseBool(input.IncludeEvidence)
	return value, nil
}

func buildAnalysisResponse(
	bootID string,
	snapshotID string,
	targetDetails target.Details,
	limitResult limitscan.Result,
	pathResult pathtree.Result,
	includeEvidence bool,
) (analysisResponse, error) {
	limits, err := mapAnalysisLimitSections(limitResult, pathResult.LimitReferences)
	if err != nil {
		return analysisResponse{}, err
	}
	tree, err := mapAnalysisTree(pathResult.Tree, includeEvidence)
	if err != nil {
		return analysisResponse{}, err
	}
	uncovered := make([]analysisUncoveredRoute, len(pathResult.UncoveredRoutes))
	for index, route := range pathResult.UncoveredRoutes {
		uncovered[index] = analysisUncoveredRoute{
			Boundary: analysisNodeFromTraversal(route.Boundary), Reason: string(route.Reason),
			TreeNodeID: route.TreeNodeID, CycleID: route.CycleID,
		}
	}
	cycles, err := mapAnalysisCycles(pathResult.Cycles, includeEvidence)
	if err != nil {
		return analysisResponse{}, err
	}
	return analysisResponse{
		BootID: bootID, SnapshotID: snapshotID, Target: targetDetails, Limits: limits,
		Tree: tree, UncoveredRoutes: uncovered, Cycles: cycles,
	}, nil
}

func mapAnalysisLimitSections(source limitscan.Result, references map[string]pathtree.LimitReference) (analysisLimitSections, error) {
	targetLimits, err := mapAnalysisLimits(source.Target, references)
	if err != nil {
		return analysisLimitSections{}, err
	}
	contained, err := mapAnalysisLimits(source.Contained, references)
	if err != nil {
		return analysisLimitSections{}, err
	}
	downstream, err := mapAnalysisLimits(source.Downstream, references)
	if err != nil {
		return analysisLimitSections{}, err
	}
	return analysisLimitSections{Target: targetLimits, Contained: contained, Downstream: downstream}, nil
}

func mapAnalysisLimits(source limitscan.Limits, references map[string]pathtree.LimitReference) (analysisLimits, error) {
	finishGroups := make([]analysisFinishByGroup, len(source.FinishByGroups))
	for index, group := range source.FinishByGroups {
		if group.Total != len(group.Items) {
			return analysisLimits{}, fmt.Errorf("finish_by group %q total does not match items", group.TimeZone)
		}
		items, err := mapAnalysisLimitItems(group.Items, references)
		if err != nil {
			return analysisLimits{}, err
		}
		finishGroups[index] = analysisFinishByGroup{TimeZone: group.TimeZone, Total: len(items), Items: items}
	}
	maxElapsed, err := mapAnalysisLimitGroup(source.MaxElapsed, references)
	if err != nil {
		return analysisLimits{}, err
	}
	raw, err := mapAnalysisLimitGroup(source.Raw, references)
	if err != nil {
		return analysisLimits{}, err
	}
	return analysisLimits{FinishByGroups: finishGroups, MaxElapsed: maxElapsed, Raw: raw}, nil
}

func mapAnalysisLimitGroup(source limitscan.Group, references map[string]pathtree.LimitReference) (analysisLimitGroup, error) {
	if source.Total != len(source.Items) {
		return analysisLimitGroup{}, errors.New("limit group total does not match items")
	}
	items, err := mapAnalysisLimitItems(source.Items, references)
	if err != nil {
		return analysisLimitGroup{}, err
	}
	return analysisLimitGroup{Total: len(items), Items: items}, nil
}

func mapAnalysisLimitItems(source []limitscan.Item, references map[string]pathtree.LimitReference) ([]analysisLimitItem, error) {
	items := make([]analysisLimitItem, len(source))
	for index, item := range source {
		reference, ok := references[item.LimitOwner.ID]
		if !ok || reference.TreeNodeID == "" {
			return nil, fmt.Errorf("limit owner %q has no tree reference", item.LimitOwner.ID)
		}
		fact, err := mapAnalysisFact(item.Fact)
		if err != nil {
			return nil, err
		}
		items[index] = analysisLimitItem{
			LimitOwner: analysisNodeFromLimit(item.LimitOwner), Fact: fact,
			TreeNodeID: reference.TreeNodeID, AlternatePathCount: reference.AlternatePathCount,
		}
		if item.ScopeRoot != nil {
			scopeRoot := analysisNodeFromLimit(*item.ScopeRoot)
			items[index].ScopeRoot = &scopeRoot
		}
	}
	return items, nil
}

func mapAnalysisFact(source limitscan.Fact) (analysisFact, error) {
	fact := analysisFact{
		ID: source.ID, Kind: string(source.Kind), BusinessDayOffset: source.BusinessDayOffset,
		LocalTime: source.LocalTime, TimeZone: source.TimeZone, SourceText: source.SourceText,
		Origin: source.Origin, Certainty: source.Certainty,
	}
	if source.Kind == limitscan.KindMaxElapsed {
		if source.DurationSeconds == nil {
			return analysisFact{}, fmt.Errorf("max_elapsed limit %q has no duration", source.ID)
		}
		duration, err := normalizeISODuration(*source.DurationSeconds)
		if err != nil {
			return analysisFact{}, fmt.Errorf("max_elapsed limit %q: %w", source.ID, err)
		}
		fact.Duration = &duration
	}
	return fact, nil
}

func normalizeISODuration(totalSeconds int64) (string, error) {
	if totalSeconds < 0 {
		return "", errors.New("duration seconds must not be negative")
	}
	days := totalSeconds / 86_400
	remainder := totalSeconds % 86_400
	hours := remainder / 3_600
	remainder %= 3_600
	minutes := remainder / 60
	seconds := remainder % 60

	result := "P"
	if days != 0 {
		result += strconv.FormatInt(days, 10) + "D"
	}
	if hours != 0 || minutes != 0 || seconds != 0 || totalSeconds == 0 {
		result += "T"
		if hours != 0 {
			result += strconv.FormatInt(hours, 10) + "H"
		}
		if minutes != 0 {
			result += strconv.FormatInt(minutes, 10) + "M"
		}
		if seconds != 0 || totalSeconds == 0 {
			result += strconv.FormatInt(seconds, 10) + "S"
		}
	}
	return result, nil
}

func mapAnalysisTree(source pathtree.TreeNode, includeEvidence bool) (analysisTreeNode, error) {
	relations, err := mapAnalysisRelations(source.ViaRelations, includeEvidence)
	if err != nil {
		return analysisTreeNode{}, err
	}
	hidden := make([]analysisHiddenConnection, len(source.HiddenConnections))
	for index, connection := range source.HiddenConnections {
		mappedRelations, err := mapAnalysisRelations(connection.ViaRelations, includeEvidence)
		if err != nil {
			return analysisTreeNode{}, err
		}
		hidden[index] = analysisHiddenConnection{
			FromID: connection.FromID, ToID: connection.ToID,
			ViaRelations: mappedRelations, ViaScope: connection.ViaScope,
		}
	}
	children := make([]analysisTreeNode, len(source.Children))
	for index, child := range source.Children {
		if child == nil {
			return analysisTreeNode{}, errors.New("path tree contains a nil child")
		}
		mapped, err := mapAnalysisTree(*child, includeEvidence)
		if err != nil {
			return analysisTreeNode{}, err
		}
		children[index] = mapped
	}
	return analysisTreeNode{
		TreeNodeID: source.TreeNodeID, Node: analysisNodeFromTraversal(source.Node),
		ViaRelations: relations, ViaScope: source.ViaScope, HiddenConnections: hidden,
		HiddenJobCount: source.HiddenJobCount, HiddenNodeIDs: append([]string(nil), source.HiddenNodeIDs...),
		HiddenNodeIDsTruncated: source.HiddenNodeIDsTruncated,
		ReferenceType:          string(source.ReferenceType), ReferenceTo: source.ReferenceTo, CycleID: source.CycleID,
		Children: children,
	}, nil
}

func mapAnalysisCycles(source []pathtree.Cycle, includeEvidence bool) ([]analysisCycle, error) {
	cycles := make([]analysisCycle, len(source))
	for index, cycle := range source {
		nodes := make([]analysisNode, len(cycle.Nodes))
		for nodeIndex, node := range cycle.Nodes {
			nodes[nodeIndex] = analysisNodeFromTraversal(node)
		}
		route := make([]analysisCycleStep, len(cycle.Route))
		for stepIndex, step := range cycle.Route {
			relations, err := mapAnalysisRelations(step.ViaRelations, includeEvidence)
			if err != nil {
				return nil, err
			}
			route[stepIndex] = analysisCycleStep{
				FromID: step.FromID, ToID: step.ToID, ViaRelations: relations, ViaScope: step.ViaScope,
			}
		}
		cycles[index] = analysisCycle{
			CycleID: cycle.CycleID, Nodes: nodes, Route: route,
			ContainsImplicitRelation:  cycle.ContainsImplicitRelation,
			ContainsUncertainRelation: cycle.ContainsUncertainRelation,
		}
	}
	return cycles, nil
}

func mapAnalysisRelations(source []traversal.Relation, includeEvidence bool) ([]analysisRelation, error) {
	relations := make([]analysisRelation, len(source))
	for index, relation := range source {
		relations[index] = analysisRelation{Kind: relation.Kind, Origin: relation.Origin, Certainty: relation.Certainty}
		if !includeEvidence || len(relation.Evidence) == 0 {
			continue
		}
		var evidence []analysisEvidence
		if err := json.Unmarshal(relation.Evidence, &evidence); err != nil {
			return nil, fmt.Errorf("relation evidence is invalid JSON: %w", err)
		}
		if evidence == nil {
			return nil, errors.New("relation evidence is not an array")
		}
		for _, item := range evidence {
			if item.Source == "" {
				return nil, errors.New("relation evidence has no source")
			}
		}
		relations[index].Evidence = &evidence
	}
	return relations, nil
}

func analysisNodeFromTraversal(node traversal.Node) analysisNode {
	return analysisNode{ID: node.ID, Type: node.Type, Name: node.Name}
}

func analysisNodeFromLimit(node limitscan.Node) analysisNode {
	return analysisNode{ID: node.ID, Type: node.Type, Name: node.Name}
}

func countAnalysisTreeNodes(root analysisTreeNode) int {
	count := 0
	stack := []analysisTreeNode{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		count++
		stack = append(stack, current.Children...)
	}
	return count
}

func countAnalysisLimits(limits analysisLimitSections) int {
	count := func(section analysisLimits) int {
		total := section.MaxElapsed.Total + section.Raw.Total
		for _, group := range section.FinishByGroups {
			total += group.Total
		}
		return total
	}
	return count(limits.Target) + count(limits.Contained) + count(limits.Downstream)
}
