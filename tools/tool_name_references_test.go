//go:build unit
// +build unit

package tools

import (
	"fmt"
	"regexp"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// registerAllToolsForDriftTest registers every statically-known tool (write
// tools included) on srv. It mirrors cmd/mcp-grafana's toolEntries(), but
// lives here because that function is in package main and can't be reused
// from a tools package test. If a new domain's AddXTools function is added
// without a line here, this test silently stops covering it — there's no
// automatic way to catch that omission, so add the call when you add the
// domain.
//
// Proxied tools (e.g. Tempo) are discovered dynamically per-session and are
// deliberately excluded: their names aren't known statically, and nothing in
// this package's static strings could reference them safely anyway.
func registerAllToolsForDriftTest(srv *server.MCPServer) {
	AddSearchTools(srv)
	AddDatasourceTools(srv, true)
	AddIncidentTools(srv, true)
	AddPrometheusTools(srv)
	AddLokiTools(srv)
	AddElasticsearchTools(srv)
	AddQuickwitTools(srv)
	AddInfluxDBTools(srv)
	AddAlertingTools(srv, true)
	AddDashboardTools(srv, true)
	AddFolderTools(srv, true)
	AddOnCallTools(srv, true)
	AddAssertsTools(srv)
	AddSiftTools(srv, true)
	AddAdminTools(srv)
	AddPyroscopeTools(srv)
	AddNavigationTools(srv, true)
	AddAnnotationTools(srv, true)
	AddRenderingTools(srv)
	AddSnapshotTools(srv, true)
	AddCloudWatchTools(srv)
	AddExamplesTools(srv)
	AddClickHouseTools(srv)
	AddSnowflakeTools(srv)
	AddRunPanelQueryTools(srv)
	AddGraphiteTools(srv)
	AddAthenaTools(srv)
	AddPluginTools(srv, true)
	AddAPITools(srv, true)
	AddConfigTools(srv)
	AddProvisioningTools(srv)
	AddAgento11yTools(srv, true)
	AddAssistantTools(srv, true)
}

// toolNameShapedTokenRe matches identifiers that could plausibly be a tool
// name reference in prose or hint text:
//
//   - triggerWordRe: a snake_case identifier of 2+ segments immediately
//     following a verb that this codebase consistently uses to point at
//     another tool ("Use X", "use X", "via X", "call X", "run X", "prefer
//     X"/"preferred X"), optionally backtick-quoted. Field/parameter names
//     are also frequently mentioned in prose (e.g. "rule_uid is required"),
//     but they don't follow these verbs in practice — this codebase's
//     convention is to name the *tool*, not the *parameter*, after these
//     words. The one observed exception is a parameter *assignment* like
//     `Use query_type="both"`, which the '=' exclusion below filters out.
//   - backtickRe: a snake_case identifier of 2+ segments that is the entire
//     content of a backtick-quoted span, e.g. "the `query_prometheus` tool".
//     Backtick-quoting a snake_case token is reserved in this codebase for
//     tool/parameter names; if you need a code span for something else,
//     rephrase so it isn't a bare match for this pattern (or extend the
//     allowlist below).
//
// What this deliberately does NOT catch: identifiers that are tool names by
// coincidence but appear without one of these signals (e.g. a parameter
// named identically to a tool, mentioned in plain prose with no verb or
// backticks); multi-word or CamelCase references; and prose that describes
// an operation of a consolidated tool (those are operation values, not tool
// names, and aren't expected to match a registered tool name at all).
var (
	triggerWordRe = regexp.MustCompile(
		`\b(?i:use|via|call|run|invoke|prefer(?:red)?)\s+(?:(?i:using)\s+)?` + "`?" + `([a-z][a-z0-9]*(?:_[a-z0-9]+)+)` + "`?",
	)
	backtickRe = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")
)

// knownNonToolIdentifiers allowlists snake_case identifiers that match the
// patterns above but are not tool names — parameters, query-language
// functions, etc. — discovered while calibrating the matcher against this
// repo. Add to this list (with a short reason) rather than loosening the
// regexes above if a new false positive shows up.
var knownNonToolIdentifiers = map[string]string{
	"query_type": `pyroscope.go: "Use query_type=\"both\"..." refers to a parameter, not a tool`,
}

type toolNameReference struct {
	source string // e.g. "tool description: dashboards_write" or "hint action (prometheus)"
	token  string
}

func findToolNameReferences(source, text string) []toolNameReference {
	var refs []toolNameReference

	for _, m := range triggerWordRe.FindAllStringSubmatchIndex(text, -1) {
		tok := text[m[2]:m[3]]
		if end := m[3]; end < len(text) && text[end] == '=' {
			continue // parameter assignment, e.g. `query_type="both"`, not a tool call
		}
		refs = append(refs, toolNameReference{source: source, token: tok})
	}
	for _, m := range backtickRe.FindAllStringSubmatchIndex(text, -1) {
		refs = append(refs, toolNameReference{source: source, token: text[m[2]:m[3]]})
	}

	return refs
}

// TestToolNameReferencesAreValid guards against the 60+ places across the
// tools package where a tool description or a generated hint string
// mentions another tool by name in prose (e.g. "Use list_prometheus_metric_names
// to verify the metric exists"). Renaming a tool doesn't touch these free-text
// mentions, so nothing else catches them going stale. This test registers
// the full (write-enabled) tool set, generates every hint variant, and
// asserts every string that looks like a tool-name reference names a tool
// that's actually registered.
func TestToolNameReferencesAreValid(t *testing.T) {
	srv := server.NewMCPServer("drift-test", "0.0.0")
	registerAllToolsForDriftTest(srv)

	registered := srv.ListTools()
	knownToolNames := make(map[string]bool, len(registered))
	for name := range registered {
		knownToolNames[name] = true
	}

	var refs []toolNameReference
	for name, st := range registered {
		refs = append(refs, findToolNameReferences(fmt.Sprintf("tool description: %s", name), st.Tool.Description)...)
	}

	// Cover every branch of GenerateEmptyResultHints, since its returned
	// PossibleCauses/SuggestedActions strings are where most of the
	// remaining references live (see tools/hints.go).
	for _, dsType := range []string{
		"prometheus", "loki", "clickhouse", "cloudwatch", "athena",
		"influxdb", "graphite", "snowflake", "unknown-datasource-type",
	} {
		hints := GenerateEmptyResultHints(HintContext{
			DatasourceType: dsType,
			Query:          `rate(foo[5m]) + histogram_quantile(0.9, bar) {app="x"} |= "err" |~ "e" seriesByTag() sumSeries() aggregateWindow group by time`,
		})
		for _, c := range hints.PossibleCauses {
			refs = append(refs, findToolNameReferences(fmt.Sprintf("hint cause (%s)", dsType), c)...)
		}
		for _, a := range hints.SuggestedActions {
			refs = append(refs, findToolNameReferences(fmt.Sprintf("hint action (%s)", dsType), a)...)
		}
	}

	var violations []string
	seen := make(map[string]bool)
	for _, ref := range refs {
		if knownToolNames[ref.token] {
			continue
		}
		if _, ok := knownNonToolIdentifiers[ref.token]; ok {
			continue
		}
		key := ref.source + "\x00" + ref.token
		if seen[key] {
			continue
		}
		seen[key] = true
		violations = append(violations, fmt.Sprintf("%s: %q looks like a tool-name reference but no tool is registered with that name", ref.source, ref.token))
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("found %d stale tool-name reference(s):\n%s", len(violations), joinLines(violations))
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += "  - " + l + "\n"
	}
	return out
}
