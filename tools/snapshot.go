package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type ListSnapshotsParams struct {
	Query string `json:"query,omitempty" jsonschema:"description=Optional search query for snapshot name"`
	Limit *int   `json:"limit,omitempty" jsonschema:"description=Maximum number of snapshots to return (Grafana defaults to 1000 when omitted)"`
}

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

type GetSnapshotParams struct {
	Key string `json:"key" jsonschema:"required,description=Snapshot key to retrieve"`
}

type SnapshotDetail struct {
	Meta      map[string]any `json:"meta"`
	Dashboard map[string]any `json:"dashboard"`
}

type CreateSnapshotParams struct {
	Dashboard map[string]any `json:"dashboard" jsonschema:"required,description=Complete dashboard model to snapshot (as returned by Grafana dashboard APIs)"`
	Name      string         `json:"name,omitempty" jsonschema:"description=Optional snapshot name"`
	Expires   *int64         `json:"expires,omitempty" jsonschema:"description=Snapshot expiration in seconds (e.g. 3600 for 1 hour)"`
	External  *bool          `json:"external,omitempty" jsonschema:"description=Store snapshot on external server. Requires key and deleteKey when true"`
	Key       string         `json:"key,omitempty" jsonschema:"description=Custom snapshot key. Required when external is true"`
	DeleteKey string         `json:"deleteKey,omitempty" jsonschema:"description=Secret key for deleting external snapshots. Required when external is true"`
}

type CreateSnapshotResult struct {
	DeleteKey string `json:"deleteKey"`
	DeleteURL string `json:"deleteUrl"`
	Key       string `json:"key"`
	URL       string `json:"url"`
	ID        int    `json:"id"`
}

type DeleteSnapshotParams struct {
	Key string `json:"key" jsonschema:"required,description=Snapshot key to delete"`
}

type DeleteSnapshotResult struct {
	Message string `json:"message"`
	ID      int    `json:"id"`
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

func createSnapshot(ctx context.Context, args CreateSnapshotParams) (*CreateSnapshotResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}
	if len(args.Dashboard) == 0 {
		return nil, fmt.Errorf("dashboard is required")
	}

	isExternal := args.External != nil && *args.External
	if isExternal && strings.TrimSpace(args.Key) == "" {
		return nil, fmt.Errorf("key is required when external is true")
	}
	if isExternal && strings.TrimSpace(args.DeleteKey) == "" {
		return nil, fmt.Errorf("deleteKey is required when external is true")
	}

	body, status, err := doSnapshotRequest(ctx, cfg, http.MethodPost, "/api/snapshots", nil, args)
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

var ListSnapshotsTool = mcpgrafana.MustTool(
	"list_snapshots",
	"List Grafana dashboard snapshots with optional query and result limit filters.",
	listSnapshots,
	mcp.WithTitleAnnotation("List snapshots"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var GetSnapshotTool = mcpgrafana.MustTool(
	"get_snapshot",
	"Get a Grafana snapshot by key, including snapshot metadata and dashboard payload.",
	getSnapshot,
	mcp.WithTitleAnnotation("Get snapshot"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var CreateSnapshotTool = mcpgrafana.MustTool(
	"create_snapshot",
	"Create a Grafana snapshot from a full dashboard payload. Supports optional expiration and external snapshot fields.",
	createSnapshot,
	mcp.WithTitleAnnotation("Create snapshot"),
	mcp.WithIdempotentHintAnnotation(false),
)

var DeleteSnapshotTool = mcpgrafana.MustTool(
	"delete_snapshot",
	"Delete a Grafana snapshot by snapshot key.",
	deleteSnapshot,
	mcp.WithTitleAnnotation("Delete snapshot"),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
)

func AddSnapshotTools(s *server.MCPServer, enableWriteTools bool) {
	ListSnapshotsTool.Register(s)
	GetSnapshotTool.Register(s)
	if enableWriteTools {
		CreateSnapshotTool.Register(s)
		DeleteSnapshotTool.Register(s)
	}
}
