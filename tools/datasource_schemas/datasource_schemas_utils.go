package datasourceschemas

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
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
		ID:          "root.isDefault",
		Key:         "isDefault",
		Label:       "Default datasource",
		Description: "When true, this datasource is pre-selected for new panels. Only one datasource per organization can be the default.",
		ValueType:   "boolean",
		Target:      "root",
		DefaultVal:  false,
	},
}

// excludedFieldIDs are fields we deliberately never advertise in guidance nor
// apply during creation. They typically hold PII or credential material
// (connection usernames at either root or jsonData target, basic-auth
// usernames). Their matching secret (password/token) lives in secureJsonData,
// which this tool also never sets, so the user finishes auth setup — username
// included — in the Grafana UI.
var excludedFieldIDs = map[string]bool{
	"root.basicAuthUser": true,
	"root.user":          true,
	"jsonData.user":      true,
	"jsonData.username":  true,
}

// IsExcludedField reports whether f is omitted for privacy or credential-handling
// reasons.
func IsExcludedField(f DsSchemaField) bool {
	return excludedFieldIDs[f.ID]
}

// CommonDatasourceFields returns a copy of the shared root fields advertised in guidance.
func CommonDatasourceFields() []DsSchemaField {
	fields := make([]DsSchemaField, len(commonDatasourceFields))
	copy(fields, commonDatasourceFields)
	return fields
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

// GuidanceField is the slim per-field representation sent to the LLM.
// It contains only what is needed to prompt the user and populate the fields map.
type GuidanceField struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Description   string `json:"description,omitempty"`
	Required      bool   `json:"required,omitempty"`
	Type          string `json:"type"`
	Default       any    `json:"default,omitempty"`
	AllowedValues []any  `json:"allowedValues,omitempty"`
}

type datasourceSchemaGuidance struct {
	Type       string          `json:"type"`
	PluginName string          `json:"plugin_name"`
	DocURL     string          `json:"doc_url,omitempty"`
	Message    string          `json:"message"`
	Fields     []GuidanceField `json:"fields"`
}

// LoadDatasourceSchema loads the embedded schema for the given plugin type, returning (nil, nil) when no schema exists for it.
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

// KnownPluginTypes returns every plugin type that has an embedded schema, in
// filename order. Datasources of other types are still creatable (they take the
// no-schema guidance path), so this is not a validation set — it is the bounded
// value set telemetry uses to keep a caller-supplied "type" from becoming an
// unbounded metric label (see observability.ToolMetricDimensions).
// TODO: once schemas are fetched from the plugins CDN rather than embedded, this
// should read from there
func KnownPluginTypes() []string {
	entries, err := datasourceSchemaFiles.ReadDir(".")
	if err != nil {
		return nil // embedded FS, so unreachable in practice
	}
	types := make([]string, 0, len(entries))
	for _, e := range entries {
		if t, ok := strings.CutSuffix(e.Name(), "_schema.json"); ok {
			types = append(types, t)
		}
	}
	return types
}

// SchemaFieldInputKey returns the key callers use for f in the fields map, namespaced by section when present.
func SchemaFieldInputKey(f DsSchemaField) string {
	if f.Section == "" {
		return f.Key
	}
	return f.Section + "." + f.Key
}

// toGuidanceField converts a DsSchemaField to the slim GuidanceField the LLM receives.
// Allowed values are extracted from UI options (preferred) or allowedValues validations.
func toGuidanceField(f DsSchemaField) GuidanceField {
	gf := GuidanceField{
		Key:         f.Key,
		Label:       f.Label,
		Description: f.Description,
		Required:    f.Required,
		Type:        f.ValueType,
		Default:     f.DefaultVal,
	}
	if f.UI != nil && len(f.UI.Options) > 0 {
		for _, opt := range f.UI.Options {
			gf.AllowedValues = append(gf.AllowedValues, opt.Value)
		}
	} else {
		for _, v := range f.Validations {
			if v.Type == "allowedValues" {
				gf.AllowedValues = v.Values
				break
			}
		}
	}
	return gf
}

// appendSchemaFields appends the slim per-field guidance for the schema's
// type-specific fields, omitting virtual, sensitive, experimental, complex, and
// conditional-optional fields. The shared common fields are added by the caller.
func appendSchemaFields(fields []GuidanceField, schema *DatasourceSchema) []GuidanceField {
	for _, f := range schema.Fields {
		if f.Kind == "virtual" {
			continue
		}

		// Never surface sensitive fields (secrets, or PII/credential usernames).
		if f.Target == "secureJsonData" || IsExcludedField(f) {
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
		fields = append(fields, toGuidanceField(f))
	}
	return fields
}

// BuildSchemaGuidance builds the field guidance sent to the LLM for the given schema and tool, omitting virtual and sensitive fields.
func BuildSchemaGuidance(schema *DatasourceSchema, toolName string) *datasourceSchemaGuidance {
	fields := make([]GuidanceField, 0, len(commonDatasourceFields)+len(schema.Fields))
	for _, f := range commonDatasourceFields {
		fields = append(fields, toGuidanceField(f))
	}
	fields = appendSchemaFields(fields, schema)

	return &datasourceSchemaGuidance{
		Type:       schema.PluginType,
		PluginName: schema.PluginName,
		DocURL:     schema.DocURL,
		Message: fmt.Sprintf(
			"Schema for %s datasource. "+
				"You MUST ask the user for the value of every required field (required=true) before calling %s again. "+
				"Do NOT infer, guess, or use default values for required fields without explicit confirmation from the user. "+
				"For optional fields, ask only if they are relevant to the user's setup. "+
				"Once you have collected all required values from the user, call %s again with those values in the fields param and set schemaReviewed=true. "+
				"The datasource display name is a REQUIRED top-level `name` argument (separate from the fields map) — always include it. "+
				"If this datasource type has no required fields, schemaReviewed=true alone confirms you are ready to create it.",
			schema.PluginName,
			toolName,
			toolName,
		),
		Fields: fields,
	}
}

// BuildUpdateSchemaGuidance builds the field guidance for update_datasource. It
// mirrors BuildSchemaGuidance but tailors the message to update semantics —
// nothing is required and omitted fields keep their current value — and drops
// the uid common field, since a datasource's uid is its identifier and cannot be
// changed via an update.
func BuildUpdateSchemaGuidance(schema *DatasourceSchema) *datasourceSchemaGuidance {
	fields := make([]GuidanceField, 0, len(commonDatasourceFields)+len(schema.Fields))
	for _, f := range commonDatasourceFields {
		if f.Key == "uid" {
			continue
		}
		fields = append(fields, toGuidanceField(f))
	}
	fields = appendSchemaFields(fields, schema)

	// For updates nothing is required: omitted fields keep their current value.
	// Clear the create-time required flag so the structured guidance does not
	// contradict the message below (agents tend to trust the flag over prose).
	for i := range fields {
		fields[i].Required = false
	}

	return &datasourceSchemaGuidance{
		Type:       schema.PluginType,
		PluginName: schema.PluginName,
		DocURL:     schema.DocURL,
		Message: fmt.Sprintf(
			"Schema for %s datasource. "+
				"Only send the fields you want to change — any field you omit keeps its current value, so nothing here is required. "+
				"Ask the user which settings they want to change and confirm each new value; do NOT infer, guess, or reset fields the user did not mention. "+
				"Once you have collected the changes from the user, call update_datasource again with the uid, the changed values in the fields param, and schemaReviewed=true. "+
				"To rename the datasource, pass the new display name as the top-level `name` argument (separate from the fields map). "+
				"Secrets (passwords, tokens, and other secureJsonData) cannot be set here — direct the user to the Grafana UI to change those.",
			schema.PluginName,
		),
		Fields: fields,
	}
}
