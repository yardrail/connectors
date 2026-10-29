package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
)

// XSDEntity is a top-level entity from IntuitRestServiceDef.xsd.
type XSDEntity struct {
	Name string
	Type string
}

// XSDComplexType is a parsed complexType from Finance.xsd.
type XSDComplexType struct {
	Name       string
	BaseName   string // from xs:extension base (empty if no base)
	Properties []XSDProperty
}

// XSDProperty is a single element within a complexType sequence.
type XSDProperty struct {
	Name      string
	TypeName  string
	MinOccurs int
	MaxOccurs int // -1 = unbounded
	Doc       string
}

// XSDEnum is a simpleType with enumeration restriction.
type XSDEnum struct {
	Name   string
	Values []string
}

// --- raw XML parse structs ---

type xsdSchema struct {
	XMLName      xml.Name         `xml:"schema"`
	Elements     []xsdElement     `xml:"element"`
	ComplexTypes []xsdComplexType `xml:"complexType"`
	SimpleTypes  []xsdSimpleType  `xml:"simpleType"`
}

type xsdElement struct {
	Name              string         `xml:"name,attr"`
	Type              string         `xml:"type,attr"`
	SubstitutionGroup string         `xml:"substitutionGroup,attr"`
	MinOccurs         string         `xml:"minOccurs,attr"`
	MaxOccurs         string         `xml:"maxOccurs,attr"`
	Abstract          string         `xml:"abstract,attr"`
	Annotation        *xsdAnnotation `xml:"annotation"`
}

type xsdComplexType struct {
	Name           string             `xml:"name,attr"`
	Sequence       *xsdSequence       `xml:"sequence"`
	ComplexContent *xsdComplexContent `xml:"complexContent"`
}

type xsdComplexContent struct {
	Extension *xsdExtension `xml:"extension"`
}

type xsdExtension struct {
	Base     string       `xml:"base,attr"`
	Sequence *xsdSequence `xml:"sequence"`
}

type xsdSequence struct {
	Elements []xsdElement `xml:"element"`
}

type xsdAnnotation struct {
	Documentation string `xml:"documentation"`
}

type xsdSimpleType struct {
	Name        string          `xml:"name,attr"`
	Restriction *xsdRestriction `xml:"restriction"`
}

type xsdRestriction struct {
	Base         string           `xml:"base,attr"`
	Enumerations []xsdEnumeration `xml:"enumeration"`
}

type xsdEnumeration struct {
	Value string `xml:"value,attr"`
}

func parseServiceDef(path string) ([]XSDEntity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var schema xsdSchema
	if err := xml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	var entities []XSDEntity
	for _, el := range schema.Elements {
		if el.SubstitutionGroup == "IntuitObject" && el.Abstract != "true" {
			entities = append(entities, XSDEntity{Name: el.Name, Type: el.Type})
		}
	}
	return entities, nil
}

func parseFinanceXSD(financePath, baseTypesPath string) (map[string]*XSDComplexType, map[string]*XSDEnum, error) {
	types := make(map[string]*XSDComplexType)
	enums := make(map[string]*XSDEnum)

	for _, path := range []string{financePath, baseTypesPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		var schema xsdSchema
		if err := xml.Unmarshal(data, &schema); err != nil {
			return nil, nil, fmt.Errorf("unmarshal %s: %w", path, err)
		}

		for _, ct := range schema.ComplexTypes {
			parsed := &XSDComplexType{Name: ct.Name}

			var elems []xsdElement
			if ct.ComplexContent != nil && ct.ComplexContent.Extension != nil {
				parsed.BaseName = ct.ComplexContent.Extension.Base
				if ct.ComplexContent.Extension.Sequence != nil {
					elems = ct.ComplexContent.Extension.Sequence.Elements
				}
			} else if ct.Sequence != nil {
				elems = ct.Sequence.Elements
			}

			for _, el := range elems {
				prop := XSDProperty{
					Name:     el.Name,
					TypeName: el.Type,
				}
				prop.MinOccurs, _ = strconv.Atoi(el.MinOccurs)
				if el.MaxOccurs == "unbounded" {
					prop.MaxOccurs = -1
				} else if el.MaxOccurs == "" {
					prop.MaxOccurs = 1
				} else {
					prop.MaxOccurs, _ = strconv.Atoi(el.MaxOccurs)
				}
				if el.Annotation != nil {
					prop.Doc = el.Annotation.Documentation
				}
				parsed.Properties = append(parsed.Properties, prop)
			}
			types[ct.Name] = parsed
		}

		for _, st := range schema.SimpleTypes {
			if st.Restriction == nil || len(st.Restriction.Enumerations) == 0 {
				continue
			}
			e := &XSDEnum{Name: st.Name}
			for _, en := range st.Restriction.Enumerations {
				e.Values = append(e.Values, en.Value)
			}
			enums[st.Name] = e
		}
	}

	return types, enums, nil
}
