package tools_test

import (
	"testing"

	"github.com/grafana/mcp-grafana/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryDataSourceToolDefinition(t *testing.T) {
	tool := tools.QueryDataSource
	require.NotNil(t, tool)
	require.Equal(t, "query_datasource", tool.Tool.Name)
	require.Equal(t, "Query a datasource using the /api/ds/query endpoint. This is a general-purpose tool for querying any type of datasource supported by Grafana, including Prometheus, Graphite, Loki, InfluxDB, MySQL, PostgreSQL, and others. The tool supports multiple queries in a single request and flexible time range specifications.", tool.Tool.Description)
	require.NotNil(t, tool.Tool.InputSchema)
	require.NotNil(t, tool.Handler)
}

func TestDataSourceQueryStructures(t *testing.T) {
	t.Run("DataSourceQuery creation", func(t *testing.T) {
		query := tools.DataSourceQuery{
			RefID: "A",
			Datasource: tools.DataSourceRef{
				Type: "prometheus",
				UID:  "prometheus-uid",
				Name: "Prometheus",
			},
			Expr:          "up",
			QueryType:     "instant",
			IntervalMs:    15000,
			MaxDataPoints: 100,
			Format:        "time_series",
			Hide:          false,
			Exemplar:      true,
		}

		assert.Equal(t, "A", query.RefID)
		assert.Equal(t, "prometheus", query.Datasource.Type)
		assert.Equal(t, "prometheus-uid", query.Datasource.UID)
		assert.Equal(t, "Prometheus", query.Datasource.Name)
		assert.Equal(t, "up", query.Expr)
		assert.Equal(t, "instant", query.QueryType)
		assert.Equal(t, 15000, query.IntervalMs)
		assert.Equal(t, 100, query.MaxDataPoints)
		assert.Equal(t, "time_series", query.Format)
		assert.False(t, query.Hide)
		assert.True(t, query.Exemplar)
	})

	t.Run("DataSourceQuery with SQL fields", func(t *testing.T) {
		query := tools.DataSourceQuery{
			RefID: "B",
			Datasource: tools.DataSourceRef{
				Type: "mysql",
				UID:  "mysql-uid",
				Name: "MySQL",
			},
			RawSQL:   "SELECT * FROM users WHERE active = 1",
			Database: "myapp",
			Table:    "users",
		}

		assert.Equal(t, "B", query.RefID)
		assert.Equal(t, "mysql", query.Datasource.Type)
		assert.Equal(t, "mysql-uid", query.Datasource.UID)
		assert.Equal(t, "MySQL", query.Datasource.Name)
		assert.Equal(t, "SELECT * FROM users WHERE active = 1", query.RawSQL)
		assert.Equal(t, "myapp", query.Database)
		assert.Equal(t, "users", query.Table)
	})

	t.Run("DataSourceQuery with Target field", func(t *testing.T) {
		query := tools.DataSourceQuery{
			RefID: "C",
			Datasource: tools.DataSourceRef{
				Type: "graphite",
				UID:  "graphite-uid",
			},
			Target: "servers.web01.cpu.usage",
		}

		assert.Equal(t, "C", query.RefID)
		assert.Equal(t, "graphite", query.Datasource.Type)
		assert.Equal(t, "graphite-uid", query.Datasource.UID)
		assert.Equal(t, "servers.web01.cpu.usage", query.Target)
	})

	t.Run("DataSourceRef creation", func(t *testing.T) {
		ref := tools.DataSourceRef{
			Type: "loki",
			UID:  "loki-uid",
			Name: "Loki",
		}

		assert.Equal(t, "loki", ref.Type)
		assert.Equal(t, "loki-uid", ref.UID)
		assert.Equal(t, "Loki", ref.Name)
	})

	t.Run("DataSourceRef minimal creation", func(t *testing.T) {
		ref := tools.DataSourceRef{
			Type: "tempo",
			UID:  "tempo-uid",
		}

		assert.Equal(t, "tempo", ref.Type)
		assert.Equal(t, "tempo-uid", ref.UID)
		assert.Empty(t, ref.Name)
	})

	t.Run("QueryDataSourceParams creation", func(t *testing.T) {
		params := tools.QueryDataSourceParams{
			Queries: []tools.DataSourceQuery{
				{
					RefID: "A",
					Datasource: tools.DataSourceRef{
						Type: "prometheus",
						UID:  "prometheus-uid",
					},
					Expr: "up",
				},
			},
			From: "now-1h",
			To:   "now",
		}

		assert.Len(t, params.Queries, 1)
		assert.Equal(t, "A", params.Queries[0].RefID)
		assert.Equal(t, "now-1h", params.From)
		assert.Equal(t, "now", params.To)
	})

	t.Run("QueryDataSourceParams with multiple queries", func(t *testing.T) {
		params := tools.QueryDataSourceParams{
			Queries: []tools.DataSourceQuery{
				{
					RefID: "A",
					Datasource: tools.DataSourceRef{
						Type: "prometheus",
						UID:  "prometheus-uid",
					},
					Expr: "up",
				},
				{
					RefID: "B",
					Datasource: tools.DataSourceRef{
						Type: "loki",
						UID:  "loki-uid",
					},
					Expr: "{job=\"grafana\"}",
				},
			},
			From: "2024-01-01T00:00:00Z",
			To:   "2024-01-01T01:00:00Z",
		}

		assert.Len(t, params.Queries, 2)
		assert.Equal(t, "A", params.Queries[0].RefID)
		assert.Equal(t, "prometheus", params.Queries[0].Datasource.Type)
		assert.Equal(t, "B", params.Queries[1].RefID)
		assert.Equal(t, "loki", params.Queries[1].Datasource.Type)
		assert.Equal(t, "2024-01-01T00:00:00Z", params.From)
		assert.Equal(t, "2024-01-01T01:00:00Z", params.To)
	})
}

func TestDataSourceQueryValidation(t *testing.T) {
	t.Run("valid prometheus query", func(t *testing.T) {
		query := tools.DataSourceQuery{
			RefID: "A",
			Datasource: tools.DataSourceRef{
				Type: "prometheus",
				UID:  "prometheus-uid",
			},
			Expr:      "up",
			QueryType: "instant",
		}

		// Basic validation - required fields are present
		assert.NotEmpty(t, query.RefID)
		assert.NotEmpty(t, query.Datasource.Type)
		assert.NotEmpty(t, query.Datasource.UID)
		assert.NotEmpty(t, query.Expr)
	})
}

func TestQueryDataSourceParamsValidation(t *testing.T) {
	t.Run("valid params with relative time", func(t *testing.T) {
		params := tools.QueryDataSourceParams{
			Queries: []tools.DataSourceQuery{
				{
					RefID: "A",
					Datasource: tools.DataSourceRef{
						Type: "prometheus",
						UID:  "prometheus-uid",
					},
					Expr: "up",
				},
			},
			From: "now-1h",
			To:   "now",
		}

		// Basic validation
		assert.NotEmpty(t, params.Queries)
		assert.NotEmpty(t, params.From)
		assert.NotEmpty(t, params.To)
		assert.Len(t, params.Queries, 1)
	})

	t.Run("valid params with absolute time", func(t *testing.T) {
		params := tools.QueryDataSourceParams{
			Queries: []tools.DataSourceQuery{
				{
					RefID: "A",
					Datasource: tools.DataSourceRef{
						Type: "prometheus",
						UID:  "prometheus-uid",
					},
					Expr: "up",
				},
			},
			From: "2024-01-01T00:00:00Z",
			To:   "2024-01-01T01:00:00Z",
		}

		// Basic validation
		assert.NotEmpty(t, params.Queries)
		assert.NotEmpty(t, params.From)
		assert.NotEmpty(t, params.To)
		assert.Contains(t, params.From, "2024-01-01")
		assert.Contains(t, params.To, "2024-01-01")
	})
}
