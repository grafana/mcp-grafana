package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// QueryDatasourceToolName is the render_grafana_app source that displays any
// datasource query. The source is passed to AddAppTool only and is never
// registered on its own, so the model sees it only as one branch of
// render_grafana_app's arguments, and the App refreshes through
// render_grafana_app.
const QueryDatasourceToolName = "query_datasource"

const (
	defaultDatasourceMaxDataPoints = 1000
	maxDatasourceViewBytes         = 1 << 20
)

// QueryDatasourceParams is an /api/ds/query request.
type QueryDatasourceParams struct {
	DatasourceUID string           `json:"datasourceUid" jsonschema:"required,description=Datasource UID for queries without a datasource"`
	Queries       []map[string]any `json:"queries" jsonschema:"required,description=/api/ds/query queries: refId plus the datasource fields (Prometheus: expr\\, legendFormat)"`
	From          string           `json:"from,omitempty" jsonschema:"description=Start; default now-1h"`
	To            string           `json:"to,omitempty" jsonschema:"description=End; default now"`
}

// withSchemaDescription names the source's branch of render_grafana_app's
// arguments, which lists source schemas without their tool descriptions.
func withSchemaDescription(description string) mcpgrafana.ToolOption {
	return func(tool *mcp.Tool) {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema.(json.RawMessage), &schema); err != nil {
			panic(err)
		}
		schema["description"] = description
		body, err := json.Marshal(schema)
		if err != nil {
			panic(err)
		}
		tool.InputSchema = json.RawMessage(body)
	}
}

// QueryDatasourceResult is the App payload: the request as sent, the absolute
// range it resolved to, the /api/ds/query results with no change, and the
// panel picked from their shape.
type QueryDatasourceResult struct {
	Request    datasourceRequest `json:"request"`
	Range      datasourceRange   `json:"range"`
	CapturedAt int64             `json:"capturedAt"`
	Results    json.RawMessage   `json:"results"`
	Panel      datasourcePanel   `json:"panel"`
	Pick       vizPick           `json:"pick"`
}

type datasourceRequest struct {
	From    string           `json:"from"`
	To      string           `json:"to"`
	Queries []map[string]any `json:"queries"`
}

type datasourceRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type datasourcePanel struct {
	Type        string         `json:"type"`
	Options     map[string]any `json:"options,omitempty"`
	FieldConfig map[string]any `json:"fieldConfig,omitempty"`
}

func queryDatasource(ctx context.Context, args QueryDatasourceParams) (*mcp.CallToolResult, error) {
	if args.DatasourceUID == "" {
		return nil, fmt.Errorf("datasourceUid is required")
	}
	if len(args.Queries) == 0 {
		return nil, fmt.Errorf("queries must contain at least one query")
	}
	if args.From == "" {
		args.From = "now-1h"
	}
	if args.To == "" {
		args.To = "now"
	}
	from, err := parseTime(args.From)
	if err != nil {
		return nil, fmt.Errorf("parsing from: %w", err)
	}
	to, err := parseTime(args.To)
	if err != nil {
		return nil, fmt.Errorf("parsing to: %w", err)
	}
	if !from.Before(to) {
		return nil, fmt.Errorf("from must be before to")
	}
	queries, refIDs := normalizeDatasourceQueries(args, to.Sub(from))

	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := doDSQuery(ctx, client, baseURL, dsQueryPayload(from, to, queries...))
	if err != nil {
		return nil, err
	}
	results, err := json.Marshal(resp.Responses)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource results: %w", err)
	}
	rule, err := runVizPick(vizPickTable, results, refIDs)
	if err != nil {
		return nil, err
	}

	out := QueryDatasourceResult{
		Request:    datasourceRequest{From: args.From, To: args.To, Queries: queries},
		Range:      datasourceRange{From: from.UnixMilli(), To: to.UnixMilli()},
		CapturedAt: time.Now().UnixMilli(),
		Results:    results,
		Panel:      datasourcePanel{Type: rule.Type, Options: rule.Options, FieldConfig: rule.FieldConfig},
		Pick:       vizPick{Rule: rule.ID, Type: rule.Type},
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource view: %w", err)
	}
	result := mcpgrafana.NewToolResultText(summarizeDatasourceResult(resp.Responses, refIDs, rule))
	if len(body) <= maxDatasourceViewBytes {
		result.StructuredContent = json.RawMessage(body)
	}
	return result, nil
}

// normalizeDatasourceQueries copies the queries and fills what Grafana needs
// and the model may leave out: the datasource, the refId and the resolution.
func normalizeDatasourceQueries(args QueryDatasourceParams, span time.Duration) ([]map[string]any, []string) {
	queries := make([]map[string]any, len(args.Queries))
	refIDs := make([]string, len(args.Queries))
	for i, q := range args.Queries {
		c := make(map[string]any, len(q)+4)
		for k, v := range q {
			c[k] = v
		}
		if _, ok := c["datasource"]; !ok {
			c["datasource"] = map[string]any{"uid": args.DatasourceUID}
		}
		refID, _ := c["refId"].(string)
		if refID == "" {
			refID = defaultRefID(i)
			c["refId"] = refID
		}
		if _, ok := c["maxDataPoints"]; !ok {
			c["maxDataPoints"] = defaultDatasourceMaxDataPoints
		}
		if _, ok := c["intervalMs"]; !ok {
			c["intervalMs"] = max(int64(1000), span.Milliseconds()/defaultDatasourceMaxDataPoints)
		}
		queries[i], refIDs[i] = c, refID
	}
	return queries, refIDs
}

func defaultRefID(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	return fmt.Sprintf("Q%d", i)
}

// summarizeDatasourceResult is the text block: the picked panel and the shape
// of each query result, with no rows.
func summarizeDatasourceResult(responses backend.Responses, refIDs []string, rule *vizPickRule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Panel %s (rule %s).", rule.Type, rule.ID)
	ids := append([]string(nil), refIDs...)
	sort.Strings(ids)
	for _, refID := range ids {
		r, ok := responses[refID]
		switch {
		case !ok:
			fmt.Fprintf(&b, " %s: no result.", refID)
		case r.Error != nil:
			fmt.Fprintf(&b, " %s: %s.", refID, r.Error)
		default:
			fmt.Fprintf(&b, " %s: %d frames.", refID, len(r.Frames))
		}
	}
	return b.String()
}

// QueryDatasourceSource is the render_grafana_app source for any datasource.
// Queries pass through to the datasource unfiltered, so raw SQL can write: its
// hints match query_sql, and the server gates it like the raw query tools.
var QueryDatasourceSource = mcpgrafana.MustTool(
	QueryDatasourceToolName,
	"Query any datasource through Grafana's /api/ds/query and display the frames.",
	queryDatasource,
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
	withSchemaDescription(QueryDatasourceToolName+": any datasource through Grafana; the view is picked from the result"),
)
