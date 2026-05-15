package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type datasourceProvisioningFile struct {
	APIVersion  int               `yaml:"apiVersion"`
	Datasources []datasourceEntry `yaml:"datasources"`
}

// datasourceEntry represents a single datasource in the provisioning file.
// Field declaration order determines the YAML key order on output.
type datasourceEntry struct {
	Name            string         `yaml:"name"`
	Type            string         `yaml:"type"`
	UID             string         `yaml:"uid,omitempty"`
	OrgID           int            `yaml:"orgId,omitempty"`
	Access          string         `yaml:"access,omitempty"`
	URL             string         `yaml:"url,omitempty"`
	User            string         `yaml:"user,omitempty"`
	Database        string         `yaml:"database,omitempty"`
	BasicAuth       *bool          `yaml:"basicAuth,omitempty"`
	BasicAuthUser   string         `yaml:"basicAuthUser,omitempty"`
	WithCredentials *bool          `yaml:"withCredentials,omitempty"`
	IsDefault       *bool          `yaml:"isDefault,omitempty"`
	Editable        *bool          `yaml:"editable,omitempty"`
	Version         int            `yaml:"version,omitempty"`
	JSONData        map[string]any `yaml:"jsonData,omitempty"`
	SecureJSONData  map[string]any `yaml:"secureJsonData,omitempty"`
	Extra           map[string]any `yaml:",inline"`
}

// applyUpdates merges root-level and jsonData updates into an existing entry.
// It uses a yaml round-trip so that type coercion (e.g. float64 → int for orgId)
// is handled by the yaml package rather than hand-written switch cases.
func applyUpdates(entry *datasourceEntry, updates map[string]any, jsonDataUpdates map[string]any) error {
	data, err := yaml.Marshal(updates)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, entry); err != nil {
		return err
	}
	if len(jsonDataUpdates) > 0 {
		if entry.JSONData == nil {
			entry.JSONData = make(map[string]any)
		}
		maps.Copy(entry.JSONData, jsonDataUpdates)
	}
	return nil
}

type ProvisionDatasourceParams struct {
	Type   string         `json:"type" jsonschema:"required,description=Datasource type (e.g. prometheus\\, loki\\, influxdb)"`
	Fields map[string]any `json:"fields,omitempty" jsonschema:"description=Datasource field values to provision\\, keyed by field key from the schema returned on the first call. The server uses each field's target (root or jsonData) to place values correctly in the output. Example: {\"url\": \"http://prometheus:9090\"\\, \"httpMethod\": \"POST\"}."`
	Format string         `json:"format,omitempty" jsonschema:"required,description=Output format: yaml (default\\, for on-premises Grafana) or terraform (for Grafana Cloud customers using Terraform).,enum=yaml,enum=terraform"`
}

// tfAttrName maps YAML/JSON field names to Terraform HCL attribute names for
// the grafana_data_source resource. Fields absent from this map are not emitted.
var tfAttrName = map[string]string{
	"name":            "name",
	"type":            "type",
	"uid":             "uid",
	"url":             "url",
	"orgId":           "org_id",
	"isDefault":       "is_default",
	"editable":        "editable",
	"basicAuth":       "basic_auth_enabled",
	"withCredentials": "with_credentials",
	"database":        "database_name",
	// "access" is intentionally absent: "proxy" is the default in the TF provider
}

// tfAttrOrder controls the output order of Terraform root-level attributes.
var tfAttrOrder = []string{
	"type", "name", "uid", "url", "orgId",
	"isDefault", "editable", "basicAuth",
	"withCredentials", "database",
}

// toTerraformLabel converts a datasource name to a valid Terraform resource label.
func toTerraformLabel(name string) string {
	var sb strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore && sb.Len() > 0 {
			sb.WriteRune('_')
			prevUnderscore = true
		}
	}
	label := strings.TrimRight(sb.String(), "_")
	if label == "" {
		label = "datasource"
	}
	if label[0] >= '0' && label[0] <= '9' {
		label = "ds_" + label
	}
	return label
}

// hclLiteral renders a Go value as an HCL literal (string, bool, or number).
func hclLiteral(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// buildTerraformHCL generates a grafana_data_source Terraform resource block.
func buildTerraformHCL(label string, rootFields map[string]any, jsonData map[string]any) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("resource \"grafana_data_source\" %q {\n", label))

	for _, key := range tfAttrOrder {
		val, ok := rootFields[key]
		if !ok {
			continue
		}
		attr := tfAttrName[key]
		sb.WriteString(fmt.Sprintf("  %s = %s\n", attr, hclLiteral(val)))
	}

	if len(jsonData) > 0 {
		sb.WriteString("\n  json_data_encoded = jsonencode({\n")
		keys := make([]string, 0, len(jsonData))
		for k := range jsonData {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("    %s = %s\n", k, hclLiteral(jsonData[k])))
		}
		sb.WriteString("  })\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

type ProvisionDatasourceResult struct {
	Content string `json:"content"`
}

// buildZip produces a base64-encoded zip archive from a map of filename -> content.
func buildZip(files map[string][]byte) (string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return "", err
		}
		if _, err := w.Write(content); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// stemToDisplayName converts a stem like "my-prometheus" to "My Prometheus".
func stemToDisplayName(stem string) string {
	parts := strings.FieldsFunc(stem, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func provisionDatasource(_ context.Context, args ProvisionDatasourceParams) (*mcp.CallToolResult, error) {
	schema, err := loadDatasourceSchema(args.Type)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		return nil, fmt.Errorf("no schema available for datasource type %q", args.Type)
	}

	// Phase 1: if no fields have been provided yet, return field guidance so the
	// client can collect the right values from the user before calling again.
	if len(args.Fields) == 0 {
		text, _ := json.Marshal(buildSchemaGuidance(schema))
		return mcp.NewToolResultText(string(text)), nil
	}

	stem := "prov-" + args.Type

	dsName, _ := args.Fields["name"].(string)
	if dsName == "" {
		dsName = stemToDisplayName(stem)
	}

	pf := &datasourceProvisioningFile{APIVersion: 1}

	// Build a field-target lookup from the common fields and schema so we can
	// route each field to the correct place in the YAML (root vs jsonData).
	fieldTarget := map[string]string{}
	for _, f := range commonDatasourceFields {
		fieldTarget[f.Key] = f.Target
	}
	for _, f := range schema.Fields {
		fieldTarget[f.Key] = f.Target
	}

	// Split caller-provided fields into root-level updates and jsonData updates.
	// access is always proxy — direct/browser mode is deprecated in Grafana.
	updates := map[string]any{
		"name":   dsName,
		"type":   args.Type,
		"access": "proxy",
	}
	jsonDataUpdates := map[string]any{}
	for k, v := range args.Fields {
		if k == "name" || k == "type" || k == "access" {
			continue // already set above
		}
		// only update jsonData and root, ignore secureJsonData
		switch fieldTarget[k] {
		case "jsonData":
			jsonDataUpdates[k] = v
		case "root":
			updates[k] = v
		}
	}

	var content string
	var zipFiles map[string][]byte

	if args.Format == "terraform" {
		label := toTerraformLabel(dsName)
		content = buildTerraformHCL(label, updates, jsonDataUpdates)
		zipFiles = map[string][]byte{stem + ".tf": []byte(content)}
	} else {
		var entry datasourceEntry
		if err := applyUpdates(&entry, updates, jsonDataUpdates); err != nil {
			return nil, fmt.Errorf("build datasource entry: %w", err)
		}
		pf.Datasources = append(pf.Datasources, entry)

		out, err := yaml.Marshal(pf)
		if err != nil {
			return nil, fmt.Errorf("marshal YAML: %w", err)
		}
		content = string(out)
		zipFiles = map[string][]byte{stem + ".yaml": out}
	}

	zipContent, err := buildZip(zipFiles)
	if err != nil {
		return nil, fmt.Errorf("build zip: %w", err)
	}

	text, _ := json.Marshal(&ProvisionDatasourceResult{
		Content: content,
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: string(text)},
			mcp.NewEmbeddedResource(mcp.BlobResourceContents{
				URI:      stem + ".zip",
				MIMEType: "application/zip",
				Blob:     zipContent,
			}),
		},
	}, nil
}

var ProvisionDatasource = mcpgrafana.MustTool(
	"provision_datasource",
	"Generate a Grafana datasource provisioning file. Supports YAML format (for on-premises Grafana) and Terraform HCL format (for Grafana Cloud customers using Terraform). IMPORTANT: always call this tool twice. First call: provide only the type — the tool returns a field schema. After receiving the schema, you MUST ask the user for every required field value explicitly; do not infer or use defaults without user confirmation. Second call: provide the type, the fields map populated with values confirmed by the user, and optionally format (\"yaml\" for on-prem, \"terraform\" for Grafana Cloud). Returns the file content as text and a zip file attachment.",
	provisionDatasource,
	mcp.WithTitleAnnotation("Provision datasource"),
)

func AddDatasourceProvisioningTools(mcp *server.MCPServer) {
	ProvisionDatasource.Register(mcp)
}
