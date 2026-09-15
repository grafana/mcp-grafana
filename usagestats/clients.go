package usagestats

import (
	"strings"

	"github.com/grafana/mcp-grafana/observability"
)

// clientNames is the allowlist for client_name: the MCP clients with a setup
// page under docs/sources/clients/, plus mcpb for the desktop extension
// bundle. Client info arrives as free text in the client's initialize request,
// so it is untrusted input and cannot be a field of its own — an unbounded
// client_name would let any connecting client write arbitrary strings into the
// usage data.
//
// A rising share of observability.ValueOther means extending this list (and
// the docs page's client table with it), never loosening the clamp.
var clientNames = observability.ValueSet(
	"claude-code",
	"claude-desktop",
	"codex",
	"cursor",
	"gemini-cli",
	"mcpb",
	"vscode-copilot",
	"windsurf",
	"zed",
)

// ClientName clamps the name a client reported in its initialize request to
// clientNames. A name outside the list becomes observability.ValueOther; a
// client that reported no name at all stays empty, which is a different fact.
//
// Matching is case-insensitive on the trimmed name only. No alias table maps
// a client's display name onto a slug, so a client whose reported name differs
// from its documented slug reports as "other" — the docs page says so.
func ClientName(reported string) string {
	return observability.BoundedValue(strings.ToLower(strings.TrimSpace(reported)), clientNames)
}

// maxClientVersionLen caps client_version. Unlike client_name there is no
// vocabulary to clamp a version against, so it travels as the client wrote it
// — which makes it untrusted input of unbounded length. The cap is what bounds
// it: without one, a client is free to push arbitrarily large text into a
// typed column and into the stored raw payload.
const maxClientVersionLen = 64

// ClientVersion returns the version to report for a client, truncated to
// maxClientVersionLen bytes.
//
// It is sent only when the client's name is allowlisted. A version string
// alongside an unrecognised name would reintroduce exactly the free-text field
// the client_name clamp exists to prevent, and a version is not interpretable
// without knowing which client it belongs to anyway. The empty string is
// omitted from the event rather than sent, so an unrecognised client carries
// no version field at all.
//
// Nothing beyond the length is validated: a version is whatever the client
// calls itself, and guessing at its shape would drop legitimate values.
func ClientVersion(clampedName, reportedVersion string) string {
	if clampedName == "" || clampedName == observability.ValueOther {
		return ""
	}
	v := strings.TrimSpace(reportedVersion)
	if len(v) > maxClientVersionLen {
		v = v[:maxClientVersionLen]
	}
	return v
}
