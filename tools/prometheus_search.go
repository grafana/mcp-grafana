package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/prometheus/prometheus/promql/parser"
)

// The defaults match the gcx search commands (grafana/gcx#1374).
const (
	defaultPrometheusSearchLimit         = 50
	defaultPrometheusSearchFuzzAlg       = "jarowinkler"
	defaultPrometheusSearchFuzzThreshold = 70
)

// PrometheusSearchParams holds the parameters that the three search tools
// share.
type PrometheusSearchParams struct {
	DatasourceUID string   `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Matches       []string `json:"matches,omitempty" jsonschema:"description=PromQL series selectors that limit the candidates. Combine as OR"`
	StartRFC3339  string   `json:"startRfc3339,omitempty" jsonschema:"description=Start time (RFC3339 or 'now-7d'). Default: last hour"`
	EndRFC3339    string   `json:"endRfc3339,omitempty" jsonschema:"description=End time (RFC3339 or 'now')"`
	FuzzAlg       string   `json:"fuzzAlg,omitempty" jsonschema:"enum=jarowinkler,enum=subsequence,default=jarowinkler,description=Fuzzy match algorithm"`
	FuzzThreshold *int     `json:"fuzzThreshold,omitempty" jsonschema:"minimum=0,maximum=100,default=70,description=Minimum match score (0-100)"`
	SortBy        string   `json:"sortBy,omitempty" jsonschema:"enum=score,enum=alpha,description=Default: score with search\\, else alpha"`
	SortDir       string   `json:"sortDir,omitempty" jsonschema:"enum=asc,enum=dsc,description=Sort direction. alpha only"`
	Limit         int      `json:"limit,omitempty" jsonschema:"minimum=1,default=50,description=The maximum number of results"`
	IncludeScore  bool     `json:"includeScore,omitempty" jsonschema:"description=Include the match score (0-1) of each result"`
}

type SearchPrometheusMetricNamesParams struct {
	PrometheusSearchParams
	Search          []string `json:"search" jsonschema:"required,description=Fuzzy search terms. Combine as OR"`
	IncludeMetadata bool     `json:"includeMetadata,omitempty" jsonschema:"description=Include the metric type\\, help and unit"`
}

type SearchPrometheusLabelNamesParams struct {
	PrometheusSearchParams
	Search []string `json:"search,omitempty" jsonschema:"description=Fuzzy search terms. Combine as OR. Give search\\, matches or both"`
}

type SearchPrometheusLabelValuesParams struct {
	PrometheusSearchParams
	Label  string   `json:"label" jsonschema:"required,description=The label whose values to search"`
	Search []string `json:"search,omitempty" jsonschema:"description=Fuzzy search terms. Combine as OR. Without search\\, values sort alpha"`
}

type PrometheusSearchResult struct {
	Name  string   `json:"name,omitempty"`
	Value string   `json:"value,omitempty"`
	Score *float64 `json:"score,omitempty"`
	Type  string   `json:"type,omitempty"`
	Help  string   `json:"help,omitempty"`
	Unit  string   `json:"unit,omitempty"`
}

type SearchPrometheusResponse struct {
	Results []PrometheusSearchResult `json:"results"`
	// HasMore is true when the server stopped at the limit. The API has no
	// cursor: raise the limit or narrow the matches to get more.
	HasMore bool `json:"hasMore"`
	// Incomplete is true when the stream ended without a trailer, because
	// of the size limit or a lost connection. A higher limit does not help.
	Incomplete bool     `json:"incomplete,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

func searchPrometheusMetricNames(ctx context.Context, args SearchPrometheusMetricNamesParams) (*SearchPrometheusResponse, error) {
	if len(args.Search) == 0 {
		return nil, errors.New("search is required; use list_prometheus_metric_names to list metric names without a search")
	}
	q, err := searchPrometheusQueryParams(args.PrometheusSearchParams, args.Search)
	if err != nil {
		return nil, err
	}
	if args.IncludeMetadata {
		q.Set("include_metadata", "true")
	}
	return searchPrometheus(ctx, args.DatasourceUID, "metric_names", q)
}

func searchPrometheusLabelNames(ctx context.Context, args SearchPrometheusLabelNamesParams) (*SearchPrometheusResponse, error) {
	if len(args.Search) == 0 && len(args.Matches) == 0 {
		return nil, errors.New("give search, matches or both")
	}
	q, err := searchPrometheusQueryParams(args.PrometheusSearchParams, args.Search)
	if err != nil {
		return nil, err
	}
	return searchPrometheus(ctx, args.DatasourceUID, "label_names", q)
}

func searchPrometheusLabelValues(ctx context.Context, args SearchPrometheusLabelValuesParams) (*SearchPrometheusResponse, error) {
	if strings.TrimSpace(args.Label) == "" {
		return nil, errors.New("label is required")
	}
	q, err := searchPrometheusQueryParams(args.PrometheusSearchParams, args.Search)
	if err != nil {
		return nil, err
	}
	q.Set("label", args.Label)
	return searchPrometheus(ctx, args.DatasourceUID, "label_values", q)
}

// searchPrometheus sends a validated search request. target is the search
// endpoint: metric_names, label_names or label_values.
func searchPrometheus(ctx context.Context, datasourceUID, target string, q url.Values) (*SearchPrometheusResponse, error) {
	backend, err := backendForDatasource(ctx, datasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting backend: %w", err)
	}
	// An exact type match: victoriaMetricsBackend embeds *prometheusBackend,
	// but VictoriaMetrics has no search API.
	pb, ok := backend.(*prometheusBackend)
	if !ok {
		return nil, fmt.Errorf("datasource %s does not support the Prometheus search API; use list_prometheus_metric_names, list_prometheus_label_names or list_prometheus_label_values", datasourceUID)
	}

	return pb.search(ctx, target, q, defaultResponseLimitBytes)
}

// searchPrometheusQueryParams validates the shared parameters and builds the
// query string. It rejects before any I/O the inputs that the servers
// reject, and inputs that the servers accept but answer with a different
// question.
func searchPrometheusQueryParams(args PrometheusSearchParams, search []string) (url.Values, error) {
	q := url.Values{}

	for _, s := range search {
		// Prometheus drops an empty term and returns every name.
		if strings.TrimSpace(s) == "" {
			return nil, errors.New("search terms must not be empty")
		}
		q.Add("search[]", s)
	}

	p := parser.NewParser(parser.Options{})
	for _, m := range args.Matches {
		if _, err := p.ParseMetricSelector(m); err != nil {
			return nil, fmt.Errorf("invalid matches selector %q: %w", m, err)
		}
		q.Add("match[]", m)
	}

	start, err := parseStartTime(args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	end, err := parseEndTime(args.EndRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return nil, errors.New("start time must not be after end time")
	}
	if !start.IsZero() {
		q.Set("start", strconv.FormatInt(start.Unix(), 10))
	}
	if !end.IsZero() {
		q.Set("end", strconv.FormatInt(end.Unix(), 10))
	}

	fuzzAlg := args.FuzzAlg
	switch fuzzAlg {
	case "":
		fuzzAlg = defaultPrometheusSearchFuzzAlg
	case "jarowinkler", "subsequence":
	default:
		return nil, fmt.Errorf("invalid fuzzAlg %q: must be jarowinkler or subsequence", fuzzAlg)
	}
	q.Set("fuzz_alg", fuzzAlg)

	fuzzThreshold := defaultPrometheusSearchFuzzThreshold
	if args.FuzzThreshold != nil {
		fuzzThreshold = *args.FuzzThreshold
	}
	if fuzzThreshold < 0 || fuzzThreshold > 100 {
		return nil, fmt.Errorf("invalid fuzzThreshold %d: must be between 0 and 100", fuzzThreshold)
	}
	q.Set("fuzz_threshold", strconv.Itoa(fuzzThreshold))

	// The server default is case-sensitive, and the tools do not expose this
	// option, so always send false.
	q.Set("case_sensitive", "false")

	sortBy := args.SortBy
	switch sortBy {
	case "":
		sortBy = "alpha"
		if len(search) > 0 {
			sortBy = "score"
		}
	case "alpha":
	case "score":
		if len(search) == 0 {
			return nil, errors.New("sortBy score requires search; use sortBy alpha")
		}
	default:
		return nil, fmt.Errorf("invalid sortBy %q: must be score or alpha", sortBy)
	}
	q.Set("sort_by", sortBy)

	switch args.SortDir {
	case "":
	case "asc", "dsc":
		if sortBy != "alpha" {
			return nil, errors.New("sortDir is valid with sortBy alpha only")
		}
		q.Set("sort_dir", args.SortDir)
	default:
		return nil, fmt.Errorf("invalid sortDir %q: must be asc or dsc", args.SortDir)
	}

	// Limit 0 is not sent: Mimir reads it as unlimited and Prometheus
	// rejects it.
	limit := args.Limit
	if limit == 0 {
		limit = defaultPrometheusSearchLimit
	}
	if limit < 0 {
		return nil, fmt.Errorf("invalid limit %d: must be positive", limit)
	}
	q.Set("limit", strconv.Itoa(limit))

	if args.IncludeScore {
		q.Set("include_score", "true")
	}
	return q, nil
}

// search calls /api/v1/search/{target} and decodes the NDJSON stream. It
// does not use promv1.API, because api.Client.Do reads the full body
// without a size limit.
func (b *prometheusBackend) search(ctx context.Context, target string, params url.Values, maxBytes int64) (*SearchPrometheusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/api/v1/search/"+target+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, searchHTTPError(resp.StatusCode, body)
	}
	return decodeSearchStream(ctx, resp.Body, maxBytes, target)
}

type searchStreamFrame struct {
	Results   []PrometheusSearchResult `json:"results"`
	Status    string                   `json:"status"`
	HasMore   bool                     `json:"has_more"`
	Warnings  []string                 `json:"warnings"`
	ErrorType string                   `json:"errorType"`
	Error     string                   `json:"error"`
}

// decodeSearchStream reads zero or more batch frames and then one trailer
// frame (status set). The API contract requires clients to accept an EOF
// without a trailer, so that case returns the results read so far with
// Incomplete set.
func decodeSearchStream(ctx context.Context, body io.Reader, maxBytes int64, target string) (*SearchPrometheusResponse, error) {
	// Read one byte past maxBytes, so that a body of exactly maxBytes is not
	// reported as cut.
	counter := &byteCountingReader{r: io.LimitReader(body, maxBytes+1)}
	dec := json.NewDecoder(counter)
	out := &SearchPrometheusResponse{Results: []PrometheusSearchResult{}}

	for {
		var frame searchStreamFrame
		if err := dec.Decode(&frame); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, fmt.Errorf("parsing search response: %w", err)
			}
			warning := fmt.Sprintf("search stream ended after %d results without a trailer (connection lost?); results may be incomplete", len(out.Results))
			if counter.n > maxBytes {
				warning = fmt.Sprintf("search response exceeded %d bytes after %d results; results are incomplete. Use a lower limit or narrower matches", maxBytes, len(out.Results))
			}
			out.HasMore = true
			out.Incomplete = true
			out.Warnings = appendNewWarnings(out.Warnings, []string{warning})
			return out, nil
		}

		// Prometheus sends warnings on the first batch and repeats them on
		// the trailer when they change. Mimir sends them on the trailer.
		out.Warnings = appendNewWarnings(out.Warnings, frame.Warnings)

		switch frame.Status {
		case "":
			out.Results = append(out.Results, frame.Results...)
		case "error":
			return nil, fmt.Errorf("searching Prometheus %s: %s", target, errorTypeMessage(frame.ErrorType, frame.Error))
		default:
			out.HasMore = frame.HasMore
			return out, nil
		}
	}
}

func searchHTTPError(status int, body []byte) error {
	var parsed struct {
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	detail := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &parsed) == nil && (parsed.ErrorType != "" || parsed.Error != "") {
		detail = errorTypeMessage(parsed.ErrorType, parsed.Error)
	}
	err := fmt.Errorf("prometheus search API returned status %d: %s", status, detail)

	// Mimir answers 404 feature_not_enabled. Prometheus answers
	// "search API disabled" with a status that operators can change.
	if (status == http.StatusNotFound && parsed.ErrorType == "feature_not_enabled") ||
		(parsed.ErrorType == "unavailable" && strings.Contains(parsed.Error, "search API disabled")) {
		return fmt.Errorf("%w. The search API is not enabled on this datasource (Prometheus needs --enable-feature=search-api, Mimir needs -querier.experimental-search-api-enabled). Use list_prometheus_metric_names, list_prometheus_label_names or list_prometheus_label_values", err)
	}
	return err
}

func errorTypeMessage(errorType, message string) string {
	switch {
	case errorType != "" && message != "":
		return errorType + ": " + message
	case errorType != "":
		return errorType
	default:
		return message
	}
}

type byteCountingReader struct {
	r io.Reader
	n int64
}

func (c *byteCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func appendNewWarnings(dst, src []string) []string {
	for _, w := range src {
		if !slices.Contains(dst, w) {
			dst = append(dst, w)
		}
	}
	return dst
}

const prometheusSearchAPINote = "Needs the experimental search API (Prometheus 3.13+, Mimir 3.2+)."

var SearchPrometheusMetricNames = mcpgrafana.MustTool(
	"search_prometheus_metric_names",
	"Fuzzy search of metric names in Prometheus or Mimir. "+prometheusSearchAPINote+" Else use list_prometheus_metric_names.",
	searchPrometheusMetricNames,
	mcp.WithTitleAnnotation("Search Prometheus metric names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var SearchPrometheusLabelNames = mcpgrafana.MustTool(
	"search_prometheus_label_names",
	"Fuzzy search of label names in Prometheus or Mimir. "+prometheusSearchAPINote+" Else use list_prometheus_label_names.",
	searchPrometheusLabelNames,
	mcp.WithTitleAnnotation("Search Prometheus label names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var SearchPrometheusLabelValues = mcpgrafana.MustTool(
	"search_prometheus_label_values",
	"Fuzzy search of the values of one label in Prometheus or Mimir. "+prometheusSearchAPINote+" Else use list_prometheus_label_values.",
	searchPrometheusLabelValues,
	mcp.WithTitleAnnotation("Search Prometheus label values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)
