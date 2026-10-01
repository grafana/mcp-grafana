//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTraceID = "0123456789abcdef0123456789abcdef"
	testSpanID  = "1111111111111111"
)

func TestRenderTraceToolDefinition(t *testing.T) {
	tool := GetTempoTraceTool.Tool
	assert.Equal(t, "get_tempo_trace", tool.Name)
	require.NotNil(t, tool.Annotations)
	assert.True(t, tool.Annotations.ReadOnlyHint)
	assert.True(t, tool.Annotations.IdempotentHint)
	require.NotNil(t, tool.Meta)

	ui, ok := tool.Meta["ui"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, mcpgrafana.TraceViewerResourceURI, ui["resourceUri"])

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	require.NoError(t, json.Unmarshal(tool.InputSchema.(json.RawMessage), &schema))
	assert.ElementsMatch(t, []string{"trace_id", "datasourceUid"}, schema.Required)
	assert.Contains(t, schema.Properties, "focus_span_id")
}

func TestRenderTraceFetchesThroughAuthenticatedGrafanaProxy(t *testing.T) {
	stacktrace := strings.Repeat("at payment.authorize (payment.go:42)\n", 128)
	traceData := testTraceData(stacktrace)
	traceJSON, err := protojson.Marshal(traceData)
	require.NoError(t, err)

	var requestedPath, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/tempo-main" {
			writeTempoDatasource(t, w)
			return
		}
		if r.URL.Path == "/api/user" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"orgId":7}`)
			return
		}
		requestedPath = r.URL.Path
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]json.RawMessage{
			"trace": traceJSON,
		}))
	}))
	t.Cleanup(server.Close)

	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		OrgID:  7,
		APIKey: "test-token",
	})
	gc := mcpgrafana.GrafanaClientFromContext(ctx)
	gc.PublicURL = "https://grafana.example.com/grafana"
	gc.Version = "11.0.0"
	focusSpanID := testSpanID

	result, err := callInteractiveTrace(ctx, GetTempoTraceParams{
		TraceID:       testTraceID,
		DatasourceUID: "tempo-main",
		FocusSpanID:   &focusSpanID,
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/datasources/proxy/uid/tempo-main/api/v2/traces/"+testTraceID, requestedPath)
	assert.Equal(t, "Bearer test-token", authorization)

	structured, ok := result.StructuredContent.(TraceViewResult)
	require.True(t, ok)
	assert.Equal(t, testTraceID, structured.TraceID)
	assert.Equal(t, "tempo-main", structured.DatasourceUID)
	require.Len(t, structured.Spans, 2)
	link, err := url.Parse(structured.GrafanaURL)
	require.NoError(t, err)
	assert.Equal(t, "grafana.example.com", link.Host)
	assert.Equal(t, "/grafana/explore", link.Path)
	assert.Empty(t, link.Query().Get("orgId"))
	assert.Equal(t, "1", link.Query().Get("schemaVersion"))
	var panes map[string]struct {
		Queries []map[string]any `json:"queries"`
	}
	require.NoError(t, json.Unmarshal([]byte(link.Query().Get("panes")), &panes))
	require.Len(t, panes[explorePaneID].Queries, 1)
	assert.Equal(t, testTraceID, panes[explorePaneID].Queries[0]["query"])
	assert.Equal(t, testSpanID, panes[explorePaneID].Queries[0]["spanId"])
	var paneRanges map[string]struct {
		Range map[string]string `json:"range"`
	}
	require.NoError(t, json.Unmarshal([]byte(link.Query().Get("panes")), &paneRanges))
	assert.Equal(t, "1749999940000", paneRanges[explorePaneID].Range["from"])
	assert.Equal(t, "1750000060125", paneRanges[explorePaneID].Range["to"])

	root := structured.Spans[0]
	assert.Equal(t, "checkout", root.ServiceName)
	assert.Equal(t, "error", root.Status, "an exception event marks an unset OTLP span as an error")
	assert.Equal(t, "production", root.Attributes["resource.deployment.environment.name"])
	require.Len(t, root.Events, 1)
	assert.Equal(t, stacktrace, root.Events[0].Attributes["exception.stacktrace"],
		"exception data must be returned intact")

	child := structured.Spans[1]
	require.NotNil(t, child.ParentID)
	assert.Equal(t, "0102030405060708", *child.ParentID)
	assert.Equal(t, "ok", child.Status)
}

func TestRenderTraceFetchesWithUserScopedAuthentication(t *testing.T) {
	traceJSON, err := protojson.Marshal(testTraceData("stack"))
	require.NoError(t, err)

	var accessToken, idToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/tempo-main" {
			writeTempoDatasource(t, w)
			return
		}
		accessToken = r.Header.Get("X-Access-Token")
		idToken = r.Header.Get("X-Grafana-Id")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]json.RawMessage{"trace": traceJSON}))
	}))
	t.Cleanup(server.Close)

	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{
		URL: server.URL, AccessToken: "user-access-token", IDToken: "user-id-token",
	})
	_, err = callInteractiveTrace(ctx, GetTempoTraceParams{TraceID: testTraceID, DatasourceUID: "tempo-main"})
	require.NoError(t, err)
	assert.Equal(t, "user-access-token", accessToken)
	assert.Equal(t, "user-id-token", idToken)
}

func TestTraceExploreURLStripsCredentialsAndUsesLegacyState(t *testing.T) {
	link, err := traceExploreURL("https://user:secret@grafana.example.com/grafana/?old=1#fragment", "9.5.0", GetTempoTraceParams{
		TraceID: testTraceID, DatasourceUID: "tempo-main",
	}, []TraceSpan{{StartTimeMS: 1750000000000, DurationMS: 125}})
	require.NoError(t, err)
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	assert.Nil(t, parsed.User)
	assert.Equal(t, "grafana.example.com", parsed.Host)
	assert.Equal(t, "/grafana/explore", parsed.Path)
	assert.Empty(t, parsed.Fragment)
	assert.Empty(t, parsed.Query().Get("old"))
	assert.Empty(t, parsed.Query().Get("orgId"))
	assert.Empty(t, parsed.Query().Get("panes"))
	var left map[string]any
	require.NoError(t, json.Unmarshal([]byte(parsed.Query().Get("left")), &left))
	assert.Equal(t, "tempo-main", left["datasource"])
	assert.Equal(t, map[string]any{"from": "1749999940000", "to": "1750000060125"}, left["range"])
}

func TestRenderTraceOmitsLinkWhenPublicURLInvalid(t *testing.T) {
	traceJSON, err := protojson.Marshal(testTraceData("stack"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/tempo-main" {
			writeTempoDatasource(t, w)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]json.RawMessage{"trace": traceJSON}))
	}))
	t.Cleanup(server.Close)
	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-token"})
	mcpgrafana.GrafanaClientFromContext(ctx).PublicURL = "not a Grafana URL"

	result, err := callInteractiveTrace(ctx, GetTempoTraceParams{TraceID: testTraceID, DatasourceUID: "tempo-main"})
	require.NoError(t, err)
	structured, ok := result.StructuredContent.(TraceViewResult)
	require.True(t, ok)
	assert.Empty(t, structured.GrafanaURL)
	assert.Len(t, structured.Spans, 2)
}

func TestRenderTraceOmitsLinkForDifferentViewerOrg(t *testing.T) {
	traceJSON, err := protojson.Marshal(testTraceData("stack"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasources/uid/tempo-main":
			writeTempoDatasource(t, w)
		case "/api/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"orgId":3}`)
		case "/api/datasources/proxy/uid/tempo-main/api/v2/traces/" + testTraceID:
			require.NoError(t, json.NewEncoder(w).Encode(map[string]json.RawMessage{"trace": traceJSON}))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: server.URL, OrgID: 7, APIKey: "test-token"})
	result, err := callInteractiveTrace(ctx, GetTempoTraceParams{TraceID: testTraceID, DatasourceUID: "tempo-main"})
	require.NoError(t, err)
	structured, ok := result.StructuredContent.(TraceViewResult)
	require.True(t, ok)
	assert.Empty(t, structured.GrafanaURL)
	assert.Len(t, structured.Spans, 2)
}

func TestRenderTraceMissingFocusPreservesTrace(t *testing.T) {
	traceData := testTraceData("stack")
	traceJSON, err := protojson.Marshal(traceData)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/tempo-main" {
			writeTempoDatasource(t, w)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]json.RawMessage{"trace": traceJSON}))
	}))
	t.Cleanup(server.Close)

	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-token"})
	missing := "aaaaaaaaaaaaaaaa"
	result, err := callInteractiveTrace(ctx, GetTempoTraceParams{
		TraceID:       testTraceID,
		DatasourceUID: "tempo-main",
		FocusSpanID:   &missing,
	})
	require.NoError(t, err)
	structured, ok := result.StructuredContent.(TraceViewResult)
	require.True(t, ok)
	require.NotNil(t, structured.FocusSpanID)
	assert.Equal(t, missing, *structured.FocusSpanID)
	assert.Len(t, structured.Spans, 2)
}

func TestDecodeTraceResponseRejectsPartialTrace(t *testing.T) {
	traceJSON, err := protojson.Marshal(testTraceData("stack"))
	require.NoError(t, err)
	response, err := json.Marshal(map[string]any{"trace": json.RawMessage(traceJSON), "status": "PARTIAL", "message": "trace exceeded max bytes"})
	require.NoError(t, err)
	_, err = decodeTraceResponse(strings.NewReader(string(response)), defaultResponseLimitBytes)
	require.ErrorContains(t, err, "partial")
}

func TestTraceStatusRespectsExplicitOKWithException(t *testing.T) {
	assert.Equal(t, "ok", traceStatus(&tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}, []TraceEvent{{Name: "exception"}}))
}

func TestDecodeTraceResponseRejectsOversizeWithoutTruncating(t *testing.T) {
	_, err := decodeTraceResponse(strings.NewReader(strings.Repeat("x", 33)), 32)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no span or exception data was truncated")

	failing := &failingReader{}
	_, err = decodeTraceResponse(failing, 32)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read Tempo response")
}

func TestNormalizeTraceRejectsSpanLimit(t *testing.T) {
	trace := &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: make([]*tracepb.Span, maxTraceSpans+1)}},
	}}}
	_, err := normalizeTrace(trace)
	require.EqualError(t, err, "trace contains more than the supported limit of 100000 spans")
}

func TestRenderTraceRejectsNonTempoDatasource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasources/uid/tempo-main" {
			t.Errorf("unexpected Tempo request: %s", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"uid": "tempo-main", "name": "Prometheus", "type": "prometheus", "id": 1,
		}))
	}))
	t.Cleanup(server.Close)

	ctx := traceTestContext(t, mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-token"})
	result, err := callInteractiveTrace(ctx, GetTempoTraceParams{TraceID: testTraceID, DatasourceUID: "tempo-main"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "datasource tempo-main is of type prometheus, not tempo")
}

func testTraceData(stacktrace string) *tracepb.TracesData {
	return &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			stringAttribute("service.name", "checkout"),
			stringAttribute("deployment.environment.name", "production"),
		}},
		ScopeSpans: []*tracepb.ScopeSpans{{
			Spans: []*tracepb.Span{
				{
					TraceId:           []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
					SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
					Name:              "POST /checkout",
					StartTimeUnixNano: 1_750_000_000_000_000_000,
					EndTimeUnixNano:   1_750_000_000_125_000_000,
					Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_UNSET},
					Attributes:        []*commonpb.KeyValue{stringAttribute("http.request.method", "POST")},
					Events: []*tracepb.Span_Event{{
						Name:         "exception",
						TimeUnixNano: 1_750_000_000_100_000_000,
						Attributes: []*commonpb.KeyValue{
							stringAttribute("exception.type", "PaymentTimeout"),
							stringAttribute("exception.message", "payment authorization timed out"),
							stringAttribute("exception.stacktrace", stacktrace),
						},
					}},
				},
				{
					TraceId:           []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
					SpanId:            []byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11},
					ParentSpanId:      []byte{1, 2, 3, 4, 5, 6, 7, 8},
					Name:              "authorize",
					StartTimeUnixNano: 1_750_000_000_020_000_000,
					EndTimeUnixNano:   1_750_000_000_080_000_000,
					Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
				},
			},
		}},
	}}}
}

func stringAttribute(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: value},
		},
	}
}

func stringPointer(value string) *string {
	return &value
}

func traceTestContext(t *testing.T, config mcpgrafana.GrafanaConfig) context.Context {
	t.Helper()
	u, err := url.Parse(config.URL)
	require.NoError(t, err)
	transportConfig := client.DefaultTransportConfig()
	transportConfig.Host = u.Host
	transportConfig.Schemes = []string{u.Scheme}
	transportConfig.APIKey = "test"
	apiClient := client.NewHTTPClientWithConfig(nil, transportConfig)
	ctx := mcpgrafana.WithGrafanaClient(
		mcpgrafana.WithGrafanaConfig(t.Context(), config),
		&mcpgrafana.GrafanaClient{GrafanaHTTPAPI: apiClient},
	)
	return ctx
}

func writeTempoDatasource(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"uid": "tempo-main", "name": "Main Tempo", "type": "tempo", "id": 1,
	}))
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

var _ io.Reader = failingReader{}

func callInteractiveTrace(ctx context.Context, args GetTempoTraceParams) (*mcp.CallToolResult, error) {
	values := map[string]any{"trace_id": args.TraceID, "datasourceUid": args.DatasourceUID}
	if args.FocusSpanID != nil {
		values["focus_span_id"] = *args.FocusSpanID
	}
	return GetTempoTraceTool.Handler(ctx, makeTempoRequest("get_tempo_trace", values))
}
