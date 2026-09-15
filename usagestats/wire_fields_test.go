//go:build unit

package usagestats

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docsPage is the user-facing contract for the wire format.
const docsPage = "../docs/sources/anonymous-usage-statistics.md"

// wireFields is the complete inventory of field names that may leave this
// process, Event's own plus the nested ToolCount's.
//
// THIS LIST IS THE CONTRACT, NOT A CONVENIENCE. Changing a field means
// changing, in the same pull request:
//
//  1. the field tables in docs/sources/anonymous-usage-statistics.md, which
//     this test checks mechanically, and its "how to read these fields"
//     section, which it cannot;
//  2. the startup notice in Reporter.Disclose, if the change alters what the
//     notice summarises as collected — the notice is the only disclosure an
//     operator sees at runtime; and
//  3. the receiver's BigQuery struct in grafana/usage-stats.
//
// Adding a field here to make the test pass, without those, ships a field
// nobody was told about.
var wireFields = []string{
	"arch",
	"auth_method",
	"calls",
	"clients_seen",
	"disabled_tools",
	"dynamic_multi_org",
	"enabled_tools",
	"errors",
	"flags",
	"grafana_version",
	"loki_guardrail_mode",
	"metrics_enabled",
	"org_id_set",
	"os",
	"process_id",
	"process_uptime_ms",
	"proxied_enabled",
	"report_reason",
	"service",
	"target_kind",
	"tls_enabled",
	"tool_calls",
	"tools_called",
	"transport",
	"version",
}

func TestWireFields(t *testing.T) {
	got := jsonFieldNames(reflect.TypeOf(Event{}))
	got = append(got, jsonFieldNames(reflect.TypeOf(ToolCount{}))...)
	sort.Strings(got)

	want := append([]string(nil), wireFields...)
	sort.Strings(want)

	assert.Equal(t, want, got, "the set of fields on the wire changed: see this test's comment for what else must change with it")
}

// TestRemovedFieldsStayRemoved pins the fields the per-process regrouping took
// off the wire, so reintroducing one is a deliberate act rather than a
// copy-paste. The session ones cannot come back at all: protocol version
// 2026-07-28 removed protocol sessions, which is why the unit is the process.
func TestRemovedFieldsStayRemoved(t *testing.T) {
	for _, gone := range []string{"session_id", "session_duration_ms", "client_name", "client_version"} {
		assert.NotContains(t, wireFields, gone)
	}
}

// TestEveryWireFieldIsDocumented stops a field shipping undocumented. The docs
// page is what an operator reads to decide whether to opt out, so a field that
// is not on it is a field collected without disclosure.
func TestEveryWireFieldIsDocumented(t *testing.T) {
	page, err := os.ReadFile(docsPage)
	require.NoError(t, err, "the usage statistics docs page must exist")
	doc := string(page)

	for _, field := range wireFields {
		assert.Contains(t, doc, "`"+field+"`", "wire field %q is not documented in %s", field, docsPage)
	}
}

// TestDocsPageDocumentsTheControls keeps the opt-out route, the destination
// and the inspection mode on the page, since they are what the startup notice
// points readers at.
func TestDocsPageDocumentsTheControls(t *testing.T) {
	page, err := os.ReadFile(docsPage)
	require.NoError(t, err)
	doc := string(page)

	for _, needed := range []string{
		ModeEnvVar,
		EndpointEnvVar,
		DefaultEndpoint,
		"--usage-stats",
		string(ModeEnabled),
		string(ModeDisabled),
		string(ModeLog),
		ProxiedToolName,
		ReasonInterval,
		ReasonShutdown,
	} {
		assert.Contains(t, doc, needed, "%s must be documented in %s", needed, docsPage)
	}
}

func jsonFieldNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}
