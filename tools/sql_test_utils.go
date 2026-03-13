package tools

// getSQLSchemaFieldKeys Helper to extract keys from schema fields map for Subset assertions
func getSQLSchemaFieldKeys(m map[string]*SQLSchemaField) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
