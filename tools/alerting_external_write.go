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

	"github.com/prometheus/common/model"
)

// External APIs replace entire groups/configurations. Serialize read-modify-write
// calls in this process. Other clients must coordinate writes separately.
var externalAlertingWriteGate = make(chan struct{}, 1)

func lockExternalAlertingWrite(ctx context.Context) (func(), error) {
	select {
	case externalAlertingWriteGate <- struct{}{}:
		return func() { <-externalAlertingWriteGate }, nil
	case <-ctx.Done():
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
	// Keep the existing datasource_uid selector for list, without changing its default.
	if p.Operation == "list" && p.ManagedBy != "datasource" {
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
		PrometheusType string `json:"prometheusType"`
		Implementation string `json:"implementation"`
		ManageAlerts   *bool  `json:"manageAlerts"`
	} `json:"jsonData"`
}

func (d externalAlertingDatasource) supportsCreation(receiver bool) bool {
	if d.UID == "" || d.UID == "grafana" || !contactPointUIDPattern.MatchString(d.UID) {
		return false
	}
	if receiver {
		return d.Type == "alertmanager" && (d.JSONData.Implementation == "mimir" || d.JSONData.Implementation == "cortex")
	}
	if d.JSONData.ManageAlerts != nil && !*d.JSONData.ManageAlerts {
		return false
	}
	return d.Type == "loki" || (d.Type == "prometheus" && (strings.EqualFold(d.JSONData.PrometheusType, "mimir") || strings.EqualFold(d.JSONData.PrometheusType, "cortex")))
}

// Resolve only after an explicit datasource-managed request. Never use isDefault
// to break ties, and never switch away from a user-specified UID on failure.
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
		if ds.supportsCreation(receiver) {
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
	return &compatible[0], nil
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
