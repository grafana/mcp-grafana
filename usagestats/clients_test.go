//go:build unit

package usagestats

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/grafana/mcp-grafana/observability"
)

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

func TestClientVersion(t *testing.T) {
	assert.Equal(t, "1.2.3", ClientVersion("cursor", "1.2.3"))
	assert.Equal(t, "1.2.3", ClientVersion("cursor", " 1.2.3 "))

	// A version alongside an unrecognised name is the free-text field the
	// client_name clamp exists to prevent.
	assert.Empty(t, ClientVersion(observability.ValueOther, "1.2.3"))
	assert.Empty(t, ClientVersion("", "1.2.3"))
}

// TestClientVersionIsLengthCapped: there is no vocabulary to clamp a version
// against, so the length cap is the only bound on it.
func TestClientVersionIsLengthCapped(t *testing.T) {
	long := strings.Repeat("v", 40*1024)
	got := ClientVersion("cursor", long)
	assert.Len(t, got, maxVersionLen)
	assert.Equal(t, strings.Repeat("v", maxVersionLen), got)

	// Exactly at the cap is untouched.
	atCap := strings.Repeat("v", maxVersionLen)
	assert.Equal(t, atCap, ClientVersion("cursor", atCap))
}

// TestVersionTruncationKeepsValidUTF8: a byte-slice cut can land mid-rune,
// which json.Marshal then rewrites to U+FFFD.
func TestVersionTruncationKeepsValidUTF8(t *testing.T) {
	// Each "é" is two bytes, so a naive cut at 64 bytes splits the 33rd.
	got := ClientVersion("cursor", strings.Repeat("é", 40))
	assert.True(t, utf8.ValidString(got), "truncated version must stay valid UTF-8")
	assert.LessOrEqual(t, len(got), maxVersionLen)
	assert.Equal(t, strings.Repeat("é", 32), got)

	// A cut that happens to land on a boundary keeps the full budget.
	assert.Equal(t, strings.Repeat("a", maxVersionLen), ClientVersion("cursor", strings.Repeat("a", 100)))
}
