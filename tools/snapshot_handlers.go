package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type SnapshotSummary struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	OrgID       int    `json:"orgId"`
	UserID      int    `json:"userId"`
	External    bool   `json:"external"`
	ExternalURL string `json:"externalUrl"`
	Expires     string `json:"expires"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
}

type SnapshotDetail struct {
	Meta      map[string]any `json:"meta"`
	Dashboard map[string]any `json:"dashboard"`
}

type CreateSnapshotResult struct {
	DeleteKey string `json:"deleteKey"`
	DeleteURL string `json:"deleteUrl"`
	Key       string `json:"key"`
	URL       string `json:"url"`
	ID        int    `json:"id"`
}

type DeleteSnapshotResult struct {
	Message string `json:"message"`
	ID      int    `json:"id"`
}

func snapshotsRead(ctx context.Context, args SnapshotReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("snapshots_read: %w", err)
	}

	switch args.Operation {
	case "list":
		return listSnapshots(ctx, ListSnapshotsParams{Query: args.Query, Limit: args.Limit})
	case "get":
		return getSnapshot(ctx, GetSnapshotParams{Key: args.Key})
	default:
		// Unreachable once validate() has passed; kept for defense in depth.
		return nil, fmt.Errorf("snapshots_read: unknown operation %q", args.Operation)
	}
}

type ListSnapshotsParams struct {
	Query string `json:"query,omitempty" jsonschema:"description=Optional search query for snapshot name"`
	Limit *int   `json:"limit,omitempty" jsonschema:"description=Maximum number of snapshots to return (Grafana defaults to 1000 when omitted)"`
}

func listSnapshots(ctx context.Context, args ListSnapshotsParams) ([]SnapshotSummary, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	query := url.Values{}
	if strings.TrimSpace(args.Query) != "" {
		query.Set("query", strings.TrimSpace(args.Query))
	}
	if args.Limit != nil {
		query.Set("limit", fmt.Sprintf("%d", *args.Limit))
	}

	body, status, err := doSnapshotRequest(ctx, cfg, http.MethodGet, "/api/dashboard/snapshots", query, nil)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list snapshots: unexpected status %d: %s", status, string(body))
	}

	var snapshots []SnapshotSummary
	if err := json.Unmarshal(body, &snapshots); err != nil {
		return nil, fmt.Errorf("decode snapshot list: %w", err)
	}
	return snapshots, nil
}

type GetSnapshotParams struct {
	Key string `json:"key" jsonschema:"required,description=Snapshot key to retrieve"`
}

func getSnapshot(ctx context.Context, args GetSnapshotParams) (*SnapshotDetail, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	key := strings.TrimSpace(args.Key)
	if key == "" {
		return nil, fmt.Errorf("snapshot key is required")
	}

	body, status, err := doSnapshotRequest(ctx, cfg, http.MethodGet, "/api/snapshots/"+url.PathEscape(key), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get snapshot: unexpected status %d: %s", status, string(body))
	}

	var snapshot SnapshotDetail
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot detail: %w", err)
	}
	return &snapshot, nil
}

func snapshotsWrite(ctx context.Context, args SnapshotWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("snapshots_write: %w", err)
	}

	switch args.Operation {
	case "create":
		return createSnapshot(ctx, args)
	case "delete":
		return deleteSnapshot(ctx, DeleteSnapshotParams{Key: args.Key})
	default:
		// Unreachable once validate() has passed; kept for defense in depth.
		return nil, fmt.Errorf("snapshots_write: unknown operation %q", args.Operation)
	}
}

func createSnapshot(ctx context.Context, args SnapshotWriteParams) (*CreateSnapshotResult, error) {
	if err := args.validateExternal(); err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	// A separate wire-format struct, not args itself: args also carries
	// "operation" (and, for 'delete', reuses the same Key field for a
	// different purpose), neither of which belongs in the POST body sent
	// to Grafana's snapshot create API.
	reqBody := struct {
		Dashboard map[string]any `json:"dashboard"`
		Name      string         `json:"name,omitempty"`
		Expires   *int64         `json:"expires,omitempty"`
		External  *bool          `json:"external,omitempty"`
		Key       string         `json:"key,omitempty"`
		DeleteKey string         `json:"deleteKey,omitempty"`
	}{
		Dashboard: args.Dashboard,
		Name:      args.Name,
		Expires:   args.Expires,
		External:  args.External,
		Key:       args.Key,
		DeleteKey: args.DeleteKey,
	}

	body, status, err := doSnapshotRequest(ctx, cfg, http.MethodPost, "/api/snapshots", nil, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("create snapshot: unexpected status %d: %s", status, string(body))
	}

	var result CreateSnapshotResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode snapshot create response: %w", err)
	}
	return &result, nil
}

type DeleteSnapshotParams struct {
	Key string `json:"key" jsonschema:"required,description=Snapshot key to delete"`
}

func deleteSnapshot(ctx context.Context, args DeleteSnapshotParams) (*DeleteSnapshotResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	key := strings.TrimSpace(args.Key)
	if key == "" {
		return nil, fmt.Errorf("snapshot key is required")
	}

	body, status, err := doSnapshotRequest(ctx, cfg, http.MethodDelete, "/api/snapshots/"+url.PathEscape(key), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("delete snapshot: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("delete snapshot: unexpected status %d: %s", status, string(body))
	}

	var result DeleteSnapshotResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode snapshot delete response: %w", err)
	}
	return &result, nil
}

func doSnapshotRequest(ctx context.Context, cfg mcpgrafana.GrafanaConfig, method, path string, query url.Values, body any) ([]byte, int, error) {
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build transport: %w", err)
	}

	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	endpoint := strings.TrimRight(cfg.URL, "/") + path
	if encodedQuery := query.Encode(); encodedQuery != "" {
		endpoint += "?" + encodedQuery
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}
