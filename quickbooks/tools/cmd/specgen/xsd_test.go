package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempXSD(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.xsd")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseServiceDef(t *testing.T) {
	t.Parallel()
	xsd := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="IntuitObject" type="IntuitEntity" abstract="true"/>
  <xs:element name="Invoice" substitutionGroup="IntuitObject" type="Invoice"/>
  <xs:element name="Account" substitutionGroup="IntuitObject" type="Account"/>
  <xs:element name="FixedAsset" substitutionGroup="IntuitObject" type="FixedAsset"/>
</xs:schema>`
	entities, err := parseServiceDef(writeTempXSD(t, xsd))
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 3 {
		t.Fatalf("got %d entities, want 3", len(entities))
	}
}

func TestParseServiceDefSkipsAbstract(t *testing.T) {
	t.Parallel()
	xsd := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="IntuitObject" type="IntuitEntity" abstract="true"/>
  <xs:element name="Transaction" substitutionGroup="IntuitObject" type="Transaction" abstract="true"/>
  <xs:element name="Invoice" substitutionGroup="IntuitObject" type="Invoice"/>
</xs:schema>`
	entities, err := parseServiceDef(writeTempXSD(t, xsd))
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Fatalf("got %d entities, want 1 (abstract should be skipped)", len(entities))
	}
	if entities[0].Name != "Invoice" {
		t.Errorf("entity = %q, want Invoice", entities[0].Name)
	}
}

func TestParseFinanceXSD_Inheritance(t *testing.T) {
	t.Parallel()
	finance := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="IntuitEntity">
    <xs:sequence>
      <xs:element name="Id" type="xs:string" minOccurs="0"/>
      <xs:element name="SyncToken" type="xs:string" minOccurs="0"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="Invoice">
    <xs:complexContent>
      <xs:extension base="IntuitEntity">
        <xs:sequence>
          <xs:element name="TotalAmt" type="xs:decimal" minOccurs="0"/>
          <xs:element name="Line" type="Line" minOccurs="0" maxOccurs="unbounded"/>
        </xs:sequence>
      </xs:extension>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	baseTypes := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"></xs:schema>`

	types, _, err := parseFinanceXSD(writeTempXSD(t, finance), writeTempXSD(t, baseTypes))
	if err != nil {
		t.Fatal(err)
	}
	inv := types["Invoice"]
	if inv == nil {
		t.Fatal("Invoice not found")
	}
	if inv.BaseName != "IntuitEntity" {
		t.Errorf("BaseName = %q, want IntuitEntity", inv.BaseName)
	}
	if len(inv.Properties) != 2 {
		t.Fatalf("got %d properties, want 2", len(inv.Properties))
	}
	line := inv.Properties[1]
	if line.Name != "Line" || line.MaxOccurs != -1 {
		t.Errorf("Line prop: name=%q maxOccurs=%d, want Line/-1", line.Name, line.MaxOccurs)
	}
}

func TestParseFinanceXSD_Enums(t *testing.T) {
	t.Parallel()
	finance := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:simpleType name="TxnTypeEnum">
    <xs:restriction base="xs:string">
      <xs:enumeration value="Invoice"/>
      <xs:enumeration value="Bill"/>
      <xs:enumeration value="Payment"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`
	baseTypes := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:simpleType name="MonthEnum">
    <xs:restriction base="xs:string">
      <xs:enumeration value="January"/>
      <xs:enumeration value="February"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`

	_, enums, err := parseFinanceXSD(writeTempXSD(t, finance), writeTempXSD(t, baseTypes))
	if err != nil {
		t.Fatal(err)
	}
	if len(enums) != 2 {
		t.Fatalf("got %d enums, want 2", len(enums))
	}
	txn := enums["TxnTypeEnum"]
	if len(txn.Values) != 3 {
		t.Errorf("TxnTypeEnum has %d values, want 3", len(txn.Values))
	}
	month := enums["MonthEnum"]
	if len(month.Values) != 2 {
		t.Errorf("MonthEnum has %d values, want 2", len(month.Values))
	}
}

func TestParseFinanceXSD_MergeFiles(t *testing.T) {
	t.Parallel()
	finance := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="Invoice">
    <xs:sequence>
      <xs:element name="TotalAmt" type="xs:decimal" minOccurs="0"/>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`
	baseTypes := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="IntuitEntity">
    <xs:sequence>
      <xs:element name="Id" type="xs:string" minOccurs="0"/>
    </xs:sequence>
  </xs:complexType>
  <xs:simpleType name="StatusEnum">
    <xs:restriction base="xs:string">
      <xs:enumeration value="Active"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`

	types, enums, err := parseFinanceXSD(writeTempXSD(t, finance), writeTempXSD(t, baseTypes))
	if err != nil {
		t.Fatal(err)
	}
	if types["Invoice"] == nil {
		t.Error("Invoice from finance.xsd should be present")
	}
	if types["IntuitEntity"] == nil {
		t.Error("IntuitEntity from baseTypes.xsd should be present")
	}
	if enums["StatusEnum"] == nil {
		t.Error("StatusEnum from baseTypes.xsd should be present")
	}
}

func TestParseFinanceXSD_UnboundedMaxOccurs(t *testing.T) {
	t.Parallel()
	finance := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="Container">
    <xs:sequence>
      <xs:element name="Single" type="xs:string" minOccurs="0" maxOccurs="1"/>
      <xs:element name="Multiple" type="xs:string" minOccurs="0" maxOccurs="unbounded"/>
      <xs:element name="Default" type="xs:string" minOccurs="0"/>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`
	baseTypes := `<?xml version="1.0" encoding="utf-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"></xs:schema>`

	types, _, err := parseFinanceXSD(writeTempXSD(t, finance), writeTempXSD(t, baseTypes))
	if err != nil {
		t.Fatal(err)
	}
	ct := types["Container"]
	if ct == nil {
		t.Fatal("Container not found")
	}

	tests := []struct {
		name      string
		wantMax   int
	}{
		{"Single", 1},
		{"Multiple", -1},
		{"Default", 1},
	}
	for _, tt := range tests {
		for _, p := range ct.Properties {
			if p.Name == tt.name {
				if p.MaxOccurs != tt.wantMax {
					t.Errorf("%s: MaxOccurs = %d, want %d", tt.name, p.MaxOccurs, tt.wantMax)
				}
			}
		}
	}
}
