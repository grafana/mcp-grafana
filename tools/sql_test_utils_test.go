package tools

// getSQLSchemaFieldKeys extracts field keys as list
func getSQLSchemaFieldKeys(m map[string]*SQLSchemaField) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
