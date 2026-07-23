// The insight-cell tool renders any Grafana result the agent has gathered as an
// interactive "insight cell" in an MCP host — a core panel (timeseries/stat/bar/
// table), a logs/trace view, or a synthesis view (worklist/rca/rulediff/timeline/
// cost) — carrying a verdict, attestation, provenance and drill actions.
//
// This is the Go home of the render surface prototyped in contrib/insight-cell.
// It is a *render substrate*: the agent does the analysis with the existing query
// tools (query_prometheus, query_loki_logs, list_alert_rules, get_annotations, …)
// and hands the assembled data here; the tool packages it into the render contract
// and the trust metadata. It does NOT query datasources or fabricate data itself.
//
// The result rides three channels so it degrades across hosts:
//   - content[0]  a text verdict (fallback for hosts without MCP Apps)
//   - content[1]  an embedded application/json resource block (kept by hosts that
//     drop structuredContent, e.g. Claude Desktop, which converts it
//     to a text block the app scans)
//   - structuredContent  the InsightCell (the spec channel)
//
// plus _meta = { ui.resourceUri, "grafana.insightCell/v0": <trust profile> }.
//
// The UI that renders these lives in ui/insight-cell/ and is embedded via
// mcpgrafana.InsightCellResourceURI (see ui_apps.go / ui_embed.go).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// insightCellMetaKey is the _meta namespace for the insight-cell trust profile.
const insightCellMetaKey = "grafana.insightCell/v0"

// --- Render contract (mirrors contrib/insight-cell/src/schema.ts) ------------
// JSON field names must match schema.ts exactly — the embedded UI reads them.

type icField struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // time | number | string
	Values []any  `json:"values"`
	Unit   string `json:"unit,omitempty"`
	Color  string `json:"color,omitempty"`
}

type icDataFrame struct {
	Name   string    `json:"name,omitempty"`
	RefID  string    `json:"refId,omitempty"`
	Fields []icField `json:"fields"`
}

type icThreshold struct {
	Value float64 `json:"value"`
	Color string  `json:"color"`
	Label string  `json:"label,omitempty"`
}

type icValueMapping struct {
	Type  string   `json:"type"` // value | range
	Value any      `json:"value,omitempty"`
	From  *float64 `json:"from,omitempty"` // pointer so a legitimate 0 bound isn't dropped
	To    *float64 `json:"to,omitempty"`   // pointer so a legitimate 0 bound isn't dropped
	Text  string   `json:"text,omitempty"`
	Color string   `json:"color,omitempty"`
}

type icRenderHint struct {
	Type        string           `json:"type"`
	Title       string           `json:"title"`
	Unit        string           `json:"unit,omitempty"`
	Decimals    *int             `json:"decimals,omitempty"`
	Thresholds  []icThreshold    `json:"thresholds,omitempty"`
	Mappings    []icValueMapping `json:"mappings,omitempty"`
	Description string           `json:"description,omitempty"`
	ValueField  string           `json:"valueField,omitempty"`
	Sort        string           `json:"sort,omitempty"`
	Target      *float64         `json:"target,omitempty"` // bullet: target/SLO marker
	Max         *float64         `json:"max,omitempty"`    // bullet: axis max
}

type icLogLine struct {
	Time   string            `json:"time"`
	Level  string            `json:"level"`
	Line   string            `json:"line"`
	Labels map[string]string `json:"labels,omitempty"`
}

type icTraceSpan struct {
	ID         string         `json:"id"`
	ParentID   string         `json:"parentId,omitempty"`
	Name       string         `json:"name"`
	Service    string         `json:"service"`
	StartMs    float64        `json:"startMs"`
	DurationMs float64        `json:"durationMs"`
	Status     string         `json:"status,omitempty"`
	Tags       map[string]any `json:"tags,omitempty"`
	RootCause  bool           `json:"rootCause,omitempty"`
}

type icTracePayload struct {
	TraceID    string        `json:"traceId"`
	DurationMs float64       `json:"durationMs"`
	Spans      []icTraceSpan `json:"spans"`
}

type icAction struct {
	Label   string         `json:"label"`
	Kind    string         `json:"kind"` // link | tool | refresh | ask
	URL     string         `json:"url,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Text    string         `json:"text,omitempty"`
	Primary bool           `json:"primary,omitempty"`
	Icon    string         `json:"icon,omitempty"`
}

type icWorklistItem struct {
	Title      string     `json:"title"`
	Priority   string     `json:"priority,omitempty"`
	Status     string     `json:"status,omitempty"`
	StatusTone string     `json:"statusTone,omitempty"`
	Why        string     `json:"why,omitempty"`
	Actions    []icAction `json:"actions,omitempty"`
}

type icRcaRoot struct {
	Title      string `json:"title"`
	Confidence string `json:"confidence"`
	Detail     string `json:"detail,omitempty"`
}

type icRcaFinding struct {
	Title    string `json:"title"`
	Kind     string `json:"kind,omitempty"`
	Severity string `json:"severity,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type icRcaPayload struct {
	RootCause *icRcaRoot     `json:"rootCause,omitempty"`
	Checks    []string       `json:"checks,omitempty"`
	Findings  []icRcaFinding `json:"findings"`
}

type icRuleDiffChange struct {
	Field     string `json:"field"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Rationale string `json:"rationale,omitempty"`
}

type icRuleDiffPayload struct {
	RuleTitle string             `json:"ruleTitle"`
	RuleUID   string             `json:"ruleUid,omitempty"`
	Summary   string             `json:"summary,omitempty"`
	Changes   []icRuleDiffChange `json:"changes"`
	Proposed  map[string]any     `json:"proposed,omitempty"`
	Applied   bool               `json:"applied,omitempty"`
}

type icChangeEvent struct {
	Time       string   `json:"time"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Correlated bool     `json:"correlated,omitempty"`
}

type icTimelinePayload struct {
	From   string          `json:"from"`
	To     string          `json:"to"`
	Events []icChangeEvent `json:"events"`
}

type icCostDriver struct {
	Name   string   `json:"name"`
	Series *float64 `json:"series,omitempty"` // pointer so a legitimate 0 isn't dropped
	Value  *float64 `json:"value,omitempty"`  // pointer so a legitimate 0 isn't dropped
	Unit   string   `json:"unit,omitempty"`
	Pct    *float64 `json:"pct,omitempty"` // pointer so a legitimate 0% isn't dropped
	Note   string   `json:"note,omitempty"`
}

type icCostTotal struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

type icCostHeadroom struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

type icCostPayload struct {
	Total    *icCostTotal    `json:"total,omitempty"`
	Drivers  []icCostDriver  `json:"drivers"`
	Headroom *icCostHeadroom `json:"headroom,omitempty"`
}

type icCallout struct {
	Tone  string `json:"tone"` // warn | crit | info
	Title string `json:"title"`
	Body  string `json:"body"`
}

type icQueryRef struct {
	Ref           string `json:"ref"`
	Expr          string `json:"expr"`
	DatasourceUID string `json:"datasourceUid"`
}

type icAttestation struct {
	AsOf string `json:"asOf"`
	Live bool   `json:"live"`
}

type icProvenance struct {
	Author     string `json:"author"`
	Datasource string `json:"datasource"`
	OrgID      int    `json:"orgId,omitempty"`
	RBACScope  string `json:"rbacScope,omitempty"`
}

type icMeta struct {
	Question    string        `json:"question"`
	Verdict     string        `json:"verdict"`
	Confidence  string        `json:"confidence"`
	TimeRange   icTimeRange   `json:"timeRange"`
	Attestation icAttestation `json:"attestation"`
	Provenance  icProvenance  `json:"provenance"`
	Query       []icQueryRef  `json:"query"`
	DataMode    string        `json:"dataMode"` // mock | live
	ResultInfo  string        `json:"resultInfo,omitempty"`
}

type icTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// InsightCell is the full render contract emitted as structuredContent.
type insightCell struct {
	RenderHint icRenderHint       `json:"renderHint"`
	Frames     []icDataFrame      `json:"frames,omitempty"`
	Logs       []icLogLine        `json:"logs,omitempty"`
	Trace      *icTracePayload    `json:"trace,omitempty"`
	Worklist   []icWorklistItem   `json:"worklist,omitempty"`
	RCA        *icRcaPayload      `json:"rca,omitempty"`
	RuleDiff   *icRuleDiffPayload `json:"rulediff,omitempty"`
	Timeline   *icTimelinePayload `json:"timeline,omitempty"`
	Cost       *icCostPayload     `json:"cost,omitempty"`
	Callout    *icCallout         `json:"callout,omitempty"`
	Actions    []icAction         `json:"actions"`
	Meta       icMeta             `json:"meta"`
}

// --- Tool input --------------------------------------------------------------

// RenderInsightCellParams is what the agent supplies after it has gathered the
// data. Populate only the fields relevant to `panel`; everything else is optional.
type RenderInsightCellParams struct {
	Panel string `json:"panel" jsonschema:"required,enum=timeseries,enum=stat,enum=bullet,enum=bar,enum=table,enum=logs,enum=trace,enum=worklist,enum=rca,enum=rulediff,enum=timeline,enum=cost,description=Which panel type to render. Core panels (timeseries/stat/bar/table) read 'frames'. 'bullet' = a single value vs a target/SLO with qualitative threshold bands (reads 'frames' + target/max; more compact than a gauge). 'logs'/'trace' read their own fields. Synthesis views: 'worklist' = ranked actionable findings (alert triage/deprecations); 'rca' = root-cause investigation (findings->root cause->evidence); 'rulediff' = a proposed alert-rule fix as a before/after diff; 'timeline' = change-correlation (deploys/config/alerts on a time axis); 'cost' = cost/cardinality drivers."`

	Title      string `json:"title,omitempty" jsonschema:"description=Panel title / the question being answered."`
	Verdict    string `json:"verdict,omitempty" jsonschema:"description=One-line answer shown as the insight title (your conclusion about the data)."`
	Insight    string `json:"insight,omitempty" jsonschema:"description=2-4 sentence explanation shown under the title: what the data shows and why it matters."`
	Confidence string `json:"confidence,omitempty" jsonschema:"enum=low,enum=medium,enum=high,description=Your confidence in the verdict. Defaults to medium."`

	Query         string `json:"query,omitempty" jsonschema:"description=The PromQL/LogQL/TraceQL expression the data came from (recorded in provenance; not executed here)."`
	DatasourceUID string `json:"datasourceUid,omitempty" jsonschema:"description=UID of the datasource the data came from (recorded in provenance)."`
	RangeHours    *int   `json:"rangeHours,omitempty" jsonschema:"description=Look-back window in hours for the recorded time range (default 1)."`

	// renderHint field config (chart types)
	Unit       string           `json:"unit,omitempty" jsonschema:"description=Grafana unit id so values format like Grafana: bytes\\, s\\, ms\\, percent\\, percentunit\\, reqps\\, short\\, Bps\\, ..."`
	Decimals   *int             `json:"decimals,omitempty" jsonschema:"description=Fixed decimal places; omit for automatic precision."`
	Thresholds []icThreshold    `json:"thresholds,omitempty" jsonschema:"description=Threshold steps: a value at/above a step takes its color (colors the stat value and draws a line on timeseries)."`
	Mappings   []icValueMapping `json:"mappings,omitempty" jsonschema:"description=Value mappings: map a specific value or numeric range to display text/color."`
	ValueField string           `json:"valueField,omitempty" jsonschema:"description=stat: which field to read as the value (default: last numeric field)."`
	Sort       string           `json:"sort,omitempty" jsonschema:"enum=desc,enum=asc,enum=none,description=bar: sort direction for ranked bars."`
	Target     *float64         `json:"target,omitempty" jsonschema:"description=bullet: the target/SLO marker drawn as a tick."`
	Max        *float64         `json:"max,omitempty" jsonschema:"description=bullet: axis max; omit to derive from value/target/thresholds."`

	// Data channels (populate the one matching `panel`)
	Frames []icDataFrame    `json:"frames,omitempty" jsonschema:"description=For timeseries/stat/bar/table: the columnar data (Grafana-style frames) you got from a query tool. A frame has fields[] each with name\\, type (time|number|string) and values[]."`
	Logs   []icLogLine      `json:"logs,omitempty" jsonschema:"description=For panel='logs': the log lines (time ISO\\, level\\, line\\, optional labels)."`
	Trace  *icTracePayload  `json:"trace,omitempty" jsonschema:"description=For panel='trace': the trace waterfall (traceId\\, durationMs\\, spans[])."`
	Items  []icWorklistItem `json:"items,omitempty" jsonschema:"description=For panel='worklist': the ranked findings you synthesized. Rank by priority and put your correlation in each item's 'why' — that reasoning is the value."`

	// rca
	RootCause *icRcaRoot     `json:"rootCause,omitempty" jsonschema:"description=For panel='rca': your root-cause hypothesis (title\\, confidence\\, detail)."`
	Checks    []string       `json:"checks,omitempty" jsonschema:"description=For panel='rca': what you examined (error logs\\, slow requests\\, recent deploys\\, ...)."`
	Findings  []icRcaFinding `json:"findings,omitempty" jsonschema:"description=For panel='rca': ranked findings with evidence\\, ideally from Sift / find_slow_requests / find_error_pattern_logs / get_annotations."`

	// rulediff — the Apply write routes to the existing write-gated update_alert_rule tool.
	RuleTitle    string             `json:"ruleTitle,omitempty" jsonschema:"description=For panel='rulediff': the alert rule's name."`
	RuleUID      string             `json:"ruleUid,omitempty" jsonschema:"description=For panel='rulediff': the alert rule UID (from list_alert_rules). Needed to apply the change via update_alert_rule."`
	RuleSummary  string             `json:"ruleSummary,omitempty" jsonschema:"description=For panel='rulediff': one line — what the fix does and why."`
	Changes      []icRuleDiffChange `json:"changes,omitempty" jsonschema:"description=For panel='rulediff': the before/after changes you're proposing."`
	ProposedRule map[string]any     `json:"proposedRule,omitempty" jsonschema:"description=For panel='rulediff': the full updated alert-rule JSON to pass to update_alert_rule when applied."`

	// timeline
	Events []icChangeEvent `json:"events,omitempty" jsonschema:"description=For panel='timeline': change events (deploys/config/alerts) to correlate against an incident. Mark the correlated one."`
	From   string          `json:"from,omitempty" jsonschema:"description=For panel='timeline': ISO window start."`
	To     string          `json:"to,omitempty" jsonschema:"description=For panel='timeline': ISO window end."`

	// cost
	Drivers   []icCostDriver  `json:"drivers,omitempty" jsonschema:"description=For panel='cost': ranked cost/cardinality drivers."`
	CostTotal *icCostTotal    `json:"costTotal,omitempty" jsonschema:"description=For panel='cost': the headline total\\, e.g. {label:'1.2M active series'\\, value:1200000}."`
	Headroom  *icCostHeadroom `json:"headroom,omitempty" jsonschema:"description=For panel='cost': the headroom trade-off making 'adding headroom costs money' explicit."`

	// chrome
	Callout *icCallout `json:"callout,omitempty" jsonschema:"description=Optional callout banner shown above the panel (tone: warn|crit|info)."`
	Actions []icAction `json:"actions,omitempty" jsonschema:"description=Drill/link/refresh/ask actions shown under the panel. kind 'tool' calls another MCP tool (e.g. update_alert_rule to apply a rulediff); 'link' opens a URL; 'refresh' re-runs render_insight_cell; 'ask' sends text back to the agent."`
}

// --- Handler -----------------------------------------------------------------

func renderInsightCell(ctx context.Context, args RenderInsightCellParams) (*mcp.CallToolResult, error) {
	cell, err := buildInsightCell(ctx, args)
	if err != nil {
		return nil, err
	}
	return insightCellResult(cell)
}

func buildInsightCell(ctx context.Context, args RenderInsightCellParams) (*insightCell, error) {
	if args.Panel == "" {
		return nil, fmt.Errorf("panel is required")
	}

	confidence := args.Confidence
	if confidence == "" {
		confidence = "medium"
	}

	title := args.Title
	if title == "" {
		title = args.Verdict
	}
	if title == "" {
		title = defaultTitleForPanel(args.Panel)
	}

	now := time.Now().UTC()
	rangeHours := 1
	if args.RangeHours != nil && *args.RangeHours > 0 {
		rangeHours = *args.RangeHours
	}
	fromISO := now.Add(-time.Duration(rangeHours) * time.Hour).Format(time.RFC3339)
	toISO := now.Format(time.RFC3339)

	// Provenance / attestation. "live" means the agent recorded a query + datasource
	// it read from a real datasource; otherwise this is representative/sample content.
	live := args.Query != "" && args.DatasourceUID != ""
	dataMode := "mock"
	if live {
		dataMode = "live"
	}
	datasource := "sample (no live datasource)"
	if cfg := mcpgrafana.GrafanaConfigFromContext(ctx); cfg.URL != "" && live {
		datasource = fmt.Sprintf("%s (%s)", cfg.URL, args.DatasourceUID)
	} else if args.DatasourceUID != "" {
		datasource = args.DatasourceUID
	}

	var queries []icQueryRef
	if args.Query != "" {
		queries = []icQueryRef{{Ref: "A", Expr: args.Query, DatasourceUID: args.DatasourceUID}}
	}

	hint := icRenderHint{
		Type:        args.Panel,
		Title:       title,
		Unit:        args.Unit,
		Decimals:    args.Decimals,
		Thresholds:  args.Thresholds,
		Mappings:    args.Mappings,
		Description: args.Insight,
		ValueField:  args.ValueField,
		Sort:        args.Sort,
		Target:      args.Target,
		Max:         args.Max,
	}

	cell := &insightCell{
		RenderHint: hint,
		Frames:     args.Frames,
		Logs:       args.Logs,
		Trace:      args.Trace,
		Worklist:   args.Items,
		Callout:    args.Callout,
		Actions:    args.Actions,
		Meta: icMeta{
			Question:    title,
			Verdict:     verdictOrDefault(args.Verdict, args.Panel),
			Confidence:  confidence,
			TimeRange:   icTimeRange{From: fromISO, To: toISO},
			Attestation: icAttestation{AsOf: toISO, Live: live},
			Provenance:  icProvenance{Author: "Grafana MCP", Datasource: datasource},
			Query:       queries,
			DataMode:    dataMode,
		},
	}
	if cell.Actions == nil {
		cell.Actions = []icAction{}
	}

	// Synthesis-view payloads. The list fields (findings/changes/drivers) are
	// marshalled without omitempty and the UI iterates them, so default nil to an
	// empty slice — a JSON `null` would make the renderer throw and blank the cell.
	if args.RootCause != nil || len(args.Findings) > 0 || len(args.Checks) > 0 {
		findings := args.Findings
		if findings == nil {
			findings = []icRcaFinding{}
		}
		cell.RCA = &icRcaPayload{RootCause: args.RootCause, Checks: args.Checks, Findings: findings}
	}
	if args.RuleTitle != "" || len(args.Changes) > 0 {
		changes := args.Changes
		if changes == nil {
			changes = []icRuleDiffChange{}
		}
		cell.RuleDiff = &icRuleDiffPayload{
			RuleTitle: args.RuleTitle,
			RuleUID:   args.RuleUID,
			Summary:   args.RuleSummary,
			Changes:   changes,
			Proposed:  args.ProposedRule,
		}
	}
	if len(args.Events) > 0 {
		// Default the axis bounds to the computed time range so the UI never builds
		// the timeline from empty (Invalid Date -> NaN pin positions).
		from, to := args.From, args.To
		if from == "" {
			from = fromISO
		}
		if to == "" {
			to = toISO
		}
		cell.Timeline = &icTimelinePayload{From: from, To: to, Events: args.Events}
	}
	if len(args.Drivers) > 0 || args.CostTotal != nil {
		drivers := args.Drivers
		if drivers == nil {
			drivers = []icCostDriver{}
		}
		cell.Cost = &icCostPayload{Total: args.CostTotal, Drivers: drivers, Headroom: args.Headroom}
	}

	return cell, nil
}

func defaultTitleForPanel(panel string) string {
	switch panel {
	case "worklist":
		return "Worklist"
	case "rca":
		return "Root cause"
	case "rulediff":
		return "Proposed rule change"
	case "timeline":
		return "Change timeline"
	case "cost":
		return "Cost & cardinality"
	case "bullet":
		return "Value vs target"
	default:
		return strings.Title(panel) //nolint:staticcheck // simple ASCII panel names
	}
}

func verdictOrDefault(verdict, panel string) string {
	if verdict != "" {
		return verdict
	}
	return defaultTitleForPanel(panel)
}

// insightCellResult packages a cell into the three-channel tool result plus the
// _meta trust profile. Mirrors contrib/insight-cell/server.ts:cellResult.
func insightCellResult(cell *insightCell) (*mcp.CallToolResult, error) {
	payload, err := json.Marshal(cell)
	if err != nil {
		return nil, fmt.Errorf("marshal insight cell: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s, %s data]\n", cell.RenderHint.Title, cell.RenderHint.Type, cell.Meta.DataMode)
	fmt.Fprintf(&b, "Verdict: %s\n", cell.Meta.Verdict)
	// The agent's 2–4 sentence explanation rides on renderHint.description; include
	// it so hosts without MCP Apps still surface the analysis, not just the verdict.
	if cell.RenderHint.Description != "" {
		fmt.Fprintf(&b, "%s\n", cell.RenderHint.Description)
	}
	if cell.Callout != nil {
		fmt.Fprintf(&b, "%s: %s\n", cell.Callout.Title, cell.Callout.Body)
	}
	live := "sample"
	if cell.Meta.Attestation.Live {
		live = "live"
	}
	fmt.Fprintf(&b, "As of %s · %s.", cell.Meta.Attestation.AsOf, live)

	// The insight-cell trust profile (Layer A) carried in _meta.
	trust := map[string]any{
		"query":       cell.Meta.Query,
		"attestation": cell.Meta.Attestation,
		"provenance":  cell.Meta.Provenance,
		"renderHint":  cell.RenderHint,
		"reasoning": map[string]any{
			"question":   cell.Meta.Question,
			"verdict":    cell.Meta.Verdict,
			"confidence": cell.Meta.Confidence,
			"source":     "agent",
		},
	}

	return &mcp.CallToolResult{
		Result: mcp.Result{
			Meta: &mcp.Meta{
				AdditionalFields: map[string]any{
					"ui":               map[string]any{"resourceUri": mcpgrafana.InsightCellResourceURI},
					insightCellMetaKey: trust,
				},
			},
		},
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: b.String()},
			mcp.EmbeddedResource{
				Type: "resource",
				Resource: mcp.TextResourceContents{
					URI:      "insightcell://payload.json",
					MIMEType: "application/json",
					Text:     string(payload),
				},
			},
		},
		StructuredContent: cell,
	}, nil
}

var RenderInsightCell = mcpgrafana.MustTool(
	"render_insight_cell",
	"Render a Grafana result you have gathered as an interactive 'insight cell' in the chat: a core panel "+
		"(timeseries, stat, bar, table), a logs view, a trace waterfall, or a synthesis view — 'worklist' (ranked, "+
		"actionable findings for alert triage / deprecations), 'rca' (root-cause investigation), 'rulediff' (a proposed "+
		"alert-rule fix shown as a before/after diff), 'timeline' (change correlation) or 'cost' (cardinality/cost drivers) "+
		"— with a verdict, attestation, provenance and drill actions. This renders data you already have: first use the "+
		"query tools (query_prometheus, query_loki_logs, list_alert_rules, get_annotations, Sift, ...), do the analysis, "+
		"then pass the results here as 'frames' (chart types) or the matching payload (items/findings/changes/events/drivers), "+
		"along with a one-line 'verdict' and a 2-4 sentence 'insight'. It does not query datasources itself. Hosts without "+
		"MCP Apps support still get the text verdict and the JSON payload.",
	renderInsightCell,
	mcp.WithTitleAnnotation("Render a Grafana insight cell"),
	mcp.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithUIResource(mcpgrafana.InsightCellResourceURI),
)

func AddInsightCellTools(mcp *server.MCPServer) {
	RenderInsightCell.Register(mcp)
}
