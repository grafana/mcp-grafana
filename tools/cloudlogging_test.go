//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCloudLoggingLimit(t *testing.T) {
	assert.Equal(t, DefaultCloudLoggingLimit, normalizeCloudLoggingLimit(0))
	assert.Equal(t, DefaultCloudLoggingLimit, normalizeCloudLoggingLimit(-5))
	assert.Equal(t, 1, normalizeCloudLoggingLimit(1))
	assert.Equal(t, 250, normalizeCloudLoggingLimit(250))
	assert.Equal(t, MaxCloudLoggingLimit, normalizeCloudLoggingLimit(MaxCloudLoggingLimit))
	assert.Equal(t, MaxCloudLoggingLimit, normalizeCloudLoggingLimit(MaxCloudLoggingLimit+1))
	assert.Equal(t, MaxCloudLoggingLimit, normalizeCloudLoggingLimit(1_000_000))
}

func TestBuildCloudLoggingPayload(t *testing.T) {
	from := time.Date(2026, 2, 2, 19, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 2, 20, 0, 0, 0, time.UTC)

	payload := buildCloudLoggingPayload("gcl-uid", "my-project", `severity>=ERROR`, "global/buckets/_Default", "_AllLogs", from, to, 50)

	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var got struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Queries []struct {
			RefID      string `json:"refId"`
			Datasource struct {
				UID  string `json:"uid"`
				Type string `json:"type"`
			} `json:"datasource"`
			QueryText     string `json:"queryText"`
			ProjectID     string `json:"projectId"`
			BucketID      string `json:"bucketId"`
			ViewID        string `json:"viewId"`
			MaxDataPoints int    `json:"maxDataPoints"`
		} `json:"queries"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, "1770058800000", got.From)
	assert.Equal(t, "1770062400000", got.To)
	require.Len(t, got.Queries, 1)
	q := got.Queries[0]
	assert.Equal(t, "A", q.RefID)
	assert.Equal(t, "gcl-uid", q.Datasource.UID)
	assert.Equal(t, CloudLoggingDatasourceType, q.Datasource.Type)
	assert.Equal(t, `severity>=ERROR`, q.QueryText)
	assert.Equal(t, "my-project", q.ProjectID)
	assert.Equal(t, "global/buckets/_Default", q.BucketID)
	assert.Equal(t, "_AllLogs", q.ViewID)
	assert.Equal(t, 50, q.MaxDataPoints)

	payload = buildCloudLoggingPayload("gcl-uid", "p", "", "", "", from, to, 0)
	assert.Equal(t, DefaultCloudLoggingLimit, payload["queries"].([]map[string]interface{})[0]["maxDataPoints"])
	payload = buildCloudLoggingPayload("gcl-uid", "p", "", "", "", from, to, 5000)
	assert.Equal(t, MaxCloudLoggingLimit, payload["queries"].([]map[string]interface{})[0]["maxDataPoints"])
}

func newCloudLoggingTestFrame() *data.Frame {
	ts := []time.Time{
		time.Date(2026, 2, 2, 19, 5, 0, 0, time.UTC),
		time.Date(2026, 2, 2, 19, 4, 0, 0, time.UTC),
	}
	trace1 := "projects/p/traces/abc"
	frame := data.NewFrame("A",
		data.NewField("timestamp", nil, ts),
		data.NewField("body", nil, []string{"first line", "second line"}),
		data.NewField("severity", nil, []string{"ERROR", "INFO"}),
		data.NewField("id", nil, []string{"id-1", "id-2"}),
		data.NewField("labels", nil, []json.RawMessage{
			json.RawMessage(`{"resource.type":"k8s_container","k8s-pod/app":"api"}`),
			json.RawMessage(`null`),
		}),
		data.NewField("traceId", nil, []*string{&trace1, nil}),
	)
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeLogLines, PreferredVisualization: data.VisTypeLogs}
	return frame
}

func TestCloudLoggingEntriesFromResponse(t *testing.T) {
	resp := &backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Frames: data.Frames{newCloudLoggingTestFrame()}},
	}}

	entries, err := cloudLoggingEntriesFromResponse(resp)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, time.Date(2026, 2, 2, 19, 5, 0, 0, time.UTC), entries[0].Timestamp)
	assert.Equal(t, "first line", entries[0].Body)
	assert.Equal(t, "ERROR", entries[0].Severity)
	assert.Equal(t, "id-1", entries[0].ID)
	assert.Equal(t, "projects/p/traces/abc", entries[0].TraceID)
	assert.JSONEq(t, `{"resource.type":"k8s_container","k8s-pod/app":"api"}`, string(entries[0].Labels))

	assert.Equal(t, "second line", entries[1].Body)
	assert.Equal(t, "", entries[1].TraceID)
	assert.Nil(t, entries[1].Labels)

	out, err := json.Marshal(entries[0])
	require.NoError(t, err)
	assert.Contains(t, string(out), `"labels":{"resource.type":"k8s_container"`)
}

func TestCloudLoggingEntriesFromResponse_MissingFieldsTolerated(t *testing.T) {
	frame := data.NewFrame("A",
		data.NewField("severity", nil, []string{"WARNING"}),
		data.NewField("body", nil, []string{"only body"}),
		data.NewField("timestamp", nil, []time.Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}),
	)
	resp := &backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Frames: data.Frames{frame}},
	}}
	entries, err := cloudLoggingEntriesFromResponse(resp)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "only body", entries[0].Body)
	assert.Equal(t, "WARNING", entries[0].Severity)
	assert.Equal(t, "", entries[0].ID)
	assert.Equal(t, "", entries[0].TraceID)
	assert.Nil(t, entries[0].Labels)
}

func TestCloudLoggingEntriesFromResponse_Empty(t *testing.T) {
	resp := &backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Frames: data.Frames{}},
	}}
	entries, err := cloudLoggingEntriesFromResponse(resp)
	require.NoError(t, err)
	assert.NotNil(t, entries, "entries must be [] not null")
	assert.Len(t, entries, 0)
}

func TestCloudLoggingEntriesFromResponse_ErrorPropagated(t *testing.T) {
	resp := &backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.DataResponse{Error: assert.AnError},
	}}
	_, err := cloudLoggingEntriesFromResponse(resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refId=A")
}

func TestParseCloudLoggingStringList(t *testing.T) {
	got, err := parseCloudLoggingStringList([]byte(`["global/buckets/_Default","global/buckets/_Required"]`))
	require.NoError(t, err)
	assert.Equal(t, []string{"global/buckets/_Default", "global/buckets/_Required"}, got)

	got, err = parseCloudLoggingStringList([]byte(`null`))
	require.NoError(t, err)
	assert.Equal(t, []string{}, got)

	_, err = parseCloudLoggingStringList([]byte(`{"not":"a list"}`))
	require.Error(t, err)
}

func TestCloudLoggingClient_Resource(t *testing.T) {
	var gotPath, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["_AllLogs","_Default"]`))
	}))
	t.Cleanup(ts.Close)

	client := &cloudLoggingClient{httpClient: http.DefaultClient, baseURL: ts.URL, uid: "gcl-uid"}

	params := map[string][]string{"ProjectId": {"my-project"}, "BucketId": {"global/buckets/_Default"}}
	got, err := client.resourceStrings(context.Background(), "/logviews", params)
	require.NoError(t, err)
	assert.Equal(t, []string{"_AllLogs", "_Default"}, got)

	assert.Equal(t, "/api/datasources/uid/gcl-uid/resources/logviews", gotPath)
	assert.Contains(t, gotQuery, "ProjectId=my-project")
	assert.Contains(t, gotQuery, "BucketId=global%2Fbuckets%2F_Default")
}

func TestCloudLoggingClient_Resource_NonOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"permission denied on project"}`))
	}))
	t.Cleanup(ts.Close)

	client := &cloudLoggingClient{httpClient: http.DefaultClient, baseURL: ts.URL, uid: "gcl-uid"}
	_, err := client.resourceStrings(context.Background(), "/projects", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 403")
	assert.Contains(t, err.Error(), "permission denied")
}

func newCloudLoggingMockGrafana(t *testing.T, dsType string, extra http.HandlerFunc) (*httptest.Server, context.Context) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/datasources/uid/") && !strings.Contains(r.URL.Path, "/resources") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   1,
				"uid":  strings.TrimPrefix(r.URL.Path, "/api/datasources/uid/"),
				"name": "GCL",
				"type": dsType,
			})
			return
		}
		if extra != nil {
			extra(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	cfg := mcpgrafana.GrafanaConfig{URL: ts.URL, APIKey: "test-api-key"}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
	ctx = mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, ts.URL, "test-api-key", nil))
	return ts, ctx
}

func TestQueryCloudLogging_RejectsWrongDatasourceType(t *testing.T) {
	_, ctx := newCloudLoggingMockGrafana(t, "influxdb", nil)

	_, err := queryCloudLogging(ctx, CloudLoggingQueryParams{DatasourceUID: "some-uid", ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is of type influxdb, not "+CloudLoggingDatasourceType)

	_, err = listCloudLoggingProjects(ctx, ListCloudLoggingProjectsParams{DatasourceUID: "some-uid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not "+CloudLoggingDatasourceType)
}

func TestQueryCloudLogging_Validation(t *testing.T) {
	ctx := context.Background()

	_, err := queryCloudLogging(ctx, CloudLoggingQueryParams{DatasourceUID: "u"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "projectId is required")

	_, err = queryCloudLogging(ctx, CloudLoggingQueryParams{DatasourceUID: "u", ProjectID: "p", ViewID: "_AllLogs"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "viewId requires bucketId")

	_, err = listCloudLoggingBuckets(ctx, ListCloudLoggingBucketsParams{DatasourceUID: "u"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "projectId is required")

	_, err = listCloudLoggingViews(ctx, ListCloudLoggingViewsParams{DatasourceUID: "u", ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucketId is required")
}

func TestQueryCloudLogging_EndToEnd(t *testing.T) {
	var gotBody map[string]interface{}
	_, ctx := newCloudLoggingMockGrafana(t, CloudLoggingDatasourceType, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ds/query" {
			http.NotFound(w, r)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		resp := &backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: data.Frames{newCloudLoggingTestFrame()}},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	result, err := queryCloudLogging(ctx, CloudLoggingQueryParams{
		DatasourceUID: "gcl-uid",
		ProjectID:     "my-project",
		Filter:        `severity>=ERROR`,
		Start:         "2026-02-02T19:00:00Z",
		End:           "2026-02-02T20:00:00Z",
		Limit:         2,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 2, result.EntryCount)
	assert.Equal(t, 2, result.Limit)
	assert.True(t, result.Truncated, "hitting the limit must flag truncation")
	assert.Nil(t, result.Hints)
	assert.Equal(t, "first line", result.Entries[0].Body)

	assert.Equal(t, "1770058800000", gotBody["from"])
	assert.Equal(t, "1770062400000", gotBody["to"])
	queries := gotBody["queries"].([]interface{})
	require.Len(t, queries, 1)
	q := queries[0].(map[string]interface{})
	assert.Equal(t, `severity>=ERROR`, q["queryText"])
	assert.Equal(t, "my-project", q["projectId"])
	assert.Equal(t, float64(2), q["maxDataPoints"])
}

func TestQueryCloudLogging_EmptyResultHasHints(t *testing.T) {
	_, ctx := newCloudLoggingMockGrafana(t, CloudLoggingDatasourceType, func(w http.ResponseWriter, r *http.Request) {
		resp := &backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: data.Frames{}},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	result, err := queryCloudLogging(ctx, CloudLoggingQueryParams{
		DatasourceUID: "gcl-uid",
		ProjectID:     "my-project",
		Filter:        `timestamp>="2026-01-01T00:00:00Z" AND severity=ERROR`,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result.EntryCount)
	assert.False(t, result.Truncated)
	require.NotNil(t, result.Hints)
	assert.Contains(t, result.Hints.Summary, "Google Cloud Logging")
	assert.Contains(t, strings.Join(result.Hints.PossibleCauses, "\n"), "timestamp clause")
	assert.Contains(t, strings.Join(result.Hints.PossibleCauses, "\n"), "severity>=ERROR")
}

func TestQueryCloudLogging_UpstreamErrorSurfaced(t *testing.T) {
	_, ctx := newCloudLoggingMockGrafana(t, CloudLoggingDatasourceType, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"error":"rpc error: code = PermissionDenied desc = The caller does not have permission","status":403}}}`))
	})

	_, err := queryCloudLogging(ctx, CloudLoggingQueryParams{DatasourceUID: "gcl-uid", ProjectID: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PermissionDenied")
}
