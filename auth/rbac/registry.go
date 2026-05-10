package rbac

// ToolGates is the static registry mapping MCP tool names to their RBAC and
// basic-role requirements. This is the SINGLE source of truth for tool gating.
//
// Adding a new tool? You must also add an entry here. The package's tests
// fail when a tool is registered on an MCPServer without a corresponding
// entry here (see auth/rbac/registry_test.go).
//
// The Permissions list reflects Grafana's RBAC actions/scopes. If you're not
// sure what permissions a Grafana endpoint requires, the README's "Tools"
// table is the secondary reference (kept in sync with this file). When the
// README table and this file disagree, this file wins (it's executable).
var ToolGates = map[string]ToolGate{
	// --- Search ---
	"search_dashboards": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},
	"search_folders": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Dashboard ---
	"get_dashboard_by_uid": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},
	"get_dashboard_summary": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},
	"get_dashboard_property": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},
	"get_dashboard_panel_queries": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},
	"update_dashboard": {
		Permissions: []Permission{
			{Action: "dashboards:create", Scope: "dashboards:*"},
			{Action: "dashboards:write", Scope: "dashboards:*"},
		},
		MinBasicRole: "Editor",
	},

	// --- Datasources ---
	"list_datasources": {
		Permissions:  []Permission{{Action: "datasources:read", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"get_datasource": {
		Permissions:  []Permission{{Action: "datasources:read", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Prometheus ---
	"query_prometheus": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_prometheus_metric_metadata": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_prometheus_metric_names": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_prometheus_label_names": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_prometheus_label_values": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_prometheus_histogram": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Loki ---
	"query_loki_logs": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_loki_label_names": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_loki_label_values": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_loki_stats": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_loki_patterns": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- InfluxDB ---
	"query_influxdb": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- ClickHouse ---
	"list_clickhouse_tables": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"describe_clickhouse_table": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_clickhouse": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- CloudWatch ---
	"list_cloudwatch_namespaces": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_cloudwatch_metrics": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_cloudwatch_dimensions": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_cloudwatch": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Elasticsearch / OpenSearch ---
	"query_elasticsearch": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Snowflake ---
	"list_snowflake_tables": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"describe_snowflake_table": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_snowflake": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Graphite ---
	"query_graphite": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_graphite_metrics": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_graphite_tags": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_graphite_density": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Pyroscope ---
	"list_pyroscope_label_names": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_pyroscope_label_values": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"list_pyroscope_profile_types": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},
	"query_pyroscope": {
		Permissions:  []Permission{{Action: "datasources:query", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- RunPanelQuery ---
	"run_panel_query": {
		Permissions: []Permission{
			{Action: "dashboards:read", Scope: "dashboards:*"},
			{Action: "datasources:query", Scope: "datasources:*"},
		},
		MinBasicRole: "Viewer",
	},

	// --- Examples ---
	"get_query_examples": {
		Permissions:  []Permission{{Action: "datasources:read", Scope: "datasources:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Alerting ---
	"alerting_manage_rules": {
		// Read is always required; write actions are checked at call time
		// against the alert.rules:write permission. We gate visibility on read.
		Permissions:  []Permission{{Action: "alert.rules:read"}},
		MinBasicRole: "Viewer",
	},
	"alerting_manage_routing": {
		Permissions:  []Permission{{Action: "alert.notifications:read"}},
		MinBasicRole: "Viewer",
	},

	// --- Annotations ---
	"get_annotations": {
		Permissions:  []Permission{{Action: "annotations:read"}},
		MinBasicRole: "Viewer",
	},
	"create_annotation": {
		Permissions:  []Permission{{Action: "annotations:write"}},
		MinBasicRole: "Editor",
	},
	"update_annotation": {
		Permissions:  []Permission{{Action: "annotations:write"}},
		MinBasicRole: "Editor",
	},
	"get_annotation_tags": {
		Permissions:  []Permission{{Action: "annotations:read"}},
		MinBasicRole: "Viewer",
	},

	// --- Rendering ---
	"get_panel_image": {
		Permissions:  []Permission{{Action: "dashboards:read", Scope: "dashboards:*"}},
		MinBasicRole: "Viewer",
	},

	// --- Admin ---
	"list_teams":               {Permissions: []Permission{{Action: "teams:read"}}, MinBasicRole: "Admin"},
	"list_users_by_org":        {Permissions: []Permission{{Action: "users:read"}}, MinBasicRole: "Admin"},
	"list_all_roles":           {Permissions: []Permission{{Action: "roles:read"}}, MinBasicRole: "Admin"},
	"get_role_details":         {Permissions: []Permission{{Action: "roles:read"}}, MinBasicRole: "Admin"},
	"get_role_assignments":     {Permissions: []Permission{{Action: "roles:read"}}, MinBasicRole: "Admin"},
	"list_user_roles":          {Permissions: []Permission{{Action: "roles:read"}}, MinBasicRole: "Admin"},
	"list_team_roles":          {Permissions: []Permission{{Action: "roles:read"}}, MinBasicRole: "Admin"},
	"get_resource_permissions": {Permissions: []Permission{{Action: "permissions:read"}}, MinBasicRole: "Admin"},
	"get_resource_description": {Permissions: []Permission{{Action: "permissions:read"}}, MinBasicRole: "Admin"},

	// --- Plugin ---
	"search_plugin_information": {Permissions: []Permission{{Action: "plugins:read"}}, MinBasicRole: "Admin"},
	"get_plugin":                {Permissions: []Permission{{Action: "plugins:read"}}, MinBasicRole: "Admin"},
	"install_plugin":            {Permissions: []Permission{{Action: "plugins:install"}}, MinBasicRole: "Admin"},

	// --- Folder ---
	"create_folder": {Permissions: []Permission{{Action: "folders:create"}}, MinBasicRole: "Editor"},

	// --- Incident (Grafana Incident; basic-role only, no fine-grained RBAC) ---
	"list_incidents":           {MinBasicRole: "Viewer"},
	"create_incident":          {MinBasicRole: "Editor"},
	"add_activity_to_incident": {MinBasicRole: "Editor"},
	"get_incident":             {MinBasicRole: "Viewer"},

	// --- Sift (basic-role only) ---
	"get_sift_investigation":   {MinBasicRole: "Viewer"},
	"get_sift_analysis":        {MinBasicRole: "Viewer"},
	"list_sift_investigations": {MinBasicRole: "Viewer"},
	"find_error_pattern_logs":  {MinBasicRole: "Editor"},
	"find_slow_requests":       {MinBasicRole: "Editor"},

	// --- OnCall (uses plugin-specific actions; treat them as their own namespace) ---
	"list_oncall_schedules":    {Permissions: []Permission{{Action: "grafana-oncall-app.schedules:read"}}},
	"get_oncall_shift":         {Permissions: []Permission{{Action: "grafana-oncall-app.schedules:read"}}},
	"get_current_oncall_users": {Permissions: []Permission{{Action: "grafana-oncall-app.schedules:read"}}},
	"list_oncall_teams":        {Permissions: []Permission{{Action: "grafana-oncall-app.user-settings:read"}}},
	"list_oncall_users":        {Permissions: []Permission{{Action: "grafana-oncall-app.user-settings:read"}}},
	"list_alert_groups":        {Permissions: []Permission{{Action: "grafana-oncall-app.alert-groups:read"}}},
	"get_alert_group":          {Permissions: []Permission{{Action: "grafana-oncall-app.alert-groups:read"}}},

	// --- Asserts (plugin; permissions are plugin-specific; gate behind plugin existence) ---
	"get_assertions": {}, // public — Grafana enforces plugin permissions

	// --- Navigation (no Grafana-side auth) ---
	"generate_deeplink": {}, // public

	// --- API (raw HTTP passthrough; whatever the user has on the underlying endpoint) ---
	// Both the read-only and write variants register under the same name "grafana_api_request";
	// visibility is public. Grafana itself enforces RBAC on the underlying endpoint.
	"grafana_api_request": {}, // public — underlying endpoint enforces RBAC
}
