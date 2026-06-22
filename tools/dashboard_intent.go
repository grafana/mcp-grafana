package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// Dashboard intent (https://grafana.com — Grafana Assistant Dashboard
// Intent feature) lives inside the dashboard JSON itself as a top-level
// `intent` block and as a per-panel `panels[].intent` block. These
// tools project that block into a normalized, snake_case shape so MCP
// clients (Cursor, incident bots, CI pipelines, etc.) don't have to
// re-walk the dashboard JSON to read it.
//
// The on-disk JSON uses camelCase (`expectedBehavior`, `failureModes`,
// `relatedSlos`, `lastVerifiedAt`) to match the rest of the dashboard
// schema. The MCP surface here is snake_case to match the assistant
// API and other MCP tools that expose dashboard data.
//
// Provenance map keys are also snake_case and pass through unchanged
// from the dashboard JSON (e.g. `expected_behavior.normal_range`,
// `failure_modes`).

const dashboardIntentSchemaVersionCurrent = 1

// DashboardIntent is the normalized shape an MCP client sees for a
// dashboard or panel intent block.
type DashboardIntent struct {
	DashboardUID     string            `json:"dashboard_uid"`
	PanelID          *int              `json:"panel_id,omitempty"`
	SchemaVersion    int               `json:"schema_version"`
	Purpose          string            `json:"purpose,omitempty"`
	Owner            string            `json:"owner,omitempty"`
	ExpectedBehavior *ExpectedBehavior `json:"expected_behavior,omitempty"`
	FailureModes     []FailureMode     `json:"failure_modes,omitempty"`
	RelatedSLOs      []RelatedSLO      `json:"related_slos,omitempty"`
	Runbooks         []Runbook         `json:"runbooks,omitempty"`
	Provenance       map[string]string `json:"provenance,omitempty"`
	LastVerifiedAt   *time.Time        `json:"last_verified_at,omitempty"`
}

type ExpectedBehavior struct {
	NormalRange    string `json:"normal_range,omitempty"`
	AlertThreshold string `json:"alert_threshold,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

type FailureMode struct {
	Tag         string `json:"tag"`
	Description string `json:"description,omitempty"`
}

type RelatedSLO struct {
	Name   string `json:"name"`
	Target string `json:"target,omitempty"`
	URL    string `json:"url,omitempty"`
}

type Runbook struct {
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

// GetDashboardIntentParams addresses a single intent block — dashboard-
// level when `panel_id` is omitted, panel-level otherwise.
type GetDashboardIntentParams struct {
	UID     string `json:"uid" jsonschema:"required,description=The UID of the dashboard whose intent to fetch."`
	PanelID *int   `json:"panel_id,omitempty" jsonschema:"description=Optional panel ID. When set\\, returns the panel's intent block. When omitted\\, returns the dashboard-level intent block."`
}

func getDashboardIntent(ctx context.Context, args GetDashboardIntentParams) (*DashboardIntent, error) {
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.UID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid %s: %w", args.UID, err)
	}
	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard %s JSON is not an object", args.UID)
	}

	if args.PanelID == nil {
		raw, ok := db["intent"]
		if !ok || raw == nil {
			return nil, fmt.Errorf("dashboard %s has no intent block", args.UID)
		}
		intent, err := projectIntent(raw, args.UID, nil)
		if err != nil {
			return nil, err
		}
		return intent, nil
	}

	panel, err := findPanelByID(db, *args.PanelID)
	if err != nil {
		return nil, err
	}
	raw, ok := panel["intent"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("panel %d on dashboard %s has no intent block", *args.PanelID, args.UID)
	}
	return projectIntent(raw, args.UID, args.PanelID)
}

var GetDashboardIntent = mcpgrafana.MustTool(
	"get_dashboard_intent",
	"Get author-declared dashboard intent (purpose\\, owner\\, expected behavior\\, failure modes\\, related SLOs\\, runbooks\\, per-field provenance) for a Grafana dashboard or one of its panels. Reads from the `intent` block embedded in the dashboard JSON. Returns an error when the dashboard has no intent block — callers can treat that the same as 'no authored intent yet'. Provenance values explain whether each field was author-written\\, lifted from an alert rule/SLO\\, computed from history\\, or drafted by an assistant.",
	getDashboardIntent,
	mcp.WithTitleAnnotation("Get dashboard intent"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListDashboardIntentParams addresses a full intent bundle for the
// dashboard — the dashboard-level block (when present) plus one entry
// per panel that has an intent block.
type ListDashboardIntentParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard whose intent bundle to fetch."`
}

// DashboardIntentBundle is the full picture of authored intent for a
// dashboard in one call — preferred over repeatedly calling
// get_dashboard_intent for every panel.
type DashboardIntentBundle struct {
	DashboardUID string             `json:"dashboard_uid"`
	Dashboard    *DashboardIntent   `json:"dashboard,omitempty"`
	Panels       []DashboardIntent  `json:"panels"`
}

func listDashboardIntent(ctx context.Context, args ListDashboardIntentParams) (*DashboardIntentBundle, error) {
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.UID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid %s: %w", args.UID, err)
	}
	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard %s JSON is not an object", args.UID)
	}

	bundle := &DashboardIntentBundle{
		DashboardUID: args.UID,
		Panels:       []DashboardIntent{},
	}

	if raw, ok := db["intent"]; ok && raw != nil {
		dashIntent, err := projectIntent(raw, args.UID, nil)
		if err != nil {
			return nil, err
		}
		bundle.Dashboard = dashIntent
	}

	for _, panel := range collectAllPanels(db) {
		raw, ok := panel["intent"]
		if !ok || raw == nil {
			continue
		}
		pid := safeInt(panel, "id")
		if pid == 0 {
			continue
		}
		panelID := pid
		panelIntent, err := projectIntent(raw, args.UID, &panelID)
		if err != nil {
			return nil, err
		}
		bundle.Panels = append(bundle.Panels, *panelIntent)
	}

	return bundle, nil
}

var ListDashboardIntent = mcpgrafana.MustTool(
	"list_dashboard_intent",
	"Get the full authored-intent bundle for a Grafana dashboard in one call: the dashboard-level intent block (when present) plus every panel-level intent block discovered while walking the dashboard's panels. Prefer this over repeatedly calling get_dashboard_intent for each panel when you want the whole picture. Returns an empty bundle (no error) for dashboards without any authored intent.",
	listDashboardIntent,
	mcp.WithTitleAnnotation("List dashboard intent bundle"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddDashboardIntentTools registers the dashboard-intent MCP tools on
// the given server. Both are read-only and unconditionally available;
// dashboards without an `intent` block return a clean error (single-
// resource get) or an empty bundle (list).
func AddDashboardIntentTools(mcp *server.MCPServer) {
	GetDashboardIntent.Register(mcp)
	ListDashboardIntent.Register(mcp)
}

// projectIntent converts the raw camelCase intent block from the
// dashboard JSON into the normalized snake_case shape MCP clients see.
// The two shapes mostly differ in casing; the fields kept in the same
// form (provenance map keys, `tag`, `name`, `title`) are passed through
// unchanged.
func projectIntent(raw interface{}, uid string, panelID *int) (*DashboardIntent, error) {
	block, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("intent block on dashboard %s is not an object", uid)
	}

	intent := &DashboardIntent{
		DashboardUID:  uid,
		PanelID:       panelID,
		SchemaVersion: safeInt(block, "schemaVersion"),
		Purpose:       safeString(block, "purpose"),
		Owner:         safeString(block, "owner"),
	}
	if intent.SchemaVersion == 0 {
		intent.SchemaVersion = dashboardIntentSchemaVersionCurrent
	}

	if eb := safeObject(block, "expectedBehavior"); eb != nil {
		intent.ExpectedBehavior = &ExpectedBehavior{
			NormalRange:    safeString(eb, "normalRange"),
			AlertThreshold: safeString(eb, "alertThreshold"),
			Notes:          safeString(eb, "notes"),
		}
	}

	for _, raw := range safeArray(block, "failureModes") {
		fm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		tag := safeString(fm, "tag")
		if tag == "" {
			continue
		}
		intent.FailureModes = append(intent.FailureModes, FailureMode{
			Tag:         tag,
			Description: safeString(fm, "description"),
		})
	}

	for _, raw := range safeArray(block, "relatedSlos") {
		slo, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name := safeString(slo, "name")
		if name == "" {
			continue
		}
		intent.RelatedSLOs = append(intent.RelatedSLOs, RelatedSLO{
			Name:   name,
			Target: safeString(slo, "target"),
			URL:    safeString(slo, "url"),
		})
	}

	for _, raw := range safeArray(block, "runbooks") {
		rb, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		title := safeString(rb, "title")
		if title == "" {
			continue
		}
		intent.Runbooks = append(intent.Runbooks, Runbook{
			Title: title,
			URL:   safeString(rb, "url"),
		})
	}

	if prov := safeObject(block, "provenance"); prov != nil {
		intent.Provenance = map[string]string{}
		for k, v := range prov {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				intent.Provenance[k] = s
			}
		}
	}

	if lv := safeString(block, "lastVerifiedAt"); lv != "" {
		if t, err := time.Parse(time.RFC3339, lv); err == nil {
			intent.LastVerifiedAt = &t
		}
	}

	return intent, nil
}
