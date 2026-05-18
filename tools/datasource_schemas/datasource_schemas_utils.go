package datasourceschemas

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed *.json
var datasourceSchemaFiles embed.FS

// commonDatasourceFields are provisioning fields that apply to every datasource type
// regardless of the plugin. They are prepended to the type-specific schema fields in
// guidance so the user is always prompted for them.
var commonDatasourceFields = []DsSchemaField{
	{
		ID:          "root.uid",
		Key:         "uid",
		Label:       "UID",
		Description: "Custom unique identifier for this datasource. If omitted, Grafana generates one automatically.",
		ValueType:   "string",
		Target:      "root",
	},
	{
		ID:          "root.orgId",
		Key:         "orgId",
		Label:       "Organization ID",
		Description: "Organization this datasource belongs to. Defaults to 1.",
		ValueType:   "number",
		Target:      "root",
		DefaultVal:  1,
	},
	{
		ID:          "root.isDefault",
		Key:         "isDefault",
		Label:       "Default datasource",
		Description: "When true, this datasource is pre-selected for new panels. Only one datasource per organization can be the default.",
		ValueType:   "boolean",
		Target:      "root",
		DefaultVal:  false,
	},
	{
		ID:          "root.editable",
		Key:         "editable",
		Label:       "Editable",
		Description: "When true, users can edit this datasource from the Grafana UI. When false, it is read-only.",
		ValueType:   "boolean",
		Target:      "root",
		DefaultVal:  false,
	},
}

// dsFieldValidation covers all FieldValidationRule shapes from the dsconfig schema.
// Fields not relevant to a given type will simply be zero-valued after unmarshaling.
type dsFieldValidation struct {
	// Common base fields
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	// Discriminator present on every concrete type
	Type string `json:"type"`
	// pattern
	Pattern string `json:"pattern,omitempty"`
	// range | length | itemCount
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// allowedValues — unknown[] in TypeScript, so []any here
	Values []any `json:"values,omitempty"`
	// custom
	Expression string `json:"expression,omitempty"`
}

// dsSchemaFieldOption is a single choice for a select-type field.
// IsDefault is set by buildSchemaGuidance, not stored in the JSON files.
type dsSchemaFieldOption struct {
	Label     string `json:"label"`
	Value     any    `json:"value"`
	IsDefault bool   `json:"isDefault,omitempty"`
}


// dsFieldUI captures the UI hints for a field. Only Options is kept in the
// guidance output; component/placeholder/rows are rendering-only and ignored.
type dsFieldUI struct {
	Options []dsSchemaFieldOption `json:"options,omitempty"`
}


// dsSchemaField mirrors the relevant fields of each entry in a datasource schema JSON.
type DsSchemaField struct {
	ID           string              `json:"id"`
	Key          string              `json:"key"`
	Label        string              `json:"label"`
	Description  string              `json:"description"`
	ValueType    string              `json:"valueType"`
	SemanticType string              `json:"semanticType,omitempty"`
	Target       string              `json:"target"`
	Section      string              `json:"section,omitempty"`
	Required     bool                `json:"required,omitempty"`
	DefaultVal   any                 `json:"defaultValue,omitempty"`
	Lifecycle    string              `json:"lifecycle,omitempty"`
	Kind         string              `json:"kind,omitempty"`
	Tags         []string            `json:"tags,omitempty"`
	DependsOn    string              `json:"dependsOn,omitempty"`
	Validations  []dsFieldValidation `json:"validations,omitempty"`
	UI           *dsFieldUI          `json:"ui,omitempty"`
}

type DatasourceSchema struct {
	PluginType string          `json:"pluginType"`
	PluginName string          `json:"pluginName"`
	DocURL     string          `json:"docURL"`
	Fields     []DsSchemaField `json:"fields"`
}

type datasourceSchemaGuidance struct {
	Type       string          `json:"type"`
	PluginName string          `json:"plugin_name"`
	DocURL     string          `json:"doc_url,omitempty"`
	Message    string          `json:"message"`
	Fields     []DsSchemaField `json:"fields"`
}

func LoadDatasourceSchema(pluginType string) (*DatasourceSchema, error) {
	data, err := datasourceSchemaFiles.ReadFile(fmt.Sprintf("%s_schema.json", pluginType))
	if err != nil {
		return nil, nil // no schema for this type
	}
	var s DatasourceSchema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schema for %s: %w", pluginType, err)
	}
	return &s, nil
}

func SchemaFieldInputKey(f DsSchemaField) string {
	if f.Section == "" {
		return f.Key
	}
	return f.Section + "." + f.Key
}

// annotateDefaultOption returns a copy of f with IsDefault set on the option
// whose value matches f.DefaultVal, mirroring the generateLLMHint behaviour.
func annotateDefaultOption(f DsSchemaField) DsSchemaField {
	if f.UI == nil || len(f.UI.Options) == 0 || f.DefaultVal == nil {
		// it is non-select UI field, return as is.
		return f
	}
	opts := make([]dsSchemaFieldOption, len(f.UI.Options))
	copy(opts, f.UI.Options)
	for i, opt := range opts {
		if fmt.Sprint(opt.Value) == fmt.Sprint(f.DefaultVal) {
			opts[i].IsDefault = true
		}
	}
	uiCopy := &dsFieldUI{Options: opts}
	f.UI = uiCopy
	return f
}

func BuildSchemaGuidance(schema *DatasourceSchema, toolName string) *datasourceSchemaGuidance {
	fields := make([]DsSchemaField, 0, len(commonDatasourceFields)+len(schema.Fields))
	fields = append(fields, commonDatasourceFields...)

	for _, f := range schema.Fields {
		if f.Kind == "virtual" {
			continue
		}

		// Never surface sensitive fields.
		if f.Target == "secureJsonData" || f.ID == "root.basicAuthUser" {
			continue
		}
		// Experimental fields are opt-in; omit from default guidance.
		if f.Lifecycle == "experimental" {
			continue
		}
		// Skip complex types (arrays / maps / nested objects) for now.
		if f.ValueType == "array" || f.ValueType == "map" || f.ValueType == "object" {
			continue
		}
		// Optional fields with a conditional dependency are advanced; skip them
		// to keep the initial guidance focused on the common case.
		if f.DependsOn != "" && !f.Required {
			continue
		}

		if f.Target != "root" && f.Target != "jsonData" {
			continue
		}

		f.Key = SchemaFieldInputKey(f)
		fields = append(fields, annotateDefaultOption(f))
	}

	return &datasourceSchemaGuidance{
		Type:       schema.PluginType,
		PluginName: schema.PluginName,
		DocURL:     schema.DocURL,
		Message: fmt.Sprintf(
			"Schema for %s datasource. "+
				"You MUST ask the user for the value of every required field (required=true) before calling provision_datasource again. "+
				"Do NOT infer, guess, or use default values for required fields without explicit confirmation from the user. "+
				"For optional fields, ask only if they are relevant to the user's setup. "+
				"Once you have collected all required values from the user, call %s again with those values in the fields param. "+
				"The server uses each field's target to route values to the correct place in the YAML.",
			schema.PluginName,
			toolName,
		),
		Fields: fields,
	}
}
