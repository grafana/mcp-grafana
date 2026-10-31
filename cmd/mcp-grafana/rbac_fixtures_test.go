package main

import (
	"fmt"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rbacFixture says how TestToolPermissions (rbac_cloud_test.go) calls a tool
// against the mcptests stack.
type rbacFixture struct {
	// args are the tool's arguments.
	args map[string]any
	// setup, if set, runs with the admin token before every call of the tool
	// and returns arguments merged over args, for tools that need a fresh
	// resource (one to delete, or a unique name to create). It registers the
	// resource's cleanup with t.Cleanup.
	setup func(t *testing.T, api rbacAPI) map[string]any
	// skip, if set, is why the tool can't be checked on mcptests.
	skip string
	// filtered marks a tool that returns only what the caller may see, so it
	// succeeds (with less) without permissions; the no-permissions check is
	// skipped for it.
	filtered bool
}

// rbacAPI calls the Grafana HTTP API with the admin token, returning an error
// for a non-2xx response, and decodes the JSON response into out when it is
// non-nil.
type rbacAPI func(method, path string, body, out any) error

// Resources on the mcptests stack the fixtures point at. rbacDashboardUID is
// created (or overwritten) by TestToolPermissions itself, with one panel
// querying rbacPrometheusUID.
const (
	rbacPrometheusUID = "grafanacloud-usage"
	rbacLokiUID       = "grafanacloud-logs"
	rbacTempoUID      = "grafanacloud-traces"
	rbacPyroscopeUID  = "grafanacloud-profiles"
	rbacGraphiteUID   = "grafanacloud-graphite"
	rbacDashboardUID  = "mcp-rbac-test-dashboard"
	rbacPanelID       = 1

	rbacLokiSelector = `{service_name=~".+"}`
	rbacFakeTraceID  = "0123456789abcdef0123456789abcdef"
)

const (
	skipNoSQL          = "mcptests has no SQL datasource"
	skipNoCloudWatch   = "mcptests has no CloudWatch datasource"
	skipNoCloudLogging = "mcptests has no Google Cloud Logging datasource"
	skipIncident       = "needs a live incident; list_incidents covers the same declared permissions"
	skipNotebooks      = "notebooks are behind the dashboard.notebooks toggle, which mcptests does not have on"
)

var rbacSeq atomic.Int64

// rbacUnique returns a name unique to this test process, for resources a
// fixture creates.
func rbacUnique(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), rbacSeq.Add(1))
}

// rbacAnnotation creates an annotation and returns its ID, deleting it when t
// ends unless the tool already has.
func rbacAnnotation(t *testing.T, api rbacAPI) float64 {
	var created struct {
		ID float64 `json:"id"`
	}
	require.NoError(t, api("POST", "/api/annotations", map[string]any{"text": "mcp rbac test", "tags": []string{"mcp-rbac-test"}}, &created))
	t.Cleanup(func() { rbacDeleteQuietly(t, api, fmt.Sprintf("/api/annotations/%d", int64(created.ID))) })
	return created.ID
}

// rbacSnapshot creates a snapshot and returns its key.
func rbacSnapshot(t *testing.T, api rbacAPI) string {
	key := rbacUnique("mcp-rbac-test")
	require.NoError(t, api("POST", "/api/snapshots", map[string]any{"dashboard": rbacSnapshotDashboard(), "key": key}, nil))
	t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/snapshots/"+key) })
	return key
}

// rbacSnapshotDashboard is a dashboard model for a snapshot of
// rbacDashboardUID: Grafana Cloud refuses snapshots of a dashboard it doesn't
// have.
func rbacSnapshotDashboard() map[string]any {
	return map[string]any{"uid": rbacDashboardUID, "title": "mcp rbac test", "panels": []any{}}
}

// rbacDeleteQuietly deletes a resource the tool under test may already have
// deleted, so a failure is only logged.
func rbacDeleteQuietly(t *testing.T, api rbacAPI, path string) {
	if err := api("DELETE", path, nil, nil); err != nil {
		t.Logf("cleanup of %s: %v", path, err)
	}
}

// rbacFirstRoleUID returns the UID of a role on the stack.
func rbacFirstRoleUID(t *testing.T, api rbacAPI) map[string]any {
	var roles []struct {
		UID string `json:"uid"`
	}
	require.NoError(t, api("GET", "/api/access-control/roles", nil, &roles))
	if len(roles) == 0 {
		t.Skip("mcptests has no roles")
	}
	return map[string]any{"roleUID": roles[0].UID}
}

var rbacFixtures = map[string]rbacFixture{
	"add_activity_to_incident":    {skip: skipIncident},
	"create_incident":             {skip: skipIncident},
	"get_incident":                {skip: skipIncident},
	"update_incident":             {skip: skipIncident},
	"list_incidents":              {args: map[string]any{"limit": 1}},
	"list_incident_custom_fields": {args: map[string]any{}},

	"agento11y_manage_agents":           {args: map[string]any{"operation": "list"}},
	"agento11y_manage_conversations":    {args: map[string]any{"operation": "list"}},
	"agento11y_manage_eval_collections": {args: map[string]any{"operation": "list_collections"}},
	"agento11y_manage_eval_rules":       {args: map[string]any{"operation": "list_rules"}},
	"agento11y_manage_evaluators":       {args: map[string]any{"operation": "list_evaluators"}},
	"agento11y_manage_experiments":      {args: map[string]any{"operation": "list"}},
	"agento11y_manage_generations":      {skip: "needs a live generation ID"},
	"agento11y_manage_test_suites":      {args: map[string]any{"operation": "list_suites"}},

	// Notebooks are behind the dashboard.notebooks toggle, which mcptests does
	// not have on, so there is nothing for these calls to succeed against.
	"create_notebook":     {skip: skipNotebooks},
	"delete_notebook":     {skip: skipNotebooks},
	"get_notebook_by_uid": {skip: skipNotebooks},
	"list_notebooks":      {skip: skipNotebooks},
	"search_notebooks":    {skip: skipNotebooks},
	"update_notebook":     {skip: skipNotebooks},

	"alerting_manage_routing": {args: map[string]any{"operation": "get_notification_policies"}},
	"alerting_routing_write": {
		args: map[string]any{"operation": "create_contact_point", "type": "email", "settings": map[string]any{"addresses": "mcp-rbac-test@example.com"}},
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			uid := rbacUnique("mcp-rbac")
			t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/v1/provisioning/contact-points/"+uid) })
			return map[string]any{"uid": uid, "name": uid}
		},
	},
	"alerting_rules_read":     {args: map[string]any{"operation": "list"}},
	"alerting_rules_write":    {args: map[string]any{"operation": "list"}},
	"alerting_silences_read":  {args: map[string]any{"operation": "list"}},
	"alerting_silences_write": {args: map[string]any{"operation": "list"}},

	"get_annotations":     {args: map[string]any{"limit": 1}},
	"get_annotation_tags": {args: map[string]any{}},
	"create_annotation": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			tag := rbacUnique("mcp-rbac-test")
			t.Cleanup(func() {
				var found []struct {
					ID float64 `json:"id"`
				}
				if err := api("GET", "/api/annotations?tags="+url.QueryEscape(tag), nil, &found); err != nil {
					t.Logf("cleanup of annotations tagged %s: %v", tag, err)
				}
				for _, a := range found {
					rbacDeleteQuietly(t, api, fmt.Sprintf("/api/annotations/%d", int64(a.ID)))
				}
			})
			return map[string]any{"text": "mcp rbac test", "tags": []string{tag}}
		},
	},
	"update_annotation": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			return map[string]any{"id": rbacAnnotation(t, api), "text": "mcp rbac test (updated)"}
		},
	},
	"delete_annotation": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			return map[string]any{"id": rbacAnnotation(t, api)}
		},
	},

	"list_datasources":         {args: map[string]any{}, filtered: true},
	"get_datasource":           {args: map[string]any{"uid": rbacPrometheusUID}},
	"check_datasources_health": {args: map[string]any{"uids": []string{rbacPrometheusUID}}},
	"create_datasource": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			name := rbacUnique("mcp-rbac-test")
			t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/datasources/name/"+url.PathEscape(name)) })
			return map[string]any{"name": name, "type": "prometheus", "url": "http://localhost:9090", "access": "proxy", "schemaReviewed": true}
		},
	},
	"update_datasource": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			name := rbacUnique("mcp-rbac-test")
			var created struct {
				Datasource struct {
					UID string `json:"uid"`
				} `json:"datasource"`
			}
			require.NoError(t, api("POST", "/api/datasources", map[string]any{"name": name, "type": "prometheus", "url": "http://localhost:9090", "access": "proxy"}, &created))
			t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/datasources/uid/"+created.Datasource.UID) })
			return map[string]any{"uid": created.Datasource.UID, "url": "http://localhost:9091", "schemaReviewed": true}
		},
	},
	"get_query_examples": {args: map[string]any{"datasourceType": "prometheus"}},

	"search_dashboards": {args: map[string]any{"query": "mcp-rbac"}, filtered: true},
	// version takes the legacy versions API, which needs more than the
	// current-dashboard path.
	"get_dashboard_by_uid":        {args: map[string]any{"uid": rbacDashboardUID, "version": 1}},
	"get_dashboard_panel_queries": {args: map[string]any{"uid": rbacDashboardUID}},
	"get_dashboard_property":      {args: map[string]any{"uid": rbacDashboardUID, "jsonPath": "$.title"}},
	"get_dashboard_summary":       {args: map[string]any{"uid": rbacDashboardUID}},
	"list_dashboard_versions":     {args: map[string]any{"uid": rbacDashboardUID}},
	"run_panel_query":             {args: map[string]any{"dashboardUid": rbacDashboardUID, "panelIds": []int{rbacPanelID}}},
	"get_panel_image":             {args: map[string]any{"dashboardUid": rbacDashboardUID, "panelId": rbacPanelID}},
	"update_dashboard": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			uid := rbacUnique("mcp-rbac")
			t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/dashboards/uid/"+uid) })
			return map[string]any{"dashboard": map[string]any{"uid": uid, "title": uid, "panels": []any{}}, "message": "mcp rbac test"}
		},
	},
	"generate_deeplink": {args: map[string]any{"resourceType": "dashboard", "dashboardUid": rbacDashboardUID}},

	"search_folders": {args: map[string]any{}, filtered: true},
	"create_folder": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			uid := rbacUnique("mcp-rbac")
			t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/folders/"+uid) })
			return map[string]any{"uid": uid, "title": uid}
		},
	},

	"list_snapshots": {args: map[string]any{}},
	"get_snapshot": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			return map[string]any{"key": rbacSnapshot(t, api)}
		},
	},
	"delete_snapshot": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			return map[string]any{"key": rbacSnapshot(t, api)}
		},
	},
	"create_snapshot": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			key := rbacUnique("mcp-rbac-test")
			t.Cleanup(func() { rbacDeleteQuietly(t, api, "/api/snapshots/"+key) })
			return map[string]any{"dashboard": rbacSnapshotDashboard(), "key": key}
		},
	},

	"list_oncall_schedules":    {args: map[string]any{}},
	"list_oncall_teams":        {args: map[string]any{}},
	"list_oncall_users":        {args: map[string]any{}},
	"list_alert_groups":        {args: map[string]any{}},
	"get_alert_group":          {skip: "needs a live alert group; list_alert_groups covers the same declared permissions"},
	"update_alert_group":       {skip: "changes the state of a live alert group"},
	"get_oncall_shift":         {skip: "needs a live on-call shift ID"},
	"get_current_oncall_users": {skip: "needs a live on-call schedule ID"},

	"get_assertions": {args: map[string]any{"startTime": "now-1h", "endTime": "now", "entityType": "Service", "entityName": "mcp-rbac-test"}},

	"list_all_roles":           {args: map[string]any{}},
	"get_role_details":         {setup: rbacFirstRoleUID},
	"get_role_assignments":     {setup: rbacFirstRoleUID},
	"list_teams":               {args: map[string]any{}},
	"list_users_by_org":        {args: map[string]any{}},
	"get_resource_description": {args: map[string]any{"resourceType": "dashboards"}},
	"get_resource_permissions": {args: map[string]any{"resource": "dashboards", "resourceId": rbacDashboardUID}},
	// TODO: find what Grafana Cloud needs here and declare it.
	"list_team_roles": {skip: "Grafana Cloud authorizes this route through iam.grafana.app rolebindings, " +
		"which a custom role with teams.roles:read (on teams:*) doesn't satisfy"},
	"list_user_roles": {
		setup: func(t *testing.T, api rbacAPI) map[string]any {
			var users []struct {
				UserID int64 `json:"userId"`
			}
			require.NoError(t, api("GET", "/api/org/users", nil, &users))
			if len(users) == 0 {
				t.Skip("mcptests has no org users")
			}
			return map[string]any{"userIds": []int64{users[0].UserID}}
		},
	},

	"query_prometheus":                {args: map[string]any{"datasourceUid": rbacPrometheusUID, "expr": "vector(1)", "endTime": "now", "queryType": "instant"}},
	"query_prometheus_histogram":      {args: map[string]any{"datasourceUid": rbacPrometheusUID, "metric": "mcp_rbac_test", "percentile": 95, "endTime": "now"}},
	"list_prometheus_label_names":     {args: map[string]any{"datasourceUid": rbacPrometheusUID, "limit": 1}},
	"list_prometheus_label_values":    {args: map[string]any{"datasourceUid": rbacPrometheusUID, "labelName": "__name__", "limit": 1}},
	"list_prometheus_metric_metadata": {args: map[string]any{"datasourceUid": rbacPrometheusUID, "limit": 1}},
	"list_prometheus_metric_names":    {args: map[string]any{"datasourceUid": rbacPrometheusUID, "limit": 1}},

	"query_loki_logs":                 {args: map[string]any{"datasourceUid": rbacLokiUID, "logql": rbacLokiSelector, "limit": 1}},
	"query_loki_patterns":             {args: map[string]any{"datasourceUid": rbacLokiUID, "logql": rbacLokiSelector}},
	"query_loki_stats":                {args: map[string]any{"datasourceUid": rbacLokiUID, "logql": rbacLokiSelector}},
	"list_loki_label_names":           {args: map[string]any{"datasourceUid": rbacLokiUID}},
	"list_loki_label_values":          {args: map[string]any{"datasourceUid": rbacLokiUID, "labelName": "service_name"}},
	"analyze_loki_labels":             {args: map[string]any{"datasourceUid": rbacLokiUID}},
	"suggest_loki_alloy_label_config": {args: map[string]any{"approvedLabels": []string{"service_name"}}},

	"search_tempo_traces":         {args: map[string]any{"datasourceUid": rbacTempoUID, "query": "{}"}},
	"query_tempo_metrics":         {args: map[string]any{"datasourceUid": rbacTempoUID, "query": "{} | rate()"}},
	"get_tempo_trace":             {args: map[string]any{"datasourceUid": rbacTempoUID, "trace_id": rbacFakeTraceID}},
	"diff_tempo_traces":           {args: map[string]any{"datasourceUid": rbacTempoUID, "base_trace_id": rbacFakeTraceID, "compare_trace_id": rbacFakeTraceID}},
	"list_tempo_attribute_names":  {args: map[string]any{"datasourceUid": rbacTempoUID}},
	"list_tempo_attribute_values": {args: map[string]any{"datasourceUid": rbacTempoUID, "name": "resource.service.name"}},
	"get_tempo_traceql_docs":      {args: map[string]any{"topic": "basic"}},

	"query_pyroscope":              {args: map[string]any{"data_source_uid": rbacPyroscopeUID, "profile_type": "process_cpu:cpu:nanoseconds:cpu:nanoseconds"}},
	"list_pyroscope_label_names":   {args: map[string]any{"data_source_uid": rbacPyroscopeUID}},
	"list_pyroscope_label_values":  {args: map[string]any{"data_source_uid": rbacPyroscopeUID, "name": "service_name"}},
	"list_pyroscope_profile_types": {args: map[string]any{"data_source_uid": rbacPyroscopeUID}},

	"query_graphite":         {args: map[string]any{"datasourceUid": rbacGraphiteUID, "target": "*"}},
	"query_graphite_density": {args: map[string]any{"datasourceUid": rbacGraphiteUID, "target": "*"}},
	"list_graphite_metrics":  {args: map[string]any{"datasourceUid": rbacGraphiteUID, "query": "*"}},
	"list_graphite_tags":     {args: map[string]any{"datasourceUid": rbacGraphiteUID}},

	"query_sql":          {skip: skipNoSQL},
	"list_sql_databases": {skip: skipNoSQL},
	"list_sql_tables":    {skip: skipNoSQL},
	"describe_sql_table": {skip: skipNoSQL},

	"query_cloudwatch":                 {skip: skipNoCloudWatch},
	"list_cloudwatch_namespaces":       {skip: skipNoCloudWatch},
	"list_cloudwatch_metrics":          {skip: skipNoCloudWatch},
	"list_cloudwatch_dimensions":       {skip: skipNoCloudWatch},
	"list_cloudwatch_dimension_values": {skip: skipNoCloudWatch},
	"query_cloudwatch_logs":            {skip: skipNoCloudWatch},
	"list_cloudwatch_log_groups":       {skip: skipNoCloudWatch},
	"list_cloudwatch_log_group_fields": {skip: skipNoCloudWatch},

	"query_cloud_logging":         {skip: skipNoCloudLogging},
	"list_cloud_logging_projects": {skip: skipNoCloudLogging},
	"list_cloud_logging_buckets":  {skip: skipNoCloudLogging},
	"list_cloud_logging_views":    {skip: skipNoCloudLogging},

	"query_elasticsearch": {skip: "mcptests has no Elasticsearch datasource"},
	"query_quickwit":      {skip: "mcptests has no Quickwit datasource"},
	"query_influxdb":      {skip: "mcptests has no InfluxDB datasource"},

	"get_plugin":                {args: map[string]any{"pluginId": "grafana-irm-app"}},
	"install_plugin":            {skip: "installs a plugin on the shared stack"},
	"search_plugin_information": {args: map[string]any{"query": "irm"}},

	"list_provisioning_repositories": {args: map[string]any{}},
	"validate_provisioning_file":     {skip: "mcptests has no provisioning repository"},

	"grafana_api_request": {args: map[string]any{"endpoint": "/api/health"}},
	"ask_assistant":       {args: map[string]any{"prompt": "Reply with the single word OK."}},
	"search_docs":         {args: map[string]any{"query": "dashboards", "limit": 1}},
	"get_doc":             {args: map[string]any{"url": "https://grafana.com/docs/grafana/latest/dashboards/", "outline_only": true}},
	"user_info":           {args: map[string]any{}},
}
