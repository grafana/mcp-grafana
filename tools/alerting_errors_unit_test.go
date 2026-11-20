package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-openapi/runtime"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// forbiddenTestContext wires the Grafana client to server.
func forbiddenTestContext(t *testing.T, server *httptest.Server) context.Context {
	t.Helper()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: server.URL})
	return mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)})
}

func TestExplainForbidden(t *testing.T) {
	forbidden := runtime.NewAPIError("unknown error", struct{}{}, http.StatusForbidden)
	err := explainForbidden(forbidden, contactPointsReadPermissions)
	require.ErrorContains(t, err, "permission denied (HTTP 403 Forbidden)")
	require.ErrorContains(t, err, contactPointsReadPermissions)
	require.ErrorIs(t, err, forbidden)

	notFound := runtime.NewAPIError("unknown error", struct{}{}, http.StatusNotFound)
	require.Same(t, notFound, explainForbidden(notFound, contactPointsReadPermissions))

	plain := errors.New("boom")
	require.Same(t, plain, explainForbidden(plain, contactPointsReadPermissions))
}

func TestAlertingProvisioning403sNamePermissions(t *testing.T) {
	server := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"You'll need additional permissions to perform this action."}`))
	}))
	t.Cleanup(server.Close)
	ctx := forbiddenTestContext(t, server)

	_, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_points"})
	require.ErrorContains(t, err, "list contact points: permission denied (HTTP 403 Forbidden)")
	require.ErrorContains(t, err, contactPointsReadPermissions)

	_, err = manageRouting(ctx, ManageRoutingParams{Operation: "get_contact_point", ContactPointTitle: strPtr("team")})
	require.ErrorContains(t, err, contactPointsReadPermissions)

	_, err = manageRouting(ctx, ManageRoutingParams{Operation: "get_time_intervals"})
	require.ErrorContains(t, err, timeIntervalsReadPermissions)

	_, err = manageRoutingWrite(ctx, ManageRoutingWriteParams{Operation: "create_contact_point", Name: "team", Type: "email", Settings: map[string]any{"addresses": "a@example.com"}})
	require.ErrorContains(t, err, contactPointsWritePermissions)

	_, err = createAlertRule(ctx, CreateAlertRuleParams{
		Title: "r", RuleGroup: "g", FolderUID: "f", Condition: "A",
		Data:        []*AlertQuery{{RefID: "A", DatasourceUID: "prom", Model: AlertQueryModel{Expr: "up"}}},
		NoDataState: "OK", ExecErrState: "Error", For: "5m", OrgID: 1,
	})
	require.ErrorContains(t, err, alertRuleCreatePermissions)
}
