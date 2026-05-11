package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
)

// lokiBackend abstracts the differences between datasource types that serve
// log data through Loki-compatible tools (native Loki, VictoriaLogs, etc.).
//
// The tool entry points in loki.go normalize user-provided parameters
// (defaults, limits, direction) and delegate the actual datasource query to
// an implementation of this interface. Each backend is responsible for
// translating the request to its native API and parsing the response back
// into the shared LogEntry shape.
type lokiBackend interface {
	// ListLabelNames returns the available label/field names within the
	// given time range. Empty start/end means "use server defaults".
	ListLabelNames(ctx context.Context, start, end time.Time) ([]string, error)

	// ListLabelValues returns the values for a single label/field within
	// the given time range.
	ListLabelValues(ctx context.Context, labelName string, start, end time.Time) ([]string, error)

	// QueryLogs runs a log/metric query and returns the raw entries plus
	// the result type ("streams" | "vector" | "matrix") and (when known)
	// the total number of lines scanned by the backend.
	QueryLogs(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error)

	// QueryStats returns counts about the streams matching a selector.
	// Backends without an exact equivalent (e.g. VictoriaLogs) populate
	// only the fields they can produce and leave the rest as zero.
	QueryStats(ctx context.Context, query string, start, end time.Time) (*Stats, error)

	// QueryPatterns returns detected log patterns. Backends that do not
	// implement pattern detection should return a clear error.
	QueryPatterns(ctx context.Context, query, step string, start, end time.Time) ([]Pattern, error)
}

// lokiQueryParams is the normalized request shape passed to lokiBackend.QueryLogs.
type lokiQueryParams struct {
	Query       string
	QueryType   string // "instant" or "range"
	Start, End  time.Time
	Limit       int    // already clamped to the configured max
	Direction   string // "forward" or "backward"
	StepSeconds int    // for range metric queries
}

// lokiQueryResult is the backend's contribution to QueryLokiLogsResult.
// The tool wrapper layers truncation handling, metadata, and hints on top.
type lokiQueryResult struct {
	Entries           []LogEntry
	ResultType        string // "streams", "vector", "matrix"
	TotalLinesScanned *int   // nil when the backend doesn't expose stats
}

// lokiBackendForDatasource resolves a UID to the appropriate backend by
// inspecting the datasource type. Native Loki is the default; VictoriaLogs
// is selected when the type matches the victoriametrics-logs-datasource
// plugin. The looked-up DataSource is forwarded to the constructors so
// they don't have to re-fetch it (mirroring backendForDatasource in
// prom_backend.go).
func lokiBackendForDatasource(ctx context.Context, uid string) (lokiBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	switch ds.Type {
	case victoriaLogsDatasourceType:
		return newVictoriaLogsBackend(ctx, uid, ds)
	default:
		return newLokiNativeBackend(ctx, uid, ds)
	}
}

// lokiNativeBackend wraps the existing Loki HTTP client to satisfy lokiBackend.
type lokiNativeBackend struct {
	client *Client
}

func newLokiNativeBackend(ctx context.Context, uid string, ds *models.DataSource) (*lokiNativeBackend, error) {
	c, err := newLokiClient(ctx, uid, ds)
	if err != nil {
		return nil, err
	}
	return &lokiNativeBackend{client: c}, nil
}

func formatRFC3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func (b *lokiNativeBackend) ListLabelNames(ctx context.Context, start, end time.Time) ([]string, error) {
	return b.client.fetchData(ctx, "/loki/api/v1/labels", formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end))
}

func (b *lokiNativeBackend) ListLabelValues(ctx context.Context, labelName string, start, end time.Time) ([]string, error) {
	urlPath := fmt.Sprintf("/loki/api/v1/label/%s/values", labelName)
	return b.client.fetchData(ctx, urlPath, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end))
}

func (b *lokiNativeBackend) QueryLogs(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error) {
	response, err := b.client.fetchQuery(ctx, fetchQueryParams{
		Query:       p.Query,
		QueryType:   p.QueryType,
		Start:       formatRFC3339OrEmpty(p.Start),
		End:         formatRFC3339OrEmpty(p.End),
		Limit:       p.Limit,
		Direction:   p.Direction,
		StepSeconds: p.StepSeconds,
	})
	if err != nil {
		return nil, err
	}

	entries, err := parseLokiQueryResponse(response)
	if err != nil {
		return nil, err
	}

	var linesScanned *int
	if response.Data.Stats != nil {
		val := response.Data.Stats.Summary.TotalLinesProcessed
		linesScanned = &val
	}

	return &lokiQueryResult{
		Entries:           entries,
		ResultType:        response.Data.ResultType,
		TotalLinesScanned: linesScanned,
	}, nil
}

func (b *lokiNativeBackend) QueryStats(ctx context.Context, query string, start, end time.Time) (*Stats, error) {
	return b.client.fetchStats(ctx, query, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end))
}

func (b *lokiNativeBackend) QueryPatterns(ctx context.Context, query, step string, start, end time.Time) ([]Pattern, error) {
	return b.client.fetchPatterns(ctx, query, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end), step)
}
