package usagestats

import "fmt"

// Transport names, as passed to SessionCoverageWarnings and reported in the
// transport field.
const (
	TransportStdio          = "stdio"
	TransportSSE            = "sse"
	TransportStreamableHTTP = "streamable-http"
)

// SessionCoverageWarnings returns the reasons the given configuration cannot
// report every MCP session — or any at all — so startup can say so instead of
// leaving an operator to conclude from an empty dataset that nobody is using
// the server.
//
// The event unit is the MCP session, and a session is only ever seen through
// mcp-go's OnRegisterSession hook. Under the streamable-http transport there
// are configurations in which that hook never fires, and mcp-grafana cannot
// fix them from the outside: minting a process-level pseudo-session instead
// would silently change the event unit for a whole class of deployment, which
// is a decision to take deliberately, not a gap to paper over.
//
// Verified against mcp-go v1.0.0:
//
//   - server/streamable_http.go:998 gates POST session registration on
//     `isInitializeRequest && sessionID != ""`.
//   - :2043 StatelessSessionIdManager.Generate returns "", and
//     WithStateLess(true) (which mcp-grafana sets when proxied tools are off)
//     installs it — so no POST ever registers a session.
//   - :647 sets isInitializeRequest = false for protocol version 2026-07-28,
//     which removed protocol-level sessions, and mints no session ID even in
//     stateful mode — so a client on that version never registers either.
func SessionCoverageWarnings(transport string, stateless bool) []string {
	if transport != TransportStreamableHTTP {
		return nil
	}
	var warnings []string
	if stateless {
		warnings = append(warnings, fmt.Sprintf(
			"no usage statistics will be collected at all: the %s transport is running statelessly (proxied tools are disabled), so the MCP server never registers a session and there is nothing to report per session. Enable proxied tools, or use another transport, if you want these reports",
			TransportStreamableHTTP))
	}
	warnings = append(warnings, fmt.Sprintf(
		"usage statistics under-report on the %s transport: protocol version %s removed protocol-level sessions, so clients using it are never counted. Reported session counts are a lower bound on real usage, and shift as clients upgrade",
		TransportStreamableHTTP, modernProtocolVersion))
	return warnings
}

// modernProtocolVersion is the MCP protocol version that removed
// protocol-level sessions (SEP-2567). Named here for the warning text only;
// mcp-go decides which requests belong to it.
const modernProtocolVersion = "2026-07-28"

// WarnSessionCoverage logs, once at startup, every reason this configuration
// cannot report all of its sessions. It is a no-op when reporting is off:
// there is nothing to caveat if nothing is collected by choice.
func (r *Reporter) WarnSessionCoverage(transport string, stateless bool) {
	if !r.Enabled() {
		return
	}
	for _, w := range SessionCoverageWarnings(transport, stateless) {
		r.cfg.Logger.Warn(w)
	}
}
