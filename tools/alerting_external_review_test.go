//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNonCreateRuleUIDCompatibility(t *testing.T) {
	uid := "prom"
	for _, operation := range []string{"list", "get", "versions", "update", "delete"} {
		for _, managedBy := range []string{"", "grafana"} {
			args := ManageRulesReadWriteParams{Operation: operation, ManagedBy: managedBy, DatasourceUID: &uid}
			require.NoError(t, args.validateCreationTarget(), "%s/%s", operation, managedBy)
			assert.Equal(t, "prom", *args.DatasourceUID, "list must retain its selector")
		}
		args := ManageRulesReadWriteParams{Operation: operation, ManagedBy: "datasource", DatasourceUID: &uid}
		require.ErrorContains(t, args.validateCreationTarget(), "supports only create")
	}
	require.ErrorContains(t, (ManageRulesReadWriteParams{Operation: "create", DatasourceUID: &uid}).validateCreationTarget(), "requires explicit")
}

func TestExplicitRulerWithoutPrometheusType(t *testing.T) {
	for _, tc := range []struct {
		name, body, subtype, wantErr string
		status                       int
	}{
		{"mimir", `{"status":"success","data":{"features":{"ruler_config_api":"true"}}}`, "mimir", "", 200},
		{"cortex", "", "cortex", "", 404},
		{"prometheus", `{"status":"success","data":{"version":"3.0"}}`, "", "does not advertise", 200},
		{"disabled ruler", `{"status":"success","data":{"features":{"ruler_config_api":"false"}}}`, "", "does not advertise", 200},
		{"forbidden", "secret", "", "HTTP 403", 403},
		{"malformed", `{}`, "", "invalid datasource buildinfo", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts, probes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/datasources/uid/mimir":
					_, _ = fmt.Fprint(w, `{"uid":"mimir","type":"prometheus","jsonData":{}}`)
				case "/api/datasources/proxy/uid/mimir/api/v1/status/buildinfo":
					assert.Equal(t, http.MethodGet, r.Method)
					probes.Add(1)
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, tc.body)
				case "/api/ruler/mimir/api/v1/rules/team/group":
					assert.Equal(t, http.MethodGet, r.Method)
					assert.Equal(t, tc.subtype, r.URL.Query().Get("subtype"))
					_, _ = fmt.Fprint(w, `{"name":"group","rules":[]}`)
				case "/api/ruler/mimir/api/v1/rules/team":
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, tc.subtype, r.URL.Query().Get("subtype"))
					posts.Add(1)
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			args := externalRuleArgs()
			uid := "mimir"
			args.DatasourceUID = &uid
			_, err := manageRulesReadWrite(mockDatasourcesCtx(server), args)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.NotContains(t, err.Error(), "secret")
				assert.Zero(t, posts.Load())
			} else {
				require.NoError(t, err)
				assert.EqualValues(t, 1, posts.Load())
			}
			assert.EqualValues(t, 1, probes.Load())
		})
	}
}

func TestExistingRulerDefaultInterval(t *testing.T) {
	for _, tc := range []struct{ name, interval, requested, wantErr string }{
		{"preserve default", "", "", ""},
		{"cannot infer default", "", "1m", "omit evaluation_interval"},
		{"same interval", `,"interval":"30s"`, "0m30s", ""},
		{"different interval", `,"interval":"30s"`, "1m", "must match"},
		{"invalid interval", `,"interval":123`, "1m", "cannot verify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/datasources" {
					_, _ = fmt.Fprintf(w, "[%s]", testMimirDatasource)
				} else if r.Method == http.MethodGet {
					_, _ = fmt.Fprintf(w, `{"name":"group","rules":[]%s}`, tc.interval)
				} else {
					var posted externalAlertingObject
					require.NoError(t, json.NewDecoder(r.Body).Decode(&posted))
					if tc.interval == "" {
						assert.NotContains(t, posted, "interval")
					} else {
						assert.JSONEq(t, `"30s"`, string(posted["interval"]))
					}
					posts.Add(1)
					w.WriteHeader(http.StatusAccepted)
				}
			}))
			defer server.Close()
			args := externalRuleArgs()
			args.EvaluationInterval = tc.requested
			_, err := manageRulesReadWrite(mockDatasourcesCtx(server), args)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Zero(t, posts.Load())
			} else {
				require.NoError(t, err)
				assert.EqualValues(t, 1, posts.Load())
			}
		})
	}
}

func TestExternalWriteIsolationAndCleanup(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	baseCtx := mockDatasourcesCtx(server)
	client, err := newAlertingClientFromContext(baseCtx)
	require.NoError(t, err)
	key := client.externalWriteKey(baseCtx, "mimir", "team", "group", false)
	unlock, err := lockExternalAlertingWrite(t.Context(), key)
	require.NoError(t, err)
	t.Cleanup(unlock)
	otherInstance := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: server.URL, OverrideURL: "http://another-stack"})
	for _, other := range []externalAlertingWriteKey{
		client.externalWriteKey(baseCtx, "another", "team", "group", false),
		client.externalWriteKey(baseCtx, "mimir", "another", "group", false),
		client.externalWriteKey(baseCtx, "mimir", "team", "another", false),
		client.externalWriteKey(baseCtx, "mimir", "", "", true),
		client.externalWriteKey(otherInstance, "mimir", "team", "group", false),
	} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		release, err := lockExternalAlertingWrite(ctx, other)
		cancel()
		require.NoError(t, err, "independent destination must not wait for the held lock")
		release()
		externalAlertingWrites.Lock()
		_, retained := externalAlertingWrites.gates[other]
		externalAlertingWrites.Unlock()
		assert.False(t, retained, "unused gates must be removed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := lockExternalAlertingWrite(ctx, key)
		if err == nil {
			release()
		}
		done <- err
	}()
	require.Eventually(t, func() bool {
		externalAlertingWrites.Lock()
		defer externalAlertingWrites.Unlock()
		return externalAlertingWrites.gates[key].refs == 2
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	externalAlertingWrites.Lock()
	refs := externalAlertingWrites.gates[key].refs
	externalAlertingWrites.Unlock()
	assert.Equal(t, 1, refs, "cancelled waiter must release its reference")
}

// An omitted organization can resolve to the same destination as an explicit
// selector. Both rule groups and receiver configurations must share a gate.
func TestExternalWriteImplicitAndExplicitOrgSerialize(t *testing.T) {
	for _, receiver := range []bool{false, true} {
		for i, config := range []mcpgrafana.GrafanaConfig{
			{OrgID: 1},
			{ExtraHeaders: map[string]string{"X-Grafana-Org-Id": "1"}},
			{ExtraHeaders: map[string]string{"x-grafana-org-id": "01"}},
		} {
			t.Run(fmt.Sprintf("receiver=%t/selector=%d", receiver, i), func(t *testing.T) {
				server := httptest.NewServer(http.NotFoundHandler())
				defer server.Close()
				implicit := mockDatasourcesCtx(server)
				client, err := newAlertingClientFromContext(implicit)
				require.NoError(t, err)
				config.URL = server.URL
				explicit := mcpgrafana.WithGrafanaConfig(t.Context(), config)
				first := client.externalWriteKey(implicit, "mimir", "team", "group", receiver)
				second := client.externalWriteKey(explicit, "mimir", "team", "group", receiver)
				unlock, err := lockExternalAlertingWrite(t.Context(), first)
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				defer cancel()
				release, err := lockExternalAlertingWrite(ctx, second)
				if release != nil {
					release()
				}
				unlock()
				require.ErrorIs(t, err, context.DeadlineExceeded, "same destination must wait regardless of org selector")
				release, err = lockExternalAlertingWrite(t.Context(), second)
				require.NoError(t, err)
				release()
				externalAlertingWrites.Lock()
				_, retained := externalAlertingWrites.gates[first]
				externalAlertingWrites.Unlock()
				assert.False(t, retained)
			})
		}
	}
}
