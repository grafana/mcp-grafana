//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stub client shipped with incident-go cannot be used for the fields API:
// its canned responses encode booleans as strings and fail to unmarshal. These
// tests therefore drive a real client against a fake server, which also lets
// them assert on what was sent.

type fakeIncidentCall struct {
	Method string
	Body   json.RawMessage
}

type fakeIncidentServer struct {
	Calls []fakeIncidentCall
}

// fakeIncidentError, used in place of a response, makes the fake server reject
// that call so tests can exercise failure handling.
type fakeIncidentError struct {
	message string
}

// newFakeIncidentClient serves the given responses, keyed by the RPC method
// name the incident client appends to its base URL (e.g. "FieldsService.GetFields").
func newFakeIncidentClient(t *testing.T, responses map[string]any) (context.Context, *fakeIncidentServer) {
	t.Helper()
	fake := &fakeIncidentServer{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		fake.Calls = append(fake.Calls, fakeIncidentCall{Method: method, Body: body})

		response, ok := responses[method]
		if !ok {
			t.Errorf("unexpected call to %s", method)
			http.Error(w, "unexpected call to "+method, http.StatusNotFound)
			return
		}
		if failure, ok := response.(fakeIncidentError); ok {
			http.Error(w, failure.message, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	t.Cleanup(server.Close)

	client := incident.NewClient(server.URL+"/", "test-token")
	return mcpgrafana.WithIncidentClient(context.Background(), client), fake
}

// bodyFor returns the decoded request body of the first call to method.
func (f *fakeIncidentServer) bodyFor(t *testing.T, method string, dest any) {
	t.Helper()
	for _, call := range f.Calls {
		if call.Method == method {
			require.NoError(t, json.Unmarshal(call.Body, dest))
			return
		}
	}
	t.Fatalf("no call to %s was made", method)
}

func (f *fakeIncidentServer) countOf(method string) int {
	count := 0
	for _, call := range f.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

func singleSelectField() incident.CustomMetadataField {
	return incident.CustomMetadataField{
		UUID:       "field-debrief",
		Name:       "Debrief Status",
		Slug:       "debrief_status",
		Type:       fieldTypeSingleSelect,
		DomainName: incidentCustomFieldDomain,
		Required:   true,
		Selectoptions: []incident.CustomMetadataFieldSelectOption{
			{UUID: "opt-completed", Label: "Report completed", Value: "completed"},
			{UUID: "opt-in-progress", Label: "Report in progress", Value: "in_progress"},
		},
	}
}

func multiSelectField() incident.CustomMetadataField {
	return incident.CustomMetadataField{
		UUID:       "field-security",
		Name:       "Security Category",
		Slug:       "security_category",
		Type:       fieldTypeMultiSelect,
		DomainName: incidentCustomFieldDomain,
		Selectoptions: []incident.CustomMetadataFieldSelectOption{
			{UUID: "opt-injection", Label: "Injection", Value: "injection"},
			{UUID: "opt-ssrf", Label: "SSRF", Value: "ssrf"},
		},
	}
}

func textField() incident.CustomMetadataField {
	return incident.CustomMetadataField{
		UUID:       "field-impact",
		Name:       "Customer Impact",
		Slug:       "customer_impact",
		Type:       "text",
		DomainName: incidentCustomFieldDomain,
	}
}

func archivedField() incident.CustomMetadataField {
	field := textField()
	field.UUID = "field-legacy"
	field.Name = "Legacy Notes"
	field.Slug = "legacy_notes"
	field.Archived = true
	return field
}

// customFieldResponses is the set of fake responses needed by tools that read
// or write custom fields.
func customFieldResponses() map[string]any {
	return map[string]any{
		"FieldsService.GetFields": incident.GetFieldsResponse{
			Fields: []incident.CustomMetadataField{singleSelectField(), multiSelectField(), textField(), archivedField()},
		},
		"FieldsService.GetFieldValues": incident.GetFieldValuesResponse{
			FieldValues: []incident.FieldValue{
				{Field: singleSelectField(), Value: "opt-completed"},
				{Field: multiSelectField(), Value: `["opt-ssrf"]`},
				{Field: textField(), Value: ""},
			},
		},
		"FieldsService.RecordFieldValue": incident.RecordFieldValueResponse{},
	}
}

func TestListIncidentCustomFieldsTool(t *testing.T) {
	t.Run("returns the field definitions with their options", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, customFieldResponses())

		result, err := listIncidentCustomFields(ctx, ListIncidentCustomFieldsParams{})
		require.NoError(t, err)
		require.Len(t, result.Fields, 3, "archived fields should be excluded by default")

		assert.Equal(t, "Debrief Status", result.Fields[0].Name)
		assert.Equal(t, fieldTypeSingleSelect, result.Fields[0].Type)
		assert.True(t, result.Fields[0].Required)
		require.Len(t, result.Fields[0].Options, 2)
		assert.Equal(t, "Report completed", result.Fields[0].Options[0].Label)
		assert.Equal(t, "completed", result.Fields[0].Options[0].Value)

		// Only the custom field domain is requested; incident labels live in
		// their own domain and are surfaced as labels instead.
		var request incident.GetFieldsRequest
		fake.bodyFor(t, "FieldsService.GetFields", &request)
		assert.Equal(t, incidentCustomFieldDomain, request.DomainName)
	})

	t.Run("includes archived fields on request", func(t *testing.T) {
		ctx, _ := newFakeIncidentClient(t, customFieldResponses())

		result, err := listIncidentCustomFields(ctx, ListIncidentCustomFieldsParams{IncludeArchived: true})
		require.NoError(t, err)
		require.Len(t, result.Fields, 4)
		assert.Equal(t, "Legacy Notes", result.Fields[3].Name)
		assert.True(t, result.Fields[3].Archived)
	})
}

func TestGetIncidentCustomFields(t *testing.T) {
	responses := customFieldResponses()
	responses["IncidentsService.GetIncident"] = incident.GetIncidentResponse{
		Incident: incident.Incident{IncidentID: "123", Title: "checkout is down", Status: "active"},
	}
	ctx, fake := newFakeIncidentClient(t, responses)

	result, err := getIncident(ctx, GetIncidentParams{ID: "123"})
	require.NoError(t, err)
	assert.Equal(t, "checkout is down", result.Title)
	require.Len(t, result.CustomFields, 3)

	assert.Equal(t, "Debrief Status", result.CustomFields[0].Name)
	assert.Equal(t, "Report completed", result.CustomFields[0].Value)
	assert.Equal(t, []string{"SSRF"}, result.CustomFields[1].Value)
	assert.Nil(t, result.CustomFields[2].Value, "an unset field should decode to null")

	var request incident.GetFieldValuesRequest
	fake.bodyFor(t, "FieldsService.GetFieldValues", &request)
	assert.Equal(t, "incident", request.TargetKind)
	assert.Equal(t, "123", request.TargetID)
	assert.Equal(t, incidentCustomFieldDomain, request.DomainName)

	// The raw fieldValues list is replaced by the resolved customFields.
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"fieldValues"`)
	assert.Contains(t, string(encoded), `"customFields"`)
}

func TestListIncidentsCustomFields(t *testing.T) {
	responses := customFieldResponses()
	responses["IncidentsService.QueryIncidentPreviews"] = incident.QueryIncidentPreviewsResponse{
		IncidentPreviews: []incident.IncidentPreview{
			{
				IncidentID: "123",
				Title:      "checkout is down",
				FieldValues: []incident.CustomMetadataFieldValue{
					{FieldUUID: "field-debrief", Value: "opt-in-progress"},
					{FieldUUID: "field-security", Value: `["opt-injection","opt-ssrf"]`},
					// Incident labels come back through the same list.
					{FieldUUID: "some-label-field", Value: `["whatever"]`},
				},
			},
		},
	}

	t.Run("off by default", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		result, err := listIncidents(ctx, ListIncidentsParams{Limit: 1})
		require.NoError(t, err)
		require.Len(t, result.Incidents, 1)
		assert.Nil(t, result.Incidents[0].CustomFields)
		assert.Zero(t, fake.countOf("FieldsService.GetFields"), "field definitions should not be fetched")

		var request incident.QueryIncidentPreviewsRequest
		fake.bodyFor(t, "IncidentsService.QueryIncidentPreviews", &request)
		assert.False(t, request.IncludeCustomFieldValues)
	})

	t.Run("resolves names and values when requested", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		result, err := listIncidents(ctx, ListIncidentsParams{Limit: 1, IncludeCustomFields: true})
		require.NoError(t, err)
		require.Len(t, result.Incidents, 1)

		customFields := result.Incidents[0].CustomFields
		require.Len(t, customFields, 2, "fields from other domains should be skipped")
		assert.Equal(t, "Debrief Status", customFields[0].Name)
		assert.Equal(t, "Report in progress", customFields[0].Value)
		assert.Equal(t, "Security Category", customFields[1].Name)
		assert.Equal(t, []string{"Injection", "SSRF"}, customFields[1].Value)

		var request incident.QueryIncidentPreviewsRequest
		fake.bodyFor(t, "IncidentsService.QueryIncidentPreviews", &request)
		assert.True(t, request.IncludeCustomFieldValues)
	})
}

func TestCreateIncidentCustomFields(t *testing.T) {
	responses := customFieldResponses()
	responses["IncidentsService.CreateIncident"] = incident.CreateIncidentResponse{
		Incident: incident.Incident{IncidentID: "123", Title: "checkout is down"},
	}

	t.Run("records the requested fields on the new incident", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		result, err := createIncident(ctx, CreateIncidentParams{
			Title:      "checkout is down",
			Severity:   "minor",
			RoomPrefix: "test",
			CustomFields: []IncidentCustomFieldInput{
				{Field: "Security Category", Values: []string{"Injection", "ssrf"}},
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.CustomFields)

		var request incident.RecordFieldValueRequest
		fake.bodyFor(t, "FieldsService.RecordFieldValue", &request)
		assert.Equal(t, "field-security", request.FieldUUID)
		assert.Equal(t, "incident", request.TargetKind)
		assert.Equal(t, "123", request.TargetID)
		assert.JSONEq(t, `["opt-injection","opt-ssrf"]`, request.Value)
	})

	t.Run("rejects an unknown field before creating the incident", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		_, err := createIncident(ctx, CreateIncidentParams{
			Title:      "checkout is down",
			Severity:   "minor",
			RoomPrefix: "test",
			CustomFields: []IncidentCustomFieldInput{
				{Field: "Blast Radius", Values: []string{"global"}},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `no incident custom field "Blast Radius"`)
		// Creating the incident is what notifies people, so a bad field must
		// not leave one behind for a retry to duplicate.
		assert.Zero(t, fake.countOf("IncidentsService.CreateIncident"))
		assert.Zero(t, fake.countOf("FieldsService.RecordFieldValue"))
	})

	t.Run("says the incident exists when recording a field fails", func(t *testing.T) {
		failing := customFieldResponses()
		failing["FieldsService.RecordFieldValue"] = fakeIncidentError{message: "fields service is down"}
		failing["IncidentsService.CreateIncident"] = responses["IncidentsService.CreateIncident"]
		ctx, _ := newFakeIncidentClient(t, failing)

		_, err := createIncident(ctx, CreateIncidentParams{
			Title:      "checkout is down",
			Severity:   "minor",
			RoomPrefix: "test",
			CustomFields: []IncidentCustomFieldInput{
				{Field: "Customer Impact", Values: []string{"unknown"}},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "incident 123 was created, do not retry")
	})
}

func TestUpdateIncidentCustomFields(t *testing.T) {
	responses := customFieldResponses()
	responses["IncidentsService.GetIncident"] = incident.GetIncidentResponse{
		Incident: incident.Incident{IncidentID: "123", Title: "checkout is down"},
	}

	t.Run("records fields and reads the incident back", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		result, err := updateIncident(ctx, UpdateIncidentParams{
			IncidentID: "123",
			CustomFields: []IncidentCustomFieldInput{
				{Field: "debrief_status", Values: []string{"Report completed"}},
				{Field: "Customer Impact", Values: []string{"20 minutes of failed checkouts"}},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "123", result.IncidentID)
		require.Len(t, result.CustomFields, 3)
		assert.Equal(t, 2, fake.countOf("FieldsService.RecordFieldValue"))
		assert.Equal(t, 1, fake.countOf("IncidentsService.GetIncident"), "the incident is fetched once no other endpoint returned it")
	})

	t.Run("clears a field when given no values", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		_, err := updateIncident(ctx, UpdateIncidentParams{
			IncidentID:   "123",
			CustomFields: []IncidentCustomFieldInput{{Field: "Customer Impact"}},
		})
		require.NoError(t, err)

		var request incident.RecordFieldValueRequest
		fake.bodyFor(t, "FieldsService.RecordFieldValue", &request)
		assert.Equal(t, "field-impact", request.FieldUUID)
		assert.Empty(t, request.Value)
	})

	t.Run("rejects too many values for a single-select field before changing anything", func(t *testing.T) {
		ctx, fake := newFakeIncidentClient(t, responses)

		_, err := updateIncident(ctx, UpdateIncidentParams{
			IncidentID: "123",
			Status:     "resolved",
			CustomFields: []IncidentCustomFieldInput{
				{Field: "Debrief Status", Values: []string{"Report completed", "Report in progress"}},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "accepts at most one value")
		assert.Zero(t, fake.countOf("FieldsService.RecordFieldValue"))
		assert.Zero(t, fake.countOf("IncidentsService.UpdateStatus"), "the status must not change when a custom field is invalid")
	})
}

func TestEncodeIncidentCustomFieldValue(t *testing.T) {
	t.Run("single-select resolves an option label to its UUID", func(t *testing.T) {
		value, err := encodeIncidentCustomFieldValue(singleSelectField(), []string{"Report completed"})
		require.NoError(t, err)
		assert.Equal(t, "opt-completed", value)
	})

	t.Run("single-select accepts an option value or UUID", func(t *testing.T) {
		value, err := encodeIncidentCustomFieldValue(singleSelectField(), []string{"in_progress"})
		require.NoError(t, err)
		assert.Equal(t, "opt-in-progress", value)

		value, err = encodeIncidentCustomFieldValue(singleSelectField(), []string{"opt-in-progress"})
		require.NoError(t, err)
		assert.Equal(t, "opt-in-progress", value)
	})

	t.Run("option matching is case-insensitive", func(t *testing.T) {
		value, err := encodeIncidentCustomFieldValue(singleSelectField(), []string{"report COMPLETED"})
		require.NoError(t, err)
		assert.Equal(t, "opt-completed", value)
	})

	t.Run("multi-select encodes a JSON array of option UUIDs", func(t *testing.T) {
		value, err := encodeIncidentCustomFieldValue(multiSelectField(), []string{"Injection", "ssrf"})
		require.NoError(t, err)
		assert.JSONEq(t, `["opt-injection","opt-ssrf"]`, value)
	})

	t.Run("text fields are passed through verbatim", func(t *testing.T) {
		value, err := encodeIncidentCustomFieldValue(textField(), []string{"Checkout was down for 20 minutes"})
		require.NoError(t, err)
		assert.Equal(t, "Checkout was down for 20 minutes", value)
	})

	t.Run("an empty list clears the field", func(t *testing.T) {
		for _, field := range []incident.CustomMetadataField{singleSelectField(), multiSelectField(), textField()} {
			value, err := encodeIncidentCustomFieldValue(field, nil)
			require.NoError(t, err)
			assert.Empty(t, value)
		}
	})

	t.Run("an unknown option is rejected and the valid ones listed", func(t *testing.T) {
		_, err := encodeIncidentCustomFieldValue(multiSelectField(), []string{"Nonsense"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `has no option "Nonsense"`)
		assert.Contains(t, err.Error(), "Injection, SSRF")
	})

	t.Run("a text field rejects more than one value", func(t *testing.T) {
		_, err := encodeIncidentCustomFieldValue(textField(), []string{"one", "two"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "accepts at most one value")
	})
}

func TestDecodeIncidentCustomFieldValue(t *testing.T) {
	t.Run("single-select maps the stored UUID back to its label", func(t *testing.T) {
		assert.Equal(t, "Report completed", decodeIncidentCustomFieldValue(singleSelectField(), "opt-completed"))
	})

	t.Run("multi-select maps each stored UUID back to its label", func(t *testing.T) {
		assert.Equal(t, []string{"Injection", "SSRF"}, decodeIncidentCustomFieldValue(multiSelectField(), `["opt-injection","opt-ssrf"]`))
	})

	t.Run("an unset value decodes to nil", func(t *testing.T) {
		assert.Nil(t, decodeIncidentCustomFieldValue(singleSelectField(), ""))
	})

	t.Run("an option that no longer exists falls back to its UUID", func(t *testing.T) {
		assert.Equal(t, "opt-deleted", decodeIncidentCustomFieldValue(singleSelectField(), "opt-deleted"))
	})

	t.Run("a multi-select value that isn't a JSON array is returned as stored", func(t *testing.T) {
		assert.Equal(t, "opt-injection", decodeIncidentCustomFieldValue(multiSelectField(), "opt-injection"))
	})

	t.Run("text values are returned verbatim", func(t *testing.T) {
		assert.Equal(t, "20 minutes", decodeIncidentCustomFieldValue(textField(), "20 minutes"))
	})
}

func TestResolveIncidentCustomField(t *testing.T) {
	fields := []incident.CustomMetadataField{singleSelectField(), multiSelectField()}

	t.Run("resolves by name, slug and UUID", func(t *testing.T) {
		for _, ref := range []string{"Security Category", "security_category", "field-security"} {
			field, err := resolveIncidentCustomField(fields, ref)
			require.NoError(t, err, "ref %q", ref)
			assert.Equal(t, "field-security", field.UUID)
		}
	})

	t.Run("name matching is case-insensitive", func(t *testing.T) {
		field, err := resolveIncidentCustomField(fields, "security category")
		require.NoError(t, err)
		assert.Equal(t, "field-security", field.UUID)
	})

	t.Run("an unknown field is rejected and the known ones listed", func(t *testing.T) {
		_, err := resolveIncidentCustomField(fields, "Blast Radius")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `no incident custom field "Blast Radius"`)
		assert.Contains(t, err.Error(), "Debrief Status, Security Category")
	})

	t.Run("reports when no custom fields are configured", func(t *testing.T) {
		_, err := resolveIncidentCustomField(nil, "Blast Radius")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no custom fields are configured")
	})
}
