package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// GraphiteDatasourceType is the type identifier for Graphite datasources
	GraphiteDatasourceType = "graphite"
)

// GraphiteClient handles queries to a Graphite datasource via Grafana proxy
type GraphiteClient struct {
	httpClient *http.Client
	baseURL    string
	uid        string
	// legacy is set on Grafana before 9.0, whose /api/ds/query does not
	// accept datasource UIDs; render queries there stay on the proxy.
	legacy bool
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
	grafanaURL := cfg.URL
	resourcesBase, proxyBase := datasourceProxyPaths(uid)
	primaryBase, fallbackBase := proxyBase, resourcesBase
	legacyMode := false
	if numericBase, uidBase, ok := fallbackProxyBases(ctx, uid); ok {
		// Legacy-compatible routing — see newPrometheusBackend for the full
		// rationale: route through the numeric-id proxy path directly, keeping
		// the uid-based proxy route as the transport-level fallback.
		primaryBase, fallbackBase = numericBase, uidBase
		legacyMode = true
	}
	baseURL := grafanaURL + primaryBase

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	// Wrap with fallback transport: try the primary base first, fall back to
	// the alternate for compatibility with different Grafana deployments (see
	// fallback_transport.go for the per-mode retry rules).
	var rt http.RoundTripper
	if legacyMode {
		rt = newLegacyDatasourceFallbackTransport(transport, primaryBase, fallbackBase)
	} else {
		rt = newDatasourceFallbackTransport(transport, primaryBase, fallbackBase)
	}

	client := &http.Client{Transport: rt}
	dsUID := ds.UID
	if dsUID == "" {
		dsUID = uid
	}
	return &GraphiteClient{httpClient: client, baseURL: baseURL, uid: dsUID, legacy: legacyMode}, nil
}

// doGet performs a GET request to the Graphite API via the Grafana proxy
func (c *GraphiteClient) doGet(ctx context.Context, path string, params url.Values) ([]byte, error) {
	fullURL := c.baseURL + path
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

	data, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
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
	Target     string              `json:"target"`
	Tags       map[string]string   `json:"tags,omitempty"`
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

var graphiteRelativeTime = regexp.MustCompile(`^-(\d+)([a-z]+)$`)

// graphiteUnits maps Graphite relative-time unit prefixes to gtime units, in
// the order Graphite matches them ("min" before "mon", both before "m").
var graphiteUnits = [][2]string{{"s", "s"}, {"min", "m"}, {"h", "h"}, {"d", "d"}, {"w", "w"}, {"mon", "M"}, {"y", "y"}}

// parseGraphiteTime resolves a from/until value to an absolute time. It
// accepts Graphite relative times ("-1h", "-5min", "-2weeks"), Unix seconds,
// and everything parseStartTime/parseEndTime accept (RFC3339, "now",
// "now-1h"). Empty returns the default.
func parseGraphiteTime(s, def string, parse func(string) (time.Time, error)) (time.Time, error) {
	if s == "" {
		s = def
	}
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(secs, 0), nil
	}
	if m := graphiteRelativeTime.FindStringSubmatch(s); m != nil {
		for _, u := range graphiteUnits {
			if strings.HasPrefix(m[2], u[0]) {
				s = "now-" + m[1] + u[1]
				break
			}
		}
	}
	return parse(s)
}

// graphiteRender evaluates target over [from, until] and returns one series
// per resolved metric. It runs through Grafana's /api/ds/query endpoint so
// the query shows up in the query service's log, except on Grafana before
// 9.0, where it calls the render API through the datasource proxy.
//
// maxDataPoints defaults to one point per second of the range so Graphite
// never consolidates; Grafana otherwise asks for 100 points.
func graphiteRender(ctx context.Context, uid, target, fromStr, untilStr string, maxDataPoints int) ([]*GraphiteSeries, time.Time, time.Time, error) {
	from, err := parseGraphiteTime(fromStr, "-1h", parseStartTime)
	if err != nil {
		return nil, from, time.Time{}, fmt.Errorf("parsing from: %w", err)
	}
	until, err := parseGraphiteTime(untilStr, "now", parseEndTime)
	if err != nil {
		return nil, from, until, fmt.Errorf("parsing until: %w", err)
	}
	if maxDataPoints <= 0 {
		maxDataPoints = max(int(until.Sub(from)/time.Second), 1)
	}

	client, err := newGraphiteClient(ctx, uid)
	if err != nil {
		return nil, from, until, fmt.Errorf("creating graphite client: %w", err)
	}

	if client.legacy {
		params := url.Values{}
		params.Set("target", target)
		params.Set("from", strconv.FormatInt(from.Unix(), 10))
		params.Set("until", strconv.FormatInt(until.Unix(), 10))
		params.Set("format", "json")
		params.Set("maxDataPoints", strconv.Itoa(maxDataPoints))
		data, err := client.doGet(ctx, "/render", params)
		if err != nil {
			return nil, from, until, fmt.Errorf("querying graphite render API: %w", err)
		}
		var rawSeries []graphiteRawSeries
		if err := json.Unmarshal(data, &rawSeries); err != nil {
			return nil, from, until, fmt.Errorf("parsing graphite render response: %w", err)
		}
		series := make([]*GraphiteSeries, 0, len(rawSeries))
		for _, rs := range rawSeries {
			series = append(series, &GraphiteSeries{Target: rs.Target, Tags: rs.Tags, Datapoints: parseGraphiteDatapoints(rs.Datapoints)})
		}
		return series, from, until, nil
	}

	httpClient, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, from, until, err
	}
	query := map[string]interface{}{
		"refId":         "A",
		"datasource":    map[string]string{"uid": client.uid, "type": GraphiteDatasourceType},
		"target":        target,
		"maxDataPoints": maxDataPoints,
	}
	resp, err := doDSQuery(ctx, httpClient, baseURL, dsQueryPayload(from, until, query))
	if err != nil {
		return nil, from, until, fmt.Errorf("querying graphite: %w", err)
	}
	series, err := graphiteFramesToSeries(resp)
	return series, from, until, err
}

// graphiteFramesToSeries converts the frames Grafana's Graphite datasource
// returns (one per series: a time field and a nullable value field whose
// labels are the series tags and whose display name is the target) back
// into GraphiteSeries.
func graphiteFramesToSeries(resp *backend.QueryDataResponse) ([]*GraphiteSeries, error) {
	series := []*GraphiteSeries{}
	r, ok := resp.Responses["A"]
	if !ok {
		return series, nil
	}
	if r.Error != nil {
		return nil, fmt.Errorf("querying graphite: %s", r.Error.Error())
	}
	for _, frame := range r.Frames {
		timeIdx, valueIdx := findTimeAndValueFields(frame.Fields)
		if timeIdx == -1 || valueIdx == -1 {
			continue
		}
		field := frame.Fields[valueIdx]
		s := &GraphiteSeries{Target: field.Name, Datapoints: make([]GraphiteDatapoint, 0, frame.Rows())}
		if field.Config != nil && field.Config.DisplayNameFromDS != "" {
			s.Target = field.Config.DisplayNameFromDS
		}
		if len(field.Labels) > 0 {
			s.Tags = map[string]string(field.Labels)
		}
		for i := 0; i < frame.Rows(); i++ {
			ms, ok := toMillis(frame.At(timeIdx, i))
			if !ok {
				continue
			}
			dp := GraphiteDatapoint{Timestamp: ms / 1000}
			if v, ok := toFloat(frame.At(valueIdx, i)); ok {
				dp.Value = &v
			}
			s.Datapoints = append(s.Datapoints, dp)
		}
		series = append(series, s)
	}
	return series, nil
}

// QueryGraphiteParams defines the parameters for querying a Graphite datasource.
type QueryGraphiteParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Graphite datasource to query"`
	Target        string `json:"target" jsonschema:"required,description=The Graphite target expression to evaluate (e.g. 'servers.web*.cpu.load5'\\, 'sumSeries(app.*.requests)'\\, 'seriesByTag(\\'name=cpu.load\\')')"`
	From          string `json:"from,omitempty" jsonschema:"description=Start of the time range. Accepts RFC3339 (e.g. '2024-01-01T00:00:00Z')\\, Unix seconds\\, or relative times (e.g. '-1h'\\, '-24h'\\, 'now-1h'). Defaults to '-1h'."`
	Until         string `json:"until,omitempty" jsonschema:"description=End of the time range. Accepts RFC3339\\, Unix seconds\\, or relative times (e.g. 'now'). Defaults to 'now'."`
	MaxDataPoints int    `json:"maxDataPoints,omitempty" jsonschema:"description=Optional maximum number of data points per series. Graphite consolidates data when the requested range exceeds this value. Defaults to returning the stored resolution."`
}

// QueryGraphiteResult wraps a Graphite render query result with optional hints.
type QueryGraphiteResult struct {
	Series []*GraphiteSeries `json:"series"`
	Hints  *EmptyResultHints `json:"hints,omitempty"`
}

func queryGraphite(ctx context.Context, args QueryGraphiteParams) (*QueryGraphiteResult, error) {
	series, from, until, err := graphiteRender(ctx, args.DatasourceUID, args.Target, args.From, args.Until, args.MaxDataPoints)
	if err != nil {
		return nil, err
	}

	result := &QueryGraphiteResult{Series: series}
	if len(series) == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: GraphiteDatasourceType,
			Query:          args.Target,
			StartTime:      from,
			EndTime:        until,
		})
	}
	return result, nil
}

// QueryGraphite is the MCP tool for querying a Graphite datasource.
var QueryGraphite = mcpgrafana.MustTool(
	"query_graphite",
	"Executes a Graphite render API query against a Graphite datasource and returns matching metric series with their datapoints. Supports the full Graphite target expression language including wildcard patterns (e.g. 'servers.web*.cpu.load5'), aggregation functions (e.g. 'sumSeries(app.*.requests)'), and tag-based queries (e.g. 'seriesByTag(\\'name=cpu.load\\')'). Datapoints with no recorded value are returned with a null value field. Time range defaults to the last hour if not specified.",
	queryGraphite,
	mcpgrafana.WithTitleAnnotation("Query Graphite metrics"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
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
	"Discover available metric paths in a Graphite datasource by browsing the metric tree. Returns nodes matching the query pattern, each indicating whether it is a leaf metric (has data) or an expandable branch (has children). Use '*' as a wildcard at any level to enumerate the tree (e.g. '*' → top-level nodes, 'servers.*' → all second-level nodes under 'servers').",
	listGraphiteMetrics,
	mcpgrafana.WithTitleAnnotation("List Graphite metrics"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
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

	var raw []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing graphite tags response: %w", err)
	}
	tags := make([]string, 0, len(raw))
	for _, t := range raw {
		if args.Prefix == "" || strings.HasPrefix(t.Tag, args.Prefix) {
			tags = append(tags, t.Tag)
		}
	}
	return tags, nil
}

// ListGraphiteTags is the MCP tool for listing tag names in a tagged Graphite instance.
var ListGraphiteTags = mcpgrafana.MustTool(
	"list_graphite_tags",
	"List available tag names in a Graphite datasource that uses tag-based metrics. Returns a list of tag name strings (e.g. [\"name\", \"env\", \"region\"]). These tags can be used to build tag-based target expressions (e.g. seriesByTag('name=cpu.load,env=prod')). Optionally filter by a prefix. Requires Graphite to be configured with tag support.",
	listGraphiteTags,
	mcpgrafana.WithTitleAnnotation("List Graphite tags"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// computeSeriesDensity derives data-density statistics from the parsed
// datapoints of a single Graphite series.
func computeSeriesDensity(target string, pts []GraphiteDatapoint) *GraphiteSeriesDensity {
	total := len(pts)

	var stepSec int64
	if total >= 2 {
		if d := pts[1].Timestamp - pts[0].Timestamp; d > 0 {
			stepSec = d
		}
	}

	var nonNull int
	var lastSeenTS int64
	var hasLastSeen bool
	var currentGap, longestGap int

	for _, dp := range pts {
		if dp.Value != nil {
			nonNull++
			lastSeenTS = dp.Timestamp
			hasLastSeen = true
			currentGap = 0
		} else {
			currentGap++
			if currentGap > longestGap {
				longestGap = currentGap
			}
		}
	}

	var fillRatio float64
	if total > 0 {
		fillRatio = float64(nonNull) / float64(total)
	}

	var lastSeen *int64
	if hasLastSeen {
		cp := lastSeenTS
		lastSeen = &cp
	}

	return &GraphiteSeriesDensity{
		Target:            target,
		FillRatio:         fillRatio,
		TotalPoints:       total,
		NonNullPoints:     nonNull,
		LastSeen:          lastSeen,
		LongestGapSec:     int64(longestGap) * stepSec,
		EstimatedInterval: stepSec,
	}
}

// QueryGraphiteDensityParams defines the inputs for the query_graphite_density tool.
type QueryGraphiteDensityParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Graphite datasource to query"`
	Target        string `json:"target" jsonschema:"required,description=Graphite target expression; supports wildcards (e.g. 'obox-cl*.sys.sessions')"`
	From          string `json:"from,omitempty" jsonschema:"description=Start of the time range. Accepts RFC3339\\, Unix seconds\\, or relative times (e.g. '-1h'\\, 'now-1h'). Defaults to '-1h'."`
	Until         string `json:"until,omitempty" jsonschema:"description=End of the time range. Accepts RFC3339\\, Unix seconds\\, or relative times (e.g. 'now'). Defaults to 'now'."`
}

// GraphiteSeriesDensity holds data-density statistics for a single Graphite series.
type GraphiteSeriesDensity struct {
	// Target is the resolved metric path.
	Target string `json:"target"`
	// FillRatio is the fraction of non-null datapoints (0.0–1.0).
	FillRatio float64 `json:"fillRatio"`
	// TotalPoints is the total datapoints returned in the window.
	TotalPoints int `json:"totalPoints"`
	// NonNullPoints is the count of datapoints that carry a value.
	NonNullPoints int `json:"nonNullPoints"`
	// LastSeen is the Unix timestamp of the most recent non-null datapoint,
	// or null if no data was seen in the window.
	LastSeen *int64 `json:"lastSeen"`
	// LongestGapSec is the duration in seconds of the longest consecutive
	// run of null datapoints.
	LongestGapSec int64 `json:"longestGap"`
	// EstimatedInterval is the inferred write interval in seconds derived
	// from the spacing between consecutive datapoints.
	EstimatedInterval int64 `json:"estimatedInterval"`
}

// QueryGraphiteDensityResult is returned by the query_graphite_density tool.
type QueryGraphiteDensityResult struct {
	Series []*GraphiteSeriesDensity `json:"series"`
}

func queryGraphiteDensity(ctx context.Context, args QueryGraphiteDensityParams) (*QueryGraphiteDensityResult, error) {
	series, _, _, err := graphiteRender(ctx, args.DatasourceUID, args.Target, args.From, args.Until, 0)
	if err != nil {
		return nil, err
	}

	result := &QueryGraphiteDensityResult{
		Series: make([]*GraphiteSeriesDensity, 0, len(series)),
	}
	for _, s := range series {
		result.Series = append(result.Series, computeSeriesDensity(s.Target, s.Datapoints))
	}
	return result, nil
}

// QueryGraphiteDensity is the MCP tool for analysing metric data density.
var QueryGraphiteDensity = mcpgrafana.MustTool(
	"query_graphite_density",
	"Analyses metric data density for one or more Graphite series over a time window. "+
		"Returns per-series statistics: fillRatio (fraction of non-null datapoints, 0.0–1.0), "+
		"totalPoints, nonNullPoints, lastSeen (Unix timestamp of most recent non-null value, or null if none), "+
		"longestGap (longest consecutive null run in seconds), and estimatedInterval (inferred write interval in seconds). "+
		"Supports wildcard targets (e.g. 'obox-cl*.sys.sessions') to diagnose stale, sparse, or dead metrics across a cluster. "+
		"A fillRatio of 0 with lastSeen null means the series reported no data in the requested window.",
	queryGraphiteDensity,
	mcpgrafana.WithTitleAnnotation("Query Graphite metric density"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// AddGraphiteTools registers all Graphite tools with the MCP server.
// The query tools are registered only when enableQueryTools is true; the
// metric and tag listing tools stay available either way.
func AddGraphiteTools(s *mcp.Server, enableQueryTools bool) {
	ListGraphiteMetrics.Register(s)
	ListGraphiteTags.Register(s)
	if enableQueryTools {
		QueryGraphite.Register(s)
		QueryGraphiteDensity.Register(s)
	}
}
