package usagestats

import (
	"strings"

	"github.com/grafana/mcp-grafana/observability"
)

// clientNames is the allowlist for clients_seen: the MCP clients with a setup
// page under docs/sources/clients/, plus mcpb for the desktop extension
// bundle. Client info arrives as free text in the client's initialize request,
// so it is untrusted input and cannot be reported verbatim — an unbounded
// client name would let any connecting client write arbitrary strings into the
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
// clientNames, for inclusion in the clients_seen set. A name outside the list
// becomes observability.ValueOther; a client that reported no name at all
// stays empty and is left out of the set entirely, which is a different fact.
//
// Matching is case-insensitive on the trimmed name only. No alias table maps
// a client's display name onto a slug, so a client whose reported name differs
// from its documented slug reports as "other" — the docs page says so.
func ClientName(reported string) string {
	return observability.BoundedValue(strings.ToLower(strings.TrimSpace(reported)), clientNames)
}
