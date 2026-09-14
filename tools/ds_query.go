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

// dsQueryQueries pulls the query list out of an /api/ds/query payload,
// accepting both the []map[string]interface{} that the in-process builders use
// and the []interface{} shape a JSON round-trip produces. ok is false only when
// the payload has no inspectable query list, so the guard can fail closed rather
// than wave through traffic it cannot read.
func dsQueryQueries(payload map[string]interface{}) (queries []map[string]interface{}, ok bool) {
	switch qs := payload["queries"].(type) {
	case []map[string]interface{}:
		return qs, true
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(qs))
		for _, e := range qs {
			m, isMap := e.(map[string]interface{})
			if !isMap {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	default:
		return nil, false
	}
}

// dsQueryDatasourceUID reports the datasource UID a single /api/ds/query query
// targets, mirroring how Grafana's query service resolves it:
//
//   - a numeric legacy "datasourceId" cannot be mapped to a type here, so its
//     presence returns ok=false and the caller fails closed;
//   - "datasource" as a bare string is the UID (the pre-8.3 form Grafana still
//     honours);
//   - "datasource" as an object uses its "uid" field;
//   - an empty or absent UID returns ("", true), meaning "resolve the default".
//
// ok is false when a datasource reference is present but not understood, so an
// unrecognised shape fails closed instead of being treated as "no datasource"
// and waved through against the org default.
func dsQueryDatasourceUID(q map[string]interface{}) (uid string, ok bool) {
	// A legacy numeric datasourceId can name any datasource, including a log
	// one, and we cannot resolve it to a type cheaply. Refuse under enforcement.
	if _, hasLegacyID := q["datasourceId"]; hasLegacyID {
		return "", false
	}
	ds, present := q["datasource"]
	if !present {
		return "", true // no datasource named -> the default
	}
	switch v := ds.(type) {
	case string:
		return v, true // bare-string form: the string is the UID ("" -> default)
	case map[string]string:
		u, hasUID := v["uid"]
		if !hasUID {
			return "", false
		}
		return u, true
	case map[string]interface{}:
		u, hasUID := v["uid"]
		if !hasUID {
			return "", false
		}
		us, isStr := u.(string)
		if !isStr {
			return "", false
		}
		return us, true
	default:
		return "", false // unrecognised datasource shape
	}
}

// guardEnforcedLokiDSQuery fails closed when an /api/ds/query payload would
// reach a Loki (or VictoriaLogs) datasource while Loki label-matcher
// enforcement is active. The enforced Loki path never uses /api/ds/query — it
// goes through the datasource proxy (see loki.go) — so any log datasource
// arriving here is an unenforced route around --loki-enforced-matchers.
//
// The type is resolved from the UID and never read from the payload, whose
// declared type is caller-influenced (the run_panel_query bypass). Anything that
// cannot be positively resolved to a non-log datasource fails closed: an
// unreadable UID, an unrecognised datasource reference, a legacy numeric
// datasourceId, or a payload whose query list cannot be inspected. A query that
// names no UID — or the magic "default" UID — is checked against every
// datasource Grafana might resolve it to (a literal "default" datasource and the
// org default), refusing if any is a log datasource.
func guardEnforcedLokiDSQuery(ctx context.Context, payload map[string]interface{}) error {
	if len(enforcedMatchers(ctx)) == 0 {
		return nil
	}
	queries, ok := dsQueryQueries(payload)
	if !ok {
		return fmt.Errorf("loki label-matcher enforcement is active but the /api/ds/query payload could not be inspected for its target datasources")
	}
	defaultChecked := false
	checked := make(map[string]bool, len(queries))
	for _, q := range queries {
		uid, ok := dsQueryDatasourceUID(q)
		if !ok {
			return fmt.Errorf("loki label-matcher enforcement is active: refusing /api/ds/query with a datasource reference that could not be resolved to a type (unrecognised shape or legacy datasourceId)")
		}
		if uid == "" || uid == "default" {
			// No explicit datasource, or the magic "default" UID: refuse if any
			// datasource Grafana might resolve it to is a log datasource. Checked
			// once per payload.
			if defaultChecked {
				continue
			}
			defaultChecked = true
			isLog, resolvable, err := defaultTargetIsLogDatasource(ctx, uid)
			if err != nil {
				return fmt.Errorf("loki label-matcher enforcement is active: refusing /api/ds/query whose default datasource could not be verified: %w", err)
			}
			if !resolvable {
				return fmt.Errorf("loki label-matcher enforcement is active: refusing /api/ds/query because no default datasource could be resolved to verify its type")
			}
			if isLog {
				return fmt.Errorf("loki label-matcher enforcement is active: refusing to query the default log datasource via /api/ds/query, which bypasses --loki-enforced-matchers; use the Loki query tools instead")
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
