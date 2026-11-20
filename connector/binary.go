package connector

// BinaryResult is the raw body of a response the spec marks as binary, with
// its content type.
type BinaryResult struct {
	Data        []byte
	ContentType string
}
