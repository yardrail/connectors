package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// EntityCategory classifies an entity by its API behavior.
type EntityCategory int

const (
	CategoryCRUD EntityCategory = iota
	CategoryReport
	CategoryQueryOnly
	CategorySpecial
)

// OperationType identifies a specific API operation.
type OperationType int

const (
	OpUnknown OperationType = iota
	OpCreate
	OpRead
	OpUpdate
	OpDelete
	OpVoid
	OpQuery
	OpPDF
	OpSend
)

// CorrelatedEntity is the fully-joined representation of one QBO entity.
type CorrelatedEntity struct {
	Name          string
	Category      EntityCategory
	Operations    []EntityOperation
	RequestModel  *Model
	ResponseModel *Model
	XSDType       *XSDComplexType
}

// EntityOperation describes one API operation for an entity.
type EntityOperation struct {
	Type        OperationType
	Method      string
	Path        string
	QueryParams map[string]string
	ExampleReq  string
	ExampleRes  string
}

// SchemaInfo pairs a model with its XSD type (if any).
type SchemaInfo struct {
	Model       *Model
	XSDType     *XSDComplexType
	DisplayName string // PascalCase name to use in the spec
}

// CorrelatedSpec is the complete result of joining all data sources.
type CorrelatedSpec struct {
	Entities    []*CorrelatedEntity
	Enums       map[string]*XSDEnum
	SharedTypes map[string]*SchemaInfo
	XSDTypes    map[string]*XSDComplexType
	// RefRewrites maps $ref display names from models JSON to canonical
	// schema names (preferring XSD PascalCase). E.g. "CashbackInfo" -> "CashBackInfo".
	RefRewrites map[string]string
}

// --- entity name normalization ---

var entityRemaps = map[string]string{
	"creditcardpaymenttxn": "creditcardpayment",
}

func normalizeEntityKey(name string) string {
	lower := strings.ToLower(name)
	if remap, ok := entityRemaps[lower]; ok {
		return remap
	}
	return lower
}

// --- index builders ---

type modelIndex struct {
	requests  map[string]*Model
	responses map[string]*Model
	shared    map[string]*Model
}

func indexModels(models map[string]*Model) *modelIndex {
	idx := &modelIndex{
		requests:  make(map[string]*Model),
		responses: make(map[string]*Model),
		shared:    make(map[string]*Model),
	}
	for key, model := range models {
		switch {
		case strings.HasSuffix(key, "request"):
			idx.requests[strings.TrimSuffix(key, "request")] = model
		case strings.HasSuffix(key, "response"):
			idx.responses[strings.TrimSuffix(key, "response")] = model
		default:
			idx.shared[key] = model
		}
	}
	return idx
}

func indexCodes(codes map[string]*EntityCodes) map[string]*EntityCodes {
	idx := make(map[string]*EntityCodes)
	for key, ec := range codes {
		idx[strings.ToLower(key)] = ec
	}
	return idx
}

// --- main correlation ---

func correlate(
	xsdEntities []XSDEntity,
	xsdTypes map[string]*XSDComplexType,
	enums map[string]*XSDEnum,
	models map[string]*Model,
	codes map[string]*EntityCodes,
) *CorrelatedSpec {
	mi := indexModels(models)
	ci := indexCodes(codes)

	spec := &CorrelatedSpec{
		Enums:       enums,
		SharedTypes: make(map[string]*SchemaInfo),
		XSDTypes:    xsdTypes,
	}

	seen := make(map[string]bool)

	// Pass 1: XSD entities — canonical CRUD entities
	for _, entity := range xsdEntities {
		key := normalizeEntityKey(entity.Name)
		seen[key] = true

		ce := &CorrelatedEntity{
			Name: entity.Name,
		}
		if xsdTypes != nil {
			ce.XSDType = xsdTypes[entity.Type]
		}

		if m, ok := mi.requests[key]; ok {
			ce.RequestModel = m
		}
		if m, ok := mi.responses[key]; ok {
			ce.ResponseModel = m
		}

		ec, hasCodes := ci[key]
		if !hasCodes {
			continue // desktop-only entity, skip
		}
		ce.Operations = deriveOperations(entity.Name, key, ec)
		ce.Category = classifyEntity(ec)

		spec.Entities = append(spec.Entities, ce)
	}

	// Pass 2: codes-only entities (reports, batch, CDC) — sorted for determinism
	codesKeys := make([]string, 0, len(ci))
	for k := range ci {
		codesKeys = append(codesKeys, k)
	}
	sort.Strings(codesKeys)
	for _, codesKey := range codesKeys {
		ec := ci[codesKey]
		if seen[codesKey] || codesKey == "zztemplate" {
			continue
		}
		ce := &CorrelatedEntity{
			Name:     inferDisplayName(codesKey),
			Category: classifyEntity(ec),
		}
		if m, ok := mi.requests[codesKey]; ok {
			ce.RequestModel = m
		}
		if m, ok := mi.responses[codesKey]; ok {
			ce.ResponseModel = m
		}
		ce.Operations = deriveOperations(ce.Name, codesKey, ec)
		spec.Entities = append(spec.Entities, ce)
	}

	// Sort entities for deterministic output
	sort.Slice(spec.Entities, func(i, j int) bool {
		return spec.Entities[i].Name < spec.Entities[j].Name
	})

	// Build a map from lowercase key -> PascalCase display name,
	// gathered from $ref entries across all models.
	refDisplayNames := make(map[string]string)
	for _, model := range models {
		for _, prop := range model.Properties {
			for _, ref := range prop.Refs {
				refDisplayNames[ref.Key] = ref.DisplayName
			}
		}
	}

	// Build lowercase -> XSD type lookup for matching shared types to XSD.
	xsdByLower := make(map[string]*XSDComplexType)
	if xsdTypes != nil {
		for name, ct := range xsdTypes {
			xsdByLower[strings.ToLower(name)] = ct
		}
	}

	// Pass 3: collect shared/reference models.
	// When a shared model has an XSD counterpart, prefer the XSD PascalCase
	// name to avoid duplicate schemas from casing mismatches.
	for key, model := range mi.shared {
		xsdType := xsdByLower[key]
		displayName := ""
		if xsdType != nil {
			displayName = xsdType.Name
		}
		if displayName == "" {
			displayName = refDisplayNames[key]
		}
		if displayName == "" {
			displayName = inferDisplayName(key)
		}
		spec.SharedTypes[key] = &SchemaInfo{
			Model:       model,
			XSDType:     xsdType,
			DisplayName: displayName,
		}
	}

	// Build ref rewrite map: models $ref DisplayName -> canonical schema name.
	spec.RefRewrites = make(map[string]string)
	for key, si := range spec.SharedTypes {
		refName := refDisplayNames[key]
		if refName != "" && refName != si.DisplayName {
			spec.RefRewrites[refName] = si.DisplayName
		}
	}

	return spec
}

func inferDisplayName(codesKey string) string {
	if len(codesKey) == 0 {
		return codesKey
	}
	return strings.ToUpper(codesKey[:1]) + codesKey[1:]
}

// --- operation derivation ---

var codeKeyPattern = regexp.MustCompile(`^([A-Za-z]+)(\d+)-(request|response)$`)

func parseCodeKey(key string) (OperationType, bool) {
	m := codeKeyPattern.FindStringSubmatch(key)
	if m == nil {
		return OpUnknown, false
	}
	isRequest := m[3] == "request"
	switch strings.ToLower(m[1]) {
	case "create":
		return OpCreate, isRequest
	case "read":
		return OpRead, isRequest
	case "update":
		return OpUpdate, isRequest
	case "delete":
		return OpDelete, isRequest
	case "void":
		return OpVoid, isRequest
	case "query":
		return OpQuery, isRequest
	case "send":
		return OpSend, isRequest
	case "pdf":
		return OpPDF, isRequest
	default:
		return OpUnknown, false
	}
}

func deriveOperations(displayName, pathName string, ec *EntityCodes) []EntityOperation {
	var ops []EntityOperation
	seenTypes := make(map[OperationType]bool)

	// Sort keys for deterministic output
	opKeys := make([]string, 0, len(ec.Operations))
	for k := range ec.Operations {
		opKeys = append(opKeys, k)
	}
	sort.Strings(opKeys)

	for _, opKey := range opKeys {
		payload := ec.Operations[opKey]
		if strings.HasSuffix(opKey, "-xml") {
			continue
		}

		opType, isRequest := parseCodeKey(opKey)
		if opType == OpUnknown || seenTypes[opType] {
			continue
		}
		seenTypes[opType] = true

		op := EntityOperation{
			Type:   opType,
			Method: operationMethod(opType),
			Path:   operationPath(opType, pathName),
		}

		if opType == OpDelete {
			op.QueryParams = map[string]string{"operation": "delete"}
		}
		if opType == OpVoid {
			if displayName == "SalesReceipt" || displayName == "Payment" {
				op.QueryParams = map[string]string{"operation": "update", "include": "void"}
			} else {
				op.QueryParams = map[string]string{"operation": "void"}
			}
		}

		if isRequest {
			op.ExampleReq = payload
			counterKey := strings.Replace(opKey, "-request", "-response", 1)
			if p, ok := ec.Operations[counterKey]; ok {
				op.ExampleRes = p
			}
		} else {
			op.ExampleRes = payload
			counterKey := strings.Replace(opKey, "-response", "-request", 1)
			if p, ok := ec.Operations[counterKey]; ok {
				op.ExampleReq = p
			}
		}

		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool {
		return ops[i].Type < ops[j].Type
	})
	return ops
}

func operationMethod(op OperationType) string {
	switch op {
	case OpRead, OpPDF, OpQuery:
		return "GET"
	default:
		return "POST"
	}
}

func operationPath(op OperationType, entityPath string) string {
	base := fmt.Sprintf("/v3/company/{realmId}/%s", entityPath)
	switch op {
	case OpRead:
		return base + "/{id}"
	case OpPDF:
		return base + "/{id}/pdf"
	case OpSend:
		return base + "/{id}/send"
	case OpQuery:
		return "/v3/company/{realmId}/query"
	default:
		return base
	}
}

func classifyEntity(ec *EntityCodes) EntityCategory {
	for opKey, payload := range ec.Operations {
		if !strings.Contains(opKey, "response") || strings.HasSuffix(opKey, "-xml") {
			continue
		}
		var parsed map[string]json.RawMessage
		if json.Unmarshal([]byte(payload), &parsed) == nil {
			_, hasHeader := parsed["Header"]
			_, hasRows := parsed["Rows"]
			if hasHeader && hasRows {
				return CategoryReport
			}
		}
	}

	for opKey := range ec.Operations {
		if strings.HasPrefix(opKey, "Create") || strings.HasPrefix(opKey, "Read") {
			return CategoryCRUD
		}
	}
	return CategoryQueryOnly
}
