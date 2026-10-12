//go:build unit

package usagestats

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiscloseClaimsNothingAboutClients pins the startup notice against the
// wire format it summarises.
//
// The notice is the only disclosure an operator sees at runtime, and under
// stdio it is the only one anyone sees at all, so a claim it makes that the
// event does not match is worse than a missing field. It claimed the server
// reported "which kinds of MCP client connected" for two commits after
// clients_seen was removed; the comment in wire_fields_test.go asking for the
// notice to be updated did not stop that, so this asserts it.
//
// Nothing about a connecting client is collected, so the notice must not
// mention clients as something reported. If a client property is ever
// collected again, this test fails and the notice has to say so.
func TestDiscloseClaimsNothingAboutClients(t *testing.T) {
	var buf bytes.Buffer
	r := New(Config{
		Mode:   ModeEnabled,
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	r.Disclose()

	notice := buf.String()
	require.NotEmpty(t, notice, "an enabled reporter must disclose at startup")
	assert.NotContains(t, strings.ToLower(notice), "client connected")
	assert.NotContains(t, strings.ToLower(notice), "clients_seen")
	assert.Contains(t, notice, "Nothing is collected about the MCP clients that connect")
}
