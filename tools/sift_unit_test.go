//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sift.go had no unit tests before this consolidation — only
// sift_cloud_test.go (139 lines, requires a live Grafana + Sift plugin).
// These are new, using an httptest server to mock the raw HTTP calls
// siftClient makes (Sift has no generated openapi-client-go client, unlike
// admin/annotations, so this mocks at the http.RoundTripper level via
// mcpgrafana.BuildTransport rather than grafana-openapi-client-go).

func siftTestContext(server *httptest.Server) context.Context {
	return mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "test",
	})
}

// --- sift_read tool definition and validation ---

func TestSiftReadToolDefinition(t *testing.T) {
	require.NotNil(t, SiftRead)
	assert.Equal(t, "sift_read", SiftRead.Tool.Name)
	for _, op := range siftReadOperations {
		assert.Contains(t, SiftRead.Tool.Description, op)
	}
}

func TestSiftReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  SiftReadParams
		wantErr string
	}{
		{name: "list_investigations needs nothing", params: SiftReadParams{Operation: "list_investigations"}},
		{
			name:    "get_investigation missing investigationId",
			params:  SiftReadParams{Operation: "get_investigation"},
			wantErr: "investigationId is required for 'get_investigation' operation",
		},
		{name: "get_investigation with investigationId", params: SiftReadParams{Operation: "get_investigation", InvestigationID: "abc"}},
		{
			name:    "get_analysis missing both fields",
			params:  SiftReadParams{Operation: "get_analysis"},
			wantErr: "investigationId is required for 'get_analysis' operation",
		},
		{
			name:    "get_analysis missing analysisId",
			params:  SiftReadParams{Operation: "get_analysis", InvestigationID: "abc"},
			wantErr: "analysisId is required for 'get_analysis' operation",
		},
		{name: "get_analysis with both fields", params: SiftReadParams{Operation: "get_analysis", InvestigationID: "abc", AnalysisID: "def"}},
		{
			name:    "unknown operation",
			params:  SiftReadParams{Operation: "bogus"},
			wantErr: `unknown operation "bogus", must be one of: list_investigations, get_investigation, get_analysis`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// --- sift_write tool definition and validation ---

func TestSiftWriteToolDefinition(t *testing.T) {
	require.NotNil(t, SiftWrite)
	assert.Equal(t, "sift_write", SiftWrite.Tool.Name)
	for _, op := range siftWriteOperations {
		assert.Contains(t, SiftWrite.Tool.Description, op)
	}
}

func TestSiftWriteParams_Validate(t *testing.T) {
	require.NoError(t, SiftWriteParams{Operation: "find_error_pattern_logs"}.validate())
	require.NoError(t, SiftWriteParams{Operation: "find_slow_requests"}.validate())

	err := SiftWriteParams{Operation: "bogus"}.validate()
	require.Error(t, err)
	assert.EqualError(t, err, `unknown operation "bogus", must be one of: list_investigations, get_investigation, get_analysis, find_error_pattern_logs, find_slow_requests`)
}

func TestSiftWriteParams_Validate_RejectsReadOperations(t *testing.T) {
	// A schema-conformant caller can never send a sift_read operation to
	// sift_write (its jsonschema enum doesn't offer them), but the
	// Go-level validate() must still reject them explicitly rather than
	// silently succeeding.
	for _, op := range siftReadOperations {
		err := SiftWriteParams{Operation: op}.validate()
		require.Error(t, err, "operation %q must not validate for sift_write", op)
		assert.Contains(t, err.Error(), "sift_read operation, not sift_write")
	}
}

// --- dispatch ---

func TestSiftReadDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := siftRead(context.Background(), SiftReadParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sift_read")
}

func TestSiftWriteDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := siftWrite(context.Background(), SiftWriteParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sift_write")
}

// --- getSiftInvestigation / sift_read "get_investigation" ---

func TestGetSiftInvestigation_UsesCorrectPath(t *testing.T) {
	id := "02adab7c-bf5b-45f2-9459-d71a2c29e11b"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/"+id, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"` + id + `","name":"test","tenantId":"t1","status":"finished"}}`))
	}))
	defer server.Close()

	result, err := getSiftInvestigation(siftTestContext(server), GetSiftInvestigationParams{ID: id})
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, investigationStatusFinished, result.Status)
}

func TestGetSiftInvestigation_InvalidUUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not make any HTTP request for an invalid UUID")
	}))
	defer server.Close()

	_, err := getSiftInvestigation(siftTestContext(server), GetSiftInvestigationParams{ID: "not-a-uuid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid investigation ID format")
}

// --- getSiftAnalysis / sift_read "get_analysis" ---

func TestGetSiftAnalysis_FindsMatchingAnalysis(t *testing.T) {
	investigationID := "02adab7c-bf5b-45f2-9459-d71a2c29e11b"
	analysisID := "11111111-1111-1111-1111-111111111111"
	otherAnalysisID := "22222222-2222-2222-2222-222222222222"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/"+investigationID+"/analyses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[
			{"id":"` + otherAnalysisID + `","name":"Other"},
			{"id":"` + analysisID + `","name":"ErrorPatternLogs"}
		]}`))
	}))
	defer server.Close()

	result, err := getSiftAnalysis(siftTestContext(server), GetSiftAnalysisParams{
		InvestigationID: investigationID,
		AnalysisID:      analysisID,
	})
	require.NoError(t, err)
	assert.Equal(t, "ErrorPatternLogs", result.Name)
}

func TestGetSiftAnalysis_NotFound(t *testing.T) {
	investigationID := "02adab7c-bf5b-45f2-9459-d71a2c29e11b"
	missingID := "33333333-3333-3333-3333-333333333333"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	_, err := getSiftAnalysis(siftTestContext(server), GetSiftAnalysisParams{
		InvestigationID: investigationID,
		AnalysisID:      missingID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- listSiftInvestigations / sift_read "list_investigations" ---

func TestListSiftInvestigations_UsesLimitQueryParam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations", r.URL.Path)
		assert.Equal(t, "5", r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	_, err := listSiftInvestigations(siftTestContext(server), ListSiftInvestigationsParams{Limit: 5})
	require.NoError(t, err)
}

func TestListSiftInvestigations_DefaultsLimitWhenZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "10", r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	_, err := listSiftInvestigations(siftTestContext(server), ListSiftInvestigationsParams{})
	require.NoError(t, err)
}

// --- findErrorPatternLogs / findSlowRequests / sift_write ---

// siftInvestigationServer mocks the create -> poll -> get-analyses flow
// shared by findErrorPatternLogs and findSlowRequests, returning a
// "finished" investigation with the given analysis name and no error
// pattern examples (so fetchErrorPatternLogExamples isn't exercised here).
func siftInvestigationServer(t *testing.T, analysisName string) *httptest.Server {
	t.Helper()
	const id = "02adab7c-bf5b-45f2-9459-d71a2c29e11b"

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_, _ = w.Write([]byte(`{"status":"success","data":{"id":"` + id + `","name":"test","status":"pending"}}`))
		case r.Method == "GET" && r.URL.Path == "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/"+id:
			_, _ = w.Write([]byte(`{"status":"success","data":{"id":"` + id + `","name":"test","status":"finished"}}`))
		case r.Method == "GET" && r.URL.Path == "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/"+id+"/analyses":
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"11111111-1111-1111-1111-111111111111","name":"` + analysisName + `","investigationId":"` + id + `"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestFindErrorPatternLogs_ReturnsTheMatchingAnalysis(t *testing.T) {
	server := siftInvestigationServer(t, "ErrorPatternLogs")
	defer server.Close()

	result, err := findErrorPatternLogs(siftTestContext(server), SiftWriteParams{
		Name:   "test",
		Labels: map[string]string{"app": "foo"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ErrorPatternLogs", result.Name)
}

func TestFindSlowRequests_ReturnsTheMatchingAnalysis(t *testing.T) {
	server := siftInvestigationServer(t, "SlowRequests")
	defer server.Close()

	result, err := findSlowRequests(siftTestContext(server), SiftWriteParams{
		Name:   "test",
		Labels: map[string]string{"app": "foo"},
	})
	require.NoError(t, err)
	assert.Equal(t, "SlowRequests", result.Name)
}

func TestFindErrorPatternLogs_AnalysisNotFound(t *testing.T) {
	// Server returns a finished investigation with no ErrorPatternLogs
	// analysis in the list.
	server := siftInvestigationServer(t, "SomeOtherCheck")
	defer server.Close()

	_, err := findErrorPatternLogs(siftTestContext(server), SiftWriteParams{
		Name:   "test",
		Labels: map[string]string{"app": "foo"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ErrorPatternLogs analysis not found")
}

// --- sift_write / sift_read dispatch routes to the right handler ---

// Only one operation is exercised end-to-end through siftWrite: the
// dispatch itself is a trivial switch, findErrorPatternLogs and
// findSlowRequests are each already proven correct in isolation above, and
// createSiftInvestigation's 5-second poll ticker makes every test that
// reaches it cost real wall-clock time — not worth paying twice more to
// re-prove routing that's this simple.
func TestSiftWriteDispatch_RoutesToTheRightHandler(t *testing.T) {
	server := siftInvestigationServer(t, "ErrorPatternLogs")
	defer server.Close()

	result, err := siftWrite(siftTestContext(server), SiftWriteParams{
		Operation: "find_error_pattern_logs",
		Name:      "test",
		Labels:    map[string]string{"app": "foo"},
	})
	require.NoError(t, err)
	a, ok := result.(*analysis)
	require.True(t, ok)
	assert.Equal(t, "ErrorPatternLogs", a.Name)
}

func TestSiftReadDispatch_RoutesToTheRightHandler(t *testing.T) {
	id := "02adab7c-bf5b-45f2-9459-d71a2c29e11b"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations":
			_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
		case "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/" + id:
			_, _ = w.Write([]byte(`{"status":"success","data":{"id":"` + id + `","name":"test"}}`))
		case "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/" + id + "/analyses":
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"11111111-1111-1111-1111-111111111111","name":"ErrorPatternLogs"}]}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	ctx := siftTestContext(server)

	t.Run("list_investigations", func(t *testing.T) {
		_, err := siftRead(ctx, SiftReadParams{Operation: "list_investigations"})
		require.NoError(t, err)
	})
	t.Run("get_investigation", func(t *testing.T) {
		_, err := siftRead(ctx, SiftReadParams{Operation: "get_investigation", InvestigationID: id})
		require.NoError(t, err)
	})
	t.Run("get_analysis", func(t *testing.T) {
		_, err := siftRead(ctx, SiftReadParams{
			Operation:       "get_analysis",
			InvestigationID: id,
			AnalysisID:      "11111111-1111-1111-1111-111111111111",
		})
		require.NoError(t, err)
	})
}
