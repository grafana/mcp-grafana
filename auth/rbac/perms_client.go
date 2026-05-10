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

// FetchOrgRole returns the user's BasicRole ("Viewer", "Editor", "Admin",
// or "" if absent / unparseable) from Grafana's /api/user endpoint.
//
// The /api/access-control/user/permissions endpoint doesn't include the
// basic role; deriving it from action names (e.g. roles:write → Admin)
// fails on real Grafana RBAC because the action constants those
// heuristics tested for are not the names Grafana emits. /api/user is the
// authoritative source.
//
// Empty role on transport-level error or non-200 status is treated as
// missing, not as a hard failure: a Grafana that doesn't expose the
// endpoint to the SA token shouldn't block tool gating entirely. Callers
// that need the role for Basic-mode gating will skip Basic-mode-only
// tools, which is the safe default.
func (p *PermsClient) FetchOrgRole(ctx context.Context, token string) (string, error) {
	url := p.grafanaURL + "/api/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch user: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("user endpoint returned %d", resp.StatusCode)
	}
	// /api/user returns the org-scoped role under "orgRole". The Viewer/
	// Editor/Admin values match what the gate expects without translation.
	var out struct {
		OrgRole string `json:"orgRole"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode user: %w", err)
	}
	return out.OrgRole, nil
}
