package snapshot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	embeddedschema "batchscope/schema"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBaseURL = "https://batchscope.invalid/schema/"

var (
	compiledSchemasOnce sync.Once
	compiledSchemas     snapshotSchemas
	compiledSchemasErr  error
	integerDuration     = regexp.MustCompile(`^P(?:(\d+)W|(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?)$`)
)

type snapshotSchemas struct {
	manifest *jsonschema.Schema
	node     *jsonschema.Schema
	relation *jsonschema.Schema
}

type manifest struct {
	NodeCount     int `json:"nodeCount"`
	RelationCount int `json:"relationCount"`
}

type node struct {
	Type       string      `json:"type"`
	ID         string      `json:"id"`
	ParentID   *string     `json:"parentId"`
	LimitFacts []limitFact `json:"limitFacts"`
	Line       int         `json:"-"`
}

type limitFact struct {
	Kind     string `json:"kind"`
	Duration string `json:"duration"`
}

type relation struct {
	FromID    string          `json:"fromId"`
	ToID      string          `json:"toId"`
	Kind      string          `json:"kind"`
	Origin    string          `json:"origin"`
	Certainty string          `json:"certainty"`
	Evidence  json.RawMessage `json:"evidence"`
}

// ValidatedRelation は、relations.ndjsonの一行と、その内容から生成したIDを結び付ける。
type ValidatedRelation struct {
	Line int
	ID   string
}

// ValidationResult は、検査後のSQLite作成で再利用する値を保持する。
// Relationsの順序はrelations.ndjsonの行順と一致する。
type ValidationResult struct {
	Relations []ValidatedRelation
}

// Validate は、展開済みスナップショットの形式と参照整合性を検査する。
// 最初のエラーで停止するため、同じ入力では常にファイル順と行順が最も早いエラーを返す。
func Validate(ctx context.Context, extracted Extracted) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, &Error{Kind: ErrorIO, Err: err}
	}

	schemas, err := getSchemas()
	if err != nil {
		return ValidationResult{}, &Error{Kind: ErrorIO, Err: err}
	}

	manifestValue, err := readJSON(ctx, extracted.Manifest, manifestName, schemas.manifest)
	if err != nil {
		return ValidationResult{}, err
	}
	var metadata manifest
	if err := remarshal(manifestValue, &metadata); err != nil {
		return ValidationResult{}, &Error{Kind: ErrorInvalidJSON, File: manifestName, Err: err}
	}

	nodes, nodeByID, err := readNodes(ctx, extracted.Nodes, schemas.node)
	if err != nil {
		return ValidationResult{}, err
	}
	if metadata.NodeCount != len(nodes) {
		return ValidationResult{}, &Error{Kind: ErrorNodeCountMismatch, File: manifestName, Pointer: "/nodeCount"}
	}
	if err := validateParents(ctx, nodes, nodeByID); err != nil {
		return ValidationResult{}, err
	}

	validatedRelations, err := readRelations(ctx, extracted.Relations, schemas.relation, nodeByID)
	if err != nil {
		return ValidationResult{}, err
	}
	if metadata.RelationCount != len(validatedRelations) {
		return ValidationResult{}, &Error{Kind: ErrorRelationCountMismatch, File: manifestName, Pointer: "/relationCount"}
	}

	return ValidationResult{Relations: validatedRelations}, nil
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

func readJSON(ctx context.Context, path, name string, schema *jsonschema.Schema) (any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, &Error{Kind: ErrorIO, File: name, Err: err}
	}
	defer file.Close()

	value, err := jsonschema.UnmarshalJSON(contextReader{ctx: ctx, reader: file})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, &Error{Kind: ErrorIO, File: name, Err: ctxErr}
		}
		return nil, &Error{Kind: ErrorInvalidJSON, File: name, Err: err}
	}
	if err := schema.Validate(value); err != nil {
		return nil, schemaError(name, 0, err)
	}
	return value, nil
}

func readNodes(ctx context.Context, path string, schema *jsonschema.Schema) ([]node, map[string]node, error) {
	nodes := make([]node, 0)
	byID := make(map[string]node)
	err := readNDJSON(ctx, path, nodesName, func(line int, contents []byte) error {
		value, err := decodeJSONLine(nodesName, line, contents)
		if err != nil {
			return err
		}
		var current node
		if err := remarshal(value, &current); err != nil {
			// 構造が想定と異なる行はSchemaが理由を説明できるため、そちらの結果を優先する。
			if schemaErr := schema.Validate(value); schemaErr != nil {
				return schemaError(nodesName, line, schemaErr)
			}
			return &Error{Kind: ErrorInvalidJSON, File: nodesName, Line: line, Err: err}
		}

		// リミットの所有者と親の有無はSchemaのif-thenでも表現しているが、
		// 取込元が原因を特定できるよう、汎用のschema_violationより具体的な理由コードを先に返す。
		// 種別自体が未知の場合は判定できないため、Schemaの結果に委ねる。
		if isNodeType(current.Type) && current.Type != "job" && len(current.LimitFacts) > 0 {
			return &Error{Kind: ErrorInvalidLimitOwner, File: nodesName, Line: line, Pointer: "/limitFacts"}
		}
		if isParentlessType(current.Type) && current.ParentID != nil {
			return &Error{Kind: ErrorInvalidParentType, File: nodesName, Line: line, Pointer: "/parentId"}
		}
		if err := schema.Validate(value); err != nil {
			return schemaError(nodesName, line, err)
		}
		for index, fact := range current.LimitFacts {
			if fact.Kind == "max_elapsed" && !validDurationSeconds(fact.Duration) {
				return &Error{Kind: ErrorInvalidDuration, File: nodesName, Line: line, Pointer: fmt.Sprintf("/limitFacts/%d/duration", index)}
			}
		}

		current.Line = line
		if previous, exists := byID[current.ID]; exists {
			if differentParents(previous.ParentID, current.ParentID) {
				return &Error{Kind: ErrorMultipleParents, File: nodesName, Line: line, Pointer: "/parentId"}
			}
			return &Error{Kind: ErrorDuplicateNode, File: nodesName, Line: line, Pointer: "/id"}
		}
		byID[current.ID] = current
		nodes = append(nodes, current)
		return nil
	})
	return nodes, byID, err
}

func readRelations(ctx context.Context, path string, schema *jsonschema.Schema, nodes map[string]node) ([]ValidatedRelation, error) {
	relations := make([]ValidatedRelation, 0)
	seen := make(map[string]struct{})
	err := readNDJSON(ctx, path, relationsName, func(line int, contents []byte) error {
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
		relations = append(relations, ValidatedRelation{Line: line, ID: id})
		return nil
	})
	return relations, err
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
		leaf := deepestValidationError(validationErr)
		pointer = jsonPointer(leaf.InstanceLocation)
	}
	return &Error{Kind: ErrorSchemaViolation, File: name, Line: line, Pointer: pointer, Err: err}
}

func deepestValidationError(root *jsonschema.ValidationError) *jsonschema.ValidationError {
	deepest := root
	var visit func(*jsonschema.ValidationError)
	visit = func(current *jsonschema.ValidationError) {
		if len(current.InstanceLocation) > len(deepest.InstanceLocation) {
			deepest = current
		}
		for _, cause := range current.Causes {
			visit(cause)
		}
	}
	visit(root)
	return deepest
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
	switch kind {
	case "management_unit", "job_network", "job", "file", "file_pattern", "job_status", "external_event":
		return true
	default:
		return false
	}
}

func isParentlessType(kind string) bool {
	switch kind {
	case "file", "file_pattern", "job_status", "external_event":
		return true
	default:
		return false
	}
}

func differentParents(left, right *string) bool {
	if left == nil || right == nil {
		return left != right
	}
	return *left != *right
}

func validateParents(ctx context.Context, ordered []node, nodes map[string]node) error {
	for _, current := range ordered {
		if current.ParentID == nil {
			continue
		}
		parent, exists := nodes[*current.ParentID]
		if !exists {
			return &Error{Kind: ErrorMissingParent, File: nodesName, Line: current.Line, Pointer: "/parentId"}
		}
		if !allowedParent(current.Type, parent.Type) {
			return &Error{Kind: ErrorInvalidParentType, File: nodesName, Line: current.Line, Pointer: "/parentId"}
		}
	}

	state := make(map[string]uint8, len(nodes))
	for _, start := range ordered {
		if err := ctx.Err(); err != nil {
			return &Error{Kind: ErrorIO, File: nodesName, Err: err}
		}
		if state[start.ID] == 2 {
			continue
		}
		path := make([]string, 0)
		current := start
		for {
			if err := ctx.Err(); err != nil {
				return &Error{Kind: ErrorIO, File: nodesName, Err: err}
			}
			if state[current.ID] == 1 {
				return &Error{Kind: ErrorParentCycle, File: nodesName, Line: current.Line, Pointer: "/parentId"}
			}
			if state[current.ID] == 2 {
				break
			}
			state[current.ID] = 1
			path = append(path, current.ID)
			if current.ParentID == nil {
				break
			}
			current = nodes[*current.ParentID]
		}
		for _, id := range path {
			state[id] = 2
		}
	}
	return nil
}

func allowedParent(child, parent string) bool {
	switch child {
	case "management_unit":
		return parent == "management_unit"
	case "job_network":
		return parent == "management_unit" || parent == "job_network"
	case "job":
		return parent == "job_network"
	default:
		return false
	}
}

func validDurationSeconds(duration string) bool {
	matches := integerDuration.FindStringSubmatch(duration)
	if matches == nil {
		return false
	}
	if matches[1] == "" && matches[2] == "" && matches[3] == "" && matches[4] == "" && matches[5] == "" {
		return false
	}
	if strings.Contains(duration, "T") && matches[3] == "" && matches[4] == "" && matches[5] == "" {
		return false
	}

	// 週は暦に依存せず常に7日として秒へ変換できるため受け入れる。
	// ISO 8601どおり他の単位とは混在させず、SQLiteの整数秒に収まる値だけを許可する。
	total := new(big.Int)
	for index, seconds := range []int64{604800, 86400, 3600, 60, 1} {
		if matches[index+1] == "" {
			continue
		}
		value := new(big.Int)
		if _, ok := value.SetString(matches[index+1], 10); !ok {
			return false
		}
		value.Mul(value, big.NewInt(seconds))
		total.Add(total, value)
	}
	return total.IsInt64()
}

func relationID(value relation) (string, error) {
	evidence := any([]any{})
	if len(value.Evidence) > 0 {
		decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(value.Evidence))
		if err != nil {
			return "", err
		}
		evidence = decoded
	}

	// JSON配列で文字列の境界を保持し、objectのキーだけを辞書順へ正規化する。
	// evidence配列の順序は入力が表す提示順として区別し、欠落と空配列は同一視する。
	canonical, err := canonicalJSON([]any{value.FromID, value.ToID, value.Kind, value.Origin, value.Certainty, evidence})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
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
