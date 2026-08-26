package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	mcpgrafana "github.com/grafana/mcp-grafana"
	querierv1 "github.com/grafana/pyroscope/api/gen/proto/go/querier/v1"
	"github.com/grafana/pyroscope/api/gen/proto/go/querier/v1/querierv1connect"
	typesv1 "github.com/grafana/pyroscope/api/gen/proto/go/types/v1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AddPyroscopeTools registers the Pyroscope tools on the MCP server.
// query_pyroscope is registered only when enableQueryTools is true; the
// label and profile-type listing tools stay available either way.
func AddPyroscopeTools(mcp *server.MCPServer, enableQueryTools bool) {
	ListPyroscopeLabelNames.Register(mcp)
	ListPyroscopeLabelValues.Register(mcp)
	ListPyroscopeProfileTypes.Register(mcp)
	if enableQueryTools {
		QueryPyroscope.Register(mcp)
	}
}

const listPyroscopeLabelNamesToolPrompt = `
Lists all available label names (keys) found in profiles within a specified Pyroscope datasource, time range, and
optional label matchers. Label matchers are typically used to qualify a service name ({service_name="foo"}). Returns a
list of unique label strings (e.g., ["app", "env", "pod"]). Label names with double underscores (e.g. __name__) are
internal and rarely useful to users. If the time range is not provided, it defaults to the last hour.
`

var ListPyroscopeLabelNames = mcpgrafana.MustTool(
	"list_pyroscope_label_names",
	listPyroscopeLabelNamesToolPrompt,
	listPyroscopeLabelNames,
	mcp.WithTitleAnnotation("List Pyroscope label names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type ListPyroscopeLabelNamesParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	Matchers      string `json:"matchers,omitempty" jsonschema:"Prometheus style matchers used t0 filter the result set (defaults to: {})"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format or relative time (e.g. 'now-1h') (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format or relative time (e.g. 'now') (defaults to now)"`
}

func listPyroscopeLabelNames(ctx context.Context, args ListPyroscopeLabelNamesParams) ([]string, error) {
	args.Matchers = stringOrDefault(args.Matchers, "{}")

	start, err := parseStartTime(args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := parseEndTime(args.EndRFC3339)
	if err != nil {
		return nil, fmt.Errorf("failed to parse end timestamp %q: %w", args.EndRFC3339, err)
	}

	start, end, err = validateTimeRange(start, end)
	if err != nil {
		return nil, err
	}

	client, err := newPyroscopeClient(ctx, args.DataSourceUID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Pyroscope client: %w", err)
	}

	req := &typesv1.LabelNamesRequest{
		Matchers: []string{args.Matchers},
		Start:    start.UnixMilli(),
		End:      end.UnixMilli(),
	}
	res, err := client.LabelNames(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fmt.Errorf("failed to call Pyroscope API: %w", err)
	}

	return res.Msg.Names, nil
}

const listPyroscopeLabelValuesToolPrompt = `
Lists all available label values for a particular label name found in profiles within a specified Pyroscope datasource,
time range, and optional label matchers. Label matchers are typically used to qualify a service name ({service_name="foo"}).
Returns a list of unique label strings (e.g. for label name "env": ["dev", "staging", "prod"]). If the time range
is not provided, it defaults to the last hour.
`

var ListPyroscopeLabelValues = mcpgrafana.MustTool(
	"list_pyroscope_label_values",
	listPyroscopeLabelValuesToolPrompt,
	listPyroscopeLabelValues,
	mcp.WithTitleAnnotation("List Pyroscope label values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type ListPyroscopeLabelValuesParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	Name          string `json:"name" jsonschema:"required,description=A label name"`
	Matchers      string `json:"matchers,omitempty" jsonschema:"description=Optionally\\, Prometheus style matchers used to filter the result set (defaults to: {})"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format or relative time (e.g. 'now-1h') (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format or relative time (e.g. 'now') (defaults to now)"`
}

func listPyroscopeLabelValues(ctx context.Context, args ListPyroscopeLabelValuesParams) ([]string, error) {
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	args.Matchers = stringOrDefault(args.Matchers, "{}")

	start, err := parseStartTime(args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := parseEndTime(args.EndRFC3339)
	if err != nil {
		return nil, fmt.Errorf("failed to parse end timestamp %q: %w", args.EndRFC3339, err)
	}

	start, end, err = validateTimeRange(start, end)
	if err != nil {
		return nil, err
	}

	client, err := newPyroscopeClient(ctx, args.DataSourceUID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Pyroscope client: %w", err)
	}

	req := &typesv1.LabelValuesRequest{
		Name:     args.Name,
		Matchers: []string{args.Matchers},
		Start:    start.UnixMilli(),
		End:      end.UnixMilli(),
	}
	res, err := client.LabelValues(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fmt.Errorf("failed to call Pyroscope API: %w", err)
	}

	return res.Msg.Names, nil
}

const listPyroscopeProfileTypesToolPrompt = `
Lists all available profile types available in a specified Pyroscope datasource and time range. Returns a list of all
available profile types (example profile type: "process_cpu:cpu:nanoseconds:cpu:nanoseconds"). A profile type has the
following structure: <name>:<sample type>:<sample unit>:<period type>:<period unit>. Not all profile types are available
for every service. If the time range is not provided, it defaults to the last hour.
`

var ListPyroscopeProfileTypes = mcpgrafana.MustTool(
	"list_pyroscope_profile_types",
	listPyroscopeProfileTypesToolPrompt,
	listPyroscopeProfileTypes,
	mcp.WithTitleAnnotation("List Pyroscope profile types"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type ListPyroscopeProfileTypesParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format or relative time (e.g. 'now-1h') (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format or relative time (e.g. 'now') (defaults to now)"`
}

func listPyroscopeProfileTypes(ctx context.Context, args ListPyroscopeProfileTypesParams) ([]string, error) {
	start, err := parseStartTime(args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := parseEndTime(args.EndRFC3339)
	if err != nil {
		return nil, fmt.Errorf("failed to parse end timestamp %q: %w", args.EndRFC3339, err)
	}

	start, end, err = validateTimeRange(start, end)
	if err != nil {
		return nil, err
	}

	client, err := newPyroscopeClient(ctx, args.DataSourceUID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Pyroscope client: %w", err)
	}

	req := &querierv1.ProfileTypesRequest{
		Start: start.UnixMilli(),
		End:   end.UnixMilli(),
	}
	res, err := client.ProfileTypes(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fmt.Errorf("failed to call Pyroscope API: %w", err)
	}

	profileTypes := make([]string, len(res.Msg.ProfileTypes))
	for i, typ := range res.Msg.ProfileTypes {
		profileTypes[i] = fmt.Sprintf("%s:%s:%s:%s:%s", typ.Name, typ.SampleType, typ.SampleUnit, typ.PeriodType, typ.PeriodUnit)
	}
	return profileTypes, nil
}

func newPyroscopeClient(ctx context.Context, uid string) (*pyroscopeClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	_, err = getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base url: %w", err)
	}
	base = base.JoinPath("api", "datasources", "proxy", "uid", uid)

	querierClient := querierv1connect.NewQuerierServiceClient(httpClient, base.String())

	client := &pyroscopeClient{
		QuerierServiceClient: querierClient,
		http:                 httpClient,
		base:                 base,
	}
	return client, nil
}

type renderRequest struct {
	ProfileType string
	Matcher     string
	Start       time.Time
	End         time.Time
	Format      string
	MaxNodes    int
}

type pyroscopeClient struct {
	querierv1connect.QuerierServiceClient
	http *http.Client
	base *url.URL
}

// Calls the /render endpoint for Pyroscope. This returns a rendered flame graph
// (typically in Flamebearer or DOT formats).
func (c *pyroscopeClient) Render(ctx context.Context, args *renderRequest) (string, error) {
	params := url.Values{}
	params.Add("query", fmt.Sprintf("%s%s", args.ProfileType, args.Matcher))
	params.Add("from", fmt.Sprintf("%d", args.Start.UnixMilli()))
	params.Add("until", fmt.Sprintf("%d", args.End.UnixMilli()))
	params.Add("format", args.Format)
	params.Add("max-nodes", fmt.Sprintf("%d", args.MaxNodes))

	res, err := c.get(ctx, "/pyroscope/render", params)
	if err != nil {
		return "", err
	}

	return string(res), nil
}

func (c *pyroscopeClient) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := c.base.JoinPath(path)

	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		_ = res.Body.Close() //nolint:errcheck
	}()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, err := io.ReadAll(io.LimitReader(res.Body, 1024))
		if err != nil {
			return nil, fmt.Errorf("pyroscope API failed with status code %d", res.StatusCode)
		}
		return nil, fmt.Errorf("pyroscope API failed with status code %d: %s", res.StatusCode, string(body))
	}

	body, err := readResponseBody(res.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("pyroscope API returned an empty response")
	}

	if strings.Contains(string(body), "Showing nodes accounting for 0, 0% of 0 total") {
		return nil, fmt.Errorf("pyroscope API returned a empty profile")
	}
	return body, nil
}

func intOrDefault(n int, def int) int {
	if n == 0 {
		return def
	}
	return n
}

func stringOrDefault(s string, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func validateTimeRange(start time.Time, end time.Time) (time.Time, time.Time, error) {
	if end.IsZero() {
		end = time.Now()
	}

	if start.IsZero() {
		start = end.Add(-1 * time.Hour)
	}

	if start.After(end) || start.Equal(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start timestamp %q must be strictly before end timestamp %q", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	return start, end, nil
}

var cleanupRegex = regexp.MustCompile(`(?m)(fontsize=\d+ )|(id="node\d+" )|(labeltooltip=".*?\)" )|(tooltip=".*?\)" )|(N\d+ -> N\d+).*|(shape=box )|(fillcolor="#\w{6}")|(color="#\w{6}" )`)

func cleanupDotProfile(profile string) string {
	return cleanupRegex.ReplaceAllStringFunc(profile, func(match string) string {
		// Preserve edge labels (e.g., "N1 -> N2")
		if m := regexp.MustCompile(`^N\d+ -> N\d+`).FindString(match); m != "" {
			return m
		}
		return ""
	})
}

var matchersRegex = regexp.MustCompile(`^\{.*\}$`)

// rawSeries is the JSON structure returned for a single time-series.
type rawSeries struct {
	Labels map[string]string `json:"labels"`
	Points [][2]float64      `json:"points"` // [[timestamp_ms, value], ...]
}

// seriesResponse is the structured metrics response embedded in the query_pyroscope result.
type seriesResponse struct {
	Series    []rawSeries       `json:"series"`
	TimeRange map[string]string `json:"time_range"`
	StepSecs  float64           `json:"step_seconds"`
}

func buildSeriesResponse(series []*typesv1.Series, start, end time.Time, step float64) *seriesResponse {
	raw := make([]rawSeries, 0, len(series))
	for _, s := range series {
		labels := make(map[string]string, len(s.Labels))
		for _, lp := range s.Labels {
			labels[lp.Name] = lp.Value
		}

		points := make([][2]float64, 0, len(s.Points))
		for _, p := range s.Points {
			points = append(points, [2]float64{float64(p.Timestamp), p.Value})
		}

		if len(points) == 0 {
			continue
		}

		raw = append(raw, rawSeries{
			Labels: labels,
			Points: points,
		})
	}

	return &seriesResponse{
		Series:    raw,
		TimeRange: map[string]string{"from": start.Format(time.RFC3339), "to": end.Format(time.RFC3339)},
		StepSecs:  step,
	}
}

// ---------------------------------------------------------------------------
// per-function profile table
// ---------------------------------------------------------------------------

// Pyroscope truncates large profiles server-side by collapsing the tail of the
// call tree into a synthetic frame with this name (truncatedNodeName in
// pyroscope's pkg/model/tree.go).
const truncatedFunctionName = "other"

type functionStat struct {
	name string
	flat int64
	cum  int64
}

func (c *pyroscopeClient) ProfileTable(ctx context.Context, profileType, matchers string, start, end time.Time, maxRows int) (string, error) {
	res, err := c.SelectMergeStacktraces(ctx, connect.NewRequest(&querierv1.SelectMergeStacktracesRequest{
		ProfileTypeID: profileType,
		LabelSelector: matchers,
		Start:         start.UnixMilli(),
		End:           end.UnixMilli(),
	}))
	if err != nil {
		return "", err
	}
	return buildProfileTable(res.Msg.Flamegraph, sampleUnitFromProfileType(profileType), maxRows)
}

// The profile type ID has the form <name>:<sample type>:<sample unit>:<period type>:<period unit>.
func sampleUnitFromProfileType(profileType string) string {
	parts := strings.Split(profileType, ":")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// buildProfileTable renders a merged flame graph as a per-function text table
// in the style of `pprof -top`: flat (self) and cumulative values per
// fully-qualified function name, ranked by flat.
//
// Flame graph levels encode nodes as [xOffset, total, self, nameIndex] quads
// with delta-encoded offsets (see NewFlameGraph in pyroscope's pkg/model).
// Level 0 is the synthetic root ("total"); sub-threshold subtrees are
// collapsed server-side into explicit "other" nodes.
func buildProfileTable(fg *querierv1.FlameGraph, unit string, maxRows int) (string, error) {
	if fg == nil || fg.Total == 0 || len(fg.Levels) == 0 {
		return "", fmt.Errorf("pyroscope API returned a empty profile")
	}
	total := fg.Total

	type fgNode struct {
		x      int64
		total  int64
		name   int64
		parent int
	}

	flat := make([]int64, len(fg.Names))
	cum := make([]int64, len(fg.Names))
	var truncated int64

	levels := make([][]fgNode, len(fg.Levels))
	for li, level := range fg.Levels {
		vals := level.Values
		nodes := make([]fgNode, 0, len(vals)/4)
		var offset int64
		parentIdx := 0
		for i := 0; i+3 < len(vals); i += 4 {
			x := vals[i] + offset
			offset = x + vals[i+1]
			n := fgNode{x: x, total: vals[i+1], name: vals[i+3]}
			self := vals[i+2]
			if li > 0 && len(levels[li-1]) > 0 {
				parents := levels[li-1]
				for parentIdx < len(parents)-1 && parents[parentIdx].x+parents[parentIdx].total <= n.x {
					parentIdx++
				}
				n.parent = parentIdx

				if int(n.name) < len(fg.Names) && fg.Names[n.name] == truncatedFunctionName {
					truncated += self
				} else {
					flat[n.name] += self
					// Count total only at the outermost occurrence of the
					// function so recursion isn't double-counted.
					recursive := false
					for pl, p := li-1, n.parent; pl > 0; pl-- {
						if levels[pl][p].name == n.name {
							recursive = true
							break
						}
						p = levels[pl][p].parent
					}
					if !recursive {
						cum[n.name] += n.total
					}
				}
			}
			nodes = append(nodes, n)
		}
		levels[li] = nodes
	}

	list := make([]*functionStat, 0, len(fg.Names))
	for i, name := range fg.Names {
		if i == 0 || (flat[i] == 0 && cum[i] == 0) {
			continue
		}
		list = append(list, &functionStat{name: name, flat: flat[i], cum: cum[i]})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].flat != list[j].flat {
			return list[i].flat > list[j].flat
		}
		if list[i].cum != list[j].cum {
			return list[i].cum > list[j].cum
		}
		return list[i].name < list[j].name
	})
	shown := list
	if len(shown) > maxRows {
		shown = shown[:maxRows]
	}
	var shownFlat int64
	for _, s := range shown {
		shownFlat += s.flat
	}

	pct := func(v int64) float64 {
		return float64(v) * 100 / float64(total)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Total: %s\n", formatSampleValue(total, unit))
	fmt.Fprintf(&b, "Showing top %d out of %d functions by flat (self) value, accounting for %.2f%% of the total\n",
		len(shown), len(list), pct(shownFlat))
	if truncated > 0 {
		fmt.Fprintf(&b, "Note: %.2f%% of the total was collapsed into an %q bucket by server-side tree truncation and cannot be attributed to functions; values below may be understated\n",
			pct(truncated), truncatedFunctionName)
	}
	fmt.Fprintf(&b, "%12s %6s %6s %12s %6s  %s\n", "flat", "flat%", "sum%", "cum", "cum%", "function")
	var sumFlat int64
	for _, s := range shown {
		sumFlat += s.flat
		fmt.Fprintf(&b, "%12s %5.2f%% %5.2f%% %12s %5.2f%%  %s\n",
			formatSampleValue(s.flat, unit), pct(s.flat), pct(sumFlat),
			formatSampleValue(s.cum, unit), pct(s.cum), s.name)
	}
	return b.String(), nil
}

func formatSampleValue(v int64, unit string) string {
	f := float64(v)
	switch unit {
	case "nanoseconds":
		switch d := time.Duration(v); {
		case d >= time.Second:
			return fmt.Sprintf("%.2fs", d.Seconds())
		case d >= time.Millisecond:
			return fmt.Sprintf("%.2fms", f/1e6)
		case d >= time.Microsecond:
			return fmt.Sprintf("%.2fus", f/1e3)
		default:
			return fmt.Sprintf("%dns", v)
		}
	case "bytes":
		switch {
		case f >= 1<<30:
			return fmt.Sprintf("%.2fGB", f/(1<<30))
		case f >= 1<<20:
			return fmt.Sprintf("%.2fMB", f/(1<<20))
		case f >= 1<<10:
			return fmt.Sprintf("%.2fkB", f/(1<<10))
		default:
			return fmt.Sprintf("%dB", v)
		}
	default:
		return strconv.FormatInt(v, 10)
	}
}

// ---------------------------------------------------------------------------
// query_pyroscope — unified tool: profile + metrics + both
// ---------------------------------------------------------------------------

const queryPyroscopeToolPrompt = `
Unified Pyroscope query tool for fetching profiles or metrics from Pyroscope. Profile data shows WHICH functions consume resources; metrics data
shows WHEN consumption spiked. Use query_type="both" for complete analysis in one call.

query_type options (extends Grafana's PyroscopeQueryType):
- "profile": returns profile data (shape controlled by format)
- "metrics": returns time-series data points
- "both" (default): returns both profile and metrics in one response

format options (shape of the profile data):
- "table" (default): per-function table with flat (self) and cumulative values, ranked by flat
- "dot": call graph in Graphviz DOT format; nodes are per source line, so one function may span several nodes
`

var QueryPyroscope = mcpgrafana.MustTool(
	"query_pyroscope",
	queryPyroscopeToolPrompt,
	queryPyroscope,
	mcp.WithTitleAnnotation("Query Pyroscope"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type QueryPyroscopeParams struct {
	DataSourceUID string   `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	ProfileType   string   `json:"profile_type" jsonschema:"required,description=The profile type\\, use list_pyroscope_profile_types to discover available types"`
	QueryType     string   `json:"query_type,omitempty" jsonschema:"description=Query type: \"profile\" (flamegraph)\\, \"metrics\" (time-series)\\, or \"both\" (default). Use \"both\" for complete analysis"`
	Format        string   `json:"format,omitempty" jsonschema:"description=Profile output format: \"table\" (default) for a per-function flat/cum table\\, or \"dot\" for a call graph in Graphviz DOT format"`
	Matchers      string   `json:"matchers,omitempty" jsonschema:"description=Prometheus style matchers (defaults to: {})"`
	GroupBy       []string `json:"group_by,omitempty" jsonschema:"description=Labels to group metrics series by"`
	Step          float64  `json:"step,omitempty" jsonschema:"description=Seconds between metrics data points (default: auto)"`
	MaxNodeDepth  int      `json:"max_node_depth,omitempty" jsonschema:"description=Max functions in the profile table or nodes in the call graph; it is a count\\, not a depth (default: 100)"`
	StartRFC3339  string   `json:"start_rfc_3339,omitempty" jsonschema:"description=Start time in RFC3339 or relative time (e.g. 'now-1h') (defaults to 1 hour ago)"`
	EndRFC3339    string   `json:"end_rfc_3339,omitempty" jsonschema:"description=End time in RFC3339 or relative time (e.g. 'now') (defaults to now)"`
}

func queryPyroscope(ctx context.Context, args QueryPyroscopeParams) (string, error) {
	queryType := strings.ToLower(strings.TrimSpace(args.QueryType))
	if queryType == "" {
		queryType = "both"
	}
	if queryType != "profile" && queryType != "metrics" && queryType != "both" {
		return "", fmt.Errorf("invalid query_type %q: must be \"profile\", \"metrics\", or \"both\"", args.QueryType)
	}

	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = "table"
	}
	if format != "table" && format != "dot" {
		return "", fmt.Errorf("invalid format %q: must be \"table\" or \"dot\"", args.Format)
	}

	// Common setup
	matchers := stringOrDefault(args.Matchers, "{}")
	if !matchersRegex.MatchString(matchers) {
		matchers = fmt.Sprintf("{%s}", matchers)
	}

	start, err := parseStartTime(args.StartRFC3339)
	if err != nil {
		return "", fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := parseEndTime(args.EndRFC3339)
	if err != nil {
		return "", fmt.Errorf("failed to parse end timestamp %q: %w", args.EndRFC3339, err)
	}

	start, end, err = validateTimeRange(start, end)
	if err != nil {
		return "", err
	}

	client, err := newPyroscopeClient(ctx, args.DataSourceUID)
	if err != nil {
		return "", fmt.Errorf("failed to create Pyroscope client: %w", err)
	}

	wantProfile := queryType == "profile" || queryType == "both"
	wantMetrics := queryType == "metrics" || queryType == "both"

	result := make(map[string]any)
	result["query_type"] = queryType

	if wantProfile {
		maxNodes := intOrDefault(args.MaxNodeDepth, 100)
		var profile string
		var profileErr error
		if format == "dot" {
			var res string
			res, profileErr = client.Render(ctx, &renderRequest{
				ProfileType: args.ProfileType,
				Matcher:     matchers,
				Start:       start,
				End:         end,
				Format:      "dot",
				MaxNodes:    maxNodes,
			})
			if profileErr == nil {
				profile = cleanupDotProfile(res)
			}
		} else {
			profile, profileErr = client.ProfileTable(ctx, args.ProfileType, matchers, start, end, maxNodes)
		}
		if profileErr != nil {
			// Single-type query: propagate error so MCP framework sets IsError=true.
			// "both" mode: embed error for partial results.
			if queryType == "profile" {
				return "", fmt.Errorf("failed to fetch profile: %w", profileErr)
			}
			result["profile"] = map[string]string{"error": profileErr.Error()}
		} else {
			result["profile"] = profile
		}
	}

	if wantMetrics {
		step := args.Step
		if step <= 0 {
			step = math.Max(end.Sub(start).Seconds()/50.0, 15.0)
		}

		seriesRes, metricsErr := client.SelectSeries(ctx, connect.NewRequest(&querierv1.SelectSeriesRequest{
			ProfileTypeID: args.ProfileType,
			LabelSelector: matchers,
			Start:         start.UnixMilli(),
			End:           end.UnixMilli(),
			GroupBy:       args.GroupBy,
			Step:          step,
		}))
		if metricsErr != nil {
			if queryType == "metrics" {
				return "", fmt.Errorf("failed to fetch metrics: %w", metricsErr)
			}
			result["metrics"] = map[string]string{"error": metricsErr.Error()}
		} else {
			result["metrics"] = buildSeriesResponse(seriesRes.Msg.Series, start, end, step)
		}
	}

	// If both queries were attempted and both failed, propagate error.
	_, profileFailed := result["profile"].(map[string]string)
	_, metricsFailed := result["metrics"].(map[string]string)
	if queryType == "both" && profileFailed && metricsFailed {
		return "", fmt.Errorf("both queries failed — profile: %s; metrics: %s",
			result["profile"].(map[string]string)["error"],
			result["metrics"].(map[string]string)["error"])
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}
	return string(out), nil
}
