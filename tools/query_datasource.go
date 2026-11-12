package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// query_datasource is a source of render_grafana_app: it charts the queries of any data source. It is not
// registered, so the model reaches it only through render_grafana_app (viz-contract tool.3).

const (
	defaultDSFrom          = "now-1h"
	defaultDSTo            = "now"
	defaultDSMaxDataPoints = 1000
	maxDSResultBytes       = 1 << 20
	maxDSTitleChars        = 100
)

// dsTime is a time of /api/ds/query: a string, or an integer of epoch milliseconds (tool.6).
type dsTime string

func (t *dsTime) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*t = dsTime(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("must be a string or an integer, got: %s", data)
	}
	*t = dsTime(strconv.FormatInt(n, 10))
	return nil
}

func (dsTime) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{{Type: "string"}, {Type: "integer"}}}
}

type QueryDatasourceParams struct {
	DatasourceUID string           `json:"datasourceUid" jsonschema:"required,description=UID of the data source. All queries use this data source."`
	Queries       []map[string]any `json:"queries" jsonschema:"required,description=The queries of /api/ds/query: a unique refId and the fields of the data source\\, for example expr and legendFormat for Prometheus."`
	From          dsTime           `json:"from,omitempty" jsonschema:"description=Start of the time range\\, in a format that /api/ds/query accepts\\, for example now-6h or epoch milliseconds. Defaults to now-1h."`
	To            dsTime           `json:"to,omitempty" jsonschema:"description=End of the time range\\, in a format that /api/ds/query accepts\\, for example now or epoch milliseconds. Defaults to now."`
	Type          dsChartType      `json:"type,omitempty"`
	Unit          string           `json:"unit,omitempty" jsonschema:"minLength=1,description=Optional Grafana unit ID for all values\\, for example bytes\\, percent or reqps."`
	Title         dsTitle          `json:"title,omitempty" jsonschema:"description=Optional title of the chart\\, for example HTTP requests by handler. The chart shows it as a panel title."`
}

// dsTitle is the title of the chart (tool.31). The schema is that of the contract: some text that is not white space,
// of at most maxTitleChars characters. The field tag gives the description.
type dsTitle string

func (dsTitle) JSONSchema() *jsonschema.Schema {
	maxLength := uint64(maxDSTitleChars)
	return &jsonschema.Schema{Type: "string", Pattern: `\S`, MaxLength: &maxLength}
}

// validate checks the schema of the title (tool.4). The length is in code points, as in JSON Schema.
func (t dsTitle) validate() error {
	if t == "" {
		return nil
	}
	if !strings.ContainsFunc(string(t), func(r rune) bool { return !isECMAScriptSpace(r) }) {
		return fmt.Errorf("title must not be only white space")
	}
	if utf8.RuneCountInString(string(t)) > maxDSTitleChars {
		return fmt.Errorf("title must be at most %d characters", maxDSTitleChars)
	}
	return nil
}

// isECMAScriptSpace reports whether \s of an ECMAScript regular expression, the dialect of JSON Schema, matches r.
func isECMAScriptSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// dsChartType is a chart of chart-types.json (tool.7). With no type, the Source picks the chart.
type dsChartType string

func (dsChartType) JSONSchema() *jsonschema.Schema {
	names := vizChartNames()
	enum := make([]any, len(names))
	for i, n := range names {
		enum[i] = n
	}
	return &jsonschema.Schema{Type: "string", Enum: enum}
}

// QueryDatasourceResult is the structured content of the tool result (viz-contract schemas/tool-result.schema.json).
type QueryDatasourceResult struct {
	Chart        string          `json:"chart"`
	Panel        dsPanel         `json:"panel"`
	Alternatives []dsAlternative `json:"alternatives"`
	Data         dsData          `json:"data"`

	// notes are for render_grafana_app, not for the App (tool.28, tool.29).
	notes []string
}

// AppNotes gives one note for each failed query (tool.29).
func (r *QueryDatasourceResult) AppNotes() []string { return r.notes }

// failedQueryNotes makes the note of each entry with an error, in the order of the refIds. The text is the
// query-failed message of notices.json (tool.29).
func failedQueryNotes(results map[string]vizResult) []string {
	refIDs := make([]string, 0, len(results))
	for refID, e := range results {
		if e.Error != "" {
			refIDs = append(refIDs, refID)
		}
	}
	slices.Sort(refIDs)
	notes := make([]string, 0, len(refIDs))
	for _, refID := range refIDs {
		notes = append(notes, fmt.Sprintf("Query %s failed: %s", refID, results[refID].Error))
	}
	return notes
}

type dsPanel struct {
	Type        string         `json:"type"`
	Title       string         `json:"title,omitempty"`
	Options     map[string]any `json:"options"`
	FieldConfig *dsFieldConfig `json:"fieldConfig,omitempty"`
}

type dsFieldConfig struct {
	Defaults struct {
		Unit string `json:"unit"`
	} `json:"defaults"`
}

// dsPanelOf makes the panel of a chart: the panel ID and the options of its row, and the unit and the title of the
// arguments (tool.22, tool.31).
func dsPanelOf(chart vizChart, unit string, title dsTitle) dsPanel {
	panel := dsPanel{Type: chart.Panel, Title: string(title), Options: maps.Clone(chart.Options)}
	if unit != "" {
		panel.FieldConfig = &dsFieldConfig{}
		panel.FieldConfig.Defaults.Unit = unit
	}
	return panel
}

type dsAlternative struct {
	Chart string  `json:"chart"`
	Panel dsPanel `json:"panel"`
}

type dsData struct {
	Range      dsRange         `json:"range"`
	CapturedAt int64           `json:"capturedAt"`
	Results    json.RawMessage `json:"results"`
}

type dsRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// dsNow is the clock of the Source. The conformance tests set it.
var dsNow = time.Now

func queryDatasource(ctx context.Context, args QueryDatasourceParams) (*mcp.CallToolResult, error) {
	if args.DatasourceUID == "" {
		return nil, fmt.Errorf("datasourceUid is required")
	}
	if args.Type != "" && vizChartByName(vizCharts, string(args.Type)) == nil {
		return nil, fmt.Errorf("type must be one of: %s", strings.Join(vizChartNames(), ", "))
	}
	if err := args.Title.validate(); err != nil {
		return nil, err
	}
	from, to, err := resolveDSRange(args.From, args.To, dsNow())
	if err != nil {
		return nil, err
	}
	queries, err := prepareDSQueries(args.DatasourceUID, args.Queries, from, to)
	if err != nil {
		return nil, err
	}
	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	// The raw body, so that data.results is the results object of the response with no change (tool.15). The
	// results are in the structured content as they came, and Grafana writes compact JSON, so a body past the budget
	// gives a result past it: stop reading there (tool.24).
	status, raw, err := doDSQueryRaw(ctx, client, baseURL, map[string]any{
		"queries": queries,
		"from":    string(cmp.Or(args.From, defaultDSFrom)),
		"to":      string(cmp.Or(args.To, defaultDSTo)),
	}, maxDSResultBytes)
	if errors.Is(err, errResponseBodyTooLarge) {
		return dsTooLarge(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying data source %s: %w", args.DatasourceUID, err)
	}
	capturedAt := dsNow().UnixMilli()
	results, rawResults, decodeErr := decodeDSResults(raw)
	// tool.16: a status other than 200 with results is a partial result, and the failed queries become notes
	// (tool.29). Without results, it is an error.
	if status != http.StatusOK && (decodeErr != nil || len(results) == 0) {
		return nil, fmt.Errorf("querying data source %s: %w", args.DatasourceUID, dsQueryStatusError(status, raw))
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("reading the response of data source %s: %w", args.DatasourceUID, decodeErr)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("the data source %s returned no results", args.DatasourceUID)
	}

	chart, alternatives, mismatch := chooseDSChart(results, args.Type, args.Unit, args.Title)
	if mismatch != "" {
		return dsToolError("type-mismatch", mismatch), nil
	}
	result := &QueryDatasourceResult{
		Chart:        chart.Name,
		Panel:        dsPanelOf(*chart, args.Unit, args.Title),
		Alternatives: alternatives,
		Data: dsData{
			Range:      dsRange{From: from.UnixMilli(), To: to.UnixMilli()},
			CapturedAt: capturedAt,
			Results:    rawResults,
		},
		notes: failedQueryNotes(results),
	}
	// Past the budget the Source gives the too-large tool error, and no structured content (tool.24). The size is that
	// of the result with the results as they came, so they are not encoded again here; json.Marshal compacts them
	// later, so the result is never larger.
	size, err := dsResultSize(*result)
	if err != nil {
		return nil, err
	}
	if size > maxDSResultBytes {
		return dsTooLarge(), nil
	}
	out := mcpgrafana.NewToolResultText(fmt.Sprintf("Queried data source %s.", args.DatasourceUID))
	out.StructuredContent = result
	return out, nil
}

// chooseDSChart gives the chart of the type, or the chart of the pick, and the other charts that fit the frames
// (tool.18 to tool.23). If the model gave a type and the frames have rows, the frames must fit it (tool.20). If they
// do not, the message of type-mismatch is the third value.
func chooseDSChart(results map[string]vizResult, chartType dsChartType, unit string, title dsTitle) (*vizChart, []dsAlternative, string) {
	summary := vizSummary(results)
	picked := vizPick(summary)
	chart := picked
	if chartType != "" {
		chart = vizChartByName(vizCharts, string(chartType))
		if summary["rowCountTotal"].(int) > 0 && !vizFits(summary, *chart) {
			var fitting []string
			for _, c := range vizCharts {
				if vizFits(summary, c) {
					fitting = append(fitting, c.Name)
				}
			}
			if len(fitting) == 0 {
				fitting = []string{picked.Name}
			}
			return nil, nil, fmt.Sprintf("%s needs %s. Charts that fit: %s.", chart.Name, chart.DataShape, strings.Join(fitting, ", "))
		}
	}
	alternatives := []dsAlternative{}
	for _, c := range vizCharts {
		if c.Name != chart.Name && vizFits(summary, c) {
			alternatives = append(alternatives, dsAlternative{Chart: c.Name, Panel: dsPanelOf(c, unit, title)})
		}
	}
	return chart, alternatives, ""
}

// decodeDSResults gives the entries of the results object of a response, for the summary and the notes, and the
// results object as it came, for data.results (tool.15).
func decodeDSResults(body []byte) (map[string]vizResult, json.RawMessage, error) {
	var envelope struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, err
	}
	if len(envelope.Results) == 0 {
		return nil, nil, nil
	}
	var results map[string]vizResult
	if err := json.Unmarshal(envelope.Results, &results); err != nil {
		return nil, nil, fmt.Errorf("reading the frames: %w", err)
	}
	return results, envelope.Results, nil
}

// dsResultSize is the size of the JSON of the result: the result with null results, plus the results.
func dsResultSize(result QueryDatasourceResult) (int, error) {
	results := result.Data.Results
	result.Data.Results = nil
	body, err := json.Marshal(result)
	if err != nil {
		return 0, fmt.Errorf("marshaling the tool result: %w", err)
	}
	return len(body) - len("null") + len(results), nil
}

func dsTooLarge() *mcp.CallToolResult {
	return dsToolError("too-large", fmt.Sprintf("The result is larger than %d bytes. Use a shorter range, a larger intervalMs or fewer series.", maxDSResultBytes))
}

// dsToolError is a tool error of states.json: one text item, the code, a colon, a space and the message (tool.25).
func dsToolError(code, message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: code + ": " + message}}}
}

// resolveDSRange gives the absolute range of from and to (tool.5, tool.6, tool.14).
func resolveDSRange(from, to dsTime, now time.Time) (time.Time, time.Time, error) {
	if from == "" {
		from = defaultDSFrom
	}
	if to == "" {
		to = defaultDSTo
	}
	tr := gtime.TimeRange{From: string(from), To: string(to), Now: now}
	start, err := tr.ParseFrom()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing from: %w", err)
	}
	end, err := tr.ParseTo()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing to: %w", err)
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be before to")
	}
	return start, end, nil
}

// prepareDSQueries checks the queries and gives each one the defaults of tool.9 to tool.12. The input is not changed.
func prepareDSQueries(uid string, queries []map[string]any, from, to time.Time) ([]map[string]any, error) {
	if len(queries) == 0 {
		return nil, fmt.Errorf("queries needs at least one query")
	}
	rangeMs := to.UnixMilli() - from.UnixMilli()
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(queries))
	for i, in := range queries {
		refID, _ := in["refId"].(string)
		if refID == "" {
			return nil, fmt.Errorf("query %d has no refId", i+1)
		}
		if seen[refID] {
			return nil, fmt.Errorf("refId %s is used more than once", refID)
		}
		seen[refID] = true

		query := make(map[string]any, len(in)+3)
		for k, v := range in {
			query[k] = v
		}
		if ds := in["datasource"]; ds != nil {
			if m, _ := ds.(map[string]any); m == nil || m["uid"] != uid {
				return nil, fmt.Errorf("query %s uses another data source: its datasource uid must be %s", refID, uid)
			}
		} else {
			query["datasource"] = map[string]any{"uid": uid}
		}
		maxDataPoints := float64(defaultDSMaxDataPoints)
		if v, ok := in["maxDataPoints"]; ok {
			n, isNumber := v.(float64)
			if !isNumber || n < 1 {
				return nil, fmt.Errorf("query %s: maxDataPoints must be a positive number", refID)
			}
			maxDataPoints = n
		} else {
			query["maxDataPoints"] = defaultDSMaxDataPoints
		}
		if _, ok := in["intervalMs"]; !ok {
			query["intervalMs"] = max(int64(float64(rangeMs)/maxDataPoints), 1)
		}
		out = append(out, query)
	}
	return out, nil
}

// QueryDatasource is not a registered tool: render_grafana_app runs it.
var QueryDatasource = withChartDescription(mcpgrafana.MustTool(
	"query_datasource",
	"Query any Grafana data source with /api/ds/query queries and return the frames for the Grafana App.",
	queryDatasource,
	mcpgrafana.WithTitleAnnotation("Query a data source"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	// The queries pass to the data source unfiltered, so they can mutate data (tool.2).
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
))

// withChartDescription gives the type argument the data shape of each chart. The registry makes the text, so a struct
// tag cannot hold it.
func withChartDescription(tool mcpgrafana.Tool) mcpgrafana.Tool {
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.InputSchema.(json.RawMessage), &schema); err != nil {
		panic(err)
	}
	schema["properties"].(map[string]any)["type"].(map[string]any)["description"] = vizChartDescription()
	body, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	tool.Tool.InputSchema = json.RawMessage(body)
	return tool
}
