// Requires the grafana-9, Prometheus, Loki, Graphite, and Tempo docker-compose
// services. Grafana 9.0 is the oldest supported release, so these tests cover
// its datasource UID routes and trace frame format.
//
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const grafana9TraceID = "97de86876c12145b90292ea788438895"

func grafana9URL() string {
	if u, ok := os.LookupEnv("GRAFANA_9_URL"); ok {
		return u
	}
	return "http://localhost:3003"
}

func tempoOTLPHTTPURL() string {
	if u, ok := os.LookupEnv("TEMPO_OTLP_HTTP_URL"); ok {
		return u
	}
	return "http://localhost:4318"
}

func TestGrafana9DatasourceUIDRoutes(t *testing.T) {
	ctx := newTestContextForURL(grafana9URL())

	t.Run("Prometheus", func(t *testing.T) {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "prometheus"})
		require.NoError(t, err)
		assert.Equal(t, "prometheus", ds.UID)

		result, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: "prometheus",
			Expr:          "time()",
			QueryType:     "instant",
			EndTime:       "now-1m",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.String())

		names, err := listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{DatasourceUID: "prometheus"})
		require.NoError(t, err)
		assert.NotEmpty(t, names)
	})

	t.Run("Loki", func(t *testing.T) {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "loki"})
		require.NoError(t, err)
		assert.Equal(t, "loki", ds.UID)

		_, err = listLokiLabelNames(ctx, ListLokiLabelNamesParams{DatasourceUID: "loki"})
		require.NoError(t, err)
	})

	t.Run("Graphite", func(t *testing.T) {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "graphite"})
		require.NoError(t, err)
		assert.Equal(t, "graphite", ds.UID)

		metrics, err := listGraphiteMetrics(ctx, ListGraphiteMetricsParams{DatasourceUID: "graphite", Query: "*"})
		require.NoError(t, err)
		assert.NotEmpty(t, metrics)
	})
}

func TestGrafana9TempoTraceRoundTrip(t *testing.T) {
	traceData := grafana9TraceData()
	moveTraceToNow(traceData)
	writeTraceToTempo(t, traceData)

	ctx := newTestContextForURL(grafana9URL())
	var trace tempoTrace
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		result, err := getTempoTrace(ctx, GetTempoTraceParams{TraceID: grafana9TraceID, DatasourceUID: "tempo"})
		if !assert.NoError(c, err) || !assert.False(c, result.IsError, tempoResultText(result)) {
			return
		}
		assert.NoError(c, json.Unmarshal([]byte(tempoResultText(result)), &trace))
	}, 15*time.Second, 250*time.Millisecond)

	require.Len(t, trace.Services, 1)
	assert.Equal(t, "hggateway", trace.Services[0].ServiceName)
	assert.Equal(t, "hosted-grafana", trace.Services[0].Resource["k8s.namespace.name"])
	require.Len(t, trace.Services[0].Spans, 2)
	assert.Equal(t, "GET", trace.Services[0].Spans[0].Attributes["http.request.method"])
	assert.Equal(t, float64(27), trace.Services[0].Spans[0].Attributes["http.response.body.size"])
	assert.Equal(t, 1.0, trace.Services[0].Spans[0].Attributes["jaeger.sampler.param"])
	require.Len(t, trace.Services[0].Spans[1].Events, 1)
	assert.Equal(t, "found resources registered for slug", trace.Services[0].Spans[1].Events[0].Name)
	assert.Equal(t, "1", trace.Services[0].Spans[1].Events[0].Attributes["count"])
}

// grafana9TraceData is a minimized version of a real hggateway trace fetched
// with gcx. It retains representative resource and scope metadata, typed span
// attributes, a parent-child relationship, and an event without carrying the
// full production trace into the test suite.
func grafana9TraceData() *tracepb.TracesData {
	traceID := []byte{0x97, 0xde, 0x86, 0x87, 0x6c, 0x12, 0x14, 0x5b, 0x90, 0x29, 0x2e, 0xa7, 0x88, 0x43, 0x88, 0x95}
	rootSpanID := []byte{0xbf, 0x09, 0xea, 0x4d, 0xe3, 0xbf, 0x25, 0x87}
	root := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            rootSpanID,
		Name:              "GET",
		Kind:              tracepb.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: 1_793_359_446_883_616_937,
		EndTimeUnixNano:   1_793_359_446_921_119_739,
		Attributes: []*commonpb.KeyValue{
			otlpStringAttribute("http.request.method", "GET"),
			otlpIntAttribute("http.response.body.size", 27),
			otlpDoubleAttribute("jaeger.sampler.param", 1),
		},
	}
	child := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            []byte{0x22, 0x53, 0x1d, 0xa4, 0xa7, 0xc9, 0xfb, 0xc9},
		ParentSpanId:      rootSpanID,
		Name:              "HTTP GET",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: 1_793_359_446_892_072_853,
		EndTimeUnixNano:   1_793_359_446_920_982_701,
		Events: []*tracepb.Span_Event{{
			Name:         "found resources registered for slug",
			TimeUnixNano: 1_793_359_446_892_179_670,
			Attributes: []*commonpb.KeyValue{
				otlpStringAttribute("count", "1"),
				otlpStringAttribute("slug", "test-stack"),
			},
		}},
	}
	return &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			otlpStringAttribute("service.name", "hggateway"),
			otlpStringAttribute("k8s.namespace.name", "hosted-grafana"),
			otlpStringAttribute("deployment.environment.name", "dev-us-central-0"),
		}},
		ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &commonpb.InstrumentationScope{
				Name:    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp",
				Version: "0.72.0",
			},
			Spans: []*tracepb.Span{root, child},
		}},
	}}}
}

func otlpStringAttribute(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: value},
		},
	}
}

func otlpIntAttribute(key string, value int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_IntValue{IntValue: value},
		},
	}
}

func otlpDoubleAttribute(key string, value float64) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value},
		},
	}
}

func writeTraceToTempo(t *testing.T, traceData *tracepb.TracesData) {
	t.Helper()
	payload, err := protojson.Marshal(&collectortracepb.ExportTraceServiceRequest{
		ResourceSpans: traceData.ResourceSpans,
	})
	require.NoError(t, err)
	payload, err = encodeOTLPJSONIDs(payload)
	require.NoError(t, err)

	resp, err := http.Post(tempoOTLPHTTPURL()+"/v1/traces", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
}

// OTLP/HTTP JSON represents trace and span IDs as hexadecimal strings, while
// protojson uses the generic base64 representation for protobuf byte fields.
func encodeOTLPJSONIDs(payload []byte) ([]byte, error) {
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	var visit func(any) error
	visit = func(value any) error {
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				if err := visit(item); err != nil {
					return err
				}
			}
		case map[string]any:
			for key, item := range value {
				if key == "traceId" || key == "spanId" || key == "parentSpanId" {
					encoded, ok := item.(string)
					if !ok || encoded == "" {
						continue
					}
					decoded, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil {
						return err
					}
					value[key] = hex.EncodeToString(decoded)
					continue
				}
				if err := visit(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(document); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func moveTraceToNow(traceData *tracepb.TracesData) {
	const originalStart = uint64(1_793_359_446_883_616_937)
	delta := uint64(time.Now().Add(-time.Second).UnixNano()) - originalStart
	for _, resourceSpans := range traceData.ResourceSpans {
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				span.StartTimeUnixNano += delta
				span.EndTimeUnixNano += delta
				for _, event := range span.Events {
					event.TimeUnixNano += delta
				}
			}
		}
	}
}

func tempoResultText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text.Text)
}
