package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// dsQueryPayload builds the standard /api/ds/query request envelope.
// Each query map should contain datasource-specific fields (refId, datasource, etc.).
func dsQueryPayload(from, to time.Time, queries ...map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"queries": queries,
		"from":    strconv.FormatInt(from.UnixMilli(), 10),
		"to":      strconv.FormatInt(to.UnixMilli(), 10),
	}
}

// doDSQuery posts a payload to Grafana's /api/ds/query endpoint and decodes
// the response into the SDK's QueryDataResponse type.
func doDSQuery(ctx context.Context, client *http.Client, baseURL string, payload map[string]interface{}) (*backend.QueryDataResponse, error) {
	return doDSQueryWithLimit(ctx, client, baseURL, payload, defaultResponseLimitBytes)
}

// doDSQueryWithLimit is like doDSQuery but allows overriding the response size limit.
func doDSQueryWithLimit(ctx context.Context, client *http.Client, baseURL string, payload map[string]interface{}, responseLimit int64) (*backend.QueryDataResponse, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("query returned status %d: %s", resp.StatusCode, string(errBody))
	}

	body, err := readResponseBody(resp.Body, responseLimit)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var queryResp backend.QueryDataResponse
	if err := json.Unmarshal(body, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return &queryResp, nil
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// newDSQueryHTTPClient builds an *http.Client suitable for calling Grafana's
// /api/ds/query endpoint, using the Grafana config from the context.
func newDSQueryHTTPClient(ctx context.Context) (*http.Client, string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := trimTrailingSlash(cfg.URL)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create transport: %w", err)
	}

	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: refuseRedirect}, baseURL, nil
}

// framesToTabularRows converts SDK data frames into row-oriented maps — the
// common format returned by ClickHouse, Snowflake, and Athena tools.
func framesToTabularRows(resp *backend.QueryDataResponse) ([]string, []map[string]interface{}, error) {
	columns := []string{}
	rows := []map[string]interface{}{}

	for refID, r := range resp.Responses {
		if r.Error != nil {
			return nil, nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		for _, frame := range r.Frames {
			cols := make([]string, len(frame.Fields))
			for i, field := range frame.Fields {
				cols[i] = field.Name
			}
			columns = cols

			rowCount := frame.Rows()
			for i := 0; i < rowCount; i++ {
				row := make(map[string]interface{})
				for colIdx, colName := range cols {
					row[colName] = frame.At(colIdx, i)
				}
				rows = append(rows, row)
			}
		}
	}

	return columns, rows, nil
}

// toInt64FromRow extracts an int64 from a row value that may be any of the
// numeric types (or their pointer variants) the SDK's data.Field can hold.
func toInt64FromRow(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case *float64:
		if n != nil {
			return int64(*n)
		}
	case float32:
		return int64(n)
	case *float32:
		if n != nil {
			return int64(*n)
		}
	case int64:
		return n
	case *int64:
		if n != nil {
			return *n
		}
	case int32:
		return int64(n)
	case *int32:
		if n != nil {
			return int64(*n)
		}
	case int16:
		return int64(n)
	case *int16:
		if n != nil {
			return int64(*n)
		}
	case int8:
		return int64(n)
	case *int8:
		if n != nil {
			return int64(*n)
		}
	case uint64:
		return int64(n)
	case *uint64:
		if n != nil {
			return int64(*n)
		}
	case uint32:
		return int64(n)
	case *uint32:
		if n != nil {
			return int64(*n)
		}
	case uint16:
		return int64(n)
	case *uint16:
		if n != nil {
			return int64(*n)
		}
	case uint8:
		return int64(n)
	case *uint8:
		if n != nil {
			return int64(*n)
		}
	}
	return 0
}

// toStringFromRow extracts a string from a row value that may be string or
// *string depending on the SDK field type.
func toStringFromRow(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case *string:
		if s != nil {
			return *s
		}
	}
	return ""
}
