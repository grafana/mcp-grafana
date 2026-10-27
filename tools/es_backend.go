package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const (
	// DefaultSearchLimit is the default number of documents to return if not specified
	DefaultSearchLimit = 10

	// MaxSearchLimit is the maximum number of documents that can be requested
	MaxSearchLimit = 100

	// Default time field to use when not configured explicitly in a datasource
	defaultTimeField = "@timestamp"

	elasticsearchDatasourceType = "elasticsearch"
	openSearchDatasourceType    = "grafana-opensearch-datasource"
)

// ElasticsearchDocument represents a single document from search results
type ElasticsearchDocument struct {
	Index     string                 `json:"_index"`
	ID        string                 `json:"_id"`
	Score     *float64               `json:"_score,omitempty"`
	Source    map[string]interface{} `json:"_source"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
}

// esBackend abstracts the differences between Elasticsearch and OpenSearch
// datasource types, which both support Lucene query syntax.
type esBackend interface {
	// Search executes a search query and returns matching documents.
	Search(ctx context.Context, index, query string, startTime, endTime time.Time, limit int) ([]ElasticsearchDocument, error)
}

// esBackendForDatasource looks up the datasource type and returns the appropriate backend.
func esBackendForDatasource(ctx context.Context, uid string) (esBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	switch ds.Type {
	case elasticsearchDatasourceType:
		return newElasticsearchBackend(ctx, ds)
	case openSearchDatasourceType:
		return newOpenSearchBackend(ctx, ds)
	default:
		return nil, fmt.Errorf("datasource %s is of type %s, not elasticsearch or opensearch", uid, ds.Type)
	}
}

// timeFieldFromDataSource reads the configured time field from datasource jsonData.
func timeFieldFromDataSource(ds *models.DataSource) string {
	if ds == nil {
		return defaultTimeField
	}
	jsonData, ok := ds.JSONData.(map[string]interface{})
	if !ok {
		return defaultTimeField
	}
	if tf, ok := jsonData["timeField"].(string); ok && tf != "" {
		return tf
	}
	return defaultTimeField
}

// elasticsearchBackend handles queries to an Elasticsearch datasource. Queries
// go through Grafana's /api/ds/query endpoint, so they show up in the query
// service's query log, when the installed plugin supports them there;
// otherwise they go through the datasource proxy.
type elasticsearchBackend struct {
	httpClient *http.Client
	baseURL    string
	grafanaURL string
	dsUID      string
	timeField  string
	// luceneViaDSQuery and dslViaDSQuery record whether the plugin honours
	// the per-query index (12.6.0) and accepts raw DSL queries without a
	// feature toggle (12.7.0) on /api/ds/query.
	luceneViaDSQuery bool
	dslViaDSQuery    bool
}

func newElasticsearchBackend(ctx context.Context, ds *models.DataSource) (*elasticsearchBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	grafanaURL := strings.TrimRight(cfg.URL, "/")
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", grafanaURL, ds.UID)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	client := &http.Client{
		Transport:     transport,
		CheckRedirect: refuseRedirect,
	}

	major, minor := elasticsearchPluginVersion(ctx, client, grafanaURL)
	return &elasticsearchBackend{
		httpClient:       client,
		baseURL:          url,
		grafanaURL:       grafanaURL,
		dsUID:            ds.UID,
		timeField:        timeFieldFromDataSource(ds),
		luceneViaDSQuery: major > 12 || major == 12 && minor >= 6,
		dslViaDSQuery:    major > 12 || major == 12 && minor >= 7,
	}, nil
}

// elasticsearchPluginVersion returns the major and minor version of the
// installed Elasticsearch plugin, or zeros if it can't be read. Grafana up to
// 12.4 bundles the plugin with the Grafana version, so those report versions
// below 12.6 too.
func elasticsearchPluginVersion(ctx context.Context, client *http.Client, grafanaURL string) (major, minor int) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grafanaURL+"/api/plugins/"+elasticsearchDatasourceType+"/settings", nil)
	if err != nil {
		return 0, 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = resp.Body.Close() }()
	var settings struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&settings) != nil {
		return 0, 0
	}
	if _, err := fmt.Sscanf(settings.Info.Version, "%d.%d", &major, &minor); err != nil {
		return 0, 0
	}
	return major, minor
}

// elasticsearchResponse represents a generic Elasticsearch search response
type elasticsearchResponse struct {
	Took     int                    `json:"took"`
	TimedOut bool                   `json:"timed_out"`
	Status   int                    `json:"status"`
	Error    interface{}            `json:"error,omitempty"`
	Shards   map[string]interface{} `json:"_shards"`
	Hits     struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore *float64                 `json:"max_score"`
		Hits     []map[string]interface{} `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]interface{} `json:"aggregations,omitempty"`
}

// msearchResponse represents the response from Elasticsearch _msearch API
type msearchResponse struct {
	Took      int                     `json:"took"`
	Responses []elasticsearchResponse `json:"responses"`
}

// Search performs a search query against Elasticsearch using the _msearch API.
// Grafana's datasource proxy only allows POST requests to /_msearch for Elasticsearch.
func (b *elasticsearchBackend) Search(ctx context.Context, index, query string, startTime, endTime time.Time, limit int) ([]ElasticsearchDocument, error) {
	sq := esSearchQuery{
		query:     query,
		startTime: startTime,
		endTime:   endTime,
		size:      limit,
		timeField: b.timeField,
	}
	isDSL := json.Unmarshal([]byte(query), &map[string]interface{}{}) == nil
	if isDSL && b.dslViaDSQuery || !isDSL && b.luceneViaDSQuery {
		return b.searchViaDSQuery(ctx, index, sq, isDSL)
	}
	return executeMSearch(ctx, b.httpClient, buildURL(b.baseURL, "/_msearch"), index, sq.build(), b.timeField)
}

// searchViaDSQuery runs a raw_document query through Grafana's /api/ds/query
// endpoint. Query DSL is sent as a raw DSL query holding the same search body
// the proxy path sends; the plugin uses that body verbatim.
func (b *elasticsearchBackend) searchViaDSQuery(ctx context.Context, index string, sq esSearchQuery, isDSL bool) ([]ElasticsearchDocument, error) {
	from, to := sq.startTime, sq.endTime
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-10 * 365 * 24 * time.Hour)
	}

	q := map[string]interface{}{
		"refId":      "A",
		"datasource": map[string]string{"uid": b.dsUID, "type": elasticsearchDatasourceType},
		"index":      index,
		"query":      sq.query,
		"metrics": []map[string]interface{}{
			{"id": "1", "type": "raw_document", "settings": map[string]string{"size": strconv.Itoa(sq.size)}},
		},
		"bucketAggs": []interface{}{},
	}
	if isDSL {
		body, err := json.Marshal(sq.build())
		if err != nil {
			return nil, fmt.Errorf("marshalling query: %w", err)
		}
		q["queryType"] = "dsl"
		q["query"] = string(body)
	}

	resp, err := doDSQuery(ctx, b.httpClient, b.grafanaURL, dsQueryPayload(from, to, q))
	if err != nil {
		return nil, fmt.Errorf("querying Elasticsearch: %w", err)
	}
	r, ok := resp.Responses["A"]
	if !ok {
		return []ElasticsearchDocument{}, nil
	}
	if r.Error != nil {
		return nil, fmt.Errorf("elasticsearch query error: %s", r.Error.Error())
	}
	docs, err := framesToDocuments(r.Frames, b.timeField)
	if err != nil {
		return nil, err
	}
	for i := range docs {
		// The plugin adds the hit's sort values and highlight next to the
		// source fields. ponytail: a source field with either name is dropped
		// too; the frame can't tell them apart.
		delete(docs[i].Source, "sort")
		delete(docs[i].Source, "highlight")
		if docs[i].Timestamp == "" {
			docs[i].Timestamp = esTimestamp(docs[i].Source[b.timeField])
		}
	}
	return docs, nil
}

// esTimestamp formats a document's time field value: strings are kept as
// they are, and epoch milliseconds become RFC3339.
func esTimestamp(v interface{}) string {
	switch ts := v.(type) {
	case string:
		return ts
	case float64:
		return time.UnixMilli(int64(ts)).UTC().Format(time.RFC3339Nano)
	}
	return ""
}

// executeMSearch runs an Elasticsearch-compatible _msearch request and converts hits to documents.
func executeMSearch(ctx context.Context, client *http.Client, url string, index string,
	searchQuery map[string]interface{}, timeField string) ([]ElasticsearchDocument, error) {
	header := map[string]interface{}{
		"index":              index,
		"ignore_unavailable": true,
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshalling header: %w", err)
	}

	queryBytes, err := json.Marshal(searchQuery)
	if err != nil {
		return nil, fmt.Errorf("marshalling query: %w", err)
	}

	// NDJSON format: each JSON object on its own line, ending with newline
	var payload bytes.Buffer
	payload.Write(headerBytes)
	payload.WriteByte('\n')
	payload.Write(queryBytes)
	payload.WriteByte('\n')

	req, err := http.NewRequestWithContext(ctx, "POST", url, &payload)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("elasticsearch API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var msearchResp msearchResponse
	if err := json.Unmarshal(bodyBytes, &msearchResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(msearchResp.Responses) == 0 {
		return nil, fmt.Errorf("no responses returned from _msearch")
	}

	esResp := &msearchResp.Responses[0]
	if esResp.Error != nil {
		return nil, fmt.Errorf("elasticsearch query error: %v", esResp.Error)
	}

	// Convert hits to documents
	documents := make([]ElasticsearchDocument, 0, len(esResp.Hits.Hits))
	for _, hit := range esResp.Hits.Hits {
		doc := ElasticsearchDocument{
			Source: make(map[string]interface{}),
		}

		if index, ok := hit["_index"].(string); ok {
			doc.Index = index
		}
		if id, ok := hit["_id"].(string); ok {
			doc.ID = id
		}
		if score, ok := hit["_score"].(float64); ok {
			doc.Score = &score
		}
		if source, ok := hit["_source"].(map[string]interface{}); ok {
			doc.Source = source
			doc.Timestamp = esTimestamp(source[timeField])
		}
		if fields, ok := hit["fields"].(map[string]interface{}); ok {
			doc.Fields = fields
		}

		documents = append(documents, doc)
	}

	return documents, nil
}

// esSearchQuery collects fields to build an Elasticsearch query DSL JSON
type esSearchQuery struct {
	query     string
	startTime time.Time
	endTime   time.Time
	size      int
	timeField string
	// sortFormat optionally sets the "format" on the time field sort clause,
	// e.g. "epoch_nanos_int" for Quickwit nanosecond timestamps.
	sortFormat string
}

// build constructs the Elasticsearch query DSL JSON
func (q esSearchQuery) build() map[string]interface{} {
	esQuery := map[string]interface{}{
		"size": q.size,
		"sort": []map[string]interface{}{
			{
				q.timeField: q.buildSortField(),
			},
		},
		"query": q.buildQueryClause(),
	}

	return esQuery
}

func (q esSearchQuery) buildSortField() map[string]string {
	sortField := map[string]string{
		"order": "desc",
	}
	if q.sortFormat != "" {
		sortField["format"] = q.sortFormat
	}
	return sortField
}

func (q esSearchQuery) buildQueryClause() map[string]interface{} {
	if q.startTime.IsZero() && q.endTime.IsZero() && q.query == "" {
		return map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	mustClauses := []map[string]interface{}{}

	if rangeClause := q.buildRangeClause(); rangeClause != nil {
		mustClauses = append(mustClauses, rangeClause)
	}

	if q.query != "" {
		var parsedQuery map[string]interface{}
		var textClause map[string]interface{}
		if err := json.Unmarshal([]byte(q.query), &parsedQuery); err == nil {
			textClause = parsedQuery
		} else {
			textClause = map[string]interface{}{
				"query_string": map[string]interface{}{
					"query": q.query,
				},
			}
		}
		if len(mustClauses) == 0 {
			return textClause
		}
		mustClauses = append(mustClauses, textClause)
	}

	return map[string]interface{}{
		"bool": map[string]interface{}{
			"must": mustClauses,
		},
	}
}

func (q esSearchQuery) buildRangeClause() map[string]interface{} {
	if q.startTime.IsZero() && q.endTime.IsZero() {
		return nil
	}
	rangeQuery := map[string]interface{}{}
	if !q.startTime.IsZero() {
		rangeQuery["gte"] = q.startTime.Format(time.RFC3339)
	}
	if !q.endTime.IsZero() {
		rangeQuery["lte"] = q.endTime.Format(time.RFC3339)
	}
	return map[string]interface{}{
		"range": map[string]interface{}{
			q.timeField: rangeQuery,
		},
	}
}
