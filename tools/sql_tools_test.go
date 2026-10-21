//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuerySQLVariableFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, reference, want string
	}{
		{name: "bare", reference: "$x", want: "O'Reilly"},
		{name: "braced", reference: "${x}", want: "O'Reilly"},
		{name: "sqlstring", reference: "${x:sqlstring}", want: "'O''Reilly'"},
		{name: "bracketed sqlstring", reference: "[[x:sqlstring]]", want: "'O''Reilly'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submitted string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/postgres-uid":
					_ = json.NewEncoder(w).Encode(map[string]string{"uid": "postgres-uid", "type": "postgres"})
				case "/api/ds/query":
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					submitted = safeString(safeArray(payload, "queries")[0].(map[string]interface{}), "rawSql")
					frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(ts.Close)
			result, err := querySQLHandler(enforceTestCtx(ts, false), sql.QuerySQLParams{
				DatasourceUID: "postgres-uid",
				Query:         "SELECT " + tc.reference + " LIMIT 1",
				Variables:     map[string]string{"x": "O'Reilly"},
			})
			require.NoError(t, err)
			assert.Equal(t, "SELECT "+tc.want+" LIMIT 1", result.ProcessedQuery)
			assert.Equal(t, result.ProcessedQuery, submitted)
		})
	}
}
