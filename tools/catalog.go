package tools

import (
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

type gate uint8

const (
	gateWrite gate = 1 << iota
	gateQuery
)

type variant struct {
	tool     mcpgrafana.Tool
	gate     gate
	liftedBy []string
}

type kind uint8

const (
	listed kind = iota
	appSource
	appSourceOnly
	appViewer
)

type toolSpec struct {
	kind     kind
	variants []variant
}

type category struct {
	name               string
	description        string
	reducedDescription string
	tools              []toolSpec
}

func gated(t mcpgrafana.Tool, g gate) toolSpec {
	return toolSpec{kind: listed, variants: []variant{{tool: t, gate: g, liftedBy: []string{t.Tool.Name}}}}
}

func read(t mcpgrafana.Tool) toolSpec       { return gated(t, 0) }
func write(t mcpgrafana.Tool) toolSpec      { return gated(t, gateWrite) }
func query(t mcpgrafana.Tool) toolSpec      { return gated(t, gateQuery) }
func writeQuery(t mcpgrafana.Tool) toolSpec { return gated(t, gateWrite|gateQuery) }

func withFallbacks(specs ...toolSpec) toolSpec {
	var merged toolSpec
	for _, s := range specs {
		merged.variants = append(merged.variants, s.variants...)
	}
	return merged
}

func liftedBy(s toolSpec, names ...string) toolSpec {
	s.variants[0].liftedBy = names
	return s
}

func asAppSource(s toolSpec) toolSpec {
	s.kind = appSource
	return s
}

func asAppSourceOnly(s toolSpec) toolSpec {
	s.kind = appSourceOnly
	return s
}

var catalog = []category{
	{name: "search", description: "Search: Find dashboards, folders, and other Grafana resources.", tools: []toolSpec{
		read(SearchDashboards), read(SearchFolders),
	}},
	{name: "datasource", description: "Datasources: List and fetch details for datasources.", tools: []toolSpec{
		read(ListDatasources), read(GetDatasource), read(CheckDatasourcesHealth),
		write(CreateDatasource), write(UpdateDatasource),
	}},
	{name: "incident", description: "Incidents: Search, create, update, and resolve incidents in Grafana Incident.", tools: []toolSpec{
		read(ListIncidents), read(GetIncident), read(ListIncidentCustomFields),
		write(CreateIncident), write(AddActivityToIncident), write(UpdateIncident),
	}},
	{
		name:               "prometheus",
		description:        "Prometheus: Run PromQL queries, retrieve metric metadata, and explore label names/values.",
		reducedDescription: "Prometheus: Retrieve metric metadata and explore metric names and label names/values. Query execution is disabled.",
		tools: []toolSpec{
			read(ListPrometheusMetricMetadata), read(ListPrometheusMetricNames), read(ListPrometheusLabelNames), read(ListPrometheusLabelValues),
			asAppSource(query(QueryPrometheus)), query(QueryPrometheusHistogram),
		},
	},
	{
		name:               "loki",
		description:        "Loki: Run LogQL queries, retrieve log metadata, and explore label names/values.",
		reducedDescription: "Loki: Retrieve log metadata and index stats, explore label names/values, and audit label strategy. Log query execution is disabled.",
		tools: []toolSpec{
			read(ListLokiLabelNames), read(ListLokiLabelValues), read(QueryLokiStats), read(AnalyzeLokiLabels),
			query(QueryLokiLogs), query(QueryLokiPatterns),
		},
	},
	{name: "elasticsearch", description: "Elasticsearch and OpenSearch: Query Elasticsearch and OpenSearch datasources using Lucene syntax or Query DSL for logs and metrics.", tools: []toolSpec{
		query(QueryElasticsearch),
	}},
	{name: "quickwit", description: "Quickwit: Query Quickwit datasources using Lucene syntax or Query DSL for logs and documents.", tools: []toolSpec{
		query(QueryQuickwit),
	}},
	{name: "influxdb", description: "InfluxDB: Query InfluxDB datasources.", tools: []toolSpec{
		writeQuery(QueryInfluxDB),
	}},
	{name: "alerting", description: "Alerting: List and fetch alert rules and notification contact points.", tools: []toolSpec{
		read(AlertRulesRead), read(ManageRouting), read(AlertSilencesRead),
		write(AlertRulesWrite), write(AlertRoutingWrite), write(AlertSilencesWrite),
	}},
	{name: "dashboard", description: "Dashboards: Search, retrieve, update, and create dashboards. Extract panel queries and datasource information.", tools: []toolSpec{
		read(GetDashboardByUID), read(ListDashboardVersions), read(GetDashboardPanelQueries), read(GetDashboardProperty), read(GetDashboardSummary),
		write(UpdateDashboard),
	}},
	{name: "folder", description: "Folders: Manage dashboard folders.", tools: []toolSpec{
		write(CreateFolder),
	}},
	{name: "oncall", description: "OnCall: View and manage on-call schedules, shifts, teams, and users.", tools: []toolSpec{
		read(ListOnCallSchedules), read(GetOnCallShift), read(GetCurrentOnCallUsers), read(ListOnCallTeams), read(ListOnCallUsers), read(ListAlertGroups), read(GetAlertGroup),
		write(UpdateAlertGroup),
	}},
	{name: "asserts", description: "Asserts: Query and analyze assertion data.", tools: []toolSpec{
		read(GetAssertions),
	}},
	{name: "admin", description: "Admin: List teams and perform administrative tasks.", tools: []toolSpec{
		read(ListTeams), read(ListUsersByOrg), read(ListAllRoles), read(GetRoleDetails), read(GetRoleAssignments),
		read(ListUserRoles), read(ListTeamRoles), read(GetResourcePermissions), read(GetResourceDescription),
	}},
	{
		name:               "pyroscope",
		description:        "Pyroscope: Profile applications and fetch profiling data.",
		reducedDescription: "Pyroscope: Explore profile types and label names/values. Query execution is disabled.",
		tools: []toolSpec{
			read(ListPyroscopeLabelNames), read(ListPyroscopeLabelValues), read(ListPyroscopeProfileTypes),
			query(QueryPyroscope),
		},
	},
	{
		name:               "navigation",
		description:        "Navigation: Generate deeplink URLs for Grafana resources like dashboards, panels, and Explore queries, with optional built-in shortening.",
		reducedDescription: "Navigation: Generate deeplink URLs for Grafana resources like dashboards, panels, and Explore queries.",
		tools: []toolSpec{
			withFallbacks(write(GenerateDeeplink), read(GenerateDeeplinkReadOnly)),
		},
	},
	{name: "tempo", description: "Tempo: Search traces with TraceQL, compute trace-derived metrics, fetch and diff traces, and explore trace attributes.", tools: []toolSpec{
		query(SearchTempoTracesTool), query(QueryTempoMetricsTool), asAppSource(query(GetTempoTraceTool)), query(DiffTempoTracesTool),
		query(ListTempoAttributeNamesTool), query(ListTempoAttributeValuesTool), query(GetTempoTraceQLDocsTool),
	}},
	{name: "annotations", description: "Annotations: Create and manage dashboard annotations.", tools: []toolSpec{
		read(GetAnnotationsTool), read(GetAnnotationTagsTool),
		write(CreateAnnotationTool), write(UpdateAnnotationTool), write(DeleteAnnotationTool),
	}},
	{name: "rendering", description: "Rendering: Export dashboard panels or full dashboards as PNG images (requires Grafana Image Renderer plugin).", tools: []toolSpec{
		read(GetPanelImage), {kind: appViewer},
	}},
	{name: "snapshot", description: "Snapshots: List, get, create, and delete dashboard snapshots.", tools: []toolSpec{
		read(ListSnapshotsTool), read(GetSnapshotTool),
		write(CreateSnapshotTool), write(DeleteSnapshotTool),
	}},
	{
		name:               "cloudwatch",
		description:        "CloudWatch: Query AWS CloudWatch datasources for metrics and logs.",
		reducedDescription: "CloudWatch: List AWS CloudWatch namespaces, metrics, dimensions, and log groups. Query execution is disabled.",
		tools: []toolSpec{
			read(ListCloudWatchNamespaces), read(ListCloudWatchMetrics), read(ListCloudWatchDimensions), read(ListCloudWatchDimensionValues),
			read(ListCloudWatchLogGroups), read(ListCloudWatchLogGroupFields),
			query(QueryCloudWatch), query(QueryCloudWatchLogs),
		},
	},
	{
		name:               "cloudlogging",
		description:        "Google Cloud Logging: Query logs with the Cloud Logging query language and list projects, log buckets, and views.",
		reducedDescription: "Google Cloud Logging: List GCP projects, log buckets, and views. Query execution is disabled.",
		tools: []toolSpec{
			read(ListCloudLoggingProjects), read(ListCloudLoggingBuckets), read(ListCloudLoggingViews),
			query(QueryCloudLogging),
		},
	},
	{name: "azuremonitor", description: "Azure Monitor: Query Azure Log Analytics workspaces and Azure Resource Graph with KQL.", tools: []toolSpec{
		query(QueryAzureLogAnalytics), query(QueryAzureResourceGraph),
	}},
	{name: "examples", description: "Examples: Query example tools.", tools: []toolSpec{
		read(GetQueryExamples),
	}},
	{
		name:               "sql",
		description:        "SQL: Query supported SQL datasources (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL, BigQuery) via Grafana with macro substitution, schema discovery, and variable support.",
		reducedDescription: "SQL: List tables and describe table schemas in SQL datasources (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL, BigQuery). Query execution is disabled.",
		tools: []toolSpec{
			read(ListSQLDatabases), read(ListSQLTables), read(DescribeSQLTable),
			writeQuery(QuerySQL),
		},
	},
	{name: "runpanelquery", description: "Run Panel Query: Execute panel queries directly.", tools: []toolSpec{
		query(RunPanelQuery),
	}},
	{
		name:               "graphite",
		description:        "Graphite: Query Graphite datasources for metrics.",
		reducedDescription: "Graphite: List Graphite metrics and tags. Query execution is disabled.",
		tools: []toolSpec{
			read(ListGraphiteMetrics), read(ListGraphiteTags),
			query(QueryGraphite), query(QueryGraphiteDensity),
		},
	},
	{name: "dsquery", description: "Data source queries: Chart the queries of any data source with render_grafana_app (query_datasource).", tools: []toolSpec{
		asAppSourceOnly(writeQuery(QueryDatasource)),
	}},
	{name: "plugin", description: "Plugins: Check whether Grafana plugins are installed and fetch plugin details.", tools: []toolSpec{
		read(SearchPlugins), read(GetPlugin),
		write(InstallPlugin),
	}},
	{name: "api", description: "API: Make authenticated HTTP requests to any Grafana API endpoint with optional jq-style response filtering.", tools: []toolSpec{
		withFallbacks(write(APIRequest), liftedBy(writeQuery(APIRequestReadOnly), QuerySQL.Tool.Name, QueryInfluxDB.Tool.Name), read(APIRequestReadOnlyGetOnly)),
	}},
	{name: "config", description: "Config: Generate operator-facing configuration snippets (e.g. Alloy label-enforcement pipelines).", tools: []toolSpec{
		read(SuggestLokiAlloyLabelConfig),
	}},
	{name: "provisioning", description: "Provisioning: List provisioning repositories (e.g. git-sync sources) to discover repository slugs for use with rendering tools.", tools: []toolSpec{
		read(ListProvisioningRepositories), read(ValidateProvisioningFile),
	}},
	{name: "agento11y", description: "Agent Observability: Search and inspect LLM conversations, generations, and evaluation scores from Grafana Agent Observability. Read its agent catalog (system prompts, tools, version history, and per-version scores). Read or manage its eval configuration (evaluators, templates, eval rules, and guards), its curated saved conversations and collections, its offline experiments with their trials and scores, and the versioned test suites those experiments run against.", tools: []toolSpec{
		read(ManageAgento11yConversations), read(ManageAgento11yGenerations), read(ManageAgento11yAgents),
		withFallbacks(write(ManageAgento11yEvaluatorsReadWrite), read(ManageAgento11yEvaluatorsRead)),
		withFallbacks(write(ManageAgento11yEvalRulesReadWrite), read(ManageAgento11yEvalRulesRead)),
		withFallbacks(write(ManageAgento11yEvalCollectionsReadWrite), read(ManageAgento11yEvalCollectionsRead)),
		withFallbacks(write(ManageAgento11yExperimentsReadWrite), read(ManageAgento11yExperimentsRead)),
		withFallbacks(write(ManageAgento11yTestSuitesReadWrite), read(ManageAgento11yTestSuitesRead)),
	}},
	{name: "assistant", description: "Assistant: Ask Grafana Assistant open-ended questions and get a full text reply (requires the Grafana Assistant plugin).", tools: []toolSpec{
		write(AskAssistant),
	}},
	{name: "docs", description: "Docs: Search and retrieve Grafana product documentation (powered by grafana.com/llms-full.txt).", tools: []toolSpec{
		read(SearchDocsTool), read(GetDocTool),
	}},
	{name: "user", description: "User: Identify the current user/credential, its capabilities, and the organizations it can access.", tools: []toolSpec{
		read(UserInfoTool),
	}},
}

// CategoryNames lists every tool category, in the order the server
// instructions list them.
func CategoryNames() []string {
	names := make([]string, 0, len(catalog))
	for _, c := range catalog {
		names = append(names, c.name)
	}
	return names
}

// MutatingQueryToolNames lists the tools that pass a query to the datasource
// unfiltered, so they can write whatever the datasource credentials allow.
func MutatingQueryToolNames() []string {
	var names []string
	for _, c := range catalog {
		for _, spec := range c.tools {
			if len(spec.variants) > 0 && spec.variants[0].gate == gateWrite|gateQuery {
				names = append(names, spec.variants[0].tool.Tool.Name)
			}
		}
	}
	return names
}
