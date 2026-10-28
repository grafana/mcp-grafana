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
		got := substituteQueryTemplateVariableValues("select "+reference, templateVariableValues{"x": {"O'Reilly"}}, sql.BigQueryDatasourceType)
		assert.Equal(t, `select "O'Reilly"`, got)
	}
	for _, datasourceType := range []string{"", "postgres", "mssql"} {
		got := substituteQueryTemplateVariableValues("select ${x:sqlstring}", templateVariableValues{"x": {"O'Reilly"}}, datasourceType)
		assert.Equal(t, "select 'O''Reilly'", got, "unknown and non-BigQuery types keep the existing formatter")
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

func TestBigQueryMetadataProjectFallback(t *testing.T) {
	for _, tool := range []struct{ name, path, response string }{
		{"datasets", "/datasets", `["dataset"]`},
		{"tables", "/tables", `["facts"]`},
		{"columns", "/dataset/table/schema", `{"schema":[]}`},
	} {
		for _, tc := range []struct {
			name, database, configuredProject, defaultResponse, wantProject, wantError string
			defaultStatus, metadataStatus                                              int
		}{
			{name: "explicit overrides configured", database: "explicit-project", configuredProject: "configured-project", wantProject: "explicit-project"},
			{name: "configured skips fallback", configuredProject: "configured-project", wantProject: "configured-project"},
			{name: "explicit skips fallback", database: "explicit-project", wantProject: "explicit-project"},
			{name: "plugin fallback", defaultResponse: `"inferred-project"`, wantProject: "inferred-project"},
			{name: "explicit domain-scoped project", database: "example.com:my-project", wantProject: "example.com:my-project"},
			{name: "configured domain-scoped project", configuredProject: "example.com:my-project", wantProject: "example.com:my-project"},
			{name: "inferred domain-scoped project", defaultResponse: `"example.com:my-project"`, wantProject: "example.com:my-project"},
			{name: "empty project", defaultResponse: `""`, wantError: "database (BigQuery project) is required"},
			{name: "null project", defaultResponse: `null`, wantError: "database (BigQuery project) is required"},
			{name: "wrong response shape", defaultResponse: `["project"]`, wantError: "decoding BigQuery default project"},
			{name: "malformed response", defaultResponse: `{`, wantError: "decoding BigQuery default project"},
			{name: "invalid inferred project", defaultResponse: `"bad/project"`, wantError: "invalid database"},
			{name: "forbidden fallback", defaultStatus: http.StatusForbidden, wantError: "resolving BigQuery default project: resource /defaultProjects returned status 403"},
			{name: "missing endpoint", defaultStatus: http.StatusNotFound, wantError: "resolving BigQuery default project: resource /defaultProjects returned status 404"},
			{name: "unavailable fallback", defaultStatus: http.StatusServiceUnavailable, wantError: "resolving BigQuery default project: resource /defaultProjects returned status 503"},
			{name: "metadata error after fallback", defaultResponse: `"inferred-project"`, wantProject: "inferred-project", metadataStatus: http.StatusForbidden, wantError: "returned status 403"},
		} {
			t.Run(tool.name+"/"+tc.name, func(t *testing.T) {
				var resourcePaths []string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/datasources/uid/bq":
						_ = json.NewEncoder(w).Encode(map[string]interface{}{
							"uid": "bq", "type": sql.BigQueryDatasourceType,
							"jsonData": map[string]string{"authenticationType": "gce", "defaultProject": tc.configuredProject},
						})
					case "/api/datasources/uid/bq/resources/defaultProjects":
						resourcePaths = append(resourcePaths, "/defaultProjects")
						assert.Equal(t, http.MethodPost, r.Method)
						var body map[string]string
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						assert.Empty(t, body)
						if tc.defaultStatus != 0 {
							w.WriteHeader(tc.defaultStatus)
						}
						_, _ = w.Write([]byte(tc.defaultResponse))
					case "/api/datasources/uid/bq/resources" + tool.path:
						resourcePaths = append(resourcePaths, tool.path)
						var body map[string]string
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						wantBody := map[string]string{"project": tc.wantProject}
						if tool.name != "datasets" {
							wantBody["dataset"] = "dataset"
						}
						if tool.name == "columns" {
							wantBody["table"] = "facts"
						}
						assert.Equal(t, wantBody, body)
						if tc.metadataStatus != 0 {
							w.WriteHeader(tc.metadataStatus)
						}
						_, _ = w.Write([]byte(tool.response))
					default:
						t.Errorf("unexpected request: %s; metadata discovery must not execute SQL", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(ts.Close)
				ctx := enforceTestCtx(ts, false)
				var err error
				switch tool.name {
				case "datasets":
					var result *sql.ListSQLDatabasesResult
					result, err = listSQLDatabasesHandler(ctx, sql.ListSQLDatabasesParams{DatasourceUID: "bq", Database: tc.database})
					if err == nil {
						require.Len(t, result.Databases, 1)
						assert.Equal(t, tc.wantProject, result.Databases[0].Database)
					}
				case "tables":
					var result *sql.ListSQLTablesResult
					result, err = listSQLTablesHandler(ctx, sql.ListSQLTablesParams{DatasourceUID: "bq", Database: tc.database, Schema: "dataset"})
					if err == nil {
						require.Len(t, result.Tables, 1)
						assert.Equal(t, tc.wantProject, result.Tables[0].Database)
					}
				case "columns":
					_, err = describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{DatasourceUID: "bq", Database: tc.database, Schema: "dataset", Table: "facts"})
				}
				if tc.wantError != "" {
					require.ErrorContains(t, err, tc.wantError)
				} else {
					require.NoError(t, err)
				}
				var wantPaths []string
				if tc.database == "" && tc.configuredProject == "" {
					wantPaths = append(wantPaths, "/defaultProjects")
				}
				if tc.wantProject != "" {
					wantPaths = append(wantPaths, tool.path)
				}
				assert.Equal(t, wantPaths, resourcePaths)
			})
		}
	}
}

func TestBigQueryMetadataIdentifierRoundTrip(t *testing.T) {
	const project = "example.com:my-project"
	// Include Unicode, decomposed marks, and punctuation to catch normalization
	// or SQL escaping of identifiers that must remain plain JSON values.
	names := []string{"table 01", "ग्राहक", "00_お客様", "étudiant-01", "e\u0301", "x%2Fy", "x'\"`;--\\y"}
	var describedNames []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/bq":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"uid": "bq", "type": sql.BigQueryDatasourceType,
				"jsonData": map[string]string{"defaultProject": project},
			})
		case "/api/datasources/uid/bq/resources/tables":
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]string{"project": project, "dataset": "dataset"}, body)
			_ = json.NewEncoder(w).Encode(names)
		case "/api/datasources/uid/bq/resources/dataset/table/schema":
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, project, body["project"])
			assert.Equal(t, "dataset", body["dataset"])
			describedNames = append(describedNames, body["table"])
			_, _ = w.Write([]byte(`{"schema":[{"name":"id","type":"STRING"}]}`))
		default:
			t.Errorf("unexpected request: %s; metadata discovery must not execute SQL", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	ctx := enforceTestCtx(ts, false)
	tables, err := listSQLTablesHandler(ctx, sql.ListSQLTablesParams{DatasourceUID: "bq", Schema: "dataset"})
	require.NoError(t, err)
	require.Len(t, tables.Tables, len(names))
	for i, table := range tables.Tables {
		assert.Equal(t, names[i], table.Name)
		assert.Equal(t, project, table.Database)
		assert.Equal(t, "dataset", table.Schema)
		columns, err := describeSQLTableHandler(ctx, sql.DescribeSQLTableParams{
			DatasourceUID: "bq", Database: table.Database, Schema: table.Schema, Table: table.Name,
		})
		require.NoError(t, err)
		assert.Equal(t, []sql.SQLColumnInfo{{Name: "id", Type: "STRING"}}, columns.Columns)
	}
	assert.Equal(t, names, describedNames, "discovered names must reach the schema API unchanged")
}
