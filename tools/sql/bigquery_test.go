//go:build unit

package sql

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bigqueryTestRunner struct {
	path     string
	body     map[string]string
	response []byte
	err      error
}

func (r *bigqueryTestRunner) RunSQL(context.Context, string, string, string, interface{}, time.Time, time.Time, map[string]interface{}) ([]string, []map[string]interface{}, error) {
	panic("BigQuery metadata must never execute SQL, including on errors")
}
func (r *bigqueryTestRunner) ResourceRequest(_ context.Context, _, path string, body map[string]string) ([]byte, error) {
	r.path, r.body = path, body
	return r.response, r.err
}

func TestBigQueryRegistrationAndPayload(t *testing.T) {
	d, err := DialectFor(BigQueryDatasourceType)
	require.NoError(t, err)
	assert.Equal(t, 1, d.DefaultFormat())
	assert.Equal(t, map[string]interface{}{"location": "EU"}, d.ExtraQueryPayloadFields(QuerySQLParams{Location: "EU"})["connectionArgs"])
	assert.Equal(t, map[string]interface{}{"location": ""}, d.ExtraQueryPayloadFields(QuerySQLParams{})["connectionArgs"])
}

func TestBigQueryMacros(t *testing.T) {
	d := &bigqueryDialect{}
	from := time.Date(2026, 1, 1, 1, 0, 0, 0, time.FixedZone("test", 3600))
	to := from.Add(time.Hour)
	query := "$__from $__to $__interval $__interval_ms $__timeFilter(timestamp(event_time)) $__timeFrom() $__timeTo() $__timeGroup(event_time, '1m') $__to_date"
	want := "1767225600000 1767229200000 3s 3000 $__timeFilter(timestamp(event_time)) $__timeFrom() $__timeTo() $__timeGroup(event_time, '1m') $__to_date"
	assert.Equal(t, want, d.SubstituteMacros(query, from, to))
	assert.Equal(t, "1s 1000", d.SubstituteMacros("$__interval $__interval_ms", from, from))
}

func TestBigQueryLimits(t *testing.T) {
	d := &bigqueryDialect{}
	assert.Equal(t, "select 1 LIMIT 100", d.EnforceLimit("select 1", 0))
	assert.Equal(t, "select 1 LIMIT 5", d.EnforceLimit("select 1;", 5))
	assert.Equal(t, "select 1 LIMIT 1000", d.EnforceLimit("select 1", 2000))
	assert.Equal(t, "select 1 limit 2", d.EnforceLimit("select 1 limit 2", 0))
	assert.Equal(t, "select 1 LIMIT 1000", d.EnforceLimit("select 1 limit 2000", 0))
}

func TestBigQueryMetadata(t *testing.T) {
	ctx := context.Background()
	d := &bigqueryDialect{}
	t.Run("datasets", func(t *testing.T) {
		r := &bigqueryTestRunner{response: []byte(`["dataset_a","dataset_b"]`)}
		result, err := d.ListDatabases(ctx, r, BigQueryDatasourceType, ListSQLDatabasesParams{Database: "project-id"})
		require.NoError(t, err)
		assert.Equal(t, "/datasets", r.path)
		assert.Equal(t, map[string]string{"project": "project-id"}, r.body)
		assert.Equal(t, []SQLDatabaseInfo{{Database: "project-id", Schema: "dataset_a"}, {Database: "project-id", Schema: "dataset_b"}}, result.Databases)
	})
	t.Run("tables", func(t *testing.T) {
		r := &bigqueryTestRunner{response: []byte(`["facts","view"]`)}
		result, err := d.ListTables(ctx, r, BigQueryDatasourceType, ListSQLTablesParams{Database: "project-id", Schema: "dataset"})
		require.NoError(t, err)
		assert.Equal(t, "/tables", r.path)
		assert.Equal(t, map[string]string{"project": "project-id", "dataset": "dataset"}, r.body)
		assert.Equal(t, []SQLTableInfo{{Database: "project-id", Schema: "dataset", Name: "facts"}, {Database: "project-id", Schema: "dataset", Name: "view"}}, result.Tables)
	})
	t.Run("columns", func(t *testing.T) {
		r := &bigqueryTestRunner{response: []byte(`{"schema":[
			{"name":"actor","type":"RECORD","description":"Actors","repeated":true,"schema":[
				{"name":"name","type":"STRING","description":"Actor name"},
				{"name":"scores","type":"INTEGER","repeated":true}
			]},
			{"name":"success","type":"BOOLEAN"},
			{"name":"amount","type":"FLOAT"},
			{"name":"payload","type":"JSON"}
		]}`)}
		result, err := d.DescribeTable(ctx, r, BigQueryDatasourceType, DescribeSQLTableParams{Database: "project-id", Schema: "dataset", Table: "facts"})
		require.NoError(t, err)
		assert.Equal(t, "/dataset/table/schema", r.path)
		assert.Equal(t, map[string]string{"project": "project-id", "dataset": "dataset", "table": "facts"}, r.body)
		assert.Equal(t, []SQLColumnInfo{
			{Name: "actor", Type: "ARRAY<STRUCT<name STRING, scores ARRAY<INT64>>>", Comment: "Actors"},
			{Name: "actor.name", Type: "STRING", Comment: "Actor name"},
			{Name: "actor.scores", Type: "ARRAY<INT64>"},
			{Name: "success", Type: "BOOL"},
			{Name: "amount", Type: "FLOAT64"},
			{Name: "payload", Type: "JSON"},
		}, result.Columns)
	})
	t.Run("empty results are arrays", func(t *testing.T) {
		r := &bigqueryTestRunner{response: []byte(`[]`)}
		dbs, err := d.ListDatabases(ctx, r, "", ListSQLDatabasesParams{Database: "project-id"})
		require.NoError(t, err)
		assert.NotNil(t, dbs.Databases)
		tables, err := d.ListTables(ctx, r, "", ListSQLTablesParams{Database: "project-id", Schema: "dataset"})
		require.NoError(t, err)
		assert.NotNil(t, tables.Tables)
		r.response = []byte(`{"schema":[]}`)
		columns, err := d.DescribeTable(ctx, r, "", DescribeSQLTableParams{Database: "project-id", Schema: "dataset", Table: "facts"})
		require.NoError(t, err)
		assert.NotNil(t, columns.Columns)
	})
	t.Run("errors", func(t *testing.T) {
		r := &bigqueryTestRunner{err: errors.New("permission denied")}
		_, err := d.ListDatabases(ctx, r, "", ListSQLDatabasesParams{Database: "project-id"})
		require.ErrorContains(t, err, "permission denied")
		_, err = d.ListTables(ctx, r, "", ListSQLTablesParams{Database: "project-id", Schema: "dataset"})
		require.ErrorContains(t, err, "permission denied")
		_, err = d.DescribeTable(ctx, r, "", DescribeSQLTableParams{Database: "project-id", Schema: "dataset", Table: "facts"})
		require.ErrorContains(t, err, "permission denied")
		r = &bigqueryTestRunner{response: []byte(`{"unexpected":true}`)}
		_, err = d.ListDatabases(ctx, r, "", ListSQLDatabasesParams{Database: "project-id"})
		require.ErrorContains(t, err, "decoding")
		_, err = d.ListTables(ctx, r, "", ListSQLTablesParams{Database: "project-id", Schema: "dataset"})
		require.ErrorContains(t, err, "decoding")
		r.response = []byte(`{"schema":"invalid"}`)
		_, err = d.DescribeTable(ctx, r, "", DescribeSQLTableParams{Database: "project-id", Schema: "dataset", Table: "facts"})
		require.ErrorContains(t, err, "decoding")
	})
}

func TestBigQueryIdentifiers(t *testing.T) {
	d := &bigqueryDialect{}
	// Naming validity belongs to BigQuery; these are transport-safe JSON values.
	for _, name := range []string{"", "project-id", "dataset_name", "table_01", "table 01", "ग्राहक", "00_お客様", "étudiant-01", "e\u0301", "example.com:my-project", "project.dataset", "bad`name", "x'; drop table t;--", "x\\y", "x%2Fy", "x%00y"} {
		require.NoError(t, d.ValidateIdentifier(name, "identifier"), "%q", name)
	}
	for _, name := range []string{"/", "x/y", "../table", "x\x00y", "x\ty", "x\ny", "x\ry", "x\x7fy", "x\u0085y", "x\u009fy"} {
		require.ErrorContains(t, d.ValidateIdentifier(name, "identifier"), "invalid identifier", "%q", name)
	}
	ctx, r := context.Background(), &bigqueryTestRunner{}
	for _, params := range []ListSQLTablesParams{{}, {Database: "project"}, {Database: "bad/project", Schema: "dataset"}, {Database: "project", Schema: "bad/dataset"}, {Schema: "bad\ndataset"}} {
		_, err := d.ListTables(ctx, r, "", params)
		require.Error(t, err)
	}
	for _, table := range []string{"", "bad/table", "bad\ntable"} {
		_, err := d.DescribeTable(ctx, r, "", DescribeSQLTableParams{Database: "project", Schema: "dataset", Table: table})
		require.Error(t, err)
	}
	_, err := d.ListDatabases(ctx, r, "", ListSQLDatabasesParams{Database: "bad/project"})
	require.ErrorContains(t, err, "invalid database")
	assert.Empty(t, r.path, "invalid identifiers must fail before resource requests")
	r.response = []byte(`""`)
	_, err = d.ListDatabases(ctx, r, "", ListSQLDatabasesParams{})
	require.ErrorContains(t, err, "database (BigQuery project) is required")
	assert.Equal(t, "/defaultProjects", r.path)
}
