//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBigQueryVariableFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, format, want string
		values             []string
	}{
		{"quotes", "sqlstring", `"O'Reilly\""`, []string{`O'Reilly"`}},
		{"backslash and newline", "sqlstring", `"a\\b\n"`, []string{"a\\b\n"}},
		{"multiple", "sqlstring", `"east","west"`, []string{"east", "west"}},
		{"empty", "sqlstring", "", nil},
		{"fallback", "unknown", "{east,west}", []string{"east", "west"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatBigQueryTemplateVariable(tc.values, tc.format))
		})
	}
	for _, reference := range []string{"${x:sqlstring}", "[[x:sqlstring]]"} {
		got := substituteTemplateVariableValuesWithFormatter("select "+reference, templateVariableValues{"x": {"O'Reilly"}}, formatBigQueryTemplateVariable)
		assert.Equal(t, `select "O'Reilly"`, got)
	}
}

func TestFillBigQueryDefaults(t *testing.T) {
	for _, tc := range []struct{ name, dsType, database, location, wantDB, wantLocation string }{
		{"defaults", sql.BigQueryDatasourceType, "", "", "default-project", "US"},
		{"overrides", sql.BigQueryDatasourceType, "other-project", "EU", "other-project", "EU"},
		{"unrelated dialect", "postgres", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := &models.DataSource{Type: tc.dsType, JSONData: map[string]interface{}{"defaultProject": "default-project", "processingLocation": "US"}}
			database, location := tc.database, tc.location
			fillBigQueryDefaults(ds, &database, &location)
			assert.Equal(t, tc.wantDB, database)
			assert.Equal(t, tc.wantLocation, location)
		})
	}
}

func TestBigQueryHandlerDefaults(t *testing.T) {
	var submitted map[string]interface{}
	var resourceBody map[string]string
	allowSQL := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/bq":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": "bq", "type": sql.BigQueryDatasourceType, "jsonData": map[string]string{"defaultProject": "default-project", "processingLocation": "US"}})
		case "/api/datasources/uid/bq/resources/datasets":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&resourceBody))
			_, _ = w.Write([]byte(`["dataset"]`))
		case "/api/datasources/uid/bq/resources/tables":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&resourceBody))
			_, _ = w.Write([]byte(`["facts"]`))
		case "/api/datasources/uid/bq/resources/dataset/table/schema":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&resourceBody))
			_, _ = w.Write([]byte(`{"schema":[{"name":"id","type":"INTEGER","description":"Identifier"}]}`))
		case "/api/ds/query":
			assert.True(t, allowSQL, "metadata handlers must not submit query jobs")
			var payload struct {
				Queries []map[string]interface{} `json:"queries"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Len(t, payload.Queries, 1)
			submitted = payload.Queries[0]
			frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	ctx := enforceTestCtx(ts, false)

	_, err := listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{DatasourceUID: "bq"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"project": "default-project"}, resourceBody)
	_, err = listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{DatasourceUID: "bq", Database: "other-project"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"project": "other-project"}, resourceBody)

	_, err = listSQLTablesHandler(ctx, sql.ListSQLTablesParams{DatasourceUID: "bq", Schema: "dataset"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"project": "default-project", "dataset": "dataset"}, resourceBody)
	_, err = describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{DatasourceUID: "bq", Database: "other-project", Schema: "dataset", Table: "facts"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"project": "other-project", "dataset": "dataset", "table": "facts"}, resourceBody)
	assert.Nil(t, submitted, "metadata discovery must not execute SQL")

	allowSQL = true
	for _, location := range []string{"", "EU"} {
		result, err := querySQLHandler(ctx, sql.QuerySQLParams{DatasourceUID: "bq", Query: "select ${n}", Variables: map[string]string{"n": "1"}, Location: location})
		require.NoError(t, err)
		want := location
		if want == "" {
			want = "US"
		}
		assert.Equal(t, map[string]interface{}{"location": want}, submitted["connectionArgs"])
		assert.Equal(t, float64(1), submitted["format"])
		assert.Equal(t, "select 1 LIMIT 100", result.ProcessedQuery)
		assert.Equal(t, 1, result.RowCount)
	}
}
