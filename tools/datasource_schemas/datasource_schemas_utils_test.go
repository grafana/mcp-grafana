//go:build unit

package datasourceschemas

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fieldKeys extracts the Key values from a field slice for easy assertion.
func fieldKeys(fields []GuidanceField) []string {
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.Key
	}
	return keys
}

func TestSchemaFieldInputKey(t *testing.T) {
	tests := []struct {
		name  string
		field DsSchemaField
		want  string
	}{
		{
			name:  "no section uses key directly",
			field: DsSchemaField{Key: "httpMethod"},
			want:  "httpMethod",
		},
		{
			name:  "with section prepends section",
			field: DsSchemaField{Key: "region", Section: "aws"},
			want:  "aws.region",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SchemaFieldInputKey(tc.field))
		})
	}
}

func TestLoadDatasourceSchema(t *testing.T) {
	t.Run("loads an embedded schema by plugin type", func(t *testing.T) {
		// Pick one schema file dynamically to verify the load mechanism without
		// enumerating every file in the directory.
		entries, err := datasourceSchemaFiles.ReadDir(".")
		require.NoError(t, err)
		require.NotEmpty(t, entries, "no schema files embedded — add at least one *_schema.json")

		pluginType := strings.TrimSuffix(entries[0].Name(), "_schema.json")
		schema, err := LoadDatasourceSchema(pluginType)
		require.NoError(t, err)
		require.NotNil(t, schema)
		assert.Equal(t, pluginType, schema.PluginType)
		assert.NotEmpty(t, schema.Fields)
	})

	t.Run("unknown type returns nil without error", func(t *testing.T) {
		schema, err := LoadDatasourceSchema("nonexistent-plugin")
		require.NoError(t, err)
		assert.Nil(t, schema)
	})
}

func TestBuildSchemaGuidance(t *testing.T) {
	t.Run("includes common root fields", func(t *testing.T) {
		schema := &DatasourceSchema{PluginType: "test-plugin", PluginName: "Test"}
		guidance := BuildSchemaGuidance(schema, "create_datasource")
		keys := fieldKeys(guidance.Fields)
		assert.Contains(t, keys, "uid")
		assert.Contains(t, keys, "isDefault")
	})

	t.Run("sets type, plugin_name, doc_url, and message", func(t *testing.T) {
		schema := &DatasourceSchema{
			PluginType: "test-plugin",
			PluginName: "Test Plugin",
			DocURL:     "https://example.com/docs",
		}
		guidance := BuildSchemaGuidance(schema, "create_datasource")
		assert.Equal(t, "test-plugin", guidance.Type)
		assert.Equal(t, "Test Plugin", guidance.PluginName)
		assert.Equal(t, "https://example.com/docs", guidance.DocURL)
		assert.Contains(t, guidance.Message, "Test Plugin")
	})

	t.Run("skips virtual fields", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{Key: "visible", Target: "jsonData", ValueType: "string"},
			{Key: "hidden", Target: "jsonData", ValueType: "string", Kind: "virtual"},
		}}
		keys := fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields)
		assert.Contains(t, keys, "visible")
		assert.NotContains(t, keys, "hidden")
	})

	t.Run("skips secureJsonData fields", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{Key: "apiKey", Target: "secureJsonData", ValueType: "string"},
		}}
		assert.NotContains(t, fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields), "apiKey")
	})

	t.Run("skips excluded PII/credential fields", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{ID: "root.user", Key: "user", Target: "root", ValueType: "string", Required: true},
			{ID: "root.basicAuthUser", Key: "basicAuthUser", Target: "root", ValueType: "string"},
			{ID: "jsonData.user", Key: "user", Target: "jsonData", ValueType: "string", Required: true},
			{ID: "jsonData.username", Key: "username", Target: "jsonData", ValueType: "string"},
		}}
		keys := fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields)
		assert.NotContains(t, keys, "user")
		assert.NotContains(t, keys, "basicAuthUser")
		assert.NotContains(t, keys, "username")
	})

	t.Run("skips experimental lifecycle fields", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{Key: "beta", Target: "jsonData", ValueType: "string", Lifecycle: "experimental"},
			{Key: "stable", Target: "jsonData", ValueType: "string"},
		}}
		keys := fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields)
		assert.NotContains(t, keys, "beta")
		assert.Contains(t, keys, "stable")
	})

	t.Run("skips complex value types", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{Key: "tags", Target: "jsonData", ValueType: "array"},
			{Key: "meta", Target: "jsonData", ValueType: "map"},
			{Key: "nested", Target: "jsonData", ValueType: "object"},
			{Key: "simple", Target: "jsonData", ValueType: "string"},
		}}
		keys := fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields)
		assert.NotContains(t, keys, "tags")
		assert.NotContains(t, keys, "meta")
		assert.NotContains(t, keys, "nested")
		assert.Contains(t, keys, "simple")
	})

	t.Run("skips optional fields with dependsOn, keeps required ones", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{Key: "optDep", Target: "jsonData", ValueType: "string", DependsOn: "other", Required: false},
			{Key: "reqDep", Target: "jsonData", ValueType: "string", DependsOn: "other", Required: true},
			{Key: "free", Target: "jsonData", ValueType: "string"},
		}}
		keys := fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields)
		assert.NotContains(t, keys, "optDep")
		assert.Contains(t, keys, "reqDep")
		assert.Contains(t, keys, "free")
	})

	t.Run("section prefix applied to output key", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{Key: "region", Section: "aws", Target: "jsonData", ValueType: "string"},
		}}
		keys := fieldKeys(BuildSchemaGuidance(schema, "create_datasource").Fields)
		assert.Contains(t, keys, "aws.region")
		assert.NotContains(t, keys, "region")
	})

	t.Run("extracts allowed values and default for select fields", func(t *testing.T) {
		schema := &DatasourceSchema{Fields: []DsSchemaField{
			{
				Key:        "method",
				Target:     "jsonData",
				ValueType:  "string",
				DefaultVal: "POST",
				UI: &dsFieldUI{Options: []dsSchemaFieldOption{
					{Label: "GET", Value: "GET"},
					{Label: "POST", Value: "POST"},
				}},
			},
		}}
		guidance := BuildSchemaGuidance(schema, "create_datasource")
		var methodField *GuidanceField
		for i := range guidance.Fields {
			if guidance.Fields[i].Key == "method" {
				methodField = &guidance.Fields[i]
				break
			}
		}
		require.NotNil(t, methodField)
		assert.Equal(t, "POST", methodField.Default)
		assert.ElementsMatch(t, []any{"GET", "POST"}, methodField.AllowedValues)
	})
}
