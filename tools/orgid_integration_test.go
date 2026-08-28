// Exercises the per-call orgId parameter end-to-end through
// OrgIDOverrideMiddleware against two Grafana instances from docker-compose:
//
//   - localhost:3000 (Grafana 13): serves the dashboard.grafana.app v1beta1 API,
//     so get_dashboard_by_uid uses the namespaced /apis/* path. The same
//     namespaced routing applies to list_provisioning_repositories. Here orgId
//     selects the org by changing the resolved namespace (default vs org-2).
//   - localhost:3002 (Grafana 11): no v1beta1 API, so get_dashboard_by_uid falls
//     back to the legacy /api/* REST path. Here orgId selects the org via the
//     X-Grafana-Org-Id header.
//
// The secondary org and its dashboard are provisioned by the orgs-seed
// docker-compose job (testdata/orgs-seed.sh). On a fresh stack the seeded org is
// always id 2 on both instances, so the tests assume orgIDTestOrg below. Run
// with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	modernGrafanaURL = "http://localhost:3000"
	legacyGrafanaURL = "http://localhost:3002"
	// orgIDTestOrg is the id of the secondary org seeded by orgs-seed. On a fresh
	// docker-compose stack the first created org is always id 2 on both
	// instances. These also match testdata/orgs-seed.sh.
	orgIDTestOrg         = int64(2)
	orgIDTestNSDashUID   = "mcp-orgid-ns"
	orgIDTestNSDashTitle = "OrgID NS Dashboard"
	orgIDTestLegacyUID   = "mcp-orgid-legacy"
	orgIDTestLegacyTitle = "OrgID Legacy Dashboard"
)

// newOrgRoutingContext builds a production-faithful context for baseURL: the
// Grafana and Kubernetes clients are constructed the same way the server builds
// them per request (via NewGrafanaClient/NewKubernetesClient), so their
// transports carry the OrgID round-tripper that reads the per-call OrgID from
// the context — which is what makes the orgId override actually route.
func newOrgRoutingContext(t *testing.T, baseURL string) context.Context {
	t.Helper()
	auth := url.UserPassword("admin", "admin")
	cfg := mcpgrafana.GrafanaConfig{URL: baseURL, BasicAuth: auth}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

	gc := mcpgrafana.NewGrafanaClient(ctx, baseURL, "", auth)
	ctx = mcpgrafana.WithGrafanaClient(ctx, gc)

	k8s, err := mcpgrafana.NewKubernetesClient(ctx)
	require.NoError(t, err)
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8s)
	return ctx
}

// callToolWithOrgID invokes the given tool's handler through the real
// OrgIDOverrideMiddleware, merging an orgId argument (omitted when orgID <= 0,
// so the connection's default org is used) into args.
func callToolWithOrgID(ctx context.Context, tool mcpgrafana.Tool, args map[string]any, orgID int64) (*mcp.CallToolResult, error) {
	handler := mcpgrafana.OrgIDOverrideMiddleware(tool.Handler)
	merged := map[string]any{}
	for k, v := range args {
		merged[k] = v
	}
	if orgID > 0 {
		merged["orgId"] = orgID
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool.Tool.Name
	req.Params.Arguments = merged
	return handler(ctx, req)
}

// TestOrgIDParameter_OptIn_Integration verifies the --dynamic-multi-org opt-in
// end to end. It mirrors newServer's wiring: the OrgIDOverrideMiddleware is only
// applied when mcpgrafana.DynamicMultiOrgEnabled is set. With the feature off, an
// orgId argument is just an unknown argument the handler ignores, so the call
// stays on the connection's default org; with it on, orgId routes the call. The
// seeded dashboard exists only in the secondary org, so "found vs not found" is
// the signal.
func TestOrgIDParameter_OptIn_Integration(t *testing.T) {
	ctx := newOrgRoutingContext(t, modernGrafanaURL)

	// callDashboard mirrors the server: wrap with the override middleware only
	// when the feature is enabled.
	callDashboard := func(orgID int64) (*mcp.CallToolResult, error) {
		handler := GetDashboardByUID.Handler
		if mcpgrafana.DynamicMultiOrgEnabled {
			handler = mcpgrafana.OrgIDOverrideMiddleware(handler)
		}
		req := mcp.CallToolRequest{}
		req.Params.Name = GetDashboardByUID.Tool.Name
		req.Params.Arguments = map[string]any{"uid": orgIDTestNSDashUID, "orgId": orgID}
		return handler(ctx, req)
	}

	t.Run("disabled: orgId is ignored, call stays on the default org", func(t *testing.T) {
		mcpgrafana.DynamicMultiOrgEnabled = false
		res, err := callDashboard(orgIDTestOrg)
		require.NoError(t, err)
		assert.Truef(t, res.IsError, "with --dynamic-multi-org off, orgId must be ignored; the dashboard exists only in org %d", orgIDTestOrg)
	})

	t.Run("enabled: orgId routes the call to the target org", func(t *testing.T) {
		mcpgrafana.DynamicMultiOrgEnabled = true
		t.Cleanup(func() { mcpgrafana.DynamicMultiOrgEnabled = false })
		res, err := callDashboard(orgIDTestOrg)
		require.NoError(t, err)
		require.Falsef(t, res.IsError, "with --dynamic-multi-org on, orgId should route to org %d: %s", orgIDTestOrg, textOrEmpty(res))
		assert.Contains(t, resultText(t, res), orgIDTestNSDashTitle)
	})
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.Truef(t, ok, "expected TextContent, got %T", res.Content[0])
	return tc.Text
}

// requireDashboardInOrg asserts that get_dashboard_by_uid, scoped to orgID,
// finds the dashboard with the expected title. It retries briefly to absorb
// read-after-write lag in the unified store.
func requireDashboardInOrg(t *testing.T, ctx context.Context, uid, title string, orgID int64) {
	t.Helper()
	var last string
	for i := 0; i < 10; i++ {
		res, err := callToolWithOrgID(ctx, GetDashboardByUID, map[string]any{"uid": uid}, orgID)
		require.NoError(t, err)
		if !res.IsError {
			require.Contains(t, resultText(t, res), title)
			return
		}
		last = resultText(t, res)
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("dashboard %q not found in org %d after retries; last error: %s", uid, orgID, last)
}

// requireDashboardNotInOrg asserts that get_dashboard_by_uid, scoped to orgID,
// does NOT find the dashboard — proving the orgId routed to a different org.
func requireDashboardNotInOrg(t *testing.T, ctx context.Context, uid string, orgID int64) {
	t.Helper()
	res, err := callToolWithOrgID(ctx, GetDashboardByUID, map[string]any{"uid": uid}, orgID)
	require.NoError(t, err)
	assert.Truef(t, res.IsError, "expected dashboard %q to be absent from org %d, but it was found", uid, orgID)
}

// TestOrgIDParameter_NamespacedAPI_Integration verifies that on the modern
// instance — where get_dashboard_by_uid uses the namespaced dashboard.grafana.app
// API — the orgId argument selects the org by changing the resolved namespace.
func TestOrgIDParameter_NamespacedAPI_Integration(t *testing.T) {
	ctx := newOrgRoutingContext(t, modernGrafanaURL)

	// orgId routes to the org-2 namespace where the seeded dashboard lives.
	requireDashboardInOrg(t, ctx, orgIDTestNSDashUID, orgIDTestNSDashTitle, orgIDTestOrg)
	// Without the override, the request targets the default namespace, where the
	// dashboard does not exist.
	requireDashboardNotInOrg(t, ctx, orgIDTestNSDashUID, 0)
}

// TestOrgIDParameter_LegacyAPI_Integration verifies that on the legacy instance
// — where get_dashboard_by_uid falls back to the legacy REST API — the orgId
// argument selects the org via the X-Grafana-Org-Id header.
func TestOrgIDParameter_LegacyAPI_Integration(t *testing.T) {
	ctx := newOrgRoutingContext(t, legacyGrafanaURL)

	// orgId sets X-Grafana-Org-Id so the legacy read hits the right org.
	requireDashboardInOrg(t, ctx, orgIDTestLegacyUID, orgIDTestLegacyTitle, orgIDTestOrg)
	// Without the override, the legacy read targets the default org, which lacks
	// the dashboard.
	requireDashboardNotInOrg(t, ctx, orgIDTestLegacyUID, 0)
}

// TestOrgIDParameter_Provisioning_Integration verifies that orgId routes the
// provisioning tools' namespaced /apis/* calls to the right org namespace. The
// seeded test-repo lives in the default org's namespace; the secondary org's
// namespace has none, so orgId=2 must not see it.
func TestOrgIDParameter_Provisioning_Integration(t *testing.T) {
	ctx := newOrgRoutingContext(t, modernGrafanaURL)

	// Default org: the seeded repository is listed.
	def, err := callToolWithOrgID(ctx, ListProvisioningRepositories, nil, 0)
	require.NoError(t, err)
	require.False(t, def.IsError, "list repositories failed: %s", textOrEmpty(def))
	assert.Contains(t, resultText(t, def), testProvisioningRepo)

	// org-2 namespace: the default org's repository is not visible there.
	secondary, err := callToolWithOrgID(ctx, ListProvisioningRepositories, nil, orgIDTestOrg)
	require.NoError(t, err)
	require.False(t, secondary.IsError, "list repositories (org 2) failed: %s", textOrEmpty(secondary))
	assert.NotContains(t, resultText(t, secondary), testProvisioningRepo,
		"default-org repository must not be visible in the secondary org's namespace")
}

// renderDashboardImage renders a stored dashboard via get_panel_image scoped to
// orgID and returns the decoded PNG bytes, asserting a successful PNG result.
func renderDashboardImage(t *testing.T, ctx context.Context, uid string, orgID int64) []byte {
	t.Helper()
	res, err := callToolWithOrgID(ctx, GetPanelImage, map[string]any{"dashboardUid": uid}, orgID)
	require.NoError(t, err)
	require.Falsef(t, res.IsError, "render failed: %s", textOrEmpty(res))
	require.NotEmpty(t, res.Content)
	img, ok := res.Content[0].(mcp.ImageContent)
	require.Truef(t, ok, "expected ImageContent, got %T", res.Content[0])
	assert.Equal(t, "image/png", img.MIMEType)
	data, err := base64.StdEncoding.DecodeString(img.Data)
	require.NoError(t, err)
	require.NotEmpty(t, data, "rendered PNG should not be empty")
	require.Equal(t, []byte{0x89, 'P', 'N', 'G'}, data[:4], "rendered image should be a PNG")
	return data
}

// activeOrgID returns the admin user's persisted active org on the instance.
func activeOrgID(t *testing.T, baseURL string) int64 {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/org", nil)
	require.NoError(t, err)
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET /api/org: %s", body)
	var org struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &org))
	return org.ID
}

// TestOrgIDParameter_RenderImage_Integration verifies that orgId routes the
// get_panel_image render to the right org. Grafana's renderer selects the org
// from the targetOrgId URL query param (not the X-Grafana-Org-Id header), so
// this exercises the buildRenderURL wiring end-to-end. The seeded dashboard
// exists only in the secondary org, so rendering it under orgId=2 (the
// dashboard) and orgId=1 (a "not found" page) must produce different images.
// Both orgs are passed explicitly so the result does not depend on the admin
// user's persisted active org.
//
// It also asserts the render does NOT change the user's active org: the naive
// orgId query param would make the renderer's frontend persist an org switch via
// /api/user/using, silently re-targeting every later non-orgId call — using
// targetOrgId avoids that, and this guards against a regression to orgId.
func TestOrgIDParameter_RenderImage_Integration(t *testing.T) {
	ctx := newOrgRoutingContext(t, modernGrafanaURL)

	orgBefore := activeOrgID(t, modernGrafanaURL)

	inOrg2 := renderDashboardImage(t, ctx, orgIDTestNSDashUID, orgIDTestOrg)
	inOrg1 := renderDashboardImage(t, ctx, orgIDTestNSDashUID, 1)

	assert.NotEqual(t, inOrg2, inOrg1,
		"orgId must change which org the dashboard is rendered from; org 2 has the dashboard, the default org does not")

	assert.Equalf(t, orgBefore, activeOrgID(t, modernGrafanaURL),
		"rendering with an explicit orgId must not change the user's persisted active org (got switched from %d)", orgBefore)
}

func textOrEmpty(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
