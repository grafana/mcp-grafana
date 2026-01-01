//go:build unit

package tools

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func intPtr(i int) *int {
	return &i
}

func TestBuildRenderURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		args     GetPanelImageParams
		contains []string
	}{
		{
			name:    "Basic dashboard render",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
			},
			contains: []string{
				"http://localhost:3000/render/d/abc123",
				"width=1000",
				"height=500",
				"scale=1",
				"kiosk=true",
			},
		},
		{
			name:    "Panel render with custom dimensions",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				PanelID:      intPtr(5),
				Width:        intPtr(800),
				Height:       intPtr(600),
			},
			contains: []string{
				"http://localhost:3000/render/d/abc123",
				"viewPanel=5",
				"width=800",
				"height=600",
			},
		},
		{
			name:    "With time range",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				TimeRange: &RenderTimeRange{
					From: "now-1h",
					To:   "now",
				},
			},
			contains: []string{
				"from=now-1h",
				"to=now",
			},
		},
		{
			name:    "With theme",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Theme:        stringPtr("light"),
			},
			contains: []string{
				"theme=light",
			},
		},
		{
			name:    "With variables",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Variables: map[string]string{
					"var-datasource": "prometheus",
					"var-host":       "server01",
				},
			},
			contains: []string{
				"var-datasource=prometheus",
				"var-host=server01",
			},
		},
		{
			name:    "With scale",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Scale:        intPtr(2),
			},
			contains: []string{
				"scale=2",
			},
		},
		{
			name:    "URL with trailing slash",
			baseURL: "http://localhost:3000/",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
			},
			contains: []string{
				"http://localhost:3000//render/d/abc123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildRenderURL(tt.baseURL, tt.args)
			require.NoError(t, err)

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected)
			}
		})
	}
}

func TestGetPanelImage(t *testing.T) {
	// Create a test PNG image (1x1 pixel)
	testPNGData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xFF, 0xFF, 0x3F,
		0x00, 0x05, 0xFE, 0x02, 0xFE, 0xDC, 0xCC, 0x59,
		0xE7, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82, // IEND chunk
	}

	t.Run("Successful image render", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path, "/render/d/test-dash")
			assert.Equal(t, "1000", r.URL.Query().Get("width"))
			assert.Equal(t, "500", r.URL.Query().Get("height"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		result, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)

		// Check that the content is image content with base64 data
		content := result.Content[0]
		imageContent, ok := content.(interface {
			GetData() string
			GetMimeType() string
		})
		if ok {
			assert.Equal(t, "image/png", imageContent.GetMimeType())
			// Verify base64 decoding works
			decoded, err := base64.StdEncoding.DecodeString(imageContent.GetData())
			require.NoError(t, err)
			assert.Equal(t, testPNGData, decoded)
		}
	})

	t.Run("Panel image with specific panel ID", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "5", r.URL.Query().Get("viewPanel"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		panelID := 5
		result, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			PanelID:      &panelID,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("Authentication header with API key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
	})

	t.Run("Image renderer not available returns helpful error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "image renderer not available")
		assert.Contains(t, err.Error(), "Grafana Image Renderer plugin")
	})

	t.Run("Server error returns error message", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 500")
	})

	t.Run("Missing Grafana URL returns error", func(t *testing.T) {
		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL: "",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "grafana URL not configured")
	})

	t.Run("With time range parameters", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "now-1h", r.URL.Query().Get("from"))
			assert.Equal(t, "now", r.URL.Query().Get("to"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			TimeRange: &RenderTimeRange{
				From: "now-1h",
				To:   "now",
			},
		})

		require.NoError(t, err)
	})

	t.Run("With dashboard variables", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "prometheus", r.URL.Query().Get("var-datasource"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			Variables: map[string]string{
				"var-datasource": "prometheus",
			},
		})

		require.NoError(t, err)
	})

	t.Run("Org ID header is set when configured", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "2", r.Header.Get("X-Grafana-Org-Id"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
			OrgID:  2,
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
	})
}
