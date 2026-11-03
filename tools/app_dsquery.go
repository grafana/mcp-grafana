package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SPIKE (query-path-spike-plan.md, option C′): a render_grafana_app source that
// posts any datasource query to /api/ds/query and hands the frames to the App
// unchanged. It is passed to AddAppTool only and is never registered on the
// server, so the model sees it only as one branch of render_grafana_app's
// `arguments`. The App refreshes through render_grafana_app itself.

// QueryDatasourceParams is the /api/ds/query request in the shape the model writes.
type QueryDatasourceParams struct {
	DatasourceUID string           `json:"datasourceUid" jsonschema:"required,description=Datasource UID; queries without a datasource use it"`
	Queries       []map[string]any `json:"queries" jsonschema:"required,description=/api/ds/query queries: refId plus the datasource fields (Prometheus: expr\\, legendFormat)"`
	From          string           `json:"from,omitempty" jsonschema:"description=Start\\, e.g. now-1h (default)"`
	To            string           `json:"to,omitempty" jsonschema:"description=End\\, e.g. now (default)"`
	Type          string           `json:"type,omitempty" jsonschema:"description=Optional panel type hint\\, e.g. timeseries. Default: chosen from the data"`
}

// QueryDatasourceResult is the App payload: the request as sent, the absolute
// range it resolved to, and the /api/ds/query results with no change.
type QueryDatasourceResult struct {
	Request struct {
		From    string           `json:"from"`
		To      string           `json:"to"`
		Queries []map[string]any `json:"queries"`
	} `json:"request"`
	Range struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	} `json:"range"`
	CapturedAt int64             `json:"capturedAt"`
	Type       string            `json:"type,omitempty"`
	Results    backend.Responses `json:"results"`
}

const maxDatasourceViewBytes = 1 << 20

func queryDatasourceTool(ctx context.Context, args QueryDatasourceParams) (*mcp.CallToolResult, error) {
	if len(args.Queries) == 0 {
		return nil, fmt.Errorf("queries must have at least one query")
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
	queries := make([]map[string]any, len(args.Queries))
	for i, q := range args.Queries {
		c := make(map[string]any, len(q)+2)
		for k, v := range q {
			c[k] = v
		}
		if _, ok := c["datasource"]; !ok {
			c["datasource"] = map[string]any{"uid": args.DatasourceUID}
		}
		if _, ok := c["refId"]; !ok {
			c["refId"] = string(rune('A' + i))
		}
		queries[i] = c
	}

	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	// doDSQuery is the chokepoint where #1182 applies the Loki enforced matchers.
	resp, err := doDSQuery(ctx, client, baseURL, dsQueryPayload(from, to, queries...))
	if err != nil {
		return nil, err
	}

	var out QueryDatasourceResult
	out.Request.From, out.Request.To, out.Request.Queries = args.From, args.To, queries
	out.Range.From, out.Range.To = from.UnixMilli(), to.UnixMilli()
	out.CapturedAt = time.Now().UnixMilli()
	out.Type = args.Type
	out.Results = resp.Responses

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource result: %w", err)
	}
	result := mcpgrafana.NewToolResultText(summarizeFrames(resp))
	if len(body) <= maxDatasourceViewBytes {
		result.StructuredContent = json.RawMessage(body)
	}
	return result, nil
}

// summarizeFrames is the text block: shape only, no rows. render_grafana_app
// replaces it with one line anyway.
func summarizeFrames(resp *backend.QueryDataResponse) string {
	var b strings.Builder
	for refID, r := range resp.Responses {
		if r.Error != nil {
			fmt.Fprintf(&b, "%s: error: %s\n", refID, r.Error)
			continue
		}
		fmt.Fprintf(&b, "%s: %d frames\n", refID, len(r.Frames))
	}
	return b.String()
}

// QueryDatasourceSource is the App source. Do not Register it.
var QueryDatasourceSource = mcpgrafana.MustTool(
	"query_datasource",
	"Query any datasource through Grafana /api/ds/query.",
	queryDatasourceTool,
	// Same hints as query_sql: raw queries pass through unfiltered and can write.
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)
