package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

func newShortenTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func newShortenTestContext(apiURL, publicURL, apiKey string) context.Context {
	cfg := mcpgrafana.GrafanaConfig{URL: apiURL, APIKey: apiKey}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
	if publicURL == "" {
		return ctx
	}
	return mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{PublicURL: publicURL})
}

func TestGenerateDeeplink(t *testing.T) {
	grafanaCfg := mcpgrafana.GrafanaConfig{
		URL: "http://localhost:3000",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

	t.Run("Dashboard deeplink", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/abc123", result)
	})

	t.Run("Panel deeplink", func(t *testing.T) {
		panelID := 5
		params := GenerateDeeplinkParams{
			ResourceType: "panel",
			DashboardUID: stringPtr("dash-123"),
			PanelID:      &panelID,
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/dash-123?viewPanel=5", result)
	})

	t.Run("Explore deeplink basic", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("prometheus-uid"),
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/explore?left=")
		assert.Contains(t, result, "prometheus-uid")
	})

	t.Run("Explore deeplink with time range inside left JSON", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("prometheus-uid"),
			TimeRange: &TimeRange{
				From: "now-1h",
				To:   "now",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)

		u, err := url.Parse(result)
		require.NoError(t, err)

		leftRaw := u.Query().Get("left")
		require.NotEmpty(t, leftRaw)

		// Range must be inside `left`, not as top-level URL params.
		assert.Contains(t, leftRaw, `"range"`)
		assert.Contains(t, leftRaw, "now-1h")
		assert.Contains(t, leftRaw, "now")
		assert.Empty(t, u.Query().Get("from"), "from should not be a top-level URL param for explore")
		assert.Empty(t, u.Query().Get("to"), "to should not be a top-level URL param for explore")

		// There must be exactly one `left` param.
		assert.Len(t, u.Query()["left"], 1)
	})

	t.Run("Explore deeplink with queries", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:  "explore",
			DatasourceUID: stringPtr("prometheus-uid"),
			Queries: []map[string]interface{}{
				{"refId": "A", "expr": "up"},
			},
			TimeRange: &TimeRange{From: "now-1h", To: "now"},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)

		u, err := url.Parse(result)
		require.NoError(t, err)

		leftRaw := u.Query().Get("left")
		assert.Contains(t, leftRaw, `"queries"`)
		assert.Contains(t, leftRaw, `"expr"`)
		assert.Contains(t, leftRaw, "up")
	})

	t.Run("With time range on dashboard", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			TimeRange: &TimeRange{
				From: "now-1h",
				To:   "now",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/d/abc123")
		assert.Contains(t, result, "from=now-1h")
		assert.Contains(t, result, "to=now")
	})

	t.Run("With additional query params", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			QueryParams: map[string]string{
				"var-datasource": "prometheus",
				"refresh":        "30s",
			},
		}

		result, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, result, "http://localhost:3000/d/abc123")
		assert.Contains(t, result, "var-datasource=prometheus")
		assert.Contains(t, result, "refresh=30s")
	})

	t.Run("Uses public URL from GrafanaClient when available", func(t *testing.T) {
		// Set up context with both config URL and a GrafanaClient with a public URL
		cfg := mcpgrafana.GrafanaConfig{
			URL: "http://internal-grafana:3000",
		}
		ctxWithPublicURL := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
		ctxWithPublicURL = mcpgrafana.WithGrafanaClient(ctxWithPublicURL, &mcpgrafana.GrafanaClient{
			PublicURL: "https://grafana.example.com",
		})

		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}

		result, err := generateDeeplink(ctxWithPublicURL, params)
		require.NoError(t, err)
		assert.Equal(t, "https://grafana.example.com/d/abc123", result)
	})

	t.Run("Falls back to config URL when public URL is empty", func(t *testing.T) {
		cfg := mcpgrafana.GrafanaConfig{
			URL: "http://localhost:3000",
		}
		ctxWithEmptyPublicURL := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
		ctxWithEmptyPublicURL = mcpgrafana.WithGrafanaClient(ctxWithEmptyPublicURL, &mcpgrafana.GrafanaClient{
			PublicURL: "",
		})

		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}

		result, err := generateDeeplink(ctxWithEmptyPublicURL, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/abc123", result)
	})

	t.Run("Falls back to config URL when no GrafanaClient in context", func(t *testing.T) {
		cfg := mcpgrafana.GrafanaConfig{
			URL: "http://localhost:3000",
		}
		ctxNoClient := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}

		result, err := generateDeeplink(ctxNoClient, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/abc123", result)
	})

	t.Run("Error cases", func(t *testing.T) {
		emptyGrafanaCfg := mcpgrafana.GrafanaConfig{
			URL: "",
		}
		emptyCtx := mcpgrafana.WithGrafanaConfig(context.Background(), emptyGrafanaCfg)
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
		}
		_, err := generateDeeplink(emptyCtx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "grafana url not configured")

		params.ResourceType = "unsupported"
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported resource type")

		// Test missing dashboardUid for dashboard
		params = GenerateDeeplinkParams{
			ResourceType: "dashboard",
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "either dashboardUid or provisioningPreview must be set")

		// Test missing dashboardUid for panel
		params = GenerateDeeplinkParams{
			ResourceType: "panel",
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "either dashboardUid or provisioningPreview must be set")

		// Test missing panelId for panel
		params = GenerateDeeplinkParams{
			ResourceType: "panel",
			DashboardUID: stringPtr("dash-123"),
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "panelId is required")

		// Test missing datasourceUid for explore
		params = GenerateDeeplinkParams{
			ResourceType: "explore",
		}
		_, err = generateDeeplink(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "datasourceUid is required")
	})

	t.Run("Provisioning preview dashboard deeplink", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			ProvisioningPreview: &DeeplinkProvisioningPreview{
				Repo: "git-global",
				Path: "otel-migration/compare.json",
				Ref:  "madaraszg/otel-migration-dashboard",
			},
		}

		deeplink, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/dashboard/provisioning/git-global/preview/otel-migration/compare.json?ref=madaraszg%2Fotel-migration-dashboard", deeplink)
	})

	t.Run("Provisioning preview panel deeplink", func(t *testing.T) {
		panelID := 5
		params := GenerateDeeplinkParams{
			ResourceType: "panel",
			ProvisioningPreview: &DeeplinkProvisioningPreview{
				Repo: "git-global",
				Path: "otel-migration/compare.json",
				Ref:  "feature-branch",
			},
			PanelID: &panelID,
		}

		deeplink, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		assert.Contains(t, deeplink, "/dashboard/provisioning/git-global/preview/otel-migration/compare.json")
		assert.Contains(t, deeplink, "ref=feature-branch")
		assert.Contains(t, deeplink, "viewPanel=5")
	})

	t.Run("Provisioning preview with pull request URL", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			ProvisioningPreview: &DeeplinkProvisioningPreview{
				Repo:           "git-global",
				Path:           "folder/dash.json",
				Ref:            "pr-branch",
				PullRequestURL: "https://github.com/example/repo/pull/42",
			},
		}

		deeplink, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		u, err := url.Parse(deeplink)
		require.NoError(t, err)
		assert.Equal(t, "/dashboard/provisioning/git-global/preview/folder/dash.json", u.Path)
		assert.Equal(t, "pr-branch", u.Query().Get("ref"))
		assert.Equal(t, "https://github.com/example/repo/pull/42", u.Query().Get("pull_request_url"))
	})

	t.Run("Provisioning preview escapes spaces in path", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			ProvisioningPreview: &DeeplinkProvisioningPreview{
				Repo: "git-global",
				Path: "folder name/dash file.json",
			},
		}

		deeplink, err := generateDeeplink(ctx, params)
		require.NoError(t, err)
		// Spaces are percent-encoded but the structural / separators are preserved.
		assert.Contains(t, deeplink, "/dashboard/provisioning/git-global/preview/folder%20name/dash%20file.json")
	})

	t.Run("Dashboard requires uid or provisioning preview", func(t *testing.T) {
		params := GenerateDeeplinkParams{ResourceType: "dashboard"}
		_, err := generateDeeplink(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "either dashboardUid or provisioningPreview must be set")
	})

	t.Run("Dashboard rejects empty dashboardUid", func(t *testing.T) {
		params := GenerateDeeplinkParams{ResourceType: "dashboard", DashboardUID: stringPtr("")}
		_, err := generateDeeplink(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "either dashboardUid or provisioningPreview must be set")
	})

	t.Run("Dashboard rejects both uid and provisioning preview", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			ProvisioningPreview: &DeeplinkProvisioningPreview{
				Repo: "git-global",
				Path: "x.json",
			},
		}
		_, err := generateDeeplink(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("Provisioning preview rejects traversal segments", func(t *testing.T) {
		cases := []struct {
			name string
			repo string
			path string
			want string
		}{
			{"repo with slash", "a/b", "x.json", "must not contain path separators"},
			{"repo is ..", "..", "x.json", "must not be a relative-directory reference"},
			{"path with ..", "ok", "a/../b.json", "must not contain relative-directory segments"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := generateDeeplink(ctx, GenerateDeeplinkParams{
					ResourceType:        "dashboard",
					ProvisioningPreview: &DeeplinkProvisioningPreview{Repo: tc.repo, Path: tc.path},
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.want)
			})
		}
	})

	t.Run("Provisioning preview requires repo and path", func(t *testing.T) {
		params := GenerateDeeplinkParams{
			ResourceType:        "dashboard",
			ProvisioningPreview: &DeeplinkProvisioningPreview{Repo: "git-global"},
		}
		_, err := generateDeeplink(ctx, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path is required")
	})
}

func TestShortenURL(t *testing.T) {
	const longDashboardURL = "https://grafana.example.com/d/test"

	t.Run("Success", func(t *testing.T) {
		var capturedPath string
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/short-urls", r.URL.Path)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			capturedPath = body["path"]

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123","url":"https://grafana.example.com/goto/abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "https://grafana.example.com", "")

		result, err := shortenURL(ctx, "https://grafana.example.com/explore?left=%7B%22datasource%22%3A%22abc%22%7D")
		require.NoError(t, err)
		assert.Equal(t, "https://grafana.example.com/goto/abc123", result)
		assert.Equal(t, "explore?left=%7B%22datasource%22%3A%22abc%22%7D", capturedPath)
	})

	t.Run("Includes auth headers", func(t *testing.T) {
		var capturedAuth string
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123","url":"https://grafana.example.com/goto/abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "", "glsa_test_token")

		_, err := shortenURL(ctx, longDashboardURL)
		require.NoError(t, err)
		assert.Equal(t, "Bearer glsa_test_token", capturedAuth)
	})

	t.Run("Rejects invalid URL", func(t *testing.T) {
		ctx := newShortenTestContext("http://localhost:3000", "", "")
		_, err := shortenURL(ctx, "http://%zz")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid url")
	})

	t.Run("No grafana URL configured", func(t *testing.T) {
		ctx := newShortenTestContext("", "", "")
		_, err := shortenURL(ctx, longDashboardURL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "grafana url not configured")
	})

	t.Run("Relative short URL response", func(t *testing.T) {
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123","url":"/goto/abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "", "")

		result, err := shortenURL(ctx, longDashboardURL)
		require.NoError(t, err)
		assert.Equal(t, ts.URL+"/goto/abc123", result)
	})

	t.Run("Uses API URL for shorten request and Public URL for relative return", func(t *testing.T) {
		var requestHost string
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestHost = r.Host
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123","url":"/goto/abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "https://grafana.public.example.com", "")

		result, err := shortenURL(ctx, "https://grafana.public.example.com/d/test")
		require.NoError(t, err)
		assert.Contains(t, requestHost, "127.0.0.1")
		assert.Equal(t, "https://grafana.public.example.com/goto/abc123", result)
	})

	t.Run("Rebases absolute short URL response to Public URL host", func(t *testing.T) {
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123","url":"http://internal-grafana:3000/goto/abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "https://grafana.public.example.com", "")

		result, err := shortenURL(ctx, "https://grafana.public.example.com/d/test")
		require.NoError(t, err)
		assert.Equal(t, "https://grafana.public.example.com/goto/abc123", result)
	})

	t.Run("Non-2xx response returns error", func(t *testing.T) {
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "", "")

		_, err := shortenURL(ctx, longDashboardURL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create short url failed with status 500")
	})

	t.Run("Missing url field in response returns error", func(t *testing.T) {
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "", "")

		_, err := shortenURL(ctx, longDashboardURL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing url field")
	})
}

func TestGenerateDeeplink_ShortenCompatibilityFallback(t *testing.T) {
	t.Run("Returns shortened URL when shortening succeeds", func(t *testing.T) {
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/short-urls", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"abc123","url":"https://grafana.example.com/goto/abc123"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "https://grafana.example.com", "")

		result, err := generateDeeplink(ctx, GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			Shorten:      true,
		})
		require.NoError(t, err)
		assert.Equal(t, "https://grafana.example.com/goto/abc123", result)
	})

	t.Run("Falls back to long URL when shortening fails", func(t *testing.T) {
		ts := newShortenTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"forbidden"}`))
		}))

		ctx := newShortenTestContext(ts.URL, "https://grafana.example.com", "")

		result, err := generateDeeplink(ctx, GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			Shorten:      true,
		})
		require.NoError(t, err)
		assert.Equal(t, "https://grafana.example.com/d/abc123", result)
	})

	t.Run("Read-only mode ignores shorten and returns long URL", func(t *testing.T) {
		ctx := newShortenTestContext("http://localhost:3000", "", "")

		result, err := generateDeeplinkReadOnly(ctx, GenerateDeeplinkParams{
			ResourceType: "dashboard",
			DashboardUID: stringPtr("abc123"),
			Shorten:      true,
		})
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000/d/abc123", result)
	})
}

// TestGenerateDeeplink_RejectsMalformedBaseURL_ClientWithEmptyPublicURL
// exercises the fall-through path where a GrafanaClient is attached but its
// PublicURL is empty (/api/frontend/settings returned an empty or malformed
// appUrl). generateDeeplink then reads config.URL, which may itself be
// malformed. Without this guard, the malformed URL flows into the returned
// deeplink (e.g. "http://%gg/d/abc123") with no error signal. Post-fix:
// ValidateGrafanaURL catches the malformed baseURL and returns a structured
// error wrapping ErrInvalidGrafanaURL.
func TestGenerateDeeplink_RejectsMalformedBaseURL_ClientWithEmptyPublicURL(t *testing.T) {
	grafanaCfg := mcpgrafana.GrafanaConfig{
		URL: "http://%gg",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

	// Zero-value GrafanaClient: generateDeeplink only reads gc.PublicURL
	// (empty), which is the code path we want to exercise. The test does
	// not invoke any method on the client, so a zero value is equivalent to
	// a real client whose fetchPublicURL call returned empty.
	ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{})

	params := GenerateDeeplinkParams{
		ResourceType: "dashboard",
		DashboardUID: stringPtr("abc123"),
	}

	_, err := generateDeeplink(ctx, params)
	require.Error(t, err)
	assert.True(t, errors.Is(err, mcpgrafana.ErrInvalidGrafanaURL),
		"expected error to wrap ErrInvalidGrafanaURL, got: %v", err)
	assert.Contains(t, err.Error(), "invalid",
		"error message must include 'invalid' for operator/LLM readability; got %q", err.Error())
}

// TestGenerateDeeplink_RejectsMalformedBaseURL_NoClient covers the same bug
// class but without any GrafanaClient attached to the context. Proves the
// ValidateGrafanaURL guard fires on config.URL alone.
func TestGenerateDeeplink_RejectsMalformedBaseURL_NoClient(t *testing.T) {
	grafanaCfg := mcpgrafana.GrafanaConfig{
		URL: "http://%gg",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

	params := GenerateDeeplinkParams{
		ResourceType: "dashboard",
		DashboardUID: stringPtr("abc123"),
	}

	_, err := generateDeeplink(ctx, params)
	require.Error(t, err)
	assert.True(t, errors.Is(err, mcpgrafana.ErrInvalidGrafanaURL),
		"expected error to wrap ErrInvalidGrafanaURL, got: %v", err)
	assert.Contains(t, err.Error(), "invalid",
		"error message must include 'invalid' for operator/LLM readability; got %q", err.Error())
}

func TestToGrafanaTimeParam(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"relative now-1h", "now-1h", "now-1h"},
		{"relative now", "now", "now"},
		{"epoch milliseconds", "1777380300000", "1777380300000"},
		{"ISO 8601 UTC", "2026-04-28T12:45:00Z", "1777380300000"},
		{"ISO 8601 with offset", "2026-04-28T13:45:00+01:00", "1777380300000"},
		{"ISO 8601 with ms", "2026-04-28T12:45:00.000Z", "1777380300000"},
		{"unrecognized format passthrough", "yesterday", "yesterday"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, toGrafanaTimeParam(tt.input))
		})
	}
}

func TestGenerateDeeplink_ISO8601TimeRange(t *testing.T) {
	grafanaCfg := mcpgrafana.GrafanaConfig{
		URL: "http://localhost:3000",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

	panelID := 8
	params := GenerateDeeplinkParams{
		ResourceType: "panel",
		DashboardUID: stringPtr("dash-123"),
		PanelID:      &panelID,
		TimeRange: &TimeRange{
			From: "2026-04-28T12:45:00Z",
			To:   "2026-04-28T13:15:00Z",
		},
	}

	result, err := generateDeeplink(ctx, params)
	require.NoError(t, err)
	assert.Contains(t, result, "from=1777380300000")
	assert.Contains(t, result, "to=1777382100000")
	assert.NotContains(t, result, "2026-04-28")
}

func TestGenerateDeeplink_ExploreISO8601TimeRange(t *testing.T) {
	grafanaCfg := mcpgrafana.GrafanaConfig{
		URL: "http://localhost:3000",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

	params := GenerateDeeplinkParams{
		ResourceType:  "explore",
		DatasourceUID: stringPtr("prometheus-uid"),
		TimeRange: &TimeRange{
			From: "2026-04-28T12:45:00Z",
			To:   "2026-04-28T13:15:00Z",
		},
	}

	result, err := generateDeeplink(ctx, params)
	require.NoError(t, err)
	assert.Contains(t, result, "1777380300000")
	assert.Contains(t, result, "1777382100000")
	assert.NotContains(t, result, "2026-04-28")
}
