package usagestats

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/grafana/mcp-grafana/v2/observability"
)

// Report reasons. Every event carries exactly one.
const (
	// ReasonInterval is a flush triggered by the periodic timer.
	ReasonInterval = "interval"
	// ReasonShutdown is the final flush as the process exits.
	ReasonShutdown = "shutdown"
)

// ServiceName identifies the reporting product to the receiver.
const ServiceName = "mcp-grafana"

// Target kinds for the Grafana instance the process talks to. Deliberately
// coarse: never the URL, hostname, stack slug, org name or org ID.
const (
	TargetKindCloud      = "cloud"
	TargetKindSelfHosted = "self_hosted"
)

// Authentication method vocabulary. Never a credential, and never a
// configured value.
const (
	AuthMethodOnBehalfOf          = "on_behalf_of"
	AuthMethodAccessToken         = "access_token"
	AuthMethodServiceAccountToken = "service_account_token"
	AuthMethodBasicAuth           = "basic_auth"
	AuthMethodAnonymous           = "anonymous"
)

// authMethods bounds AuthMethod, so a value added to the vocabulary without
// being documented reports as observability.ValueOther rather than travelling
// unannounced.
var authMethods = observability.ValueSet(
	AuthMethodOnBehalfOf,
	AuthMethodAccessToken,
	AuthMethodServiceAccountToken,
	AuthMethodBasicAuth,
	AuthMethodAnonymous,
)

// maxVersionLen caps grafana_version, which comes from outside this binary.
// It has no vocabulary to clamp against — a version is whatever the instance
// calls itself, and forks and internal builds legitimately use strings like
// "12.1.0-acmecorp.4+g1a2b3c-internal" — so the length is the only bound
// available, and without it the reporting instance can push arbitrarily large
// text into a typed column and into the stored raw payload.
//
// Only the length is bounded. A pattern match would be the wrong tool: it
// would discard legitimate versions to no benefit, since the risk here is
// volume, not shape.
const maxVersionLen = 64

// truncateRunes cuts s to at most limit bytes without splitting a rune.
// Cutting mid-rune would leave invalid UTF-8 that json.Marshal silently
// rewrites to U+FFFD, corrupting the tail of an otherwise valid version.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// ToolCount is the per-tool delta recorded since the previous flush.
type ToolCount struct {
	Calls  int64 `json:"calls"`
	Errors int64 `json:"errors"`
}

// Event is the wire contract. The json tags ARE the contract: every one of
// them is listed in docs/sources/anonymous-usage-statistics.md and asserted by
// TestWireFields, so a field cannot be added, renamed or removed without the
// docs page and the startup notice being updated alongside it.
//
// The unit is the PROCESS, not the MCP session. Protocol version 2026-07-28
// removed protocol-level sessions (SEP-2567), so mcp-go registers no session
// for a client using it and a per-session event would never be built at all on
// the streamable-http transport — see the Reporter doc comment. Tool-call
// hooks still fire for those requests, so the counts below are aggregated over
// every session and every sessionless request the process served.
//
// Fields are flat scalars apart from ToolCalls, which the receiver stores as a
// JSON column.
type Event struct {
	// Envelope.
	Service         string `json:"service"`
	Version         string `json:"version"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	ProcessID       string `json:"process_id"`
	ReportReason    string `json:"report_reason"`
	ProcessUptimeMS int64  `json:"process_uptime_ms"`

	// ReportSeq numbers this process's reports from 1, so a gap in the
	// sequence for a process_id identifies a report that was built and lost.
	// Counters reset on send rather than on acknowledgement, so a lost report
	// takes its delta with it; without this the loss is invisible and totals
	// look like counts rather than the floor they are.
	ReportSeq int64 `json:"report_seq"`

	// Tool usage since the previous flush, aggregated over the whole process.
	// Both are omitted when no tool was called in this window, so "no tools
	// called" reads as NULL rather than as an empty string and an empty
	// object.
	ToolsCalled string               `json:"tools_called,omitempty"`
	ToolCalls   map[string]ToolCount `json:"tool_calls,omitempty"`

	// The Grafana instance the process talked to, described without
	// identifying it.
	//
	// GrafanaVersion is the instance's own string, capped at maxVersionLen.
	// It and AuthMethod are omitted entirely when the process resolved more
	// than one distinct value — a multi-tenant HTTP process can serve several
	// — so absence means "no single value" rather than inventing a "mixed"
	// sentinel that a reader could mistake for a real one.
	GrafanaVersion string `json:"grafana_version,omitempty"`
	TargetKind     string `json:"target_kind,omitempty"`
	AuthMethod     string `json:"auth_method,omitempty"`

	// Server configuration, by name and resolved state only. No flag value
	// an operator can type free text into is included.
	Transport string `json:"transport"`
	Flags     string `json:"flags"`

	// EnvSet is the sorted names of the configuration environment variables
	// that are set, from the fixed inventory in configEnvVars. Names only,
	// never values. It is the other half of Flags: flag.Visit sees only
	// flags, so without this a container deployment reports almost no
	// configuration at all.
	EnvSet          string `json:"env_set"`
	EnabledTools    string `json:"enabled_tools"`
	DisabledTools   string `json:"disabled_tools"`
	TLSEnabled      bool   `json:"tls_enabled"`
	MetricsEnabled  bool   `json:"metrics_enabled"`
	DynamicMultiOrg bool   `json:"dynamic_multi_org"`
	ProxiedEnabled  bool   `json:"proxied_enabled"`
}

// GrafanaTarget describes the Grafana instance a request talks to, as read
// from the resolved configuration in its context.
//
// URL is consumed by TargetKind and then discarded: it never reaches an Event,
// and no field derived from it is finer-grained than cloud/self_hosted.
type GrafanaTarget struct {
	URL        string
	Version    string
	AuthMethod string
}

// grafanaCloudHostSuffixes are the only positive signals for a Grafana
// Cloud stack that can be read from configuration alone: the public stack
// domain plus the Grafana-operated ops and dev domains. A Cloud instance
// reached through a custom domain therefore reports self_hosted; see the docs
// page, which states the misclassification rather than implying target_kind
// is authoritative.
var grafanaCloudHostSuffixes = []string{".grafana.net", ".grafana-ops.net", ".grafana-dev.net"}

// TargetKind classifies a Grafana URL as cloud or self-hosted. An empty or
// unparseable URL returns "", meaning no target was resolved, which is
// distinct from self_hosted.
func TargetKind(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range grafanaCloudHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return TargetKindCloud
		}
	}
	return TargetKindSelfHosted
}

// AuthMethodFor names the credential category a connection resolved, in the
// same precedence order the Grafana client's auth round-tripper applies.
func AuthMethodFor(accessTokenSet, idTokenSet, serviceAccountTokenSet, basicAuthSet bool) string {
	switch {
	case accessTokenSet && idTokenSet:
		return AuthMethodOnBehalfOf
	case accessTokenSet:
		return AuthMethodAccessToken
	case serviceAccountTokenSet:
		return AuthMethodServiceAccountToken
	case basicAuthSet:
		return AuthMethodBasicAuth
	default:
		return AuthMethodAnonymous
	}
}

// soleValue returns the single member of values, or "" when the set is empty
// or holds more than one.
//
// This is how a field that can resolve differently per request is reported at
// process scope: one value travels, several mean the field is omitted. Absence
// is the honest answer — a "mixed" sentinel would sit in the same column as
// real values and be counted as one.
func soleValue(values map[string]struct{}) string {
	if len(values) != 1 {
		return ""
	}
	for v := range values {
		return v
	}
	return ""
}

// Transport values for Event.Transport, the complete wire vocabulary.
const (
	TransportStdio          = "stdio"
	TransportSSE            = "sse"
	TransportStreamableHTTP = "streamable-http"
)

// transports bounds Event.Transport. The flag is validated before the server
// starts, but that validation runs after the reporter is built and the
// shutdown flush is deferred, so an invalid --transport would otherwise reach
// the wire verbatim on the error path — and an operator who mistypes it can
// put anything there, including a hostname. Clamping here rather than
// reordering the validation keeps the guarantee if that order changes again.
var transports = observability.ValueSet(TransportStdio, TransportSSE, TransportStreamableHTTP)

// configEnvVars is the inventory for Event.EnvSet: every environment variable
// this server reads its own configuration from. Names only ever travel, never
// values, and only a name from this list — it is a fixed inventory compiled
// into the binary, not a scan of the process environment, so an unrelated
// variable cannot be reported even by accident.
//
// Flags alone under-report configuration badly: Event.Flags comes from
// flag.Visit, and container and Kubernetes deployments configure almost
// entirely by environment variable, so for those the flags field is close to
// empty however the server is set up.
//
// Credential-bearing names are included because the name says only that a
// credential of that kind was supplied, which auth_method already reports.
var configEnvVars = []string{
	"GRAFANA_API_KEY",
	"GRAFANA_EXTRA_HEADERS",
	"GRAFANA_FORWARD_HEADERS",
	"GRAFANA_LEGACY_8_URL",
	"GRAFANA_LOKI_GUARDRAIL_MAX_BYTES",
	"GRAFANA_LOKI_GUARDRAIL_MAX_RANGE",
	"GRAFANA_LOKI_GUARDRAIL_MODE",
	"GRAFANA_MCP_SERVER_NAME",
	"GRAFANA_ORG_ID",
	"GRAFANA_PASSWORD",
	"GRAFANA_SERVICE_ACCOUNT_TOKEN",
	"GRAFANA_SERVICE_ACCOUNT_TOKEN_FILE",
	"GRAFANA_SOCKS5_PROXY",
	"GRAFANA_URL",
	"GRAFANA_USAGE_STATS",
	"GRAFANA_USAGE_STATS_ENDPOINT",
	"GRAFANA_USERNAME",
	"MCP_GRAFANA_SERVER_TOKEN",
}

// EnvSet returns the sorted names of the configuration environment variables
// that are set to a non-empty value, as a comma-joined list. Values are never
// read for reporting - only whether each name is present.
func EnvSet(lookup func(string) string) string {
	var set []string
	for _, name := range configEnvVars {
		if strings.TrimSpace(lookup(name)) != "" {
			set = append(set, name)
		}
	}
	return strings.Join(set, ",")
}
