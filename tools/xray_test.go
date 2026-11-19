//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newXRayQueryMock(t *testing.T, frame *data.Frame, gotBody *map[string]interface{}) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(gotBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: data.Frames{frame}},
		}})
	}
}

// newXRayTraceSummariesFrame has the fields the X-Ray plugin returns for getTraceSummaries.
func newXRayTraceSummariesFrame(ids []string, starts []time.Time, durations []float64) *data.Frame {
	frame := data.NewFrame("TraceSummaries",
		data.NewField("Id", nil, []*string{}),
		data.NewField("Start Time", nil, []*time.Time{}),
		data.NewField("Method", nil, []*string{}),
		data.NewField("Response", nil, []*int32{}),
		data.NewField("Response Time", nil, []*float64{}),
		data.NewField("URL", nil, []*string{}),
		data.NewField("Client IP", nil, []*string{}),
		data.NewField("Annotations", nil, []*int64{}),
	)
	method, url, ip, status, annotations := "POST", "https://api.example.com/graphql", "203.0.113.7", int32(502), int64(1)
	for i := range ids {
		frame.AppendRow(&ids[i], &starts[i], &method, &status, &durations[i], &url, &ip, &annotations)
	}
	return frame
}

func TestSearchXRayTraces(t *testing.T) {
	t0 := time.Date(2026, 2, 2, 19, 0, 0, 0, time.UTC)
	frame := newXRayTraceSummariesFrame(
		[]string{"1-old", "1-new", "1-mid"},
		[]time.Time{t0, t0.Add(2 * time.Minute), t0.Add(time.Minute)},
		[]float64{0.5, 1.25, 3},
	)
	var gotBody map[string]interface{}
	ctx := newAzureMonitorMockGrafana(t, XRayDatasourceType, newXRayQueryMock(t, frame, &gotBody))

	result, err := searchXRayTraces(ctx, SearchXRayTracesParams{
		DatasourceUID:    "xray-uid",
		FilterExpression: `service("checkout") AND fault = true`,
		Start:            "2026-02-02T19:00:00Z",
		End:              "2026-02-02T20:00:00Z",
		Region:           "us-east-2",
		Limit:            2,
	})
	require.NoError(t, err)

	assert.True(t, result.Truncated)
	assert.Equal(t, []XRayTraceSummary{
		{ID: "1-new", StartTime: "2026-02-02T19:02:00Z", DurationMs: 1250, Method: "POST", URL: "https://api.example.com/graphql", Status: 502},
		{ID: "1-mid", StartTime: "2026-02-02T19:01:00Z", DurationMs: 3000, Method: "POST", URL: "https://api.example.com/graphql", Status: 502},
	}, result.Traces, "newest first, without the client IP")

	assert.Equal(t, "1770058800000", gotBody["from"])
	assert.Equal(t, "1770062400000", gotBody["to"])
	q := gotBody["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "X-Ray", q["queryMode"])
	assert.Equal(t, "getTraceSummaries", q["queryType"])
	assert.Equal(t, `service("checkout") AND fault = true`, q["query"])
	assert.Equal(t, "us-east-2", q["region"])
}

func TestSearchXRayTraces_Defaults(t *testing.T) {
	var gotBody map[string]interface{}
	ctx := newAzureMonitorMockGrafana(t, XRayDatasourceType, newXRayQueryMock(t, newXRayTraceSummariesFrame(nil, nil, nil), &gotBody))

	result, err := searchXRayTraces(ctx, SearchXRayTracesParams{DatasourceUID: "xray-uid"})
	require.NoError(t, err)
	assert.NotNil(t, result.Traces)
	assert.False(t, result.Truncated)

	q := gotBody["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "", q["query"])
	_, hasRegion := q["region"]
	assert.False(t, hasRegion, "no region lets the plugin use the datasource default")

	_, err = searchXRayTraces(ctx, SearchXRayTracesParams{DatasourceUID: "xray-uid", Start: "now", End: "now-1h"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "end time must be after start time")
}

func TestXRay_RejectsWrongDatasourceType(t *testing.T) {
	ctx := newAzureMonitorMockGrafana(t, "loki", nil)

	_, err := searchXRayTraces(ctx, SearchXRayTracesParams{DatasourceUID: "u"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is of type loki, not "+XRayDatasourceType)

	_, err = getXRayTrace(ctx, GetXRayTraceParams{DatasourceUID: "u", TraceID: "1-a-b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not "+XRayDatasourceType)
}

// newXRayTraceFrame wraps segment documents the way the plugin returns
// getTrace: one Traces frame whose Trace field is a JSON types.Trace.
func newXRayTraceFrame(t *testing.T, docs ...string) *data.Frame {
	t.Helper()
	segments := make([]map[string]string, len(docs))
	for i, d := range docs {
		segments[i] = map[string]string{"Document": d, "Id": "ignored"}
	}
	trace, err := json.Marshal(map[string]interface{}{"Id": "1-6738a1b2-0123456789abcdef01234567", "Duration": 1.0, "LimitExceeded": false, "Segments": segments})
	require.NoError(t, err)
	return data.NewFrame("Traces", data.NewField("Trace", nil, []string{string(trace)}))
}

func TestGetXRayTrace(t *testing.T) {
	// Out of order on purpose: the API does not order segments.
	lambda := `{"id":"b1","parent_id":"s1","name":"bifrost","origin":"AWS::Lambda::Function","start_time":1770058800.125,"end_time":1770058800.375,"fault":true,
		"cause":{"exceptions":[{"type":"TypeError","message":"x is undefined"}]},
		"subsegments":[{"id":"s3","name":"Overhead","start_time":1770058800.3125,"end_time":1770058800.375},{"id":"s2","name":"DynamoDB","start_time":1770058800.25,"end_time":1770058800.3125}]}`
	web := `{"id":"a1","name":"web","origin":"AWS::ECS::Container","start_time":1770058800,"end_time":1770058800.5,"fault":true,"http":{"response":{"status":502}},
		"subsegments":[{"id":"s1","name":"bifrost","start_time":1770058800.0625,"end_time":1770058800.4375,"fault":true,"http":{"response":{"status":500}},"cause":"0123456789abcdef"}]}`
	orphan := `{"id":"c1","parent_id":"not-in-trace","name":"worker","start_time":1770058800.75,"end_time":1770058801,"in_progress":false}`
	inferred := `{"id":"i1","parent_id":"s2","name":"DynamoDB","inferred":true,"origin":"AWS::DynamoDB::Table","start_time":1770058800.25,"end_time":1770058800.3125}`

	var gotBody map[string]interface{}
	ctx := newAzureMonitorMockGrafana(t, XRayDatasourceType, newXRayQueryMock(t, newXRayTraceFrame(t, lambda, inferred, orphan, web), &gotBody))

	result, err := getXRayTrace(ctx, GetXRayTraceParams{DatasourceUID: "xray-uid", TraceID: " 6738a1b20123456789abcdef01234567 "})
	require.NoError(t, err)

	assert.Equal(t, &XRayTraceResult{
		TraceID:    "1-6738a1b2-0123456789abcdef01234567",
		StartTime:  "2026-02-02T19:00:00Z",
		DurationMs: 1000,
		Segments: []*XRayTraceNode{
			{Name: "web", Origin: "AWS::ECS::Container", DurationMs: 500, HTTPStatus: 502, Fault: true, Children: []*XRayTraceNode{
				{Name: "bifrost", StartOffsetMs: 62.5, DurationMs: 375, HTTPStatus: 500, Fault: true, Children: []*XRayTraceNode{
					{Name: "bifrost", Origin: "AWS::Lambda::Function", StartOffsetMs: 125, DurationMs: 250, Fault: true, Exception: "TypeError: x is undefined", Children: []*XRayTraceNode{
						{Name: "DynamoDB", StartOffsetMs: 250, DurationMs: 62.5}, // without its inferred segment
						{Name: "Overhead", StartOffsetMs: 312.5, DurationMs: 62.5},
					}},
				}},
			}},
			{Name: "worker", StartOffsetMs: 750, DurationMs: 250},
		},
	}, result)

	q := gotBody["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "X-Ray", q["queryMode"])
	assert.Equal(t, "getTrace", q["queryType"])
	assert.Equal(t, "6738a1b20123456789abcdef01234567", q["query"], "W3C IDs pass through; the plugin converts them")
}

func TestGetXRayTrace_Errors(t *testing.T) {
	ctx := newAzureMonitorMockGrafana(t, XRayDatasourceType, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"error":"trace not found","status":500}}}`))
	})

	_, err := getXRayTrace(ctx, GetXRayTraceParams{DatasourceUID: "xray-uid", TraceID: "1-a-b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trace not found")

	_, err = getXRayTrace(ctx, GetXRayTraceParams{DatasourceUID: "xray-uid", TraceID: "  "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "traceId is required")
}

func TestParseXRayTrace_CyclicParentsDoNotHang(t *testing.T) {
	a := `{"id":"a","parent_id":"bs","name":"a","start_time":1,"end_time":2,"subsegments":[{"id":"as","name":"as","start_time":1,"end_time":2}]}`
	b := `{"id":"b","parent_id":"as","name":"b","start_time":1,"end_time":2,"subsegments":[{"id":"bs","name":"bs","start_time":1,"end_time":2}]}`

	result, err := parseXRayTrace(data.Frames{newXRayTraceFrame(t, a, b)})
	require.NoError(t, err)
	assert.Empty(t, result.Segments)
}

func TestXRayMs(t *testing.T) {
	assert.Equal(t, 1.0, xrayMs(1770058800.001-1770058800), "float noise is rounded to the microsecond")
	assert.Equal(t, 2517.993, xrayMs(2.5179934501))
}
