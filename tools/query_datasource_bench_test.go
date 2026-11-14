//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// dsSeriesBody is a compact /api/ds/query response with one frame per series, each of points rows.
func dsSeriesBody(refIDs []string, series, points int) []byte {
	var b strings.Builder
	b.WriteString(`{"results":{`)
	for r, refID := range refIDs {
		if r > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%q:{"status":200,"frames":[`, refID)
		for s := range series {
			if s > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"schema":{"fields":[{"name":"Time","type":"time"},{"name":"Value","type":"number"}]},"data":{"values":[[`)
			for i := range points {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, "%d", 1790352577294+int64(i)*60000)
			}
			b.WriteString("],[")
			for i := range points {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString("0.0123456")
			}
			b.WriteString("]]}}")
		}
		b.WriteString("]}")
	}
	b.WriteString("}}")
	return []byte(b.String())
}

func benchmarkQueryDatasource(b *testing.B, body []byte, arguments string, wantError bool) {
	b.Helper()
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	b.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: grafana.URL})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "query_datasource", Arguments: json.RawMessage(arguments)}}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		result, err := QueryDatasource.Handler(ctx, request)
		if err != nil || result.IsError != wantError {
			b.Fatalf("err %v, result %+v", err, result)
		}
	}
}

// A response near the budget: 20 series of 2000 points, about 960 KB.
func BenchmarkQueryDatasourceNearBudget(b *testing.B) {
	benchmarkQueryDatasource(b, dsSeriesBody([]string{"A"}, 20, 2000),
		`{"datasourceUid":"prom","queries":[{"refId":"A"}]}`, false)
}

// A response twice the budget gives too-large without reading or decoding all of it.
func BenchmarkQueryDatasourcePastBudget(b *testing.B) {
	benchmarkQueryDatasource(b, dsSeriesBody([]string{"A", "B"}, 20, 2000),
		`{"datasourceUid":"prom","queries":[{"refId":"A"},{"refId":"B"}]}`, true)
}
