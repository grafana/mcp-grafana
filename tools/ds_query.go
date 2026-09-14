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

// lokiLikeDatasourceType reports whether a resolved datasource type is a log
// datasource whose reads must go through the enforced Loki path.
func lokiLikeDatasourceType(t string) bool {
	return t == "loki" || t == victoriaLogsDatasourceType
}

// dsQueryDatasourceUID extracts the datasource UID from one /api/ds/query query
// object, tolerating both the map[string]string and map[string]interface{}
// shapes the various tools build.
func dsQueryDatasourceUID(q map[string]interface{}) string {
	switch ds := q["datasource"].(type) {
	case map[string]string:
		return ds["uid"]
	case map[string]interface{}:
		if uid, ok := ds["uid"].(string); ok {
			return uid
		}
	}
	return ""
}

// guardEnforcedLokiDSQuery fails closed when an /api/ds/query payload would
// reach a Loki (or VictoriaLogs) datasource while Loki label-matcher
// enforcement is active. The enforced Loki path never uses /api/ds/query — it
// goes through the datasource proxy (see loki.go) — so any log datasource
// arriving here is an unenforced route around --loki-enforced-matchers.
//
// The type is resolved from the UID and never read from the payload, whose
// declared type is caller-influenced (the run_panel_query bypass). A UID whose
// type cannot be resolved fails closed rather than being forwarded. A query that
// names no UID — or Grafana's magic "default" UID — resolves against the org
// default datasource, so its type is checked the same way; an unresolvable
// default also fails closed.
func guardEnforcedLokiDSQuery(ctx context.Context, payload map[string]interface{}) error {
	if len(enforcedMatchers(ctx)) == 0 {
		return nil
	}
	queries, ok := payload["queries"].([]map[string]interface{})
	if !ok {
		return fmt.Errorf("loki label-matcher enforcement is active but the /api/ds/query payload could not be inspected for its target datasources")
	}
	defaultChecked := false
	checked := make(map[string]bool, len(queries))
	for _, q := range queries {
		uid := dsQueryDatasourceUID(q)
		if uid == "" || uid == "default" {
			// No explicit datasource, or Grafana's magic "default" UID: the
			// query resolves against the org default. Check that default's type
			// once.
			if defaultChecked {
				continue
			}
			defaultChecked = true
			dsType, err := defaultDatasourceType(ctx)
			if err != nil {
				return fmt.Errorf("loki label-matcher enforcement is active: refusing /api/ds/query whose default datasource could not be verified: %w", err)
			}
			if lokiLikeDatasourceType(dsType) {
				return fmt.Errorf("loki label-matcher enforcement is active: refusing to query the default log datasource (type %q) via /api/ds/query, which bypasses --loki-enforced-matchers; use the Loki query tools instead", dsType)
			}
			continue
		}
		if checked[uid] {
			continue
		}
		checked[uid] = true
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
		if err != nil {
			return fmt.Errorf("loki label-matcher enforcement is active: refusing /api/ds/query for datasource %q whose type could not be verified: %w", uid, err)
		}
		if lokiLikeDatasourceType(ds.Type) {
			return fmt.Errorf("loki label-matcher enforcement is active: refusing to query log datasource %q (type %q) via /api/ds/query, which bypasses --loki-enforced-matchers; use the Loki query tools instead", uid, ds.Type)
		}
	}
	return nil
}

// doDSQuery posts a payload to Grafana's /api/ds/query endpoint and decodes
// the response into the SDK's QueryDataResponse type.
func doDSQuery(ctx context.Context, client *http.Client, baseURL string, payload map[string]interface{}) (*backend.QueryDataResponse, error) {
	return doDSQueryWithLimit(ctx, client, baseURL, payload, defaultResponseLimitBytes)
}

// doDSQueryWithLimit is like doDSQuery but allows overriding the response size limit.
func doDSQueryWithLimit(ctx context.Context, client *http.Client, baseURL string, payload map[string]interface{}, responseLimit int64) (*backend.QueryDataResponse, error) {
	if err := guardEnforcedLokiDSQuery(ctx, payload); err != nil {
		return nil, err
	}

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
