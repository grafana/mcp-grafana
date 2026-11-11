//go:build unit

package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListAlertmanagerReceiversImplementationPath(t *testing.T) {
	const cortexBody = "alertmanager_config: |\n  route:\n    receiver: team\n  receivers:\n    - name: team\n"
	const promBody = `{"config":{"original":"route:\n  receiver: team\nreceivers:\n  - name: team\n"}}`
	for _, tt := range []struct {
		name     string
		jsonData string
		wantPath string
	}{
		{"missing implementation defaults to cortex", `{}`, "/api/v1/alerts"},
		{"null implementation defaults to cortex", `{"implementation":null}`, "/api/v1/alerts"},
		{"mimir", `{"implementation":"mimir"}`, "/api/v1/alerts"},
		{"prometheus", `{"implementation":"prometheus"}`, "/api/v2/status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/datasources/uid/am" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"uid":"am","type":"alertmanager","jsonData":` + tt.jsonData + `}`))
					return
				}
				require.Equal(t, "/api/datasources/proxy/uid/am"+tt.wantPath, r.URL.Path)
				if tt.wantPath == "/api/v2/status" {
					_, _ = w.Write([]byte(promBody))
					return
				}
				_, _ = w.Write([]byte(cortexBody))
			}))
			t.Cleanup(server.Close)

			receivers, err := listAlertmanagerReceivers(mockDatasourcesCtx(server), ListContactPointsParams{DatasourceUID: strPtr("am")})
			require.NoError(t, err)
			require.Len(t, receivers, 1)
			require.Equal(t, "team", receivers[0].Name)
		})
	}
}
