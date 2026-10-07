package tools

// RequiredPermissions maps each tool name to the Grafana RBAC actions its
// calls need. A caller missing any of them gets a 403 from that tool.
//
// Every registered tool must have an entry, including tools that need no
// RBAC action (an empty list): TestRequiredPermissions_CoversEveryTool in
// cmd/mcp-grafana fails otherwise. Declare the actions for every operation a
// tool can perform, including write operations behind a read-write variant.
var RequiredPermissions = map[string][]string{
	// Admin
	"list_teams":               {"teams:read"},
	"list_users_by_org":        {"org.users:read"},
	"list_all_roles":           {"roles:read"},
	"get_role_details":         {"roles:read"},
	"get_role_assignments":     {"roles:read", "users.roles:read", "teams.roles:read"},
	"list_user_roles":          {"users.roles:read"},
	"list_team_roles":          {"teams.roles:read"},
	"get_resource_permissions": resourcePermissionsRead,
	"get_resource_description": resourcePermissionsRead,
	"user_info":                {},

	// Dashboards and folders
	"search_dashboards":           {"dashboards:read", "folders:read"},
	"get_dashboard_by_uid":        {"dashboards:read"},
	"get_dashboard_summary":       {"dashboards:read"},
	"get_dashboard_property":      {"dashboards:read"},
	"get_dashboard_panel_queries": {"dashboards:read"},
	"list_dashboard_versions":     {"dashboards:read"},
	"update_dashboard":            {"dashboards:read", "dashboards:create", "dashboards:write", "folders:read"},
	"search_folders":              {"folders:read"},
	"create_folder":               {"folders:read", "folders:create", "folders:write"},
	"run_panel_query":             {"dashboards:read", "datasources:read", "datasources:query"},
	"get_panel_image":             {"dashboards:read", "datasources:query"},
	"generate_deeplink":           {}, // short URLs need no RBAC action

	// Snapshots
	"list_snapshots":  {"snapshots:read"},
	"get_snapshot":    {"snapshots:read"},
	"create_snapshot": {"snapshots:create"},
	"delete_snapshot": {"snapshots:delete"},

	// Annotations
	"get_annotations":     {"annotations:read"},
	"get_annotation_tags": {"annotations:read"},
	"create_annotation":   {"annotations:create"},
	"update_annotation":   {"annotations:write"},
	"delete_annotation":   {"annotations:delete"},

	// Datasources
	"list_datasources":            {"datasources:read"},
	"get_datasource":              {"datasources:read"},
	"check_datasources_health":    datasourceQuery,
	"create_datasource":           {"datasources:read", "datasources:create"},
	"update_datasource":           {"datasources:read", "datasources:write"},
	"get_query_examples":          {},
	"list_cloud_logging_projects": datasourceQuery,
	"list_cloud_logging_buckets":  datasourceQuery,
	"list_cloud_logging_views":    datasourceQuery,
	"query_cloud_logging":         datasourceQuery,

	// Prometheus
	"list_prometheus_metric_names":    datasourceQuery,
	"list_prometheus_metric_metadata": datasourceQuery,
	"list_prometheus_label_names":     datasourceQuery,
	"list_prometheus_label_values":    datasourceQuery,
	"query_prometheus":                datasourceQuery,
	"query_prometheus_histogram":      datasourceQuery,

	// Loki
	"list_loki_label_names":           datasourceQuery,
	"list_loki_label_values":          datasourceQuery,
	"query_loki_logs":                 datasourceQuery,
	"query_loki_patterns":             datasourceQuery,
	"query_loki_stats":                datasourceQuery,
	"analyze_loki_labels":             datasourceQuery,
	"suggest_loki_alloy_label_config": {},

	// Tempo
	"list_tempo_attribute_names":  datasourceQuery,
	"list_tempo_attribute_values": datasourceQuery,
	"search_tempo_traces":         datasourceQuery,
	"get_tempo_trace":             datasourceQuery,
	"diff_tempo_traces":           datasourceQuery,
	"query_tempo_metrics":         datasourceQuery,
	"get_tempo_traceql_docs":      {}, // embedded docs

	// Pyroscope
	"list_pyroscope_label_names":   datasourceQuery,
	"list_pyroscope_label_values":  datasourceQuery,
	"list_pyroscope_profile_types": datasourceQuery,
	"query_pyroscope":              datasourceQuery,

	// CloudWatch
	"list_cloudwatch_namespaces":       datasourceQuery,
	"list_cloudwatch_metrics":          datasourceQuery,
	"list_cloudwatch_dimensions":       datasourceQuery,
	"list_cloudwatch_dimension_values": datasourceQuery,
	"query_cloudwatch":                 datasourceQuery,

	// Graphite, InfluxDB, Elasticsearch, Quickwit, SQL
	"list_graphite_metrics":  datasourceQuery,
	"list_graphite_tags":     datasourceQuery,
	"query_graphite":         datasourceQuery,
	"query_graphite_density": datasourceQuery,
	"query_influxdb":         datasourceQuery,
	"query_elasticsearch":    datasourceQuery,
	"query_quickwit":         datasourceQuery,
	"list_sql_databases":     datasourceQuery,
	"list_sql_tables":        datasourceQuery,
	"describe_sql_table":     datasourceQuery,
	"query_sql":              datasourceQuery,

	// Alerting
	"alerting_rules_read": {"alert.rules:read", "alert.rules.external:read", "folders:read"},
	"alerting_rules_write": {
		"alert.rules:read", "alert.rules:create", "alert.rules:write", "alert.rules:delete",
		"alert.provisioning.provenance:write", "folders:read",
	},
	"alerting_silences_read": {"alert.silences:read", "alert.instances:read"},
	// General (not rule-scoped) silences are gated on alert.instances:*, not
	// alert.silences:*.
	"alerting_silences_write": {
		"alert.silences:read", "alert.silences:create", "alert.silences:write",
		"alert.instances:read", "alert.instances:create", "alert.instances:write",
	},
	"alerting_manage_routing": {"alert.notifications:read", "alert.notifications.external:read"},
	// POST /api/v1/provisioning/contact-points: receivers:create plus
	// provenance:write.
	"alerting_routing_write": {"alert.notifications.receivers:create", "alert.provisioning.provenance:write"},

	// Incidents
	"list_incidents":              incidentRead,
	"get_incident":                incidentRead,
	"list_incident_custom_fields": incidentRead,
	"create_incident":             incidentWrite,
	"update_incident":             incidentWrite,
	"add_activity_to_incident":    incidentWrite,

	// OnCall / IRM
	"list_oncall_schedules":    {"plugins.app:access", "grafana-irm-app.schedules:read"},
	"get_oncall_shift":         {"plugins.app:access", "grafana-irm-app.schedules:read"},
	"get_current_oncall_users": {"plugins.app:access", "grafana-irm-app.schedules:read", "grafana-irm-app.user-settings:read"},
	"list_oncall_teams":        {"plugins.app:access", "grafana-irm-app.user-settings:read"},
	"list_oncall_users":        {"plugins.app:access", "grafana-irm-app.user-settings:read"},
	"list_alert_groups":        {"plugins.app:access", "grafana-irm-app.alert-groups:read"},
	"get_alert_group":          {"plugins.app:access", "grafana-irm-app.alert-groups:read"},
	"update_alert_group":       {"plugins.app:access", "grafana-irm-app.alert-groups:read", "grafana-irm-app.alert-groups:write"},

	// Agent Observability. The manage_* tools register one name for read-only
	// and read-write variants, so they list the write action too.
	"agento11y_manage_agents":           agento11yRead,
	"agento11y_manage_conversations":    agento11yRead,
	"agento11y_manage_generations":      agento11yRead,
	"agento11y_manage_eval_collections": agento11yWrite,
	"agento11y_manage_eval_rules":       agento11yWrite,
	"agento11y_manage_evaluators":       agento11yWrite,
	"agento11y_manage_experiments":      agento11yWrite,
	"agento11y_manage_test_suites":      agento11yWrite,

	// Plugin-backed tools
	"ask_assistant":  {"plugins.app:access"},
	"get_assertions": {"plugins.app:access"},

	// Plugins
	"get_plugin":                {},
	"install_plugin":            {"plugins:install"},
	"search_plugin_information": {}, // grafana.com catalog

	// Provisioning (Kubernetes-style resource actions)
	"list_provisioning_repositories": {"provisioning.grafana.app/repositories:list"},
	"validate_provisioning_file":     {"provisioning.grafana.app/repositories:get"},

	// Docs (grafana.com)
	"search_docs": {},
	"get_doc":     {},

	// grafana_api_request calls arbitrary endpoints with whatever the token
	// holds; it needs no fixed action of its own.
	"grafana_api_request": {},
}

var (
	datasourceQuery = []string{"datasources:read", "datasources:query"}

	resourcePermissionsRead = []string{
		"dashboards.permissions:read",
		"datasources.permissions:read",
		"folders.permissions:read",
		"serviceaccounts.permissions:read",
		"teams.permissions:read",
		"users.permissions:read",
	}

	incidentRead  = []string{"plugins.app:access", "grafana-incident-app.incidents:read"}
	incidentWrite = []string{"plugins.app:access", "grafana-incident-app.incidents:read", "grafana-incident-app.incidents:write"}

	agento11yRead = []string{
		"plugins.app:access",
		"grafana-agento11y-app.conversations:read",
		"grafana-agento11y-app.data:read",
	}
	agento11yWrite = append(append([]string{}, agento11yRead...), "grafana-agento11y-app.eval:write")
)
