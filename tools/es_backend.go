package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/grafana-openapi-client-go/models"
)

const (
	// DefaultSearchLimit is the default number of documents to return if not specified
	DefaultSearchLimit = 10

	// MaxSearchLimit is the maximum number of documents that can be requested
	MaxSearchLimit = 100

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
	Search(ctx context.Context, index, query string, startTime, endTime *time.Time, limit int) ([]ElasticsearchDocument, error)
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

// elasticsearchBackend handles queries to an Elasticsearch datasource via Grafana proxy.
type elasticsearchBackend struct {
	httpClient *http.Client
	baseURL    string
}

func newElasticsearchBackend(ctx context.Context, ds *models.DataSource) (*elasticsearchBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), ds.UID)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	client := &http.Client{
		Transport: transport,
	}

	return &elasticsearchBackend{
		httpClient: client,
		baseURL:    url,
	}, nil
}

// buildURL constructs a full URL for an Elasticsearch API endpoint
func (b *elasticsearchBackend) buildURL(urlPath string) string {
	fullURL := b.baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	return fullURL + urlPath
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
	Took      int                      `json:"took"`
	Responses []elasticsearchResponse  `json:"responses"`
}

// Search performs a search query against Elasticsearch using the _msearch API.
// Grafana's datasource proxy only allows POST requests to /_msearch for Elasticsearch.
func (b *elasticsearchBackend) Search(ctx context.Context, index, query string, startTime, endTime *time.Time, limit int) ([]ElasticsearchDocument, error) {
	searchQuery := buildElasticsearchQuery(query, startTime, endTime, limit)

	// Build NDJSON payload for _msearch API
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

	fullURL := b.buildURL("/_msearch")

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, &payload)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elasticsearch API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 1024*1024*48)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var msearchResp msearchResponse
	if err := json.Unmarshal(bodyBytes, &msearchResp); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
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
			switch ts := source["@timestamp"].(type) {
			case string:
				doc.Timestamp = ts
			case float64:
				sec := int64(ts) / 1000
				nsec := (int64(ts) % 1000) * int64(time.Millisecond)
				doc.Timestamp = time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
			}
		}
		if fields, ok := hit["fields"].(map[string]interface{}); ok {
			doc.Fields = fields
		}

		documents = append(documents, doc)
	}

	return documents, nil
}

// buildElasticsearchQuery constructs an Elasticsearch query DSL JSON object
func buildElasticsearchQuery(query string, startTime, endTime *time.Time, size int) map[string]interface{} {
	esQuery := map[string]interface{}{
		"size": size,
		"sort": []map[string]interface{}{
			{
				"@timestamp": map[string]string{
					"order": "desc",
				},
			},
		},
	}

	var queryClause map[string]interface{}

	if startTime != nil || endTime != nil || query != "" {
		mustClauses := []map[string]interface{}{}

		if startTime != nil || endTime != nil {
			rangeQuery := map[string]interface{}{
				"@timestamp": map[string]interface{}{},
			}
			if startTime != nil {
				rangeQuery["@timestamp"].(map[string]interface{})["gte"] = startTime.Format(time.RFC3339)
			}
			if endTime != nil {
				rangeQuery["@timestamp"].(map[string]interface{})["lte"] = endTime.Format(time.RFC3339)
			}
			mustClauses = append(mustClauses, map[string]interface{}{
				"range": rangeQuery,
			})
		}

		if query != "" {
			var parsedQuery map[string]interface{}
			if err := json.Unmarshal([]byte(query), &parsedQuery); err == nil {
				mustClauses = append(mustClauses, parsedQuery)
			} else {
				mustClauses = append(mustClauses, map[string]interface{}{
					"query_string": map[string]interface{}{
						"query": query,
					},
				})
			}
		}

		queryClause = map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustClauses,
			},
		}
	} else {
		queryClause = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	esQuery["query"] = queryClause

	return esQuery
}
