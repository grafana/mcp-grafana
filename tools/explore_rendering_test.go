//go:build unit

package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildExploreRenderURL(t *testing.T) {
	args := ExploreRenderParams{
		DatasourceUID: "metrics uid",
		Queries: []ExploreRenderQuery{
			{Model: map[string]any{"expr": "rate(http_requests_total[5m])", "range": true}},
			{RefID: "Z", Model: map[string]any{"expr": "up", "format": "time_series"}},
		},
		TimeRange: RenderTimeRange{From: "now-1h", To: "now"},
		Theme:     "light",
		Variables: map[string]string{"var-namespace": "prod/eu"},
	}

	result, err := buildExploreRenderURL("http://grafana.example/", 7, "prometheus", args)
	require.NoError(t, err)

	parsed, err := url.Parse(result)
	require.NoError(t, err)
	assert.Equal(t, "/explore", parsed.Path)
	assert.Equal(t, "7", parsed.Query().Get("orgId"))
	assert.Equal(t, "1", parsed.Query().Get("schemaVersion"))
	assert.Equal(t, "light", parsed.Query().Get("theme"))
	assert.Equal(t, "prod/eu", parsed.Query().Get("var-namespace"))

	var panes map[string]explorePane
	require.NoError(t, json.Unmarshal([]byte(parsed.Query().Get("panes")), &panes))
	require.Contains(t, panes, "0")
	assert.Equal(t, map[string]string{"type": "prometheus", "uid": "metrics uid"}, panes["0"].Datasource)
	require.Len(t, panes["0"].Queries, 2)
	assert.Equal(t, "A", panes["0"].Queries[0]["refId"])
	assert.Equal(t, "Z", panes["0"].Queries[1]["refId"])
	assert.Equal(t, "rate(http_requests_total[5m])", panes["0"].Queries[0]["expr"])
	assert.Equal(t, "now-1h", panes["0"].Range.From)
}

func TestValidateArtifactPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "reports"), 0o700))

	path, err := validateArtifactPath(root, filepath.Join(root, "reports", "chart.png"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "reports", "chart.png"), path)

	_, err = validateArtifactPath(root, filepath.Join(root, "..", "outside.png"))
	require.ErrorContains(t, err, "below")

	_, err = validateArtifactPath(root, filepath.Join(root, "reports", "chart.svg"))
	require.ErrorContains(t, err, ".png")

	_, err = validateArtifactPath(root, "reports/chart.png")
	require.ErrorContains(t, err, "absolute")
}

func TestWriteArtifactAtomically(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "chart.png")
	data := []byte("png bytes")

	require.NoError(t, writeArtifactAtomically(path, data))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, got)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Equal(t, hex.EncodeToString(func() []byte {
		sum := sha256.Sum256(data)
		return sum[:]
	}()), hex.EncodeToString(func() []byte {
		sum := sha256.Sum256(got)
		return sum[:]
	}()))
}

func TestValidateExploreRenderParams(t *testing.T) {
	args := ExploreRenderParams{
		DatasourceUID: "prom",
		Queries:       []ExploreRenderQuery{{Model: map[string]any{"expr": "up"}}},
		TimeRange:     RenderTimeRange{From: "now-1h", To: "now"},
		OutputPath:    "/tmp/chart.png",
	}
	options, err := validateExploreRenderParams(args)
	require.NoError(t, err)
	assert.Equal(t, defaultExploreWidth, options.width)
	assert.Equal(t, defaultExploreHeight, options.height)
	assert.Equal(t, defaultExploreCrop, options.crop)

	args.Width = 100
	_, err = validateExploreRenderParams(args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "width")
}
