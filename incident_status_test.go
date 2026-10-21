package mcpgrafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/incident-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectIncidentErrorStatuses(t *testing.T) {
	status := http.StatusForbidden
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"incidentPreviews":[{"incidentID":"1"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":"You'll need additional permissions to perform this action. Permissions needed: plugins.app:access"}`))
	}))
	t.Cleanup(srv.Close)

	c := incident.NewClient(srv.URL+"/", "token")
	rejectIncidentErrorStatuses(c)
	is := incident.NewIncidentsService(c)

	// Without the check, incident-go decodes Grafana's JSON 403 as an empty
	// success.
	_, err := is.QueryIncidentPreviews(context.Background(), incident.QueryIncidentPreviewsRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "Permissions needed: plugins.app:access")

	status = http.StatusOK
	res, err := is.QueryIncidentPreviews(context.Background(), incident.QueryIncidentPreviewsRequest{})
	require.NoError(t, err)
	assert.Len(t, res.IncidentPreviews, 1)
}
