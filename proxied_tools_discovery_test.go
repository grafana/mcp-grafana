package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	grafana_client "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// fakeDatasource describes one tempo-type datasource to serve from the fake
// Grafana instance's /api/datasources list, along with the handler that
// answers its /api/datasources/proxy/uid/<uid>/api/mcp endpoint (both the
// probe DELETE and any real MCP traffic).
type fakeDatasource struct {
	uid     string
	name    string
	handler http.Handler
}

// newFakeGrafana serves a minimal /api/datasources list (one entry per ds,
// all type "tempo") and routes /api/datasources/proxy/uid/<uid>/api/mcp to
// that datasource's handler, mirroring the real Grafana datasource proxy path
// discoverMCPDatasources/NewProxiedClient talk to. Returns a context carrying
// a GrafanaConfig/GrafanaClient pointed at the returned server, matching
// newProxiedToolsTestContext's shape in session_test.go.
func newFakeGrafana(t *testing.T, dss ...fakeDatasource) (*httptest.Server, context.Context) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		type dsListItem struct {
			UID  string `json:"uid"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		list := make([]dsListItem, 0, len(dss))
		for _, ds := range dss {
			list = append(list, dsListItem{UID: ds.uid, Name: ds.name, Type: "tempo"})
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(list))
	})
	for _, ds := range dss {
		path := fmt.Sprintf("/api/datasources/proxy/uid/%s/api/mcp", ds.uid)
		mux.Handle(path, ds.handler)
	}

	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	parsed, err := url.Parse(httpServer.URL)
	require.NoError(t, err)

	cfg := grafana_client.DefaultTransportConfig()
	cfg.Host = parsed.Host
	cfg.Schemes = []string{"http"}
	grafanaClient := grafana_client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	grafanaCfg := GrafanaConfig{URL: httpServer.URL}
	ctx := WithGrafanaConfig(context.Background(), grafanaCfg)
	ctx = WithGrafanaClient(ctx, &GrafanaClient{GrafanaHTTPAPI: grafanaClient})
	return httpServer, ctx
}

// scriptedHandler answers with the status codes in sequence (one per call,
// repeating the last for any call beyond the sequence's length), regardless
// of HTTP method. It's used to simulate a datasource's probe endpoint
// behaving transiently or deterministically across retry attempts.
func scriptedHandler(t *testing.T, statuses ...int) (http.HandlerFunc, func() int32) {
	t.Helper()
	var calls int32
	return func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		idx := int(n) - 1
		if idx >= len(statuses) {
			idx = len(statuses) - 1
		}
		w.WriteHeader(statuses[idx])
	}, func() int32 { return atomic.LoadInt32(&calls) }
}

func TestDiscoverMCPDatasources_ProbeRetry(t *testing.T) {
	t.Run("transient failure then success is retried and included", func(t *testing.T) {
		handler, callCount := scriptedHandler(t, http.StatusServiceUnavailable, http.StatusOK)
		_, ctx := newFakeGrafana(t, fakeDatasource{uid: "flaky", name: "Flaky Tempo", handler: handler})

		discovered, candidateCount, _, err := discoverMCPDatasources(ctx, slog.Default(), newDiscoveryMetrics(nil))
		require.NoError(t, err)

		assert.Equal(t, 1, candidateCount)
		require.Len(t, discovered, 1)
		assert.Equal(t, "flaky", discovered[0].UID)
		assert.Equal(t, int32(2), callCount(), "probe should be retried exactly once after the transient 503")
	})

	t.Run("deterministic 404 is excluded without retry", func(t *testing.T) {
		handler, callCount := scriptedHandler(t, http.StatusNotFound)
		_, ctx := newFakeGrafana(t, fakeDatasource{uid: "unsupported", name: "Not MCP", handler: handler})

		discovered, candidateCount, _, err := discoverMCPDatasources(ctx, slog.Default(), newDiscoveryMetrics(nil))
		require.NoError(t, err)

		assert.Equal(t, 1, candidateCount)
		assert.Empty(t, discovered)
		assert.Equal(t, int32(1), callCount(), "a clean 404 must not be retried")
	})

	t.Run("persistent transient failure exhausts retries and is excluded", func(t *testing.T) {
		handler, callCount := scriptedHandler(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusServiceUnavailable)
		_, ctx := newFakeGrafana(t, fakeDatasource{uid: "down", name: "Down Tempo", handler: handler})

		discovered, candidateCount, _, err := discoverMCPDatasources(ctx, slog.Default(), newDiscoveryMetrics(nil))
		require.NoError(t, err)

		assert.Equal(t, 1, candidateCount)
		assert.Empty(t, discovered)
		assert.Equal(t, int32(mcpRetryMaxAttempts), callCount(), "probe should stop retrying at mcpRetryMaxAttempts")
	})

	t.Run("independent candidates succeed and fail independently", func(t *testing.T) {
		goodHandler, goodCalls := scriptedHandler(t, http.StatusOK)
		badHandler, badCalls := scriptedHandler(t, http.StatusNotFound)
		_, ctx := newFakeGrafana(t,
			fakeDatasource{uid: "good", name: "Good Tempo", handler: goodHandler},
			fakeDatasource{uid: "bad", name: "Bad Tempo", handler: badHandler},
		)

		discovered, candidateCount, _, err := discoverMCPDatasources(ctx, slog.Default(), newDiscoveryMetrics(nil))
		require.NoError(t, err)

		assert.Equal(t, 2, candidateCount)
		require.Len(t, discovered, 1)
		assert.Equal(t, "good", discovered[0].UID)
		assert.Equal(t, int32(1), goodCalls())
		assert.Equal(t, int32(1), badCalls())
	})
}

// TestDiscoverMCPDatasources_MetricsUseInjectedMeterProvider verifies that
// newDiscoveryMetrics routes its instruments to an explicitly given provider
// instead of the (possibly noop) global one, so discovery/connect metrics
// reach a scrapeable registry in deployments that reset
// otel.GetMeterProvider() for reasons unrelated to mcp-grafana (see issue
// #1072).
func TestDiscoverMCPDatasources_MetricsUseInjectedMeterProvider(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	handler, _ := scriptedHandler(t, http.StatusOK)
	_, ctx := newFakeGrafana(t, fakeDatasource{uid: "good", name: "Good Tempo", handler: handler})

	_, _, _, err := discoverMCPDatasources(ctx, slog.Default(), newDiscoveryMetrics(provider))
	require.NoError(t, err)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	require.Len(t, data.ScopeMetrics, 1)

	names := make(map[string]bool)
	for _, m := range data.ScopeMetrics[0].Metrics {
		names[m.Name] = true
	}
	assert.True(t, names["mcp.discovery.probe_success"])
}

func TestBuildProxiedToolSet_ConnectFailureExcludedOthersSucceed(t *testing.T) {
	// "good" is a real, working MCP server: probing and connecting to it must
	// succeed. Each "bad-N" answers the probe (200, so it's discovered) but
	// always fails the connect handshake (503) after a delay, so each must be
	// retried then excluded without blocking "good" or each other.
	goodMCPServer := server.NewMCPServer("good-tempo", "0.0.0")
	goodMCPServer.AddTool(mcp.NewTool("example"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, nil
	})
	goodStreamable := server.NewStreamableHTTPServer(goodMCPServer, server.WithStateLess(true))

	const (
		badCount = 4
		badDelay = 400 * time.Millisecond
	)
	slowFailingHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			// Answer the discovery probe successfully so this candidate is
			// discovered and buildProxiedToolSet actually attempts to connect.
			w.WriteHeader(http.StatusOK)
			return
		}
		time.Sleep(badDelay)
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	dss := []fakeDatasource{{uid: "good", name: "Good Tempo", handler: goodStreamable}}
	for i := 0; i < badCount; i++ {
		dss = append(dss, fakeDatasource{
			uid:     fmt.Sprintf("bad-%d", i),
			name:    fmt.Sprintf("Bad Tempo %d", i),
			handler: http.HandlerFunc(slowFailingHandler),
		})
	}
	_, ctx := newFakeGrafana(t, dss...)

	sm := NewSessionManager()
	t.Cleanup(sm.Close)
	tm := NewToolManager(sm, server.NewMCPServer("test", "0.0.0"), WithProxiedTools(true))

	start := time.Now()
	built, err := tm.buildProxiedToolSet(ctx, slog.Default())
	elapsed := time.Since(start)
	require.NoError(t, err)

	assert.Equal(t, buildStats{candidates: badCount + 1, discovered: badCount + 1, connectFailed: badCount}, built.stats)
	require.Len(t, built.clients, 1)
	// Clients are keyed by (org, type, uid); this build has no per-call org, so
	// they live under the connection org the build resolved.
	_, hasGood := built.clients[proxiedClientKey(built.connectionOrgID, "tempo", "good")]
	assert.True(t, hasGood, "the working datasource must still be connected")

	// Each bad candidate takes mcpRetryMaxAttempts*badDelay to exhaust its
	// retries (~800ms here). Sequentially, badCount of them would compound to
	// ~badCount*mcpRetryMaxAttempts*badDelay (~3.2s); concurrently, they all
	// exhaust their retries in parallel, so total time stays close to a
	// single candidate's worst case regardless of badCount.
	assert.Less(t, elapsed, time.Duration(badCount)*time.Duration(mcpRetryMaxAttempts)*badDelay,
		"connecting to datasources must happen concurrently, not sequentially")
}
