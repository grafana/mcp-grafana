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

// SchemaInstruction is a free-text directive attached to a schema, used to
// convey guidance the structured fields can't capture (e.g. "this datasource
// supports basic auth, no auth, or forwarded OAuth — ask the user which to
// use"). Only instructions tagged for the LLM are surfaced in guidance; see
// instructionTagLLM.
type SchemaInstruction struct {
	Message string   `json:"message"`
	Tags    []string `json:"tags,omitempty"`
}

type DatasourceSchema struct {
	PluginType   string              `json:"pluginType"`
	PluginName   string              `json:"pluginName"`
	DocURL       string              `json:"docURL"`
	Fields       []DsSchemaField     `json:"fields"`
	Instructions []SchemaInstruction `json:"instructions,omitempty"`
}

// instructionTagLLM marks a SchemaInstruction as intended for the agent. Only
// instructions carrying this tag are stripped out of the schema and surfaced in
// the guidance sent to the LLM.
const instructionTagLLM = "llm"

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
	// DependsOn, when set, is the condition under which this field is relevant
	// (e.g. "jsonData.selectedAuthType == 'github-app'"). The agent should only
	// collect and send the field when the condition holds — typically after the
	// user has chosen an auth method or other branching option.
	DependsOn string `json:"dependsOn,omitempty"`
}

type datasourceSchemaGuidance struct {
	Type       string          `json:"type"`
	PluginName string          `json:"plugin_name"`
	DocURL     string          `json:"doc_url,omitempty"`
	Message    string          `json:"message"`
	// Instructions are free-text, schema-authored directives (e.g. which auth
	// methods are supported and how to pick one). They complement the structured
	// Fields for cases the field schema can't express on its own.
	Instructions []string        `json:"instructions,omitempty"`
	Fields       []GuidanceField `json:"fields"`
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
		DependsOn:   f.DependsOn,
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

// BuildSchemaGuidance builds the field guidance sent to the LLM for the given schema and tool, omitting virtual and sensitive fields.
func BuildSchemaGuidance(schema *DatasourceSchema, toolName string) *datasourceSchemaGuidance {
	fields := make([]GuidanceField, 0, len(commonDatasourceFields)+len(schema.Fields))
	for _, f := range commonDatasourceFields {
		fields = append(fields, toGuidanceField(f))
	}

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
		// Conditional fields (those with a dependsOn) are kept and surfaced with
		// their condition in GuidanceField.DependsOn. Many auth branches live on
		// optional conditional fields — e.g. a GitHub App's appId/installationId
		// only apply when selectedAuthType == 'github-app' — so dropping them
		// would leave the agent unable to complete that auth path.

		if f.Target != "root" && f.Target != "jsonData" {
			continue
		}

		f.Key = SchemaFieldInputKey(f)
		fields = append(fields, toGuidanceField(f))
	}

	return &datasourceSchemaGuidance{
		Type:       schema.PluginType,
		PluginName: schema.PluginName,
		DocURL:     schema.DocURL,
		Message: fmt.Sprintf(
			"Schema for %s datasource. "+
				"You MUST ask the user for the value of every required field (required=true) before calling %s again. "+
				"Do NOT infer, guess, or use default values for required fields without explicit confirmation from the user. "+
				"For optional fields, ask only if they are relevant to the user's setup. "+
				"Some fields are conditional: a field with a `dependsOn` condition applies only when that condition holds (typically after the user picks an auth method or similar option). Ask for and send a conditional field only once its condition is satisfied. "+
				"Follow any directives in the `instructions` array — they describe choices (such as which authentication method to use) that the fields alone don't capture. "+
				"Once you have collected all required values from the user, call %s again with those values in the fields param and set schemaReviewed=true. "+
				"The datasource display name is a REQUIRED top-level `name` argument (separate from the fields map) — always include it. "+
				"If this datasource type has no required fields, schemaReviewed=true alone confirms you are ready to create it.",
			schema.PluginName,
			toolName,
			toolName,
		),
		Instructions: llmInstructions(schema.Instructions),
		Fields:       fields,
	}
}

// llmInstructions returns the messages of instructions tagged for the LLM, in
// schema order. Returns nil when there are none so the field is omitted from
// the guidance JSON.
func llmInstructions(instructions []SchemaInstruction) []string {
	var out []string
	for _, ins := range instructions {
		for _, tag := range ins.Tags {
			if tag == instructionTagLLM {
				out = append(out, ins.Message)
				break
			}
		}
	}
	return out
}
