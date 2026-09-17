//go:build unit

package usagestats

import (
	"context"
	"testing"

	"github.com/grafana/mcp-grafana/observability"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportSeqNumbersReportsFromOne: a gap in report_seq for a process_id is
// how a lost report is detected. Counters reset on send rather than on
// acknowledgement, so a lost report takes its delta with it; without the
// sequence the loss is invisible and totals look like counts.
func TestReportSeqNumbersReportsFromOne(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})

	for range 3 {
		r.flush(context.Background(), ReasonInterval)
	}

	require.Len(t, c.received(), 3)
	for i, e := range c.received() {
		assert.Equal(t, int64(i+1), e.ReportSeq)
	}
}

// TestTransportIsClampedOnTheWire: --transport is validated only after the
// reporter is built and the shutdown flush deferred, so an invalid value
// reaches the reporter and the error path still flushes. An operator who
// mistypes it can supply anything, including a hostname, so the wire value is
// clamped rather than relying on that ordering.
func TestTransportIsClampedOnTheWire(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"stdio", "stdio"},
		{"sse", "sse"},
		{"streamable-http", "streamable-http"},
		{"https://private-tenant.example", observability.ValueOther},
		{"Stdio", observability.ValueOther},
		{"", ""},
	} {
		c := newCollector(t)
		r := New(Config{Mode: ModeEnabled, Endpoint: c.URL, Transport: tc.in})
		r.flush(context.Background(), ReasonInterval)
		require.Len(t, c.received(), 1)
		assert.Equal(t, tc.want, c.received()[0].Transport, "transport %q", tc.in)
		assert.NotContains(t, c.rawBodies()[0], "private-tenant")
	}
}

// TestEnvSetReportsNamesNotValues: env_set is the other half of flags, and the
// inventory is fixed in the binary rather than a scan of the environment, so
// an unrelated variable cannot be reported even by accident.
func TestEnvSetReportsNamesNotValues(t *testing.T) {
	env := map[string]string{
		"GRAFANA_URL":                   "https://secret-stack.grafana.net",
		"GRAFANA_SERVICE_ACCOUNT_TOKEN": "glsa_supersecret",
		"GRAFANA_ORG_ID":                "  ",
		"AWS_SECRET_ACCESS_KEY":         "not-ours-and-not-reported",
	}
	got := EnvSet(func(k string) string { return env[k] })

	assert.Equal(t, "GRAFANA_SERVICE_ACCOUNT_TOKEN,GRAFANA_URL", got)
	assert.NotContains(t, got, "secret-stack")
	assert.NotContains(t, got, "glsa_")
	assert.NotContains(t, got, "AWS_SECRET_ACCESS_KEY")
}
