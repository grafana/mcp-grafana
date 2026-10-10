//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	sqldialect "github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardClickHouseMacroParity(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, tc := range []struct {
			name, start, end, interval, intervalMs string
		}{
			{"hour", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", "3s", "3000"},
			{"subsecond", "2024-01-01T00:00:00.100Z", "2024-01-01T00:00:00.900Z", "1s", "1000"},
			{"whole second boundary", "2024-01-01T00:00:00.900Z", "2024-01-01T00:33:20.100Z", "2s", "2000"},
			{"long exact seconds", "2024-01-01T00:00:00Z", "2024-01-08T00:00:00Z", "604s", "604000"},
		} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, tc.name), func(t *testing.T) {
				from, err := time.Parse(time.RFC3339Nano, tc.start)
				require.NoError(t, err)
				to, err := time.Parse(time.RFC3339Nano, tc.end)
				require.NoError(t, err)
				macros := []string{"__from", "__to", "__range_ms", "__range_s", "__range", "__rate_interval", "__interval_ms", "__interval"}
				values := []string{fmt.Sprint(from.UnixMilli()), fmt.Sprint(to.UnixMilli()), fmt.Sprint(to.Sub(from).Milliseconds()), fmt.Sprint(int64(to.Sub(from).Seconds())), formatPrometheusDuration(to.Sub(from)), "1m", tc.intervalMs, tc.interval}
				var fragments, expected []string
				for i, macro := range macros {
					fragments = append(fragments, "$"+macro, "${"+macro+"}")
					expected = append(expected, values[i], values[i])
				}
				query := "SELECT 1 /* " + strings.Join(fragments, " ") + " */ WHERE $__timeFilter(ts) LIMIT 1"
				want := "SELECT 1 /* " + strings.Join(expected, " ") + fmt.Sprintf(" */ WHERE ts >= toDateTime(%d) AND ts <= toDateTime(%d) LIMIT 1", from.Unix(), to.Unix())
				calls := 0
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/datasources/uid/clickhouse-uid" {
						_, _ = w.Write([]byte(`{"uid":"clickhouse-uid","type":"grafana-clickhouse-datasource"}`))
						return
					}
					require.Equal(t, "/api/ds/query", r.URL.Path)
					calls++
					var payload map[string]interface{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					target := safeArray(payload, "queries")[0].(map[string]interface{})
					assert.Equal(t, want, target["rawSql"], "submitted SQL must match inspection")
					frames := data.Frames{data.NewFrame("", data.NewField("value", nil, []int64{1}))}
					_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: frames}}})
				}))
				t.Cleanup(ts.Close)
				ctx := enforceTestCtx(ts, false)
				db := preparationFixture(v2, map[string]interface{}{"name": "unused", "type": "custom"}, query)
				setPreparationDatasource(db, v2, "clickhouse-uid", sqldialect.ClickHouseDatasourceType)
				args := DashboardPanelQueriesParams{Start: tc.start, End: tc.end}
				inspected, err := inspectPreparationFixture(ctx, db, v2, args)
				require.NoError(t, err)
				require.Len(t, inspected, 1)
				require.Empty(t, inspected[0].Warnings)
				assert.Equal(t, want, inspected[0].ProcessedQuery)
				result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1, Start: tc.start, End: tc.end})
				require.NoError(t, err)
				assert.Equal(t, want, result.Query)
				assert.Equal(t, 1, calls)
				dialect, err := sqldialect.DialectFor(sqldialect.ClickHouseDatasourceType)
				require.NoError(t, err)
				assert.Equal(t, tc.interval+" "+tc.intervalMs, dialect.SubstituteMacros("$__interval $__interval_ms", from, to))
				assert.Equal(t, want, dialect.SubstituteMacros(want, from, to), "execution must not change prepared macros")
			})
		}
	}
}
