package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
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
	Fields map[string]any `json:"fields,omitempty" jsonschema:"description=Datasource field values to provision, keyed by field key from the schema returned on the first call. The server uses each field's target (root or jsonData) to place values correctly in the YAML. Example: {\"url\": \"http://prometheus:9090\", \"httpMethod\": \"POST\"}."`
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

	// Build a field-target lookup from the schema so we can route each field
	// to the correct place in the YAML (root vs jsonData).
	fieldTarget := map[string]string{}
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
		if k == "name" {
			continue // already set above
		}
		if fieldTarget[k] == "jsonData" {
			jsonDataUpdates[k] = v
		} else {
			updates[k] = v
		}
	}

	var entry datasourceEntry
	if err := applyUpdates(&entry, updates, jsonDataUpdates); err != nil {
		return nil, fmt.Errorf("build datasource entry: %w", err)
	}
	pf.Datasources = append(pf.Datasources, entry)

	out, err := yaml.Marshal(pf)
	if err != nil {
		return nil, fmt.Errorf("marshal YAML: %w", err)
	}
	content := string(out)

	zipContent, err := buildZip(map[string][]byte{stem + ".yaml": out})
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
	"Generate a Grafana datasource provisioning YAML file. IMPORTANT: always call this tool twice. First call: provide only the type — the tool returns a field schema. After receiving the schema, you MUST ask the user for every required field value explicitly; do not infer or use defaults without user confirmation. Second call: provide the type plus the fields map populated with values confirmed by the user. Returns the YAML content as text and a zip file attachment that can be extracted into your local provisioning/datasources directory.",
	provisionDatasource,
	mcp.WithTitleAnnotation("Provision datasource"),
)

func AddDatasourceProvisioningTools(mcp *server.MCPServer) {
	ProvisionDatasource.Register(mcp)
}
