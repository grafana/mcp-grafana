package mcpgrafana

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orgsTestServer serves the /api/org, /api/user and /api/user/orgs endpoints used
// by accessibleOrgIDs. userOrgsStatus controls the /api/user/orgs response.
func orgsTestServer(t *testing.T, userOrgsStatus int, userOrgsBody string) *httptest.Server {
	return orgsTestServerWithOrg(t, `{"id":1,"name":"Main Org."}`, http.StatusOK, userOrgsStatus, userOrgsBody)
}

// orgsTestServerWithOrg additionally controls /api/org (orgBody, or a 404 when
// empty) and /api/user (userStatus), so the degraded paths can be exercised.
func orgsTestServerWithOrg(t *testing.T, orgBody string, userStatus, userOrgsStatus int, userOrgsBody string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/org":
			if orgBody == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(orgBody))
		case "/api/user":
			if userStatus != http.StatusOK {
				w.WriteHeader(userStatus)
				return
			}
			_, _ = w.Write([]byte(`{"orgId":3}`))
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

	t.Run("disabled returns only the connection org", func(t *testing.T) {
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

	t.Run("enabled falls back to the connection org when orgs are not enumerable", func(t *testing.T) {
		// e.g. a service-account token: /api/user/orgs is not available.
		ts := orgsTestServer(t, http.StatusForbidden, "")
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, def := accessibleOrgIDs(ctx, logger)
		assert.Equal(t, []int64{1}, orgs)
		assert.Equal(t, int64(1), def)
	})

	t.Run("enabled includes the connection org when the user list omits it", func(t *testing.T) {
		// /api/org reports org 1 but membership only lists org 2: the default must
		// still be discovered, or a call omitting orgId would find no clients.
		ts := orgsTestServer(t, http.StatusOK, `[{"orgId":2}]`)
		DynamicMultiOrgEnabled = true
		t.Cleanup(func() { DynamicMultiOrgEnabled = false })
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, def := accessibleOrgIDs(ctx, logger)
		assert.ElementsMatch(t, []int64{1, 2}, orgs)
		assert.Equal(t, int64(1), def)
	})
}

// A zero connection org must never reach the discovery list: 0 scopes no request,
// so it would rediscover the identity's own org under a placeholder key, doubling
// the clients for those datasources, and would leave connectionOrgID at 0, which
// no discovered client is keyed by.
func TestAccessibleOrgIDsZeroConnectionOrg(t *testing.T) {
	logger := slog.Default()
	DynamicMultiOrgEnabled = true
	t.Cleanup(func() { DynamicMultiOrgEnabled = false })

	t.Run("falls back to the identity's own org when /api/org is unavailable", func(t *testing.T) {
		// /api/org 404s and nothing is configured, but /api/user reports org 3.
		ts := orgsTestServerWithOrg(t, "", http.StatusOK, http.StatusOK, `[{"orgId":1},{"orgId":2}]`)
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, connectionOrg := accessibleOrgIDs(ctx, logger)
		assert.Equal(t, int64(3), connectionOrg, "a call omitting orgId must target a real org")
		assert.ElementsMatch(t, []int64{1, 2, 3}, orgs)
		assert.NotContains(t, orgs, int64(0), "0 must never be discovered")
	})

	t.Run("no placeholder org when neither endpoint can resolve one", func(t *testing.T) {
		ts := orgsTestServerWithOrg(t, "", http.StatusForbidden, http.StatusOK, `[{"orgId":1},{"orgId":2}]`)
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL})
		orgs, connectionOrg := accessibleOrgIDs(ctx, logger)
		assert.Equal(t, int64(0), connectionOrg)
		assert.ElementsMatch(t, []int64{1, 2}, orgs, "membership is still discovered, without a 0 entry")
		assert.NotContains(t, orgs, int64(0))
	})

	t.Run("a configured org is used when /api/org is unavailable", func(t *testing.T) {
		ts := orgsTestServerWithOrg(t, "", http.StatusOK, http.StatusOK, `[{"orgId":1},{"orgId":2}]`)
		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{URL: ts.URL, OrgID: 7})
		orgs, connectionOrg := accessibleOrgIDs(ctx, logger)
		assert.Equal(t, int64(7), connectionOrg, "GRAFANA_ORG_ID wins over the persisted org")
		assert.ElementsMatch(t, []int64{1, 2, 7}, orgs)
	})
}

func TestGetServerClientOrgKeyed(t *testing.T) {
	tm := &ToolManager{serverClients: map[string]*ProxiedClient{}, connectionOrgID: 2}
	want := &ProxiedClient{DatasourceUID: "x", DatasourceType: "tempo", OrgID: 2}
	tm.serverClients[proxiedClientKey(2, "tempo", "x")] = want

	t.Run("explicit org matches", func(t *testing.T) {
		got, err := tm.GetServerClient(2, "tempo", "x")
		require.NoError(t, err)
		assert.Same(t, want, got)
	})

	t.Run("org 0 normalizes to the connection org", func(t *testing.T) {
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

// The shared, credential-keyed proxiedToolSet holds clients for every org the
// credential can reach, so the same lookup rules as the stdio path must hold on
// the set: exact org match, 0 normalizing to the set's connection org, and a
// non-matching org failing rather than silently returning another org's client.
func TestAcquireProxiedClientForCallOrgKeyed(t *testing.T) {
	org2 := &ProxiedClient{DatasourceUID: "x", DatasourceType: "tempo", OrgID: 2}
	org3 := &ProxiedClient{DatasourceUID: "x", DatasourceType: "tempo", OrgID: 3}

	newSet := func() *proxiedToolSet {
		return &proxiedToolSet{
			clients: map[string]*ProxiedClient{
				proxiedClientKey(2, "tempo", "x"): org2,
				proxiedClientKey(3, "tempo", "x"): org3,
			},
			connectionOrgID: 2,
			built:           true,
			// One live session reference, so releasing an in-flight call does not
			// tear the set down mid-test.
			refs: 1,
		}
	}

	t.Run("same UID in two orgs resolves to distinct clients", func(t *testing.T) {
		tm, _ := newTestToolManager(t, time.Hour, nil)
		set := newSet()

		got2, release2, err := tm.acquireProxiedClientForCall(set, 2, "tempo", "x")
		require.NoError(t, err)
		defer release2()
		assert.Same(t, org2, got2)

		got3, release3, err := tm.acquireProxiedClientForCall(set, 3, "tempo", "x")
		require.NoError(t, err)
		defer release3()
		assert.Same(t, org3, got3)
	})

	t.Run("org 0 normalizes to the set's connection org", func(t *testing.T) {
		tm, _ := newTestToolManager(t, time.Hour, nil)
		got, release, err := tm.acquireProxiedClientForCall(newSet(), 0, "tempo", "x")
		require.NoError(t, err)
		defer release()
		assert.Same(t, org2, got)
	})

	t.Run("an org with no such datasource errors and names the org", func(t *testing.T) {
		tm, _ := newTestToolManager(t, time.Hour, nil)
		_, release, err := tm.acquireProxiedClientForCall(newSet(), 4, "tempo", "x")
		require.Error(t, err)
		assert.Nil(t, release, "release must be nil on error")
		assert.Contains(t, err.Error(), "org 4")
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
