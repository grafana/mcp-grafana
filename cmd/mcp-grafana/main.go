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
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/observability"
	"github.com/grafana/mcp-grafana/v2/resources"
	"github.com/grafana/mcp-grafana/v2/tools"
	"github.com/grafana/mcp-grafana/v2/usagestats"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
)

const (
	defaultServerName      = "mcp-grafana"
	dynamicAddressFileName = "mcp-grafana-address"
)

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

func (dt *disabledTools) disabledByFlag() map[string]bool {
	return map[string]bool{
		"search":        dt.search,
		"datasource":    dt.datasource,
		"incident":      dt.incident,
		"prometheus":    dt.prometheus,
		"loki":          dt.loki,
		"elasticsearch": dt.elasticsearch,
		"quickwit":      dt.quickwit,
		"influxdb":      dt.influxdb,
		"alerting":      dt.alerting,
		"dashboard":     dt.dashboard,
		"folder":        dt.folder,
		"oncall":        dt.oncall,
		"asserts":       dt.asserts,
		"admin":         dt.admin,
		"pyroscope":     dt.pyroscope,
		"navigation":    dt.navigation,
		"tempo":         dt.tempo,
		"annotations":   dt.annotations,
		"rendering":     dt.rendering,
		"snapshot":      dt.snapshot,
		"cloudwatch":    dt.cloudwatch,
		"cloudlogging":  dt.cloudlogging,
		"azuremonitor":  dt.azuremonitor,
		"examples":      dt.examples,
		"sql":           dt.sql,
		"runpanelquery": dt.runpanelquery,
		"graphite":      dt.graphite,
		"dsquery":       dt.dsquery,
		"plugin":        dt.plugin,
		"api":           dt.api,
		"config":        dt.config,
		"provisioning":  dt.provisioning,
		"agento11y":     dt.agento11y,
		"assistant":     dt.assistant,
		"docs":          dt.docs,
		"user":          dt.user,
	}
}

// honouredWriteToolOverrides are the names --enable-write-tools acted on
// before every tool moved into the catalog.
var honouredWriteToolOverrides = []string{
	"alerting_rules_write", "alerting_routing_write", "alerting_silences_write",
	"query_sql", "query_influxdb", "query_datasource", "generate_deeplink",
}

func (dt *disabledTools) policy() tools.Policy {
	dt.normalizeEnabledTools()
	disabled := dt.disabledByFlag()
	var p tools.Policy
	for _, name := range strings.Split(dt.enabledTools, ",") {
		if !disabled[name] {
			p.Categories = append(p.Categories, name)
		}
	}
	p.ReadOnly = dt.write
	p.NoQuery = dt.query
	for _, name := range strings.Split(dt.writeToolOverrides, ",") {
		if name = strings.TrimSpace(name); slices.Contains(honouredWriteToolOverrides, name) {
			p.Allow = append(p.Allow, name)
		}
	}
	if dt.enableQuery {
		p.Allow = append(p.Allow, tools.MutatingQueryToolNames()...)
	}
	return p
}

type disabledTools struct {
	enabledTools string

	writeToolOverrides string

	search, datasource, incident,
	prometheus, loki, elasticsearch, quickwit, influxdb, alerting,
	dashboard, folder, oncall, asserts, admin,
	pyroscope, navigation, tempo, annotations, rendering, cloudwatch, cloudlogging, azuremonitor, write, query, enableQuery,
	snapshot, examples, sql, graphite, dsquery,
	runpanelquery, plugin, api, config, provisioning,
	agento11y, assistant, docs, user bool
}

type grafanaConfig struct {
	debug bool

	// Request-selected Grafana targets for HTTP transports.
	allowURLOverride          bool
	allowedURLs               string
	allowCrossOriginRedirects bool

	// TLS configuration
	tlsCertFile   string
	tlsKeyFile    string
	tlsCAFile     string
	tlsSkipVerify bool

	maxLokiLogLimit int

	lokiGuardrailMode      string
	lokiGuardrailMaxBytes  int64
	lokiGuardrailMaxRange  time.Duration
	lokiGuardrailOnFailure string

	prometheusGuardrailRequireSelectiveLabels bool
	prometheusGuardrailMode                   string
	prometheusGuardrailMaxRange               time.Duration
	prometheusGuardrailMaxPoints              int64
	prometheusGuardrailMaxMetricCardinality   int64
	prometheusGuardrailCardinalityCacheTTL    time.Duration
	prometheusGuardrailCardinalityScopeLabels string

	includeArgsInSpans bool

	timeout time.Duration

	dynamicMultiOrg bool

	prometheusEnforcedMatchers   string
	lokiEnforcedMatchers         string
	lokiLabelEnumerationFallback string

	dashboardSchema string
}

func (dt *disabledTools) addFlags() {
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,snapshot,plugin,api,config,provisioning,docs,user", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")
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
	flag.BoolVar(&dt.admin, "disable-admin", false, "Disable admin tools")
	flag.BoolVar(&dt.pyroscope, "disable-pyroscope", false, "Disable pyroscope tools")
	flag.BoolVar(&dt.navigation, "disable-navigation", false, "Disable navigation tools")
	flag.BoolVar(&dt.tempo, "disable-tempo", false, "Disable Tempo tracing tools")
	flag.BoolVar(&dt.tempo, "disable-proxied", false, "Deprecated: use --disable-tempo instead")
	flag.BoolVar(&dt.write, "disable-write", false, "Disable write tools (create/update operations)")
	flag.BoolVar(&dt.query, "disable-query", false, "Disable query tools (tools that execute a query against a datasource, e.g. query_prometheus, query_loki_logs, run_panel_query). Metadata and discovery tools stay available.")
	flag.BoolVar(&dt.enableQuery, "enable-query", false, "Keep the raw-SQL query tools (query_sql, query_influxdb) registered even under --disable-write, and keep query_datasource, a source of render_grafana_app, available. They pass the query through unfiltered, so they can mutate data if the datasource credentials permit it; use this when those credentials are known to be read-only. Has no effect if --disable-query is also set. For the raw-SQL tools it is equivalent to --enable-write-tools=query_sql,query_influxdb; kept as a shorthand for that common case.")
	flag.StringVar(&dt.writeToolOverrides, "enable-write-tools", "", "Comma separated list of individual tool names to keep registered even under --disable-write, for tools whose write behavior is scoped enough to opt back in independently. Has no effect on a tool whose whole category is disabled.")
	flag.BoolVar(&dt.annotations, "disable-annotations", false, "Disable annotation tools")
	flag.BoolVar(&dt.rendering, "disable-rendering", false, "Disable rendering tools (panel/dashboard image export)")
	flag.BoolVar(&dt.snapshot, "disable-snapshot", false, "Disable snapshot tools")
	flag.BoolVar(&dt.cloudwatch, "disable-cloudwatch", false, "Disable CloudWatch tools")
	flag.BoolVar(&dt.cloudlogging, "disable-cloudlogging", false, "Disable Google Cloud Logging tools")
	flag.BoolVar(&dt.azuremonitor, "disable-azuremonitor", false, "Disable Azure Monitor tools")
	flag.BoolVar(&dt.examples, "disable-examples", false, "Disable query examples tools")
	flag.BoolVar(&dt.sql, "disable-sql", false, "Disable SQL tools (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL, BigQuery)")
	flag.BoolVar(&dt.sql, "disable-clickhouse", false, "Deprecated: use --disable-sql instead")
	flag.BoolVar(&dt.sql, "disable-snowflake", false, "Deprecated: use --disable-sql instead")
	flag.BoolVar(&dt.sql, "disable-athena", false, "Deprecated: use --disable-sql instead")
	flag.BoolVar(&dt.runpanelquery, "disable-runpanelquery", false, "Disable run panel query tools")
	flag.BoolVar(&dt.graphite, "disable-graphite", false, "Disable Graphite tools")
	flag.BoolVar(&dt.dsquery, "disable-dsquery", false, "Disable the query_datasource source of render_grafana_app")
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
	flag.BoolVar(&gc.allowURLOverride, "allow-grafana-url-override", false, "Enable X-Grafana-URL selection for HTTP/SSE requests. Falls back to GRAFANA_ALLOW_URL_OVERRIDE. Without --allowed-grafana-urls, callers may target any HTTP(S) URL reachable by this server.")
	flag.StringVar(&gc.allowedURLs, "allowed-grafana-urls", "", "Optional comma-separated exact Grafana base URLs allowed when --allow-grafana-url-override is enabled. Falls back to GRAFANA_ALLOWED_URLS.")
	flag.BoolVar(&gc.allowCrossOriginRedirects, "allow-cross-origin-redirects", false, "Allow Grafana HTTP clients to follow redirects to a different scheme, host, or port. Falls back to GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS. This can expose Grafana credentials to the redirect target.")

	flag.StringVar(&gc.tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file for client authentication")
	flag.StringVar(&gc.tlsKeyFile, "tls-key-file", "", "Path to TLS private key file for client authentication")
	flag.StringVar(&gc.tlsCAFile, "tls-ca-file", "", "Path to TLS CA certificate file for server verification")
	flag.BoolVar(&gc.tlsSkipVerify, "tls-skip-verify", false, "Skip TLS certificate verification (insecure)")

	flag.IntVar(&gc.maxLokiLogLimit, "max-loki-log-limit", tools.MaxLokiLogLimit, "Maximum number of log lines returned per query_loki_logs call")

	flag.StringVar(&gc.lokiGuardrailMode, "loki-guardrail-mode", mcpgrafana.LokiGuardrailOff, "Loki query cost guardrail mode for query_loki_logs and query_loki_patterns: 'off' (default), 'shadow' (evaluate and log queries that would be blocked, but let them run; still pays the index/stats round trip, including for queries a static check already blocks), or 'enforce' (reject blocked queries with rewrite guidance). Falls back to the GRAFANA_LOKI_GUARDRAIL_MODE environment variable when the flag is not set.")
	flag.Int64Var(&gc.lokiGuardrailMaxBytes, "loki-guardrail-max-bytes", 100<<30, "Maximum bytes a single query_loki_logs call may scan, estimated via Loki's index/stats API before running the query (query_loki_patterns is checked for selector and time range only, since patterns are served by the pattern ingesters). 0 disables the byte-budget check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_MAX_BYTES environment variable when the flag is not set.")
	flag.StringVar(&gc.lokiGuardrailOnFailure, "loki-guardrail-on-estimate-failure", mcpgrafana.LokiGuardrailFailureAllow, "What the Loki guardrail does when it cannot assess a query (no stream selector it understands, or the index/stats estimate failed): 'allow' (default) runs the query, 'block' rejects it in enforce mode. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_ON_ESTIMATE_FAILURE environment variable when the flag is not set.")
	flag.DurationVar(&gc.lokiGuardrailMaxRange, "loki-guardrail-max-range", 24*time.Hour, "Maximum effective time range for a single query_loki_logs or query_loki_patterns call, including range-vector durations like [30d]. Accepts Go duration strings, e.g. 24h. 0 disables the range check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_LOKI_GUARDRAIL_MAX_RANGE environment variable when the flag is not set.")
	flag.StringVar(&gc.prometheusGuardrailMode, "prometheus-guardrail-mode", mcpgrafana.PrometheusGuardrailOff, "Prometheus query guardrail mode for query_prometheus, query_prometheus_histogram and the Prometheus queries of run_panel_query: 'off' (default), 'shadow' (evaluate and log queries that would be blocked, but let them run), or 'enforce' (reject blocked queries with rewrite guidance). Falls back to the GRAFANA_PROMETHEUS_GUARDRAIL_MODE environment variable when the flag is not set.")
	flag.BoolVar(&gc.prometheusGuardrailRequireSelectiveLabels, "prometheus-guardrail-require-selective-labels", false, "Require a non-empty positive label constraint beyond the metric name in each PromQL vector selector. Blanket regexes and negative matchers do not provide scope. Independent of range and points limits; only applies when the guardrail is not 'off'. Falls back to GRAFANA_PROMETHEUS_GUARDRAIL_REQUIRE_SELECTIVE_LABELS when the flag is not set.")
	flag.DurationVar(&gc.prometheusGuardrailMaxRange, "prometheus-guardrail-max-range", 192*time.Hour, "Maximum effective time range for a single PromQL query: its time range plus the longest range vector or subquery window in the expression. Accepts Go duration strings, e.g. 192h. 0 disables the range check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_PROMETHEUS_GUARDRAIL_MAX_RANGE environment variable when the flag is not set.")
	flag.Int64Var(&gc.prometheusGuardrailMaxPoints, "prometheus-guardrail-max-points", 11000, "Maximum points per series a PromQL range query may request, computed from its time range and step. 0 disables the points check. Only applies when the guardrail is not 'off'. Falls back to the GRAFANA_PROMETHEUS_GUARDRAIL_MAX_POINTS environment variable when the flag is not set.")
	flag.StringVar(&gc.prometheusEnforcedMatchers, "prometheus-enforced-matchers", "", "PromQL label matchers AND-ed into every native-Prometheus query to restrict readable series (e.g. `namespace!~\"vault|payments\"`). Queries that cannot be parsed are rejected. Requires --disable-api to be effective, otherwise it can be bypassed via the raw datasource proxy.")

	flag.Int64Var(&gc.prometheusGuardrailMaxMetricCardinality, "prometheus-guardrail-max-metric-cardinality", 0, "Require an exact scope-label matcher for known metrics above this TSDB series count. 0 disables this best-effort heuristic (default); only active when the guardrail is not 'off'. Falls back to GRAFANA_PROMETHEUS_GUARDRAIL_MAX_METRIC_CARDINALITY.")
	flag.DurationVar(&gc.prometheusGuardrailCardinalityCacheTTL, "prometheus-guardrail-cardinality-cache-ttl", 10*time.Minute, "TTL for Prometheus TSDB cardinality snapshots. 0 uses the default 10m. Falls back to GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_CACHE_TTL.")
	flag.StringVar(&gc.prometheusGuardrailCardinalityScopeLabels, "prometheus-guardrail-cardinality-scope-labels", "job,instance,namespace,pod,service,cluster", "Comma-separated labels whose non-empty exact matchers satisfy the cardinality policy. Empty uses the default job,instance,namespace,pod,service,cluster. Falls back to GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_SCOPE_LABELS.")

	flag.StringVar(&gc.lokiEnforcedMatchers, "loki-enforced-matchers", "", "LogQL label matchers AND-ed into every native-Loki query to restrict readable streams (e.g. `namespace!~\"vault|payments\"`). Queries that cannot be parsed are rejected. Requires --disable-api to be effective, otherwise it can be bypassed via the raw datasource proxy.")
	flag.StringVar(&gc.lokiLabelEnumerationFallback, "loki-label-enumeration-fallback", tools.LabelEnumFallbackReject, "Behaviour of list_loki_label_names/list_loki_label_values when --loki-enforced-matchers cannot scope them (purely-negative matchers only): 'reject' (fail closed) or 'unfiltered' (allow unscoped enumeration of label metadata; never exposes log lines).")

	flag.StringVar(&gc.dashboardSchema, "dashboard-schema", "", "Set to 'v2' to make update_dashboard reject a classic v1 (panels[]) body that would create a new dashboard, when the connected Grafana serves dashboard schema v2. The error carries a minimal v2 body to retry with. Existing v1 dashboards stay editable, and a Grafana without v2 is unaffected. Empty (default) changes nothing. Falls back to the GRAFANA_DASHBOARD_SCHEMA environment variable when the flag is not set.")

	flag.BoolVar(&gc.includeArgsInSpans, "include-args-in-spans", false, "Include tool call arguments in OpenTelemetry spans. Only enable in non-production environments or when arguments are known not to contain PII.")
	flag.DurationVar(&gc.timeout, "grafana-timeout", 0, "Time limit for requests made by the Grafana client. Accepts Go duration strings, e.g. 10s, 500ms. Unset, it is 30s.")

	flag.BoolVar(&gc.dynamicMultiOrg, "dynamic-multi-org", false, "Allow tool calls to select a Grafana organization per call via an optional orgId argument (org is otherwise fixed at connection startup). Adds an orgId argument to every tool's schema.")
}

// applyGrafanaURLOverrideEnv uses the same flag-over-environment precedence as
// the other Grafana options. An explicitly empty allowlist flag clears an
// inherited list; an explicitly false switch disables an inherited true value.
func (gc *grafanaConfig) applyGrafanaURLOverrideEnv(setFlags map[string]bool) error {
	if !setFlags["allow-grafana-url-override"] {
		if raw, ok := os.LookupEnv("GRAFANA_ALLOW_URL_OVERRIDE"); ok {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("invalid GRAFANA_ALLOW_URL_OVERRIDE: %w", err)
			}
			gc.allowURLOverride = value
		}
	}
	if !setFlags["allowed-grafana-urls"] {
		gc.allowedURLs = os.Getenv("GRAFANA_ALLOWED_URLS")
	}
	return nil
}

func (gc *grafanaConfig) applyRedirectEnv(setFlags map[string]bool) error {
	if setFlags["allow-cross-origin-redirects"] {
		return nil
	}
	if raw, ok := os.LookupEnv("GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS"); ok {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS: %w", err)
		}
		gc.allowCrossOriginRedirects = value
	}
	return nil
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
	if v := os.Getenv("GRAFANA_LOKI_GUARDRAIL_ON_ESTIMATE_FAILURE"); v != "" && !setFlags["loki-guardrail-on-estimate-failure"] {
		gc.lokiGuardrailOnFailure = v
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

// applyPrometheusGuardrailEnv fills guardrail settings from
// GRAFANA_PROMETHEUS_GUARDRAIL_* environment variables for flags not set on
// the command line, with the same precedence as applyLokiGuardrailEnv.
func (gc *grafanaConfig) applyPrometheusGuardrailEnv(setFlags map[string]bool) error {
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_MODE"); v != "" && !setFlags["prometheus-guardrail-mode"] {
		gc.prometheusGuardrailMode = v
	}
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_MAX_RANGE"); v != "" && !setFlags["prometheus-guardrail-max-range"] {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_PROMETHEUS_GUARDRAIL_MAX_RANGE %q: %w", v, err)
		}
		gc.prometheusGuardrailMaxRange = d
	}
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_MAX_POINTS"); v != "" && !setFlags["prometheus-guardrail-max-points"] {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_PROMETHEUS_GUARDRAIL_MAX_POINTS %q: %w", v, err)
		}
		gc.prometheusGuardrailMaxPoints = n
	}
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_REQUIRE_SELECTIVE_LABELS"); v != "" && !setFlags["prometheus-guardrail-require-selective-labels"] {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_PROMETHEUS_GUARDRAIL_REQUIRE_SELECTIVE_LABELS %q: %w", v, err)
		}
		gc.prometheusGuardrailRequireSelectiveLabels = b
	}
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_MAX_METRIC_CARDINALITY"); v != "" && !setFlags["prometheus-guardrail-max-metric-cardinality"] {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_PROMETHEUS_GUARDRAIL_MAX_METRIC_CARDINALITY %q: %w", v, err)
		}
		gc.prometheusGuardrailMaxMetricCardinality = n
	}
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_CACHE_TTL"); v != "" && !setFlags["prometheus-guardrail-cardinality-cache-ttl"] {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_CACHE_TTL %q: %w", v, err)
		}
		gc.prometheusGuardrailCardinalityCacheTTL = d
	}
	if v := os.Getenv("GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_SCOPE_LABELS"); v != "" && !setFlags["prometheus-guardrail-cardinality-scope-labels"] {
		gc.prometheusGuardrailCardinalityScopeLabels = v
	}

	return nil
}

// applyDashboardSchemaEnv fills --dashboard-schema from GRAFANA_DASHBOARD_SCHEMA
// when the flag is not set, with the same precedence as applyLokiGuardrailEnv,
// and rejects any value other than empty or v2.
func (gc *grafanaConfig) applyDashboardSchemaEnv(setFlags map[string]bool) error {
	if v := os.Getenv("GRAFANA_DASHBOARD_SCHEMA"); v != "" && !setFlags["dashboard-schema"] {
		gc.dashboardSchema = v
	}
	if gc.dashboardSchema != "" && gc.dashboardSchema != mcpgrafana.DashboardSchemaV2 {
		return fmt.Errorf("invalid dashboard schema %q (--dashboard-schema or GRAFANA_DASHBOARD_SCHEMA): must be v2 or empty", gc.dashboardSchema)
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
	switch gc.lokiGuardrailOnFailure {
	case "", mcpgrafana.LokiGuardrailFailureAllow, mcpgrafana.LokiGuardrailFailureBlock:
	default:
		return fmt.Errorf("invalid Loki guardrail estimate-failure behaviour %q (--loki-guardrail-on-estimate-failure or GRAFANA_LOKI_GUARDRAIL_ON_ESTIMATE_FAILURE): must be one of allow, block", gc.lokiGuardrailOnFailure)
	}
	if gc.lokiGuardrailMaxBytes < 0 {
		return fmt.Errorf("invalid Loki guardrail max bytes %d (--loki-guardrail-max-bytes or GRAFANA_LOKI_GUARDRAIL_MAX_BYTES): must be >= 0 (0 disables the byte-budget check)", gc.lokiGuardrailMaxBytes)
	}
	if gc.lokiGuardrailMaxRange < 0 {
		return fmt.Errorf("invalid Loki guardrail max range %s (--loki-guardrail-max-range or GRAFANA_LOKI_GUARDRAIL_MAX_RANGE): must be >= 0 (0 disables the range check)", gc.lokiGuardrailMaxRange)
	}
	return nil
}

func (gc *grafanaConfig) validatePrometheusGuardrail() error {
	switch gc.prometheusGuardrailMode {
	case mcpgrafana.PrometheusGuardrailOff, mcpgrafana.PrometheusGuardrailShadow, mcpgrafana.PrometheusGuardrailEnforce:
	default:
		return fmt.Errorf("invalid Prometheus guardrail mode %q (--prometheus-guardrail-mode or GRAFANA_PROMETHEUS_GUARDRAIL_MODE): must be one of off, shadow, enforce", gc.prometheusGuardrailMode)
	}
	if gc.prometheusGuardrailMaxRange < 0 {
		return fmt.Errorf("invalid Prometheus guardrail max range %s (--prometheus-guardrail-max-range or GRAFANA_PROMETHEUS_GUARDRAIL_MAX_RANGE): must be >= 0 (0 disables the range check)", gc.prometheusGuardrailMaxRange)
	}
	if gc.prometheusGuardrailMaxPoints < 0 {
		return fmt.Errorf("invalid Prometheus guardrail max points %d (--prometheus-guardrail-max-points or GRAFANA_PROMETHEUS_GUARDRAIL_MAX_POINTS): must be >= 0 (0 disables the points check)", gc.prometheusGuardrailMaxPoints)
	}
	if gc.prometheusGuardrailMaxMetricCardinality < 0 {
		return fmt.Errorf("invalid Prometheus guardrail max metric cardinality %d (--prometheus-guardrail-max-metric-cardinality or GRAFANA_PROMETHEUS_GUARDRAIL_MAX_METRIC_CARDINALITY): must be >= 0 (0 disables the check)", gc.prometheusGuardrailMaxMetricCardinality)
	}
	if gc.prometheusGuardrailCardinalityCacheTTL < 0 {
		return fmt.Errorf("invalid Prometheus guardrail cardinality cache TTL %s (--prometheus-guardrail-cardinality-cache-ttl or GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_CACHE_TTL): must be >= 0 (0 uses 10m)", gc.prometheusGuardrailCardinalityCacheTTL)
	}
	if slices.Contains(gc.cardinalityScopeLabels(), "__name__") {
		return fmt.Errorf("invalid Prometheus guardrail cardinality scope labels (--prometheus-guardrail-cardinality-scope-labels or GRAFANA_PROMETHEUS_GUARDRAIL_CARDINALITY_SCOPE_LABELS): __name__ is not a scope label")
	}

	return nil
}

// cardinalityScopeLabels returns nil for an empty setting, selecting the
// library defaults. Trim whitespace and duplicates for predictable matching.
func (gc *grafanaConfig) cardinalityScopeLabels() []string {
	var labels []string
	for _, label := range strings.Split(gc.prometheusGuardrailCardinalityScopeLabels, ",") {
		label = strings.TrimSpace(label)
		if label != "" && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return labels
}

// resourceKinds returns the MCP resource kinds to serve: each follows its
// tool category.
func (dt *disabledTools) resourceKinds() []resources.Kind {
	categories := dt.policy().Categories
	var kinds []resources.Kind
	if slices.Contains(categories, "datasource") {
		kinds = append(kinds, resources.Datasources)
	}
	if slices.Contains(categories, "incident") {
		kinds = append(kinds, resources.ActiveIncidents)
	}
	return kinds
}

// categoryReport lists the tool categories that are actually active and the
// ones a --disable-* flag turned off, for the usage-statistics enabled_tools /
// disabled_tools fields. The two are not complements: a category this build
// knows about that is neither named in --enabled-tools nor explicitly disabled
// appears in neither list.
//
// Both lists are bounded by the tool catalog, so neither can carry an
// operator-supplied string — a category named in --enabled-tools that this
// build does not know about is reported by neither.
func (dt *disabledTools) categoryReport() (enabled, disabled []string) {
	active := dt.activeCategories()
	flagged := dt.disabledByFlag()
	for _, name := range tools.CategoryNames() {
		switch {
		case active[name]:
			enabled = append(enabled, name)
		case flagged[name]:
			disabled = append(disabled, name)
		}
	}
	return enabled, disabled
}

func (dt *disabledTools) activeCategories() map[string]bool {
	active := map[string]bool{}
	for _, c := range tools.Resolve(dt.policy()).Categories {
		active[c.Name] = true
	}
	return active
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
	if dt.query && dt.enableQuery {
		slog.Warn("--enable-query has no effect because --disable-query is set; no query tools will be registered")
	}
	p := dt.policy()
	disabled := dt.disabledByFlag()
	for _, name := range strings.Split(dt.enabledTools, ",") {
		if disabled[name] {
			slog.Info("Disabling tools", "category", name)
		}
	}
	tools.Resolve(p).Register(s)
}

func (dt *disabledTools) buildInstructions() string {
	var capabilities []string
	for _, c := range tools.Resolve(dt.policy()).Categories {
		capabilities = append(capabilities, c.Description)
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

// sseKeepAliveFailureThreshold is how many keepalive pings in a row a client
// may miss before its session is closed. go-sdk closes on the first miss by
// default, so one slow reply would end a session the pings exist to keep.
const sseKeepAliveFailureThreshold = 3

// minSSEKeepAlive is the shortest --sse-keepalive accepted. Each ping must be
// answered within half the interval, so a tiny value (a typo like 1ms)
// would disconnect clients almost at once.
const minSSEKeepAlive = time.Second

// keepAlive is the interval between pings the server sends to each connected
// client; 0 disables them. run() passes a non-zero value only for the SSE
// transport (see effectiveSSEKeepAlive).
func newServer(serverName string, dt disabledTools, obs *observability.Observability, usage *usagestats.Reporter, instructionsAppend string, keepAlive time.Duration) *mcp.Server {
	instructions := appendInstructions(dt.buildInstructions(), instructionsAppend)

	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: mcpgrafana.Version()}, &mcp.ServerOptions{
		Instructions:              instructions,
		KeepAlive:                 keepAlive,
		KeepAliveFailureThreshold: sseKeepAliveFailureThreshold,
	})

	dt.processTools(s)
	mcpgrafana.RegisterAppResources(s)
	resources.Register(s, dt.resourceKinds()...)

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

func validateGrafanaURLOverridePolicy(transport string, cfg mcpgrafana.GrafanaConfig) error {
	if transport != "sse" && transport != "streamable-http" {
		return nil
	}
	if len(cfg.AllowedGrafanaURLs) > 0 && !cfg.AllowGrafanaURLOverride {
		return fmt.Errorf("--allowed-grafana-urls requires --allow-grafana-url-override")
	}
	return nil
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

func (hsc httpSecurityConfig) policy(addresses ...string) mcpgrafana.HostOriginPolicy {
	hosts := splitAndTrim(hsc.allowedHosts)
	if len(hosts) == 0 {
		for _, address := range addresses {
			candidates := append([]string{strings.ToLower(address)}, mcpgrafana.DefaultAllowedHosts(address)...)
			for _, host := range candidates {
				if !slices.Contains(hosts, host) {
					hosts = append(hosts, host)
				}
			}
		}
	}
	return mcpgrafana.HostOriginPolicy{
		AllowedHosts:   hosts,
		AllowedOrigins: splitAndTrim(hsc.allowedOrigins),
	}
}

// warnPrometheusEnforcementBypasses logs, at startup, every enabled tool through
// which an LLM could reach Prometheus metric data WITHOUT going through the
// enforced Prometheus backend — so --prometheus-enforced-matchers would not apply.
func warnPrometheusEnforcementBypasses(dt disabledTools) {
	active := dt.activeCategories()
	for _, b := range []struct{ category, flag, reason string }{
		{"api", "--disable-api", "grafana_api_request can query the Prometheus datasource proxy directly, fully bypassing enforcement"},
		{"rendering", "--disable-rendering", "get_panel_image renders Prometheus panels server-side via the Grafana renderer, producing images that contain unrestricted metrics"},
		{"dsquery", "--disable-dsquery", "render_grafana_app can use query_datasource to send unfiltered Prometheus queries"},
		{"assistant", "--disable-assistant", "ask_assistant delegates to Grafana Assistant, which reads Prometheus server-side across all series; enforced matchers are not applied to what it reports back"},
	} {
		if active[b.category] {
			slog.Warn("Prometheus label-matcher enforcement can be bypassed by an enabled tool",
				"disable_with", b.flag, "reason", b.reason)
		}
	}
	if active["snapshot"] {
		slog.Info("Prometheus label-matcher enforcement: dashboard snapshots can return panel data captured outside enforcement (e.g. pre-existing snapshots); disable with --disable-snapshot if snapshots may contain restricted metrics")
	}
}

// warnLokiEnforcementBypasses logs, at startup, every enabled tool through which
// an LLM could reach Loki log data WITHOUT going through the enforced Loki
// backend — so --loki-enforced-matchers would not apply. Each line names the
// mechanism and the flag that closes it.
func warnLokiEnforcementBypasses(dt disabledTools) {
	active := dt.activeCategories()
	for _, b := range []struct{ category, flag, reason string }{
		{"api", "--disable-api", "grafana_api_request can query the Loki datasource proxy directly, fully bypassing enforcement"},
		{"rendering", "--disable-rendering", "get_panel_image renders Loki panels server-side via the Grafana renderer, producing images that contain unrestricted log lines"},
		{"assistant", "--disable-assistant", "ask_assistant delegates to Grafana Assistant, which reads Loki server-side across all streams; enforced matchers are not applied to what it reports back"},
	} {
		if active[b.category] {
			slog.Warn("Loki label-matcher enforcement can be bypassed by an enabled tool",
				"disable_with", b.flag, "reason", b.reason)
		}
	}
	if active["snapshot"] {
		slog.Info("Loki label-matcher enforcement: dashboard snapshots can return log-panel data captured outside enforcement (e.g. pre-existing snapshots); disable with --disable-snapshot if snapshots may contain restricted logs")
	}
}

// corsMiddleware adds CORS headers when the request Origin matches an allowed
// origin. The official go-sdk sets no CORS headers (unlike mark3labs which had
// built-in CORS support), so this is needed for browser-based MCP clients.
//
// The preflight allowlist covers the MCP protocol headers plus the Grafana
// request headers the server documents for per-request URL, credential and
// org selection, so browser clients on an allowed origin can use them too.
var corsAllowedHeaders = strings.Join([]string{
	"Content-Type", "Authorization", "Mcp-Session-Id", "MCP-Protocol-Version", "Last-Event-ID",
	"X-Grafana-URL", "X-Grafana-Service-Account-Token", "X-Grafana-API-Key", "X-Grafana-Org-Id",
}, ", ")

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
		// The response depends on Origin, so shared caches must key on it.
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || allowedSet[strings.ToLower(origin)]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
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

// runHTTPServer starts an *http.Server on an already-bound listener and blocks
// until ctx is cancelled or the server returns an error. On cancellation it
// performs a graceful shutdown with a 5-second deadline.
func runHTTPServer(
	ctx context.Context,
	srv *http.Server,
	listener net.Listener,
	transportName string,
	tls tlsConfig,
) error {
	serverErr := make(chan error, 1)
	go func() {
		var err error
		if tls.certFile != "" {
			err = srv.ServeTLS(listener, tls.certFile, tls.keyFile)
		} else {
			err = srv.Serve(listener)
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

func listenHTTP(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", address, err)
	}
	if _, port, splitErr := net.SplitHostPort(address); splitErr == nil && port == "0" {
		if err := writeBoundAddress(dynamicAddressFileName, listener.Addr().String()); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	return listener, nil
}

func addressWithBoundPort(address, boundAddress string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return boundAddress
	}
	_, port, err := net.SplitHostPort(boundAddress)
	if err != nil {
		return boundAddress
	}
	return net.JoinHostPort(host, port)
}

func writeBoundAddress(path, address string) error {
	if path == "" {
		return nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create address file beside %q: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.WriteString(address + "\n"); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write bound address to %q: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close bound address file %q: %w", temporaryPath, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish bound address file %q: %w", path, err)
	}
	return nil
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

// validateSSEKeepAlive rejects a negative --sse-keepalive, or one below
// minSSEKeepAlive. 0 disables pings.
func validateSSEKeepAlive(interval time.Duration) error {
	if interval < 0 {
		return fmt.Errorf("invalid --sse-keepalive %s: must be >= 0 (0 disables keepalive pings)", interval)
	}
	if interval > 0 && interval < minSSEKeepAlive {
		return fmt.Errorf("invalid --sse-keepalive %s: must be 0 (disabled) or at least %s", interval, minSSEKeepAlive)
	}
	return nil
}

// effectiveSSEKeepAlive returns the ping interval the MCP server is built with.
// Pings exist to stop reverse proxies from closing the SSE transport's idle
// event stream, which deletes the session, so other transports ignore the flag.
func effectiveSSEKeepAlive(transport string, interval time.Duration) time.Duration {
	if transport != "sse" {
		return 0
	}
	return interval
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

// normalizeBasePath canonicalises --base-path to "" (server root) or to
// "/prefix" without a trailing slash, so every mux pattern derived from it
// agrees.
func normalizeBasePath(basePath string) string {
	joined := path.Join("/", basePath)
	if joined == "/" {
		return ""
	}
	return joined
}

// newHTTPMux builds the mux shared by the SSE and streamable-http transports,
// carrying the MCP handler only. The operational endpoints are added by
// registerOps, which decides per endpoint whether it stays on this mux or
// moves to its own listener.
//
// mcpPattern is the ServeMux pattern the MCP handler is mounted on.
// callerToken, when non-empty, gates the MCP handler behind bearer auth — the
// operational endpoints stay open so probes and scrapers keep working.
// corsOrigins wraps the MCP handler in CORS handling outside the auth check,
// so browser preflights are answered without a token.
//
// /healthz and /metrics are internal-only endpoints consumed by
// infrastructure (liveness probes, scrapers), not by MCP clients, so
// registerOps mounts them at the server root only — never under --base-path.
// Putting them on the same prefix as the application endpoints would make it
// harder for a reverse proxy to expose the API while keeping these internal.
func newHTTPMux(mcpPattern string, mcpHandler http.Handler, callerToken string, corsOrigins []string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(mcpPattern, corsMiddleware(corsOrigins, withCallerAuth(callerToken, observability.WrapHandler(mcpHandler, mcpPattern))))
	return mux
}

// newSSEMux mounts the SSE handler at <base>/sse.
//
// basePath must already be normalized by the caller (normalizeBasePath).
func newSSEMux(mcpHandler http.Handler, basePath, callerToken string, corsOrigins []string) *http.ServeMux {
	return newHTTPMux(sseEndpointPath(basePath), mcpHandler, callerToken, corsOrigins)
}

// sseEndpointPath is where the SSE handler listens. The go-sdk SSEHandler
// serves both the event stream (GET) and the session's message POSTs
// (?sessionid=...) on the path it is mounted at, so one exact mount covers
// the whole transport.
func sseEndpointPath(basePath string) string {
	return basePath + "/sse"
}

// newStreamableHTTPMux mounts the streamable-http handler.
//
// mcpEndpoint must already be computed by the caller (streamableEndpointPath).
func newStreamableHTTPMux(mcpHandler http.Handler, mcpEndpoint, callerToken string, corsOrigins []string) *http.ServeMux {
	return newHTTPMux(mcpEndpoint, mcpHandler, callerToken, corsOrigins)
}

// streamableEndpointPath is where the streamable-http server listens once
// --base-path is taken into account.
func streamableEndpointPath(basePath, endpointPath string) string {
	joined := path.Join("/", basePath, endpointPath)
	// path.Join drops a trailing slash, but to ServeMux that slash is the
	// difference between an exact mount and a subtree. --endpoint-path=/mcp/
	// mounted a subtree before --base-path reached this transport, and a
	// client pointed at that URL must keep working, so carry the slash over.
	if joined != "/" && strings.HasSuffix(endpointPath, "/") {
		joined += "/"
	}
	return joined
}

// operationalMounts lists the patterns registerOps mounts on the MCP
// listener alongside the MCP handler. Each is only there when it has no
// address of its own: --healthz-address and --metrics-address move the
// endpoint to a side listener, and metrics off means nothing is mounted at
// all — in those cases the path is free for the MCP handler.
func operationalMounts(healthzAddress string, obs observability.Config) []string {
	var mounts []string
	if healthzAddress == "" {
		mounts = append(mounts, "/healthz")
	}
	if obs.MetricsEnabled && obs.MetricsAddress == "" {
		mounts = append(mounts, "/metrics")
	}
	return mounts
}

// checkMountPattern rejects a pattern ServeMux would not serve as the literal
// path it was written as. ServeMux reads a pattern as "[METHOD ]/path", so a
// space or tab turns the first segment into a method and panics on
// registration; '{' opens a wildcard segment, which registers fine but then
// matches paths nobody asked for.
func checkMountPattern(pattern string) error {
	i := strings.IndexAny(pattern, " \t{")
	if i < 0 {
		return nil
	}
	return fmt.Errorf("resolves to %q, which is not a route path: %q cannot appear in one", pattern, pattern[i])
}

// validateMountFlags rejects --base-path / --endpoint-path values the HTTP
// transports could not serve. ServeMux panics both on a malformed pattern and
// on the duplicate registration a reserved path would cause, so these are
// caught at flag-parsing time rather than half-way through starting a server.
//
// The checks run on the resolved pattern, not on the raw flags: --endpoint-path
// is joined with --base-path and cleaned on the way to the mux, so "healthz",
// "/healthz/" and "/foo/../metrics" all reach an operational path too.
func validateMountFlags(transport, basePath, endpointPath, healthzAddress string, obs observability.Config) error {
	raw := basePath
	basePath = normalizeBasePath(basePath)
	// "..", "." and "/foo/../.." all clean to the server root, so a prefix the
	// operator wrote would silently become no prefix at all — with the MCP
	// routes then served at every path their reverse proxy exposes. An empty
	// flag is the honest way to ask for that.
	if basePath == "" && strings.Trim(raw, "/") != "" {
		return fmt.Errorf("invalid --base-path: %q resolves to the server root; leave it unset to serve without a prefix", raw)
	}
	switch transport {
	case "sse":
		// The SSE handler is mounted at <base>/sse, which can never equal one
		// of the operational paths — only syntax can go wrong.
		if err := checkMountPattern(sseEndpointPath(basePath)); err != nil {
			return fmt.Errorf("invalid --base-path: %w", err)
		}
	case "streamable-http":
		flags := "--endpoint-path"
		if basePath != "" {
			flags = "--base-path with --endpoint-path"
		}
		// Empty is not "the default": it mounts the MCP handler on the whole
		// listener, answering every path that nothing else claims. ServeMux
		// used to reject the empty pattern outright; keep that loud.
		if endpointPath == "" {
			return fmt.Errorf("invalid %s: cannot be empty; use %q to serve MCP at the server root", flags, "/")
		}
		mcpEndpoint := streamableEndpointPath(basePath, endpointPath)
		if err := checkMountPattern(mcpEndpoint); err != nil {
			return fmt.Errorf("invalid %s: %w", flags, err)
		}
		if slices.Contains(operationalMounts(healthzAddress, obs), mcpEndpoint) {
			return fmt.Errorf("invalid %s: resolves to %q, which is served at the server root as an operational endpoint", flags, mcpEndpoint)
		}
	}
	return nil
}

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, hsc httpSecurityConfig, ca callerAuthConfig, obs observability.Config, us usagestats.Config, healthzAddress, instructionsAppend string, sseKeepAlive time.Duration) error {
	// --base-path feeds every mux pattern, so normalize it once here and let
	// every mux mount read the same value. main() has already rejected values
	// these mounts could not serve.
	basePath = normalizeBasePath(basePath)
	callerToken := ca.resolveToken()
	if err := validateGrafanaURLOverridePolicy(transport, gc); err != nil {
		return err
	}

	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(stderrHandler))

	o, err := observability.Setup(obs)
	if err != nil {
		return fmt.Errorf("failed to setup observability: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := o.Shutdown(shutdownCtx); err != nil {
			slog.Warn("observability shutdown incomplete (telemetry endpoint may be unreachable)", "error", err)
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
	us.HTTPTransport = gc.ProxyOnlyTransport()
	usage := usagestats.New(us)
	usage.Disclose()

	keepAlive := effectiveSSEKeepAlive(transport, sseKeepAlive)
	if keepAlive != sseKeepAlive {
		slog.Warn("--sse-keepalive only applies to the SSE transport; ignoring it", "transport", transport)
	}
	s := newServer(obs.ServerName, dt, o, usage, instructionsAppend, keepAlive)
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

	if transport == "sse" || transport == "streamable-http" {
		checkCallerAuthPolicy(transport, addr, callerToken, slog.Default())
		mcpgrafana.WarnIfOrgIDUnset(slog.Default())
		if gc.AllowGrafanaURLOverride && len(gc.AllowedGrafanaURLs) == 0 {
			slog.Error("SECURITY: unrestricted Grafana URL overrides are enabled. Every MCP caller can make requests to arbitrary HTTP(S) targets reachable by this server, including internal services. Use an authenticating proxy that authorizes targets and supplies matching tokens, and restrict outbound network access. An allowlist alone is not a substitute for these controls.")
		}
		if callerToken != "" && mcpgrafana.ForwardsAuthorizationHeader() {
			return fmt.Errorf("refusing to start: caller authentication is enabled (--server-auth-token / %s) while GRAFANA_FORWARD_HEADERS forwards the Authorization header. Authorization is reserved for MCP caller authentication and would leak to Grafana. Remove Authorization from GRAFANA_FORWARD_HEADERS, or unset the caller token to run in proxy-forwarding mode", serverAuthTokenEnvVar)
		}
	}

	var listener net.Listener
	boundAddress := addr
	if transport == "sse" || transport == "streamable-http" {
		listener, err = listenHTTP(addr)
		if err != nil {
			return err
		}
		defer func() { _ = listener.Close() }()
		boundAddress = listener.Addr().String()
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
		// environment credentials for SSE connections. For the same reason
		// X-Grafana-URL overrides cannot work over SSE: the target selected
		// on the stream's GET carries through, but the caller's token on each
		// POST does not, so those calls fail closed without credentials.
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

		ssePath := sseEndpointPath(basePath)
		mux := newSSEMux(
			mcpgrafana.GrafanaURLOverrideMiddleware(gc.AllowGrafanaURLOverride, gc.AllowedGrafanaURLs, sseHandler),
			basePath,
			callerToken,
			hsc.corsOrigins(),
		)
		runOpsServers(registerOps(mux, o, healthzAddress, obs))

		httpSrv := &http.Server{
			Handler: mcpgrafana.DNSRebindingProtectionMiddleware(
				hsc.policy(boundAddress, addressWithBoundPort(addr, boundAddress)),
			)(mux),
			// Derive HTTP request contexts from the cancellable ctx so that
			// SIGTERM cancels long-lived SSE streams and Shutdown completes
			// within its deadline instead of blocking on open connections.
			BaseContext: func(_ net.Listener) context.Context { return ctx },
		}
		slog.Info("Starting Grafana MCP server using SSE transport",
			"version", mcpgrafana.Version(), "address", boundAddress, "ssePath", ssePath, "keepAlive", keepAlive, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, httpSrv, listener, "SSE", tls)

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

		// --base-path is documented for this transport too, so fold it into the
		// endpoint the server listens on.
		mcpEndpoint := streamableEndpointPath(basePath, endpointPath)
		mux := newStreamableHTTPMux(
			mcpgrafana.GrafanaURLOverrideMiddleware(gc.AllowGrafanaURLOverride, gc.AllowedGrafanaURLs, streamHandler),
			mcpEndpoint,
			callerToken,
			hsc.corsOrigins(),
		)
		runOpsServers(registerOps(mux, o, healthzAddress, obs))

		httpSrv := &http.Server{
			Handler: mcpgrafana.DNSRebindingProtectionMiddleware(
				hsc.policy(boundAddress, addressWithBoundPort(addr, boundAddress)),
			)(mux),
			BaseContext: func(_ net.Listener) context.Context { return ctx },
		}
		slog.Info("Starting Grafana MCP server using StreamableHTTP transport",
			"version", mcpgrafana.Version(), "address", boundAddress, "endpointPath", mcpEndpoint, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, httpSrv, listener, "StreamableHTTP", tls)

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
	basePath := flag.String("base-path", "", "Base path for the sse or streamable-http server. /healthz and /metrics are always served at the root, not under this prefix")
	endpointPath := flag.String("endpoint-path", "/mcp", "Endpoint path for the streamable-http server, appended to --base-path")
	sseKeepAlive := flag.Duration("sse-keepalive", 0, "Interval between ping requests sent to each SSE client, so reverse proxies and load balancers do not close an idle event stream (and the session with it). Set it below the proxy's idle timeout, e.g. 30s behind ingress-nginx's default 60s proxy-read-timeout. A client is disconnected after 3 pings in a row go unanswered for half the interval each. Accepts Go duration strings, minimum 1s. 0 (the default) disables pings. Only applies to --transport sse.")
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
	if err := gc.applyPrometheusGuardrailEnv(setFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gc.applyDashboardSchemaEnv(setFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gc.applyGrafanaURLOverrideEnv(setFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gc.applyRedirectEnv(setFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gc.validateLokiGuardrail(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if gc.lokiGuardrailMode != mcpgrafana.LokiGuardrailOff {
		slog.Info("Loki guardrail enabled", "mode", gc.lokiGuardrailMode, "max_bytes", gc.lokiGuardrailMaxBytes, "max_range", gc.lokiGuardrailMaxRange, "on_estimate_failure", gc.lokiGuardrailOnFailure)
	}
	if err := gc.validatePrometheusGuardrail(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if gc.prometheusGuardrailMode != mcpgrafana.PrometheusGuardrailOff {
		slog.Info("Prometheus guardrail enabled", "mode", gc.prometheusGuardrailMode, "max_range", gc.prometheusGuardrailMaxRange, "max_points", gc.prometheusGuardrailMaxPoints, "require_selective_labels", gc.prometheusGuardrailRequireSelectiveLabels, "max_metric_cardinality", gc.prometheusGuardrailMaxMetricCardinality, "cardinality_cache_ttl", gc.prometheusGuardrailCardinalityCacheTTL, "cardinality_scope_labels", gc.cardinalityScopeLabels())
	}

	if err := validateMountFlags(transport, *basePath, *endpointPath, *healthzAddress, obs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := validateSSEKeepAlive(*sseKeepAlive); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	socks5Proxy, err := socks5ProxyFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	mcpgrafana.DynamicMultiOrgEnabled = gc.dynamicMultiOrg

	grafanaConfig := mcpgrafana.GrafanaConfig{
		Debug:                                     gc.debug,
		AllowGrafanaURLOverride:                   gc.allowURLOverride,
		AllowCrossOriginRedirects:                 gc.allowCrossOriginRedirects,
		MaxLokiLogLimit:                           gc.maxLokiLogLimit,
		LokiGuardrailMode:                         gc.lokiGuardrailMode,
		LokiGuardrailMaxBytes:                     gc.lokiGuardrailMaxBytes,
		LokiGuardrailMaxRange:                     gc.lokiGuardrailMaxRange,
		LokiGuardrailOnEstimateFailure:            gc.lokiGuardrailOnFailure,
		PrometheusGuardrailRequireSelectiveLabels: gc.prometheusGuardrailRequireSelectiveLabels,
		PrometheusGuardrailMode:                   gc.prometheusGuardrailMode,
		PrometheusGuardrailMaxRange:               gc.prometheusGuardrailMaxRange,
		PrometheusGuardrailMaxPoints:              gc.prometheusGuardrailMaxPoints,
		PrometheusGuardrailMaxMetricCardinality:   gc.prometheusGuardrailMaxMetricCardinality,
		PrometheusGuardrailCardinalityCacheTTL:    gc.prometheusGuardrailCardinalityCacheTTL,
		PrometheusGuardrailCardinalityScopeLabels: gc.cardinalityScopeLabels(),
		IncludeArgumentsInSpans:                   gc.includeArgsInSpans,
		Timeout:                                   gc.timeout,
		SOCKS5ProxyURL:                            socks5Proxy,
		DashboardSchema:                           gc.dashboardSchema,
	}
	grafanaConfig.AllowedGrafanaURLs, err = mcpgrafana.ParseGrafanaURLOverrides(gc.allowedURLs)
	if err != nil {
		source := "--allowed-grafana-urls"
		if !setFlags["allowed-grafana-urls"] {
			source = "GRAFANA_ALLOWED_URLS"
		}
		fmt.Fprintf(os.Stderr, "invalid %s: %v\n", source, err)
		os.Exit(2)
	}
	if gc.tlsCertFile != "" || gc.tlsKeyFile != "" || gc.tlsCAFile != "" || gc.tlsSkipVerify {
		grafanaConfig.TLSConfig = &mcpgrafana.TLSConfig{
			CertFile:   gc.tlsCertFile,
			KeyFile:    gc.tlsKeyFile,
			CAFile:     gc.tlsCAFile,
			SkipVerify: gc.tlsSkipVerify,
		}
	}

	promEnforcedMatchers, err := tools.ParseEnforcedMatchers(gc.prometheusEnforcedMatchers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --prometheus-enforced-matchers: %v\n", err)
		os.Exit(2)
	}
	if len(promEnforcedMatchers) > 0 {
		grafanaConfig.PrometheusEnforcedMatchers = promEnforcedMatchers
		warnPrometheusEnforcementBypasses(dt)
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

	if err := run(transport, *addr, *basePath, *endpointPath, level, dt, grafanaConfig, tls, hsc, ca, obs, usageStats, *healthzAddress, *instructionsAppend, *sseKeepAlive); err != nil {
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
