package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata/golden")

func TestBuildOpenAPI_EnumSchema(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Enums: map[string]*XSDEnum{
			"StatusEnum": {Name: "StatusEnum", Values: []string{"Active", "Inactive"}},
		},
		SharedTypes: make(map[string]*SchemaInfo),
	}
	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}
	s := doc.Components.Schemas["StatusEnum"]
	if s == nil {
		t.Fatal("StatusEnum schema missing")
	}
	if len(s.Value.Enum) != 2 {
		t.Errorf("enum has %d values, want 2", len(s.Value.Enum))
	}
}

func TestBuildOpenAPI_EntityPaths(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Entities: []*CorrelatedEntity{{
			Name:     "Invoice",
			Category: CategoryCRUD,
			ResponseModel: &Model{
				Key:        "invoiceresponse",
				Properties: map[string]*ModelProperty{},
			},
			Operations: []EntityOperation{
				{Type: OpCreate, Method: "POST", Path: "/v3/company/{realmId}/invoice"},
				{Type: OpRead, Method: "GET", Path: "/v3/company/{realmId}/invoice/{id}"},
			},
		}},
		Enums:       map[string]*XSDEnum{},
		SharedTypes: make(map[string]*SchemaInfo),
	}

	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	postPath := doc.Paths.Value("/v3/company/{realmId}/invoice")
	if postPath == nil || postPath.Post == nil {
		t.Fatal("POST /invoice path missing")
	}
	if postPath.Post.OperationID != "createInvoice" {
		t.Errorf("operationId = %q, want createInvoice", postPath.Post.OperationID)
	}

	getPath := doc.Paths.Value("/v3/company/{realmId}/invoice/{id}")
	if getPath == nil || getPath.Get == nil {
		t.Fatal("GET /invoice/{id} path missing")
	}
	if getPath.Get.OperationID != "readInvoice" {
		t.Errorf("operationId = %q, want readInvoice", getPath.Get.OperationID)
	}
}

func TestBuildOpenAPI_SharedEnvelopes(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Enums:       map[string]*XSDEnum{},
		SharedTypes: make(map[string]*SchemaInfo),
	}
	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"QueryResponseEnvelope", "ReportEnvelope", "FaultEnvelope"} {
		if doc.Components.Schemas[name] == nil {
			t.Errorf("missing shared envelope schema %q", name)
		}
	}
}

func TestBuildOpenAPI_SecurityScheme(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Enums:       map[string]*XSDEnum{},
		SharedTypes: make(map[string]*SchemaInfo),
	}
	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	oauth := doc.Components.SecuritySchemes["OAuth2"]
	if oauth == nil || oauth.Value == nil {
		t.Fatal("OAuth2 security scheme missing")
	}
	if oauth.Value.Type != "oauth2" {
		t.Errorf("type = %q, want oauth2", oauth.Value.Type)
	}
}

func TestBuildOpenAPI_Serializable(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Entities: []*CorrelatedEntity{{
			Name:     "Account",
			Category: CategoryCRUD,
			RequestModel: &Model{
				Key: "accountrequest",
				Properties: map[string]*ModelProperty{
					"Name": {Name: "Name", Type: "String"},
				},
				Required: []string{"Name"},
			},
			ResponseModel: &Model{
				Key: "accountresponse",
				Properties: map[string]*ModelProperty{
					"Id":   {Name: "Id", Type: "String", ReadOnly: true},
					"Name": {Name: "Name", Type: "String"},
				},
			},
			Operations: []EntityOperation{
				{Type: OpCreate, Method: "POST", Path: "/v3/company/{realmId}/account"},
				{Type: OpRead, Method: "GET", Path: "/v3/company/{realmId}/account/{id}"},
			},
		}},
		Enums:       map[string]*XSDEnum{},
		SharedTypes: make(map[string]*SchemaInfo),
	}

	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty output")
	}
}

func TestBuildOpenAPI_AllOfInheritance(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Enums:       map[string]*XSDEnum{},
		SharedTypes: make(map[string]*SchemaInfo),
		XSDTypes: map[string]*XSDComplexType{
			"IntuitEntity": {
				Name: "IntuitEntity",
				Properties: []XSDProperty{
					{Name: "Id", TypeName: "xs:string", MaxOccurs: 1},
					{Name: "SyncToken", TypeName: "xs:string", MaxOccurs: 1},
				},
			},
			"Invoice": {
				Name:     "Invoice",
				BaseName: "IntuitEntity",
				Properties: []XSDProperty{
					{Name: "TotalAmt", TypeName: "xs:decimal", MaxOccurs: 1},
				},
			},
		},
	}

	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	inv := doc.Components.Schemas["Invoice"]
	if inv == nil {
		t.Fatal("Invoice schema missing")
	}
	if inv.Value.AllOf == nil {
		t.Fatal("Invoice should use allOf for inheritance")
	}
	if len(inv.Value.AllOf) != 2 {
		t.Fatalf("allOf has %d entries, want 2", len(inv.Value.AllOf))
	}
	if inv.Value.AllOf[0].Ref != "#/components/schemas/IntuitEntity" {
		t.Errorf("allOf[0] ref = %q, want IntuitEntity ref", inv.Value.AllOf[0].Ref)
	}
	extension := inv.Value.AllOf[1].Value
	if extension == nil {
		t.Fatal("allOf[1] should be inline schema")
	}
	if _, ok := extension.Properties["TotalAmt"]; !ok {
		t.Error("extension should have TotalAmt property")
	}
}

func TestBuildOpenAPI_PolymorphicRef(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Entities: []*CorrelatedEntity{{
			Name:     "Invoice",
			Category: CategoryCRUD,
			ResponseModel: &Model{
				Key: "invoiceresponse",
				Properties: map[string]*ModelProperty{
					"Line": {
						Name:    "Line",
						Type:    "Invoice line object",
						IsArray: true,
						Refs: []ModelRef{
							{Key: "salesitemline", DisplayName: "SalesItemLine"},
							{Key: "groupline", DisplayName: "GroupLine"},
							{Key: "descriptiononlyline", DisplayName: "DescriptionOnlyLine"},
						},
					},
				},
			},
			Operations: []EntityOperation{
				{Type: OpRead, Method: "GET", Path: "/v3/company/{realmId}/invoice/{id}"},
			},
		}},
		Enums:       map[string]*XSDEnum{},
		SharedTypes: make(map[string]*SchemaInfo),
	}

	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	inv := doc.Components.Schemas["Invoice"]
	if inv == nil {
		t.Fatal("Invoice schema missing")
	}
	lineProp := inv.Value.Properties["Line"]
	if lineProp == nil {
		t.Fatal("Line property missing")
	}

	data, _ := json.Marshal(lineProp)
	s := string(data)

	if !strings.Contains(s, "oneOf") {
		t.Error("Line should contain oneOf for polymorphic refs")
	}
	if !strings.Contains(s, "array") {
		t.Error("Line should be array type")
	}
	if !strings.Contains(s, "SalesItemLine") {
		t.Error("Line should reference SalesItemLine")
	}
}

func TestBuildOpenAPI_Golden(t *testing.T) {
	t.Parallel()

	spec := &CorrelatedSpec{
		Entities: []*CorrelatedEntity{{
			Name:     "Account",
			Category: CategoryCRUD,
			RequestModel: &Model{
				Key: "accountrequest",
				Properties: map[string]*ModelProperty{
					"Name":        {Name: "Name", Type: "String", RequiredFlag: "Required"},
					"AccountType": {Name: "AccountType", Type: "AccountTypeEnum"},
				},
				Required: []string{"Name", "AccountType"},
			},
			ResponseModel: &Model{
				Key: "accountresponse",
				Properties: map[string]*ModelProperty{
					"Id":   {Name: "Id", Type: "String", ReadOnly: true},
					"Name": {Name: "Name", Type: "String"},
				},
			},
			Operations: []EntityOperation{
				{Type: OpCreate, Method: "POST", Path: "/v3/company/{realmId}/account"},
				{Type: OpRead, Method: "GET", Path: "/v3/company/{realmId}/account/{id}"},
			},
		}},
		Enums: map[string]*XSDEnum{
			"AccountTypeEnum": {Name: "AccountTypeEnum", Values: []string{"Bank", "CreditCard", "Expense"}},
		},
		SharedTypes: make(map[string]*SchemaInfo),
	}

	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatal(err)
	}

	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "golden", "account_only.json")

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("output differs from golden (run with -update to accept)")
	}
}

func TestBuildOpenAPI_FullPipeline(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	serviceDef := filepath.Join(root, "quickbooks/spec/IntuitRestServiceDef.xsd")
	finance := filepath.Join(root, "quickbooks/spec/Finance.xsd")
	baseTypes := filepath.Join(root, "quickbooks/spec/IntuitBaseTypes.xsd")
	codesModels := filepath.Join(root, "quickbooks/spec/CodesModelsJsonObjects_v2.json")

	if _, err := os.Stat(serviceDef); os.IsNotExist(err) {
		t.Skip("vendored spec files not present")
	}

	entities, err := parseServiceDef(serviceDef)
	if err != nil {
		t.Fatalf("parseServiceDef: %v", err)
	}
	xsdTypes, enums, err := parseFinanceXSD(finance, baseTypes)
	if err != nil {
		t.Fatalf("parseFinanceXSD: %v", err)
	}
	models, codes, err := parseCodesModels(codesModels)
	if err != nil {
		t.Fatalf("parseCodesModels: %v", err)
	}

	spec := correlate(entities, xsdTypes, enums, models, codes)
	doc, err := buildOpenAPI(spec)
	if err != nil {
		t.Fatalf("buildOpenAPI: %v", err)
	}

	// Verify serializable
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	// Structural assertions on the full pipeline output
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("re-parse: %v", err)
	}

	if parsed["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", parsed["openapi"])
	}

	paths, _ := parsed["paths"].(map[string]any)
	if len(paths) < 80 {
		t.Errorf("paths = %d, want >= 80", len(paths))
	}

	components, _ := parsed["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if len(schemas) < 400 {
		t.Errorf("schemas = %d, want >= 400", len(schemas))
	}

	// Verify specific entities exist
	for _, name := range []string{"Invoice", "Account", "Customer", "Bill", "Payment", "Vendor"} {
		if schemas[name] == nil {
			t.Errorf("missing schema %q", name)
		}
		if schemas[name+"Envelope"] == nil {
			t.Errorf("missing schema %qEnvelope", name)
		}
		if schemas[name+"CreateRequest"] == nil {
			t.Errorf("missing schema %qCreateRequest", name)
		}
	}

	// Verify enums have values
	enumCount := 0
	for _, v := range schemas {
		s, _ := v.(map[string]any)
		if s["enum"] != nil {
			enumCount++
		}
	}
	if enumCount < 80 {
		t.Errorf("enum schemas = %d, want >= 80", enumCount)
	}

	// Verify Invoice paths exist
	if paths["/v3/company/{realmId}/invoice"] == nil {
		t.Error("missing POST /invoice path")
	}
	if paths["/v3/company/{realmId}/invoice/{id}"] == nil {
		t.Error("missing GET /invoice/{id} path")
	}
}

