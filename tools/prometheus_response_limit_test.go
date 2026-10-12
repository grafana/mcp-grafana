//go:build unit

package tools

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusBackendResponseLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     int64
		encoding string
		instant  bool
		fallback bool
		wantErr  bool
	}{
		{name: "below limit", size: defaultResponseLimitBytes - 1},
		{name: "at limit", size: defaultResponseLimitBytes},
		{name: "above limit", size: defaultResponseLimitBytes + 1, wantErr: true},
		{name: "chunked at limit", size: defaultResponseLimitBytes, encoding: "chunked"},
		{name: "chunked above limit", size: defaultResponseLimitBytes + 1, encoding: "chunked", wantErr: true},
		{name: "gzip at limit", size: defaultResponseLimitBytes, encoding: "gzip"},
		{name: "gzip above limit", size: defaultResponseLimitBytes + 1, encoding: "gzip", wantErr: true},
		{name: "instant at limit", size: defaultResponseLimitBytes, instant: true},
		{name: "instant above limit", size: defaultResponseLimitBytes + 1, instant: true, wantErr: true},
		{name: "fallback above limit", size: defaultResponseLimitBytes + 1, fallback: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"up"},"values":[[1700000000,"1"]]}]}}`
			if tc.instant {
				result = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1700000000,"1"]}]}}`
			}
			body := result + strings.Repeat(" ", int(tc.size)-len(result))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.fallback && strings.Contains(r.URL.Path, "/resources/") {
					http.Error(w, "use the proxy endpoint", http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch tc.encoding {
				case "gzip":
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					_, _ = io.WriteString(writer, body)
					_ = writer.Close()
				case "chunked":
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, body)
				default:
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
					_, _ = io.WriteString(w, body)
				}
			}))
			t.Cleanup(server.Close)
			ctx := mockDatasourcesCtx(server)
			uid := "response-limit-" + strings.ReplaceAll(tc.name, " ", "-")
			t.Cleanup(func() { fallbackEndpoints.Delete("/api/datasources/uid/" + uid + "/resources/api/v1/query_range") })
			backend, err := newPrometheusBackend(ctx, uid, &models.DataSource{})
			require.NoError(t, err)
			queryType := "range"
			if tc.instant {
				queryType = "instant"
			}
			end := time.Unix(1700000000, 0)
			value, _, err := backend.Query(ctx, "up", queryType, end.Add(-time.Hour), end, 60)
			if tc.wantErr {
				require.ErrorContains(t, err, "response body exceeds maximum size of 10485760 bytes; try narrowing your query")
				assert.Nil(t, value)
				return
			}
			require.NoError(t, err)
			if tc.instant {
				assert.Equal(t, model.Vector{&model.Sample{
					Metric: model.Metric{"__name__": "up"}, Timestamp: 1700000000000, Value: 1,
				}}, value)
				return
			}
			assert.Equal(t, model.Matrix{&model.SampleStream{
				Metric: model.Metric{"__name__": "up"},
				Values: []model.SamplePair{{Timestamp: 1700000000000, Value: 1}},
			}}, value)
		})
	}
}

func TestPrometheusBackendResponseLimitMetadataAndErrors(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, strings.Repeat("x", int(defaultResponseLimitBytes)+1))
			}))
			t.Cleanup(server.Close)
			backend, err := newPrometheusBackend(mockDatasourcesCtx(server), "prom-uid", &models.DataSource{})
			require.NoError(t, err)
			values, err := backend.LabelValues(context.Background(), "__name__", nil, time.Time{}, time.Time{})
			require.ErrorContains(t, err, "response body exceeds maximum size")
			assert.Nil(t, values)
		})
	}
}

func TestPrometheusLimitedResponseBodyStopsReadingAndCloses(t *testing.T) {
	underlying := &trackedPrometheusBody{Reader: strings.NewReader(strings.Repeat("x", int(defaultResponseLimitBytes)*2))}
	body := &prometheusLimitedResponseBody{
		ReadCloser: underlying,
		reader:     io.LimitedReader{R: underlying, N: defaultResponseLimitBytes + 1},
	}
	_, err := io.Copy(io.Discard, body)
	require.ErrorContains(t, err, "response body exceeds maximum size")
	assert.Equal(t, defaultResponseLimitBytes+1, underlying.bytesRead)
	_, err = body.Read(make([]byte, 1))
	require.ErrorContains(t, err, "response body exceeds maximum size")
	assert.Equal(t, defaultResponseLimitBytes+1, underlying.bytesRead)
	require.NoError(t, body.Close())
	assert.True(t, underlying.closed)
}

type trackedPrometheusBody struct {
	io.Reader
	bytesRead int64
	closed    bool
}

func (b *trackedPrometheusBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.bytesRead += int64(n)
	return n, err
}

func (b *trackedPrometheusBody) Close() error {
	b.closed = true
	return nil
}
