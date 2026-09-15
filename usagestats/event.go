package usagestats

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/grafana/mcp-grafana/observability"
)

// Report reasons. Every event carries exactly one.
const (
	// ReasonSessionEnd is a flush triggered by the MCP session ending, either
	// mid-life (the client disconnected) or during server shutdown.
	ReasonSessionEnd = "session_end"
	// ReasonInterval is a flush triggered by the periodic timer, for sessions
	// that are still live.
	ReasonInterval = "interval"
)

// ServiceName identifies the reporting product to the receiver.
const ServiceName = "mcp-grafana"

// Target kinds for the Grafana instance a session talks to. Deliberately
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

// maxVersionLen caps every version string that comes from outside this binary:
// the MCP client's reported version and the Grafana instance's reported one.
// Neither has a vocabulary to clamp against — a version is whatever the thing
// reporting it calls itself, and forks and internal builds legitimately use
// strings like "12.1.0-acmecorp.4+g1a2b3c-internal" — so the length is the
// only bound available, and without it either side can push arbitrarily large
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
// Fields are flat scalars apart from ToolCalls, which the receiver stores as a
// JSON column.
type Event struct {
	// Envelope.
	Service           string `json:"service"`
	Version           string `json:"version"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	ProcessID         string `json:"process_id"`
	SessionID         string `json:"session_id"`
	ReportReason      string `json:"report_reason"`
	SessionDurationMS int64  `json:"session_duration_ms"`

	// Client, from the initialize request. ClientName is clamped to a
	// vocabulary (see ClientName); ClientVersion is the client's own string,
	// capped at maxVersionLen, and is omitted entirely unless the name matched
	// — so "unrecognised client" reads as NULL rather than as an empty string.
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version,omitempty"`

	// Tool usage since the previous flush. Both are omitted when the session
	// called no tools in this window, so "no tools called" reads as NULL
	// rather than as an empty string and an empty object.
	ToolsCalled string               `json:"tools_called,omitempty"`
	ToolCalls   map[string]ToolCount `json:"tool_calls,omitempty"`

	// The Grafana instance the session talked to, described without
	// identifying it. GrafanaVersion is the instance's own string, capped at
	// maxVersionLen for the same reason ClientVersion is.
	GrafanaVersion string `json:"grafana_version"`
	TargetKind     string `json:"target_kind"`
	OrgIDSet       bool   `json:"org_id_set"`
	AuthMethod     string `json:"auth_method"`

	// Server configuration, by name and resolved state only. No flag value
	// an operator can type free text into is included.
	Transport         string `json:"transport"`
	Flags             string `json:"flags"`
	EnabledTools      string `json:"enabled_tools"`
	DisabledTools     string `json:"disabled_tools"`
	LokiGuardrailMode string `json:"loki_guardrail_mode"`
	TLSEnabled        bool   `json:"tls_enabled"`
	MetricsEnabled    bool   `json:"metrics_enabled"`
	DynamicMultiOrg   bool   `json:"dynamic_multi_org"`
	ProxiedEnabled    bool   `json:"proxied_enabled"`
}

// GrafanaTarget describes the Grafana instance a session talks to, as read
// from the session's resolved configuration.
//
// URL is consumed by TargetKind and then discarded: it never reaches an Event,
// and no field derived from it is finer-grained than cloud/self_hosted.
type GrafanaTarget struct {
	URL        string
	Version    string
	AuthMethod string
	OrgIDSet   bool
}

// grafanaCloudHostSuffix is the only positive signal for a Grafana Cloud
// stack that can be read from configuration alone. A Cloud instance reached
// through a custom domain therefore reports self_hosted; see the docs page,
// which states the misclassification rather than implying target_kind is
// authoritative.
const grafanaCloudHostSuffix = ".grafana.net"

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
	if strings.HasSuffix(strings.ToLower(u.Hostname()), grafanaCloudHostSuffix) {
		return TargetKindCloud
	}
	return TargetKindSelfHosted
}

// AuthMethodFor names the credential category a session resolved, in the same
// precedence order the Grafana client's auth round-tripper applies.
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
