// Package usagestats reports anonymous usage statistics about the MCP server
// itself to Grafana Labs, so the team can see which tools are used, which
// error, and which transports and configurations are in the field.
//
// Every value that leaves this package is either a constant, a number, a
// boolean, a random UUID, or a string clamped to a vocabulary declared here
// and shared with observability's metric-label allowlist. Nothing derived from
// an operator's Grafana instance, credentials, tool arguments, or free-text
// flags is sent — see docs/sources/anonymous-usage-statistics.md, which is the
// user-facing contract and is kept in step with Event by TestWireFields.
package usagestats

import "strings"

// Mode is the resolved reporting mode.
type Mode string

const (
	// ModeEnabled builds an event per MCP session and POSTs it to the endpoint.
	ModeEnabled Mode = "enabled"
	// ModeDisabled builds nothing and opens no connection.
	ModeDisabled Mode = "disabled"
	// ModeLog prints the event that would be sent to stderr and sends nothing.
	ModeLog Mode = "log"
)

// DefaultMode is the mode used when neither the flag nor the environment
// variable selects one.
//
// It is ModeDisabled in this release because the receiving endpoint is not
// live yet: the default flips to ModeEnabled in a separate release once
// grafana/usage-stats is accepting mcp-grafana reports, so that no build ever
// ships pointing at an endpoint that does not exist.
const DefaultMode = ModeDisabled

const (
	// ModeEnvVar selects the reporting mode. The --usage-stats flag wins over it.
	ModeEnvVar = "GRAFANA_USAGE_STATS"
	// DoNotTrackEnvVar disables reporting when set to "1", following the
	// cross-tool DO_NOT_TRACK convention (https://donottrack.sh/).
	// GRAFANA_USAGE_STATS and --usage-stats both win over it, so a machine
	// that sets it globally can still opt a single server back in
	// deliberately.
	DoNotTrackEnvVar = "DO_NOT_TRACK"
	// EndpointEnvVar overrides where reports are sent. It is not an opt-out.
	EndpointEnvVar = "GRAFANA_USAGE_STATS_ENDPOINT"
	// DefaultEndpoint is Grafana's usage-statistics service, the same service
	// that receives usage reports from Grafana, Loki, Mimir and Tempo.
	DefaultEndpoint = "https://stats.grafana.org/mcp-grafana-usage-report"
)

// ResolveMode resolves the reporting mode. Precedence, highest first: the
// --usage-stats flag, GRAFANA_USAGE_STATS, DO_NOT_TRACK, DefaultMode.
//
// DO_NOT_TRACK sits below the two explicit settings on purpose. It is a
// machine-wide preference, so a host that sets it should still be able to opt
// one server back in by naming this server's own setting; it can only ever
// disable, never enable.
//
// Any unrecognised non-empty value resolves to ModeDisabled rather than to the
// default: a typo in an opt-out must not silently turn reporting on.
func ResolveMode(flagValue string, flagSet bool, envValue, doNotTrack string) Mode {
	switch {
	case flagSet:
		return parseMode(flagValue)
	case strings.TrimSpace(envValue) != "":
		return parseMode(envValue)
	case DoNotTrack(doNotTrack):
		return ModeDisabled
	default:
		return DefaultMode
	}
}

// DoNotTrack reports whether a DO_NOT_TRACK value opts out. Only "1" does:
// that is the value the convention defines, and treating any non-empty value
// as an opt-out would make DO_NOT_TRACK=0 disable reporting.
//
// Surrounding whitespace is trimmed but nothing else is accepted. Trimming is
// not a second value: a quoted "1 " in a compose file or a Kubernetes
// manifest is still the value 1, and not recognising it would leave reporting
// on, which is the wrong direction to fail in for an opt-out.
func DoNotTrack(v string) bool {
	return strings.TrimSpace(v) == "1"
}

func parseMode(v string) Mode {
	switch Mode(strings.ToLower(strings.TrimSpace(v))) {
	case ModeEnabled:
		return ModeEnabled
	case ModeLog:
		return ModeLog
	default:
		return ModeDisabled
	}
}

// ResolveEndpoint returns the destination for reports: the
// GRAFANA_USAGE_STATS_ENDPOINT value when set, else DefaultEndpoint.
func ResolveEndpoint(envValue string) string {
	if v := strings.TrimSpace(envValue); v != "" {
		return v
	}
	return DefaultEndpoint
}
