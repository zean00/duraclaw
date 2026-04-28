package knowledge

type Document struct {
	ID         string
	CustomerID string
	Text       string
	Metadata   map[string]any
}
