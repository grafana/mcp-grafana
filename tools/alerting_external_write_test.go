//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMimirDatasource = `{"uid":"mimir","name":"Mimir","type":"prometheus","jsonData":{"prometheusType":"Mimir"}}`
const testAMDatasource = `{"uid":"am","name":"External AM","type":"alertmanager","jsonData":{"implementation":"mimir"}}`
const testExternalAMConfig = `{"template_files":{"notify.tmpl":"original template"},"alertmanager_config":{"global":{"smtp_auth_password":"keep-password"},"route":{"receiver":"existing","routes":[{"receiver":"existing","matchers":["team=pmo"]}]},"receivers":[{"name":"existing","webhook_configs":[{"url":"https://example.org/secret-hook"}]}],"inhibit_rules":[{"source_matchers":["severity=critical"],"target_matchers":["severity=warning"],"equal":["alertname"]}],"templates":["notify.tmpl"],"future_field":{"number":9007199254740993}}}`

func externalRuleArgs() ManageRulesReadWriteParams {
	return ManageRulesReadWriteParams{Operation: "create", ManagedBy: "datasource", Title: "NewAlert", Namespace: "team", RuleGroup: "group", Expression: "up == 0", For: "5m"}
}

func externalReceiverArgs() ManageRoutingWriteParams {
	return ManageRoutingWriteParams{Operation: "create_contact_point", ManagedBy: "datasource", Name: "NewReceiver", Type: "webhook", Settings: map[string]any{"url": "https://example.org/new-secret"}}
}

func TestExternalAlertingDatasourceSelection(t *testing.T) {
	for _, tc := range []struct {
		name, uid, response, wantUID, wantErr string
		receiver                              bool
	}{
		{name: "sole ruler", response: "[" + testMimirDatasource + "]", wantUID: "mimir"},
		{name: "sole receiver", response: "[" + testAMDatasource + "]", receiver: true, wantUID: "am"},
		{name: "explicit ruler", uid: "mimir", response: testMimirDatasource, wantUID: "mimir"},
		{name: "explicit receiver", uid: "am", response: testAMDatasource, receiver: true, wantUID: "am"},
		{name: "sole default receiver", response: `[{"uid":"am","type":"alertmanager","jsonData":{}}]`, receiver: true, wantUID: "am"},
		{name: "explicit default receiver", uid: "am", response: `{"uid":"am","type":"alertmanager","jsonData":{}}`, receiver: true, wantUID: "am"},
		{name: "receiver without jsonData", response: `[{"uid":"am","type":"alertmanager"}]`, receiver: true, wantUID: "am"},
		{name: "receiver null implementation", response: `[{"uid":"am","type":"alertmanager","jsonData":{"implementation":null}}]`, receiver: true, wantUID: "am"},
		{name: "receiver empty implementation", response: `[{"uid":"am","type":"alertmanager","jsonData":{"implementation":""}}]`, receiver: true, wantErr: "no compatible"},
		{name: "receiver unknown implementation", response: `[{"uid":"am","type":"alertmanager","jsonData":{"implementation":"other"}}]`, receiver: true, wantErr: "no compatible"},
		{name: "default receiver participates in ambiguity", response: "[" + testAMDatasource + `,{"uid":"default-am","type":"alertmanager","jsonData":{}}]`, receiver: true, wantErr: "Ask the user to choose"},
		{name: "wrong kind", uid: "mimir", response: testMimirDatasource, receiver: true, wantErr: "no compatible"},
		{name: "mismatched uid", uid: "am", response: testMimirDatasource, wantErr: "requested UID"},
		{name: "none", response: "[]", wantErr: "no compatible"},
		{name: "standalone prometheus", response: `[{"uid":"prom","type":"prometheus","jsonData":{"prometheusType":"Prometheus"}}]`, wantErr: "no compatible"},
		{name: "unknown ruler is not auto selected", response: `[{"uid":"mimir","type":"prometheus","jsonData":{}}]`, wantErr: "no compatible"},
		{name: "standalone alertmanager", response: `[{"uid":"prom","type":"alertmanager","jsonData":{"implementation":"prometheus"}}]`, receiver: true, wantErr: "no compatible"},
		{name: "disabled ruler", response: `[{"uid":"loki","type":"loki","jsonData":{"manageAlerts":false}}]`, wantErr: "no compatible"},
		{name: "loki", response: `[{"uid":"loki","type":"loki"}]`, wantUID: "loki"},
		{name: "cortex", response: strings.ReplaceAll("["+testMimirDatasource+"]", "Mimir", "Cortex"), wantUID: "mimir"},
		{name: "ignore unrelated", response: "[" + testAMDatasource + "," + testMimirDatasource + "]", wantUID: "mimir"},
		{name: "ambiguous even with default", response: "[" + testMimirDatasource + `,{"uid":"loki","name":"Loki","type":"loki","isDefault":true}]`, wantErr: "Ask the user to choose"},
		{name: "ambiguous receivers", response: "[" + testAMDatasource + "," + strings.ReplaceAll(testAMDatasource, `"am"`, `"am2"`) + "]", receiver: true, wantErr: "Ask the user to choose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				path := "/api/datasources"
				if tc.uid != "" {
					path += "/uid/" + tc.uid
				}
				assert.Equal(t, path, r.URL.Path)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			client, err := newAlertingClientFromContext(mockDatasourcesCtx(server))
			require.NoError(t, err)
			ds, err := client.resolveExternalAlertingDatasource(t.Context(), tc.uid, tc.receiver)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantUID, ds.UID)
			}
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestCreateExternalReceiverWithDefaultImplementation(t *testing.T) {
	for _, explicitUID := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicitUID=%v", explicitUID), func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				metadataPath := "/api/datasources"
				metadata := `[{"uid":"am","name":"Default Cortex","type":"alertmanager","jsonData":{}}]`
				if explicitUID {
					metadataPath += "/uid/am"
					metadata = `{"uid":"am","name":"Default Cortex","type":"alertmanager","jsonData":{}}`
				}
				if r.URL.Path == metadataPath {
					assert.Equal(t, http.MethodGet, r.Method)
					_, _ = w.Write([]byte(metadata))
					return
				}
				assert.Equal(t, "/api/alertmanager/am/config/api/v1/alerts", r.URL.Path)
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(testExternalAMConfig))
					return
				}
				assert.Equal(t, http.MethodPost, r.Method)
				posts.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			args := externalReceiverArgs()
			if explicitUID {
				args.DatasourceUID = "am"
			}
			session := routingTestSession(t, mockDatasourcesCtx(server), true)
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "alerting_routing_write", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)
			assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"datasource_uid":"am"`)
			assert.EqualValues(t, 1, posts.Load())
		})
	}
}

func TestCreateDatasourceRulePreservesGroup(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, backend := range []string{"Mimir", "Cortex", "Loki"} {
			t.Run(fmt.Sprintf("%s/existing=%v", backend, existing), func(t *testing.T) {
				var posts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/datasources" {
						ds := strings.ReplaceAll(testMimirDatasource, "Mimir", backend)
						if backend == "Loki" {
							ds = strings.ReplaceAll(ds, `"type":"prometheus"`, `"type":"loki"`)
						}
						_, _ = fmt.Fprintf(w, "[%s]", ds)
						return
					}
					if backend != "Loki" {
						assert.Equal(t, strings.ToLower(backend), r.URL.Query().Get("subtype"))
					}
					if r.Method == http.MethodGet {
						assert.Equal(t, "/api/ruler/mimir/api/v1/rules/team/group", r.URL.Path)
						if existing {
							_, _ = w.Write([]byte(`{"name":"group","interval":"30s","limit":25,"future":9007199254740993,"rules":[{"record":"existing_metric","expr":"sum(up)","labels":{"team":"bat"}}]}`))
						} else {
							w.WriteHeader(http.StatusNotFound)
							_, _ = w.Write([]byte(`{"message":"group does not exist"}`))
						}
						return
					}
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "/api/ruler/mimir/api/v1/rules/team", r.URL.Path)
					var group externalAlertingObject
					require.NoError(t, json.NewDecoder(r.Body).Decode(&group))
					var rules []json.RawMessage
					require.NoError(t, json.Unmarshal(group["rules"], &rules))
					if existing {
						assert.JSONEq(t, `"30s"`, string(group["interval"]))
						assert.Equal(t, "9007199254740993", string(group["future"]))
						assert.Equal(t, "25", string(group["limit"]))
						require.Len(t, rules, 2)
						assert.JSONEq(t, `{"record":"existing_metric","expr":"sum(up)","labels":{"team":"bat"}}`, string(rules[0]))
					} else {
						require.Len(t, rules, 1)
						assert.JSONEq(t, `"1m"`, string(group["interval"]))
					}
					assert.JSONEq(t, `{"alert":"NewAlert","expr":"up == 0","for":"5m"}`, string(rules[len(rules)-1]))
					posts.Add(1)
					w.WriteHeader(http.StatusAccepted)
				}))
				defer server.Close()
				session := routingTestSession(t, mockDatasourcesCtx(server), true)
				result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "alerting_rules_write", Arguments: externalRuleArgs()})
				require.NoError(t, err)
				require.False(t, result.IsError, "%v", result.Content)
				assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"datasource_uid":"mimir"`)
				assert.EqualValues(t, 1, posts.Load())
			})
		}
	}
}

func TestCreateExternalReceiverPreservesConfiguration(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/datasources/uid/am" {
			_, _ = w.Write([]byte(testAMDatasource))
			return
		}
		assert.Equal(t, "/api/alertmanager/am/config/api/v1/alerts", r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(testExternalAMConfig))
			return
		}
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Empty(t, r.Header.Get("X-Disable-Provenance"))
		var posted externalAlertingObject
		require.NoError(t, json.NewDecoder(r.Body).Decode(&posted))
		original, err := decodeExternalAlertingObject([]byte(testExternalAMConfig))
		require.NoError(t, err)
		originalAM, err := decodeExternalAlertingObject(original["alertmanager_config"])
		require.NoError(t, err)
		postedAM, err := decodeExternalAlertingObject(posted["alertmanager_config"])
		require.NoError(t, err)
		var receivers []json.RawMessage
		require.NoError(t, json.Unmarshal(postedAM["receivers"], &receivers))
		require.Len(t, receivers, 2)
		assert.JSONEq(t, `{"name":"NewReceiver","webhook_configs":[{"url":"https://example.org/new-secret","send_resolved":true}]}`, string(receivers[1]))
		require.NoError(t, setExternalAlertingField(postedAM, "receivers", receivers[:1]))
		for key, value := range originalAM {
			assert.JSONEq(t, string(value), string(postedAM[key]), key)
		}
		assert.Equal(t, `{"number":9007199254740993}`, string(postedAM["future_field"]))
		assert.JSONEq(t, string(original["template_files"]), string(posted["template_files"]))
		posts.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	args := externalReceiverArgs()
	args.DatasourceUID = "am"
	session := routingTestSession(t, mockDatasourcesCtx(server), true)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "alerting_routing_write", Arguments: args})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	text := result.Content[0].(*mcp.TextContent).Text
	assert.JSONEq(t, `{"uid":"","name":"NewReceiver","type":"webhook","datasource_uid":"am"}`, text)
	assert.NotContains(t, text, "secret")
	assert.NotContains(t, text, "password")
	assert.EqualValues(t, 1, posts.Load())
}

func TestExternalAlertingRejectsUnsafeInputsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ctx := mockDatasourcesCtx(server)
	for _, mutate := range []func(*ManageRulesReadWriteParams){
		func(p *ManageRulesReadWriteParams) { p.ManagedBy = "" },
		func(p *ManageRulesReadWriteParams) { p.ManagedBy = "invalid" },
		func(p *ManageRulesReadWriteParams) { p.DatasourceUID = new("grafana") },
		func(p *ManageRulesReadWriteParams) { p.DatasourceUID = new("../grafana") },
		func(p *ManageRulesReadWriteParams) { p.Operation = "update" },
		func(p *ManageRulesReadWriteParams) { p.Operation = "delete" },
		func(p *ManageRulesReadWriteParams) { p.Expression = "" },
		func(p *ManageRulesReadWriteParams) { p.Namespace = "" },
		func(p *ManageRulesReadWriteParams) { p.FolderUID = "folder" },
		func(p *ManageRulesReadWriteParams) { p.For = "bad" },
		func(p *ManageRulesReadWriteParams) { p.EvaluationInterval = "0s" },
	} {
		args := externalRuleArgs()
		mutate(&args)
		_, err := manageRulesReadWrite(ctx, args)
		require.Error(t, err)
	}
	for _, mutate := range []func(*ManageRoutingWriteParams){
		func(p *ManageRoutingWriteParams) { p.ManagedBy = ""; p.DatasourceUID = "am" },
		func(p *ManageRoutingWriteParams) { p.ManagedBy = "invalid" },
		func(p *ManageRoutingWriteParams) { p.DatasourceUID = "grafana" },
		func(p *ManageRoutingWriteParams) { p.UID = "grafana-only" },
		func(p *ManageRoutingWriteParams) { p.DisableProvenance = new(true) },
		func(p *ManageRoutingWriteParams) { p.Type = "unsupported" },
		func(p *ManageRoutingWriteParams) {
			p.Settings = map[string]any{"addresses": "grafana@example.org"}
			p.Type = "email"
		},
		func(p *ManageRoutingWriteParams) { p.Settings["send_resolved"] = true },
	} {
		args := externalReceiverArgs()
		mutate(&args)
		_, err := manageRoutingWrite(ctx, args)
		require.Error(t, err)
	}
	assert.Zero(t, calls.Load())
}

func TestExternalCreationReadFailuresNeverWrite(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		receiver   bool
	}{
		{name: "ruler forbidden", status: 403, body: "secret"},
		{name: "ruler unavailable", status: 500},
		{name: "unsupported ruler", status: 404, body: `{"message":"not found"}`},
		{name: "duplicate rule", status: 200, body: `{"name":"group","rules":[{"alert":"NewAlert","expr":"up"}]}`},
		{name: "invalid group", status: 200, body: `{}`},
		{name: "am forbidden", status: 403, receiver: true, body: "secret"},
		{name: "am absent", status: 404, receiver: true},
		{name: "am malformed", status: 200, receiver: true, body: `{}`},
		{name: "am redacted", status: 200, receiver: true, body: strings.ReplaceAll(testExternalAMConfig, "keep-password", "<secret>")},
		{name: "duplicate receiver", status: 200, receiver: true, body: strings.ReplaceAll(testExternalAMConfig, "existing", "NewReceiver")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				if r.URL.Path == "/api/datasources" {
					_, _ = fmt.Fprintf(w, "[%s,%s]", testMimirDatasource, testAMDatasource)
					return
				}
				assert.NotContains(t, r.URL.Path, "/provisioning/")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			ctx := mockDatasourcesCtx(server)
			var err error
			if tc.receiver {
				_, err = manageRoutingWrite(ctx, externalReceiverArgs())
			} else {
				_, err = manageRulesReadWrite(ctx, externalRuleArgs())
			}
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "keep-password")
		})
	}
}

func TestExternalReceiverConcurrentCreatesPreserveBoth(t *testing.T) {
	var mu sync.Mutex
	config := []byte(testExternalAMConfig)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/datasources" {
			_, _ = fmt.Fprintf(w, "[%s]", testAMDatasource)
		} else if r.Method == http.MethodGet {
			_, _ = w.Write(config)
		} else {
			var err error
			config, err = readResponseBody(r.Body, defaultResponseLimitBytes)
			assert.NoError(t, err)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()
	ctx := mockDatasourcesCtx(server)
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two"} {
		wg.Go(func() {
			args := externalReceiverArgs()
			args.Name = name
			_, err := manageRoutingWrite(ctx, args)
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, string(config), `"name":"one"`)
	assert.Contains(t, string(config), `"name":"two"`)
	assert.Contains(t, string(config), `"name":"existing"`)
}

func TestExternalAlertingCancelledWait(t *testing.T) {
	key := externalAlertingWriteKey{instance: t.Name()}
	unlock, err := lockExternalAlertingWrite(t.Context(), key)
	require.NoError(t, err)
	defer unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = lockExternalAlertingWrite(ctx, key)
	require.ErrorIs(t, err, context.Canceled)
}

func TestGrafanaRuleCreationRemainsDefault(t *testing.T) {
	for _, managedBy := range []string{"", "grafana"} {
		t.Run("managed_by="+managedBy, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/v1/provisioning/alert-rules", r.URL.Path)
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Equal(t, "mimir-rules", body["data"].([]any)[0].(map[string]any)["datasourceUid"])
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				body["uid"] = "grafana-created"
				_ = json.NewEncoder(w).Encode(body)
				posts.Add(1)
			}))
			defer server.Close()
			_, err := manageRulesReadWrite(mockCtxWithClient(server), ManageRulesReadWriteParams{
				Operation: "create", ManagedBy: managedBy, Title: "Grafana rule", RuleGroup: "group", FolderUID: "folder", Condition: "A",
				Data:        []map[string]any{{"datasourceUid": "mimir-rules", "refId": "A", "model": map[string]any{"expr": "up == 0"}}},
				NoDataState: "OK", ExecErrState: "OK", For: "5m", OrgID: 1,
			})
			require.NoError(t, err)
			assert.EqualValues(t, 1, posts.Load())
		})
	}
}

func TestExternalAlertingWriteErrorsAndRedirects(t *testing.T) {
	for _, status := range []int{302, 400, 403, 409, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodPost, r.Method)
				w.Header().Set("Location", "/login")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("backend echoed secret-password"))
			}))
			defer server.Close()
			client, err := newAlertingClientFromContext(mockDatasourcesCtx(server))
			require.NoError(t, err)
			err = client.postExternalAlerting(t.Context(), "/config", nil, map[string]string{"password": "secret-password"})
			require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", status))
			assert.NotContains(t, err.Error(), "secret-password")
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestRulerNamesWithPathSeparators(t *testing.T) {
	for _, value := range []string{"team/nested", `team\nested`, ".", "..", "QUERY_NAMESPACE"} {
		query := url.Values{}
		assert.Equal(t, "QUERY_NAMESPACE", rulerPathSegment(value, "namespace", query))
		assert.Equal(t, value, query.Get("namespace"))
	}
	query := url.Values{}
	assert.Equal(t, "QUERY_GROUP", rulerPathSegment("group/nested", "group", query))
	assert.Equal(t, "group/nested", query.Get("group"))
	assert.Equal(t, "team%20one", rulerPathSegment("team one", "namespace", url.Values{}))
}

func TestExternalAlertingResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(defaultResponseLimitBytes)+1)))
	}))
	defer server.Close()
	client, err := newAlertingClientFromContext(mockDatasourcesCtx(server))
	require.NoError(t, err)
	_, _, err = client.externalAlertingRequest(t.Context(), http.MethodGet, "/config", nil, nil)
	require.Error(t, err)
}
