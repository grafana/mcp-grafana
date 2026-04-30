//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVMServer captures the most recent /api/ds/query payload and returns a
// canned dsQueryResponse so tests can assert payload shape and response decoding
// independently.
type fakeVMServer struct {
	server      *httptest.Server
	lastPayload map[string]interface{}
	response    dsQueryResponse
}

func newFakeVMServer(t *testing.T, response dsQueryResponse) *fakeVMServer {
	t.Helper()
	f := &fakeVMServer{response: response}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/ds/query", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &f.lastPayload))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(f.response))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func newTestVMBackend(t *testing.T, server *httptest.Server) *victoriaMetricsBackend {
	t.Helper()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: server.URL})
	ds := &models.DataSource{UID: "vm-uid", Type: victoriaMetricsDatasourceType}
	b, err := newVictoriaMetricsBackend(ctx, "vm-uid", ds)
	require.NoError(t, err)
	// Override baseURL on the test server (defensive — should already match).
	b.baseURL = server.URL
	return b
}

func TestVictoriaMetricsBackendQuery_PayloadShape(t *testing.T) {
	cases := []struct {
		name           string
		queryType      string
		wantInstant    bool
		wantRange      bool
		stepSeconds    int
		wantInterval   string
		wantIntervalMs float64
	}{
		{
			name:           "instant query",
			queryType:      "instant",
			wantInstant:    true,
			wantRange:      false,
			stepSeconds:    0,
			wantInterval:   "60s",
			wantIntervalMs: 60000,
		},
		{
			name:           "range query with step",
			queryType:      "range",
			wantInstant:    false,
			wantRange:      true,
			stepSeconds:    30,
			wantInterval:   "30s",
			wantIntervalMs: 30000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeVMServer(t, dsQueryResponse{Results: map[string]dsQueryResult{
				"A": {Frames: []dsQueryFrame{}},
			}})
			b := newTestVMBackend(t, fake.server)

			start := time.Unix(1700000000, 0)
			end := time.Unix(1700003600, 0)
			_, err := b.Query(context.Background(), `up{job="prometheus"}`, tc.queryType, start, end, tc.stepSeconds)
			require.NoError(t, err)

			require.NotNil(t, fake.lastPayload)
			queries, ok := fake.lastPayload["queries"].([]interface{})
			require.True(t, ok, "payload should contain queries array")
			require.Len(t, queries, 1)

			q := queries[0].(map[string]interface{})
			assert.Equal(t, "A", q["refId"])
			assert.Equal(t, `up{job="prometheus"}`, q["expr"])
			assert.Equal(t, tc.wantInstant, q["instant"])
			assert.Equal(t, tc.wantRange, q["range"])
			assert.Equal(t, tc.wantInterval, q["interval"])
			assert.Equal(t, tc.wantIntervalMs, q["intervalMs"])

			ds := q["datasource"].(map[string]interface{})
			assert.Equal(t, victoriaMetricsDatasourceType, ds["type"])
			assert.Equal(t, "vm-uid", ds["uid"])

			assert.Equal(t, "1700000000000", fake.lastPayload["from"])
			assert.Equal(t, "1700003600000", fake.lastPayload["to"])
		})
	}
}

func TestVictoriaMetricsBackendQuery_DecodesInstantResponse(t *testing.T) {
	fake := newFakeVMServer(t, dsQueryResponse{Results: map[string]dsQueryResult{
		"A": {Frames: []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Name: "up",
					Fields: []dsQueryFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"job": "prometheus"}},
					},
				},
				Data: dsQueryFrameData{
					Values: [][]interface{}{
						{float64(1700000000000)},
						{float64(1)},
					},
				},
			},
		}},
	}})
	b := newTestVMBackend(t, fake.server)

	v, err := b.Query(context.Background(), "up", "instant", time.Time{}, time.Unix(1700000000, 0), 0)
	require.NoError(t, err)

	vec, ok := v.(model.Vector)
	require.True(t, ok)
	require.Len(t, vec, 1)
	assert.Equal(t, model.SampleValue(1), vec[0].Value)
	assert.Equal(t, model.LabelValue("up"), vec[0].Metric["__name__"])
	assert.Equal(t, model.LabelValue("prometheus"), vec[0].Metric["job"])
}

func TestVictoriaMetricsBackendQuery_DecodesRangeResponseAsMatrix(t *testing.T) {
	fake := newFakeVMServer(t, dsQueryResponse{Results: map[string]dsQueryResult{
		"A": {Frames: []dsQueryFrame{
			{
				Schema: dsQueryFrameSchema{
					Name: "up",
					Fields: []dsQueryFrameField{
						{Name: "Time", Type: "time"},
						{Name: "Value", Type: "number", Labels: map[string]string{"job": "prometheus"}},
					},
				},
				Data: dsQueryFrameData{
					Values: [][]interface{}{
						{float64(1700000000000), float64(1700000060000)},
						{float64(1), float64(0)},
					},
				},
			},
		}},
	}})
	b := newTestVMBackend(t, fake.server)

	v, err := b.Query(
		context.Background(),
		"up",
		"range",
		time.Unix(1700000000, 0),
		time.Unix(1700000060, 0),
		60,
	)
	require.NoError(t, err)

	want := model.Matrix{{
		Metric: model.Metric{"__name__": "up", "job": "prometheus"},
		Values: []model.SamplePair{
			{Timestamp: model.Time(1700000000000), Value: 1},
			{Timestamp: model.Time(1700000060000), Value: 0},
		},
	}}
	assert.Equal(t, want, v)
}

func TestVictoriaMetricsBackendQuery_PropagatesUpstreamError(t *testing.T) {
	fake := newFakeVMServer(t, dsQueryResponse{Results: map[string]dsQueryResult{
		"A": {Error: "rate: bad expression"},
	}})
	b := newTestVMBackend(t, fake.server)

	_, err := b.Query(context.Background(), "rate(", "instant", time.Time{}, time.Unix(1700000000, 0), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate: bad expression")
}

func TestVictoriaMetricsBackendQuery_RejectsInvalidQueryType(t *testing.T) {
	fake := newFakeVMServer(t, dsQueryResponse{})
	b := newTestVMBackend(t, fake.server)

	_, err := b.Query(context.Background(), "up", "bogus", time.Time{}, time.Unix(1700000000, 0), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid query type")
	assert.Nil(t, fake.lastPayload, "should not hit the server with an invalid query type")
}

func TestVictoriaMetricsBackendQuery_DefaultsToNowWhenBothTimesZero(t *testing.T) {
	fake := newFakeVMServer(t, dsQueryResponse{Results: map[string]dsQueryResult{
		"A": {Frames: []dsQueryFrame{}},
	}})
	b := newTestVMBackend(t, fake.server)

	before := time.Now().UnixMilli()
	_, err := b.Query(context.Background(), "up", "instant", time.Time{}, time.Time{}, 0)
	require.NoError(t, err)
	after := time.Now().UnixMilli()

	require.NotNil(t, fake.lastPayload)
	from, err := strconv.ParseInt(fake.lastPayload["from"].(string), 10, 64)
	require.NoError(t, err)
	to, err := strconv.ParseInt(fake.lastPayload["to"].(string), 10, 64)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, from, before)
	assert.LessOrEqual(t, to, after)
	assert.Equal(t, from, to, "both times default to the same now() value")
}

func TestVictoriaMetricsBackendQuery_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(server.Close)

	b := newTestVMBackend(t, server)
	_, err := b.Query(context.Background(), "up", "instant", time.Time{}, time.Unix(1700000000, 0), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "querying VictoriaMetrics instant")
	assert.Contains(t, err.Error(), "status 500")
}
