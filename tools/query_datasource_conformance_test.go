//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Source test of the viz contract (fixtures.8). The sets are copies of grafana/viz-contract/fixtures at the
// commit bb6adf3 (see viz_contract.go).

func readFixtureJSON(t *testing.T, dir, name string, into any) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, into))
	return true
}

func TestQueryDatasourceConformance(t *testing.T) {
	dirs, err := filepath.Glob("testdata/viz-contract/*")
	require.NoError(t, err)
	require.NotEmpty(t, dirs)
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var args json.RawMessage
			var meta struct{ Now, CapturedAt int64 }
			var response struct {
				Status int             `json:"status"`
				Body   json.RawMessage `json:"body"`
			}
			var want struct {
				IsError bool              `json:"isError"`
				Content []mcp.TextContent `json:"content"`
			}
			var wantResult map[string]any
			require.True(t, readFixtureJSON(t, dir, "args.json", &args))
			require.True(t, readFixtureJSON(t, dir, "meta.json", &meta))
			require.True(t, readFixtureJSON(t, dir, "response.json", &response))
			require.True(t, readFixtureJSON(t, dir, "result.json", &wantResult))
			require.True(t, readFixtureJSON(t, dir, "result.json", &want))
			var wantRequest map[string]any
			hasRequest := readFixtureJSON(t, dir, "request.json", &wantRequest)

			// The clock gives now of meta.json for the range, and capturedAt of meta.json for the response.
			times := []int64{meta.Now, meta.CapturedAt}
			dsNow = func() time.Time {
				defer func() { times = times[min(1, len(times)-1):] }()
				return time.UnixMilli(times[0])
			}
			t.Cleanup(func() { dsNow = time.Now })

			var posted map[string]any
			grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				require.NoError(t, json.Unmarshal(body, &posted))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.Status)
				_, _ = w.Write(response.Body)
			}))
			t.Cleanup(grafana.Close)
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: grafana.URL})
			got, err := QueryDatasource.Handler(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
				Name: "query_datasource", Arguments: args,
			}})

			if response.Status != http.StatusOK {
				// tool.16: one tool error for the whole call is also correct.
				if err != nil || got.IsError {
					return
				}
			}
			require.NoError(t, err)
			if hasRequest {
				assert.Equal(t, wantRequest, posted, "the request body")
			}
			if want.IsError {
				require.True(t, got.IsError)
				require.Len(t, got.Content, 1)
				assert.Equal(t, want.Content[0].Text, got.Content[0].(*mcp.TextContent).Text)
				return
			}
			require.False(t, got.IsError, got.Content[0].(*mcp.TextContent).Text)
			body, err := json.Marshal(got.StructuredContent)
			require.NoError(t, err)
			var gotResult map[string]any
			require.NoError(t, json.Unmarshal(body, &gotResult))
			delete(wantResult, "grafanaUrl")
			assert.Equal(t, wantResult, gotResult)
		})
	}
}
