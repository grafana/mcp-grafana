//go:build unit

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/require"
)

// storedAlertRuleJSON is an alert rule as the provisioning API serves it, with
// every field an update could reset populated with a non-default value.
const storedAlertRuleJSON = `{
  "id": 42,
  "uid": "rule-1",
  "orgID": 7,
  "folderUID": "folder-a",
  "ruleGroup": "group-a",
  "title": "High CPU",
  "condition": "C",
  "data": [
    {
      "refId": "A",
      "relativeTimeRange": {"from": 600, "to": 0},
      "datasourceUid": "prom-uid",
      "model": {"expr": "avg(rate(cpu_seconds_total[5m]))", "intervalMs": 1000, "maxDataPoints": 43200, "refId": "A"}
    },
    {
      "refId": "C",
      "relativeTimeRange": {"from": 600, "to": 0},
      "datasourceUid": "__expr__",
      "model": {"type": "threshold", "expression": "A", "refId": "C", "conditions": [{"evaluator": {"type": "gt", "params": [0.8]}}]}
    }
  ],
  "updated": "2026-01-02T03:04:05Z",
  "noDataState": "Alerting",
  "execErrState": "Error",
  "for": "5m",
  "keep_firing_for": "2m",
  "labels": {"severity": "critical", "team": "infra"},
  "annotations": {"summary": "CPU is high", "runbook_url": "https://example.com/runbook"},
  "isPaused": false,
  "notification_settings": {
    "receiver": "oncall",
    "group_by": ["alertname", "grafana_folder"],
    "group_wait": "30s",
    "group_interval": "5m",
    "repeat_interval": "4h",
    "mute_time_intervals": ["weekends"],
    "active_time_intervals": ["business-hours"]
  },
  "missingSeriesEvalsToResolve": 3
}`

// storedRecordingRuleJSON is a recording rule as the provisioning API serves
// it: noDataState and execErrState come back empty because Grafana ignores them.
const storedRecordingRuleJSON = `{
  "id": 43,
  "uid": "rule-1",
  "orgID": 7,
  "folderUID": "folder-a",
  "ruleGroup": "group-a",
  "title": "cpu:rate5m",
  "condition": "",
  "data": [
    {
      "refId": "A",
      "relativeTimeRange": {"from": 600, "to": 0},
      "datasourceUid": "prom-uid",
      "model": {"expr": "avg(rate(cpu_seconds_total[5m]))", "refId": "A"}
    }
  ],
  "updated": "2026-01-02T03:04:05Z",
  "noDataState": "",
  "execErrState": "",
  "for": "0s",
  "labels": {"team": "infra"},
  "record": {"from": "A", "metric": "cpu:rate5m", "target_datasource_uid": "prom-uid"}
}`

// alertRuleUpdateRecorder is a fake provisioning API holding one rule. It
// serves the stored rule on GET and records the body of every PUT.
type alertRuleUpdateRecorder struct {
	mu        sync.Mutex
	stored    string
	getStatus int
	gets      int
	putBodies [][]byte
	putHeader http.Header
}

func (rec *alertRuleUpdateRecorder) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if r.URL.Path != "/api/v1/provisioning/alert-rules/rule-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			rec.gets++
			if rec.getStatus != 0 {
				w.WriteHeader(rec.getStatus)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			_, _ = w.Write([]byte(rec.stored))
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading PUT body: %v", err)
			}
			rec.putBodies = append(rec.putBodies, body)
			rec.putHeader = r.Header.Clone()
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func newAlertRuleUpdateRecorder(t *testing.T, stored string) (*alertRuleUpdateRecorder, context.Context) {
	t.Helper()
	rec := &alertRuleUpdateRecorder{stored: stored}
	server := httptest.NewServer(rec.handler(t))
	t.Cleanup(server.Close)
	return rec, mockCtxWithClient(server)
}

// canonicalJSON re-encodes a JSON value with sorted object keys and no
// insignificant whitespace, keeping numbers exactly as written.
func canonicalJSON(t *testing.T, raw []byte) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v))
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return string(out)
}

func jsonFields(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

// clientEncoding returns the rule exactly as the OpenAPI client would send it
// back unchanged, after applying mutate. It is the reference for a whole-body
// comparison of the update request.
func clientEncoding(t *testing.T, stored string, mutate func(*models.ProvisionedAlertRule)) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stored))
	dec.UseNumber()
	var rule models.ProvisionedAlertRule
	require.NoError(t, dec.Decode(&rule))
	if mutate != nil {
		mutate(&rule)
	}
	out, err := json.Marshal(&rule)
	require.NoError(t, err)
	return canonicalJSON(t, out)
}

// pruneZero removes object members holding a JSON zero value (false, 0, "" or
// null). The OpenAPI client model omits such members when it encodes a rule
// ("isPaused": false, "relativeTimeRange.to": 0) and Grafana reads an absent
// member as that same zero, so absent and zero are the same stored value.
func pruneZero(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, member := range val {
			switch m := member.(type) {
			case nil:
				continue
			case bool:
				if !m {
					continue
				}
			case string:
				if m == "" {
					continue
				}
			case json.Number:
				if f, err := m.Float64(); err == nil && f == 0 {
					continue
				}
			}
			out[k] = pruneZero(member)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = pruneZero(item)
		}
		return out
	default:
		return v
	}
}

// storedValueJSON is canonicalJSON with zero-valued object members dropped, so
// two encodings of the same stored value compare equal byte for byte.
func storedValueJSON(t *testing.T, raw []byte) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v))
	out, err := json.Marshal(pruneZero(map[string]any{"v": v}))
	require.NoError(t, err)
	return string(out)
}

// requireFieldsUnchanged asserts that every field of the stored rule other than
// those in changed reaches the PUT with the value the API served, compared as
// canonical JSON bytes. Two encodings differ without the value differing: the
// client model omits zero values (see pruneZero) and re-encodes durations and
// timestamps ("5m" becomes "5m0s"), so those are compared by value.
func requireFieldsUnchanged(t *testing.T, stored string, put []byte, changed ...string) {
	t.Helper()
	skip := make(map[string]bool, len(changed))
	for _, f := range changed {
		skip[f] = true
	}
	storedFields := jsonFields(t, []byte(stored))
	putFields := jsonFields(t, put)

	for name, want := range storedFields {
		if skip[name] {
			continue
		}
		got, ok := putFields[name]
		if !ok {
			require.Equalf(t, `{}`, storedValueJSON(t, want), "field %q of the stored rule is missing from the update request", name)
			continue
		}
		switch name {
		case "for", "keep_firing_for":
			var wantStr, gotStr string
			require.NoError(t, json.Unmarshal(want, &wantStr))
			require.NoError(t, json.Unmarshal(got, &gotStr))
			wantDur, err := time.ParseDuration(wantStr)
			require.NoError(t, err)
			gotDur, err := time.ParseDuration(gotStr)
			require.NoError(t, err)
			require.Equalf(t, wantDur, gotDur, "field %q changed", name)
		case "updated":
			var wantTime, gotTime time.Time
			require.NoError(t, json.Unmarshal(want, &wantTime))
			require.NoError(t, json.Unmarshal(got, &gotTime))
			require.Truef(t, wantTime.Equal(gotTime), "field %q changed: %s != %s", name, wantTime, gotTime)
		default:
			require.Equalf(t, storedValueJSON(t, want), storedValueJSON(t, got), "field %q changed", name)
		}
	}
	for name := range putFields {
		if _, ok := storedFields[name]; !ok && !skip[name] {
			t.Errorf("update request added field %q that the stored rule does not have: %s", name, putFields[name])
		}
	}
}

func TestManageRulesWrite_UpdateIsPartial(t *testing.T) {
	// The fields the tool must never touch unless the caller supplies them.
	preserved := []string{
		"title", "condition", "data", "folderUID", "ruleGroup", "labels", "annotations",
		"notification_settings", "for", "keep_firing_for", "noDataState", "execErrState",
		"orgID", "isPaused", "missingSeriesEvalsToResolve",
	}
	storedFields := jsonFields(t, []byte(storedAlertRuleJSON))
	for _, name := range preserved {
		require.Containsf(t, storedFields, name, "fixture must populate %q", name)
	}

	paused := true
	tests := []struct {
		name    string
		params  ManageRulesWriteParams
		changed string
		want    string
		mutate  func(*models.ProvisionedAlertRule)
	}{
		{
			name:    "only is_paused",
			params:  ManageRulesWriteParams{IsPaused: &paused},
			changed: "isPaused",
			want:    `true`,
			mutate:  func(r *models.ProvisionedAlertRule) { r.IsPaused = true },
		},
		{
			name:    "only title",
			params:  ManageRulesWriteParams{Title: "High CPU (renamed)"},
			changed: "title",
			want:    `"High CPU (renamed)"`,
			mutate: func(r *models.ProvisionedAlertRule) {
				title := "High CPU (renamed)"
				r.Title = &title
			},
		},
		{
			name:    "only labels",
			params:  ManageRulesWriteParams{Labels: map[string]string{"severity": "warning"}},
			changed: "labels",
			want:    `{"severity":"warning"}`,
			mutate: func(r *models.ProvisionedAlertRule) {
				r.Labels = map[string]string{"severity": "warning"}
			},
		},
		{
			name:    "only no_data_state",
			params:  ManageRulesWriteParams{NoDataState: "OK"},
			changed: "noDataState",
			want:    `"OK"`,
			mutate: func(r *models.ProvisionedAlertRule) {
				state := "OK"
				r.NoDataState = &state
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)

			params := tc.params
			params.Operation = "update"
			params.RuleUID = "rule-1"
			_, err := manageRulesWrite(ctx, params)
			require.NoError(t, err)

			require.Equal(t, 1, rec.gets, "update must read the existing rule once")
			require.Len(t, rec.putBodies, 1, "update must issue exactly one PUT")
			put := rec.putBodies[0]
			require.Equal(t, "true", rec.putHeader.Get("X-Disable-Provenance"))

			// The supplied field carries the new value...
			require.Equal(t, tc.want, canonicalJSON(t, jsonFields(t, put)[tc.changed]))
			// ...every other field is what was stored...
			requireFieldsUnchanged(t, storedAlertRuleJSON, put, tc.changed)
			// ...and the request body as a whole equals the stored rule with
			// only that one field changed.
			require.Equal(t, clientEncoding(t, storedAlertRuleJSON, tc.mutate), canonicalJSON(t, put))
		})
	}

	t.Run("update with no fields resends the stored rule unchanged", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)

		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{Operation: "update", RuleUID: "rule-1"})
		require.NoError(t, err)

		require.Len(t, rec.putBodies, 1)
		requireFieldsUnchanged(t, storedAlertRuleJSON, rec.putBodies[0])
		require.Equal(t, clientEncoding(t, storedAlertRuleJSON, nil), canonicalJSON(t, rec.putBodies[0]))
	})

	t.Run("every supplied field is applied", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)

		evals := int64(5)
		disableProvenance := false
		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{
			Operation:                   "update",
			RuleUID:                     "rule-1",
			Title:                       "New title",
			RuleGroup:                   "group-b",
			FolderUID:                   "folder-b",
			Condition:                   "B",
			Data:                        []map[string]any{{"refId": "B", "datasourceUid": "other-prom", "model": map[string]any{"expr": "up == 0"}}},
			NoDataState:                 "OK",
			ExecErrState:                "OK",
			For:                         "10m",
			KeepFiringFor:               "1m",
			IsPaused:                    &paused,
			NotificationSettings:        map[string]any{"receiver": "email"},
			MissingSeriesEvalsToResolve: &evals,
			Annotations:                 map[string]string{"summary": "changed"},
			Labels:                      map[string]string{"severity": "warning"},
			OrgID:                       9,
			DisableProvenance:           &disableProvenance,
		})
		require.NoError(t, err)

		require.Len(t, rec.putBodies, 1)
		require.Empty(t, rec.putHeader.Get("X-Disable-Provenance"))
		var sent models.ProvisionedAlertRule
		require.NoError(t, json.Unmarshal(rec.putBodies[0], &sent))
		require.Equal(t, "rule-1", sent.UID)
		require.Equal(t, "New title", *sent.Title)
		require.Equal(t, "group-b", *sent.RuleGroup)
		require.Equal(t, "folder-b", *sent.FolderUID)
		require.Equal(t, "B", *sent.Condition)
		require.Len(t, sent.Data, 1)
		require.Equal(t, "other-prom", sent.Data[0].DatasourceUID)
		require.Equal(t, "OK", *sent.NoDataState)
		require.Equal(t, "OK", *sent.ExecErrState)
		require.Equal(t, 10*time.Minute, time.Duration(*sent.For))
		require.Equal(t, time.Minute, time.Duration(sent.KeepFiringFor))
		require.True(t, sent.IsPaused)
		require.Equal(t, "email", *sent.NotificationSettings.Receiver)
		require.Equal(t, int64(5), sent.MissingSeriesEvalsToResolve)
		require.Equal(t, map[string]string{"summary": "changed"}, sent.Annotations)
		require.Equal(t, map[string]string{"severity": "warning"}, sent.Labels)
		require.Equal(t, int64(9), *sent.OrgID)
		// Server-owned fields the caller cannot set are still passed through.
		require.Equal(t, int64(42), sent.ID)
	})

	t.Run("recording rule with empty stored states can be partially updated", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedRecordingRuleJSON)

		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{
			Operation: "update", RuleUID: "rule-1", Labels: map[string]string{"team": "platform"},
		})
		require.NoError(t, err)

		require.Len(t, rec.putBodies, 1)
		put := rec.putBodies[0]
		// Grafana ignores both states on recording rules; the placeholder only
		// satisfies the client-side enum validation.
		requireFieldsUnchanged(t, storedRecordingRuleJSON, put, "labels", "noDataState", "execErrState")
		fields := jsonFields(t, put)
		require.Equal(t, `{"team":"platform"}`, canonicalJSON(t, fields["labels"]))
		require.Equal(t, `"OK"`, canonicalJSON(t, fields["noDataState"]))
		require.Equal(t, `"OK"`, canonicalJSON(t, fields["execErrState"]))
	})
}

func TestManageRulesWrite_UpdateErrors(t *testing.T) {
	t.Run("missing rule is reported and nothing is written", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)
		rec.getStatus = http.StatusNotFound

		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{Operation: "update", RuleUID: "rule-1", Title: "x"})
		require.ErrorContains(t, err, "fetch existing rule-1")
		require.Empty(t, rec.putBodies)
	})

	t.Run("a supplied value that makes the rule invalid is rejected before writing", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)

		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{Operation: "update", RuleUID: "rule-1", NoDataState: "Sometimes"})
		require.ErrorContains(t, err, "invalid rule configuration")
		require.Empty(t, rec.putBodies)
	})

	t.Run("invalid supplied durations are rejected before writing", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)

		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{Operation: "update", RuleUID: "rule-1", For: "soon"})
		require.ErrorContains(t, err, "invalid duration format for parameter for")
		_, err = manageRulesWrite(ctx, ManageRulesWriteParams{Operation: "update", RuleUID: "rule-1", KeepFiringFor: "later"})
		require.ErrorContains(t, err, "invalid duration format for parameter keepFiringFor")
		require.Empty(t, rec.putBodies)
	})

	t.Run("an explicitly empty data array is rejected without any request", func(t *testing.T) {
		rec, ctx := newAlertRuleUpdateRecorder(t, storedAlertRuleJSON)

		_, err := manageRulesWrite(ctx, ManageRulesWriteParams{Operation: "update", RuleUID: "rule-1", Data: []map[string]any{}})
		require.ErrorContains(t, err, "data must contain at least one query")
		require.Zero(t, rec.gets)
		require.Empty(t, rec.putBodies)
	})

	t.Run("create still requires the full set of rule fields", func(t *testing.T) {
		_, err := manageRulesWrite(context.Background(), ManageRulesWriteParams{
			Operation: "create", RuleGroup: "group-a", FolderUID: "folder-a", Condition: "A",
			Data: []map[string]any{{"refId": "A"}}, NoDataState: "OK", ExecErrState: "OK", For: "5m", OrgID: 1,
		})
		require.ErrorContains(t, err, "title is required")
	})
}

// The description, the JSON schema and the behaviour must tell the same story:
// only 'operation' is required by the schema, 'create' needs the full rule, and
// 'update' needs just rule_uid plus the fields to change.
func TestAlertRulesWrite_SchemaMatchesPartialUpdate(t *testing.T) {
	raw, err := json.Marshal(AlertRulesWrite.Tool.InputSchema)
	require.NoError(t, err)
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))

	require.Equal(t, []string{"operation"}, schema.Required)
	for name, prop := range schema.Properties {
		require.NotContainsf(t, prop.Description, "'create'\\, 'update'", "property %q", name)
		require.NotContainsf(t, prop.Description, "required for 'create', 'update'", "property %q still says update requires it", name)
		require.NotContainsf(t, prop.Description, "create/update", "property %q still says update requires it", name)
		require.NotContainsf(t, prop.Description, "'create'/'update'", "property %q still says update requires it", name)
	}
	for _, name := range []string{"title", "rule_group", "condition", "data", "no_data_state", "exec_err_state", "org_id"} {
		require.Containsf(t, schema.Properties[name].Description, "required for 'create')", "property %q", name)
	}
	require.Contains(t, schema.Properties["rule_uid"].Description, "required for 'update'")
	require.Contains(t, schema.Properties["operation"].Description, "only the fields to change")

	require.Contains(t, AlertRulesWrite.Tool.Description, "partial update")
	require.Contains(t, AlertRulesWrite.Tool.Description, "only 'create' requires the full set of rule fields")
	require.NotContains(t, AlertRulesWrite.Tool.Description, "replaces the whole rule")
}

// notification_settings and record are objects with their own optional fields.
// A partial update merges the supplied fields into the stored object rather
// than replacing it, so changing the receiver does not drop grouping or timing.
func TestManageRulesWrite_UpdateMergesNestedSettings(t *testing.T) {
	update := func(t *testing.T, stored string, params ManageRulesWriteParams) (*alertRuleUpdateRecorder, error) {
		t.Helper()
		rec, ctx := newAlertRuleUpdateRecorder(t, stored)
		params.Operation = "update"
		params.RuleUID = "rule-1"
		_, err := manageRulesWrite(ctx, params)
		return rec, err
	}
	sentSettings := func(t *testing.T, rec *alertRuleUpdateRecorder) map[string]json.RawMessage {
		t.Helper()
		require.Len(t, rec.putBodies, 1)
		return jsonFields(t, jsonFields(t, rec.putBodies[0])["notification_settings"])
	}
	storedSettings := jsonFields(t, jsonFields(t, []byte(storedAlertRuleJSON))["notification_settings"])

	t.Run("changing the receiver keeps grouping, timing and intervals", func(t *testing.T) {
		rec, err := update(t, storedAlertRuleJSON, ManageRulesWriteParams{
			NotificationSettings: map[string]any{"receiver": "email"},
		})
		require.NoError(t, err)

		sent := sentSettings(t, rec)
		require.Equal(t, `"email"`, canonicalJSON(t, sent["receiver"]))
		for _, name := range []string{"group_by", "group_wait", "group_interval", "repeat_interval", "mute_time_intervals", "active_time_intervals"} {
			require.Containsf(t, sent, name, "stored %s was dropped", name)
			require.Equalf(t, canonicalJSON(t, storedSettings[name]), canonicalJSON(t, sent[name]), "stored %s changed", name)
		}
		// Nothing outside notification_settings moved either.
		requireFieldsUnchanged(t, storedAlertRuleJSON, rec.putBodies[0], "notification_settings")
	})

	t.Run("one timing field changes alone", func(t *testing.T) {
		rec, err := update(t, storedAlertRuleJSON, ManageRulesWriteParams{
			NotificationSettings: map[string]any{"repeatInterval": "12h"},
		})
		require.NoError(t, err)

		sent := sentSettings(t, rec)
		require.Equal(t, `"12h"`, canonicalJSON(t, sent["repeat_interval"]))
		for _, name := range []string{"receiver", "group_by", "group_wait", "group_interval", "mute_time_intervals", "active_time_intervals"} {
			require.Equalf(t, canonicalJSON(t, storedSettings[name]), canonicalJSON(t, sent[name]), "stored %s changed", name)
		}
	})

	t.Run("an explicitly empty list clears that list only", func(t *testing.T) {
		rec, err := update(t, storedAlertRuleJSON, ManageRulesWriteParams{
			NotificationSettings: map[string]any{"muteTimeIntervals": []string{}},
		})
		require.NoError(t, err)

		sent := sentSettings(t, rec)
		require.Equal(t, `[]`, canonicalJSON(t, sent["mute_time_intervals"]))
		for _, name := range []string{"receiver", "group_by", "group_wait", "group_interval", "repeat_interval", "active_time_intervals"} {
			require.Equalf(t, canonicalJSON(t, storedSettings[name]), canonicalJSON(t, sent[name]), "stored %s changed", name)
		}
	})

	t.Run("an empty object changes nothing", func(t *testing.T) {
		rec, err := update(t, storedAlertRuleJSON, ManageRulesWriteParams{
			NotificationSettings: map[string]any{}, Record: map[string]any{},
		})
		require.NoError(t, err)

		require.Len(t, rec.putBodies, 1)
		requireFieldsUnchanged(t, storedAlertRuleJSON, rec.putBodies[0])
		require.Equal(t, clientEncoding(t, storedAlertRuleJSON, nil), canonicalJSON(t, rec.putBodies[0]))
	})

	t.Run("settings without a receiver on a rule that has none are rejected before writing", func(t *testing.T) {
		rec, err := update(t, storedRecordingRuleJSON, ManageRulesWriteParams{
			NotificationSettings: map[string]any{"groupWait": "1m"},
		})
		require.ErrorContains(t, err, "invalid rule configuration")
		require.ErrorContains(t, err, "receiver")
		require.Empty(t, rec.putBodies)
	})

	t.Run("one record field changes alone", func(t *testing.T) {
		rec, err := update(t, storedRecordingRuleJSON, ManageRulesWriteParams{
			Record: map[string]any{"targetDatasourceUid": "other-prom"},
		})
		require.NoError(t, err)

		require.Len(t, rec.putBodies, 1)
		record := jsonFields(t, jsonFields(t, rec.putBodies[0])["record"])
		require.Equal(t, `"A"`, canonicalJSON(t, record["from"]))
		require.Equal(t, `"cpu:rate5m"`, canonicalJSON(t, record["metric"]))
		require.Equal(t, `"other-prom"`, canonicalJSON(t, record["target_datasource_uid"]))
	})

	t.Run("an incomplete record on a rule that has none is rejected before writing", func(t *testing.T) {
		rec, err := update(t, storedAlertRuleJSON, ManageRulesWriteParams{
			Record: map[string]any{"metric": "cpu:rate5m"},
		})
		require.ErrorContains(t, err, "invalid rule configuration")
		require.ErrorContains(t, err, "from")
		require.Empty(t, rec.putBodies)
	})

	t.Run("a complete record turns an alert rule into a recording rule", func(t *testing.T) {
		rec, err := update(t, storedAlertRuleJSON, ManageRulesWriteParams{
			Record: map[string]any{"from": "A", "metric": "cpu:rate5m"},
		})
		require.NoError(t, err)

		require.Len(t, rec.putBodies, 1)
		record := jsonFields(t, jsonFields(t, rec.putBodies[0])["record"])
		require.Equal(t, `"A"`, canonicalJSON(t, record["from"]))
		require.Equal(t, `"cpu:rate5m"`, canonicalJSON(t, record["metric"]))
		requireFieldsUnchanged(t, storedAlertRuleJSON, rec.putBodies[0], "record")
	})
}
