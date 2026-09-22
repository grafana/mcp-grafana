package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/observability"
	"github.com/grafana/mcp-grafana/v2/tools"
	"github.com/grafana/mcp-grafana/v2/usagestats"
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

func maybeAddTools(s *mcp.Server, tf func(*mcp.Server), enabledTools []string, disable bool, category string) {
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

func isCategoryEnabled(enabledTools []string, disabled bool, category string) bool {
	return slices.Contains(enabledTools, category) && !disabled
}

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
	"tempo":         "Tempo: Search traces with TraceQL, compute trace-derived metrics, fetch and diff traces, and explore trace attributes.",
	"annotations":   "Annotations: Create and manage dashboard annotations.",
	"rendering":     "Rendering: Export dashboard panels or full dashboards as PNG images (requires Grafana Image Renderer plugin).",
	"snapshot":      "Snapshots: List, get, create, and delete dashboard snapshots.",
	"plugin":        "Plugins: Check whether Grafana plugins are installed and fetch plugin details.",
	"cloudwatch":    "CloudWatch: Query AWS CloudWatch datasources for metrics and logs.",
	"examples":      "Examples: Query example tools.",
	"sql":           "SQL: Query supported SQL datasources (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL) via Grafana with macro substitution, schema discovery, and variable support.",
	"runpanelquery": "Run Panel Query: Execute panel queries directly.",
	"graphite":      "Graphite: Query Graphite datasources for metrics.",
	"api":           "API: Make authenticated HTTP requests to any Grafana API endpoint with optional jq-style response filtering.",
	"config":        "Config: Generate operator-facing configuration snippets (e.g. Alloy label-enforcement pipelines).",
	"provisioning":  "Provisioning: List provisioning repositories (e.g. git-sync sources) to discover repository slugs for use with rendering tools.",
	"agento11y":     "Agent Observability: Search and inspect LLM conversations, generations, and evaluation scores from Grafana Agent Observability. Read its agent catalog (system prompts, tools, version history, and per-version scores). Read or manage its eval configuration (evaluators, templates, eval rules, and guards), its curated saved conversations and collections, its offline experiments with their trials and scores, and the versioned test suites those experiments run against.",
	"assistant":     "Assistant: Ask Grafana Assistant open-ended questions and get a full text reply (requires the Grafana Assistant plugin).",
	"docs":          "Docs: Search and retrieve Grafana product documentation (powered by grafana.com/llms-full.txt).",
	"user":          "User: Identify the current user/credential, its capabilities, and the organizations it can access.",
}

var categoryDescriptionNoQuery = map[string]string{
	"prometheus": "Prometheus: Retrieve metric metadata and explore metric names and label names/values. Query execution is disabled.",
	"loki":       "Loki: Retrieve log metadata and index stats, explore label names/values, and audit label strategy. Log query execution is disabled.",
	"pyroscope":  "Pyroscope: Explore profile types and label names/values. Query execution is disabled.",
	"cloudwatch": "CloudWatch: List AWS CloudWatch namespaces, metrics, and dimensions. Query execution is disabled.",
	"sql":        "SQL: List tables and describe table schemas in SQL datasources (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL). Query execution is disabled.",
	"graphite":   "Graphite: List Graphite metrics and tags. Query execution is disabled.",
}

var queryOnlyCategories = []string{"elasticsearch", "quickwit", "influxdb", "runpanelquery", "tempo"}

var mutatingQueryCategories = []string{"sql", "influxdb"}

var enableQueryToolNames = []string{"query_sql", "query_influxdb"}

func (dt *disabledTools) writeToolOverridden(names ...string) bool {
	overrides := strings.Split(dt.writeToolOverrides, ",")
	for i, o := range overrides {
		overrides[i] = strings.TrimSpace(o)
	}
	if dt.enableQuery {
		overrides = append(overrides, enableQueryToolNames...)
	}
	for _, name := range names {
		if slices.Contains(overrides, name) {
			return true
		}
	}
	return false
}

func (dt *disabledTools) writeToolEnabled(names ...string) bool {
	return !dt.write || dt.writeToolOverridden(names...)
}

func (dt *disabledTools) queryToolsEnabled(category string) bool {
	if dt.query {
		return false
	}
	if slices.Contains(mutatingQueryCategories, category) {
		return dt.writeToolEnabled("query_" + category)
	}
	return true
}

func (dt *disabledTools) mutatingQueryToolsEnabled() bool {
	if dt.query {
		return false
	}
	return dt.writeToolEnabled(enableQueryToolNames...)
}

type disabledTools struct {
	enabledTools string

	writeToolOverrides string

	search, datasource, incident,
	prometheus, loki, elasticsearch, quickwit, influxdb, alerting,
	dashboard, folder, oncall, asserts, sift, admin,
	pyroscope, navigation, tempo, annotations, rendering, cloudwatch, write, query, enableQuery,
	snapshot, examples, sql, graphite,
	runpanelquery, plugin, api, config, provisioning,
	agento11y, assistant, docs, user bool
}

type grafanaConfig struct {
	debug bool

	tlsCertFile   string
	tlsKeyFile    string
	tlsCAFile     string
	tlsSkipVerify bool

	maxLokiLogLimit int

	lokiGuardrailMode     string
	lokiGuardrailMaxBytes int64
	lokiGuardrailMaxRange time.Duration

	includeArgsInSpans bool

	timeout time.Duration

	dynamicMultiOrg bool

	lokiEnforcedMatchers         string
	lokiLabelEnumerationFallback string
}

func (dt *disabledTools) addFlags() {
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,tempo,annotations,rendering,snapshot,plugin,api,config,provisioning,docs,user", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")
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
	flag.BoolVar(&dt.tempo, "disable-tempo", false, "Disable Tempo tracing tools")
	flag.BoolVar(&dt.tempo, "disable-proxied", false, "Deprecated: use --disable-tempo instead")
	flag.BoolVar(&dt.write, "disable-write", false, "Disable write tools (create/update operations)")
	flag.BoolVar(&dt.query, "disable-query", false, "Disable query tools (tools that execute a query against a datasource, e.g. query_prometheus, query_loki_logs, run_panel_query). Metadata and discovery tools stay available.")
	flag.BoolVar(&dt.enableQuery, "enable-query", false, "Keep the raw-SQL query tools (query_sql, query_influxdb) registered even under --disable-write. They pass the query through unfiltered, so they can mutate data if the datasource credentials permit it; use this when those credentials are known to be read-only. Has no effect if --disable-query is also set. Equivalent to --enable-write-tools=query_sql,query_influxdb; kept as a shorthand for that common case.")
	flag.StringVar(&dt.writeToolOverrides, "enable-write-tools", "", "Comma separated list of individual tool names to keep registered even under --disable-write, for tools whose write behavior is scoped enough to opt back in independently (e.g. find_error_pattern_logs,find_slow_requests, which only create ephemeral Sift investigation records and never touch a Grafana dashboard, alert, or datasource). Has no effect on a tool whose whole category is disabled, e.g. via --disable-sift.")
	flag.BoolVar(&dt.annotations, "disable-annotations", false, "Disable annotation tools")
	flag.BoolVar(&dt.rendering, "disable-rendering", false, "Disable rendering tools (panel/dashboard image export)")
	flag.BoolVar(&dt.snapshot, "disable-snapshot", false, "Disable snapshot tools")
	flag.BoolVar(&dt.cloudwatch, "disable-cloudwatch", false, "Disable CloudWatch tools")
	flag.BoolVar(&dt.examples, "disable-examples", false, "Disable query examples tools")
	flag.BoolVar(&dt.sql, "disable-sql", false, "Disable SQL tools (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL)")
	flag.BoolVar(&dt.sql, "disable-clickhouse", false, "Deprecated: use --disable-sql instead")
	flag.BoolVar(&dt.sql, "disable-snowflake", false, "Deprecated: use --disable-sql instead")
	flag.BoolVar(&dt.sql, "disable-athena", false, "Deprecated: use --disable-sql instead")
	flag.BoolVar(&dt.runpanelquery, "disable-runpanelquery", false, "Disable run panel query tools")
	flag.BoolVar(&dt.graphite, "disable-graphite", false, "Disable Graphite tools")
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

	flag.StringVar(&gc.tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file for client authentication")
	flag.StringVar(&gc.tlsKeyFile, "tls-key-file", "", "Path to TLS private key file for client authentication")
	flag.StringVar(&gc.tlsCAFile, "tls-ca-file", "", "Path to TLS CA certificate file for server verification")
	flag.BoolVar(&gc.tlsSkipVerify, "tls-skip-verify", false, "Skip TLS certificate verification (insecure)")

	flag.IntVar(&gc.maxLokiLogLimit, "max-loki-log-limit", tools.MaxLokiLogLimit, "Maximum number of log lines returned per query_loki_logs call")

	flag.StringVar(&gc.lokiGuardrailMode, "loki-guardrail-mode", mcpgrafana.LokiGuardrailOff, "Loki query cost guardrail mode for query_loki_logs: 'off' (default), 'shadow' (evaluate and log queries that would be blocked, but let them run; still pays the index/stats round trip), or 'enforce' (reject blocked queries with rewrite guidance). Falls back to the GRAFANA_LOKI_GUARDRAIL_MODE environment variable when the flag is not set.")
	flag.Int64Var(&gc.lokiGuardrailMaxBytes, "loki-guardrail-max-bytes", 100<<30, "Maximum bytes a single query_loki_logs call may scan, estimated via Loki's index/stats API before running the query. 0 disables the byte-budget check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_MAX_BYTES environment variable when the flag is not set.")
	flag.DurationVar(&gc.lokiGuardrailMaxRange, "loki-guardrail-max-range", 24*time.Hour, "Maximum effective time range for a single query_loki_logs call, including range-vector durations like [30d]. Accepts Go duration strings, e.g. 24h. 0 disables the range check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_MAX_RANGE environment variable when the flag is not set.")

	flag.StringVar(&gc.lokiEnforcedMatchers, "loki-enforced-matchers", "", "LogQL label matchers AND-ed into every native-Loki query to restrict readable streams (e.g. `namespace!~\"vault|payments\"`). Queries that cannot be parsed are rejected. Requires --disable-api to be effective, otherwise it can be bypassed via the raw datasource proxy.")
	flag.StringVar(&gc.lokiLabelEnumerationFallback, "loki-label-enumeration-fallback", tools.LabelEnumFallbackReject, "Behaviour of list_loki_label_names/list_loki_label_values when --loki-enforced-matchers cannot scope them (purely-negative matchers only): 'reject' (fail closed) or 'unfiltered' (allow unscoped enumeration of label metadata; never exposes log lines).")

	flag.BoolVar(&gc.includeArgsInSpans, "include-args-in-spans", false, "Include tool call arguments in OpenTelemetry spans. Only enable in non-production environments or when arguments are known not to contain PII.")
	flag.DurationVar(&gc.timeout, "grafana-timeout", mcpgrafana.DefaultGrafanaClientTimeout, "Time limit for requests made by the Grafana client. Accepts Go duration strings, e.g. 10s, 500ms.")

	flag.BoolVar(&gc.dynamicMultiOrg, "dynamic-multi-org", false, "Allow tool calls to select a Grafana organization per call via an optional orgId argument (org is otherwise fixed at connection startup). Adds an orgId argument to every tool's schema.")
}

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

type toolEntry struct {
	adder    func(*mcp.Server)
	disabled bool
	category string
}

func (dt *disabledTools) toolEntries() []toolEntry {
	enableWriteTools := !dt.write
	enableQueryTools := !dt.query
	return []toolEntry{
		{tools.AddSearchTools, dt.search, "search"},
		{func(s *mcp.Server) { tools.AddDatasourceTools(s, enableWriteTools) }, dt.datasource, "datasource"},
		{func(s *mcp.Server) { tools.AddIncidentTools(s, enableWriteTools) }, dt.incident, "incident"},
		{func(s *mcp.Server) { tools.AddPrometheusTools(s, enableQueryTools) }, dt.prometheus, "prometheus"},
		{func(s *mcp.Server) { tools.AddLokiTools(s, enableQueryTools) }, dt.loki, "loki"},
		{func(s *mcp.Server) { tools.AddElasticsearchTools(s, enableQueryTools) }, dt.elasticsearch, "elasticsearch"},
		{func(s *mcp.Server) { tools.AddQuickwitTools(s, enableQueryTools) }, dt.quickwit, "quickwit"},
		{func(s *mcp.Server) { tools.AddInfluxDBTools(s, dt.queryToolsEnabled("influxdb")) }, dt.influxdb, "influxdb"},
		{func(s *mcp.Server) { tools.AddAlertingTools(s, enableWriteTools) }, dt.alerting, "alerting"},
		{func(s *mcp.Server) { tools.AddDashboardTools(s, enableWriteTools) }, dt.dashboard, "dashboard"},
		{func(s *mcp.Server) { tools.AddFolderTools(s, enableWriteTools) }, dt.folder, "folder"},
		{func(s *mcp.Server) { tools.AddOnCallTools(s, enableWriteTools) }, dt.oncall, "oncall"},
		{tools.AddAssertsTools, dt.asserts, "asserts"},
		{func(s *mcp.Server) {
			tools.AddSiftTools(s, dt.writeToolEnabled("find_error_pattern_logs", "find_slow_requests"))
		}, dt.sift, "sift"},
		{tools.AddAdminTools, dt.admin, "admin"},
		{func(s *mcp.Server) { tools.AddPyroscopeTools(s, enableQueryTools) }, dt.pyroscope, "pyroscope"},
		{func(s *mcp.Server) { tools.AddNavigationTools(s, enableWriteTools) }, dt.navigation, "navigation"},
		{func(s *mcp.Server) { tools.AddTempoTools(s, enableQueryTools) }, dt.tempo, "tempo"},
		{func(s *mcp.Server) { tools.AddAnnotationTools(s, enableWriteTools) }, dt.annotations, "annotations"},
		{tools.AddRenderingTools, dt.rendering, "rendering"},
		{func(s *mcp.Server) { tools.AddSnapshotTools(s, enableWriteTools) }, dt.snapshot, "snapshot"},
		{func(s *mcp.Server) { tools.AddCloudWatchTools(s, enableQueryTools) }, dt.cloudwatch, "cloudwatch"},
		{tools.AddExamplesTools, dt.examples, "examples"},
		{func(s *mcp.Server) { tools.AddSQLTools(s, dt.queryToolsEnabled("sql")) }, dt.sql, "sql"},
		{func(s *mcp.Server) { tools.AddRunPanelQueryTools(s, enableQueryTools) }, dt.runpanelquery, "runpanelquery"},
		{func(s *mcp.Server) { tools.AddGraphiteTools(s, enableQueryTools) }, dt.graphite, "graphite"},
		{func(s *mcp.Server) { tools.AddPluginTools(s, enableWriteTools) }, dt.plugin, "plugin"},
		{func(s *mcp.Server) { tools.AddAPITools(s, enableWriteTools, dt.mutatingQueryToolsEnabled()) }, dt.api, "api"},
		{tools.AddConfigTools, dt.config, "config"},
		{tools.AddProvisioningTools, dt.provisioning, "provisioning"},
		{func(s *mcp.Server) { tools.AddAgento11yTools(s, enableWriteTools) }, dt.agento11y, "agento11y"},
		{func(s *mcp.Server) { tools.AddAssistantTools(s, enableWriteTools) }, dt.assistant, "assistant"},
		{tools.AddDocsTools, dt.docs, "docs"},
		{tools.AddUserTools, dt.user, "user"},
	}
}

// categoryReport lists the tool categories that are actually active and the
// ones a --disable-* flag turned off, for the usage-statistics enabled_tools /
// disabled_tools fields. The two are not complements: a category this build
// knows about that is neither named in --enabled-tools nor explicitly disabled
// appears in neither list.
//
// Both lists are bounded by toolEntries, so neither can carry an
// operator-supplied string — a category named in --enabled-tools that this
// build does not know about is reported by neither.
func (dt *disabledTools) categoryReport() (enabled, disabled []string) {
	enabledTools := strings.Split(dt.enabledTools, ",")
	for _, e := range dt.toolEntries() {
		switch {
		case dt.categoryRegistersTools(e, enabledTools):
			enabled = append(enabled, e.category)
		case e.disabled:
			disabled = append(disabled, e.category)
		}
	}
	return enabled, disabled
}

// categoryRegistersTools reports whether a category that survived
// --enabled-tools and --disable-<category> actually registers a tool once the
// write and query gates are applied.
//
// It exists so buildInstructions and categoryReport cannot disagree. They ask
// the same question for different audiences — one advertises the capability to
// the agent, the other reports it as enabled — and a category that registers
// nothing must do neither. They did disagree: --enabled-tools=assistant with
// --disable-write reported assistant enabled while registering no tool, and
// --disable-query did the same for every query-only category.
func (dt *disabledTools) categoryRegistersTools(e toolEntry, enabledTools []string) bool {
	if !isCategoryEnabled(enabledTools, e.disabled, e.category) {
		return false
	}
	// AddAssistantTools registers nothing when write tools are disabled.
	if e.category == "assistant" && dt.write {
		return false
	}
	// A category whose every tool executes a query registers nothing once
	// query tools are gated off.
	if !dt.queryToolsEnabled(e.category) && slices.Contains(queryOnlyCategories, e.category) {
		return false
	}
	return true
}

// categoryAliases maps deprecated category names to their current replacements.
// When an alias appears in --enabled-tools it is replaced with the target name.
//
// For aliases that were never in the default --enabled-tools list (the SQL
// dialects), the target's --disable flag is also cleared so that an explicit
// opt-in wins over an unrelated --disable-sql. The "proxied" alias is NOT
// auto-cleared because "proxied" was part of the v1 default list, so existing
// configs may have both "proxied" in --enabled-tools (carried forward) AND
// --disable-proxied to turn it off — auto-clearing would silently re-enable it.
var categoryAliases = map[string]string{
	"clickhouse": "sql",
	"snowflake":  "sql",
	"athena":     "sql",
	"proxied":    "tempo",
}

// normalizeEnabledTools rewrites the enabled-tools list, replacing deprecated
// category aliases with their current names.
func (dt *disabledTools) normalizeEnabledTools() {
	parts := strings.Split(dt.enabledTools, ",")
	resolved := map[string]bool{}
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if target, ok := categoryAliases[p]; ok {
			slog.Warn("Deprecated category alias in --enabled-tools, mapped to new name", "old", p, "new", target)
			resolved[target] = true
			p = target
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	dt.enabledTools = strings.Join(out, ",")
	// SQL dialect aliases were never in the default list, so their presence is
	// an explicit opt-in that should override --disable-sql.
	if resolved["sql"] {
		dt.sql = false
	}
	// "proxied" was in the v1 default list, so we intentionally do NOT clear
	// dt.tempo here — --disable-proxied / --disable-tempo must still win.
}

func (dt *disabledTools) processTools(s *mcp.Server) {
	dt.normalizeEnabledTools()
	if dt.query && dt.enableQuery {
		slog.Warn("--enable-query has no effect because --disable-query is set; no query tools will be registered")
	}
	if dt.sift && dt.writeToolOverridden("find_error_pattern_logs", "find_slow_requests") {
		slog.Warn("--enable-write-tools naming find_error_pattern_logs/find_slow_requests has no effect because --disable-sift is set; no sift tools will be registered")
	}
	enabledTools := strings.Split(dt.enabledTools, ",")
	for _, e := range dt.toolEntries() {
		maybeAddTools(s, e.adder, enabledTools, e.disabled, e.category)
	}
}

func (dt *disabledTools) buildInstructions() string {
	dt.normalizeEnabledTools()
	enabledTools := strings.Split(dt.enabledTools, ",")

	var capabilities []string
	for _, e := range dt.toolEntries() {
		// Don't advertise a capability the server won't actually expose; the
		// same gate decides what categoryReport reports as enabled.
		if !dt.categoryRegistersTools(e, enabledTools) {
			continue
		}
		// Registered, but with its query tools gated off, so describe the
		// reduced capability rather than the full one.
		if !dt.queryToolsEnabled(e.category) {
			if desc, ok := categoryDescriptionNoQuery[e.category]; ok {
				capabilities = append(capabilities, desc)
				continue
			}
		}
		if desc, ok := categoryDescription[e.category]; ok {
			capabilities = append(capabilities, desc)
		}
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

func appendInstructions(base, extra string) string {
	if extra = strings.TrimSpace(extra); extra != "" {
		return base + "\n" + extra + "\n"
	}
	return base
}

func newServer(serverName string, dt disabledTools, obs *observability.Observability, usage *usagestats.Reporter, instructionsAppend string) *mcp.Server {
	instructions := appendInstructions(dt.buildInstructions(), instructionsAppend)

	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: mcpgrafana.Version()}, &mcp.ServerOptions{
		Instructions: instructions,
	})

	dt.processTools(s)
	mcpgrafana.RegisterAppResources(s)

	s.AddReceivingMiddleware(obs.MCPMiddleware())
	s.AddReceivingMiddleware(usage.MCPMiddleware())

	// Restore the private cache scope that the old OnAfterListTools hook set.
	// The go-sdk defaults to "public"; shared proxies rely on "private" to
	// avoid serving stale tool lists across credential contexts.
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err == nil && method == "tools/list" {
				if r, ok := result.(*mcp.ListToolsResult); ok {
					r.CacheScope = "private"
				} else if result != nil {
					slog.Error("tools/list returned unexpected result type; cache scope defaults to public — update this assertion for the new go-sdk type", "type", fmt.Sprintf("%T", result))
				}
			}
			return result, err
		}
	})

	// OrgID and GrafanaContext middleware are registered per-transport in
	// run(), not here, so that HTTP transports can place OrgID inside
	// GrafanaContext (the go-sdk's addMiddleware wraps outermost-last).
	// Recovery middleware is also registered in run() — after all other
	// middleware — so it is truly outermost and catches panics everywhere.

	return s
}

func recoveryMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			defer func() {
				if p := recover(); p != nil {
					slog.Error("panic in MCP handler", "method", method, "panic", p)
					result = nil
					err = fmt.Errorf("internal error")
				}
			}()
			return next(ctx, method, req)
		}
	}
}

type tlsConfig struct {
	certFile, keyFile string
}

func (tc *tlsConfig) addFlags() {
	flag.StringVar(&tc.certFile, "server.tls-cert-file", "", "Path to TLS certificate file for server HTTPS (required for TLS)")
	flag.StringVar(&tc.keyFile, "server.tls-key-file", "", "Path to TLS private key file for server HTTPS (required for TLS)")
}

type httpSecurityConfig struct {
	allowedHosts   string
	allowedOrigins string
}

func (hsc *httpSecurityConfig) addFlags() {
	flag.StringVar(&hsc.allowedHosts, "allowed-hosts", "", "Comma-separated allowlist of Host header values for the HTTP/SSE transports. Defaults to loopback variants of --address. Use \"*\" to disable Host validation (only safe behind a trusted reverse proxy that validates Host).")
	flag.StringVar(&hsc.allowedOrigins, "allowed-origins", "", "Comma-separated allowlist of Origin header values for the HTTP/SSE transports. Empty (the default) rejects any request that carries an Origin header — appropriate for non-browser MCP clients. Use \"*\" to disable validation.")
}

const serverAuthTokenEnvVar = "MCP_GRAFANA_SERVER_TOKEN"

type callerAuthConfig struct {
	token string
}

func (ca *callerAuthConfig) addFlags() {
	flag.StringVar(&ca.token, "server-auth-token", "", "Bearer token that callers must present in the Authorization header to use the HTTP/SSE transports. Falls back to the "+serverAuthTokenEnvVar+" environment variable. When set, unauthenticated requests are rejected with 401. Has no effect on the stdio transport.")
}

func (ca callerAuthConfig) resolveToken() string {
	if t := strings.TrimSpace(ca.token); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv(serverAuthTokenEnvVar))
}

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
	logger.Error("SECURITY: serving on a non-loopback address with NO caller authentication. Anyone who can reach this address can invoke MCP tools and use any Grafana credentials the server is configured with. This will become a startup error in a future release: set --server-auth-token (or "+serverAuthTokenEnvVar+") to require a bearer token.", "address", address)
}

func withCallerAuth(token string, h http.Handler) http.Handler {
	if token == "" {
		return h
	}
	return mcpgrafana.RequireBearerToken(token, slog.Default())(h)
}

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

// warnLokiEnforcementBypasses logs, at startup, every enabled tool through which
// an LLM could reach Loki log data WITHOUT going through the enforced Loki
// backend — so --loki-enforced-matchers would not apply. Each line names the
// mechanism and the flag that closes it.
func warnLokiEnforcementBypasses(dt disabledTools) {
	enabledTools := strings.Split(dt.enabledTools, ",")
	type bypass struct {
		category string
		disabled bool
		flag     string
		reason   string
	}
	for _, b := range []bypass{
		{"api", dt.api, "--disable-api", "grafana_api_request can query the Loki datasource proxy directly, fully bypassing enforcement"},
		{"rendering", dt.rendering, "--disable-rendering", "get_panel_image renders Loki panels server-side via the Grafana renderer, producing images that contain unrestricted log lines"},
		{"sift", dt.sift, "--disable-sift", "Sift investigations (e.g. find_error_pattern_logs) analyze Loki logs server-side across all streams; enforced matchers are not applied to that analysis"},
	} {
		if isCategoryEnabled(enabledTools, b.disabled, b.category) {
			slog.Warn("Loki label-matcher enforcement can be bypassed by an enabled tool",
				"disable_with", b.flag, "reason", b.reason)
		}
	}
	if isCategoryEnabled(enabledTools, dt.assistant, "assistant") && !dt.write {
		slog.Warn("Loki label-matcher enforcement can be bypassed by an enabled tool",
			"disable_with", "--disable-assistant",
			"reason", "ask_assistant delegates to Grafana Assistant, which reads Loki server-side across all streams; enforced matchers are not applied to what it reports back")
	}
	if isCategoryEnabled(enabledTools, dt.snapshot, "snapshot") {
		slog.Info("Loki label-matcher enforcement: dashboard snapshots can return log-panel data captured outside enforcement (e.g. pre-existing snapshots); disable with --disable-snapshot if snapshots may contain restricted logs")
	}
}

// corsMiddleware adds CORS headers when the request Origin matches an allowed
// origin. The official go-sdk sets no CORS headers (unlike mark3labs which had
// built-in CORS support), so this is needed for browser-based MCP clients.
func corsMiddleware(origins []string, next http.Handler) http.Handler {
	if len(origins) == 0 {
		return next
	}
	allowedSet := make(map[string]bool, len(origins))
	allowAll := false
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
		allowedSet[strings.ToLower(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || allowedSet[strings.ToLower(origin)]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, MCP-Protocol-Version")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (hsc httpSecurityConfig) corsOrigins() []string {
	return splitAndTrim(hsc.allowedOrigins)
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

// isBenignStdioClose returns true for errors the go-sdk surfaces during
// normal stdio transport shutdown (stdin EOF or connection teardown).
func isBenignStdioClose(err error) bool {
	return errors.Is(err, mcp.ErrConnectionClosed)
}

// runHTTPServer starts an *http.Server and blocks until ctx is cancelled or
// the server returns an error. When tlsCert is non-empty, it uses TLS. On
// context cancellation it performs a graceful shutdown with a 5-second deadline.
func runHTTPServer(ctx context.Context, srv *http.Server, transportName, tlsCert, tlsKey string) error {
	serverErr := make(chan error, 1)
	go func() {
		var err error
		if tlsCert != "" {
			err = srv.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		slog.Info(fmt.Sprintf("%s server shutting down...", transportName))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

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

// grafanaTarget describes the Grafana instance a session talks to for the
// usage-statistics report. It reads only the resolved configuration in the
// context and issues no request of its own: GrafanaVersionIfKnown, unlike
// GrafanaVersion, never falls through to a fetch, so a session that has not
// yet talked to Grafana — or an instance whose settings endpoint is forbidden
// — reports an empty version rather than paying a timeout on every tool call.
// Reporting must never add traffic to the operator's Grafana.
//
// The URL is passed for classification into cloud/self_hosted only; the
// reporter discards it.
func grafanaTarget(ctx context.Context) usagestats.GrafanaTarget {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	return usagestats.GrafanaTarget{
		URL:     cfg.URL,
		Version: mcpgrafana.GrafanaVersionIfKnown(ctx),
		AuthMethod: usagestats.AuthMethodFor(
			cfg.AccessToken != "",
			cfg.IDToken != "",
			cfg.APIKey != "",
			cfg.BasicAuth != nil,
		),
	}
}

// effectiveTLSEnabled reports whether the server will actually serve HTTPS.
// Both HTTP transports (SSE and streamable-http) pass the TLS config to
// runHTTPServer; stdio does not serve HTTP at all.
func effectiveTLSEnabled(transport string, tls tlsConfig) bool {
	return (transport == "streamable-http" || transport == "sse") && tls.certFile != ""
}

// effectiveMetricsEnabled reports whether /metrics is actually served.
// --metrics still builds a meter provider under stdio, but registerOps is only
// called from the HTTP branches, so no route exists to scrape.
func effectiveMetricsEnabled(transport string, metricsEnabled bool) bool {
	return metricsEnabled && transport != "stdio"
}

// nativeToolNames is the set of tool names the server registered itself. It
// bounds the tool names the usage-statistics reporter may emit, so it must be
// taken before any proxied tool is registered: those names come from a remote
// MCP server and collapse to a single pseudo-name instead.
func nativeToolNames(dt disabledTools) map[string]struct{} {
	bare := mcp.NewServer(&mcp.Implementation{Name: "enumerator", Version: "0"}, nil)
	dt.processTools(bare)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = bare.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "tool-enumerator", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		slog.Warn("failed to enumerate native tools for usage stats", "error", err)
		return nil
	}
	defer func() { _ = session.Close() }()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		slog.Warn("failed to list native tools for usage stats", "error", err)
		return nil
	}
	names := make(map[string]struct{}, len(result.Tools))
	for _, t := range result.Tools {
		names[t.Name] = struct{}{}
	}
	return names
}

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, hsc httpSecurityConfig, ca callerAuthConfig, obs observability.Config, us usagestats.Config, healthzAddress, instructionsAppend string) error {
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

	if lp := o.LoggerProvider(); lp != nil {
		otlpHandler := otelslog.NewHandler(defaultServerName, otelslog.WithLoggerProvider(lp))
		slog.SetDefault(slog.New(observability.NewFanoutHandler(stderrHandler, otlpHandler)))
		slog.Info("OTLP log export configured", "endpoint", observability.OTLPLogsEndpoint())
	}

	if o.TracerProvider() != nil {
		slog.Info("OTLP trace export configured", "endpoint", observability.OTLPTracesEndpoint())
	}

	gc.MeterProvider = o.MeterProvider()

	if (tls.certFile == "") != (tls.keyFile == "") {
		return fmt.Errorf("incomplete TLS configuration: both --server.tls-cert-file and --server.tls-key-file must be provided together")
	}

	var clientCache *mcpgrafana.ClientCache
	if transport != "stdio" {
		clientCache = mcpgrafana.NewClientCache(nil, mcpgrafana.WithClientCacheMeterProvider(o.MeterProvider()))
		defer clientCache.Close()
	}

	// The reporter supplies the server's session and tool-call hooks, so it is
	// built before the server; the tool-name allowlist it needs only exists
	// once the server has registered its tools, and is handed over below.
	us.Transport = transport
	// Normalise first: processTools and buildInstructions each do this to their
	// own copy of dt, rewriting the deprecated category aliases and
	// clearing the target's disable flag when appropriate. Reading dt before
	// that reported the flags as typed rather than the categories the server
	// actually registered.
	dt.normalizeEnabledTools()
	us.EnabledTools, us.DisabledTools = dt.categoryReport()
	us.TLSEnabled = effectiveTLSEnabled(transport, tls)
	us.MetricsEnabled = effectiveMetricsEnabled(transport, obs.MetricsEnabled)
	us.DynamicMultiOrg = mcpgrafana.DynamicMultiOrgEnabled
	us.Target = grafanaTarget
	// The other half of us.Flags: flag.Visit sees only flags, and container
	// deployments configure almost entirely by environment variable.
	us.EnvSet = usagestats.EnvSet(os.Getenv)
	usage := usagestats.New(us)
	usage.Disclose()

	s := newServer(obs.ServerName, dt, o, usage, instructionsAppend)
	usage.SetNativeTools(nativeToolNames(dt))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Registered after the observability shutdown so it runs before it:
	// the final flush logs through the handler o.Shutdown removes.
	defer usage.Shutdown()
	usage.Start(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		<-sigChan
		slog.Info("Received shutdown signal")
		cancel()

		if transport == "stdio" {
			_ = os.Stdin.Close()
		}
	}()

	callerToken := ca.resolveToken()
	if transport == "sse" || transport == "streamable-http" {
		checkCallerAuthPolicy(transport, addr, callerToken, slog.Default())
		if callerToken != "" && mcpgrafana.ForwardsAuthorizationHeader() {
			return fmt.Errorf("refusing to start: caller authentication is enabled (--server-auth-token / %s) while GRAFANA_FORWARD_HEADERS forwards the Authorization header. Authorization is reserved for MCP caller authentication and would leak to Grafana. Remove Authorization from GRAFANA_FORWARD_HEADERS, or unset the caller token to run in proxy-forwarding mode", serverAuthTokenEnvVar)
		}
	}

	disableLocalhostProtection := len(splitAndTrim(hsc.allowedHosts)) > 0

	switch transport {
	case "stdio":
		cf := mcpgrafana.ComposedStdioContextFunc(gc)
		ctx = cf(ctx)
		if mcpgrafana.DynamicMultiOrgEnabled {
			s.AddReceivingMiddleware(mcpgrafana.OrgIDOverrideMiddleware())
		}
		s.AddReceivingMiddleware(recoveryMiddleware())

		slog.Info("Starting Grafana MCP server using stdio transport", "version", mcpgrafana.Version())

		err := s.Run(ctx, &mcp.StdioTransport{})
		if err != nil && !isBenignStdioClose(err) && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("server error: %v", err)
		}
		return nil

	case "sse":
		// TODO(go-sdk): The go-sdk SSE transport does not populate
		// RequestExtra.Header on message POSTs, so GrafanaContextMiddleware
		// cannot read per-request credentials. This is a regression: the
		// previous mark3labs SDK ran WithSSEContextFunc on each POST with the
		// full http.Request. GrafanaContextMiddleware now falls back to
		// environment credentials for SSE connections.
		// SSE is deprecated in the MCP spec (superseded by streamable-http,
		// which propagates headers correctly). Fixing this for SSE requires
		// either upstream go-sdk support or per-connection server instances.
		httpFn := mcpgrafana.ComposedHTTPContextFunc(gc, clientCache)
		// OrgID must be registered before GrafanaContext: AddReceivingMiddleware
		// wraps the previous handler, so the last-registered middleware is outermost
		// (runs first). OrgID needs to run after GrafanaContext has populated the
		// config, so it must be innermost (registered first).
		if mcpgrafana.DynamicMultiOrgEnabled {
			s.AddReceivingMiddleware(mcpgrafana.OrgIDOverrideMiddleware())
		}
		s.AddReceivingMiddleware(mcpgrafana.GrafanaContextMiddleware(httpFn))
		s.AddReceivingMiddleware(recoveryMiddleware())

		sseHandler := mcp.NewSSEHandler(func(_ *http.Request) *mcp.Server { return s }, &mcp.SSEOptions{
			DisableLocalhostProtection: disableLocalhostProtection,
		})

		mux := http.NewServeMux()
		// The go-sdk SSEHandler serves the SSE stream at its mount path.
		// Default to "/sse" for backwards compatibility (mark3labs served
		// at {basePath}/sse internally); a non-empty --base-path is used
		// as the mount prefix.
		ssePath := "/sse"
		if basePath != "" {
			ssePath = strings.TrimRight(basePath, "/") + "/sse"
		}
		mux.Handle(ssePath, corsMiddleware(hsc.corsOrigins(), withCallerAuth(callerToken, observability.WrapHandler(
			mcpgrafana.ValidateGrafanaURLMiddleware(sseHandler), //nolint:staticcheck // Retained temporarily to reject malformed legacy headers.
			ssePath,
		))))
		runOpsServers(registerOps(mux, o, healthzAddress, obs))

		httpSrv := &http.Server{
			Addr:    addr,
			Handler: mcpgrafana.DNSRebindingProtectionMiddleware(hsc.policy(addr))(mux),
			// Derive HTTP request contexts from the cancellable ctx so that
			// SIGTERM cancels long-lived SSE streams and Shutdown completes
			// within its deadline instead of blocking on open connections.
			BaseContext: func(_ net.Listener) context.Context { return ctx },
		}
		slog.Info("Starting Grafana MCP server using SSE transport",
			"version", mcpgrafana.Version(), "address", addr, "ssePath", ssePath, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, httpSrv, "SSE", tls.certFile, tls.keyFile)

	case "streamable-http":
		httpFn := mcpgrafana.ComposedHTTPContextFunc(gc, clientCache)
		if mcpgrafana.DynamicMultiOrgEnabled {
			s.AddReceivingMiddleware(mcpgrafana.OrgIDOverrideMiddleware())
		}
		s.AddReceivingMiddleware(mcpgrafana.GrafanaContextMiddleware(httpFn))
		s.AddReceivingMiddleware(recoveryMiddleware())

		streamHandler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
			Stateless:                  true,
			JSONResponse:               true,
			DisableLocalhostProtection: disableLocalhostProtection,
		})

		mux := http.NewServeMux()
		mux.Handle(endpointPath, corsMiddleware(hsc.corsOrigins(), withCallerAuth(callerToken, observability.WrapHandler(
			mcpgrafana.ValidateGrafanaURLMiddleware(streamHandler), //nolint:staticcheck // Retained temporarily to reject malformed legacy headers.
			endpointPath,
		))))
		runOpsServers(registerOps(mux, o, healthzAddress, obs))

		httpSrv := &http.Server{
			Addr:        addr,
			Handler:     mcpgrafana.DNSRebindingProtectionMiddleware(hsc.policy(addr))(mux),
			BaseContext: func(_ net.Listener) context.Context { return ctx },
		}
		slog.Info("Starting Grafana MCP server using StreamableHTTP transport",
			"version", mcpgrafana.Version(), "address", addr, "endpointPath", endpointPath, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, httpSrv, "StreamableHTTP", tls.certFile, tls.keyFile)

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
	_ = flag.Int("session-idle-timeout-minutes", 30, "Deprecated: the official go-sdk manages sessions internally. This flag is ignored.")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	instructionsAppend := flag.String("instructions-append", "", "Text appended to the server instructions returned to MCP clients on initialize, so every connecting agent sees it.")
	usageStatsMode := flag.String("usage-stats", "", "Anonymous usage statistics reporting: 'enabled', 'disabled', or 'log' to print the report that would be sent to stderr and send nothing. Overrides the "+usagestats.ModeEnvVar+" environment variable, which in turn overrides "+usagestats.DoNotTrackEnvVar+"; any unrecognised value disables reporting. See https://grafana.com/docs/grafana/latest/developer-resources/mcp/anonymous-usage-statistics/")
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
	setFlagNames := []string{}
	flag.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
		setFlagNames = append(setFlagNames, f.Name)
	})

	// Flag NAMES only: --server-name, --instructions-append and the address
	// flags all carry operator-chosen text, so no flag value is reported.
	usageStats := usagestats.Config{
		Mode:     usagestats.ResolveMode(*usageStatsMode, setFlags["usage-stats"], os.Getenv(usagestats.ModeEnvVar), os.Getenv(usagestats.DoNotTrackEnvVar)),
		Endpoint: usagestats.ResolveEndpoint(os.Getenv(usagestats.EndpointEnvVar)),
		Version:  mcpgrafana.Version(),
		Flags:    setFlagNames,
	}

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

	mcpgrafana.DynamicMultiOrgEnabled = gc.dynamicMultiOrg

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

	enforcedMatchers, err := tools.ParseEnforcedMatchers(gc.lokiEnforcedMatchers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --loki-enforced-matchers: %v\n", err)
		os.Exit(2)
	}
	if len(enforcedMatchers) > 0 {
		grafanaConfig.LokiEnforcedMatchers = enforcedMatchers
		switch gc.lokiLabelEnumerationFallback {
		case tools.LabelEnumFallbackReject, tools.LabelEnumFallbackUnfiltered:
			grafanaConfig.LokiLabelEnumerationFallback = gc.lokiLabelEnumerationFallback
		default:
			fmt.Fprintf(os.Stderr, "invalid --loki-label-enumeration-fallback %q: must be %q or %q\n", gc.lokiLabelEnumerationFallback, tools.LabelEnumFallbackReject, tools.LabelEnumFallbackUnfiltered)
			os.Exit(2)
		}
		warnLokiEnforcementBypasses(dt)
	}

	obs.ServerName = serverName
	obs.ServerVersion = mcpgrafana.Version()

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

	if err := run(transport, *addr, *basePath, *endpointPath, level, dt, grafanaConfig, tls, hsc, ca, obs, usageStats, *healthzAddress, *instructionsAppend); err != nil {
		slog.Error("Fatal error", "error", err)
		os.Exit(1)
	}
}

func parseLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}
	return l
}

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

type flagAction int

const (
	flagActionUnset flagAction = iota
	flagActionContinue
	flagActionVersion
	flagActionInvalidSlowLevel
)

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
