package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
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
func failedQueryNotes(responses backend.Responses) []string {
	refIDs := make([]string, 0, len(responses))
	for refID, r := range responses {
		if r.Error != nil {
			refIDs = append(refIDs, refID)
		}
	}
	slices.Sort(refIDs)
	notes := make([]string, 0, len(refIDs))
	for _, refID := range refIDs {
		notes = append(notes, fmt.Sprintf("Query %s failed: %s", refID, responses[refID].Error))
	}
	return notes
}

type dsPanel struct {
	Type    string         `json:"type"`
	Options map[string]any `json:"options"`
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

func queryDatasource(ctx context.Context, args QueryDatasourceParams) (*mcp.CallToolResult, error) {
	if args.DatasourceUID == "" {
		return nil, fmt.Errorf("datasourceUid is required")
	}
	from, to, err := resolveDSRange(args.From, args.To, time.Now())
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
	resp, err := doDSQuery(ctx, client, baseURL, dsQueryPayload(from, to, queries...))
	if err != nil {
		return nil, fmt.Errorf("querying data source %s: %w", args.DatasourceUID, err)
	}
	capturedAt := time.Now().UnixMilli()
	if len(resp.Responses) == 0 {
		return nil, fmt.Errorf("the data source %s returned no results", args.DatasourceUID)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshaling results: %w", err)
	}
	var envelope struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("reading results: %w", err)
	}

	// A later slice picks the chart from the data. Until then every result is a time series.
	result := &QueryDatasourceResult{
		Chart:        "timeseries",
		Panel:        dsPanel{Type: "grafana-echartscartesian-panel", Options: map[string]any{"seriesType": "line"}},
		Alternatives: []dsAlternative{},
		Data: dsData{
			Range:      dsRange{From: from.UnixMilli(), To: to.UnixMilli()},
			CapturedAt: capturedAt,
			Results:    envelope.Results,
		},
		notes: failedQueryNotes(resp.Responses),
	}
	out := mcpgrafana.NewToolResultText(fmt.Sprintf("Queried data source %s.", args.DatasourceUID))
	// Past the budget there is no structured content, and render_grafana_app reports it.
	if body, err := json.Marshal(result); err == nil && len(body) <= maxDSResultBytes {
		out.StructuredContent = result
	}
	return out, nil
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

var QueryDatasource = mcpgrafana.MustTool(
	"query_datasource",
	"Query any Grafana data source with /api/ds/query queries and return the frames for the Grafana App. render_grafana_app runs it: it is not a registered tool.",
	queryDatasource,
	mcpgrafana.WithTitleAnnotation("Query a data source"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	// The queries pass to the data source unfiltered, so they can mutate data (tool.2).
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)
