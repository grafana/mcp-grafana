package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/common/model"
)

type externalAlertingWriteKey struct {
	instance, uid, namespace, group string
	receiver                        bool
}

type externalAlertingWriteGate struct {
	gate chan struct{}
	refs int
}

// External APIs replace whole groups/configurations. Share a gate only for the
// same destination and remove it when its last holder or waiter finishes.
var externalAlertingWrites = struct {
	sync.Mutex
	gates map[externalAlertingWriteKey]*externalAlertingWriteGate
}{gates: make(map[externalAlertingWriteKey]*externalAlertingWriteGate)}

func (c *alertingClient) externalWriteKey(ctx context.Context, uid, namespace, group string, receiver bool) externalAlertingWriteKey {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	instance := c.baseURL.String()
	if cfg.OverrideURL != "" {
		instance = cfg.OverrideURL
	}
	// The token's default organization is not known locally and may match an
	// explicit org selector. Share the destination gate across organizations
	// rather than let implicit and explicit requests race on the same config.
	return externalAlertingWriteKey{strings.TrimRight(instance, "/"), uid, namespace, group, receiver}
}

func lockExternalAlertingWrite(ctx context.Context, key externalAlertingWriteKey) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	externalAlertingWrites.Lock()
	entry := externalAlertingWrites.gates[key]
	if entry == nil {
		entry = &externalAlertingWriteGate{gate: make(chan struct{}, 1)}
		externalAlertingWrites.gates[key] = entry
	}
	entry.refs++
	externalAlertingWrites.Unlock()
	release := func() {
		externalAlertingWrites.Lock()
		defer externalAlertingWrites.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(externalAlertingWrites.gates, key)
		}
	}
	select {
	case entry.gate <- struct{}{}:
		unlock := func() { <-entry.gate; release() }
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		return unlock, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

func validateAlertingTarget(managedBy, uid string) error {
	if managedBy != "" && managedBy != "grafana" && managedBy != "datasource" {
		return fmt.Errorf("managed_by must be grafana or datasource")
	}
	if managedBy != "datasource" && uid != "" {
		return fmt.Errorf("creating in datasource_uid requires explicit managed_by=datasource")
	}
	if uid != "" && (uid == "grafana" || !contactPointUIDPattern.MatchString(uid)) {
		return fmt.Errorf("datasource_uid must be a valid external datasource UID, not grafana")
	}
	return nil
}

func (p ManageRulesReadWriteParams) validateCreationTarget() error {
	uid := ""
	if p.DatasourceUID != nil {
		uid = *p.DatasourceUID
	}
	// Preserve the existing selector on list and ignored UID on all other
	// non-create operations. This only changes creation target selection.
	if p.Operation != "create" && p.ManagedBy != "datasource" {
		uid = ""
	}
	if err := validateAlertingTarget(p.ManagedBy, uid); err != nil {
		return err
	}
	if p.ManagedBy == "datasource" && p.Operation != "create" {
		return fmt.Errorf("managed_by=datasource supports only create, use alerting_rules_read to list external rules")
	}
	if (p.Namespace != "" || p.Expression != "" || p.EvaluationInterval != "") && p.ManagedBy != "datasource" {
		return fmt.Errorf("namespace, expression and evaluation_interval require managed_by=datasource")
	}
	return nil
}

func (p ManageRulesReadWriteParams) validateDatasourceRule() error {
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Namespace) == "" || strings.TrimSpace(p.RuleGroup) == "" || strings.TrimSpace(p.Expression) == "" {
		return fmt.Errorf("title, namespace, rule_group and expression are required for datasource-managed creation")
	}
	if p.FolderUID != "" || p.RuleUID != "" || p.Condition != "" || len(p.Data) != 0 || p.NoDataState != "" || p.ExecErrState != "" || p.IsPaused || p.NotificationSettings != nil || p.Record != nil || p.MissingSeriesEvalsToResolve != 0 || p.OrgID != 0 || p.DisableProvenance != nil {
		return fmt.Errorf("datasource-managed creation does not accept Grafana-only rule fields (folder_uid, rule_uid, condition, data, states, pause, notification_settings, record, missing_series_evals_to_resolve, org_id or disable_provenance)")
	}
	for name, value := range map[string]string{"for": p.For, "keep_firing_for": p.KeepFiringFor, "evaluation_interval": p.EvaluationInterval} {
		if value == "" {
			continue
		}
		d, err := model.ParseDuration(value)
		if err != nil || d < 0 || (name == "evaluation_interval" && d == 0) {
			return fmt.Errorf("invalid %s duration", name)
		}
	}
	return nil
}

type externalAlertingDatasource struct {
	UID      string `json:"uid"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	JSONData struct {
		PrometheusType string  `json:"prometheusType"`
		Implementation *string `json:"implementation"`
		ManageAlerts   *bool   `json:"manageAlerts"`
	} `json:"jsonData"`
}

func (d externalAlertingDatasource) supportsCreation(receiver, explicit bool) bool {
	if d.UID == "" || d.UID == "grafana" || !contactPointUIDPattern.MatchString(d.UID) {
		return false
	}
	if receiver {
		// Match Grafana's external Alertmanager proxy default. A missing or null
		// implementation means Cortex, but an explicitly empty value is invalid.
		implementation := "cortex"
		if d.JSONData.Implementation != nil {
			implementation = *d.JSONData.Implementation
		}
		return d.Type == "alertmanager" && (implementation == "mimir" || implementation == "cortex")
	}
	if d.JSONData.ManageAlerts != nil && !*d.JSONData.ManageAlerts {
		return false
	}
	return d.Type == "loki" || (d.Type == "prometheus" && (strings.EqualFold(d.JSONData.PrometheusType, "mimir") || strings.EqualFold(d.JSONData.PrometheusType, "cortex") || (explicit && d.JSONData.PrometheusType == "")))
}

// Resolve only after the caller sets managed_by=datasource, whether requested
// directly or inferred from established context. Never use isDefault to break
// ties, and never switch away from a user-specified UID on failure.
func (c *alertingClient) resolveExternalAlertingDatasource(ctx context.Context, uid string, receiver bool) (*externalAlertingDatasource, error) {
	path := "/api/datasources"
	if uid != "" {
		path += "/uid/" + url.PathEscape(uid)
	}
	body, status, err := c.externalAlertingRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("read datasource metadata: HTTP %d", status)
	}
	var candidates []externalAlertingDatasource
	if uid != "" {
		var ds externalAlertingDatasource
		if err := json.Unmarshal(body, &ds); err != nil || ds.UID != uid {
			return nil, fmt.Errorf("invalid datasource metadata for requested UID")
		}
		candidates = append(candidates, ds)
	} else if err := json.Unmarshal(body, &candidates); err != nil {
		return nil, fmt.Errorf("invalid datasource list")
	}
	compatible := make([]externalAlertingDatasource, 0, len(candidates))
	for _, ds := range candidates {
		if ds.supportsCreation(receiver, uid != "") {
			compatible = append(compatible, ds)
		}
	}
	kind := "Mimir/Cortex or Loki ruler"
	if receiver {
		kind = "Mimir/Cortex Alertmanager"
	}
	if len(compatible) == 0 {
		return nil, fmt.Errorf("no compatible %s datasource found for creation (requested UID %q), check datasource type and configuration", kind, uid)
	}
	if len(compatible) > 1 {
		names := make([]string, 0, len(compatible))
		for _, ds := range compatible {
			names = append(names, fmt.Sprintf("%q (UID %q)", ds.Name, ds.UID))
		}
		sort.Strings(names)
		return nil, fmt.Errorf("multiple compatible %s datasources: %s. Ask the user to choose, then retry with datasource_uid. Nothing was created", kind, strings.Join(names, ", "))
	}
	ds := &compatible[0]
	if !receiver && ds.Type == "prometheus" && ds.JSONData.PrometheusType == "" {
		subtype, err := c.discoverExternalRulerType(ctx, ds.UID)
		if err != nil {
			return nil, err
		}
		ds.JSONData.PrometheusType = subtype
	}
	return ds, nil
}

// Follow Grafana's buildinfo discovery for an explicitly selected datasource.
// Cortex has no buildinfo endpoint. Its ruler is checked by the subsequent
// group GET before any write. Never probe every datasource during auto-selection.
func (c *alertingClient) discoverExternalRulerType(ctx context.Context, uid string) (string, error) {
	path := "/api/datasources/proxy/uid/" + url.PathEscape(uid) + "/api/v1/status/buildinfo"
	body, status, err := c.externalAlertingRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "cortex", nil
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("discover datasource ruler type: HTTP %d, configure prometheusType explicitly", status)
	}
	var info struct {
		Status string `json:"status"`
		Data   struct {
			Features map[string]string `json:"features"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &info) != nil || info.Status != "success" {
		return "", fmt.Errorf("invalid datasource buildinfo, configure prometheusType explicitly")
	}
	if info.Data.Features["ruler_config_api"] != "true" {
		return "", fmt.Errorf("datasource does not advertise a writable Mimir ruler API, configure prometheusType explicitly for Cortex")
	}
	return "mimir", nil
}

// Do not surface backend response bodies: configuration APIs can echo secrets.
// Callers inspect status separately to distinguish a missing group from errors.
func (c *alertingClient) externalAlertingRequest(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
	u := c.baseURL.JoinPath(path)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build external alerting request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// A login redirect is not a successful configuration write. Do not replay
	// config bodies (which may contain secrets) at a redirected destination.
	client := *c.httpClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("external alerting request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	data, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read external alerting response: %w", err)
	}
	return data, resp.StatusCode, nil
}

func (c *alertingClient) postExternalAlerting(ctx context.Context, path string, query url.Values, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode external alerting configuration")
	}
	_, status, err := c.externalAlertingRequest(ctx, http.MethodPost, path, query, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("write external alerting configuration: HTTP %d, no Grafana-managed fallback attempted", status)
	}
	return nil
}

// Keep untouched configuration as raw JSON, including secrets and unknown fields.
type externalAlertingObject map[string]json.RawMessage

func decodeExternalAlertingObject(data []byte) (externalAlertingObject, error) {
	var obj externalAlertingObject
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("invalid external alerting configuration object")
	}
	return obj, nil
}

func setExternalAlertingField(obj externalAlertingObject, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("invalid external alerting field %s", key)
	}
	obj[key] = b
	return nil
}
