package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/PaesslerAG/gval"
	"github.com/PaesslerAG/jsonpath"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/dashboards"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// dashboardAPIGroup is the Kubernetes-style API group serving dashboards.
const dashboardAPIGroup = "dashboard.grafana.app"

// dashboardReadVersion is the API version used both to gate the k8s path and to
// perform the initial (classic-schema) fetch. v1beta1 is served from Grafana 12
// onward — which is exactly where dashboard schema v2 can exist — so its
// presence is the signal that the native v2 path is worthwhile. On older
// Grafana (no v1beta1) there are no v2 dashboards, so the legacy API is lossless
// and we use it instead.
const dashboardReadVersion = "v1beta1"

type GetDashboardByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

// dashboardResult is the internal representation of a fetched dashboard. The
// Spec is the dashboard body in its native stored schema — classic v1 JSON
// (panels[]/templating.list) or the v2 spec (elements/layout/variables) —
// never a lossy down-conversion.
type dashboardResult struct {
	// Spec is the dashboard body (the k8s `spec`, or the legacy `dashboard` JSON).
	Spec map[string]interface{}
	// APIVersion is the stored schema version (e.g. "v1beta1", "v2beta1"). Empty
	// when fetched via the legacy API.
	APIVersion string
	// IsV2 is true when the dashboard is stored in the v2 schema.
	IsV2 bool
	// Meta carries dashboard metadata (folder UID, etc.).
	Meta *models.DashboardMeta
	// Object is the full Kubernetes object (metadata, resourceVersion, status)
	// when fetched via the k8s API; nil on the legacy path. Needed for writes.
	Object map[string]interface{}
}

// DashboardResponse is the get_dashboard_by_uid tool output. It preserves the
// legacy `dashboard`/`meta` fields and adds `apiVersion`/`isV2` so the agent
// knows which schema (v1 classic vs v2 elements/layout) it received.
type DashboardResponse struct {
	Dashboard  interface{}           `json:"dashboard"`
	Meta       *models.DashboardMeta `json:"meta,omitempty"`
	APIVersion string                `json:"apiVersion,omitempty"`
	IsV2       bool                  `json:"isV2"`
}

func getDashboardByUID(ctx context.Context, args GetDashboardByUIDParams) (*DashboardResponse, error) {
	res, err := fetchDashboard(ctx, args.UID)
	if err != nil {
		return nil, err
	}
	return &DashboardResponse{
		Dashboard:  res.Spec,
		Meta:       res.Meta,
		APIVersion: res.APIVersion,
		IsV2:       res.IsV2,
	}, nil
}

// fetchDashboard retrieves a dashboard in its native stored schema. It uses the
// Kubernetes-style dashboard.grafana.app API (which can return native v2) when
// that API serves v1beta1 — the marker for a Grafana new enough to store v2
// dashboards — and otherwise falls back to the legacy REST API (which is
// lossless on older Grafana, where no v2 dashboards exist). The capability is
// discovered once per client (connection) and cached.
//
// It fails closed on an inconclusive capability check: if discovery errors
// (rather than definitively reporting the group/version absent), we do NOT fall
// back to the legacy API, because doing so on a v2-capable Grafana would read —
// and, for update_dashboard, write — a lossy v1 conversion that corrupts the
// stored v2 dashboard. Only a definitive "v1beta1 not served" routes to legacy.
func fetchDashboard(ctx context.Context, uid string) (*dashboardResult, error) {
	if k8s := mcpgrafana.KubernetesClientFromContext(ctx); k8s != nil {
		versions, err := k8s.GroupVersions(ctx, dashboardAPIGroup)
		if err != nil {
			return nil, fmt.Errorf("determine %s capability: %w", dashboardAPIGroup, err)
		}
		if slices.Contains(versions, dashboardReadVersion) {
			return fetchDashboardViaK8s(ctx, k8s, uid)
		}
	}
	return fetchDashboardLegacy(ctx, uid)
}

// fetchDashboardViaK8s fetches a dashboard through the dashboard.grafana.app API.
// It requests v1beta1 first (one round-trip for the common classic case), then
// re-fetches at the stored version when the dashboard is actually stored as v2.
// Callers only reach this after confirming v1beta1 is served, so a 404 here is a
// genuine "dashboard not found".
func fetchDashboardViaK8s(ctx context.Context, k8s *mcpgrafana.KubernetesClient, uid string) (*dashboardResult, error) {
	ns, nsFromSettings := mcpgrafana.DashboardNamespace(ctx)

	obj, err := k8s.Get(ctx, dashboardDescriptor(dashboardReadVersion), ns, uid)
	if err != nil {
		return nil, k8sDashboardErr("get", uid, ns, nsFromSettings, err)
	}

	// If the dashboard is stored as v2, the v1beta1 response is a lossy
	// down-conversion (status.conversion.storedVersion is set even when the
	// conversion did not error); re-fetch at the stored version to get the
	// native object.
	if storedVersion := k8sNestedString(obj, "status", "conversion", "storedVersion"); strings.HasPrefix(storedVersion, "v2") {
		nativeObj, err := k8s.Get(ctx, dashboardDescriptor(storedVersion), ns, uid)
		if err != nil {
			return nil, fmt.Errorf("get native %s dashboard via k8s api %s: %w", storedVersion, uid, err)
		}
		obj = nativeObj
	}

	return dashboardResultFromK8s(obj)
}

// dashboardDescriptor builds a ResourceDescriptor for the dashboards resource at
// the given API version.
func dashboardDescriptor(version string) mcpgrafana.ResourceDescriptor {
	return mcpgrafana.ResourceDescriptor{
		Group:    dashboardAPIGroup,
		Version:  version,
		Resource: "dashboards",
	}
}

// dashboardResultFromK8s converts a Kubernetes dashboard object into a
// dashboardResult, extracting the native spec, version, and folder metadata.
func dashboardResultFromK8s(obj map[string]interface{}) (*dashboardResult, error) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("k8s dashboard response missing or invalid 'spec'")
	}

	// Version: "dashboard.grafana.app/v2beta1" -> "v2beta1".
	version := ""
	if apiVersion, ok := obj["apiVersion"].(string); ok {
		if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
			version = apiVersion[i+1:]
		} else {
			version = apiVersion
		}
	}

	meta := &models.DashboardMeta{}
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		if name, ok := metadata["name"].(string); ok && name != "" {
			// Classic (v1) dashboards expect the uid inside the body; the v2
			// spec carries identity in metadata, so only set it for v1 shapes.
			if _, hasElements := spec["elements"]; !hasElements {
				spec["uid"] = name
			}
		}
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			if folderUID, ok := annotations["grafana.app/folder"].(string); ok {
				meta.FolderUID = folderUID
			}
		}
	}

	return &dashboardResult{
		Spec:       spec,
		APIVersion: version,
		IsV2:       strings.HasPrefix(version, "v2"),
		Meta:       meta,
		Object:     obj,
	}, nil
}

// fetchDashboardLegacy fetches a dashboard via the legacy REST API. The result
// is always classic v1 JSON (the legacy endpoint down-converts v2 dashboards).
func fetchDashboardLegacy(ctx context.Context, uid string) (*dashboardResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	dashboard, err := c.Dashboards.GetDashboardByUIDWithParams(
		dashboards.NewGetDashboardByUIDParamsWithContext(ctx).WithUID(uid),
	)
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid %s: %w", uid, err)
	}
	spec, _ := dashboard.Payload.Dashboard.(map[string]interface{})
	return &dashboardResult{
		Spec: spec,
		Meta: dashboard.Payload.Meta,
	}, nil
}

// k8sNestedString walks a nested map[string]interface{} by keys and returns the
// string at the end, or "" if any step is missing or not the expected type.
func k8sNestedString(obj map[string]interface{}, keys ...string) string {
	current := obj
	for i, key := range keys {
		if i == len(keys)-1 {
			s, _ := current[key].(string)
			return s
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

// PatchOperation represents a single patch operation
type PatchOperation struct {
	Op    string      `json:"op" jsonschema:"required,description=Operation type: 'replace'\\, 'add'\\, 'remove'"`
	Path  string      `json:"path" jsonschema:"required,description=JSONPath to the property to modify. Supports: '$.title'\\, '$.panels[0].title'\\, '$.panels[0].targets[0].expr'\\, '$.panels[1].targets[0].datasource'\\, '$.templating.list/-' (append a variable)\\, '$.annotations.list/-' (append a saved dashboard annotation query/definition). For appending to arrays\\, use '/- ' syntax: '$.panels/- ' (append to panels array) or '$.panels[2]/- ' (append to nested array at index 2)."`
	Value interface{} `json:"value,omitempty" jsonschema:"description=New value for replace/add operations. When adding a saved dashboard annotation query/definition\\, append an object to '$.annotations.list' rather than calling create_annotation."`
}

type UpdateDashboardParams struct {
	// For full dashboard updates (creates new dashboards or complete rewrites)
	Dashboard map[string]interface{} `json:"dashboard,omitempty" jsonschema:"description=The full dashboard JSON. Use for creating new dashboards or complete updates. Saved dashboard annotation queries/definitions live in 'annotations.list' inside this JSON; they are different from annotation events created with create_annotation. Large dashboards consume significant context - consider using patches for small changes."`

	// For targeted updates using patch operations (preferred for existing dashboards)
	UID        string           `json:"uid,omitempty" jsonschema:"description=UID of existing dashboard to update. Must be used together with 'operations'. Providing 'uid' without 'operations' will fail."`
	Operations []PatchOperation `json:"operations,omitempty" jsonschema:"description=Array of patch operations for targeted updates. More efficient than full dashboard JSON for small changes. Common paths: '$.templating.list/-' to add a variable\\, '$.annotations.list/-' to add a saved dashboard annotation query/definition\\, '$.panels[0].targets[0].expr' to replace a panel query."`

	// Common parameters
	FolderUID string `json:"folderUid,omitempty" jsonschema:"description=The UID of the dashboard's folder"`
	Message   string `json:"message,omitempty" jsonschema:"description=Set a commit message for the version history"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=Overwrite the dashboard if it exists. Otherwise create one"`
	UserID    int64  `json:"userId,omitempty" jsonschema:"description=ID of the user making the change"`
}

// updateDashboard intelligently handles dashboard updates using either full JSON or patch operations.
// It automatically uses the most efficient approach based on the provided parameters.
func updateDashboard(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	// Determine the update strategy based on provided parameters
	if len(args.Operations) > 0 && args.UID != "" {
		// Patch-based update: fetch current dashboard and apply operations
		return updateDashboardWithPatches(ctx, args)
	} else if args.Dashboard != nil {
		// Full dashboard update: use the provided JSON
		return updateDashboardWithFullJSON(ctx, args)
	} else if args.UID != "" && len(args.Operations) == 0 {
		return nil, fmt.Errorf("'uid' was provided without 'operations'. To update an existing dashboard, provide both 'uid' and 'operations' (array of patch operations). To replace a dashboard entirely, provide 'dashboard' (full JSON) instead")
	} else if len(args.Operations) > 0 && args.UID == "" {
		return nil, fmt.Errorf("'operations' were provided without 'uid'. To apply patch operations, provide the 'uid' of the existing dashboard to update along with the 'operations' array")
	} else {
		return nil, fmt.Errorf("no dashboard content provided. You must use one of two modes: (1) Patch mode (preferred for existing dashboards): provide 'uid' + 'operations' array with targeted changes. (2) Full JSON mode: provide 'dashboard' with the complete dashboard object. Do NOT retry this same call — choose a mode and provide the required fields")
	}
}

// updateDashboardWithPatches applies patch operations to an existing dashboard
func updateDashboardWithPatches(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	// Sort array element remove operations from highest to lowest index to avoid index-shifting issues
	sortedOps, err := sortArrayRemovesDescending(args.Operations)
	if err != nil {
		return nil, err
	}
	args.Operations = sortedOps

	// Get the current dashboard in its native stored schema.
	res, err := fetchDashboard(ctx, args.UID)
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	dashboardMap := res.Spec
	if dashboardMap == nil {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	// Preserve the numeric ID before patching so it survives any
	// accidental mutation by patch operations (classic v1 dashboards only).
	origID := dashboardMap["id"]

	// Apply each patch operation. The JSONPath engine is schema-agnostic, so
	// the same code patches both classic v1 bodies and v2 specs; only the paths
	// differ (e.g. $.panels[0].title vs $.elements.<name>.spec.title).
	for i, op := range args.Operations {
		switch op.Op {
		case "replace", "add":
			if err := applyJSONPath(dashboardMap, op.Path, op.Value, false); err != nil {
				return nil, fmt.Errorf("operation %d (%s at %s): %w", i, op.Op, op.Path, err)
			}
		case "remove":
			if err := applyJSONPath(dashboardMap, op.Path, nil, true); err != nil {
				return nil, fmt.Errorf("operation %d (%s at %s): %w", i, op.Op, op.Path, err)
			}
		default:
			return nil, fmt.Errorf("operation %d: unsupported operation '%s'", i, op.Op)
		}
	}

	// v2 dashboards are written back through the Kubernetes API to preserve the
	// native schema (the legacy save endpoint would down-convert to v1).
	if res.IsV2 {
		return updateDashboardV2(ctx, args.UID, res, args)
	}

	// Restore identity fields so the Grafana API updates the existing
	// dashboard in place instead of creating a clone with a new UID.
	// The UID is always taken from the request args (the value used to
	// fetch the dashboard) to guarantee consistency even when the
	// dashboard body returned by the API did not include it.
	dashboardMap["uid"] = args.UID
	if origID != nil {
		dashboardMap["id"] = origID
	}

	// Use the folder UID from the existing dashboard if not provided
	folderUID := args.FolderUID
	if folderUID == "" && res.Meta != nil {
		folderUID = res.Meta.FolderUID
	}

	// Update with the patched dashboard
	return updateDashboardWithFullJSON(ctx, UpdateDashboardParams{
		Dashboard: dashboardMap,
		FolderUID: folderUID,
		Message:   args.Message,
		Overwrite: true,
		UserID:    args.UserID,
	})
}

// updateDashboardV2 writes a (patched) v2 dashboard back through the
// dashboard.grafana.app Kubernetes API, preserving the native v2 schema. The
// supplied dashboardResult carries the full k8s object (including
// metadata.resourceVersion) whose spec has already been mutated in place.
func updateDashboardV2(ctx context.Context, uid string, res *dashboardResult, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	k8s := mcpgrafana.KubernetesClientFromContext(ctx)
	if k8s == nil {
		return nil, fmt.Errorf("a Kubernetes-capable Grafana is required to update a v2 dashboard, but no k8s client is available")
	}
	if res.Object == nil {
		return nil, fmt.Errorf("missing Kubernetes object for v2 dashboard update")
	}

	// res.Spec aliases res.Object["spec"], so the applied patches are already
	// reflected; set it explicitly to be safe against future refactors.
	res.Object["spec"] = res.Spec

	// Honor folderUid (move) and message (version-history note) via annotations.
	applyV2WriteMetadata(res.Object, args)

	ns, nsFromSettings := mcpgrafana.DashboardNamespace(ctx)
	updated, err := k8s.Update(ctx, dashboardDescriptor(res.APIVersion), ns, uid, res.Object)
	if err != nil {
		return nil, k8sDashboardErr("update", uid, ns, nsFromSettings, err)
	}
	return postDashboardOKBodyFromK8s(updated, uid), nil
}

// postDashboardOKBodyFromK8s builds a legacy-shaped save response from a k8s
// dashboard object, so update_dashboard returns a consistent result regardless
// of which API backend performed the write.
func postDashboardOKBodyFromK8s(obj map[string]interface{}, uid string) *models.PostDashboardOKBody {
	status := "success"
	body := &models.PostDashboardOKBody{
		UID:    &uid,
		Status: &status,
	}
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		// The legacy dashboard "version" is the k8s metadata.generation: Grafana's
		// own unstructured->legacy conversion does exactly this mapping
		// (spec["version"] = obj.GetGeneration()), so the value here matches what a
		// legacy read elsewhere would report.
		if generation, ok := metadata["generation"].(float64); ok {
			version := int64(generation)
			body.Version = &version
		}
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			if folderUID, ok := annotations[annoKeyFolder].(string); ok {
				body.FolderUID = folderUID
			}
		}
	}
	return body
}

// Grafana app-platform metadata annotations (see grafana/pkg/apimachinery/utils).
const (
	annoKeyFolder  = "grafana.app/folder"  // folder UID (v2 equivalent of folderUid)
	annoKeyMessage = "grafana.app/message" // commit/version-history message
)

// setDashboardAnnotation sets a metadata annotation on a k8s dashboard object,
// creating the metadata/annotations maps if needed.
func setDashboardAnnotation(obj map[string]interface{}, key, value string) {
	metadata, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		metadata = map[string]interface{}{}
		obj["metadata"] = metadata
	}
	annotations, ok := metadata["annotations"].(map[string]interface{})
	if !ok {
		annotations = map[string]interface{}{}
		metadata["annotations"] = annotations
	}
	annotations[key] = value
}

// applyV2WriteMetadata applies the supplementary update_dashboard parameters that
// the v2 (k8s) write path supports via metadata annotations: folderUid moves the
// dashboard, and message records a version-history note (the app-platform
// equivalents of the legacy save command fields).
func applyV2WriteMetadata(obj map[string]interface{}, args UpdateDashboardParams) {
	if args.FolderUID != "" {
		setDashboardAnnotation(obj, annoKeyFolder, args.FolderUID)
	}
	if args.Message != "" {
		setDashboardAnnotation(obj, annoKeyMessage, args.Message)
	}
}

// isK8sNotFound reports whether err is (wraps) a Kubernetes API 404.
func isK8sNotFound(err error) bool {
	var apiErr *mcpgrafana.KubernetesAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}

// k8sDashboardErr wraps a dashboard k8s API error. When it is a 404 and the
// namespace was only derived from the org id (frontend settings was
// unavailable), it adds a hint that the namespace itself may be wrong (e.g.
// Grafana Cloud uses "stacks-{id}") rather than the dashboard being absent — so
// the same diagnostic applies to both reads and writes.
func k8sDashboardErr(action, uid, ns string, nsFromSettings bool, err error) error {
	if isK8sNotFound(err) && !nsFromSettings {
		return fmt.Errorf("%s dashboard %q: not found in namespace %q, which was derived from the org id because /api/frontend/settings was unavailable; the namespace may be wrong (e.g. Grafana Cloud uses \"stacks-{id}\"): %w", action, uid, ns, err)
	}
	return fmt.Errorf("%s dashboard %q via k8s api: %w", action, uid, err)
}

// updateDashboardWithFullJSON performs a traditional full dashboard update
func updateDashboardWithFullJSON(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	// A full dashboard JSON in the v2 schema (elements/layout) must be written
	// through the Kubernetes API; the legacy save endpoint only accepts v1.
	if isV2DashboardJSON(args.Dashboard) {
		return createOrUpdateDashboardV2(ctx, args)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	cmd := &models.SaveDashboardCommand{
		Dashboard: args.Dashboard,
		FolderUID: args.FolderUID,
		Message:   args.Message,
		Overwrite: args.Overwrite,
		UserID:    args.UserID,
	}
	dashboard, err := c.Dashboards.PostDashboardWithParams(
		dashboards.NewPostDashboardParamsWithContext(ctx).WithBody(cmd),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to save dashboard: %w", err)
	}
	return dashboard.Payload, nil
}

// isV2DashboardJSON reports whether a full dashboard JSON body uses the v2
// schema, identified by the top-level `elements` or `layout` keys that replace
// the classic `panels[]`.
func isV2DashboardJSON(dashboard map[string]interface{}) bool {
	if dashboard == nil {
		return false
	}
	_, hasElements := dashboard["elements"]
	_, hasLayout := dashboard["layout"]
	return hasElements || hasLayout
}

// createOrUpdateDashboardV2 creates or replaces a dashboard from a full v2 spec
// via the dashboard.grafana.app Kubernetes API. When the spec carries a uid that
// already exists, it performs an in-place update (preserving resourceVersion);
// otherwise it creates a new dashboard.
func createOrUpdateDashboardV2(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	k8s := mcpgrafana.KubernetesClientFromContext(ctx)
	if k8s == nil {
		return nil, fmt.Errorf("a Kubernetes-capable Grafana is required to save a v2 dashboard, but no k8s client is available")
	}

	// The v2 spec carries identity in metadata, not the body; lift and strip a
	// uid if one was provided in the JSON. Copy the map first so we don't mutate
	// the caller's input (which it may reuse for retries/follow-ups).
	spec := maps.Clone(args.Dashboard)
	uid := args.UID
	if uid == "" {
		if u, ok := spec["uid"].(string); ok {
			uid = u
		}
	}
	delete(spec, "uid")

	ns, nsFromSettings := mcpgrafana.DashboardNamespace(ctx)
	// TODO: negotiate the version to write instead of hardcoding v2beta1 — e.g.
	// discover the group's preferred/served v2 version via GET /apis/dashboard.grafana.app
	// (it could be v2, v2beta1, v2alpha1 depending on the Grafana version) rather
	// than assuming v2beta1. An existing v2 dashboard reuses its own stored version
	// below; this default only applies when creating a brand-new dashboard.
	version := "v2beta1"

	// If a uid is given and the dashboard already exists, update it in place
	// using its current object (for resourceVersion); if it genuinely does not
	// exist, create it. A non-404 error (transient/permission) must NOT be
	// treated as "absent" — that would turn an update into a create/conflict and
	// hide the real failure — so surface it.
	if uid != "" {
		existing, err := fetchDashboardViaK8s(ctx, k8s, uid)
		switch {
		case err == nil && existing.Object != nil:
			// Mirror the legacy save: refuse to replace an existing dashboard
			// unless overwrite was requested.
			if !args.Overwrite {
				return nil, fmt.Errorf("dashboard %q already exists; set overwrite=true to replace it", uid)
			}
			// Grafana pins a dashboard's stored schema version at creation: a
			// dashboard stored as classic v1 cannot be replaced in place with a v2
			// (elements/layout) body — Grafana silently down-converts it back to v1,
			// dropping any v2-only content. Reject rather than lose data; the caller
			// should create a new dashboard for v2.
			if !existing.IsV2 {
				return nil, fmt.Errorf("dashboard %q is stored as classic v1 and cannot be replaced in place with a v2 (elements/layout) body: Grafana pins the stored schema version, so the v2 content would be silently down-converted to v1. Create a new dashboard (a different uid) for the v2 version instead", uid)
			}
			obj := existing.Object
			obj["spec"] = spec
			version = existing.APIVersion // an existing v2 dashboard's stored v2 version
			applyV2WriteMetadata(obj, args)
			updated, err := k8s.Update(ctx, dashboardDescriptor(version), ns, uid, obj)
			if err != nil {
				return nil, k8sDashboardErr("update", uid, ns, nsFromSettings, err)
			}
			return postDashboardOKBodyFromK8s(updated, uid), nil
		case err != nil && !isK8sNotFound(err):
			return nil, fmt.Errorf("check existing v2 dashboard %s: %w", uid, err)
		}
		// not found — fall through to create
	}

	// Create a new dashboard object.
	metadata := map[string]interface{}{"namespace": ns}
	if uid != "" {
		metadata["name"] = uid
	}
	obj := map[string]interface{}{
		"apiVersion": dashboardAPIGroup + "/" + version,
		"kind":       "Dashboard",
		"metadata":   metadata,
		"spec":       spec,
	}
	applyV2WriteMetadata(obj, args)
	created, err := k8s.Create(ctx, dashboardDescriptor(version), ns, obj)
	if err != nil {
		return nil, k8sDashboardErr("create", uid, ns, nsFromSettings, err)
	}
	createdUID := uid
	if createdUID == "" {
		createdUID = k8sNestedString(created, "metadata", "name")
	}
	return postDashboardOKBodyFromK8s(created, createdUID), nil
}

// sortArrayRemovesDescending reorders remove operations on the same array
// from highest index to lowest. This prevents the index-shifting footgun
// where removing a lower index first causes subsequent operations to target wrong elements.
// It also rejects duplicate indices on the same array (likely an LLM mistake).
func sortArrayRemovesDescending(operations []PatchOperation) ([]PatchOperation, error) {
	type arrayRemoveInfo struct {
		arrayPath string
		index     int
		opIndex   int
	}

	// Collect array remove operations grouped by array path
	removesByArray := make(map[string][]arrayRemoveInfo)

	for i, op := range operations {
		if op.Op != "remove" {
			continue
		}

		// Parse the path to check if it's an array element removal
		path := op.Path
		if len(path) > 2 && path[:2] == "$." {
			path = path[2:]
		}

		segments := parseJSONPath(path)
		if len(segments) == 0 {
			continue
		}

		// Check if the final segment is an array access
		finalSeg := segments[len(segments)-1]
		if !finalSeg.IsArray || finalSeg.IsAppend {
			continue
		}

		// Build the array path (everything except the index)
		arrayPath := ""
		for j, seg := range segments {
			if j > 0 {
				arrayPath += "."
			}
			arrayPath += seg.Key
			if seg.IsArray && j < len(segments)-1 {
				arrayPath += fmt.Sprintf("[%d]", seg.Index)
			}
		}

		removesByArray[arrayPath] = append(removesByArray[arrayPath], arrayRemoveInfo{
			arrayPath: arrayPath,
			index:     finalSeg.Index,
			opIndex:   i,
		})
	}

	// Check for duplicate indices and sort each group descending
	for arrayPath, removes := range removesByArray {
		// Check for duplicate indices
		seen := make(map[int]bool)
		for _, r := range removes {
			if seen[r.index] {
				return nil, fmt.Errorf("duplicate remove at index %d on '%s'; each index should only be removed once", r.index, arrayPath)
			}
			seen[r.index] = true
		}

		// Sort descending by index
		sort.Slice(removes, func(i, j int) bool {
			return removes[i].index > removes[j].index
		})
		removesByArray[arrayPath] = removes
	}

	// Rebuild the operations slice with array removes reordered
	result := make([]PatchOperation, len(operations))
	copy(result, operations)

	for _, removes := range removesByArray {
		if len(removes) <= 1 {
			continue
		}
		// Collect the original positions of these remove ops
		positions := make([]int, len(removes))
		for i, r := range removes {
			positions[i] = r.opIndex
		}
		// Sort positions ascending so we can place sorted removes in order
		sort.Ints(positions)
		// Place the sorted (descending by index) removes into the original positions
		for i, pos := range positions {
			result[pos] = operations[removes[i].opIndex]
		}
	}

	return result, nil
}

var GetDashboardByUID = mcpgrafana.MustTool(
	"get_dashboard_by_uid",
	"Retrieves the complete dashboard, including panels, variables, and settings, for a specific dashboard identified by its UID. The response includes 'apiVersion' and 'isV2': when 'isV2' is true the dashboard uses the v2 schema (panels live under 'elements' keyed by name, arranged by 'layout'; variables under 'variables'), otherwise it is classic v1 ('panels[]' with 'templating.list'). WARNING: Large dashboards can consume significant context window space. Consider using get_dashboard_summary for overview or get_dashboard_property for specific data instead.",
	getDashboardByUID,
	mcp.WithTitleAnnotation("Get dashboard details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var UpdateDashboard = mcpgrafana.MustTool(
	"update_dashboard",
	"Create or update a dashboard. Two modes: (1) Full JSON — provide 'dashboard' for new dashboards or complete replacements. (2) Patch — provide 'uid' + 'operations' to make targeted changes to an existing dashboard. One of these two modes is required; 'folderUid'\\, 'message'\\, and 'overwrite' are supplementary and do nothing on their own. Dashboard authoring guidance: if a saved query must support one\\, many\\, or All values from a multi-select variable inside a regex expression or matcher\\, save '${var:regex}' rather than plain '$var'. Saved dashboard annotation queries/definitions must be written into dashboard JSON under 'annotations.list'; the create_annotation tool creates annotation events and does not add a reusable dashboard annotation query/definition to the saved dashboard. For stat panels over the current dashboard range\\, make the query return the range-level result the stat should display; panel-side reduction only reduces returned series and does not compute peak-over-range or ratio-of-peaks semantics for you. Patch operations support JSONPaths like '$.panels[0].targets[0].expr'\\, '$.panels[1].title'\\, '$.panels[2].targets[0].datasource'\\, '$.templating.list/-'\\, and '$.annotations.list/-'. Append to arrays with '/- ' syntax: '$.panels/- '. Remove by index: {\"op\": \"remove\"\\, \"path\": \"$.panels[2]\"}. Multiple removes on the same array are automatically reordered to avoid index-shifting issues. Note: only numeric array indices are supported in patch paths; filter expressions like [?(@.id==2)] and wildcards like [*] are not supported. v2 dashboards (check 'isV2' from get_dashboard_by_uid) use a different shape: patch '$.elements.<name>.spec.title' or '$.elements.<name>.spec.data.spec.queries[0].spec' and edit '$.variables'/'$.layout' rather than '$.panels'/'$.templating.list'. Full-JSON saves containing top-level 'elements'/'layout' are written as v2 and require a Kubernetes-capable Grafana. After creating or updating a dashboard\\, verify that panel queries return data by using `run_panel_query` or the appropriate query tool (`query_prometheus`\\, `query_loki_logs`\\, etc.) to validate expressions before considering the task complete.",
	updateDashboard,
	mcp.WithTitleAnnotation("Create or update dashboard"),
	mcp.WithDestructiveHintAnnotation(true),
)

type DashboardPanelQueriesParams struct {
	UID       string            `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	PanelID   *int              `json:"panelId,omitempty" jsonschema:"description=Optional panel ID to filter to a specific panel"`
	Variables map[string]string `json:"variables,omitempty" jsonschema:"description=Optional variable substitutions (e.g.\\, {\"job\": \"api-server\"})"`
}

type datasourceInfo struct {
	UID  string `json:"uid"`
	Type string `json:"type"`
}

type panelQuery struct {
	Title             string         `json:"title"`
	Query             string         `json:"query"`
	ProcessedQuery    string         `json:"processedQuery,omitempty"`
	Datasource        datasourceInfo `json:"datasource"`
	RefID             string         `json:"refId,omitempty"`
	RequiredVariables []VariableInfo `json:"requiredVariables,omitempty"`
}

func GetDashboardPanelQueriesTool(ctx context.Context, args DashboardPanelQueriesParams) ([]panelQuery, error) {
	res, err := fetchDashboard(ctx, args.UID)
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	db := res.Spec
	if db == nil {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	if res.IsV2 {
		return getPanelQueriesV2(db, args)
	}

	// Determine if variable processing is needed
	var dashboardVars map[string]VariableInfo
	if args.Variables != nil {
		dashboardVars = extractDashboardVariables(db)
	}

	// Determine which panels to process
	var panels []map[string]interface{}
	if args.PanelID != nil {
		panel, err := findPanelByID(db, *args.PanelID)
		if err != nil {
			return nil, err
		}
		panels = []map[string]interface{}{panel}
	} else {
		panels = collectAllPanels(db)
	}

	result := make([]panelQuery, 0)
	for _, panel := range panels {
		queries := extractPanelQueries(panel, dashboardVars, args.Variables)
		result = append(result, queries...)
	}

	return result, nil
}

var GetDashboardPanelQueries = mcpgrafana.MustTool(
	"get_dashboard_panel_queries",
	"Retrieve panel queries from a Grafana dashboard. Supports all datasource types (Prometheus, Loki, CloudWatch, SQL, etc.) and row-nested panels. Optionally filter to a specific panel by ID with `panelId`. Optionally provide `variables` for template variable substitution, which populates `processedQuery` and `requiredVariables` fields. Returns an array of objects with fields: title, query (raw expression), datasource (object with uid and type), and optionally processedQuery, refId, and requiredVariables.",
	GetDashboardPanelQueriesTool,
	mcp.WithTitleAnnotation("Get dashboard panel queries"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// GetDashboardPropertyParams defines parameters for getting specific dashboard properties
type GetDashboardPropertyParams struct {
	UID      string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	JSONPath string `json:"jsonPath" jsonschema:"required,description=JSONPath expression to extract specific data (e.g.\\, '$.panels[0].title' for first panel title\\, '$.panels[*].title' for all panel titles\\, '$.templating.list' for variables\\, '$.annotations.list' for saved dashboard annotation queries/definitions)"`
}

// getDashboardProperty retrieves specific parts of a dashboard using JSONPath expressions.
// This helps reduce context window usage by fetching only the needed data.
func getDashboardProperty(ctx context.Context, args GetDashboardPropertyParams) (interface{}, error) {
	res, err := fetchDashboard(ctx, args.UID)
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	// Convert dashboard to JSON for JSONPath processing. The spec is in its
	// native schema (classic v1 or v2), so v2 paths target elements/layout.
	dashboardJSON, err := json.Marshal(res.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal dashboard to JSON: %w", err)
	}

	var dashboardData interface{}
	if err := json.Unmarshal(dashboardJSON, &dashboardData); err != nil {
		return nil, fmt.Errorf("unmarshal dashboard JSON: %w", err)
	}

	// Apply JSONPath expression
	builder := gval.Full(jsonpath.Language())
	path, err := builder.NewEvaluable(args.JSONPath)
	if err != nil {
		return nil, fmt.Errorf("create JSONPath evaluable '%s': %w", args.JSONPath, err)
	}

	result, err := path(ctx, dashboardData)
	if err != nil {
		return nil, fmt.Errorf("apply JSONPath '%s': %w", args.JSONPath, err)
	}

	return result, nil
}

var GetDashboardProperty = mcpgrafana.MustTool(
	"get_dashboard_property",
	"Get specific parts of a dashboard using JSONPath expressions to minimize context window usage. JSONPath targets the dashboard's native schema. Classic v1 paths: '$.title' (title)\\, '$.panels[*].title' (all panel titles)\\, '$.panels[0]' (first panel)\\, '$.templating.list' (variables)\\, '$.annotations.list' (saved dashboard annotation queries/definitions)\\, '$.tags' (tags)\\, '$.panels[*].targets[*].expr' (all queries). v2 dashboards (see isV2 from get_dashboard_by_uid) use different paths: '$.title'\\, '$.elements' (panels\\, keyed by name)\\, '$.variables' (variables)\\, '$.annotations'. Use this instead of get_dashboard_by_uid when you only need specific dashboard properties.",
	getDashboardProperty,
	mcp.WithTitleAnnotation("Get dashboard property"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// GetDashboardSummaryParams defines parameters for getting a dashboard summary
type GetDashboardSummaryParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

// DashboardSummary provides a compact overview of a dashboard without the full JSON
type DashboardSummary struct {
	UID         string                `json:"uid"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	PanelCount  int                   `json:"panelCount"`
	Panels      []PanelSummary        `json:"panels"`
	Variables   []VariableSummary     `json:"variables,omitempty"`
	TimeRange   TimeRangeSummary      `json:"timeRange"`
	Refresh     string                `json:"refresh,omitempty"`
	Meta        *models.DashboardMeta `json:"meta,omitempty"`
}

type PanelSummary struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	QueryCount  int    `json:"queryCount"`
}

type VariableSummary struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

type TimeRangeSummary struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// getDashboardSummary provides a compact overview of a dashboard to help with context management
func getDashboardSummary(ctx context.Context, args GetDashboardSummaryParams) (*DashboardSummary, error) {
	res, err := fetchDashboard(ctx, args.UID)
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	db := res.Spec
	if db == nil {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	if res.IsV2 {
		return dashboardSummaryV2(db, args.UID, res.Meta)
	}

	summary := &DashboardSummary{
		UID:  args.UID,
		Meta: res.Meta,
	}

	// Extract basic info using helper functions
	extractBasicDashboardInfo(db, summary)

	// Extract time range
	summary.TimeRange = extractTimeRange(db)

	// Extract panel summaries. Modern dashboards put panels at the top
	// level; legacy schemaVersion <= 14 dashboards put them under
	// "rows":[{panels:[...]}] with no top-level "panels".
	if panels := safeArray(db, "panels"); panels != nil {
		summary.PanelCount = len(panels)
		for _, p := range panels {
			if panelObj, ok := p.(map[string]interface{}); ok {
				summary.Panels = append(summary.Panels, extractPanelSummary(panelObj))
			}
		}
	} else if rows := safeArray(db, "rows"); rows != nil {
		for _, r := range rows {
			row, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			for _, p := range safeArray(row, "panels") {
				if panelObj, ok := p.(map[string]interface{}); ok {
					summary.Panels = append(summary.Panels, extractPanelSummary(panelObj))
				}
			}
		}
		summary.PanelCount = len(summary.Panels)
	}

	// Extract variable summaries
	if templating := safeObject(db, "templating"); templating != nil {
		if list := safeArray(templating, "list"); list != nil {
			for _, v := range list {
				if variable, ok := v.(map[string]interface{}); ok {
					summary.Variables = append(summary.Variables, extractVariableSummary(variable))
				}
			}
		}
	}

	return summary, nil
}

var GetDashboardSummary = mcpgrafana.MustTool(
	"get_dashboard_summary",
	"Get a compact summary of a dashboard including title\\, panel count\\, panel types\\, variables\\, and other metadata without the full JSON. Use this for dashboard overview and planning modifications without consuming large context windows.",
	getDashboardSummary,
	mcp.WithTitleAnnotation("Get dashboard summary"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// applyJSONPath applies a value to a JSONPath or removes it if remove=true
func applyJSONPath(data map[string]interface{}, path string, value interface{}, remove bool) error {
	// Trim whitespace to handle paths like "$.panels/- " (trailing space)
	path = strings.TrimSpace(path)

	// Remove the leading "$." if present
	if len(path) > 2 && path[:2] == "$." {
		path = path[2:]
	}

	// Detect unsupported JSONPath syntax and return actionable errors
	if strings.Contains(path, "?(@") || strings.Contains(path, "?(") {
		return fmt.Errorf("JSONPath filter expressions (e.g., [?(@.id==2)]) are not supported in patch operations. Use numeric array indices instead (e.g., $.panels[1]). Use get_dashboard_summary to find panel array indices")
	}
	if strings.Contains(path, "[*]") {
		return fmt.Errorf("JSONPath wildcard expressions (e.g., [*]) are not supported in patch operations. Use numeric array indices instead (e.g., $.panels[0])")
	}

	// Split the path into segments
	segments := parseJSONPath(path)
	if len(segments) == 0 {
		return fmt.Errorf("empty JSONPath")
	}

	// Navigate to the parent of the target
	current := data
	for i, segment := range segments[:len(segments)-1] {
		next, err := navigateSegment(current, segment)
		if err != nil {
			return fmt.Errorf("at segment %d (%s): %w", i, segment.String(), err)
		}
		current = next
	}

	// Apply the final operation
	finalSegment := segments[len(segments)-1]
	if remove {
		return removeAtSegment(current, finalSegment)
	}
	return setAtSegment(current, finalSegment, value)
}

// JSONPathSegment represents a segment of a JSONPath
type JSONPathSegment struct {
	Key      string
	Index    int
	IsArray  bool
	IsAppend bool // true when using /- syntax to append to array
}

func (s JSONPathSegment) String() string {
	if s.IsAppend {
		return fmt.Sprintf("%s/-", s.Key)
	}
	if s.IsArray {
		return fmt.Sprintf("%s[%d]", s.Key, s.Index)
	}
	return s.Key
}

// parseJSONPath parses a JSONPath string into segments
// Supports paths like "panels[0].targets[1].expr", "title", "templating.list[0].name"
// Also supports append syntax: "panels/-" or "panels[2]/-"
func parseJSONPath(path string) []JSONPathSegment {
	var segments []JSONPathSegment

	// Handle empty path
	if path == "" {
		return segments
	}

	// Enhanced regex to handle /- append syntax
	// Matches: key, key[index], key/-, key[index]/-
	re := regexp.MustCompile(`([^.\[\]\/]+)(?:\[(\d+)\])?(?:(\/-))?`)
	matches := re.FindAllStringSubmatch(path, -1)

	for _, match := range matches {
		if len(match) >= 2 && match[1] != "" {
			segment := JSONPathSegment{
				Key:      match[1],
				IsArray:  len(match) >= 3 && match[2] != "",
				IsAppend: len(match) >= 4 && match[3] == "/-",
			}

			if segment.IsArray && !segment.IsAppend {
				if index, err := strconv.Atoi(match[2]); err == nil {
					segment.Index = index
				}
			}

			segments = append(segments, segment)
		}
	}

	return segments
}

// validateArrayAccess validates array access for a segment
func validateArrayAccess(current map[string]interface{}, segment JSONPathSegment) ([]interface{}, error) {
	arr, ok := current[segment.Key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("field '%s' is not an array", segment.Key)
	}

	// For append operations, we don't need to validate index bounds
	if segment.IsAppend {
		return arr, nil
	}

	if segment.Index < 0 || segment.Index >= len(arr) {
		return nil, fmt.Errorf("index %d out of bounds for array '%s' (length %d)", segment.Index, segment.Key, len(arr))
	}

	return arr, nil
}

// navigateSegment navigates to the next level in the JSON structure
func navigateSegment(current map[string]interface{}, segment JSONPathSegment) (map[string]interface{}, error) {
	// Append operations can only be at the final segment
	if segment.IsAppend {
		return nil, fmt.Errorf("append operation (/- ) can only be used at the final path segment")
	}

	if segment.IsArray {
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return nil, err
		}

		// Get the object at the index
		obj, ok := arr[segment.Index].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("element at %s[%d] is not an object", segment.Key, segment.Index)
		}

		return obj, nil
	}

	// Get the object
	obj, ok := current[segment.Key].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("field '%s' is not an object", segment.Key)
	}

	return obj, nil
}

// setAtSegment sets a value at the final segment
func setAtSegment(current map[string]interface{}, segment JSONPathSegment, value interface{}) error {
	if segment.IsAppend {
		// Handle append operation: add to the end of the array
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return err
		}

		// Append the value to the array
		arr = append(arr, value)
		current[segment.Key] = arr
		return nil
	}

	if segment.IsArray {
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return err
		}

		// Set the value in the array
		arr[segment.Index] = value
		return nil
	}

	// Set the value directly
	current[segment.Key] = value
	return nil
}

// removeAtSegment removes a value at the final segment
func removeAtSegment(current map[string]interface{}, segment JSONPathSegment) error {
	if segment.IsAppend {
		return fmt.Errorf("cannot use remove operation with append syntax (/- ) at %s", segment.Key)
	}

	if segment.IsArray {
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return err
		}
		current[segment.Key] = slices.Delete(arr, segment.Index, segment.Index+1)
		return nil
	}

	delete(current, segment.Key)
	return nil
}

// Helper functions for safe type conversions and field extraction

// safeGet safely extracts a value from a map with type conversion
func safeGet[T any](data map[string]interface{}, key string, defaultVal T) T {
	if val, ok := data[key]; ok {
		if typedVal, ok := val.(T); ok {
			return typedVal
		}
	}
	return defaultVal
}

func safeString(data map[string]interface{}, key string) string {
	return safeGet(data, key, "")
}

func safeStringSlice(data map[string]interface{}, key string) []string {
	var result []string
	if arr := safeArray(data, key); arr != nil {
		for _, item := range arr {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
	}
	return result
}

func safeFloat64(data map[string]interface{}, key string) float64 {
	return safeGet(data, key, 0.0)
}

func safeInt(data map[string]interface{}, key string) int {
	return int(safeFloat64(data, key))
}

func safeObject(data map[string]interface{}, key string) map[string]interface{} {
	return safeGet(data, key, map[string]interface{}(nil))
}

func safeArray(data map[string]interface{}, key string) []interface{} {
	return safeGet(data, key, []interface{}(nil))
}

// extractBasicDashboardInfo extracts common dashboard fields
func extractBasicDashboardInfo(db map[string]interface{}, summary *DashboardSummary) {
	summary.Title = safeString(db, "title")
	summary.Description = safeString(db, "description")
	summary.Tags = safeStringSlice(db, "tags")
	summary.Refresh = safeString(db, "refresh")
}

// extractTimeRange extracts time range information
func extractTimeRange(db map[string]interface{}) TimeRangeSummary {
	timeObj := safeObject(db, "time")
	if timeObj == nil {
		return TimeRangeSummary{}
	}

	return TimeRangeSummary{
		From: safeString(timeObj, "from"),
		To:   safeString(timeObj, "to"),
	}
}

// extractPanelSummary creates a panel summary from panel data
func extractPanelSummary(panel map[string]interface{}) PanelSummary {
	summary := PanelSummary{
		ID:          safeInt(panel, "id"),
		Title:       safeString(panel, "title"),
		Type:        safeString(panel, "type"),
		Description: safeString(panel, "description"),
	}

	// Count queries
	if targets := safeArray(panel, "targets"); targets != nil {
		summary.QueryCount = len(targets)
	}

	return summary
}

// extractVariableSummary creates a variable summary from variable data
func extractVariableSummary(variable map[string]interface{}) VariableSummary {
	return VariableSummary{
		Name:  safeString(variable, "name"),
		Type:  safeString(variable, "type"),
		Label: safeString(variable, "label"),
	}
}

func AddDashboardTools(mcp *server.MCPServer, enableWriteTools bool) {
	GetDashboardByUID.Register(mcp)
	if enableWriteTools {
		UpdateDashboard.Register(mcp)
	}
	GetDashboardPanelQueries.Register(mcp)
	GetDashboardProperty.Register(mcp)
	GetDashboardSummary.Register(mcp)
}
