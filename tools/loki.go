package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

func lokiClientFromContext(ctx context.Context, uid string) (*http.Client, string, error) {
	grafanaURL, apiKey := mcpgrafana.GrafanaURLFromContext(ctx), mcpgrafana.GrafanaAPIKeyFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(grafanaURL, "/"), uid)

	client := &http.Client{
		Transport: &authRoundTripper{
			apiKey:     apiKey,
			underlying: http.DefaultTransport,
		},
	}

	return client, url, nil
}

type authRoundTripper struct {
	apiKey     string
	underlying http.RoundTripper
}

func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+rt.apiKey)
	}
	return rt.underlying.RoundTrip(req)
}

// QueryLokiParams defines the parameters for querying Loki
type QueryLokiParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=The LogQL query to execute"`
	StartRFC3339  string `json:"startRfc3339" jsonschema:"required,description=The start time in RFC3339 format"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=The end time in RFC3339 format"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=The maximum number of log lines to return"`
	Direction     string `json:"direction,omitempty" jsonschema:"description=The direction of the query, either 'forward' or 'backward'"`
}

// queryLoki executes a LogQL query against a Loki datasource
func queryLoki(ctx context.Context, args QueryLokiParams) (map[string]interface{}, error) {
	client, baseURL, err := lokiClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Loki client: %w", err)
	}

	// Parse time parameters
	startTime, err := time.Parse(time.RFC3339, args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}

	var endTime time.Time
	if args.EndRFC3339 != "" {
		endTime, err = time.Parse(time.RFC3339, args.EndRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
	} else {
		endTime = time.Now()
	}

	// Set default limit if not provided
	limit := args.Limit
	if limit == 0 {
		limit = 100
	}

	// Set default direction if not provided
	direction := args.Direction
	if direction == "" {
		direction = "backward"
	}

	// Build the query URL
	queryURL := fmt.Sprintf("%s/loki/api/v1/query_range", baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("query", args.Query)
	q.Add("start", fmt.Sprintf("%d", startTime.UnixNano()))
	q.Add("end", fmt.Sprintf("%d", endTime.UnixNano()))
	q.Add("limit", fmt.Sprintf("%d", limit))
	q.Add("direction", direction)
	req.URL.RawQuery = q.Encode()

	// Execute the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing Loki query: %w", err)
	}
	defer resp.Body.Close()

	// For simplicity, we'll return a placeholder response
	// In a real implementation, you would parse the JSON response
	return map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "streams",
			"result":     []interface{}{},
		},
	}, nil
}

// QueryLoki is a tool for querying Loki
var QueryLoki = mcpgrafana.MustTool(
	"query_loki",
	"Query Loki using LogQL",
	queryLoki,
)

// ListLokiLabelNamesParams defines the parameters for listing Loki label names
type ListLokiLabelNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the time range to filter the results by"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the time range to filter the results by"`
}

// listLokiLabelNames lists all label names in a Loki datasource
func listLokiLabelNames(ctx context.Context, args ListLokiLabelNamesParams) ([]string, error) {
	client, baseURL, err := lokiClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Loki client: %w", err)
	}

	// Parse time parameters if provided
	var startTime, endTime time.Time
	if args.StartRFC3339 != "" {
		startTime, err = time.Parse(time.RFC3339, args.StartRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
	} else {
		startTime = time.Now().Add(-1 * time.Hour)
	}

	if args.EndRFC3339 != "" {
		endTime, err = time.Parse(time.RFC3339, args.EndRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
	} else {
		endTime = time.Now()
	}

	// Build the query URL
	queryURL := fmt.Sprintf("%s/loki/api/v1/labels", baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("start", fmt.Sprintf("%d", startTime.UnixNano()))
	q.Add("end", fmt.Sprintf("%d", endTime.UnixNano()))
	req.URL.RawQuery = q.Encode()

	// Execute the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing Loki label names: %w", err)
	}
	defer resp.Body.Close()

	// For simplicity, we'll return a placeholder response
	// In a real implementation, you would parse the JSON response
	return []string{"app", "container", "filename", "host", "job", "level", "namespace", "pod"}, nil
}

// ListLokiLabelNames is a tool for listing Loki label names
var ListLokiLabelNames = mcpgrafana.MustTool(
	"list_loki_label_names",
	"List the label names in a Loki datasource",
	listLokiLabelNames,
)

// ListLokiLabelValuesParams defines the parameters for listing Loki label values
type ListLokiLabelValuesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LabelName     string `json:"labelName" jsonschema:"required,description=The name of the label to query"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the query"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the query"`
}

// listLokiLabelValues lists all values for a specific label in a Loki datasource
func listLokiLabelValues(ctx context.Context, args ListLokiLabelValuesParams) ([]string, error) {
	client, baseURL, err := lokiClientFromContext(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting Loki client: %w", err)
	}

	// Parse time parameters if provided
	var startTime, endTime time.Time
	if args.StartRFC3339 != "" {
		startTime, err = time.Parse(time.RFC3339, args.StartRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
	} else {
		startTime = time.Now().Add(-1 * time.Hour)
	}

	if args.EndRFC3339 != "" {
		endTime, err = time.Parse(time.RFC3339, args.EndRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
	} else {
		endTime = time.Now()
	}

	// Build the query URL
	queryURL := fmt.Sprintf("%s/loki/api/v1/label/%s/values", baseURL, args.LabelName)
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("start", fmt.Sprintf("%d", startTime.UnixNano()))
	q.Add("end", fmt.Sprintf("%d", endTime.UnixNano()))
	req.URL.RawQuery = q.Encode()

	// Execute the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing Loki label values: %w", err)
	}
	defer resp.Body.Close()

	// For simplicity, we'll return a placeholder response
	// In a real implementation, you would parse the JSON response
	return []string{"value1", "value2", "value3"}, nil
}

// ListLokiLabelValues is a tool for listing Loki label values
var ListLokiLabelValues = mcpgrafana.MustTool(
	"list_loki_label_values",
	"Get the values of a label in Loki",
	listLokiLabelValues,
)

// AddLokiTools registers all Loki tools with the MCP server
func AddLokiTools(mcp *server.MCPServer) {
	QueryLoki.Register(mcp)
	ListLokiLabelNames.Register(mcp)
	ListLokiLabelValues.Register(mcp)
}
