package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

// Grafana IRM exposes both incident custom fields and incident labels through
// the same fields API, distinguished by their domain. Only the 'incident'
// domain holds the custom fields configured under incident response settings;
// labels live in the 'labels' domain and are handled by the label parameters on
// the incident tools instead.
const incidentCustomFieldDomain = "incident"

// incidentFieldTargetKind is the target kind used when reading and recording
// custom field values against an incident.
const incidentFieldTargetKind = "incident"

// Custom field types supported by Grafana IRM. Text and number fields store
// their value verbatim; select fields store select option UUIDs.
const (
	fieldTypeSingleSelect = "single-select"
	fieldTypeMultiSelect  = "multi-select"
)

// IncidentCustomFieldOption is one selectable option of a select custom field.
type IncidentCustomFieldOption struct {
	UUID        string `json:"uuid"`
	Label       string `json:"label"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// IncidentCustomFieldDefinition describes a custom field that can be set on an
// incident.
type IncidentCustomFieldDefinition struct {
	UUID        string                      `json:"uuid"`
	Name        string                      `json:"name"`
	Type        string                      `json:"type"`
	Description string                      `json:"description,omitempty"`
	Required    bool                        `json:"required,omitempty"`
	Archived    bool                        `json:"archived,omitempty"`
	Options     []IncidentCustomFieldOption `json:"options,omitempty"`
}

// IncidentCustomFieldValue is a custom field and its value on an incident, with
// the raw stored value decoded into something readable.
type IncidentCustomFieldValue struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// IncidentCustomFieldInput sets the value of a single custom field on an
// incident.
type IncidentCustomFieldInput struct {
	Field  string   `json:"field" jsonschema:"required,description=The name or UUID of the custom field to set. Use list_incident_custom_fields to discover the available fields"`
	Values []string `json:"values" jsonschema:"description=The value(s) to set. Text and number fields take a single value; single-select fields take one option; multi-select fields take one or more options. Select options may be given as their label\\, value or UUID. Pass an empty list to clear the field"`
}

type ListIncidentCustomFieldsParams struct {
	IncludeArchived bool `json:"includeArchived" jsonschema:"description=Whether to include archived custom fields. Archived fields cannot be set on new incidents but may still hold values on older ones"`
}

type ListIncidentCustomFieldsResult struct {
	Fields []IncidentCustomFieldDefinition `json:"fields"`
}

func listIncidentCustomFields(ctx context.Context, args ListIncidentCustomFieldsParams) (*ListIncidentCustomFieldsResult, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	fields, err := fetchIncidentCustomFields(ctx, c)
	if err != nil {
		return nil, err
	}

	definitions := make([]IncidentCustomFieldDefinition, 0, len(fields))
	for _, f := range fields {
		if f.Archived && !args.IncludeArchived {
			continue
		}
		definitions = append(definitions, describeIncidentCustomField(f))
	}
	return &ListIncidentCustomFieldsResult{Fields: definitions}, nil
}

var ListIncidentCustomFields = mcpgrafana.MustTool(
	"list_incident_custom_fields",
	"List the custom fields configured for Grafana incidents, including their type and, for select fields, the options that may be chosen. Use this to discover which fields exist and which values are valid before setting them with create_incident or update_incident.",
	listIncidentCustomFields,
	mcp.WithTitleAnnotation("List incident custom fields"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

func describeIncidentCustomField(f incident.CustomMetadataField) IncidentCustomFieldDefinition {
	options := make([]IncidentCustomFieldOption, 0, len(f.Selectoptions))
	for _, o := range f.Selectoptions {
		options = append(options, IncidentCustomFieldOption{
			UUID:        o.UUID,
			Label:       o.Label,
			Value:       o.Value,
			Description: o.Description,
		})
	}
	return IncidentCustomFieldDefinition{
		UUID:        f.UUID,
		Name:        f.Name,
		Type:        f.Type,
		Description: f.Description,
		Required:    f.Required,
		Archived:    f.Archived,
		Options:     options,
	}
}

// fetchIncidentCustomFields returns the custom field definitions in the
// incident domain.
func fetchIncidentCustomFields(ctx context.Context, c *incident.Client) ([]incident.CustomMetadataField, error) {
	fs := incident.NewFieldsService(c)
	resp, err := fs.GetFields(ctx, incident.GetFieldsRequest{DomainName: incidentCustomFieldDomain})
	if err != nil {
		return nil, fmt.Errorf("list incident custom fields: %w", err)
	}
	return resp.Fields, nil
}

// indexIncidentCustomFields keys field definitions by UUID so that the values
// returned alongside an incident can be matched to their definition.
func indexIncidentCustomFields(fields []incident.CustomMetadataField) map[string]incident.CustomMetadataField {
	byUUID := make(map[string]incident.CustomMetadataField, len(fields))
	for _, f := range fields {
		byUUID[f.UUID] = f
	}
	return byUUID
}

// incidentCustomFieldValues reads the custom field values recorded against an
// incident. The API returns each field's definition alongside its value, so no
// separate lookup is needed to decode select options.
func incidentCustomFieldValues(ctx context.Context, c *incident.Client, incidentID string) ([]IncidentCustomFieldValue, error) {
	fs := incident.NewFieldsService(c)
	resp, err := fs.GetFieldValues(ctx, incident.GetFieldValuesRequest{
		TargetKind: incidentFieldTargetKind,
		TargetID:   incidentID,
		DomainName: incidentCustomFieldDomain,
	})
	if err != nil {
		return nil, fmt.Errorf("get custom field values for incident %s: %w", incidentID, err)
	}

	values := make([]IncidentCustomFieldValue, 0, len(resp.FieldValues))
	for _, fv := range resp.FieldValues {
		values = append(values, IncidentCustomFieldValue{
			UUID:  fv.Field.UUID,
			Name:  fv.Field.Name,
			Type:  fv.Field.Type,
			Value: decodeIncidentCustomFieldValue(fv.Field, fv.Value),
		})
	}
	return values, nil
}

// summarizeIncidentCustomFieldValues decodes the raw field values attached to
// an incident preview. Values whose field is not in byUUID belong to another
// domain (incident labels share this API) and are skipped.
func summarizeIncidentCustomFieldValues(byUUID map[string]incident.CustomMetadataField, fieldValues []incident.CustomMetadataFieldValue) []IncidentCustomFieldValue {
	values := make([]IncidentCustomFieldValue, 0, len(fieldValues))
	for _, fv := range fieldValues {
		field, ok := byUUID[fv.FieldUUID]
		if !ok {
			continue
		}
		values = append(values, IncidentCustomFieldValue{
			UUID:  field.UUID,
			Name:  field.Name,
			Type:  field.Type,
			Value: decodeIncidentCustomFieldValue(field, fv.Value),
		})
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// decodeIncidentCustomFieldValue turns a raw stored value into something a
// caller can read. Select fields store select option UUIDs — a bare UUID for
// single-select, a JSON array of UUIDs for multi-select — which are mapped back
// to their labels. Text and number fields are stored verbatim.
func decodeIncidentCustomFieldValue(field incident.CustomMetadataField, raw string) any {
	if raw == "" {
		return nil
	}
	switch field.Type {
	case fieldTypeSingleSelect:
		return selectOptionLabel(field, raw)
	case fieldTypeMultiSelect:
		var uuids []string
		if err := json.Unmarshal([]byte(raw), &uuids); err != nil {
			// Not the encoding we expect; hand back what was stored rather
			// than dropping the value.
			return raw
		}
		labels := make([]string, 0, len(uuids))
		for _, uuid := range uuids {
			labels = append(labels, selectOptionLabel(field, uuid))
		}
		return labels
	default:
		return raw
	}
}

// selectOptionLabel maps a select option UUID to its label, falling back to the
// UUID itself for options that no longer exist on the field.
func selectOptionLabel(field incident.CustomMetadataField, uuid string) string {
	for _, o := range field.Selectoptions {
		if o.UUID == uuid {
			return o.Label
		}
	}
	return uuid
}

// preparedIncidentCustomField is a custom field value that has been resolved
// against its field definition and encoded, ready to be recorded.
type preparedIncidentCustomField struct {
	uuid  string
	name  string
	value string
}

// prepareIncidentCustomFields resolves the requested fields and encodes their
// values without writing anything.
//
// Preparing is kept separate from recording so that a bad field name, an
// unknown select option or too many values is rejected before any part of the
// incident is mutated — for create_incident in particular, failing afterwards
// would leave an incident behind that a retry would duplicate.
func prepareIncidentCustomFields(ctx context.Context, c *incident.Client, inputs []IncidentCustomFieldInput) ([]preparedIncidentCustomField, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	definitions, err := fetchIncidentCustomFields(ctx, c)
	if err != nil {
		return nil, err
	}

	prepared := make([]preparedIncidentCustomField, 0, len(inputs))
	for _, in := range inputs {
		field, err := resolveIncidentCustomField(definitions, in.Field)
		if err != nil {
			return nil, err
		}
		value, err := encodeIncidentCustomFieldValue(field, in.Values)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedIncidentCustomField{uuid: field.UUID, name: field.Name, value: value})
	}
	return prepared, nil
}

// recordIncidentCustomFields writes prepared custom field values to an
// incident.
//
// The API records one field at a time, so if a later field fails the earlier
// ones have already been written. The error names the field that failed.
func recordIncidentCustomFields(ctx context.Context, c *incident.Client, incidentID string, prepared []preparedIncidentCustomField) error {
	if len(prepared) == 0 {
		return nil
	}

	fs := incident.NewFieldsService(c)
	for _, field := range prepared {
		if _, err := fs.RecordFieldValue(ctx, incident.RecordFieldValueRequest{
			FieldUUID:  field.uuid,
			Value:      field.value,
			TargetKind: incidentFieldTargetKind,
			TargetID:   incidentID,
		}); err != nil {
			return fmt.Errorf("set custom field %q on incident %s: %w", field.name, incidentID, err)
		}
	}
	return nil
}

// resolveIncidentCustomField finds the field a caller referred to by UUID,
// name or slug.
func resolveIncidentCustomField(fields []incident.CustomMetadataField, ref string) (incident.CustomMetadataField, error) {
	if ref == "" {
		return incident.CustomMetadataField{}, fmt.Errorf("custom field name or UUID must not be empty")
	}
	for _, f := range fields {
		if f.UUID == ref {
			return f, nil
		}
	}
	for _, f := range fields {
		if strings.EqualFold(f.Name, ref) || strings.EqualFold(f.Slug, ref) {
			return f, nil
		}
	}

	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	if len(names) == 0 {
		return incident.CustomMetadataField{}, fmt.Errorf("no incident custom field %q: no custom fields are configured", ref)
	}
	return incident.CustomMetadataField{}, fmt.Errorf("no incident custom field %q, available fields are: %s", ref, strings.Join(names, ", "))
}

// encodeIncidentCustomFieldValue turns caller-supplied values into the wire
// format the field expects: select option UUIDs for select fields (a JSON array
// for multi-select), and the value verbatim for everything else. An empty list
// encodes to the empty string, which clears the field.
func encodeIncidentCustomFieldValue(field incident.CustomMetadataField, values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}

	switch field.Type {
	case fieldTypeMultiSelect:
		uuids := make([]string, 0, len(values))
		for _, v := range values {
			uuid, err := resolveSelectOption(field, v)
			if err != nil {
				return "", err
			}
			uuids = append(uuids, uuid)
		}
		encoded, err := json.Marshal(uuids)
		if err != nil {
			return "", fmt.Errorf("encode values for custom field %q: %w", field.Name, err)
		}
		return string(encoded), nil
	case fieldTypeSingleSelect:
		if len(values) > 1 {
			return "", fmt.Errorf("custom field %q is a single-select field and accepts at most one value, got %d", field.Name, len(values))
		}
		return resolveSelectOption(field, values[0])
	default:
		if len(values) > 1 {
			return "", fmt.Errorf("custom field %q is a %s field and accepts at most one value, got %d", field.Name, field.Type, len(values))
		}
		return values[0], nil
	}
}

// resolveSelectOption finds the UUID of the option a caller referred to by
// UUID, label or value.
func resolveSelectOption(field incident.CustomMetadataField, value string) (string, error) {
	for _, o := range field.Selectoptions {
		if o.UUID == value {
			return o.UUID, nil
		}
	}
	for _, o := range field.Selectoptions {
		if strings.EqualFold(o.Label, value) || strings.EqualFold(o.Value, value) {
			return o.UUID, nil
		}
	}

	labels := make([]string, 0, len(field.Selectoptions))
	for _, o := range field.Selectoptions {
		labels = append(labels, o.Label)
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("custom field %q has no option %q: the field has no options", field.Name, value)
	}
	return "", fmt.Errorf("custom field %q has no option %q, valid options are: %s", field.Name, value, strings.Join(labels, ", "))
}
