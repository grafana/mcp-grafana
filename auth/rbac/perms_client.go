package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PermissionSet maps action → list of scopes the user has that action on.
// This mirrors Grafana's /api/access-control/user/permissions response shape.
type PermissionSet map[string][]string

// PermsClient calls Grafana's /api/access-control/user/permissions.
type PermsClient struct {
	grafanaURL string
	httpClient *http.Client
}

// NewPermsClient builds a client. httpClient is optional — if nil, a default
// 10-second-timeout client is used. The client should NOT follow redirects:
// callers pass user-controlled tokens and we don't want to leak them on
// misconfigured proxies. NewPermsClient configures CheckRedirect accordingly.
func NewPermsClient(grafanaURL string, httpClient *http.Client) *PermsClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	// Wrap with a no-redirect copy so the caller's client retains its own
	// configuration but follows our redirect policy here.
	cp := *httpClient
	cp.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &PermsClient{
		grafanaURL: strings.TrimRight(grafanaURL, "/"),
		httpClient: &cp,
	}
}

// Fetch retrieves the user's permission set. token is the per-user bearer
// (Grafana SA token in Mode C, OAuth access token in Mode A).
func (p *PermsClient) Fetch(ctx context.Context, token string) (PermissionSet, error) {
	url := p.grafanaURL + "/api/access-control/user/permissions?reloadcache=false"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch permissions: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("permissions endpoint returned %d", resp.StatusCode)
	}
	var out PermissionSet
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode permissions: %w", err)
	}
	return out, nil
}
