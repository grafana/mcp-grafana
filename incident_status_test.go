package mcpgrafana

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/incident-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const incidentDenialBody = `{"message":"You'll need additional permissions to perform this action. Permissions needed: plugins.app:access"}`

func queryIncidents(t *testing.T, url string) (*incident.QueryIncidentPreviewsResponse, error) {
	t.Helper()
	c := incident.NewClient(url+"/", "token")
	rejectIncidentErrorStatuses(c)
	return incident.NewIncidentsService(c).QueryIncidentPreviews(context.Background(), incident.QueryIncidentPreviewsRequest{})
}

func TestRejectIncidentErrorStatuses(t *testing.T) {
	t.Run("error status", func(t *testing.T) {
		// Without the check, incident-go decodes Grafana's JSON 403 as an
		// empty success.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(incidentDenialBody))
		}))
		t.Cleanup(srv.Close)

		_, err := queryIncidents(t, srv.URL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
		assert.Contains(t, err.Error(), "Permissions needed: plugins.app:access")
	})

	t.Run("gzipped error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusForbidden)
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte(incidentDenialBody))
			_ = zw.Close()
		}))
		t.Cleanup(srv.Close)

		_, err := queryIncidents(t, srv.URL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Permissions needed: plugins.app:access")
	})

	t.Run("success and redirects pass through", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/old/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/new/"+r.URL.Path[len("/old/"):], http.StatusTemporaryRedirect)
		})
		mux.HandleFunc("/new/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"incidentPreviews":[{"incidentID":"1"}]}`))
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		res, err := queryIncidents(t, srv.URL+"/old")
		require.NoError(t, err)
		assert.Len(t, res.IncidentPreviews, 1)
	})
}
