//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudMonitoringDiscoveryEndOnlyRange(t *testing.T) {
	end := time.Now().Add(-3 * time.Hour).Truncate(time.Millisecond)

	for _, tc := range []struct {
		name string
		call func(b *cloudMonitoringBackend) error
	}{
		{"label names", func(b *cloudMonitoringBackend) error {
			_, err := b.LabelNames(t.Context(), nil, time.Time{}, end)
			return err
		}},
		{"label values", func(b *cloudMonitoringBackend) error {
			_, err := b.LabelValues(t.Context(), "resource.type", nil, time.Time{}, end)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var from, to int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					From string `json:"from"`
					To   string `json:"to"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				from, _ = strconv.ParseInt(payload.From, 10, 64)
				to, _ = strconv.ParseInt(payload.To, 10, 64)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":{}}`))
			}))
			defer server.Close()

			b := &cloudMonitoringBackend{httpClient: server.Client(), baseURL: server.URL, datasourceUID: "cm", defaultProject: "project"}
			require.NoError(t, tc.call(b))

			assert.Equal(t, end.UnixMilli(), to, "the requested end should be kept")
			assert.Equal(t, end.Add(-time.Hour).UnixMilli(), from, "an end-only range should start an hour before the end, not after it")
		})
	}
}
