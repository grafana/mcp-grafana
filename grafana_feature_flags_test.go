//go:build unit

package mcpgrafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGrafanaFeatureEnabled(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		status         int
		enabled, known bool
	}{
		{"enabled", `{"featureToggles":{"cloudWatchDynamicLabels":true}}`, 200, true, true},
		{"disabled omitted", `{"featureToggles":{}}`, 200, false, true},
		{"disabled explicit", `{"featureToggles":{"cloudWatchDynamicLabels":false}}`, 200, false, true},
		{"unreported", `{}`, 200, false, false},
		{"null", `{"featureToggles":null}`, 200, false, false},
		{"forbidden", `{}`, 403, false, false},
		{"invalid", `invalid`, 200, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearFrontendSettingsCaches()
			t.Cleanup(clearFrontendSettingsCaches)
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "/api/frontend/settings", r.URL.Path)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(ts.Close)
			ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
			for range 2 {
				enabled, known := GrafanaFeatureEnabled(ctx, "cloudWatchDynamicLabels")
				assert.Equal(t, tc.enabled, enabled)
				assert.Equal(t, tc.known, known)
			}
			wantCalls := 1
			if tc.status != 200 || tc.name == "invalid" {
				wantCalls = 2
			}
			assert.Equal(t, wantCalls, calls)
		})
	}
}
