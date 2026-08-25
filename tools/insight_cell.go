// The insight-cell tool renders any Grafana result the agent has gathered as an
// interactive "insight cell" in an MCP host — a core panel (timeseries/stat/bar/
// table), a logs/trace view, or a synthesis view (worklist/rca/rulediff/timeline/
// cost) — carrying a verdict, attestation, provenance and drill actions.
//
// It is a *render substrate*: the agent does the analysis with the existing query
// tools (query_prometheus, query_loki_logs, alerting_manage_rules, get_annotations,
// …) and hands the assembled data here; the tool packages it into the render
// contract and the trust metadata. It does NOT query datasources or fabricate data
// itself, and the rendered cell is structurally read-only: it can never invoke
// another MCP tool (see sanitizeActions). Writes go through the agent.
//
// The result rides three channels so it degrades across hosts:
//   - content[0]  a text verdict (fallback for hosts without MCP Apps)
//   - content[1]  an embedded application/json resource block (kept by hosts that
//     drop structuredContent, e.g. Claude Desktop, which converts it
//     to a text block the app scans)
//   - structuredContent  the insightCell (the spec channel)
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

// --- Render contract (mirrors ui/insight-cell/src/schema.ts) -----------------
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

// icAction is a cell action. Kinds are deliberately limited to ones that keep
// the cell read-only: "link" opens a URL via the host, "refresh" re-runs
// render_insight_cell with the same payload, and "ask" hands text back to the
// agent as the next message. There is no kind that invokes an arbitrary MCP
// tool from inside the cell — that would make the tool's ReadOnlyHint a lie.
type icAction struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"` // link | refresh | ask
	URL     string `json:"url,omitempty"`
	Text    string `json:"text,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Icon    string `json:"icon,omitempty"`
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

// icAttestation and icProvenance are agent-declared, not verified: the server
// cannot check that the frames actually came from the recorded query and
// datasource — it repackages what the agent gathered. Hence "renderedBy"
// (what this server did) rather than "author" (which would imply the data
// itself is vouched for), and "declaredLive"/"declared" wording in the UI.
type icAttestation struct {
	AsOf string `json:"asOf"`
	Live bool   `json:"live"` // agent declared a query + datasource; not verified
}

type icProvenance struct {
	RenderedBy string `json:"renderedBy"` // who packaged/rendered the cell, not who produced the data
	Datasource string `json:"datasource"` // agent-declared origin of the data
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
	DataMode    string        `json:"dataMode"` // agent-supplied | synthesized (declared, not verified)
}

type icTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// insightCell is the full render contract emitted as structuredContent.
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

	Query         string `json:"query,omitempty" jsonschema:"description=The PromQL/LogQL/TraceQL expression the data came from (recorded as agent-declared provenance; not executed or verified here)."`
	DatasourceUID string `json:"datasourceUid,omitempty" jsonschema:"description=UID of the datasource the data came from (recorded as agent-declared provenance; not verified here)."`
	RangeHours    *int   `json:"rangeHours,omitempty" jsonschema:"description=Look-back window in hours for the recorded time range (default 1)."`
	DataAsOf      string `json:"dataAsOf,omitempty" jsonschema:"description=RFC3339 timestamp of when you actually gathered the data from the query tools. Defaults to now\\, which is only correct if you just fetched it. Keep the original stamp when re-rendering the same data (the cell's refresh action does this automatically) so stale data never claims to be fresh."`

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

	// rulediff — the cell only *proposes*; it cannot write. Applying goes through
	// the agent calling the write-gated alerting_manage_rules tool, then
	// re-rendering the cell with applied=true.
	RuleTitle    string             `json:"ruleTitle,omitempty" jsonschema:"description=For panel='rulediff': the alert rule's name."`
	RuleUID      string             `json:"ruleUid,omitempty" jsonschema:"description=For panel='rulediff': the alert rule UID (from alerting_manage_rules operation 'list'). Needed when the user asks you to apply the change via alerting_manage_rules."`
	RuleSummary  string             `json:"ruleSummary,omitempty" jsonschema:"description=For panel='rulediff': one line — what the fix does and why."`
	Changes      []icRuleDiffChange `json:"changes,omitempty" jsonschema:"description=For panel='rulediff': the before/after changes you're proposing."`
	ProposedRule map[string]any     `json:"proposedRule,omitempty" jsonschema:"description=For panel='rulediff': the full updated alert-rule JSON to pass to alerting_manage_rules (operation 'update') if the user asks to apply it."`
	Applied      bool               `json:"applied,omitempty" jsonschema:"description=For panel='rulediff': set true when re-rendering the diff after alerting_manage_rules applied it\\, so the cell shows the APPLIED state."`

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
	Actions []icAction `json:"actions,omitempty" jsonschema:"description=Follow-up actions shown under the panel. kind 'link' opens a URL; 'refresh' re-runs render_insight_cell with the same payload; 'ask' sends the action's text back to you (the agent) as the next message. The cell is read-only and cannot invoke MCP tools itself — for a write like applying a rulediff\\, add an 'ask' action (e.g. label 'Apply this change') whose text asks you to apply it via alerting_manage_rules and re-render with applied=true."`
}

// --- Handler -----------------------------------------------------------------

// panelNames mirrors the enum on RenderInsightCellParams.Panel. The jsonschema
// enum is advisory to the model only — nothing enforces it at call time — so
// the handler validates against this list itself.
var panelNames = []string{
	"timeseries", "stat", "bullet", "bar", "table",
	"logs", "trace",
	"worklist", "rca", "rulediff", "timeline", "cost",
}

var validPanels = func() map[string]bool {
	m := make(map[string]bool, len(panelNames))
	for _, p := range panelNames {
		m[p] = true
	}
	return m
}()

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
	if !validPanels[args.Panel] {
		return nil, fmt.Errorf("unknown panel %q, must be one of: %s", args.Panel, strings.Join(panelNames, ", "))
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

	// Anchor the recorded time range and attestation on when the data was
	// gathered, not on when this handler runs: a refresh replays the same data
	// through this tool, and stamping time.Now() would make stale data claim to
	// be fresh. First renders (no dataAsOf) default to now, which matches the
	// "query first, then render" flow the tool description prescribes.
	anchor := time.Now().UTC()
	if args.DataAsOf != "" {
		parsed, err := time.Parse(time.RFC3339, args.DataAsOf)
		if err != nil {
			return nil, fmt.Errorf("dataAsOf must be an RFC3339 timestamp, got %q: %w", args.DataAsOf, err)
		}
		anchor = parsed.UTC()
	}
	rangeHours := 1
	if args.RangeHours != nil && *args.RangeHours > 0 {
		rangeHours = *args.RangeHours
	}
	fromISO := anchor.Add(-time.Duration(rangeHours) * time.Hour).Format(time.RFC3339)
	toISO := anchor.Format(time.RFC3339)

	// Provenance / attestation. "agent-supplied" means the agent *declared* a
	// query + datasource it read the data from — it should be real, but the
	// server cannot verify that the frames actually came from that query.
	// "synthesized" means no such declaration: representative/sample content or
	// synthesis-view material assembled by the agent. Deliberately not called
	// "live"/"mock" so the label never claims more than the server can check —
	// see the icAttestation/icProvenance comment.
	live := args.Query != "" && args.DatasourceUID != ""
	dataMode := "synthesized"
	if live {
		dataMode = "agent-supplied"
	}
	datasource := "sample (no datasource declared)"
	if live {
		// Prefer the public app URL (what a user can actually open) over the
		// configured URL, which may be an in-cluster endpoint. This string shows
		// in the cell header, text fallback, and trust _meta.
		base, err := grafanaBaseURLFromContext(ctx)
		if err != nil || base == "" {
			base = mcpgrafana.GrafanaConfigFromContext(ctx).URL
		}
		if base != "" {
			datasource = fmt.Sprintf("%s (%s)", base, args.DatasourceUID)
		} else {
			datasource = args.DatasourceUID
		}
	} else if args.DatasourceUID != "" {
		datasource = args.DatasourceUID
	}

	// Always an array (never nil): the UI iterates meta.query and refresh reads
	// query[0], so a JSON null would blank the common no-query (sample) path.
	queries := []icQueryRef{}
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
			Provenance:  icProvenance{RenderedBy: "Grafana MCP", Datasource: datasource},
			Query:       queries,
			DataMode:    dataMode,
		},
	}
	// These slice fields are marshalled without omitempty and the UI iterates
	// them, so a nil slice (JSON `null`) would make the renderer throw and blank
	// the cell. Normalise every such field to an empty slice via orEmpty.
	cell.Actions = sanitizeActions(orEmpty(cell.Actions))
	for i := range cell.Worklist {
		cell.Worklist[i].Actions = sanitizeActions(cell.Worklist[i].Actions)
	}
	if cell.Trace != nil {
		cell.Trace.Spans = orEmpty(cell.Trace.Spans)
	}
	for i := range cell.Frames {
		cell.Frames[i].Fields = orEmpty(cell.Frames[i].Fields)
		for j := range cell.Frames[i].Fields {
			cell.Frames[i].Fields[j].Values = orEmpty(cell.Frames[i].Fields[j].Values)
		}
	}

	// Synthesis-view payloads.
	if args.RootCause != nil || len(args.Findings) > 0 || len(args.Checks) > 0 {
		cell.RCA = &icRcaPayload{RootCause: args.RootCause, Checks: args.Checks, Findings: orEmpty(args.Findings)}
	}
	if args.RuleTitle != "" || len(args.Changes) > 0 {
		cell.RuleDiff = &icRuleDiffPayload{
			RuleTitle: args.RuleTitle,
			RuleUID:   args.RuleUID,
			Summary:   args.RuleSummary,
			Changes:   orEmpty(args.Changes),
			Proposed:  args.ProposedRule,
			Applied:   args.Applied,
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
		cell.Cost = &icCostPayload{Total: args.CostTotal, Drivers: orEmpty(args.Drivers), Headroom: args.Headroom}
	}

	return cell, nil
}

// sanitizeActions keeps the cell structurally read-only: the tool is annotated
// ReadOnlyHint, so an action that isn't one of the safe kinds (link / refresh /
// ask) is dropped here and never reaches the UI — regardless of what an agent
// put in `actions`. Writes happen when the agent itself calls a write-gated
// tool (e.g. alerting_manage_rules), typically prompted by an "ask" action.
func sanitizeActions(actions []icAction) []icAction {
	out := actions[:0]
	for _, a := range actions {
		switch a.Kind {
		case "link", "refresh", "ask":
			out = append(out, a)
		}
	}
	return out
}

// orEmpty returns s, or an empty (non-nil) slice when s is nil, so a UI-iterated
// contract field never marshals as JSON `null`. Prefer this over per-field
// guards so a newly added slice field is one call away from being safe.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
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
// _meta trust profile.
//
// The full cell is deliberately emitted twice — structuredContent (the spec
// channel) and an embedded application/json resource block — because host
// support is inconsistent: some hosts drop structuredContent entirely, and
// Claude Desktop converts the resource block to a text block the app then
// scans (see extractCell in ui/insight-cell/src/mcp-app.ts for the fallback
// order). Together with the tool-call arguments the same frames/table payload
// can therefore travel through model context ~2-3x. That token cost is the
// price of rendering on hosts we don't control; revisit dropping the embedded
// block once structuredContent forwarding is reliable across major hosts.
// Keep payloads small: the tool description steers agents to pass shaped,
// analysis-sized data, not raw query dumps.
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
	fmt.Fprintf(&b, "As of %s · %s data.", cell.Meta.Attestation.AsOf, cell.Meta.DataMode)

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
		"— with a verdict, attestation, provenance and follow-up actions. This renders data you already have: first use the "+
		"query tools (query_prometheus, query_loki_logs, alerting_manage_rules, get_annotations, Sift, ...), do the analysis, "+
		"then pass the results here as 'frames' (chart types) or the matching payload (items/findings/changes/events/drivers), "+
		"along with a one-line 'verdict' and a 2-4 sentence 'insight'. It does not query datasources itself, and the rendered "+
		"cell is read-only — it never invokes other MCP tools; writes (like applying a rulediff) happen when you call the "+
		"write-gated tool yourself. Hosts without MCP Apps support still get the text verdict and the JSON payload.",
	renderInsightCell,
	mcp.WithTitleAnnotation("Render a Grafana insight cell"),
	mcp.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithUIResource(mcpgrafana.InsightCellResourceURI),
)

func AddInsightCellTools(mcp *server.MCPServer) {
	RenderInsightCell.Register(mcp)
}
