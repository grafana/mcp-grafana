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

func AddPyroscopeTools(mcp *server.MCPServer) {
	ListPyroscopeLabelNames.Register(mcp)
	ListPyroscopeLabelValues.Register(mcp)
	ListPyroscopeProfileTypes.Register(mcp)
	FetchPyroscopeProfile.Register(mcp)
	FetchPyroscopeSeries.Register(mcp)
	FetchPyroscope.Register(mcp)
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
)

type ListPyroscopeLabelNamesParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	Matchers      string `json:"matchers,omitempty" jsonschema:"Prometheus style matchers used t0 filter the result set (defaults to: {})"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

func listPyroscopeLabelNames(ctx context.Context, args ListPyroscopeLabelNamesParams) ([]string, error) {
	args.Matchers = stringOrDefault(args.Matchers, "{}")

	start, err := rfc3339OrDefault(args.StartRFC3339, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := rfc3339OrDefault(args.EndRFC3339, time.Time{})
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
)

type ListPyroscopeLabelValuesParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	Name          string `json:"name" jsonschema:"required,description=A label name"`
	Matchers      string `json:"matchers,omitempty" jsonschema:"description=Optionally\\, Prometheus style matchers used to filter the result set (defaults to: {})"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

func listPyroscopeLabelValues(ctx context.Context, args ListPyroscopeLabelValuesParams) ([]string, error) {
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	args.Matchers = stringOrDefault(args.Matchers, "{}")

	start, err := rfc3339OrDefault(args.StartRFC3339, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := rfc3339OrDefault(args.EndRFC3339, time.Time{})
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
)

type ListPyroscopeProfileTypesParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

func listPyroscopeProfileTypes(ctx context.Context, args ListPyroscopeProfileTypesParams) ([]string, error) {
	start, err := rfc3339OrDefault(args.StartRFC3339, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := rfc3339OrDefault(args.EndRFC3339, time.Time{})
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

const fetchPyroscopeProfileToolPrompt = `
Fetches a profile from a Pyroscope data source for a given time range. By default, the time range is tha past 1 hour.
The profile type is required, available profile types can be fetched via the list_pyroscope_profile_types tool. Not all
profile types are available for every service. Expect some queries to return empty result sets, this indicates the
profile type does not exist for that query. In such a case, consider trying a related profile type or giving up.
Matchers are not required, but highly recommended, they are generally used to select an application by the service_name
label (e.g. {service_name="foo"}). Use the list_pyroscope_label_names tool to fetch available label names, and the
list_pyroscope_label_values tool to fetch available label values. The returned profile is in DOT format.
`

var FetchPyroscopeProfile = mcpgrafana.MustTool(
	"fetch_pyroscope_profile",
	fetchPyroscopeProfileToolPrompt,
	fetchPyroscopeProfile,
	mcp.WithTitleAnnotation("Fetch Pyroscope profile"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type FetchPyroscopeProfileParams struct {
	DataSourceUID string `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	ProfileType   string `json:"profile_type" jsonschema:"required,description=Type profile type\\, use the list_pyroscope_profile_types tool to fetch available profile types"`
	Matchers      string `json:"matchers,omitempty" jsonschema:"description=Optionally\\, Prometheus style matchers used to filter the result set (defaults to: {})"`
	MaxNodeDepth  int    `json:"max_node_depth,omitempty" jsonschema:"description=Optionally\\, the maximum depth of nodes in the resulting profile. Less depth results in smaller profiles that execute faster\\, more depth result in larger profiles that have more detail. A value of -1 indicates to use an unbounded node depth (default: 100). Reducing max node depth from the default will negatively impact the accuracy of the profile"`
	StartRFC3339  string `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

func fetchPyroscopeProfile(ctx context.Context, args FetchPyroscopeProfileParams) (string, error) {
	args.Matchers = stringOrDefault(args.Matchers, "{}")
	matchersRegex := regexp.MustCompile(`^\{.*\}$`)
	if !matchersRegex.MatchString(args.Matchers) {
		args.Matchers = fmt.Sprintf("{%s}", args.Matchers)
	}

	args.MaxNodeDepth = intOrDefault(args.MaxNodeDepth, 100)

	start, err := rfc3339OrDefault(args.StartRFC3339, time.Time{})
	if err != nil {
		return "", fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := rfc3339OrDefault(args.EndRFC3339, time.Time{})
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

	req := &renderRequest{
		ProfileType: args.ProfileType,
		Matcher:     args.Matchers,
		Start:       start,
		End:         end,
		Format:      "dot",
		MaxNodes:    args.MaxNodeDepth,
	}
	res, err := client.Render(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to call Pyroscope API: %w", err)
	}

	res = cleanupDotProfile(res)
	return res, nil
}

func newPyroscopeClient(ctx context.Context, uid string) (*pyroscopeClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	httpClient := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(
			transport,
		),
		Timeout: 10 * time.Second,
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
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("pyroscope API failed with status code %d", res.StatusCode)
		}
		return nil, fmt.Errorf("pyroscope API failed with status code %d: %s", res.StatusCode, string(body))
	}

	const limit = 1 << 25 // 32 MiB
	body, err := io.ReadAll(io.LimitReader(res.Body, limit))
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

func rfc3339OrDefault(s string, def time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)

	var err error
	if s != "" {
		def, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, err
		}
	}

	return def, nil
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

var cleanupRegex = regexp.MustCompile(`(?m)(fontsize=\d+ )|(id="node\d+" )|(labeltooltip=".*?\)" )|(tooltip=".*?\)" )|(N\d+ -> N\d+).*|(N\d+ \[label="other.*\n)|(shape=box )|(fillcolor="#\w{6}")|(color="#\w{6}" )`)

func cleanupDotProfile(profile string) string {
	return cleanupRegex.ReplaceAllStringFunc(profile, func(match string) string {
		// Preserve edge labels (e.g., "N1 -> N2")
		if m := regexp.MustCompile(`^N\d+ -> N\d+`).FindString(match); m != "" {
			return m
		}
		return ""
	})
}

// ---------------------------------------------------------------------------
// query_pyroscope_series — time-series metrics from profile samples
// ---------------------------------------------------------------------------

const fetchPyroscopeSeriesToolPrompt = `
Queries time-series metrics from a Pyroscope datasource. Returns profile values aggregated over time intervals — the
"when" dimension of profiling (vs fetch_pyroscope_profile which shows the "what" dimension). Same label_selector and
profile_type parameters as fetch_pyroscope_profile. Use both together for complete analysis: series shows when spikes
happen, profile shows which functions cause them. If the time range is not provided, it defaults to the last hour.
`

var FetchPyroscopeSeries = mcpgrafana.MustTool(
	"fetch_pyroscope_series",
	fetchPyroscopeSeriesToolPrompt,
	fetchPyroscopeSeries,
	mcp.WithTitleAnnotation("Query Pyroscope series"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type FetchPyroscopeSeriesParams struct {
	DataSourceUID string   `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	ProfileType   string   `json:"profile_type" jsonschema:"required,description=The profile type\\, use the list_pyroscope_profile_types tool to fetch available profile types"`
	Matchers      string   `json:"matchers,omitempty" jsonschema:"description=Optionally\\, Prometheus style matchers used to filter the result set (defaults to: {})"`
	GroupBy       []string `json:"group_by,omitempty" jsonschema:"description=Optionally\\, labels to group the series by (e.g. [\"pod\"\\, \"version\"])"`
	Step          float64  `json:"step,omitempty" jsonschema:"description=Optionally\\, seconds between data points (default: auto-calculated from time range)"`
	StartRFC3339  string   `json:"start_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string   `json:"end_rfc_3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

func fetchPyroscopeSeries(ctx context.Context, args FetchPyroscopeSeriesParams) (string, error) {
	args.Matchers = stringOrDefault(args.Matchers, "{}")
	matchersRegex := regexp.MustCompile(`^\{.*\}$`)
	if !matchersRegex.MatchString(args.Matchers) {
		args.Matchers = fmt.Sprintf("{%s}", args.Matchers)
	}

	start, err := rfc3339OrDefault(args.StartRFC3339, time.Time{})
	if err != nil {
		return "", fmt.Errorf("failed to parse start timestamp %q: %w", args.StartRFC3339, err)
	}

	end, err := rfc3339OrDefault(args.EndRFC3339, time.Time{})
	if err != nil {
		return "", fmt.Errorf("failed to parse end timestamp %q: %w", args.EndRFC3339, err)
	}

	start, end, err = validateTimeRange(start, end)
	if err != nil {
		return "", err
	}

	// Auto-calculate step: aim for ~50 data points
	step := args.Step
	if step <= 0 {
		durationSec := end.Sub(start).Seconds()
		step = math.Max(durationSec/50.0, 15.0)
	}

	client, err := newPyroscopeClient(ctx, args.DataSourceUID)
	if err != nil {
		return "", fmt.Errorf("failed to create Pyroscope client: %w", err)
	}

	req := &querierv1.SelectSeriesRequest{
		ProfileTypeID: args.ProfileType,
		LabelSelector: args.Matchers,
		Start:         start.UnixMilli(),
		End:           end.UnixMilli(),
		GroupBy:       args.GroupBy,
		Step:          step,
	}
	res, err := client.SelectSeries(ctx, connect.NewRequest(req))
	if err != nil {
		return "", fmt.Errorf("failed to call Pyroscope SelectSeries API: %w", err)
	}

	output := formatSeriesResponse(res.Msg.Series, start, end, step)
	return output, nil
}

// rawSeries is the JSON structure returned for a single time-series.
type rawSeries struct {
	Labels map[string]string `json:"labels"`
	Points [][2]float64      `json:"points"` // [[timestamp_ms, value], ...]
}

func formatSeriesResponse(series []*typesv1.Series, start, end time.Time, step float64) string {
	if len(series) == 0 {
		return "No series data returned for the given query and time range."
	}

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

	if len(raw) == 0 {
		return "No series data returned for the given query and time range."
	}

	out, err := json.Marshal(map[string]any{
		"series":       raw,
		"time_range":   map[string]string{"from": start.Format(time.RFC3339), "to": end.Format(time.RFC3339)},
		"step_seconds": step,
	})
	if err != nil {
		return fmt.Sprintf("failed to marshal series response: %v", err)
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// query_pyroscope — unified tool: profile + metrics + both
// ---------------------------------------------------------------------------

const fetchPyroscopeToolPrompt = `
Unified Pyroscope query tool that mirrors Grafana's query_type parameter. Combines profile (flamegraph) and
metrics (time-series) in a single call. Profile data shows WHICH functions consume resources; metrics data
shows WHEN consumption spiked. Use query_type="both" for complete analysis in one call.

query_type options:
- "profile": returns DOT-format call graph (same as fetch_pyroscope_profile)
- "metrics": returns time-series data points (same as query_pyroscope_series)
- "both" (default): returns both profile and metrics in one response
`

var FetchPyroscope = mcpgrafana.MustTool(
	"fetch_pyroscope",
	fetchPyroscopeToolPrompt,
	fetchPyroscopeUnified,
	mcp.WithTitleAnnotation("Query Pyroscope"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type FetchPyroscopeUnifiedParams struct {
	DataSourceUID string   `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	ProfileType   string   `json:"profile_type" jsonschema:"required,description=The profile type\\, use list_pyroscope_profile_types to discover available types"`
	QueryType     string   `json:"query_type,omitempty" jsonschema:"description=Query type: \"profile\" (flamegraph)\\, \"metrics\" (time-series)\\, or \"both\" (default). Use \"both\" for complete analysis"`
	Matchers      string   `json:"matchers,omitempty" jsonschema:"description=Prometheus style matchers (defaults to: {})"`
	GroupBy       []string `json:"group_by,omitempty" jsonschema:"description=Labels to group metrics series by"`
	Step          float64  `json:"step,omitempty" jsonschema:"description=Seconds between metrics data points (default: auto)"`
	MaxNodeDepth  int      `json:"max_node_depth,omitempty" jsonschema:"description=Max depth for profile call graph (default: 100)"`
	StartRFC3339  string   `json:"start_rfc_3339,omitempty" jsonschema:"description=Start time in RFC3339 (defaults to 1 hour ago)"`
	EndRFC3339    string   `json:"end_rfc_3339,omitempty" jsonschema:"description=End time in RFC3339 (defaults to now)"`
}

func fetchPyroscopeUnified(ctx context.Context, args FetchPyroscopeUnifiedParams) (string, error) {
	queryType := strings.ToLower(strings.TrimSpace(args.QueryType))
	if queryType == "" {
		queryType = "both"
	}
	if queryType != "profile" && queryType != "metrics" && queryType != "both" {
		return "", fmt.Errorf("invalid query_type %q: must be \"profile\", \"metrics\", or \"both\"", args.QueryType)
	}

	wantProfile := queryType == "profile" || queryType == "both"
	wantMetrics := queryType == "metrics" || queryType == "both"

	var profileResult, metricsResult string
	var profileErr, metricsErr error

	if wantProfile {
		profileResult, profileErr = fetchPyroscopeProfile(ctx, FetchPyroscopeProfileParams{
			DataSourceUID: args.DataSourceUID,
			ProfileType:   args.ProfileType,
			Matchers:      args.Matchers,
			MaxNodeDepth:  args.MaxNodeDepth,
			StartRFC3339:  args.StartRFC3339,
			EndRFC3339:    args.EndRFC3339,
		})
	}

	if wantMetrics {
		metricsResult, metricsErr = fetchPyroscopeSeries(ctx, FetchPyroscopeSeriesParams{
			DataSourceUID: args.DataSourceUID,
			ProfileType:   args.ProfileType,
			Matchers:      args.Matchers,
			GroupBy:       args.GroupBy,
			Step:          args.Step,
			StartRFC3339:  args.StartRFC3339,
			EndRFC3339:    args.EndRFC3339,
		})
	}

	// Build combined response
	result := make(map[string]any)
	result["query_type"] = queryType

	if wantProfile {
		if profileErr != nil {
			result["profile"] = map[string]string{"error": profileErr.Error()}
		} else {
			result["profile"] = profileResult
		}
	}

	if wantMetrics {
		if metricsErr != nil {
			result["metrics"] = map[string]string{"error": metricsErr.Error()}
		} else {
			// Parse metrics JSON back to structured data for clean nesting
			var metricsData any
			if err := json.Unmarshal([]byte(metricsResult), &metricsData); err != nil {
				metricsData = metricsResult
			}
			result["metrics"] = metricsData
		}
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}
	return string(out), nil
}
