package mcpgrafana

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orgsTestServer serves the /api/org and /api/user/orgs endpoints used by
// accessibleOrgIDs. userOrgsStatus controls the /api/user/orgs response.
func orgsTestServer(t *testing.T, userOrgsStatus int, userOrgsBody string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/org":
			_, _ = w.Write([]byte(`{"id":1,"name":"Main Org."}`))
		case "/api/user/orgs":
			if userOrgsStatus != http.StatusOK {
				w.WriteHeader(userOrgsStatus)
				return
			}
			_, _ = w.Write([]byte(userOrgsBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestAccessibleOrgIDs(t *testing.T) {
	logger := slog.Default()

	t.Run("disabled returns only the default org", func(t *testing.T) {
		ts := orgsTestServer(t, http.StatusOK, `[{"orgId":1},{"orgId":2}]`)
		DynamicMultiOrgEnabled = false
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, def := accessibleOrgIDs(ctx, logger)
		assert.Equal(t, []int64{1}, orgs)
		assert.Equal(t, int64(1), def)
	})

	t.Run("enabled returns all the user's orgs including the default", func(t *testing.T) {
		ts := orgsTestServer(t, http.StatusOK, `[{"orgId":1},{"orgId":2}]`)
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, def := accessibleOrgIDs(ctx, logger)
		assert.ElementsMatch(t, []int64{1, 2}, orgs)
		assert.Equal(t, int64(1), def)
	})

	t.Run("enabled falls back to the default org when orgs are not enumerable", func(t *testing.T) {
		// e.g. a service-account token: /api/user/orgs is not available.
		ts := orgsTestServer(t, http.StatusForbidden, "")
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, def := accessibleOrgIDs(ctx, logger)
		assert.Equal(t, []int64{1}, orgs)
		assert.Equal(t, int64(1), def)
	})
}

func TestGetServerClientOrgKeyed(t *testing.T) {
	tm := &ToolManager{serverClients: map[string]*ProxiedClient{}, defaultOrgID: 2}
	want := &ProxiedClient{DatasourceUID: "x", DatasourceType: "tempo", OrgID: 2}
	tm.serverClients[proxiedClientKey(2, "tempo", "x")] = want

	t.Run("explicit org matches", func(t *testing.T) {
		got, err := tm.GetServerClient(2, "tempo", "x")
		require.NoError(t, err)
		assert.Same(t, want, got)
	})

	t.Run("org 0 normalizes to the default org", func(t *testing.T) {
		got, err := tm.GetServerClient(0, "tempo", "x")
		require.NoError(t, err)
		assert.Same(t, want, got)
	})

	t.Run("a different org does not match", func(t *testing.T) {
		_, err := tm.GetServerClient(1, "tempo", "x")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "org 1")
	})
}

func TestAddDatasourceUidParameterOrgID(t *testing.T) {
	base := mcp.Tool{Name: "traceql-search"}

	t.Run("no orgId when dynamic multi-org is off", func(t *testing.T) {
		DynamicMultiOrgEnabled = false
		got := addDatasourceUidParameter(base, "tempo")
		assert.Equal(t, "tempo_traceql-search", got.Name)
		assert.Contains(t, got.InputSchema.Properties, "datasourceUid")
		assert.NotContains(t, got.InputSchema.Properties, OrgIDArgument)
	})

	t.Run("orgId added when dynamic multi-org is on", func(t *testing.T) {
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		got := addDatasourceUidParameter(base, "tempo")
		assert.Contains(t, got.InputSchema.Properties, "datasourceUid")
		assert.Contains(t, got.InputSchema.Properties, OrgIDArgument)
	})
}
