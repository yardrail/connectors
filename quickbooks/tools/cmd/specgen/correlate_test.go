package main

import "testing"

func TestCorrelate_HappyPath(t *testing.T) {
	t.Parallel()

	entities := []XSDEntity{{Name: "Invoice", Type: "Invoice"}}
	xsdTypes := map[string]*XSDComplexType{
		"Invoice": {Name: "Invoice", BaseName: "IntuitEntity"},
	}
	enums := map[string]*XSDEnum{}
	models := map[string]*Model{
		"invoicerequest":  {Key: "invoicerequest"},
		"invoiceresponse": {Key: "invoiceresponse"},
	}
	codes := map[string]*EntityCodes{
		"Invoice": {EntityName: "Invoice", Operations: map[string]string{
			"Create0-request":  `{"Line":[]}`,
			"Create0-response": `{"Invoice":{},"time":"2024-01-01"}`,
			"Read0-response":   `{"Invoice":{},"time":"2024-01-01"}`,
		}},
	}

	spec := correlate(entities, xsdTypes, enums, models, codes)

	if len(spec.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(spec.Entities))
	}
	inv := spec.Entities[0]
	if inv.Name != "Invoice" {
		t.Errorf("name = %q, want Invoice", inv.Name)
	}
	if inv.RequestModel == nil || inv.ResponseModel == nil {
		t.Error("should have both request and response models")
	}
	if inv.Category != CategoryCRUD {
		t.Errorf("category = %v, want CRUD", inv.Category)
	}
}

func TestCorrelate_ResponseOnly(t *testing.T) {
	t.Parallel()

	entities := []XSDEntity{{Name: "TaxCode", Type: "TaxCode"}}
	xsdTypes := map[string]*XSDComplexType{
		"TaxCode": {Name: "TaxCode"},
	}
	models := map[string]*Model{
		"taxcoderesponse": {Key: "taxcoderesponse"},
	}
	codes := map[string]*EntityCodes{
		"Taxcode": {EntityName: "Taxcode", Operations: map[string]string{
			"Read0-response": `{"TaxCode":{}}`,
		}},
	}

	spec := correlate(entities, xsdTypes, map[string]*XSDEnum{}, models, codes)

	if len(spec.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(spec.Entities))
	}
	tc := spec.Entities[0]
	if tc.RequestModel != nil {
		t.Error("TaxCode should have no request model")
	}
	if tc.ResponseModel == nil {
		t.Error("TaxCode should have response model")
	}
}

func TestCorrelate_CreditCardRemap(t *testing.T) {
	t.Parallel()

	entities := []XSDEntity{{Name: "CreditCardPaymentTxn", Type: "CreditCardPaymentTxn"}}
	xsdTypes := map[string]*XSDComplexType{
		"CreditCardPaymentTxn": {Name: "CreditCardPaymentTxn"},
	}
	models := map[string]*Model{
		"creditcardpaymentrequest":  {Key: "creditcardpaymentrequest"},
		"creditcardpaymentresponse": {Key: "creditcardpaymentresponse"},
	}
	codes := map[string]*EntityCodes{
		"Creditcardpayment": {EntityName: "Creditcardpayment", Operations: map[string]string{
			"Read0-response": `{"CreditCardPaymentTxn":{},"time":"2024-01-01"}`,
		}},
	}

	spec := correlate(entities, xsdTypes, map[string]*XSDEnum{}, models, codes)

	if len(spec.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(spec.Entities))
	}
	if spec.Entities[0].RequestModel == nil {
		t.Error("CreditCardPaymentTxn should match creditcardpaymentrequest model")
	}
}

func TestCorrelate_DesktopOnlySkipped(t *testing.T) {
	t.Parallel()

	entities := []XSDEntity{
		{Name: "Invoice", Type: "Invoice"},
		{Name: "FixedAsset", Type: "FixedAsset"},
	}
	xsdTypes := map[string]*XSDComplexType{
		"Invoice":    {Name: "Invoice"},
		"FixedAsset": {Name: "FixedAsset"},
	}
	codes := map[string]*EntityCodes{
		"Invoice": {EntityName: "Invoice", Operations: map[string]string{
			"Read0-response": `{"Invoice":{}}`,
		}},
	}

	spec := correlate(entities, xsdTypes, map[string]*XSDEnum{}, map[string]*Model{}, codes)

	if len(spec.Entities) != 1 {
		t.Fatalf("got %d entities, want 1 (FixedAsset should be skipped)", len(spec.Entities))
	}
}

func TestCorrelate_ReportEntity(t *testing.T) {
	t.Parallel()

	codes := map[string]*EntityCodes{
		"Balancesheet": {EntityName: "Balancesheet", Operations: map[string]string{
			"Query0-response": `{"Header":{},"Rows":{},"Columns":{}}`,
		}},
	}

	spec := correlate(nil, nil, map[string]*XSDEnum{}, map[string]*Model{}, codes)

	if len(spec.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(spec.Entities))
	}
	if spec.Entities[0].Category != CategoryReport {
		t.Errorf("category = %v, want Report", spec.Entities[0].Category)
	}
}

func TestCorrelate_SharedModels(t *testing.T) {
	t.Parallel()

	models := map[string]*Model{
		"invoicerequest":  {Key: "invoicerequest"},
		"invoiceresponse": {Key: "invoiceresponse"},
		"referencetype":   {Key: "referencetype"},
		"physicaladdress": {Key: "physicaladdress"},
	}
	codes := map[string]*EntityCodes{
		"Invoice": {EntityName: "Invoice", Operations: map[string]string{
			"Read0-response": `{"Invoice":{}}`,
		}},
	}

	spec := correlate(
		[]XSDEntity{{Name: "Invoice", Type: "Invoice"}},
		map[string]*XSDComplexType{"Invoice": {Name: "Invoice"}},
		map[string]*XSDEnum{},
		models,
		codes,
	)

	if len(spec.SharedTypes) != 2 {
		t.Errorf("got %d shared types, want 2 (referencetype, physicaladdress)", len(spec.SharedTypes))
	}
	if _, ok := spec.SharedTypes["referencetype"]; !ok {
		t.Error("referencetype should be in SharedTypes")
	}
}

func TestDeriveOperations(t *testing.T) {
	t.Parallel()

	ec := &EntityCodes{Operations: map[string]string{
		"Create0-request":     `{}`,
		"Create0-response":    `{}`,
		"Read0-response":      `{}`,
		"Delete0-request":     `{}`,
		"Void0-request":       `{}`,
		"Send0-response":      `{}`,
		"PDF0-response":       `{}`,
		"Query0-request":      `select * from Invoice`,
		"Create0-request-xml": `<xml/>`,
		"Update1-request":     `{}`,
	}}

	ops := deriveOperations("Invoice", "invoice", ec)

	types := make(map[OperationType]bool)
	for _, op := range ops {
		types[op.Type] = true
	}

	for _, want := range []OperationType{OpCreate, OpRead, OpDelete, OpVoid, OpSend, OpPDF, OpQuery} {
		if !types[want] {
			t.Errorf("missing operation type %v", want)
		}
	}
}

func TestParseCodeKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key     string
		wantOp  OperationType
		wantReq bool
	}{
		{"Create0-request", OpCreate, true},
		{"Read0-response", OpRead, false},
		{"Delete0-request", OpDelete, true},
		{"Void0-request", OpVoid, true},
		{"Send0-response", OpSend, false},
		{"PDF0-response", OpPDF, false},
		{"Query0-request", OpQuery, true},
		{"Create0-request-xml", OpUnknown, false},
		{"garbage", OpUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			op, isReq := parseCodeKey(tt.key)
			if op != tt.wantOp || isReq != tt.wantReq {
				t.Errorf("parseCodeKey(%q) = (%v, %v), want (%v, %v)",
					tt.key, op, isReq, tt.wantOp, tt.wantReq)
			}
		})
	}
}
