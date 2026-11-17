package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// receiversServer serves an empty provisioning contact point list, the
// receivers API discovery document and the given receivers.
func receiversServer(t *testing.T, receivers []map[string]any, served bool) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	server := httptest.NewServer(withFrontendSettings("stacks-1", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/provisioning/contact-points":
			_, _ = w.Write([]byte(`[]`))
		case "/apis/" + receiversAPIGroup:
			if !served {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"versions":[{"version":"v0alpha1"},{"version":"v1beta1"}]}`))
		case "/apis/" + receiversAPIGroup + "/v1beta1/namespaces/stacks-1/receivers":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": receivers, "metadata": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, &paths
}

// receiversContext wires the Grafana client, and optionally the Kubernetes
// client, to server.
func receiversContext(t *testing.T, server *httptest.Server, withK8s bool) context.Context {
	t.Helper()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: server.URL})
	ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)})
	if withK8s {
		ctx = mcpgrafana.WithKubernetesClient(ctx, &mcpgrafana.KubernetesClient{BaseURL: server.URL, HTTPClient: server.Client()})
	}
	return ctx
}

func TestGetContactPoint_FallsBackToReceiversAPI(t *testing.T) {
	receivers := []map[string]any{
		{"metadata": map[string]any{"name": "other-uid"}, "spec": map[string]any{"title": "other", "integrations": []any{}}},
		{"metadata": map[string]any{"name": "ZW1wdHk"}, "spec": map[string]any{"title": "empty", "integrations": []any{}}},
		{"metadata": map[string]any{"name": "imported-uid"}, "spec": map[string]any{"title": "pager", "integrations": []any{
			map[string]any{"uid": "i1", "type": "webhook", "version": "v0mimir1", "settings": map[string]any{"url": "https://example.com"}, "secureFields": map[string]any{"password": true}},
		}}},
	}

	for _, tc := range []struct {
		title, uid, note string
		integrations     int
	}{
		{"empty", "ZW1wdHk", "has no integrations", 0},
		{"pager", "imported-uid", "imported from a Mimir or Alertmanager configuration", 1},
	} {
		t.Run(tc.title, func(t *testing.T) {
			server, _ := receiversServer(t, receivers, true)
			ctx := receiversContext(t, server, true)

			result, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_point", ContactPointTitle: strPtr(tc.title)})
			require.NoError(t, err)
			detail, ok := result.(*receiverDetail)
			require.True(t, ok, "got %T", result)
			require.Equal(t, tc.title, detail.Name)
			require.Equal(t, tc.uid, detail.UID)
			require.Len(t, detail.Integrations, tc.integrations)
			require.Contains(t, detail.Note, tc.note)
			if tc.integrations > 0 {
				require.Equal(t, "v0mimir1", detail.Integrations[0].Version)
				require.Equal(t, []string{"password"}, detail.Integrations[0].SecureFields)
			}
		})
	}
}

func TestGetContactPoint_NotFoundAnywhere(t *testing.T) {
	server, _ := receiversServer(t, nil, true)
	ctx := receiversContext(t, server, true)

	_, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_point", ContactPointTitle: strPtr("missing")})
	require.EqualError(t, err, `contact point "missing" not found`)
}

func TestGetContactPoint_ReceiversAPINotServed(t *testing.T) {
	server, paths := receiversServer(t, nil, false)
	ctx := receiversContext(t, server, true)

	_, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_point", ContactPointTitle: strPtr("empty")})
	require.EqualError(t, err, `contact point "empty" not found`)
	require.NotContains(t, *paths, "/apis/"+receiversAPIGroup+"/v1beta1/namespaces/stacks-1/receivers")
}

func TestGetContactPoint_NoKubernetesClient(t *testing.T) {
	server, _ := receiversServer(t, nil, true)
	ctx := receiversContext(t, server, false)

	_, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_point", ContactPointTitle: strPtr("empty")})
	require.EqualError(t, err, `contact point "empty" not found`)
}

func TestPreferredReceiversVersion(t *testing.T) {
	require.Equal(t, "v1beta1", preferredReceiversVersion([]string{"v0alpha1", "v1beta1"}))
	require.Equal(t, "v0alpha1", preferredReceiversVersion([]string{"v0alpha1"}))
	require.Equal(t, "v2", preferredReceiversVersion([]string{"v2"}))
	require.Empty(t, preferredReceiversVersion([]string{}))
}
