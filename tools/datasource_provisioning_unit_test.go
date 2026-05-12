package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func fieldKeys(fields []dsSchemaField) []string {
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.Key
	}
	return keys
}

func mustExtractText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "first content item must be TextContent")
	return tc.Text
}

func mustExtractProvisionResult(t *testing.T, result *mcp.CallToolResult) ProvisionDatasourceResult {
	t.Helper()
	var pr ProvisionDatasourceResult
	require.NoError(t, json.Unmarshal([]byte(mustExtractText(t, result)), &pr))
	return pr
}

func mustExtractGuidance(t *testing.T, result *mcp.CallToolResult) datasourceSchemaGuidance {
	t.Helper()
	var g datasourceSchemaGuidance
	require.NoError(t, json.Unmarshal([]byte(mustExtractText(t, result)), &g))
	return g
}

func mustExtractZipBlob(t *testing.T, result *mcp.CallToolResult) mcp.BlobResourceContents {
	t.Helper()
	require.Len(t, result.Content, 2, "phase 2 result must have text + zip")
	resource, ok := result.Content[1].(mcp.EmbeddedResource)
	require.True(t, ok, "second content item must be EmbeddedResource")
	blob, ok := resource.Resource.(mcp.BlobResourceContents)
	require.True(t, ok, "resource must be BlobResourceContents")
	return blob
}

// minimalSchema returns a controlled schema for filter tests that covers every
// category of field that buildSchemaGuidance should include or exclude.
func minimalSchema() *datasourceSchema {
	return &datasourceSchema{
		PluginType: "testtype",
		PluginName: "Test Plugin",
		DocURL:     "https://example.com/docs",
		Fields: []dsSchemaField{
			// ✓ included
			{ID: "root.url", Key: "url", ValueType: "string", Target: "root", Required: true},
			{ID: "jsonData.timeout", Key: "timeout", ValueType: "number", Target: "jsonData"},
			// required + dependsOn → included (required overrides the optional-with-dependsOn rule)
			{ID: "root.req_dep", Key: "req_dep", ValueType: "string", Target: "jsonData", DependsOn: "url", Required: true},
			// ✗ excluded: virtual kind
			{ID: "v.virt", Key: "virt", ValueType: "string", Target: "root", Kind: "virtual"},
			// ✗ excluded: secureJsonData target
			{ID: "root.pw", Key: "pw", ValueType: "string", Target: "secureJsonData"},
			// ✗ excluded: root.basicAuthUser sentinel
			{ID: "root.basicAuthUser", Key: "basicAuthUser", ValueType: "string", Target: "root"},
			// ✗ excluded: experimental lifecycle
			{ID: "root.exp", Key: "exp", ValueType: "string", Target: "jsonData", Lifecycle: "experimental"},
			// ✗ excluded: array valueType
			{ID: "root.arr", Key: "arr", ValueType: "array", Target: "jsonData"},
			// ✗ excluded: object valueType
			{ID: "root.obj", Key: "obj", ValueType: "object", Target: "jsonData"},
			// ✗ excluded: optional + dependsOn
			{ID: "root.cond", Key: "cond", ValueType: "string", Target: "jsonData", DependsOn: "url", Required: false},
		},
	}
}

// ── stemToDisplayName ────────────────────────────────────────────────────────

func TestStemToDisplayName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"prometheus", "Prometheus"},
		{"my-prometheus", "My Prometheus"},
		{"prov_loki", "Prov Loki"},
		{"multi-word-stem", "Multi Word Stem"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, stemToDisplayName(tc.in))
		})
	}
}

// ── loadDatasourceSchema ─────────────────────────────────────────────────────

func TestLoadDatasourceSchema_PrometheusReturnsSchema(t *testing.T) {
	schema, err := loadDatasourceSchema("prometheus")
	require.NoError(t, err)
	require.NotNil(t, schema)
	assert.Equal(t, "prometheus", schema.PluginType)
	assert.Equal(t, "Prometheus", schema.PluginName)
	assert.NotEmpty(t, schema.Fields)
}

func TestLoadDatasourceSchema_UnknownTypeReturnsNil(t *testing.T) {
	schema, err := loadDatasourceSchema("nonexistent-type-xyz")
	require.NoError(t, err)
	assert.Nil(t, schema)
}

// ── buildSchemaGuidance ──────────────────────────────────────────────────────

func TestBuildSchemaGuidance_IncludesRootAndJsonDataFields(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	keys := fieldKeys(guidance.Fields)
	assert.Contains(t, keys, "url")
	assert.Contains(t, keys, "timeout")
}

func TestBuildSchemaGuidance_IncludesRequiredFieldWithDependsOn(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.Contains(t, fieldKeys(guidance.Fields), "req_dep")
}

func TestBuildSchemaGuidance_ExcludesVirtualFields(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.NotContains(t, fieldKeys(guidance.Fields), "virt")
}

func TestBuildSchemaGuidance_ExcludesSecureJsonDataFields(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.NotContains(t, fieldKeys(guidance.Fields), "pw")
}

func TestBuildSchemaGuidance_ExcludesBasicAuthUserSentinel(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.NotContains(t, fieldKeys(guidance.Fields), "basicAuthUser")
}

func TestBuildSchemaGuidance_ExcludesExperimentalFields(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.NotContains(t, fieldKeys(guidance.Fields), "exp")
}

func TestBuildSchemaGuidance_ExcludesArrayAndObjectValueTypes(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	keys := fieldKeys(guidance.Fields)
	assert.NotContains(t, keys, "arr")
	assert.NotContains(t, keys, "obj")
}

func TestBuildSchemaGuidance_ExcludesOptionalFieldsWithDependsOn(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.NotContains(t, fieldKeys(guidance.Fields), "cond")
}

func TestBuildSchemaGuidance_PrependsCommonFieldsFirst(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	require.GreaterOrEqual(t, len(guidance.Fields), len(commonDatasourceFields))
	for i, f := range commonDatasourceFields {
		assert.Equal(t, f.Key, guidance.Fields[i].Key, "common field at index %d", i)
	}
}

func TestBuildSchemaGuidance_PopulatesMetadata(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.Equal(t, "testtype", guidance.Type)
	assert.Equal(t, "Test Plugin", guidance.PluginName)
	assert.Equal(t, "https://example.com/docs", guidance.DocURL)
}

func TestBuildSchemaGuidance_MessageContainsPluginNameAndInstruction(t *testing.T) {
	guidance := buildSchemaGuidance(minimalSchema())
	assert.Contains(t, guidance.Message, "Test Plugin")
	assert.Contains(t, guidance.Message, "MUST")
}

func TestBuildSchemaGuidance_AnnotatesDefaultOption(t *testing.T) {
	schema := minimalSchema()
	schema.Fields = append(schema.Fields, dsSchemaField{
		ID:        "jsonData.method",
		Key:       "method",
		ValueType: "string",
		Target:    "jsonData",
		DefaultVal: "POST",
		UI: &dsFieldUI{
			Options: []dsSchemaFieldOption{
				{Label: "GET", Value: "GET"},
				{Label: "POST", Value: "POST"},
			},
		},
	})

	guidance := buildSchemaGuidance(schema)

	var field *dsSchemaField
	for i := range guidance.Fields {
		if guidance.Fields[i].Key == "method" {
			field = &guidance.Fields[i]
			break
		}
	}
	require.NotNil(t, field, "method field must be present")
	require.NotNil(t, field.UI)
	require.Len(t, field.UI.Options, 2)

	for _, opt := range field.UI.Options {
		if fmt.Sprint(opt.Value) == "POST" {
			assert.True(t, opt.IsDefault, "POST should be marked as default")
		} else {
			assert.False(t, opt.IsDefault, "GET should not be marked as default")
		}
	}
}

func TestBuildSchemaGuidance_OptionsWithNoDefaultLeaveIsDefaultUnset(t *testing.T) {
	schema := minimalSchema()
	schema.Fields = append(schema.Fields, dsSchemaField{
		ID:        "jsonData.method",
		Key:       "method",
		ValueType: "string",
		Target:    "jsonData",
		// no DefaultVal
		UI: &dsFieldUI{
			Options: []dsSchemaFieldOption{
				{Label: "GET", Value: "GET"},
				{Label: "POST", Value: "POST"},
			},
		},
	})

	guidance := buildSchemaGuidance(schema)

	var field *dsSchemaField
	for i := range guidance.Fields {
		if guidance.Fields[i].Key == "method" {
			field = &guidance.Fields[i]
			break
		}
	}
	require.NotNil(t, field)
	for _, opt := range field.UI.Options {
		assert.False(t, opt.IsDefault, "no option should be marked default when DefaultVal is nil")
	}
}

func TestBuildSchemaGuidance_UnmatchedDefaultLeavesIsDefaultUnset(t *testing.T) {
	schema := minimalSchema()
	schema.Fields = append(schema.Fields, dsSchemaField{
		ID:         "jsonData.method",
		Key:        "method",
		ValueType:  "string",
		Target:     "jsonData",
		DefaultVal: "PATCH", // not present in options
		UI: &dsFieldUI{
			Options: []dsSchemaFieldOption{
				{Label: "GET", Value: "GET"},
				{Label: "POST", Value: "POST"},
			},
		},
	})

	guidance := buildSchemaGuidance(schema)

	var field *dsSchemaField
	for i := range guidance.Fields {
		if guidance.Fields[i].Key == "method" {
			field = &guidance.Fields[i]
			break
		}
	}
	require.NotNil(t, field)
	for _, opt := range field.UI.Options {
		assert.False(t, opt.IsDefault, "no option should be marked default when default value is not in the options list")
	}
}

// ── applyUpdates ─────────────────────────────────────────────────────────────

func TestApplyUpdates_SetsKnownRootFields(t *testing.T) {
	var entry datasourceEntry
	require.NoError(t, applyUpdates(&entry, map[string]any{
		"name":   "My DS",
		"type":   "prometheus",
		"access": "proxy",
		"url":    "http://localhost:9090",
	}, nil))
	assert.Equal(t, "My DS", entry.Name)
	assert.Equal(t, "prometheus", entry.Type)
	assert.Equal(t, "proxy", entry.Access)
	assert.Equal(t, "http://localhost:9090", entry.URL)
}

func TestApplyUpdates_SetsJsonData(t *testing.T) {
	var entry datasourceEntry
	require.NoError(t, applyUpdates(&entry, nil, map[string]any{
		"httpMethod":   "POST",
		"timeInterval": "15s",
	}))
	require.NotNil(t, entry.JSONData)
	assert.Equal(t, "POST", entry.JSONData["httpMethod"])
	assert.Equal(t, "15s", entry.JSONData["timeInterval"])
}

func TestApplyUpdates_MergesIntoExistingJsonData(t *testing.T) {
	entry := datasourceEntry{JSONData: map[string]any{"existing": "keep"}}
	require.NoError(t, applyUpdates(&entry, nil, map[string]any{"new": "add"}))
	assert.Equal(t, "keep", entry.JSONData["existing"])
	assert.Equal(t, "add", entry.JSONData["new"])
}

func TestApplyUpdates_SetsBooleanPointerFields(t *testing.T) {
	var entry datasourceEntry
	require.NoError(t, applyUpdates(&entry, map[string]any{
		"isDefault": true,
		"editable":  false,
	}, nil))
	require.NotNil(t, entry.IsDefault)
	assert.True(t, *entry.IsDefault)
	require.NotNil(t, entry.Editable)
	assert.False(t, *entry.Editable)
}

// ── provisionDatasource ──────────────────────────────────────────────────────

func promArgs(fields map[string]any) ProvisionDatasourceParams {
	return ProvisionDatasourceParams{Type: "prometheus", Fields: fields}
}

func TestProvisionDatasource_UnknownType_ReturnsError(t *testing.T) {
	_, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{Type: "no-such-type-xyz"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no schema available")
}

func TestProvisionDatasource_Phase1_ReturnsSchemaGuidance(t *testing.T) {
	result, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{Type: "prometheus"})
	require.NoError(t, err)

	guidance := mustExtractGuidance(t, result)
	assert.Equal(t, "prometheus", guidance.Type)
	assert.Equal(t, "Prometheus", guidance.PluginName)
	assert.NotEmpty(t, guidance.Fields)
	assert.NotEmpty(t, guidance.Message)
}

func TestProvisionDatasource_Phase1_CommonFieldsIncluded(t *testing.T) {
	result, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{Type: "prometheus"})
	require.NoError(t, err)

	guidance := mustExtractGuidance(t, result)
	keys := fieldKeys(guidance.Fields)
	assert.Contains(t, keys, "uid")
	assert.Contains(t, keys, "isDefault")
	assert.Contains(t, keys, "editable")
}

func TestProvisionDatasource_Phase2_GeneratesValidYAML(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url": "http://prometheus:9090",
	}))
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	var pf datasourceProvisioningFile
	require.NoError(t, yaml.Unmarshal([]byte(pr.Content), &pf))
	assert.Equal(t, 1, pf.APIVersion)
	require.Len(t, pf.Datasources, 1)
}

func TestProvisionDatasource_Phase2_NameFromFields(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"name": "My Prometheus",
		"url":  "http://prometheus:9090",
	}))
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	assert.Contains(t, pr.Content, "name: My Prometheus")
}

func TestProvisionDatasource_Phase2_DefaultNameDerivedFromStem(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url": "http://prometheus:9090",
	}))
	require.NoError(t, err)

	// Default FileName is "prov-{type}" → stem "prov-prometheus" → "Prov Prometheus"
	pr := mustExtractProvisionResult(t, result)
	assert.Contains(t, pr.Content, "name: Prov Prometheus")
}

func TestProvisionDatasource_Phase2_AccessAlwaysProxy(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url": "http://prometheus:9090",
	}))
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	assert.Contains(t, pr.Content, "access: proxy")
}

func TestProvisionDatasource_Phase2_UrlRoutedToRoot(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url": "http://prometheus:9090",
	}))
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	var pf datasourceProvisioningFile
	require.NoError(t, yaml.Unmarshal([]byte(pr.Content), &pf))
	assert.Equal(t, "http://prometheus:9090", pf.Datasources[0].URL)
	if pf.Datasources[0].JSONData != nil {
		assert.NotContains(t, pf.Datasources[0].JSONData, "url")
	}
}

func TestProvisionDatasource_Phase2_JsonDataFieldRoutedToJsonData(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url":        "http://prometheus:9090",
		"httpMethod": "POST",
	}))
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	var pf datasourceProvisioningFile
	require.NoError(t, yaml.Unmarshal([]byte(pr.Content), &pf))
	require.NotNil(t, pf.Datasources[0].JSONData)
	assert.Equal(t, "POST", pf.Datasources[0].JSONData["httpMethod"])
}

func TestProvisionDatasource_Phase2_NameIsFirstKey(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url": "http://prometheus:9090",
	}))
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	// Find the datasource list entry (after the leading "- ")
	entryStart := strings.Index(pr.Content, "- ")
	require.NotEqual(t, -1, entryStart)
	rest := pr.Content[entryStart:]
	nameIdx := strings.Index(rest, "name:")
	typeIdx := strings.Index(rest, "type:")
	accessIdx := strings.Index(rest, "access:")
	require.Greater(t, nameIdx, -1)
	assert.Less(t, nameIdx, typeIdx, "name must appear before type")
	assert.Less(t, nameIdx, accessIdx, "name must appear before access")
}

func TestProvisionDatasource_Phase2_WritesFileToDisk(t *testing.T) {
	dir := t.TempDir()
	result, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{
		Type:      "prometheus",
		Directory: dir,
		Fields:    map[string]any{"name": "Written DS", "url": "http://prometheus:9090"},
	})
	require.NoError(t, err)

	pr := mustExtractProvisionResult(t, result)
	assert.True(t, pr.FileCreated)
	require.NotEmpty(t, pr.FilePath)
	require.FileExists(t, pr.FilePath)
	data, err := os.ReadFile(pr.FilePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: Written DS")
}

func TestProvisionDatasource_Phase2_Conflict_NoForceUpdate(t *testing.T) {
	dir := t.TempDir()
	args := ProvisionDatasourceParams{
		Type:      "prometheus",
		Directory: dir,
		Fields:    map[string]any{"name": "Existing DS", "url": "http://prometheus:9090"},
	}
	_, err := provisionDatasource(context.Background(), args)
	require.NoError(t, err)

	result, err := provisionDatasource(context.Background(), args)
	require.NoError(t, err)
	pr := mustExtractProvisionResult(t, result)
	assert.True(t, pr.Conflict)
	assert.Contains(t, pr.Summary, "Existing DS")
}

func TestProvisionDatasource_Phase2_ForceUpdate_OverwritesEntry(t *testing.T) {
	dir := t.TempDir()
	_, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{
		Type:      "prometheus",
		Directory: dir,
		Fields:    map[string]any{"name": "My DS", "url": "http://old:9090"},
	})
	require.NoError(t, err)

	result, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{
		Type:        "prometheus",
		Directory:   dir,
		ForceUpdate: true,
		Fields:      map[string]any{"name": "My DS", "url": "http://new:9090"},
	})
	require.NoError(t, err)
	pr := mustExtractProvisionResult(t, result)
	assert.False(t, pr.Conflict)
	assert.Contains(t, pr.Content, "http://new:9090")
	assert.NotContains(t, pr.Content, "http://old:9090")
}

func TestProvisionDatasource_Phase2_ZipAttachmentIsValidArchive(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"url": "http://prometheus:9090",
	}))
	require.NoError(t, err)

	blob := mustExtractZipBlob(t, result)
	assert.Equal(t, "application/zip", blob.MIMEType)
	assert.True(t, strings.HasSuffix(blob.URI, ".zip"))

	zipBytes, err := base64.StdEncoding.DecodeString(blob.Blob)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	assert.True(t, strings.HasSuffix(zr.File[0].Name, ".yaml"))
}

func TestProvisionDatasource_Phase2_ZipContainsYAMLContent(t *testing.T) {
	result, err := provisionDatasource(context.Background(), promArgs(map[string]any{
		"name": "Zip DS",
		"url":  "http://prometheus:9090",
	}))
	require.NoError(t, err)

	blob := mustExtractZipBlob(t, result)
	zipBytes, err := base64.StdEncoding.DecodeString(blob.Blob)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)

	rc, err := zr.File[0].Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })
	var buf bytes.Buffer
	_, err = buf.ReadFrom(rc)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "name: Zip DS")
}

func TestProvisionDatasource_Phase2_CustomFileName(t *testing.T) {
	result, err := provisionDatasource(context.Background(), ProvisionDatasourceParams{
		Type:     "prometheus",
		FileName: "custom-{type}",
		Fields:   map[string]any{"url": "http://prometheus:9090"},
	})
	require.NoError(t, err)

	blob := mustExtractZipBlob(t, result)
	assert.Equal(t, "custom-prometheus.zip", blob.URI)
}
