package connector

// CWLClassEntry maps a CWL class IRI to its connector function metadata.
type CWLClassEntry struct {
	ClassIRI string
	Info     CWLClassInfo
}

// CWLClassInfo describes a single connector function as a CWL process class.
type CWLClassInfo struct {
	ConnectorID string
	Function    string
	ClassName   string
	Description string
	Inputs      []CWLFieldInfo
	Outputs     []CWLFieldInfo
}

// CWLFieldInfo describes a single input or output field of a CWL process class.
type CWLFieldInfo struct {
	Name        string
	Type        string
	Required    bool
	Description string
}
