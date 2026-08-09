package snapshot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"batchscope/internal/limits"
	embeddedschema "batchscope/schema"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBaseURL = "https://batchscope.invalid/schema/"

var (
	compiledSchemasOnce sync.Once
	compiledSchemas     snapshotSchemas
	compiledSchemasErr  error
	durationComponents  = regexp.MustCompile(`^P(?:(\d+(?:[.,]\d+)?)W|(?:(\d+(?:[.,]\d+)?)D)?(?:T(?:(\d+(?:[.,]\d+)?)H)?(?:(\d+(?:[.,]\d+)?)M)?(?:(\d+(?:[.,]\d+)?)S)?)?)$`)
)

type snapshotSchemas struct {
	manifest *jsonschema.Schema
	node     *jsonschema.Schema
	relation *jsonschema.Schema
}

type manifest struct {
	NodeCount     json.Number `json:"nodeCount"`
	RelationCount json.Number `json:"relationCount"`
}

type nodeInput struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Path       *string         `json:"path"`
	ParentID   *string         `json:"parentId"`
	LimitFacts []limitFact     `json:"limitFacts"`
	Locator    json.RawMessage `json:"locator"`
	Attributes json.RawMessage `json:"attributes"`
}

type limitFact struct {
	ID                string      `json:"id"`
	Kind              string      `json:"kind"`
	BusinessDayOffset json.Number `json:"businessDayOffset"`
	LocalTime         string      `json:"localTime"`
	TimeZone          string      `json:"timeZone"`
	Duration          string      `json:"duration"`
	SourceText        *string     `json:"sourceText"`
	Origin            string      `json:"origin"`
	Certainty         string      `json:"certainty"`
}

type relation struct {
	FromID    string          `json:"fromId"`
	ToID      string          `json:"toId"`
	Kind      string          `json:"kind"`
	Origin    string          `json:"origin"`
	Certainty string          `json:"certainty"`
	Evidence  json.RawMessage `json:"evidence"`
}

type nodeKind uint8

const (
	nodeKindUnknown nodeKind = iota
	nodeKindManagementUnit
	nodeKindJobNetwork
	nodeKindJob
	nodeKindFile
	nodeKindFilePattern
	nodeKindJobStatus
	nodeKindExternalEvent
)

type nodeRecord struct {
	Type      nodeKind
	ParentID  string
	HasParent bool
	Line      int
}

// ValidationResult は、検査後のSQLite作成で再利用する値を保持する。
// SQLite登録はNDJSONを再読込し、relation_idもその時点で再生成するため、検査中の行データやIDは保持しない。
type ValidationResult struct {
	NodeCount     int
	RelationCount int
}

// Validate は、展開済みスナップショットの形式と参照整合性を検査する。
// 検査フェーズは次の順序に固定し、同じ入力には同じエラーを返す。
//  1. manifest.json: サイズ、UTF-8、JSON、Schema、件数の整数性
//  2. nodes.ndjsonの各行: JSON、意味、Schema
//  3. ノード横断: 件数、行番号順の親参照とノードIDまたはリミットIDの重複または複数の親、親子関係の循環
//  4. relations.ndjsonの各行: JSON、Schema、参照の存在、重複
//  5. 依存関係の件数
//
// 行内で完結する検査と、全ノードを読み終えて初めて判定できる検査は別のフェーズで実行する。
// ノードIDとリミットIDの重複は行の読み込み中に記録するが、より前の行にある親の問題を優先するため報告はノード横断で行う。
// ノード横断では各ノードの親の存在と種別を調べ、ID重複または複数の親と行番号を比較する。
// 検査種別より行番号を優先し、循環内でも最小行を返すことで、取込元が先に直すべき箇所を示す。
func Validate(ctx context.Context, extracted Extracted) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, &Error{Kind: ErrorIO, Err: err}
	}

	schemas, err := getSchemas()
	if err != nil {
		return ValidationResult{}, &Error{Kind: ErrorIO, Err: err}
	}

	metadata, err := readManifest(ctx, extracted.Manifest, schemas.manifest)
	if err != nil {
		return ValidationResult{}, err
	}

	orderedNodeIDs, nodeByID, nodeCount, duplicateErr, err := readNodes(ctx, extracted.Nodes, schemas.node)
	if err != nil {
		return ValidationResult{}, err
	}
	if metadata.nodeCount != nodeCount {
		return ValidationResult{}, &Error{Kind: ErrorNodeCountMismatch, File: manifestName, Pointer: "/nodeCount"}
	}
	if err := validateNodeReferences(orderedNodeIDs, nodeByID, duplicateErr); err != nil {
		return ValidationResult{}, err
	}
	if err := validateParentCycles(ctx, orderedNodeIDs, nodeByID); err != nil {
		return ValidationResult{}, err
	}

	relationCount, err := readRelations(ctx, extracted.Relations, schemas.relation, nodeByID)
	if err != nil {
		return ValidationResult{}, err
	}
	if metadata.relationCount != relationCount {
		return ValidationResult{}, &Error{Kind: ErrorRelationCountMismatch, File: manifestName, Pointer: "/relationCount"}
	}

	return ValidationResult{NodeCount: nodeCount, RelationCount: relationCount}, nil
}

type validatedManifest struct {
	nodeCount     int
	relationCount int
}

func getSchemas() (snapshotSchemas, error) {
	compiledSchemasOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		for _, name := range []string{"manifest.schema.json", "node.schema.json", "relation.schema.json", "limit-fact.schema.json"} {
			contents, err := embeddedschema.Files.ReadFile(name)
			if err != nil {
				compiledSchemasErr = fmt.Errorf("read embedded %s: %w", name, err)
				return
			}
			document, err := jsonschema.UnmarshalJSON(bytes.NewReader(contents))
			if err != nil {
				compiledSchemasErr = fmt.Errorf("parse embedded %s: %w", name, err)
				return
			}
			if err := compiler.AddResource(schemaBaseURL+name, document); err != nil {
				compiledSchemasErr = fmt.Errorf("register embedded %s: %w", name, err)
				return
			}
		}
		compiledSchemas.manifest, compiledSchemasErr = compiler.Compile(schemaBaseURL + "manifest.schema.json")
		if compiledSchemasErr != nil {
			return
		}
		compiledSchemas.node, compiledSchemasErr = compiler.Compile(schemaBaseURL + "node.schema.json")
		if compiledSchemasErr != nil {
			return
		}
		compiledSchemas.relation, compiledSchemasErr = compiler.Compile(schemaBaseURL + "relation.schema.json")
	})
	return compiledSchemas, compiledSchemasErr
}

func readManifest(ctx context.Context, path string, schema *jsonschema.Schema) (validatedManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return validatedManifest{}, &Error{Kind: ErrorIO, File: manifestName, Err: err}
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, limits.MaxManifestBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return validatedManifest{}, &Error{Kind: ErrorIO, File: manifestName, Err: ctxErr}
		}
		return validatedManifest{}, &Error{Kind: ErrorIO, File: manifestName, Err: err}
	}
	if int64(len(contents)) > limits.MaxManifestBytes {
		return validatedManifest{}, &Error{Kind: ErrorManifestSizeLimit, File: manifestName}
	}
	if !utf8.Valid(contents) {
		return validatedManifest{}, &Error{Kind: ErrorInvalidUTF8, File: manifestName}
	}

	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(contents))
	if err != nil {
		return validatedManifest{}, &Error{Kind: ErrorInvalidJSON, File: manifestName, Err: err}
	}
	if err := schema.Validate(value); err != nil {
		return validatedManifest{}, schemaError(manifestName, 0, err)
	}

	var decoded manifest
	if err := remarshal(value, &decoded); err != nil {
		return validatedManifest{}, &Error{Kind: ErrorInvalidJSON, File: manifestName, Err: err}
	}
	nodeCount, ok := jsonIntegerAsInt(decoded.NodeCount)
	if !ok {
		return validatedManifest{}, &Error{Kind: ErrorSchemaViolation, File: manifestName, Pointer: "/nodeCount"}
	}
	relationCount, ok := jsonIntegerAsInt(decoded.RelationCount)
	if !ok {
		return validatedManifest{}, &Error{Kind: ErrorSchemaViolation, File: manifestName, Pointer: "/relationCount"}
	}
	return validatedManifest{nodeCount: nodeCount, relationCount: relationCount}, nil
}

func jsonIntegerAsInt(number json.Number) (int, bool) {
	value, ok := jsonIntegerAsInt64(number)
	if !ok {
		return 0, false
	}
	converted := int(value)
	return converted, int64(converted) == value
}

func jsonIntegerAsInt64(number json.Number) (int64, bool) {
	// Ratで小数点と指数表記を正確に解釈し、JSON上の表記が異なる整数を同じ値として扱う。
	value, ok := new(big.Rat).SetString(number.String())
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0, false
	}
	return value.Num().Int64(), true
}

func readNodes(ctx context.Context, path string, schema *jsonschema.Schema) ([]string, map[string]nodeRecord, int, *Error, error) {
	orderedIDs := make([]string, 0)
	byID := make(map[string]nodeRecord)
	limitIDs := make(map[string]struct{})
	count := 0
	var duplicateErr *Error
	err := readNDJSON(ctx, path, nodesName, func(line int, contents []byte) error {
		count++
		id, record, facts, err := validateNodeLine(schema, line, contents)
		if err != nil {
			return err
		}
		if previous, exists := byID[id]; exists {
			// 最初の定義は単体では正しいため、取り除くべき後続の定義側の行を報告する。
			// 以降の重複は最初の1件より必ず後ろにあり、行番号の比較へ影響しないので保持しない。
			if duplicateErr == nil {
				if differentParents(previous, record) {
					duplicateErr = &Error{Kind: ErrorMultipleParents, File: nodesName, Line: line, Pointer: "/parentId"}
				} else {
					duplicateErr = &Error{Kind: ErrorDuplicateNode, File: nodesName, Line: line, Pointer: "/id"}
				}
			}
			return nil
		}
		for index, fact := range facts {
			if _, exists := limitIDs[fact.ID]; exists {
				if duplicateErr == nil {
					duplicateErr = &Error{
						Kind: ErrorDuplicateLimit, File: nodesName, Line: line,
						Pointer: jsonPointer([]string{"limitFacts", strconv.Itoa(index), "id"}),
					}
				}
				continue
			}
			limitIDs[fact.ID] = struct{}{}
		}
		byID[id] = record
		orderedIDs = append(orderedIDs, id)
		return nil
	})
	return orderedIDs, byID, count, duplicateErr, err
}

func validateNodeLine(schema *jsonschema.Schema, line int, contents []byte) (string, nodeRecord, []limitFact, error) {
	value, err := decodeJSONLine(nodesName, line, contents)
	if err != nil {
		return "", nodeRecord{}, nil, err
	}
	var current nodeInput
	if err := remarshal(value, &current); err != nil {
		// 構造が想定と異なる行はSchemaが理由を説明できるため、そちらの結果を優先する。
		if schemaErr := schema.Validate(value); schemaErr != nil {
			return "", nodeRecord{}, nil, schemaError(nodesName, line, schemaErr)
		}
		return "", nodeRecord{}, nil, &Error{Kind: ErrorInvalidJSON, File: nodesName, Line: line, Err: err}
	}

	// リミットの所有者と親の有無はSchemaのif-thenでも表現しているが、
	// 取込元が原因を特定できるよう、汎用のschema_violationより具体的な理由コードを先に返す。
	// 種別自体が未知の場合は判定できないため、Schemaの結果に委ねる。
	if isNodeType(current.Type) && current.Type != "job" && len(current.LimitFacts) > 0 {
		return "", nodeRecord{}, nil, &Error{Kind: ErrorInvalidLimitOwner, File: nodesName, Line: line, Pointer: "/limitFacts"}
	}
	if isParentlessType(current.Type) && current.ParentID != nil {
		return "", nodeRecord{}, nil, &Error{Kind: ErrorInvalidParentType, File: nodesName, Line: line, Pointer: "/parentId"}
	}
	for index, fact := range current.LimitFacts {
		if fact.Kind == "finish_by" && fact.BusinessDayOffset != "" {
			if _, ok := jsonIntegerAsInt64(fact.BusinessDayOffset); !ok {
				return "", nodeRecord{}, nil, &Error{
					Kind: ErrorSchemaViolation, File: nodesName, Line: line,
					Pointer: jsonPointer([]string{"limitFacts", strconv.Itoa(index), "businessDayOffset"}),
				}
			}
		}
		if fact.Kind == "max_elapsed" {
			if _, ok := durationSeconds(fact.Duration); ok {
				continue
			}
			return "", nodeRecord{}, nil, &Error{Kind: ErrorInvalidDuration, File: nodesName, Line: line, Pointer: fmt.Sprintf("/limitFacts/%d/duration", index)}
		}
	}
	if err := schema.Validate(value); err != nil {
		return "", nodeRecord{}, nil, schemaError(nodesName, line, err)
	}

	record := nodeRecord{Type: nodeKindOf(current.Type), Line: line}
	if current.ParentID != nil {
		// 親なしをポインターで保持すると全ノード分の参照領域が増えるため、値と有無を分ける。
		record.ParentID = *current.ParentID
		record.HasParent = true
	}
	return current.ID, record, current.LimitFacts, nil
}

func readRelations(ctx context.Context, path string, schema *jsonschema.Schema, nodes map[string]nodeRecord) (int, error) {
	count := 0
	seen := make(map[[sha256.Size]byte]struct{})
	err := readNDJSON(ctx, path, relationsName, func(line int, contents []byte) error {
		count++
		value, err := decodeJSONLine(relationsName, line, contents)
		if err != nil {
			return err
		}
		if err := schema.Validate(value); err != nil {
			return schemaError(relationsName, line, err)
		}
		var current relation
		if err := remarshal(value, &current); err != nil {
			return &Error{Kind: ErrorInvalidJSON, File: relationsName, Line: line, Err: err}
		}
		if _, exists := nodes[current.FromID]; !exists {
			return &Error{Kind: ErrorMissingNode, File: relationsName, Line: line, Pointer: "/fromId"}
		}
		if _, exists := nodes[current.ToID]; !exists {
			return &Error{Kind: ErrorMissingNode, File: relationsName, Line: line, Pointer: "/toId"}
		}

		id, err := relationID(current)
		if err != nil {
			return &Error{Kind: ErrorInvalidJSON, File: relationsName, Line: line, Pointer: "/evidence", Err: err}
		}
		if _, duplicated := seen[id]; duplicated {
			return &Error{Kind: ErrorDuplicateRelation, File: relationsName, Line: line}
		}
		seen[id] = struct{}{}
		return nil
	})
	return count, err
}

func readNDJSON(ctx context.Context, path, name string, consume func(int, []byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return &Error{Kind: ErrorIO, File: name, Err: err}
	}
	defer file.Close()

	reader := bufio.NewReader(contextReader{ctx: ctx, reader: file})
	for line := 1; ; line++ {
		contents, readErr := readLine(reader, defaultArchiveLimits.ndjsonLine)
		if errors.Is(readErr, errLineTooLong) {
			return &Error{Kind: ErrorNDJSONLineLimit, File: name, Line: line}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return &Error{Kind: ErrorIO, File: name, Line: line, Err: ctxErr}
			}
			return &Error{Kind: ErrorIO, File: name, Line: line, Err: readErr}
		}
		if len(contents) == 0 && errors.Is(readErr, io.EOF) {
			return nil
		}
		contents = bytes.TrimSuffix(contents, []byte{'\n'})
		contents = bytes.TrimSuffix(contents, []byte{'\r'})
		if err := consume(line, contents); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func decodeJSONLine(name string, line int, contents []byte) (any, error) {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(contents))
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidJSON, File: name, Line: line, Err: err}
	}
	return value, nil
}

func remarshal(value any, target any) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(contents, target)
}

func schemaError(name string, line int, err error) *Error {
	pointer := ""
	var validationErr *jsonschema.ValidationError
	if errors.As(err, &validationErr) {
		leaf := stableValidationLeaf(validationErr)
		pointer = jsonPointer(leaf.InstanceLocation)
	}
	return &Error{Kind: ErrorSchemaViolation, File: name, Line: line, Pointer: pointer, Err: err}
}

func stableValidationLeaf(root *jsonschema.ValidationError) *jsonschema.ValidationError {
	leaves := make([]*jsonschema.ValidationError, 0)
	var visit func(*jsonschema.ValidationError)
	visit = func(current *jsonschema.ValidationError) {
		if len(current.Causes) == 0 {
			leaves = append(leaves, current)
			return
		}
		causes := current.Causes
		if strings.HasSuffix(current.BasicOutput().KeywordLocation, "/oneOf") {
			// 排他的な分岐の不一致は無関係な分岐の違反も含むため、違反葉が最少の分岐を原因箇所の選定に使う。
			causes = []*jsonschema.ValidationError{closestValidationCause(causes)}
		}
		for _, cause := range causes {
			visit(cause)
		}
	}
	visit(root)
	// ライブラリ内のcause順は公開された選定規則ではないため、入力位置、Schema位置、メッセージで全順序を作る。
	slices.SortFunc(leaves, compareValidationErrors)
	return leaves[0]
}

func closestValidationCause(causes []*jsonschema.ValidationError) *jsonschema.ValidationError {
	ordered := slices.Clone(causes)
	slices.SortFunc(ordered, func(left, right *jsonschema.ValidationError) int {
		if compared := len(validationLeaves(left)) - len(validationLeaves(right)); compared != 0 {
			return compared
		}
		return strings.Compare(validationCauseKey(left), validationCauseKey(right))
	})
	return ordered[0]
}

func validationCauseKey(root *jsonschema.ValidationError) string {
	leaves := validationLeaves(root)
	slices.SortFunc(leaves, compareValidationErrors)
	var key strings.Builder
	for _, leaf := range leaves {
		key.WriteString(jsonPointer(leaf.InstanceLocation))
		key.WriteByte(0)
		key.WriteString(leaf.BasicOutput().KeywordLocation)
		key.WriteByte(0)
		key.WriteString(leaf.Error())
		key.WriteByte(0)
	}
	return key.String()
}

func validationLeaves(root *jsonschema.ValidationError) []*jsonschema.ValidationError {
	leaves := make([]*jsonschema.ValidationError, 0)
	var visit func(*jsonschema.ValidationError)
	visit = func(current *jsonschema.ValidationError) {
		if len(current.Causes) == 0 {
			leaves = append(leaves, current)
			return
		}
		for _, cause := range current.Causes {
			visit(cause)
		}
	}
	visit(root)
	return leaves
}

func compareValidationErrors(left, right *jsonschema.ValidationError) int {
	if compared := strings.Compare(jsonPointer(left.InstanceLocation), jsonPointer(right.InstanceLocation)); compared != 0 {
		return compared
	}
	leftKeyword := left.BasicOutput().KeywordLocation
	rightKeyword := right.BasicOutput().KeywordLocation
	if compared := strings.Compare(leftKeyword, rightKeyword); compared != 0 {
		return compared
	}
	return strings.Compare(left.Error(), right.Error())
}

func jsonPointer(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	escaped := make([]string, len(parts))
	for index, part := range parts {
		part = strings.ReplaceAll(part, "~", "~0")
		escaped[index] = strings.ReplaceAll(part, "/", "~1")
	}
	return "/" + strings.Join(escaped, "/")
}

func isNodeType(kind string) bool {
	return nodeKindOf(kind) != nodeKindUnknown
}

func nodeKindOf(kind string) nodeKind {
	switch kind {
	case "management_unit":
		return nodeKindManagementUnit
	case "job_network":
		return nodeKindJobNetwork
	case "job":
		return nodeKindJob
	case "file":
		return nodeKindFile
	case "file_pattern":
		return nodeKindFilePattern
	case "job_status":
		return nodeKindJobStatus
	case "external_event":
		return nodeKindExternalEvent
	default:
		return nodeKindUnknown
	}
}

func isParentlessType(kind string) bool {
	switch nodeKindOf(kind) {
	case nodeKindFile, nodeKindFilePattern, nodeKindJobStatus, nodeKindExternalEvent:
		return true
	default:
		return false
	}
}

func differentParents(left, right nodeRecord) bool {
	if left.HasParent != right.HasParent {
		return true
	}
	return left.HasParent && left.ParentID != right.ParentID
}

func validateNodeReferences(orderedIDs []string, nodes map[string]nodeRecord, duplicateErr *Error) error {
	for _, id := range orderedIDs {
		current := nodes[id]
		if duplicateErr != nil && duplicateErr.Line < current.Line {
			return duplicateErr
		}
		if !current.HasParent {
			continue
		}
		parent, exists := nodes[current.ParentID]
		if !exists {
			return &Error{Kind: ErrorMissingParent, File: nodesName, Line: current.Line, Pointer: "/parentId"}
		}
		if !allowedParent(current.Type, parent.Type) {
			return &Error{Kind: ErrorInvalidParentType, File: nodesName, Line: current.Line, Pointer: "/parentId"}
		}
	}
	if duplicateErr != nil {
		return duplicateErr
	}
	return nil
}

func validateParentCycles(ctx context.Context, orderedIDs []string, nodes map[string]nodeRecord) error {
	state := make(map[string]uint8, len(nodes))
	for _, startID := range orderedIDs {
		if err := ctx.Err(); err != nil {
			return &Error{Kind: ErrorIO, File: nodesName, Err: err}
		}
		if state[startID] == 2 {
			continue
		}
		path := make([]string, 0)
		currentID := startID
		for {
			if err := ctx.Err(); err != nil {
				return &Error{Kind: ErrorIO, File: nodesName, Err: err}
			}
			current := nodes[currentID]
			if state[currentID] == 1 {
				firstCycleIndex := slices.Index(path, currentID)
				cycleLine := nodes[path[firstCycleIndex]].Line
				for _, id := range path[firstCycleIndex+1:] {
					cycleLine = min(cycleLine, nodes[id].Line)
				}
				return &Error{Kind: ErrorParentCycle, File: nodesName, Line: cycleLine, Pointer: "/parentId"}
			}
			if state[currentID] == 2 {
				break
			}
			state[currentID] = 1
			path = append(path, currentID)
			if !current.HasParent {
				break
			}
			currentID = current.ParentID
		}
		for _, id := range path {
			state[id] = 2
		}
	}
	return nil
}

func allowedParent(child, parent nodeKind) bool {
	switch child {
	case nodeKindManagementUnit:
		return parent == nodeKindManagementUnit
	case nodeKindJobNetwork:
		return parent == nodeKindManagementUnit || parent == nodeKindJobNetwork
	case nodeKindJob:
		return parent == nodeKindJobNetwork
	default:
		return false
	}
}

func durationSeconds(duration string) (int64, bool) {
	matches := durationComponents.FindStringSubmatch(duration)
	if matches == nil {
		return 0, false
	}
	if matches[1] == "" && matches[2] == "" && matches[3] == "" && matches[4] == "" && matches[5] == "" {
		return 0, false
	}
	if strings.Contains(duration, "T") && matches[3] == "" && matches[4] == "" && matches[5] == "" {
		return 0, false
	}

	for index := 1; index < len(matches); index++ {
		if !strings.ContainsAny(matches[index], ".,") {
			continue
		}
		for smaller := index + 1; smaller < len(matches); smaller++ {
			if matches[smaller] != "" {
				return 0, false
			}
		}
	}

	// 点とコンマはISO 8601で認められる小数記号として同一視し、二進浮動小数を介さず整数秒性を判定する。
	total := new(big.Rat)
	for index, seconds := range []int64{604800, 86400, 3600, 60, 1} {
		if matches[index+1] == "" {
			continue
		}
		component := strings.ReplaceAll(matches[index+1], ",", ".")
		value, ok := new(big.Rat).SetString(component)
		if !ok {
			return 0, false
		}
		value.Mul(value, new(big.Rat).SetInt64(seconds))
		total.Add(total, value)
	}
	if !total.IsInt() || !total.Num().IsInt64() {
		return 0, false
	}
	return total.Num().Int64(), true
}

func relationID(value relation) ([sha256.Size]byte, error) {
	evidence := any([]any{})
	if len(value.Evidence) > 0 {
		decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(value.Evidence))
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		evidence = decoded
	}

	// JSON配列で文字列の境界を保持し、objectのキーだけを辞書順へ正規化する。
	// evidence配列の順序は入力が表す提示順として区別し、欠落と空配列は同一視する。
	canonical, err := canonicalJSON([]any{value.FromID, value.ToID, value.Kind, value.Origin, value.Certainty, evidence})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func canonicalJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		output.WriteString(strconv.FormatBool(typed))
	case string:
		encoded, _ := json.Marshal(typed)
		output.Write(encoded)
	case json.Number:
		number, ok := new(big.Rat).SetString(typed.String())
		if !ok || !number.IsInt() {
			return fmt.Errorf("non-integer evidence number %q", typed)
		}
		output.WriteString(number.Num().String())
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			output.Write(encoded)
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}
