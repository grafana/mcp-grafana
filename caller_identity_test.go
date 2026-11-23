package mcpgrafana

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearSharedCredentialEnv(t *testing.T) {
	t.Helper()
	for _, name := range sharedCredentialEnvVars {
		t.Setenv(name, "")
	}
	t.Setenv(grafanaExtraHeadersEnvVar, "")
}

func TestSharedCredentialsFromEnv(t *testing.T) {
	clearSharedCredentialEnv(t)
	assert.Empty(t, SharedCredentialsFromEnv(nil))

	t.Setenv(grafanaServiceAccountTokenEnvVar, "sa-secret")
	t.Setenv(grafanaUsernameEnvVar, "admin")
	assert.Equal(t, []string{grafanaServiceAccountTokenEnvVar, grafanaUsernameEnvVar}, SharedCredentialsFromEnv(nil))
}

func TestSharedCredentialsFromEnv_ExtraHeaders(t *testing.T) {
	cases := []struct {
		name     string
		extra    string
		identity []string
		want     []string
	}{
		{name: "authorization", extra: `{"Authorization":"Bearer sa-secret"}`, want: []string{"GRAFANA_EXTRA_HEADERS[Authorization]"}},
		{name: "cookie any case", extra: `{"cookie":"grafana_session=sa-secret"}`, want: []string{"GRAFANA_EXTRA_HEADERS[cookie]"}},
		{name: "token header", extra: `{"X-Grafana-Service-Account-Token":"sa-secret"}`, want: []string{"GRAFANA_EXTRA_HEADERS[X-Grafana-Service-Account-Token]"}},
		{name: "identity header", extra: `{"X-Webauth-User":"admin"}`, identity: []string{"X-WEBAUTH-USER"}, want: []string{"GRAFANA_EXTRA_HEADERS[X-Webauth-User]"}},
		{name: "forwarded header", extra: `{"X-Forwarded-User":"svc"}`, want: []string{"GRAFANA_EXTRA_HEADERS[X-Forwarded-User]"}},
		{name: "forwarded header any case", extra: `{"x-api-key":"svc"}`, want: []string{"GRAFANA_EXTRA_HEADERS[x-api-key]"}},
		{name: "non-credential header", extra: `{"X-Scope-OrgID":"tenant"}`},
		{name: "invalid json is ignored", extra: `not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSharedCredentialEnv(t)
			t.Setenv(grafanaForwardHeadersEnvVar, "X-Forwarded-User,X-Api-Key")
			t.Setenv(grafanaExtraHeadersEnvVar, tc.extra)
			got := SharedCredentialsFromEnv(tc.identity)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, strings.Join(got, ","), "sa-secret")
		})
	}
}

func TestSharedCredentialsFromEnv_URLUserinfo(t *testing.T) {
	clearSharedCredentialEnv(t)
	t.Setenv(grafanaURLEnvVar, "https://svc:pw-secret@grafana.example.com")
	got := SharedCredentialsFromEnv(nil)
	assert.Equal(t, []string{"GRAFANA_URL userinfo"}, got)
	assert.NotContains(t, strings.Join(got, ","), "pw-secret")

	t.Setenv(grafanaURLEnvVar, "https://grafana.example.com")
	assert.Empty(t, SharedCredentialsFromEnv(nil))
	t.Setenv(grafanaURLEnvVar, "")
	assert.Empty(t, SharedCredentialsFromEnv(nil))
}

// Proves the premise of the userinfo refusal: net/http sends URL userinfo as
// Authorization: Basic when the request sets no Authorization, via the
// transport this server uses for Grafana calls.
func TestGrafanaClientSendsURLUserinfoAsBasicAuth(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	cfg := &GrafanaConfig{URL: strings.Replace(ts.URL, "http://", "http://svc:pw@", 1)}
	var out map[string]any
	require.NoError(t, grafanaGetJSON(context.Background(), cfg, "/api/user", &out))
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("svc:pw")), got)
}

func TestUnforwardedHeaders(t *testing.T) {
	t.Setenv(grafanaForwardHeadersEnvVar, "Authorization,x-webauth-user")
	assert.Empty(t, UnforwardedHeaders([]string{"X-WEBAUTH-USER"}))
	assert.Equal(t, []string{"X-Remote-User"}, UnforwardedHeaders([]string{"X-Remote-User"}))
}

func TestRequireUserCredentials(t *testing.T) {
	cases := []struct {
		name       string
		forward    string
		identity   []string
		method     string
		header     http.Header
		basicAuth  bool
		wantStatus int
	}{
		{name: "no credential", header: http.Header{}, wantStatus: http.StatusUnauthorized},
		{name: "unlisted header", header: http.Header{"X-Other": {"alice"}}, wantStatus: http.StatusUnauthorized},
		{name: "forwarded traceparent only", forward: "traceparent,tracestate,baggage", header: http.Header{"Traceparent": {"00-abc-def-01"}, "Baggage": {"k=v"}}, wantStatus: http.StatusUnauthorized},
		{name: "forwarded non-auth header", forward: "X-Webauth-User", header: http.Header{"X-Webauth-User": {"alice"}}, wantStatus: http.StatusUnauthorized},
		{name: "bearer not forwarded", header: http.Header{"Authorization": {"Bearer jwt"}}, wantStatus: http.StatusUnauthorized},
		{name: "forwarded authorization", forward: "traceparent,Authorization", header: http.Header{"Authorization": {"Bearer jwt"}}, wantStatus: http.StatusOK},
		{name: "forwarded cookie", forward: "Cookie", header: http.Header{"Cookie": {"grafana_session=x"}}, wantStatus: http.StatusOK},
		{name: "opted-in identity header", forward: "X-Webauth-User", identity: []string{"X-WEBAUTH-USER"}, header: http.Header{"X-Webauth-User": {"alice"}}, wantStatus: http.StatusOK},
		{name: "token header", header: http.Header{grafanaServiceAccountTokenHeader: {"t"}}, wantStatus: http.StatusOK},
		{name: "deprecated api key header", header: http.Header{grafanaAPIKeyHeader: {"t"}}, wantStatus: http.StatusOK},
		{name: "basic auth", header: http.Header{}, basicAuth: true, wantStatus: http.StatusOK},
		{name: "preflight", method: http.MethodOptions, header: http.Header{}, wantStatus: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(grafanaForwardHeadersEnvVar, tc.forward)
			reached := false
			handler := RequireUserCredentials(tc.identity, slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))
			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, "/mcp", nil)
			for k, vs := range tc.header {
				req.Header.Set(k, vs[0])
			}
			if tc.basicAuth {
				req.SetBasicAuth("alice", "pw")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached)
			assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "no Bearer challenge: it would start OAuth discovery")
		})
	}
}

// fakeGrafana serves /api/user: the forwarded X-Webauth-User is the login,
// and "Bearer bad" is rejected with 401. It counts lookups per login.
type fakeGrafana struct {
	mu   sync.Mutex
	hits map[string]int
}

func (f *fakeGrafana) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/user" {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") == "Bearer bad" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	login := r.Header.Get("X-Webauth-User")
	f.mu.Lock()
	f.hits[login]++
	f.mu.Unlock()
	ids := map[string]int64{"alice": 1, "bob": 2}
	_ = json.NewEncoder(w).Encode(map[string]any{"login": login, "id": ids[login], "orgId": 3})
}

func (f *fakeGrafana) hitsFor(login string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[login]
}

type callLogHarness struct {
	grafana *fakeGrafana
	logs    *bytes.Buffer
	handler mcp.MethodHandler
}

func newCallLogHarness(t *testing.T, toolResult *mcp.CallToolResult) *callLogHarness {
	t.Helper()
	g := &fakeGrafana{hits: map[string]int{}}
	ts := httptest.NewServer(g)
	t.Cleanup(ts.Close)
	clearSharedCredentialEnv(t)
	t.Setenv(grafanaURLEnvVar, ts.URL)
	t.Setenv(grafanaForwardHeadersEnvVar, "X-Webauth-User")

	cache := NewClientCache(nil)
	t.Cleanup(cache.Close)
	logs := &bytes.Buffer{}
	tool := func(context.Context, string, mcp.Request) (mcp.Result, error) { return toolResult, nil }
	h := ToolCallLogMiddleware(slog.New(slog.NewJSONHandler(logs, nil)))(tool)
	h = GrafanaContextMiddleware(ComposedHTTPContextFunc(GrafanaConfig{}, cache))(h)
	return &callLogHarness{grafana: g, logs: logs, handler: h}
}

func (h *callLogHarness) call(t *testing.T, header http.Header, args string) {
	t.Helper()
	h.callCtx(t, context.Background(), header, args)
}

func (h *callLogHarness) callCtx(t *testing.T, ctx context.Context, header http.Header, args string) {
	t.Helper()
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "query_prometheus", Arguments: json.RawMessage(args)},
		Extra:  &mcp.RequestExtra{Header: header},
	}
	_, err := h.handler(ctx, "tools/call", req)
	assert.NoError(t, err) // assert, not require: also called from goroutines
}

func (h *callLogHarness) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

func TestToolCallLog_KeyedPerForwardedIdentity(t *testing.T) {
	h := newCallLogHarness(t, &mcp.CallToolResult{})
	for _, user := range []string{"alice", "bob", "alice"} {
		h.call(t, http.Header{"X-Webauth-User": {user}}, `{}`)
	}

	lines := h.lines(t)
	require.Len(t, lines, 3)
	for i, want := range []struct {
		user string
		id   float64
	}{{"alice", 1}, {"bob", 2}, {"alice", 1}} {
		assert.Equal(t, want.user, lines[i]["grafana_user"])
		assert.Equal(t, want.id, lines[i]["user_id"])
		assert.Equal(t, float64(3), lines[i]["org_id"])
		assert.Equal(t, float64(http.StatusOK), lines[i]["identity_status"])
		assert.Equal(t, "ok", lines[i]["result"])
		assert.Equal(t, "query_prometheus", lines[i]["tool"])
	}
	// Same forwarded identity reuses the cached client and its identity.
	assert.Equal(t, 1, h.grafana.hitsFor("alice"))
	assert.Equal(t, 1, h.grafana.hitsFor("bob"))
}

func TestToolCallLog_ConcurrentCallsShareOneLookup(t *testing.T) {
	h := newCallLogHarness(t, &mcp.CallToolResult{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.call(t, http.Header{"X-Webauth-User": {"alice"}}, `{}`)
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, h.grafana.hitsFor("alice"))
}

func TestToolCallLog_UnauthorizedCallIsLogged(t *testing.T) {
	h := newCallLogHarness(t, &mcp.CallToolResult{IsError: true})
	h.call(t, http.Header{grafanaServiceAccountTokenHeader: {"bad"}}, `{}`)

	lines := h.lines(t)
	require.Len(t, lines, 1)
	assert.Equal(t, "", lines[0]["grafana_user"])
	assert.Equal(t, float64(0), lines[0]["user_id"])
	assert.Equal(t, float64(http.StatusUnauthorized), lines[0]["identity_status"])
	assert.Equal(t, "error", lines[0]["result"])
	assert.NotContains(t, h.logs.String(), "bad", "credential must not be logged")
}

func TestToolCallLog_DatasourceUIDWithoutArguments(t *testing.T) {
	h := newCallLogHarness(t, &mcp.CallToolResult{})
	h.call(t, http.Header{"X-Webauth-User": {"alice"}},
		`{"datasourceUid":"prom-1","expr":"up{customer=\"acme-secret\"}"}`)
	h.call(t, http.Header{"X-Webauth-User": {"alice"}}, `{"query":"no uid here"}`)

	lines := h.lines(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "prom-1", lines[0]["datasource_uid"])
	assert.NotContains(t, lines[1], "datasource_uid")
	out := h.logs.String()
	for _, leaked := range []string{"acme-secret", "expr", "no uid here"} {
		assert.NotContains(t, out, leaked)
	}
}

func TestToolCallLog_SkipsOtherMethods(t *testing.T) {
	h := newCallLogHarness(t, &mcp.CallToolResult{})
	_, err := h.handler(context.Background(), "tools/list", &mcp.ListToolsRequest{})
	require.NoError(t, err)
	assert.Empty(t, h.logs.String())
}

func TestToolCallLog_OverrideIsMarked(t *testing.T) {
	h := newCallLogHarness(t, &mcp.CallToolResult{})
	h.call(t, http.Header{"X-Webauth-User": {"alice"}}, `{}`)
	h.callCtx(t, context.WithValue(context.Background(), grafanaOverrideKey{}, "https://other.example.com"),
		http.Header{"X-Webauth-User": {"alice"}}, `{}`)

	lines := h.lines(t)
	require.Len(t, lines, 2)
	assert.NotContains(t, lines[0], "override")
	assert.Equal(t, true, lines[1]["override"])
}

func TestToolCallLog_LookupStartsBeforeTool(t *testing.T) {
	lookedUp := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		lookedUp <- struct{}{}
		_, _ = w.Write([]byte(`{"login":"alice","id":1,"orgId":1}`))
	}))
	t.Cleanup(ts.Close)
	clearSharedCredentialEnv(t)
	t.Setenv(grafanaURLEnvVar, ts.URL)
	cache := NewClientCache(nil)
	t.Cleanup(cache.Close)

	var toolSawLookup bool
	tool := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		select {
		case <-lookedUp: // only reachable if the lookup began before the tool ran
			toolSawLookup = true
		case <-time.After(5 * time.Second):
		}
		return &mcp.CallToolResult{}, nil
	}
	logs := &bytes.Buffer{}
	h := ToolCallLogMiddleware(slog.New(slog.NewJSONHandler(logs, nil)))(tool)
	h = GrafanaContextMiddleware(ComposedHTTPContextFunc(GrafanaConfig{}, cache))(h)
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "t", Arguments: json.RawMessage(`{}`)},
		Extra:  &mcp.RequestExtra{Header: http.Header{}},
	}
	_, err := h(context.Background(), "tools/call", req)
	require.NoError(t, err)
	assert.True(t, toolSawLookup)
	assert.Contains(t, logs.String(), `"grafana_user":"alice"`)
}

func TestIdentityLookupWaitRespectsCancellation(t *testing.T) {
	l := identityLookup{done: make(chan identityResult, 1)} // never completes
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, identityResult{}, l.wait(ctx))

	l.done <- identityResult{identity: callerIdentity{Login: "alice"}, status: 200}
	assert.Equal(t, "alice", l.wait(context.Background()).identity.Login)
}
