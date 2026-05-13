package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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
	Directory   string         `json:"directory,omitempty" jsonschema:"description=Directory where the tool will attempt to write the YAML provisioning file. Optional. Writing to disk is only reliable when the server is running via stdio (local mode). In remote deployments the write targets the server's filesystem\\, not the client's — omit this and use the returned content or zip attachment instead."`
	FileName    string         `json:"file_name,omitempty" jsonschema:"description=Template for the file name. Supports {type} placeholder. Defaults to 'prov-{type}'."`
	Type        string         `json:"type" jsonschema:"required,description=Datasource type (e.g. prometheus\\, loki\\, influxdb)"`
	ForceUpdate bool           `json:"force_update,omitempty" jsonschema:"description=If true\\, overwrite an existing datasource entry with the same name. Defaults to false\\, which returns a conflict error instead."`
	Fields      map[string]any `json:"fields,omitempty" jsonschema:"description=Datasource field values to provision\\, keyed by field key from the schema returned on the first call. The server uses each field's target (root or jsonData) to place values correctly in the YAML. Example: {\"url\": \"http://prometheus:9090\"\\, \"httpMethod\": \"POST\"}."`
}

type ProvisionDatasourceResult struct {
	FilePath    string `json:"file_path,omitempty"`
	FileCreated bool   `json:"file_created,omitempty"`
	Conflict    bool   `json:"conflict,omitempty"`
	Content     string `json:"content"`
	Summary     string `json:"summary"`
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

	fileName := args.FileName
	if fileName == "" {
		fileName = "prov-{type}"
	}
	stem := filepath.Base(strings.ReplaceAll(fileName, "{type}", args.Type))

	dsName, _ := args.Fields["name"].(string)
	if dsName == "" {
		dsName = stemToDisplayName(stem)
	}

	// Load existing file if a directory was given, otherwise start fresh.
	pf := &datasourceProvisioningFile{APIVersion: 1}
	isNew := true
	var filePath string

	if args.Directory != "" {
		filePath = filepath.Join(args.Directory, stem+".yaml")
		if data, err := os.ReadFile(filePath); err == nil {
			isNew = false
			if err := yaml.Unmarshal(data, pf); err != nil {
				return nil, fmt.Errorf("parse %s: %w", filePath, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", filePath, err)
		}
	}

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

	// Upsert: merge into existing entry by name, or append.
	// this will only be work when running the server locally
	// when running remote we won't have access to read local user files
	upserted := false
	for i, existing := range pf.Datasources {
		if existing.Name == dsName {
			if !args.ForceUpdate {
				text, _ := json.Marshal(&ProvisionDatasourceResult{
					FilePath: filePath,
					Conflict: true,
					Summary:  fmt.Sprintf("Datasource '%s' already exists in %s. Set force_update to true to overwrite it, or provide a different name.", dsName, filePath),
				})
				return mcp.NewToolResultText(string(text)), nil
			}
			if err := applyUpdates(&pf.Datasources[i], updates, jsonDataUpdates); err != nil {
				return nil, fmt.Errorf("merge datasource: %w", err)
			}
			upserted = true
			break
		}
	}
	if !upserted {
		var entry datasourceEntry
		if err := applyUpdates(&entry, updates, jsonDataUpdates); err != nil {
			return nil, fmt.Errorf("build datasource entry: %w", err)
		}
		pf.Datasources = append(pf.Datasources, entry)
	}

	out, err := yaml.Marshal(pf)
	if err != nil {
		return nil, fmt.Errorf("marshal YAML: %w", err)
	}
	content := string(out)

	zipContent, err := buildZip(map[string][]byte{stem + ".yaml": out})
	if err != nil {
		return nil, fmt.Errorf("build zip: %w", err)
	}

	// Write to disk only when a directory was provided.
	if args.Directory != "" {
		if err := os.MkdirAll(args.Directory, 0755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", args.Directory, err)
		}
		if err := os.WriteFile(filePath, out, 0644); err != nil {
			return nil, fmt.Errorf("write %s: %w", filePath, err)
		}
	}

	var summary string
	if args.Directory == "" {
		summary = fmt.Sprintf("Generated provisioning YAML for datasource '%s'. The content field contains the YAML — the attached zip file can be extracted directly into your local provisioning/datasources directory.", dsName)
	} else {
		action := "update"
		if isNew {
			action = "create"
		}
		summary = fmt.Sprintf("Attempted to %s provisioning file %s with datasource '%s'. Write to disk is only guaranteed when running via stdio — in remote mode this targeted the server's filesystem.", action, filePath, dsName)
	}

	text, _ := json.Marshal(&ProvisionDatasourceResult{
		FilePath:    filePath,
		FileCreated: isNew && args.Directory != "",
		Content:     content,
		Summary:     summary,
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
	"Generate a Grafana datasource provisioning YAML file. IMPORTANT: always call this tool twice. First call: provide only the type — the tool returns a field schema. After receiving the schema, you MUST ask the user for every required field value explicitly; do not infer or use defaults without user confirmation. Second call: provide the type plus the fields map populated with values confirmed by the user. Always returns the YAML content as text and a zip file attachment. Optionally attempts to write to disk when a directory is provided — only reliable when running via stdio (local mode). In remote deployments use the returned content or zip attachment to save the file locally.",
	provisionDatasource,
	mcp.WithTitleAnnotation("Provision datasource"),
)

func AddDatasourceProvisioningTools(mcp *server.MCPServer) {
	ProvisionDatasource.Register(mcp)
}
