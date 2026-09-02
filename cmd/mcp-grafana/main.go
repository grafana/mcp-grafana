package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/observability"
	"github.com/grafana/mcp-grafana/tools"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
)

const defaultServerName = "mcp-grafana"

var serverNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validateServerName(name string) error {
	if name == "" {
		return fmt.Errorf("server name must not be empty; expected 1–128 characters matching %s", serverNamePattern)
	}
	if len(name) > 128 {
		return fmt.Errorf("server name %q is too long (%d characters); maximum is 128", name, len(name))
	}
	if !serverNamePattern.MatchString(name) {
		return fmt.Errorf("server name %q contains invalid characters; must match %s (start with alphanumeric, then alphanumerics, dots, hyphens, or underscores)", name, serverNamePattern)
	}
	return nil
}

func resolveServerName(flagValue string, flagExplicitlySet bool, envValue, defaultValue string) string {
	if flagExplicitlySet {
		return flagValue
	}
	if envValue != "" {
		return envValue
	}
	return defaultValue
}

func maybeAddTools(s *server.MCPServer, tf func(*server.MCPServer), enabledTools []string, disable bool, category string) {
	if !slices.Contains(enabledTools, category) {
		slog.Debug("Not enabling tools", "category", category)
		return
	}
	if disable {
		slog.Info("Disabling tools", "category", category)
		return
	}
	slog.Debug("Enabling tools", "category", category)
	tf(s)
}

// isCategoryEnabled reports whether a tool category is active given the
// enabled-tools list and the per-category disable flag.
func isCategoryEnabled(enabledTools []string, disabled bool, category string) bool {
	return slices.Contains(enabledTools, category) && !disabled
}

// categoryDescription maps a tool category to the description shown in server instructions.
var categoryDescription = map[string]string{
	"search":        "Search: Find dashboards, folders, and other Grafana resources.",
	"datasource":    "Datasources: List and fetch details for datasources.",
	"incident":      "Incidents: Search, create, update, and resolve incidents in Grafana Incident.",
	"prometheus":    "Prometheus: Run PromQL queries, retrieve metric metadata, and explore label names/values.",
	"loki":          "Loki: Run LogQL queries, retrieve log metadata, and explore label names/values.",
	"elasticsearch": "Elasticsearch and OpenSearch: Query Elasticsearch and OpenSearch datasources using Lucene syntax or Query DSL for logs and metrics.",
	"quickwit":      "Quickwit: Query Quickwit datasources using Lucene syntax or Query DSL for logs and documents.",
	"influxdb":      "InfluxDB: Query InfluxDB datasources.",
	"alerting":      "Alerting: List and fetch alert rules and notification contact points.",
	"dashboard":     "Dashboards: Search, retrieve, update, and create dashboards. Extract panel queries and datasource information.",
	"folder":        "Folders: Manage dashboard folders.",
	"oncall":        "OnCall: View and manage on-call schedules, shifts, teams, and users.",
	"asserts":       "Asserts: Query and analyze assertion data.",
	"sift":          "Sift Investigations: Start and manage Sift investigations, analyze logs/traces, find error patterns, and detect slow requests.",
	"admin":         "Admin: List teams and perform administrative tasks.",
	"pyroscope":     "Pyroscope: Profile applications and fetch profiling data.",
	"navigation":    "Navigation: Generate deeplink URLs for Grafana resources like dashboards, panels, and Explore queries, with optional built-in shortening.",
	"annotations":   "Annotations: Create and manage dashboard annotations.",
	"rendering":     "Rendering: Export dashboard panels or full dashboards as PNG images (requires Grafana Image Renderer plugin).",
	"snapshot":      "Snapshots: List, get, create, and delete dashboard snapshots.",
	"plugin":        "Plugins: Check whether Grafana plugins are installed and fetch plugin details.",
	"cloudwatch":    "CloudWatch: Query AWS CloudWatch datasources for metrics and logs.",
	"examples":      "Examples: Query example tools.",
	"clickhouse":    "ClickHouse: Query ClickHouse datasources via Grafana with macro and variable substitution support.",
	"snowflake":     "Snowflake: Query Snowflake datasources via Grafana (including the SNOWFLAKE.TELEMETRY.EVENTS event table) with macro and variable substitution support.",
	"runpanelquery": "Run Panel Query: Execute panel queries directly.",
	"graphite":      "Graphite: Query Graphite datasources for metrics.",
	"athena":        "Athena: Query Amazon Athena datasources via Grafana with SQL, macro substitution, and schema discovery.",
	"api":           "API: Make authenticated HTTP requests to any Grafana API endpoint with optional jq-style response filtering.",
	"config":        "Config: Generate operator-facing configuration snippets (e.g. Alloy label-enforcement pipelines).",
	"provisioning":  "Provisioning: List provisioning repositories (e.g. git-sync sources) to discover repository slugs for use with rendering tools.",
	"agento11y":     "Agent Observability: Search and inspect LLM conversations, generations, and evaluation scores from Grafana Agent Observability. Read its agent catalog (system prompts, tools, version history, and per-version scores). Read or manage its eval configuration (evaluators, templates, eval rules, and guards), its curated saved conversations and collections, its offline experiments with their trials and scores, and the versioned test suites those experiments run against.",
	"assistant":     "Assistant: Ask Grafana Assistant open-ended questions and get a full text reply (requires the Grafana Assistant plugin).",
	"docs":          "Docs: Search and retrieve Grafana product documentation (powered by grafana.com/llms-full.txt).",
	"user":          "User: Identify the current user/credential, its capabilities, and the organizations it can access.",
}

// categoryDescriptionNoQuery replaces categoryDescription for categories that
// keep some tools when --disable-query is set. Advertising "run PromQL queries"
// when query_prometheus is not registered sends the model after a tool that
// isn't there, so these say what the category can still do.
var categoryDescriptionNoQuery = map[string]string{
	"prometheus": "Prometheus: Retrieve metric metadata and explore metric names and label names/values. Query execution is disabled.",
	"loki":       "Loki: Retrieve log metadata and index stats, explore label names/values, and audit label strategy. Log query execution is disabled.",
	"pyroscope":  "Pyroscope: Explore profile types and label names/values. Query execution is disabled.",
	"cloudwatch": "CloudWatch: List AWS CloudWatch namespaces, metrics, and dimensions. Query execution is disabled.",
	"clickhouse": "ClickHouse: List tables and describe table schemas in ClickHouse datasources. Query execution is disabled.",
	"snowflake":  "Snowflake: List tables and describe table schemas in Snowflake datasources. Query execution is disabled.",
	"graphite":   "Graphite: List Graphite metrics and tags. Query execution is disabled.",
	"athena":     "Athena: Discover catalogs, databases, tables, and table schemas in Amazon Athena datasources. Query execution is disabled.",
}

// queryOnlyCategories register no tools at all when their query tools are
// disabled, because every tool they contain executes a query.
var queryOnlyCategories = []string{"elasticsearch", "quickwit", "influxdb", "runpanelquery"}

// mutatingQueryCategories hold query tools that pass raw SQL or InfluxQL to the
// datasource unfiltered: query_clickhouse can run DROP TABLE, query_influxdb can
// run DELETE, and so on, whenever the datasource credentials permit it. They are
// query tools and write tools at once, so read-only mode removes them along with
// the rest of the write tools, and --enable-query puts them back for operators
// whose datasource credentials are known to be read-only.
var mutatingQueryCategories = []string{"clickhouse", "snowflake", "athena", "influxdb"}

// queryToolsEnabled reports whether a category's query tools should be
// registered. --disable-query turns off every query tool; --disable-write
// additionally turns off the ones that can mutate data, unless --enable-query
// overrides it.
func (dt *disabledTools) queryToolsEnabled(category string) bool {
	if dt.query {
		return false
	}
	if slices.Contains(mutatingQueryCategories, category) {
		return !dt.write || dt.enableQuery
	}
	return true
}

// disabledTools indicates whether each category of tools should be disabled.
type disabledTools struct {
	enabledTools string

	search, datasource, incident,
	prometheus, loki, elasticsearch, quickwit, influxdb, alerting,
	dashboard, folder, oncall, asserts, sift, admin,
	pyroscope, navigation, proxied, annotations, rendering, cloudwatch, write, query, enableQuery,
	snapshot, examples, clickhouse, snowflake, graphite,
	runpanelquery, athena, plugin, api, config, provisioning,
	agento11y, assistant, docs, user bool
}

// Configuration for the Grafana client.
type grafanaConfig struct {
	// Whether to enable debug mode for the Grafana transport.
	debug bool

	// TLS configuration
	tlsCertFile   string
	tlsKeyFile    string
	tlsCAFile     string
	tlsSkipVerify bool

	// Loki configuration
	maxLokiLogLimit int

	// Loki query cost guardrail configuration
	lokiGuardrailMode     string
	lokiGuardrailMaxBytes int64
	lokiGuardrailMaxRange time.Duration

	// includeArgsInSpans enables logging of tool arguments in OpenTelemetry spans.
	includeArgsInSpans bool

	// timeout is the time limit for requests made by the Grafana client.
	timeout time.Duration

	// dynamicMultiOrg allows tool calls to select a Grafana organization per
	// call via an optional orgId argument. Off by default; startup-time
	// multi-org (GRAFANA_ORG_ID / X-Grafana-Org-Id) is unaffected.
	dynamicMultiOrg bool
}

func (dt *disabledTools) addFlags() {
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering,snapshot,plugin,api,config,provisioning,docs,user", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")
	flag.BoolVar(&dt.search, "disable-search", false, "Disable search tools")
	flag.BoolVar(&dt.datasource, "disable-datasource", false, "Disable datasource tools")
	flag.BoolVar(&dt.incident, "disable-incident", false, "Disable incident tools")
	flag.BoolVar(&dt.prometheus, "disable-prometheus", false, "Disable prometheus tools")
	flag.BoolVar(&dt.loki, "disable-loki", false, "Disable loki tools")
	flag.BoolVar(&dt.elasticsearch, "disable-elasticsearch", false, "Disable elasticsearch and opensearch tools")
	flag.BoolVar(&dt.quickwit, "disable-quickwit", false, "Disable quickwit tools")
	flag.BoolVar(&dt.influxdb, "disable-influxdb", false, "Disable InfluxDB tools")
	flag.BoolVar(&dt.alerting, "disable-alerting", false, "Disable alerting tools")
	flag.BoolVar(&dt.dashboard, "disable-dashboard", false, "Disable dashboard tools")
	flag.BoolVar(&dt.folder, "disable-folder", false, "Disable folder tools")
	flag.BoolVar(&dt.oncall, "disable-oncall", false, "Disable oncall tools")
	flag.BoolVar(&dt.asserts, "disable-asserts", false, "Disable asserts tools")
	flag.BoolVar(&dt.sift, "disable-sift", false, "Disable sift tools")
	flag.BoolVar(&dt.admin, "disable-admin", false, "Disable admin tools")
	flag.BoolVar(&dt.pyroscope, "disable-pyroscope", false, "Disable pyroscope tools")
	flag.BoolVar(&dt.navigation, "disable-navigation", false, "Disable navigation tools")
	flag.BoolVar(&dt.proxied, "disable-proxied", false, "Disable proxied tools (tools from external MCP servers)")
	flag.BoolVar(&dt.write, "disable-write", false, "Disable write tools (create/update operations)")
	flag.BoolVar(&dt.query, "disable-query", false, "Disable query tools (tools that execute a query against a datasource, e.g. query_prometheus, query_loki_logs, run_panel_query). Metadata and discovery tools stay available.")
	flag.BoolVar(&dt.enableQuery, "enable-query", false, "Keep the raw-SQL query tools (query_clickhouse, query_snowflake, query_athena, query_influxdb) registered even under --disable-write. They pass the query through unfiltered, so they can mutate data if the datasource credentials permit it; use this when those credentials are known to be read-only. Has no effect if --disable-query is also set.")
	flag.BoolVar(&dt.annotations, "disable-annotations", false, "Disable annotation tools")
	flag.BoolVar(&dt.rendering, "disable-rendering", false, "Disable rendering tools (panel/dashboard image export)")
	flag.BoolVar(&dt.snapshot, "disable-snapshot", false, "Disable snapshot tools")
	flag.BoolVar(&dt.cloudwatch, "disable-cloudwatch", false, "Disable CloudWatch tools")
	flag.BoolVar(&dt.examples, "disable-examples", false, "Disable query examples tools")
	flag.BoolVar(&dt.clickhouse, "disable-clickhouse", false, "Disable ClickHouse tools")
	flag.BoolVar(&dt.snowflake, "disable-snowflake", false, "Disable Snowflake tools")
	flag.BoolVar(&dt.runpanelquery, "disable-runpanelquery", false, "Disable run panel query tools")
	flag.BoolVar(&dt.graphite, "disable-graphite", false, "Disable Graphite tools")
	flag.BoolVar(&dt.athena, "disable-athena", false, "Disable Athena tools")
	flag.BoolVar(&dt.plugin, "disable-plugin", false, "Disable plugin tools")
	flag.BoolVar(&dt.api, "disable-api", false, "Disable API tools")
	flag.BoolVar(&dt.config, "disable-config", false, "Disable config-generation tools")
	flag.BoolVar(&dt.provisioning, "disable-provisioning", false, "Disable provisioning tools")
	flag.BoolVar(&dt.agento11y, "disable-agento11y", false, "Disable Agent Observability tools")
	flag.BoolVar(&dt.assistant, "disable-assistant", false, "Disable Grafana Assistant tools")
	flag.BoolVar(&dt.docs, "disable-docs", false, "Disable documentation tools")
	flag.BoolVar(&dt.user, "disable-user", false, "Disable user info tools")
}

func (gc *grafanaConfig) addFlags() {
	flag.BoolVar(&gc.debug, "debug", false, "Enable debug mode for the Grafana transport")

	// TLS configuration flags
	flag.StringVar(&gc.tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file for client authentication")
	flag.StringVar(&gc.tlsKeyFile, "tls-key-file", "", "Path to TLS private key file for client authentication")
	flag.StringVar(&gc.tlsCAFile, "tls-ca-file", "", "Path to TLS CA certificate file for server verification")
	flag.BoolVar(&gc.tlsSkipVerify, "tls-skip-verify", false, "Skip TLS certificate verification (insecure)")

	// Loki configuration flags
	flag.IntVar(&gc.maxLokiLogLimit, "max-loki-log-limit", tools.MaxLokiLogLimit, "Maximum number of log lines returned per query_loki_logs call")

	// Loki query cost guardrail flags
	flag.StringVar(&gc.lokiGuardrailMode, "loki-guardrail-mode", mcpgrafana.LokiGuardrailOff, "Loki query cost guardrail mode for query_loki_logs: 'off' (default), 'shadow' (evaluate and log queries that would be blocked, but let them run; still pays the index/stats round trip), or 'enforce' (reject blocked queries with rewrite guidance). Falls back to the GRAFANA_LOKI_GUARDRAIL_MODE environment variable when the flag is not set.")
	flag.Int64Var(&gc.lokiGuardrailMaxBytes, "loki-guardrail-max-bytes", 100<<30, "Maximum bytes a single query_loki_logs call may scan, estimated via Loki's index/stats API before running the query. 0 disables the byte-budget check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_MAX_BYTES environment variable when the flag is not set.")
	flag.DurationVar(&gc.lokiGuardrailMaxRange, "loki-guardrail-max-range", 24*time.Hour, "Maximum effective time range for a single query_loki_logs call, including range-vector durations like [30d]. Accepts Go duration strings, e.g. 24h. 0 disables the range check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_MAX_RANGE environment variable when the flag is not set.")

	flag.BoolVar(&gc.includeArgsInSpans, "include-args-in-spans", false, "Include tool call arguments in OpenTelemetry spans. Only enable in non-production environments or when arguments are known not to contain PII.")
	flag.DurationVar(&gc.timeout, "grafana-timeout", mcpgrafana.DefaultGrafanaClientTimeout, "Time limit for requests made by the Grafana client. Accepts Go duration strings, e.g. 10s, 500ms.")

	// Multi-org: allow per-call org selection via an optional orgId argument.
	flag.BoolVar(&gc.dynamicMultiOrg, "dynamic-multi-org", false, "Allow tool calls to select a Grafana organization per call via an optional orgId argument (org is otherwise fixed at connection startup). Adds an orgId argument to every tool's schema.")
}

// applyLokiGuardrailEnv fills guardrail settings from GRAFANA_LOKI_GUARDRAIL_*
// environment variables for flags not set on the command line. Explicit flags
// win; the env fallback exists because container/sidecar deployments (the
// guardrail's main audience) configure via environment.
func (gc *grafanaConfig) applyLokiGuardrailEnv(setFlags map[string]bool) error {
	if v := os.Getenv("GRAFANA_LOKI_GUARDRAIL_MODE"); v != "" && !setFlags["loki-guardrail-mode"] {
		gc.lokiGuardrailMode = v
	}
	if v := os.Getenv("GRAFANA_LOKI_GUARDRAIL_MAX_BYTES"); v != "" && !setFlags["loki-guardrail-max-bytes"] {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_LOKI_GUARDRAIL_MAX_BYTES %q: %w", v, err)
		}
		gc.lokiGuardrailMaxBytes = n
	}
	if v := os.Getenv("GRAFANA_LOKI_GUARDRAIL_MAX_RANGE"); v != "" && !setFlags["loki-guardrail-max-range"] {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_LOKI_GUARDRAIL_MAX_RANGE %q: %w", v, err)
		}
		gc.lokiGuardrailMaxRange = d
	}
	return nil
}

// socks5ProxyFromEnv reads GRAFANA_SOCKS5_PROXY and validates it so a
// misconfigured proxy fails at startup instead of surfacing later when the
// first Grafana client is built. The error names the env var but never the
// raw value, which may contain proxy credentials. Extracted from main so the
// handling is unit-testable.
func socks5ProxyFromEnv() (string, error) {
	raw := os.Getenv("GRAFANA_SOCKS5_PROXY")
	if raw == "" {
		return "", nil
	}
	if err := mcpgrafana.ValidateSOCKS5ProxyURL(raw); err != nil {
		return "", fmt.Errorf("invalid GRAFANA_SOCKS5_PROXY: %w", err)
	}
	return raw, nil
}

// validateLokiGuardrail rejects invalid guardrail settings (unknown mode,
// negative limits) after flag and env processing. Extracted from main so the
// validation is unit-testable.
func (gc *grafanaConfig) validateLokiGuardrail() error {
	switch gc.lokiGuardrailMode {
	case mcpgrafana.LokiGuardrailOff, mcpgrafana.LokiGuardrailShadow, mcpgrafana.LokiGuardrailEnforce:
	default:
		return fmt.Errorf("invalid Loki guardrail mode %q (--loki-guardrail-mode or GRAFANA_LOKI_GUARDRAIL_MODE): must be one of off, shadow, enforce", gc.lokiGuardrailMode)
	}
	if gc.lokiGuardrailMaxBytes < 0 {
		return fmt.Errorf("invalid Loki guardrail max bytes %d (--loki-guardrail-max-bytes or GRAFANA_LOKI_GUARDRAIL_MAX_BYTES): must be >= 0 (0 disables the byte-budget check)", gc.lokiGuardrailMaxBytes)
	}
	if gc.lokiGuardrailMaxRange < 0 {
		return fmt.Errorf("invalid Loki guardrail max range %s (--loki-guardrail-max-range or GRAFANA_LOKI_GUARDRAIL_MAX_RANGE): must be >= 0 (0 disables the range check)", gc.lokiGuardrailMaxRange)
	}
	return nil
}

// toolEntry pairs a tool registration function with its category and disable flag.
type toolEntry struct {
	adder    func(*server.MCPServer)
	disabled bool
	category string
}

// toolEntries returns the ordered list of tool categories with their registration
// functions. This is the single source of truth for category-to-adder mapping,
// used by both processTools (registration) and buildInstructions (instructions).
func (dt *disabledTools) toolEntries() []toolEntry {
	enableWriteTools := !dt.write
	enableQueryTools := !dt.query
	enableMutatingQueryTools := dt.queryToolsEnabled("clickhouse")
	return []toolEntry{
		{tools.AddSearchTools, dt.search, "search"},
		{func(mcp *server.MCPServer) { tools.AddDatasourceTools(mcp, enableWriteTools) }, dt.datasource, "datasource"},
		{func(mcp *server.MCPServer) { tools.AddIncidentTools(mcp, enableWriteTools) }, dt.incident, "incident"},
		{func(mcp *server.MCPServer) { tools.AddPrometheusTools(mcp, enableQueryTools) }, dt.prometheus, "prometheus"},
		{func(mcp *server.MCPServer) { tools.AddLokiTools(mcp, enableQueryTools) }, dt.loki, "loki"},
		{func(mcp *server.MCPServer) { tools.AddElasticsearchTools(mcp, enableQueryTools) }, dt.elasticsearch, "elasticsearch"},
		{func(mcp *server.MCPServer) { tools.AddQuickwitTools(mcp, enableQueryTools) }, dt.quickwit, "quickwit"},
		{func(mcp *server.MCPServer) { tools.AddInfluxDBTools(mcp, enableMutatingQueryTools) }, dt.influxdb, "influxdb"},
		{func(mcp *server.MCPServer) { tools.AddAlertingTools(mcp, enableWriteTools) }, dt.alerting, "alerting"},
		{func(mcp *server.MCPServer) { tools.AddDashboardTools(mcp, enableWriteTools) }, dt.dashboard, "dashboard"},
		{func(mcp *server.MCPServer) { tools.AddFolderTools(mcp, enableWriteTools) }, dt.folder, "folder"},
		{func(mcp *server.MCPServer) { tools.AddOnCallTools(mcp, enableWriteTools) }, dt.oncall, "oncall"},
		{tools.AddAssertsTools, dt.asserts, "asserts"},
		{func(mcp *server.MCPServer) { tools.AddSiftTools(mcp, enableWriteTools) }, dt.sift, "sift"},
		{tools.AddAdminTools, dt.admin, "admin"},
		{func(mcp *server.MCPServer) { tools.AddPyroscopeTools(mcp, enableQueryTools) }, dt.pyroscope, "pyroscope"},
		{func(mcp *server.MCPServer) { tools.AddNavigationTools(mcp, enableWriteTools) }, dt.navigation, "navigation"},
		{func(mcp *server.MCPServer) { tools.AddAnnotationTools(mcp, enableWriteTools) }, dt.annotations, "annotations"},
		{tools.AddRenderingTools, dt.rendering, "rendering"},
		{func(mcp *server.MCPServer) { tools.AddSnapshotTools(mcp, enableWriteTools) }, dt.snapshot, "snapshot"},
		{func(mcp *server.MCPServer) { tools.AddCloudWatchTools(mcp, enableQueryTools) }, dt.cloudwatch, "cloudwatch"},
		{tools.AddExamplesTools, dt.examples, "examples"},
		{func(mcp *server.MCPServer) { tools.AddClickHouseTools(mcp, enableMutatingQueryTools) }, dt.clickhouse, "clickhouse"},
		{func(mcp *server.MCPServer) { tools.AddSnowflakeTools(mcp, enableMutatingQueryTools) }, dt.snowflake, "snowflake"},
		{func(mcp *server.MCPServer) { tools.AddRunPanelQueryTools(mcp, enableQueryTools) }, dt.runpanelquery, "runpanelquery"},
		{func(mcp *server.MCPServer) { tools.AddGraphiteTools(mcp, enableQueryTools) }, dt.graphite, "graphite"},
		{func(mcp *server.MCPServer) { tools.AddAthenaTools(mcp, enableMutatingQueryTools) }, dt.athena, "athena"},
		{func(mcp *server.MCPServer) { tools.AddPluginTools(mcp, enableWriteTools) }, dt.plugin, "plugin"},
		{func(mcp *server.MCPServer) { tools.AddAPITools(mcp, enableWriteTools, enableMutatingQueryTools) }, dt.api, "api"},
		{tools.AddConfigTools, dt.config, "config"},
		{tools.AddProvisioningTools, dt.provisioning, "provisioning"},
		{func(mcp *server.MCPServer) { tools.AddAgento11yTools(mcp, enableWriteTools) }, dt.agento11y, "agento11y"},
		{func(mcp *server.MCPServer) { tools.AddAssistantTools(mcp, enableWriteTools) }, dt.assistant, "assistant"},
		{tools.AddDocsTools, dt.docs, "docs"},
		{tools.AddUserTools, dt.user, "user"},
	}
}

// processTools registers enabled tool categories on the server.
func (dt *disabledTools) processTools(s *server.MCPServer) {
	if dt.query && dt.enableQuery {
		slog.Warn("--enable-query has no effect because --disable-query is set; no query tools will be registered")
	}
	enabledTools := strings.Split(dt.enabledTools, ",")
	for _, e := range dt.toolEntries() {
		maybeAddTools(s, e.adder, enabledTools, e.disabled, e.category)
	}
}

// buildInstructions constructs the server instruction string listing only
// the capabilities that are actually enabled.
func (dt *disabledTools) buildInstructions() string {
	enabledTools := strings.Split(dt.enabledTools, ",")

	var capabilities []string
	for _, e := range dt.toolEntries() {
		if !isCategoryEnabled(enabledTools, e.disabled, e.category) {
			continue
		}
		// The assistant category is entirely write-gated: AddAssistantTools
		// registers no tools when write tools are disabled. Don't advertise a
		// capability the server won't actually expose.
		if e.category == "assistant" && dt.write {
			continue
		}
		// Likewise for categories whose every tool executes a query: they
		// register nothing at all once their query tools are gated off.
		if !dt.queryToolsEnabled(e.category) {
			if slices.Contains(queryOnlyCategories, e.category) {
				continue
			}
			if desc, ok := categoryDescriptionNoQuery[e.category]; ok {
				capabilities = append(capabilities, desc)
				continue
			}
		}
		if desc, ok := categoryDescription[e.category]; ok {
			capabilities = append(capabilities, desc)
		}
	}

	// Proxied tools are registered via hooks (not maybeAddTools), so they
	// are not in toolEntries. Include their description when enabled.
	if !dt.proxied {
		capabilities = append(capabilities, "Proxied Tools: Access tools from external MCP servers (like Tempo) through dynamic discovery.")
	}

	var b strings.Builder
	b.WriteString("This server provides access to your Grafana instance and the surrounding ecosystem.\n\n")

	if len(capabilities) > 0 {
		b.WriteString("Available Capabilities:\n")
		for _, c := range capabilities {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("No tool categories are currently enabled.\n")
	}

	b.WriteString("\nTimestamp parameters without a timezone offset are interpreted as UTC. Include an offset like '-05:00' or use relative syntax like 'now-1h' to query in a different timezone.\n")
	return b.String()
}

func newServer(serverName, transport string, dt disabledTools, obs *observability.Observability, sessionIdleTimeoutMinutes int) (*server.MCPServer, *mcpgrafana.ToolManager, *mcpgrafana.SessionManager) {
	sm := mcpgrafana.NewSessionManager(
		mcpgrafana.WithSessionTTL(time.Duration(sessionIdleTimeoutMinutes)*time.Minute),
		mcpgrafana.WithSessionMeterProvider(obs.MeterProvider()),
	)

	// Declare variables that will be initialized after server creation.
	// The hooks below capture these by pointer, so they must be declared first.
	var stm *mcpgrafana.ToolManager
	var s *server.MCPServer

	// Create hooks
	hooks := &server.Hooks{
		OnRegisterSession:   []server.OnRegisterSessionHookFunc{sm.CreateSession},
		OnUnregisterSession: []server.OnUnregisterSessionHookFunc{sm.RemoveSession},
	}

	// Add proxied tools hooks if enabled and we're not running in stdio mode.
	// (stdio mode is handled by InitializeAndRegisterServerTools; per-session tools
	// are not supported).
	if transport != "stdio" && !dt.proxied {
		// ensureSessionRegistered registers an ephemeral session in MCPServer.sessions
		// if it's not already there. This is needed for horizontal scaling: when a
		// request lands on a pod that didn't handle the initialize call, the SDK
		// creates an ephemeral session that isn't registered, causing AddSessionTools
		// to fail with ErrSessionNotFound. RegisterSession uses LoadOrStore
		// internally, so this is a no-op for already-registered sessions.
		ensureSessionRegistered := func(ctx context.Context) {
			if s != nil {
				if session := server.ClientSessionFromContext(ctx); session != nil {
					_ = s.RegisterSession(ctx, session)
				}
			}
		}

		// OnBeforeListTools: Discover, connect, and register tools
		hooks.OnBeforeListTools = []server.OnBeforeListToolsFunc{
			func(ctx context.Context, id any, request *mcp.ListToolsRequest) {
				ensureSessionRegistered(ctx)
				if stm != nil {
					if session := server.ClientSessionFromContext(ctx); session != nil {
						stm.InitializeAndRegisterProxiedTools(ctx, session)
					}
				}
			},
		}

		// OnBeforeCallTool: Fallback in case client calls tool without listing first
		hooks.OnBeforeCallTool = []server.OnBeforeCallToolFunc{
			func(ctx context.Context, id any, request *mcp.CallToolRequest) {
				ensureSessionRegistered(ctx)
				if stm != nil {
					if session := server.ClientSessionFromContext(ctx); session != nil {
						stm.InitializeAndRegisterProxiedTools(ctx, session)
					}
				}
			},
		}
	}

	// Merge observability hooks with existing hooks
	hooks = observability.MergeHooks(hooks, obs.MCPHooks())

	// Register tools and build the instruction string from enabled categories.
	// processTools both registers tools on the server and collects descriptions
	// of enabled categories, so we need a temporary nil server reference first.
	// Instead, we split: compute instructions from flags, then create server,
	// then register tools.
	instructions := dt.buildInstructions()

	serverOpts := []server.ServerOption{
		server.WithInstructions(instructions),
		server.WithHooks(hooks),
		// A tool call with a JSON type mismatch (e.g. a number where the
		// schema declares a string) is a client/agent input error, not a
		// server fault: surface it as a structured tool result (IsError:
		// true, with the validation message) instead of a raw JSON-RPC
		// -32603 internal error, so the agent can see what was wrong and
		// self-correct. See SEP-1303.
		server.WithInputSchemaValidation(),
	}
	if mcpgrafana.DynamicMultiOrgEnabled {
		// Honor an optional per-call "orgId" argument so a single connection can
		// target multiple Grafana organizations (overrides the connection-level
		// org for that call). Only wired in when --dynamic-multi-org is set.
		serverOpts = append(serverOpts, server.WithToolHandlerMiddleware(mcpgrafana.OrgIDOverrideMiddleware))
	}
	s = server.NewMCPServer(serverName, mcpgrafana.Version(), serverOpts...)

	// Initialize ToolManager now that server is created
	stm = mcpgrafana.NewToolManager(sm, s,
		mcpgrafana.WithProxiedTools(!dt.proxied),
		mcpgrafana.WithToolManagerLogger(slog.Default()),
		mcpgrafana.WithToolManagerMeterProvider(obs.MeterProvider()),
	)

	// Give the SessionManager a reference to the MCPServer so the reaper can
	// unregister sessions from the SDK's internal session map.
	// (NewToolManager above already wires the SessionManager's ToolManager
	// reference back onto sm, so no separate SetToolManager call is needed here.)
	sm.SetMCPServer(s)

	dt.processTools(s)
	mcpgrafana.RegisterAppResources(s)
	return s, stm, sm
}

type tlsConfig struct {
	certFile, keyFile string
}

func (tc *tlsConfig) addFlags() {
	flag.StringVar(&tc.certFile, "server.tls-cert-file", "", "Path to TLS certificate file for server HTTPS (required for TLS)")
	flag.StringVar(&tc.keyFile, "server.tls-key-file", "", "Path to TLS private key file for server HTTPS (required for TLS)")
}

// httpSecurityConfig holds the Host/Origin allowlists enforced on HTTP-based
// transports. See DNSRebindingProtectionMiddleware for semantics.
type httpSecurityConfig struct {
	allowedHosts   string
	allowedOrigins string
}

func (hsc *httpSecurityConfig) addFlags() {
	flag.StringVar(&hsc.allowedHosts, "allowed-hosts", "", "Comma-separated allowlist of Host header values for the HTTP/SSE transports. Defaults to loopback variants of --address. Use \"*\" to disable validation (only safe behind a trusted reverse proxy that rewrites Host).")
	flag.StringVar(&hsc.allowedOrigins, "allowed-origins", "", "Comma-separated allowlist of Origin header values for the HTTP/SSE transports. Empty (the default) rejects any request that carries an Origin header — appropriate for non-browser MCP clients. Use \"*\" to disable validation.")
}

// serverAuthTokenEnvVar is the env fallback for --server-auth-token, so the
// secret need not appear in the process arguments.
const serverAuthTokenEnvVar = "MCP_GRAFANA_SERVER_TOKEN"

// callerAuthConfig configures authentication of *callers* to the HTTP/SSE
// transports — distinct from the credentials the server uses to reach Grafana.
// It gates who may invoke the MCP server at all.
type callerAuthConfig struct {
	// token, when non-empty, is required as "Authorization: Bearer <token>" on
	// every request to the MCP endpoint.
	token string
}

func (ca *callerAuthConfig) addFlags() {
	flag.StringVar(&ca.token, "server-auth-token", "", "Bearer token that callers must present in the Authorization header to use the HTTP/SSE transports. Falls back to the "+serverAuthTokenEnvVar+" environment variable. When set, unauthenticated requests are rejected with 401. Has no effect on the stdio transport.")
}

// resolveToken returns the caller token, falling back to the env var. It is
// trimmed so whitespace from a secrets mount can't produce a never-matching token.
func (ca callerAuthConfig) resolveToken() string {
	if t := strings.TrimSpace(ca.token); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv(serverAuthTokenEnvVar))
}

// checkCallerAuthPolicy logs the caller-authentication posture of a network
// transport at startup. Caller auth is enforced only when a token is configured
// (see withCallerAuth); this surfaces the posture so it isn't silently exposed:
//
//   - Token configured → callers are authenticated; logged at INFO.
//   - Loopback bind → only local processes can connect; logged at WARN with a hint.
//   - Non-loopback bind, no token → reachable and unauthenticated; logged at ERROR
//     (the highest --log-level, so the exposure can't be filtered out) and will
//     refuse to start in a future major release.
//
// A nil logger falls back to slog.Default().
func checkCallerAuthPolicy(transport, address, token string, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if token != "" {
		logger.Info("Caller authentication enabled: requests must present a valid bearer token", "transport", transport)
		return
	}
	if mcpgrafana.IsLoopbackOnlyBind(address) {
		logger.Warn("No caller authentication configured. The server is bound to a loopback address, so only local processes can reach it. Set --server-auth-token (or "+serverAuthTokenEnvVar+") to require authentication for non-local callers.", "address", address)
		return
	}
	// Logged at ERROR (not WARN) so the exposure is visible even under
	// --log-level error; error is the highest configurable level.
	logger.Error("SECURITY: serving on a non-loopback address with NO caller authentication. Anyone who can reach this address can invoke MCP tools and use any Grafana credentials the server is configured with. This will become a startup error in a future release: set --server-auth-token (or "+serverAuthTokenEnvVar+") to require a bearer token.", "address", address)
}

// withCallerAuth wraps h with bearer-token authentication when a token is
// configured, and returns it unchanged otherwise. Only the MCP endpoint is
// wrapped; health/metrics endpoints stay open.
func withCallerAuth(token string, h http.Handler) http.Handler {
	if token == "" {
		return h
	}
	return mcpgrafana.RequireBearerToken(token, slog.Default())(h)
}

// policy resolves the configured flags into a HostOriginPolicy. An
// --allowed-hosts whose parsed form is empty (unset, "," " , ", etc.) falls
// back to DefaultAllowedHosts so a malformed value cannot silently disable
// the Host check.
func (hsc httpSecurityConfig) policy(address string) mcpgrafana.HostOriginPolicy {
	hosts := splitAndTrim(hsc.allowedHosts)
	if len(hosts) == 0 {
		hosts = mcpgrafana.DefaultAllowedHosts(address)
	}
	return mcpgrafana.HostOriginPolicy{
		AllowedHosts:   hosts,
		AllowedOrigins: splitAndTrim(hsc.allowedOrigins),
	}
}

func (hsc httpSecurityConfig) corsOrigins() []string {
	if origins := splitAndTrim(hsc.allowedOrigins); len(origins) > 0 {
		for i, o := range origins {
			origins[i] = strings.ToLower(o)
		}
		return origins
	}
	// Sentinel keeps mcp-go's corsConfig.enabled() true so its SSE default
	// of Access-Control-Allow-Origin: * is suppressed.
	return []string{"https://mcp-grafana.invalid"}
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// httpServer represents a server with Start and Shutdown methods
type httpServer interface {
	Start(addr string) error
	Shutdown(ctx context.Context) error
}

// runHTTPServer handles the common logic for running HTTP-based servers
func runHTTPServer(ctx context.Context, srv httpServer, addr, transportName string) error {
	// Start server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(addr); err != nil {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Wait for either server error or shutdown signal
	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		slog.Info(fmt.Sprintf("%s server shutting down...", transportName))

		// Create a timeout context for shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown error: %v", err)
		}
		slog.Debug("Shutdown called, waiting for connections to close...")

		// Wait for server to finish
		select {
		case err := <-serverErr:
			// http.ErrServerClosed is expected when shutting down
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("server error during shutdown: %v", err)
			}
		case <-shutdownCtx.Done():
			slog.Warn(fmt.Sprintf("%s server did not stop gracefully within timeout", transportName))
		}
	}

	return nil
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// registerOps mounts /healthz and /metrics. An empty address keeps the route
// on mux; otherwise it goes on a side mux keyed by address (so matching
// --healthz-address and --metrics-address share a listener). Callers pass the
// result to runOpsServers. Side listeners skip Host/Origin checks.
func registerOps(mux *http.ServeMux, o *observability.Observability, healthzAddr string, obs observability.Config) map[string]*http.ServeMux {
	side := map[string]*http.ServeMux{}
	target := func(addr string) *http.ServeMux {
		if addr == "" {
			return mux
		}
		if side[addr] == nil {
			side[addr] = http.NewServeMux()
		}
		return side[addr]
	}

	target(healthzAddr).HandleFunc("/healthz", handleHealthz)
	if obs.MetricsEnabled {
		target(obs.MetricsAddress).Handle("/metrics", o.MetricsHandler())
	}
	return side
}

func runOpsServers(servers map[string]*http.ServeMux) {
	for addr, h := range servers {
		go runOpsServer(addr, h)
	}
}

func runOpsServer(addr string, h http.Handler) {
	slog.Info("Starting ops server", "address", addr)
	if err := http.ListenAndServe(addr, h); err != nil {
		slog.Error("ops server error", "error", err)
	}
}

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, hsc httpSecurityConfig, ca callerAuthConfig, obs observability.Config, sessionIdleTimeoutMinutes int, healthzAddress string) error {
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(stderrHandler))

	o, err := observability.Setup(obs)
	if err != nil {
		return fmt.Errorf("failed to setup observability: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := o.Shutdown(shutdownCtx); err != nil {
			slog.Error("failed to shutdown observability", "error", err)
		}
	}()

	// The otelslog bridge attaches trace_id / span_id from context, so log
	// records correlate with the spans mcp-grafana already emits.
	if lp := o.LoggerProvider(); lp != nil {
		otlpHandler := otelslog.NewHandler(defaultServerName, otelslog.WithLoggerProvider(lp))
		slog.SetDefault(slog.New(observability.NewFanoutHandler(stderrHandler, otlpHandler)))
		// Announce through the fanout so both stderr and OTLP subscribers see
		// the startup signal. If the first OTLP batch fails, the stderr branch
		// of the fanout still lands the record.
		slog.Info("OTLP log export configured", "endpoint", observability.OTLPLogsEndpoint())
	}

	// Announced after the log fanout so this line is itself exported when both
	// signals are on.
	if o.TracerProvider() != nil {
		slog.Info("OTLP trace export configured", "endpoint", observability.OTLPTracesEndpoint())
	}

	// Instrumentation that lives inside tool handlers (the Loki cost
	// guardrail) has no constructor to take a meter provider option, so it
	// reads one off the GrafanaConfig instead. Set explicitly rather than
	// relying on otel.GetMeterProvider() so the counters land on this
	// process's provider.
	gc.MeterProvider = o.MeterProvider()

	// Create a client cache for HTTP-based transports to avoid per-request
	// transport allocation (see https://github.com/grafana/mcp-grafana/issues/682).
	var clientCache *mcpgrafana.ClientCache
	if transport != "stdio" {
		clientCache = mcpgrafana.NewClientCache(nil, mcpgrafana.WithClientCacheMeterProvider(o.MeterProvider()))
		defer clientCache.Close()
	}

	s, tm, sm := newServer(obs.ServerName, transport, dt, o, sessionIdleTimeoutMinutes)
	defer sm.Close()

	// Create a context that will be cancelled on shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Handle shutdown signals
	go func() {
		<-sigChan
		slog.Info("Received shutdown signal")
		cancel()

		// For stdio, close stdin to unblock the Listen call
		if transport == "stdio" {
			_ = os.Stdin.Close()
		}
	}()

	// Resolve the caller-auth token once and surface the auth posture before we
	// start listening. stdio is a local pipe, so it is exempt.
	callerToken := ca.resolveToken()
	if transport == "sse" || transport == "streamable-http" {
		checkCallerAuthPolicy(transport, addr, callerToken, slog.Default())
		// With caller auth active, Authorization holds the caller token (stripped
		// after validation). Forwarding it to Grafana would leak it, so refuse the
		// contradictory combination.
		if callerToken != "" && mcpgrafana.ForwardsAuthorizationHeader() {
			return fmt.Errorf("refusing to start: caller authentication is enabled (--server-auth-token / %s) while GRAFANA_FORWARD_HEADERS forwards the Authorization header. Authorization is reserved for MCP caller authentication and would leak to Grafana. Remove Authorization from GRAFANA_FORWARD_HEADERS, or unset the caller token to run in proxy-forwarding mode", serverAuthTokenEnvVar)
		}
	}

	// Start the appropriate server based on transport
	switch transport {
	case "stdio":
		srv := server.NewStdioServer(s)
		cf := mcpgrafana.ComposedStdioContextFunc(gc)
		srv.SetContextFunc(cf)

		// For stdio (single-tenant), initialize proxied tools on the server directly
		if !dt.proxied {
			stdioCtx := cf(ctx)
			if err := tm.InitializeAndRegisterServerTools(stdioCtx); err != nil {
				slog.Error("failed to initialize proxied tools for stdio", "error", err)
			}
		}

		slog.Info("Starting Grafana MCP server using stdio transport", "version", mcpgrafana.Version())

		err := srv.Listen(ctx, os.Stdin, os.Stdout)
		if err != nil && err != context.Canceled {
			return fmt.Errorf("server error: %v", err)
		}
		return nil

	case "sse":
		httpSrv := &http.Server{Addr: addr}
		srv := server.NewSSEServer(s,
			server.WithSSEContextFunc(mcpgrafana.ComposedSSEContextFunc(gc, clientCache)),
			server.WithStaticBasePath(basePath),
			server.WithHTTPServer(httpSrv),
			server.WithSSECORS(server.WithCORSAllowedOrigins(hsc.corsOrigins()...)),
		)
		mux := http.NewServeMux()
		if basePath == "" {
			basePath = "/"
		}
		mux.Handle(basePath, withCallerAuth(callerToken, observability.WrapHandler(
			mcpgrafana.ValidateGrafanaURLMiddleware(srv), //nolint:staticcheck // Retained temporarily to reject malformed legacy headers.
			basePath,
		)))
		runOpsServers(registerOps(mux, o, healthzAddress, obs))
		// Wrap the full mux so ops routes left on it are validated too.
		httpSrv.Handler = mcpgrafana.DNSRebindingProtectionMiddleware(hsc.policy(addr))(mux)
		slog.Info("Starting Grafana MCP server using SSE transport",
			"version", mcpgrafana.Version(), "address", addr, "basePath", basePath, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, srv, addr, "SSE")
	case "streamable-http":
		httpSrv := &http.Server{Addr: addr}
		opts := []server.StreamableHTTPOption{
			server.WithHTTPContextFunc(mcpgrafana.ComposedHTTPContextFunc(gc, clientCache)),
			server.WithStateLess(dt.proxied), // Stateful when proxied tools enabled (requires sessions)
			server.WithEndpointPath(endpointPath),
			server.WithStreamableHTTPServer(httpSrv),
			server.WithStreamableHTTPCORS(server.WithCORSAllowedOrigins(hsc.corsOrigins()...)),
			// Enable the SDK's idle-session sweeper so per-session transport state
			// (the tool/resource maps populated by AddSessionTools, keyed by
			// session ID in the server's shared stores) is freed when a client
			// disconnects without sending a DELETE. Without it, UnregisterSession
			// only drops the session handle and those stores grow without bound,
			// leaking a fixed amount of memory per session that is ever created.
			// Use the same idle timeout as our own SessionManager reaper so the
			// two teardown paths stay aligned; a zero value disables both.
			server.WithSessionIdleTTL(time.Duration(sessionIdleTimeoutMinutes) * time.Minute),
		}
		if tls.certFile != "" || tls.keyFile != "" {
			opts = append(opts, server.WithTLSCert(tls.certFile, tls.keyFile))
		}
		srv := server.NewStreamableHTTPServer(s, opts...)
		mux := http.NewServeMux()
		mux.Handle(endpointPath, withCallerAuth(callerToken, observability.WrapHandler(
			mcpgrafana.ValidateGrafanaURLMiddleware(srv), //nolint:staticcheck // Retained temporarily to reject malformed legacy headers.
			endpointPath,
		)))
		runOpsServers(registerOps(mux, o, healthzAddress, obs))
		// Wrap the full mux so ops routes left on it are validated too.
		httpSrv.Handler = mcpgrafana.DNSRebindingProtectionMiddleware(hsc.policy(addr))(mux)
		slog.Info("Starting Grafana MCP server using StreamableHTTP transport",
			"version", mcpgrafana.Version(), "address", addr, "endpointPath", endpointPath, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, srv, addr, "StreamableHTTP")
	default:
		return fmt.Errorf("invalid transport type: %s. Must be 'stdio', 'sse' or 'streamable-http'", transport)
	}
}

func main() {
	var transport string
	flag.StringVar(&transport, "t", "stdio", "Transport type (stdio, sse or streamable-http)")
	flag.StringVar(
		&transport,
		"transport",
		"stdio",
		"Transport type (stdio, sse or streamable-http)",
	)
	var serverName string
	flag.StringVar(&serverName, "server-name", defaultServerName, "Server name used in the MCP handshake and OTel service.name. Overrides GRAFANA_MCP_SERVER_NAME env var.")
	addr := flag.String("address", "localhost:8000", "The host and port to start the sse server on")
	basePath := flag.String("base-path", "", "Base path for the sse server")
	endpointPath := flag.String("endpoint-path", "/mcp", "Endpoint path for the streamable-http server")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	sessionIdleTimeoutMinutes := flag.Int("session-idle-timeout-minutes", 30, "Session idle timeout in minutes. Sessions with no activity for this duration are automatically reaped. Set to 0 to disable session reaping")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	var dt disabledTools
	dt.addFlags()
	var gc grafanaConfig
	gc.addFlags()
	var tls tlsConfig
	tls.addFlags()
	var hsc httpSecurityConfig
	hsc.addFlags()
	var ca callerAuthConfig
	ca.addFlags()
	var obs observability.Config
	flag.BoolVar(&obs.MetricsEnabled, "metrics", false, "Enable Prometheus metrics endpoint")
	flag.StringVar(&obs.MetricsAddress, "metrics-address", "", "Separate address for metrics server (e.g., :9090). If empty, metrics are served on the main server at /metrics")
	healthzAddress := flag.String("healthz-address", "", "Separate address for /healthz (e.g., :8080). If empty, /healthz is served on the main HTTP server. A side listener is not wrapped by Host/Origin validation, matching --metrics-address.")
	flag.DurationVar(&obs.SlowRequestThreshold, "slow-request-threshold", 0, "Log an event when any MCP request (tool invocation, list, resource read, etc.) takes longer than this threshold. Accepts Go duration strings, e.g. 500ms, 5s. Default 0 disables slow-request logging.")
	var slowRequestLogLevelStr string
	flag.StringVar(&slowRequestLogLevelStr, "slow-request-log-level", "warn", "Log level for slow-request events. One of \"info\" or \"warn\". Default \"warn\".")
	flag.Parse()

	action, slowLevel, err := handleFlagsPostParse(*showVersion, slowRequestLogLevelStr)
	switch action {
	case flagActionVersion:
		fmt.Println(mcpgrafana.Version())
		os.Exit(0)
	case flagActionInvalidSlowLevel:
		fmt.Fprintf(os.Stderr, "invalid --slow-request-log-level: %v\n", err)
		os.Exit(2)
	case flagActionContinue:
		obs.SlowRequestLogLevel = slowLevel
	default:
		// flagActionUnset or any unexpected value — refuse to proceed silently.
		fmt.Fprintf(os.Stderr, "internal error: unexpected flag action %v\n", action)
		os.Exit(2)
	}

	serverNameFlagSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "server-name" {
			serverNameFlagSet = true
		}
	})
	serverName = resolveServerName(serverName, serverNameFlagSet, os.Getenv("GRAFANA_MCP_SERVER_NAME"), defaultServerName)
	if err := validateServerName(serverName); err != nil {
		source := "--server-name"
		if !serverNameFlagSet {
			source = "GRAFANA_MCP_SERVER_NAME"
		}
		fmt.Fprintf(os.Stderr, "invalid %s: %v\n", source, err)
		os.Exit(2)
	}

	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	if err := gc.applyLokiGuardrailEnv(setFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gc.validateLokiGuardrail(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if gc.lokiGuardrailMode != mcpgrafana.LokiGuardrailOff {
		slog.Info("Loki guardrail enabled", "mode", gc.lokiGuardrailMode, "max_bytes", gc.lokiGuardrailMaxBytes, "max_range", gc.lokiGuardrailMaxRange)
	}

	socks5Proxy, err := socks5ProxyFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// Enable per-call org selection before any tools are registered, so their
	// schemas and the override middleware are wired in consistently.
	mcpgrafana.DynamicMultiOrgEnabled = gc.dynamicMultiOrg

	// Convert local grafanaConfig to mcpgrafana.GrafanaConfig
	grafanaConfig := mcpgrafana.GrafanaConfig{
		Debug:                   gc.debug,
		MaxLokiLogLimit:         gc.maxLokiLogLimit,
		LokiGuardrailMode:       gc.lokiGuardrailMode,
		LokiGuardrailMaxBytes:   gc.lokiGuardrailMaxBytes,
		LokiGuardrailMaxRange:   gc.lokiGuardrailMaxRange,
		IncludeArgumentsInSpans: gc.includeArgsInSpans,
		Timeout:                 gc.timeout,
		SOCKS5ProxyURL:          socks5Proxy,
	}
	if gc.tlsCertFile != "" || gc.tlsKeyFile != "" || gc.tlsCAFile != "" || gc.tlsSkipVerify {
		grafanaConfig.TLSConfig = &mcpgrafana.TLSConfig{
			CertFile:   gc.tlsCertFile,
			KeyFile:    gc.tlsKeyFile,
			CAFile:     gc.tlsCAFile,
			SkipVerify: gc.tlsSkipVerify,
		}
	}

	// Set OTel resource identity
	obs.ServerName = serverName
	obs.ServerVersion = mcpgrafana.Version()

	// Map transport flag to semconv network.transport values
	switch transport {
	case "stdio":
		obs.NetworkTransport = mcpconv.NetworkTransportPipe
	case "sse", "streamable-http":
		obs.NetworkTransport = mcpconv.NetworkTransportTCP
	}

	level := parseLevel(*logLevel)
	if grafanaConfig.Debug && level > slog.LevelDebug {
		level = slog.LevelDebug
	}

	if err := run(transport, *addr, *basePath, *endpointPath, level, dt, grafanaConfig, tls, hsc, ca, obs, *sessionIdleTimeoutMinutes, *healthzAddress); err != nil {
		panic(err)
	}
}

func parseLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// parseSlowRequestLogLevel parses the --slow-request-log-level flag value.
// Only "info" and "warn" are accepted (case-insensitive). Any other value,
// including the empty string or values with surrounding whitespace, returns
// a non-nil error so main() can fail-fast on misconfiguration rather than
// silently defaulting.
//
// On error the returned slog.Level is the zero value (slog.LevelInfo == 0).
// Callers MUST check the error before using the level; using the zero level
// on a rejected input would silently select INFO, which is not the CLI's
// advertised default of WARN.
func parseSlowRequestLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	default:
		return 0, fmt.Errorf("must be \"info\" or \"warn\", got %q", s)
	}
}

// flagAction encodes what main() should do after flag.Parse().
// flagActionUnset is reserved as the zero value so an accidentally-zero-valued
// return from a future code path trips the switch's default: case rather
// than silently taking the Continue branch.
type flagAction int

const (
	flagActionUnset flagAction = iota
	flagActionContinue
	flagActionVersion
	flagActionInvalidSlowLevel
)

// handleFlagsPostParse decides what main() should do after flag.Parse().
// It is pure (no os.Exit, no I/O) so it is unit-testable. --version
// short-circuits before slow-request-log-level validation so it prints
// regardless of other flags' values (matches pre-#756 behavior).
//
// The returned slog.Level is only meaningful when action == flagActionContinue;
// the other branches return a zero level that the caller must not read.
func handleFlagsPostParse(showVersion bool, slowLevelStr string) (flagAction, slog.Level, error) {
	if showVersion {
		return flagActionVersion, 0, nil
	}
	slowLevel, err := parseSlowRequestLogLevel(slowLevelStr)
	if err != nil {
		return flagActionInvalidSlowLevel, 0, err
	}
	return flagActionContinue, slowLevel, nil
}
