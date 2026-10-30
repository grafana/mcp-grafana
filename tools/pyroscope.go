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
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	querierv1 "github.com/grafana/pyroscope/api/gen/proto/go/querier/v1"
	"github.com/grafana/pyroscope/api/gen/proto/go/querier/v1/querierv1connect"
	typesv1 "github.com/grafana/pyroscope/api/gen/proto/go/types/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddPyroscopeTools registers the Pyroscope tools on the MCP server.
// query_pyroscope is registered only when enableQueryTools is true; the
// label and profile-type listing tools stay available either way.
func AddPyroscopeTools(s *mcp.Server, enableQueryTools bool) {
	ListPyroscopeLabelNames.Register(s)
	ListPyroscopeLabelValues.Register(s)
	ListPyroscopeProfileTypes.Register(s)
	if enableQueryTools {
		QueryPyroscope.Register(s)
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
	mcpgrafana.WithTitleAnnotation("List Pyroscope label names"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
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

	return nonNilStrings(res.Msg.Names), nil
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
	mcpgrafana.WithTitleAnnotation("List Pyroscope label values"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
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

	return nonNilStrings(res.Msg.Names), nil
}

// nonNilStrings returns s, or an empty slice if s is nil, so that an empty
// result serialises as [] rather than null.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
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
	mcpgrafana.WithTitleAnnotation("List Pyroscope profile types"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
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
	// Unit is the unit Grafana reports for the values, e.g. "cores" or
	// "binBps" for cumulative profile types, which it converts to a rate.
	Unit string `json:"unit,omitempty"`
}

// seriesFramesToResponse converts the "series" frames Grafana's Pyroscope
// datasource returns (one per series, with a time and a value field) into a
// seriesResponse. Exemplar and annotation frames are skipped.
//
// The datasource raises the step to its minStep, so the reported step is the
// smallest gap between points, falling back to the requested step when no
// series has two points.
func seriesFramesToResponse(frames data.Frames, start, end time.Time, step float64) *seriesResponse {
	res := &seriesResponse{
		Series:    []rawSeries{},
		TimeRange: map[string]string{"from": start.Format(time.RFC3339), "to": end.Format(time.RFC3339)},
		StepSecs:  step,
	}
	var minGapMs float64
	for _, frame := range frames {
		if frame.Name != "series" {
			continue
		}
		timeIdx, valueIdx := findTimeAndValueFields(frame.Fields)
		if timeIdx == -1 || valueIdx == -1 {
			continue
		}
		points := make([][2]float64, 0, frame.Rows())
		for i := 0; i < frame.Rows(); i++ {
			ts, tsOk := toMillis(frame.At(timeIdx, i))
			val, valOk := toFloat(frame.At(valueIdx, i))
			if tsOk && valOk {
				if n := len(points); n > 0 {
					if gap := float64(ts) - points[n-1][0]; gap > 0 && (minGapMs == 0 || gap < minGapMs) {
						minGapMs = gap
					}
				}
				points = append(points, [2]float64{float64(ts), val})
			}
		}
		if len(points) == 0 {
			continue
		}
		valueField := frame.Fields[valueIdx]
		labels := map[string]string(valueField.Labels)
		if labels == nil {
			labels = map[string]string{}
		}
		if valueField.Config != nil && res.Unit == "" {
			res.Unit = valueField.Config.Unit
		}
		res.Series = append(res.Series, rawSeries{Labels: labels, Points: points})
	}
	if minGapMs > 0 {
		res.StepSecs = minGapMs / 1000
	}
	return res
}

// pyroscopeDSQuery runs one Pyroscope query of the given queryType
// ("profile" or "metrics") through Grafana's /api/ds/query endpoint, so it
// goes through the query service.
func pyroscopeDSQuery(ctx context.Context, uid, queryType, profileType, matchers string, groupBy []string, start, end time.Time, step float64) (data.Frames, error) {
	httpClient, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]interface{}{
		"refId":         "A",
		"datasource":    map[string]string{"uid": uid, "type": "grafana-pyroscope-datasource"},
		"queryType":     queryType,
		"profileTypeId": profileType,
		"labelSelector": matchers,
		"groupBy":       nonNilStrings(groupBy),
	}
	if step > 0 {
		// The datasource uses max(intervalMs, the datasource's minStep) as
		// the series step. Grafana reads intervalMs as an integer and
		// ignores a fractional value.
		query["intervalMs"] = int64(math.Round(step * 1000))
		query["maxDataPoints"] = int64(math.Ceil(end.Sub(start).Seconds()/step)) + 1
	}
	resp, err := doDSQuery(ctx, httpClient, baseURL, dsQueryPayload(start, end, query))
	if err != nil {
		return nil, err
	}
	r, ok := resp.Responses["A"]
	if !ok {
		return nil, fmt.Errorf("pyroscope query returned no result")
	}
	if r.Error != nil {
		return nil, r.Error
	}
	return r.Frames, nil
}

// pyroscopeDSProfileTable fetches the merged flame graph through
// /api/ds/query and renders it with buildProfileTable.
func pyroscopeDSProfileTable(ctx context.Context, uid, profileType, matchers string, start, end time.Time, maxRows int) (string, error) {
	frames, err := pyroscopeDSQuery(ctx, uid, "profile", profileType, matchers, nil, start, end, 0)
	if err != nil {
		return "", err
	}
	for _, frame := range frames {
		if frame.Meta == nil || frame.Meta.PreferredVisualization != data.VisTypeFlameGraph {
			continue
		}
		nodes, err := flameFrameNodes(frame)
		if err != nil {
			return "", err
		}
		return buildProfileTable(nodes, sampleUnitFromProfileType(profileType), maxRows)
	}
	return "", fmt.Errorf("pyroscope query returned no flame graph frame")
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

// flameNode is one node of a merged flame graph. Parents precede their
// children, and the root (the synthetic "total" node) has parent -1.
type flameNode struct {
	name   string
	total  int64
	self   int64
	parent int
}

// flameFrameNodes reads the flame graph frame Grafana's Pyroscope datasource
// returns from /api/ds/query: one row per node in depth-first order, with
// level, value (total), self and an enum label field.
func flameFrameNodes(frame *data.Frame) ([]flameNode, error) {
	level, _ := frame.FieldByName("level")
	value, _ := frame.FieldByName("value")
	self, _ := frame.FieldByName("self")
	label, _ := frame.FieldByName("label")
	if level == nil || value == nil || self == nil || label == nil {
		return nil, fmt.Errorf("unexpected Pyroscope flame graph frame: want level, value, self and label fields")
	}
	var names []string
	if c := label.Config; c != nil && c.TypeConfig != nil && c.TypeConfig.Enum != nil {
		names = c.TypeConfig.Enum.Text
	}
	int64At := func(f *data.Field, i int) int64 {
		v, _ := f.At(i).(int64)
		return v
	}

	nodes := make([]flameNode, 0, frame.Rows())
	var stack []int // stack[l] is the index of the latest node at level l
	for i := 0; i < frame.Rows(); i++ {
		lvl := int(int64At(level, i))
		if lvl < 0 || lvl > len(stack) {
			return nil, fmt.Errorf("invalid Pyroscope flame graph frame: row %d is at level %d after level %d", i, lvl, len(stack)-1)
		}
		n := flameNode{total: int64At(value, i), self: int64At(self, i), parent: -1}
		var idx int
		switch v := label.At(i).(type) {
		case data.EnumItemIndex:
			idx = int(v)
		case int64:
			// Older datasource versions send enum indexes as int64.
			idx = int(v)
		default:
			return nil, fmt.Errorf("invalid Pyroscope flame graph frame: label field has type %T", v)
		}
		if idx >= 0 && idx < len(names) {
			n.name = names[idx]
		}
		if lvl > 0 {
			n.parent = stack[lvl-1]
		}
		stack = append(stack[:lvl], len(nodes))
		nodes = append(nodes, n)
	}
	return nodes, nil
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
// nodes[0] is the synthetic root ("total"); sub-threshold subtrees are
// collapsed server-side into explicit "other" nodes.
func buildProfileTable(nodes []flameNode, unit string, maxRows int) (string, error) {
	if len(nodes) == 0 || nodes[0].total == 0 {
		return "", fmt.Errorf("pyroscope API returned a empty profile")
	}
	total := nodes[0].total

	stats := map[string]*functionStat{}
	var truncated int64
	for _, n := range nodes {
		if n.parent < 0 {
			continue
		}
		if n.name == truncatedFunctionName {
			truncated += n.self
			continue
		}
		s := stats[n.name]
		if s == nil {
			s = &functionStat{name: n.name}
			stats[n.name] = s
		}
		s.flat += n.self
		// Count total only at the outermost occurrence of the function so
		// recursion isn't double-counted.
		recursive := false
		for p := n.parent; p >= 0 && nodes[p].parent >= 0; p = nodes[p].parent {
			if nodes[p].name == n.name {
				recursive = true
				break
			}
		}
		if !recursive {
			s.cum += n.total
		}
	}

	list := make([]*functionStat, 0, len(stats))
	for _, s := range stats {
		if s.flat == 0 && s.cum == 0 {
			continue
		}
		list = append(list, s)
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
- "metrics": returns time-series data points; for cumulative profile types (e.g. CPU time, allocations) each point is a
  per-second rate over its step, in the reported unit (e.g. "cores" for CPU time)
- "both" (default): returns both profile and metrics in one response

format options (shape of the profile data):
- "table" (default): per-function table with flat (self) and cumulative values, ranked by flat
- "dot": call graph in Graphviz DOT format; nodes are per source line, so one function may span several nodes
`

var QueryPyroscope = mcpgrafana.MustTool(
	"query_pyroscope",
	queryPyroscopeToolPrompt,
	queryPyroscope,
	mcpgrafana.WithTitleAnnotation("Query Pyroscope"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

type QueryPyroscopeParams struct {
	DataSourceUID string   `json:"data_source_uid" jsonschema:"required,description=The UID of the datasource to query"`
	ProfileType   string   `json:"profile_type" jsonschema:"required,description=The profile type (e.g. process_cpu:cpu:nanoseconds:cpu:nanoseconds)"`
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
			// /api/ds/query has no DOT rendering, so the call graph still
			// comes from the datasource proxy.
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
			profile, profileErr = pyroscopeDSProfileTable(ctx, args.DataSourceUID, args.ProfileType, matchers, start, end, maxNodes)
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

		frames, metricsErr := pyroscopeDSQuery(ctx, args.DataSourceUID, "metrics", args.ProfileType, matchers, args.GroupBy, start, end, step)
		if metricsErr != nil {
			if queryType == "metrics" {
				return "", fmt.Errorf("failed to fetch metrics: %w", metricsErr)
			}
			result["metrics"] = map[string]string{"error": metricsErr.Error()}
		} else {
			result["metrics"] = seriesFramesToResponse(frames, start, end, step)
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
