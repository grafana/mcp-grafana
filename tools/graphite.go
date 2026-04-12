package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// GraphiteDatasourceType is the type identifier for Graphite datasources
	GraphiteDatasourceType = "graphite"

	graphiteResponseLimitBytes = 1024 * 1024 * 10 // 10MB
)

// GraphiteClient handles queries to a Graphite datasource via Grafana proxy
type GraphiteClient struct {
	httpClient *http.Client
	baseURL    string
}

func newGraphiteClient(ctx context.Context, uid string) (*GraphiteClient, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}
	if ds.Type != GraphiteDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, GraphiteDatasourceType)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	grafanaURL := strings.TrimRight(cfg.URL, "/")
	resourcesBase, proxyBase := datasourceProxyPaths(uid)
	baseURL := grafanaURL + proxyBase

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	// Wrap with fallback transport: try /proxy first, fall back to /resources
	// on 403/500 for compatibility with different managed Grafana deployments.
	var rt http.RoundTripper = mcpgrafana.NewUserAgentTransport(transport)
	rt = newDatasourceFallbackTransport(rt, proxyBase, resourcesBase)

	client := &http.Client{Transport: rt}
	return &GraphiteClient{httpClient: client, baseURL: baseURL}, nil
}

// doGet performs a GET request to the Graphite API via the Grafana proxy
func (c *GraphiteClient) doGet(ctx context.Context, path string, params url.Values) ([]byte, error) {
	fullURL := strings.TrimRight(c.baseURL, "/") + path
	if len(params) > 0 {
		fullURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("graphite API returned status %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, graphiteResponseLimitBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	return data, nil
}

// GraphiteDatapoint is a single metric sample. Value is nil when Graphite
// reports no data for that timestamp (null in the JSON response).
type GraphiteDatapoint struct {
	Value     *float64 `json:"value"`
	Timestamp int64    `json:"timestamp"`
}

// GraphiteSeries is a metric series as returned by the Graphite render API.
type GraphiteSeries struct {
	Target     string             `json:"target"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Datapoints []GraphiteDatapoint `json:"datapoints"`
}

// graphiteRawSeries is the wire format for the Graphite render API response.
// Each datapoint is [value_or_null, unix_timestamp].
type graphiteRawSeries struct {
	Target     string              `json:"target"`
	Tags       map[string]string   `json:"tags,omitempty"`
	Datapoints [][]json.RawMessage `json:"datapoints"`
}

// parseGraphiteDatapoints converts the raw render API datapoints to typed values.
func parseGraphiteDatapoints(raw [][]json.RawMessage) []GraphiteDatapoint {
	pts := make([]GraphiteDatapoint, 0, len(raw))
	for _, pair := range raw {
		if len(pair) < 2 {
			continue
		}
		var ts int64
		if err := json.Unmarshal(pair[1], &ts); err != nil {
			continue
		}
		var val *float64
		if string(pair[0]) != "null" {
			var f float64
			if err := json.Unmarshal(pair[0], &f); err == nil {
				val = &f
			}
		}
		pts = append(pts, GraphiteDatapoint{Value: val, Timestamp: ts})
	}
	return pts
}

// parseGraphiteTime converts a time string to a value Graphite's render API
// accepts for its `from`/`until` parameters.
//
//   - Empty string → returned as-is (caller should supply a default).
//   - Graphite relative formats ("-1h", "-24h", "now", …) → passed through unchanged.
//   - RFC 3339 strings → converted to a Unix timestamp (integer seconds).
func parseGraphiteTime(s string) string {
	if s == "" || s == "now" || strings.HasPrefix(s, "-") {
		return s
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Unknown format — pass through and let Graphite decide.
		return s
	}
	return strconv.FormatInt(t.Unix(), 10)
}

// QueryGraphiteParams defines the parameters for querying a Graphite datasource.
type QueryGraphiteParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Graphite datasource to query"`
	Target        string `json:"target" jsonschema:"required,description=The Graphite target expression to evaluate (e.g. 'servers.web*.cpu.load5'\\, 'sumSeries(app.*.requests)'\\, 'seriesByTag(\\'name=cpu.load\\')')"`
	From          string `json:"from,omitempty" jsonschema:"description=Start of the time range. Accepts RFC3339 (e.g. '2024-01-01T00:00:00Z') or Graphite relative times (e.g. '-1h'\\, '-24h'). Defaults to '-1h'."`
	Until         string `json:"until,omitempty" jsonschema:"description=End of the time range. Accepts RFC3339 or Graphite relative times (e.g. 'now'). Defaults to 'now'."`
	MaxDataPoints int    `json:"maxDataPoints,omitempty" jsonschema:"description=Optional maximum number of data points per series. Graphite consolidates data when the requested range exceeds this value."`
}

// QueryGraphiteResult wraps a Graphite render query result with optional hints.
type QueryGraphiteResult struct {
	Series []*GraphiteSeries `json:"series"`
	Hints  *EmptyResultHints `json:"hints,omitempty"`
}

func queryGraphite(ctx context.Context, args QueryGraphiteParams) (*QueryGraphiteResult, error) {
	client, err := newGraphiteClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating graphite client: %w", err)
	}

	from := args.From
	if from == "" {
		from = "-1h"
	}
	until := args.Until
	if until == "" {
		until = "now"
	}

	params := url.Values{}
	params.Set("target", args.Target)
	params.Set("from", parseGraphiteTime(from))
	params.Set("until", parseGraphiteTime(until))
	params.Set("format", "json")
	if args.MaxDataPoints > 0 {
		params.Set("maxDataPoints", strconv.Itoa(args.MaxDataPoints))
	}

	data, err := client.doGet(ctx, "/render", params)
	if err != nil {
		return nil, fmt.Errorf("querying graphite render API: %w", err)
	}

	var rawSeries []graphiteRawSeries
	if err := json.Unmarshal(data, &rawSeries); err != nil {
		return nil, fmt.Errorf("parsing graphite render response: %w", err)
	}

	series := make([]*GraphiteSeries, 0, len(rawSeries))
	for _, rs := range rawSeries {
		series = append(series, &GraphiteSeries{
			Target:     rs.Target,
			Tags:       rs.Tags,
			Datapoints: parseGraphiteDatapoints(rs.Datapoints),
		})
	}

	result := &QueryGraphiteResult{Series: series}
	if len(series) == 0 {
		var startTime, endTime time.Time
		if t, err := time.Parse(time.RFC3339, args.From); err == nil {
			startTime = t
		}
		if t, err := time.Parse(time.RFC3339, args.Until); err == nil {
			endTime = t
		}
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: GraphiteDatasourceType,
			Query:          args.Target,
			StartTime:      startTime,
			EndTime:        endTime,
		})
	}
	return result, nil
}

// QueryGraphite is the MCP tool for querying a Graphite datasource.
var QueryGraphite = mcpgrafana.MustTool(
	"query_graphite",
	"WORKFLOW: list_graphite_metrics -> query_graphite.\n\nExecutes a Graphite render API query against a Graphite datasource and returns matching metric series with their datapoints. Supports the full Graphite target expression language including wildcard patterns (e.g. 'servers.web*.cpu.load5'), aggregation functions (e.g. 'sumSeries(app.*.requests)'), and tag-based queries (e.g. 'seriesByTag(\\'name=cpu.load\\')'). Datapoints with no recorded value are returned with a null value field. Time range defaults to the last hour if not specified.",
	queryGraphite,
	mcp.WithTitleAnnotation("Query Graphite metrics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// GraphiteMetricNode is a node in the Graphite metric hierarchy as returned
// by the find API.
type GraphiteMetricNode struct {
	// ID is the full dotted path of this node (e.g. "servers.web01.cpu").
	ID string `json:"id"`
	// Text is the last segment of the path (e.g. "cpu").
	Text string `json:"text"`
	// Leaf indicates whether this node is an actual metric (true) or a
	// branch that can be expanded further (false).
	Leaf bool `json:"leaf"`
	// Expandable indicates whether this node has children.
	Expandable bool `json:"expandable"`
}

// graphiteRawMetricNode is the wire format returned by Graphite's find API;
// leaf and expandable are encoded as integers (0 or 1).
type graphiteRawMetricNode struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Leaf       int    `json:"leaf"`
	Expandable int    `json:"expandable"`
}

// ListGraphiteMetricsParams defines the parameters for the list_graphite_metrics tool.
type ListGraphiteMetricsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Graphite datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=Metric path pattern to search. Use '*' as a wildcard at any level (e.g. '*' lists top-level nodes\\, 'servers.*' lists all servers\\, 'servers.web01.*' lists all metrics under web01)."`
}

func listGraphiteMetrics(ctx context.Context, args ListGraphiteMetricsParams) ([]GraphiteMetricNode, error) {
	client, err := newGraphiteClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating graphite client: %w", err)
	}

	query := args.Query
	if query == "" {
		query = "*"
	}

	params := url.Values{}
	params.Set("query", query)

	data, err := client.doGet(ctx, "/metrics/find", params)
	if err != nil {
		return nil, fmt.Errorf("listing graphite metrics: %w", err)
	}

	var rawNodes []graphiteRawMetricNode
	if err := json.Unmarshal(data, &rawNodes); err != nil {
		return nil, fmt.Errorf("parsing graphite metrics response: %w", err)
	}

	nodes := make([]GraphiteMetricNode, 0, len(rawNodes))
	for _, rn := range rawNodes {
		nodes = append(nodes, GraphiteMetricNode{
			ID:         rn.ID,
			Text:       rn.Text,
			Leaf:       rn.Leaf == 1,
			Expandable: rn.Expandable == 1,
		})
	}
	return nodes, nil
}

// ListGraphiteMetrics is the MCP tool for browsing the Graphite metric tree.
var ListGraphiteMetrics = mcpgrafana.MustTool(
	"list_graphite_metrics",
	"Discover available metric paths in a Graphite datasource by browsing the metric tree. Returns nodes matching the query pattern\\, each indicating whether it is a leaf metric (has data) or an expandable branch (has children). Use '*' as a wildcard at any level to enumerate the tree (e.g. '*' → top-level nodes\\, 'servers.*' → all second-level nodes under 'servers'). Drill down progressively to find the full metric path before querying with query_graphite.",
	listGraphiteMetrics,
	mcp.WithTitleAnnotation("List Graphite metrics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListGraphiteTagsParams defines the parameters for the list_graphite_tags tool.
type ListGraphiteTagsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Graphite datasource to query"`
	Prefix        string `json:"prefix,omitempty" jsonschema:"description=Optional prefix to filter tag names (e.g. 'env' returns tags whose name starts with 'env')."`
}

func listGraphiteTags(ctx context.Context, args ListGraphiteTagsParams) ([]string, error) {
	client, err := newGraphiteClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating graphite client: %w", err)
	}

	params := url.Values{}
	if args.Prefix != "" {
		params.Set("tagPrefix", args.Prefix)
	}

	data, err := client.doGet(ctx, "/tags", params)
	if err != nil {
		return nil, fmt.Errorf("listing graphite tags: %w", err)
	}

	var tags []string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil, fmt.Errorf("parsing graphite tags response: %w", err)
	}
	return tags, nil
}

// ListGraphiteTags is the MCP tool for listing tag names in a tagged Graphite instance.
var ListGraphiteTags = mcpgrafana.MustTool(
	"list_graphite_tags",
	"List available tag names in a Graphite datasource that uses tag-based metrics. Returns a list of tag name strings (e.g. [\"name\"\\, \"env\"\\, \"region\"]). These tags can be used to build tag-based target expressions for query_graphite (e.g. seriesByTag('name=cpu.load\\,env=prod')). Optionally filter by a prefix. Requires Graphite to be configured with tag support.",
	listGraphiteTags,
	mcp.WithTitleAnnotation("List Graphite tags"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddGraphiteTools registers all Graphite tools with the MCP server.
func AddGraphiteTools(mcp *server.MCPServer) {
	QueryGraphite.Register(mcp)
	ListGraphiteMetrics.Register(mcp)
	ListGraphiteTags.Register(mcp)
}