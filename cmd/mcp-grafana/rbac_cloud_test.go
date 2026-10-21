//go:build cloud

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// TestToolPermissions checks every tool's declared RBAC permissions against
// the Grafana Cloud stack at GRAFANA_URL. For each distinct permission set it
// creates a custom role holding exactly those actions and a service account
// with basic role None holding only that role. Each tool is then called three
// times:
//
//   - with the admin token, as a baseline: a tool that fails here has an
//     environment or fixture problem, not a permission one, and is skipped;
//   - with its declared permissions, which must succeed;
//   - with no permissions, which must fail unless the tool declares none
//     (or, for a tool that returns only what the caller may see, is skipped).
//
// Actions of the form group/resource:verb are left out of the roles: Grafana
// checks them only for on-behalf-of tokens and rejects them in custom roles.
//
// Arguments come from rbacFixtures. The token in GRAFANA_SERVICE_ACCOUNT_TOKEN
// must be able to create roles and service accounts.
func TestToolPermissions(t *testing.T) {
	grafanaURL := strings.TrimSuffix(os.Getenv("GRAFANA_URL"), "/")
	adminToken := os.Getenv("GRAFANA_SERVICE_ACCOUNT_TOKEN")
	if grafanaURL == "" || adminToken == "" {
		t.Skip("GRAFANA_URL and GRAFANA_SERVICE_ACCOUNT_TOKEN must be set")
	}
	// Every session passes its token in a header; clearing the environment
	// keeps a session that lost it from silently running as admin.
	t.Setenv("GRAFANA_URL", grafanaURL)
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")
	t.Setenv("GRAFANA_API_KEY", "")

	admin := &rbacAdmin{url: grafanaURL, token: adminToken}
	admin.loadPermissions(t)
	admin.skipUnlessPermitted(t, "roles:write", "roles:delete", "serviceaccounts:create", "serviceaccounts:delete", "users.roles:add")
	require.NoError(t, admin.api("POST", "/api/dashboards/db", map[string]any{
		"overwrite": true,
		"dashboard": map[string]any{
			"uid":   rbacDashboardUID,
			"title": "MCP RBAC test (managed by TestToolPermissions)",
			"panels": []any{map[string]any{
				"id": rbacPanelID, "type": "timeseries", "title": "vector(1)",
				"gridPos":    map[string]any{"x": 0, "y": 0, "w": 12, "h": 8},
				"datasource": map[string]any{"type": "prometheus", "uid": rbacPrometheusUID},
				"targets":    []any{map[string]any{"refId": "A", "expr": "vector(1)", "datasource": map[string]any{"type": "prometheus", "uid": rbacPrometheusUID}}},
			}},
		},
	}, nil))

	tools := listAllCategoryTools(t, disabledTools{})
	adminSession := newRBACSession(t, adminToken)
	noneToken, err := admin.serviceAccountToken(t, nil)
	require.NoError(t, err)
	noneSession := newRBACSession(t, noneToken)
	granted := map[string]*rbacSession{}

	parent := t
	for _, name := range slices.Sorted(maps.Keys(tools)) {
		t.Run(name, func(t *testing.T) {
			f := rbacFixtures[name]
			if f.skip != "" {
				t.Skip(f.skip)
			}
			call := func(s *rbacSession) (string, []string) {
				args := maps.Clone(f.args)
				if args == nil {
					args = map[string]any{}
				}
				if f.setup != nil {
					maps.Copy(args, f.setup(t, admin.api))
				}
				return s.call(t, name, args)
			}

			if failure, _ := call(adminSession); failure != "" {
				t.Skipf("fails with the admin token, so this stack can't check it: %s", failure)
			}

			declared, _ := mcpgrafana.ToolRequiredPermissions(tools[name])
			perms := slices.DeleteFunc(declared, func(a string) bool { return strings.Contains(a, "/") })
			key := strings.Join(perms, ",")
			s, ok := granted[key]
			if !ok {
				// Sessions outlive the subtest that creates them, so they and
				// their service accounts and roles are cleaned up with the
				// parent.
				token, err := admin.serviceAccountToken(parent, perms)
				require.NoError(t, err)
				s = newRBACSession(parent, token)
				granted[key] = s
			}
			if failure, denials := call(s); failure != "" {
				t.Errorf("fails with exactly its declared permissions %v: %s\nRBAC denials: %v", perms, failure, denials)
			}

			if f.filtered {
				return
			}
			failure, denials := call(noneSession)
			switch {
			case len(perms) == 0 && failure != "":
				t.Errorf("declares no permissions but fails without any: %s\nRBAC denials: %v", failure, denials)
			case len(perms) > 0 && failure == "":
				t.Errorf("declares %v but succeeds without any permissions", perms)
			case len(perms) > 0 && len(denials) == 0:
				t.Logf("fails without permissions, but no Grafana RBAC denial was recorded: %s", failure)
			}
		})
	}
}

// rbacAdmin provisions roles and service accounts with the admin token.
type rbacAdmin struct {
	url, token string
	// held maps each action the admin token holds to its scopes.
	held map[string][]string
}

func (a *rbacAdmin) api(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.url+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (a *rbacAdmin) loadPermissions(t *testing.T) {
	t.Helper()
	require.NoError(t, a.api("GET", "/api/access-control/user/permissions", nil, &a.held))
}

func (a *rbacAdmin) skipUnlessPermitted(t *testing.T, actions ...string) {
	t.Helper()
	for _, action := range actions {
		if _, ok := a.held[action]; !ok {
			t.Skipf("the GRAFANA_SERVICE_ACCOUNT_TOKEN token lacks %s, needed to provision test roles", action)
		}
	}
}

// serviceAccountToken creates a service account with basic role None and, if
// perms is non-empty, a custom role granting exactly those actions, and
// returns a token for it. Both are deleted when t ends.
//
// Each action is granted on the scopes the admin token holds it on: Grafana
// refuses to create a role granting more than its creator holds, so that is
// the broadest grant available. Scopes naming a single resource (such as the
// dashboard this test creates) are left out; Grafana rejects some of them in
// roles, and the wildcard scopes beside them already cover them.
func (a *rbacAdmin) serviceAccountToken(t *testing.T, perms []string) (string, error) {
	name := rbacUnique("mcp-rbac-test")

	var permissions []map[string]string
	for _, p := range perms {
		scopes, ok := a.held[p]
		if !ok {
			return "", fmt.Errorf("the admin token lacks the declared permission %s, so it can't grant it", p)
		}
		for _, scope := range slices.Compact(slices.Sorted(slices.Values(scopes))) {
			singleResource := (strings.Contains(scope, ":uid:") || strings.Contains(scope, ":id:")) && !strings.HasSuffix(scope, "*")
			if !singleResource {
				permissions = append(permissions, map[string]string{"action": p, "scope": scope})
			}
		}
	}

	var sa struct {
		ID int64 `json:"id"`
	}
	if err := a.api("POST", "/api/serviceaccounts", map[string]any{"name": name, "role": "None"}, &sa); err != nil {
		return "", err
	}
	t.Cleanup(func() { rbacDeleteQuietly(t, a.api, fmt.Sprintf("/api/serviceaccounts/%d", sa.ID)) })

	if len(permissions) > 0 {
		var role struct {
			UID string `json:"uid"`
		}
		// An unknown action fails here ("was not found in the list of valid
		// actions"), which usually means a typo in a declaration.
		if err := a.api("POST", "/api/access-control/roles", map[string]any{
			"name": "custom:" + name, "displayName": name, "version": 1, "permissions": permissions,
		}, &role); err != nil {
			return "", fmt.Errorf("creating a role with the declared permissions %v: %w", perms, err)
		}
		t.Cleanup(func() { rbacDeleteQuietly(t, a.api, "/api/access-control/roles/"+role.UID+"?force=true") })
		if err := a.api("POST", fmt.Sprintf("/api/access-control/users/%d/roles", sa.ID), map[string]any{"roleUid": role.UID}, nil); err != nil {
			return "", err
		}
	}

	var token struct {
		Key string `json:"key"`
	}
	if err := a.api("POST", fmt.Sprintf("/api/serviceaccounts/%d/tokens", sa.ID), map[string]any{"name": name}, &token); err != nil {
		return "", err
	}
	return token.Key, nil
}

// rbacSession is an in-process MCP session whose tool calls reach Grafana
// with one token, recording the RBAC denials they get back.
type rbacSession struct {
	session  *mcp.ClientSession
	recorder *denialRecorder
}

func newRBACSession(t *testing.T, token string) *rbacSession {
	t.Helper()
	recorder := &denialRecorder{base: http.DefaultTransport}
	httpFn := mcpgrafana.ComposedHTTPContextFunc(mcpgrafana.GrafanaConfig{BaseTransport: recorder})
	header := http.Header{}
	header.Set("X-Grafana-Service-Account-Token", token)

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	dt := disabledTools{}
	for _, e := range dt.toolEntries() {
		dt.enabledTools += e.category + ","
	}
	dt.processTools(srv)
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = httpFn(ctx, (&http.Request{Header: header}).WithContext(ctx))
			return next(ctx, method, req)
		}
	})
	return &rbacSession{session: connectTestClient(t, srv), recorder: recorder}
}

// call calls a tool and returns why it failed ("" if it succeeded) and the
// RBAC denials Grafana sent during the call.
func (s *rbacSession) call(t *testing.T, name string, args map[string]any) (string, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s.recorder.take()
	res, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	denials := s.recorder.take()
	switch {
	case err != nil:
		return err.Error(), denials
	case res.IsError:
		var text []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text = append(text, tc.Text)
			}
		}
		msg := strings.Join(text, "\n")
		if msg == "" {
			msg = "tool returned an error with no text"
		}
		return truncate(msg, 500), denials
	}
	return "", denials
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// denialRecorder is an http.RoundTripper that records Grafana's RBAC
// denials. A 403 alone isn't one: a datasource backend can return 403 for its
// own reasons (mcptests' Prometheus does, to every caller), so it matches the
// bodies Grafana sends when access control refuses a request.
type denialRecorder struct {
	base    http.RoundTripper
	mu      sync.Mutex
	denials []string
}

func (r *denialRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if d := rbacDenial(body); d != "" {
		r.mu.Lock()
		r.denials = append(r.denials, req.Method+" "+req.URL.Path+": "+d)
		r.mu.Unlock()
	}
	return resp, nil
}

func (r *denialRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.denials
	r.denials = nil
	return d
}

// rbacDenial returns the message of a Grafana RBAC denial body: the legacy
// API's (which names the missing action) or an /apis Status with code 403. It
// returns "" for any other body.
func rbacDenial(body []byte) string {
	var e struct {
		AccessErrorID string `json:"accessErrorId"`
		Kind          string `json:"kind"`
		Code          int    `json:"code"`
		Message       string `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	switch {
	case e.AccessErrorID != "":
		return e.Message
	case e.Kind == "Status" && e.Code == http.StatusForbidden:
		if e.Message == "" {
			return "forbidden"
		}
		return e.Message
	}
	return ""
}
