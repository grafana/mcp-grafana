//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	vizcontract "github.com/grafana/viz-contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Source test of the viz contract (fixtures.8). The sets come from github.com/grafana/viz-contract, at the version
// in go.mod.

var vizFixtures = vizcontract.Fixtures()

// vizSourceFixtures gives the fixture sets for the Source: the ones with a response.json.
func vizSourceFixtures(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(vizFixtures, ".")
	require.NoError(t, err)
	var dirs []string
	for _, e := range entries {
		if _, err := fs.Stat(vizFixtures, path.Join(e.Name(), "response.json")); err == nil {
			dirs = append(dirs, e.Name())
		}
	}
	require.NotEmpty(t, dirs)
	return dirs
}

func readFixtureJSON(t *testing.T, dir, name string, into any) bool {
	t.Helper()
	data, err := fs.ReadFile(vizFixtures, path.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, into))
	return true
}

func TestQueryDatasourceConformance(t *testing.T) {
	for _, dir := range vizSourceFixtures(t) {
		t.Run(dir, func(t *testing.T) {
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

			// tool.16 allows one tool error for the whole call too, but this Source gives partial results, so a set with
			// a status other than 200 must match its result.
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
