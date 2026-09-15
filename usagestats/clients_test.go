//go:build unit

package usagestats

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/grafana/mcp-grafana/observability"
)

// TestTruncateRunesKeepsValidUTF8: a byte-slice cut can land mid-rune, which
// json.Marshal then rewrites to U+FFFD, corrupting the tail of the value.
func TestTruncateRunesKeepsValidUTF8(t *testing.T) {
	// Each "é" is two bytes, so a naive cut at 64 bytes splits the 33rd.
	got := truncateRunes(strings.Repeat("é", 40), maxVersionLen)
	assert.True(t, utf8.ValidString(got), "truncated value must stay valid UTF-8")
	assert.LessOrEqual(t, len(got), maxVersionLen)
	assert.Equal(t, strings.Repeat("é", 32), got)

	// A cut that lands on a boundary keeps the full budget, and a short value
	// is untouched.
	assert.Equal(t, strings.Repeat("a", maxVersionLen), truncateRunes(strings.Repeat("a", 100), maxVersionLen))
	assert.Equal(t, "12.1.0", truncateRunes("12.1.0", maxVersionLen))
}

func TestClientName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reported string
		want     string
	}{
		{name: "documented client", reported: "claude-code", want: "claude-code"},
		{name: "case and whitespace normalised", reported: " Cursor ", want: "cursor"},
		{name: "desktop extension bundle", reported: "mcpb", want: "mcpb"},

		// Client info is free text from the initialize request, so anything
		// off the list must collapse rather than travel verbatim.
		{name: "unknown client", reported: "my-internal-agent", want: observability.ValueOther},
		{name: "display name that is not the slug", reported: "Visual Studio Code", want: observability.ValueOther},
		{name: "injection attempt", reported: "acme-corp internal build for customer 42", want: observability.ValueOther},
		{name: "no name reported stays empty", reported: "", want: ""},
		{name: "whitespace-only name stays empty", reported: "   ", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClientName(tc.reported))
		})
	}
}

// TestClientNameAllowlistMatchesDocumentedClients keeps the allowlist tied to
// the client setup pages: a client documented under docs/sources/clients/ but
// missing here would silently report as "other".
func TestClientNameAllowlistMatchesDocumentedClients(t *testing.T) {
	documented := []string{
		"claude-code", "claude-desktop", "codex", "cursor",
		"gemini-cli", "vscode-copilot", "windsurf", "zed",
	}
	for _, slug := range documented {
		assert.Equal(t, slug, ClientName(slug), "client documented under docs/sources/clients/%s.md is not allowlisted", slug)
	}
	assert.Len(t, clientNames, len(documented)+1, "allowlist should be the documented clients plus mcpb")
}
