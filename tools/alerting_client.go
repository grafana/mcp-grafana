package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/alertmanager/config"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/prometheus/model/labels"
	"gopkg.in/yaml.v3"

	grafanaModels "github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	defaultTimeout    = 30 * time.Second
	rulesEndpointPath = "/api/prometheus/grafana/api/v1/rules"
)

type alertingClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func newAlertingClientFromContext(ctx context.Context) (*alertingClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Grafana base URL %q: %w", baseURL, err)
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	return &alertingClient{
		baseURL: parsedBaseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   defaultTimeout,
		},
	}, nil
}

func (c *alertingClient) makeRequest(ctx context.Context, path string, params url.Values) (*http.Response, error) {
	return c.makeJSONRequest(ctx, http.MethodGet, path, params, nil, http.StatusOK)
}

func (c *alertingClient) makeJSONRequest(ctx context.Context, method, path string, params url.Values, body any, okStatuses ...int) (*http.Response, error) {
	u := c.baseURL.JoinPath(path)
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	p := u.String()

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request body for %s: %w", p, err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, p, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request to %s: %w", p, err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request to %s: %w", p, err)
	}
	if len(okStatuses) == 0 {
		okStatuses = []int{http.StatusOK}
	}
	for _, status := range okStatuses {
		if resp.StatusCode == status {
			return resp, nil
		}
	}
	{
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close() //nolint:errcheck
		return nil, fmt.Errorf("grafana API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}
}

const (
	// maxRulesLimit is the hard limit on the number of rules to return.
	maxRulesLimit = 200
	// maxAlertsLimit is the hard limit on the number of alert instances per rule.
	maxAlertsLimit = 200
)

// GetRulesOpts contains optional server-side filtering parameters for the
// Prometheus rules API endpoint.
// FolderUID, RuleGroup, States, LimitAlerts are available since Grafana 10.0.
// SearchFolder, RuleName, RuleType, RuleLimit, Matchers require Grafana 12.4+.
type GetRulesOpts struct {
	FolderUID    string   // Filter by folder UID
	SearchFolder string   // Search folders by full path using partial matching
	RuleGroup    string   // Filter by exact rule group name
	RuleName     string   // Search by rule name (substring match)
	RuleType     string   // Filter by rule type (e.g. "alerting", "recording")
	States       []string // Filter by rule state (e.g. "firing", "pending", "normal", "nodata", "error")
	RuleLimit    int      // Maximum number of rules to return (max 200)
	LimitAlerts  int      // Maximum number of alert instances per rule (max 200)
	// Matchers filters alert instances by labels. Each matcher is JSON-encoded
	// as a Prometheus matcher object (e.g. {"type":0,"name":"severity","value":"critical"}).
	// Multiple matchers are AND-ed together.
	Matchers []*labels.Matcher
}

func (o *GetRulesOpts) queryValues() url.Values {
	params := url.Values{}
	if o.FolderUID != "" {
		params.Set("folder_uid", o.FolderUID)
	}
	if o.SearchFolder != "" {
		params.Set("search.folder", o.SearchFolder)
	}
	if o.RuleGroup != "" {
		params.Set("rule_group", o.RuleGroup)
	}
	if o.RuleName != "" {
		params.Set("search.rule_name", o.RuleName)
	}
	if o.RuleType != "" {
		params.Set("rule_type", o.RuleType)
	}
	for _, s := range o.States {
		params.Add("state", s)
	}
	ruleLimit := o.RuleLimit
	if ruleLimit > maxRulesLimit {
		ruleLimit = maxRulesLimit
	}
	if ruleLimit > 0 {
		params.Set("rule_limit", strconv.Itoa(ruleLimit))
	}
	limitAlerts := o.LimitAlerts
	if limitAlerts > maxAlertsLimit {
		limitAlerts = maxAlertsLimit
	}
	if limitAlerts > 0 {
		params.Set("limit_alerts", strconv.Itoa(limitAlerts))
	}
	for _, m := range o.Matchers {
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		params.Add("matcher", string(b))
	}
	return params
}

func (c *alertingClient) GetRules(ctx context.Context, opts *GetRulesOpts) (*rulesResponse, error) {
	var params url.Values
	if opts != nil {
		params = opts.queryValues()
	}
	resp, err := c.makeRequest(ctx, rulesEndpointPath, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get alert rules from Grafana API: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var rulesResponse rulesResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&rulesResponse); err != nil {
		return nil, fmt.Errorf("failed to decode rules response from %s: %w", rulesEndpointPath, err)
	}

	return &rulesResponse, nil
}

type rulesResponse struct {
	Data struct {
		RuleGroups []ruleGroup      `json:"groups"`
		NextToken  string           `json:"groupNextToken,omitempty"`
		Totals     map[string]int64 `json:"totals,omitempty"`
	} `json:"data"`
}

type ruleGroup struct {
	Name           string         `json:"name"`
	FolderUID      string         `json:"folderUid"`
	Rules          []alertingRule `json:"rules"`
	Interval       float64        `json:"interval"`
	LastEvaluation time.Time      `json:"lastEvaluation"`
	EvaluationTime float64        `json:"evaluationTime"`
}

type alertingRule struct {
	State          string           `json:"state,omitempty"`
	Name           string           `json:"name,omitempty"`
	Query          string           `json:"query,omitempty"`
	Duration       float64          `json:"duration,omitempty"`
	KeepFiringFor  float64          `json:"keepFiringFor,omitempty"`
	Annotations    labels.Labels    `json:"annotations,omitempty"`
	ActiveAt       *time.Time       `json:"activeAt,omitempty"`
	Alerts         []alert          `json:"alerts,omitempty"`
	Totals         map[string]int64 `json:"totals,omitempty"`
	TotalsFiltered map[string]int64 `json:"totalsFiltered,omitempty"`
	UID            string           `json:"uid"`
	FolderUID      string           `json:"folderUid"`
	Labels         labels.Labels    `json:"labels,omitempty"`
	Health         string           `json:"health"`
	LastError      string           `json:"lastError,omitempty"`
	Type           string           `json:"type"`
	LastEvaluation time.Time        `json:"lastEvaluation"`
	EvaluationTime float64          `json:"evaluationTime"`
}

type alert struct {
	Labels      labels.Labels `json:"labels"`
	Annotations labels.Labels `json:"annotations"`
	State       string        `json:"state"`
	ActiveAt    *time.Time    `json:"activeAt"`
	Value       string        `json:"value"`
}

// GetDatasourceRules queries a datasource's Prometheus ruler API
func (c *alertingClient) GetDatasourceRules(ctx context.Context, datasourceUID string, opts *GetRulesOpts) (*v1.RulesResult, error) {
	// use the Grafana unified endpoint - maybe we need to use the datasource proxy endpoint in the future as this
	// is an api for internal use
	path := fmt.Sprintf("/api/prometheus/%s/api/v1/rules", datasourceUID)
	var params url.Values
	if opts != nil {
		params = opts.queryValues()
	}
	resp, err := c.makeRequest(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get datasource rules: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var response struct {
		Status string         `json:"status"`
		Data   v1.RulesResult `json:"data"`
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode datasource rules response: %w", err)
	}

	if response.Status != "success" {
		return nil, fmt.Errorf("datasource rules API returned status: %s", response.Status)
	}

	return &response.Data, nil
}

// GetAlertmanagerConfig queries an Alertmanager datasource for its configuration
// The implementation type determines the API path:
// - prometheus: /api/v2/status (returns upstream AlertmanagerStatus with YAML config)
// - mimir/cortex: /api/v1/alerts (returns YAML with nested alertmanager_config)
func (c *alertingClient) GetAlertmanagerConfig(ctx context.Context, datasourceUID, implementation string) (*config.Config, error) {
	// determine the API path based on implementation type
	var apiPath string
	var isPrometheusV2 bool
	switch strings.ToLower(implementation) {
	case "prometheus":
		apiPath = "/api/v2/status"
		isPrometheusV2 = true
	case "mimir", "cortex":
		apiPath = "/api/v1/alerts"
	default:
		// default to prometheus
		apiPath = "/api/v2/status"
		isPrometheusV2 = true
	}

	path := fmt.Sprintf("/api/datasources/proxy/uid/%s%s", datasourceUID, apiPath)
	resp, err := c.makeRequest(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get Alertmanager config: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	if isPrometheusV2 {
		var statusResp models.AlertmanagerStatus
		decoder := json.NewDecoder(resp.Body)
		if err := decoder.Decode(&statusResp); err != nil {
			return nil, fmt.Errorf("failed to decode Alertmanager status response: %w", err)
		}

		var cfg config.Config
		if statusResp.Config != nil && statusResp.Config.Original != nil && *statusResp.Config.Original != "" {
			if err := yaml.Unmarshal([]byte(*statusResp.Config.Original), &cfg); err != nil {
				return nil, fmt.Errorf("failed to parse Alertmanager YAML config: %w", err)
			}
		}

		return &cfg, nil
	}

	// Mimir/Cortex /api/v1/alerts returns YAML with alertmanager_config field
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Alertmanager config response: %w", err)
	}

	var mimirResp struct {
		TemplateFiles      any    `yaml:"template_files"`
		AlertmanagerConfig string `yaml:"alertmanager_config"` // Nested YAML string
	}
	if err := yaml.Unmarshal(bodyBytes, &mimirResp); err != nil {
		return nil, fmt.Errorf("failed to decode Mimir alertmanager response: %w", err)
	}

	// Parse the nested alertmanager_config YAML string
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(mimirResp.AlertmanagerConfig), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Mimir alertmanager_config YAML: %w", err)
	}

	return &cfg, nil
}

func alertmanagerProxyPath(datasourceUID, apiPath string) string {
	return fmt.Sprintf("/api/datasources/proxy/uid/%s%s", datasourceUID, apiPath)
}

func (c *alertingClient) GetAlertmanagerAlerts(ctx context.Context, datasourceUID string, params url.Values) (models.GettableAlerts, error) {
	resp, err := c.makeJSONRequest(ctx, http.MethodGet, alertmanagerProxyPath(datasourceUID, "/api/v2/alerts"), params, nil, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("failed to get Alertmanager alerts: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var alerts models.GettableAlerts
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		return nil, fmt.Errorf("failed to decode Alertmanager alerts response: %w", err)
	}
	return alerts, nil
}

func (c *alertingClient) GetAlertmanagerSilences(ctx context.Context, datasourceUID string, params url.Values) (models.GettableSilences, error) {
	resp, err := c.makeJSONRequest(ctx, http.MethodGet, alertmanagerProxyPath(datasourceUID, "/api/v2/silences"), params, nil, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("failed to get Alertmanager silences: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var silences models.GettableSilences
	if err := json.NewDecoder(resp.Body).Decode(&silences); err != nil {
		return nil, fmt.Errorf("failed to decode Alertmanager silences response: %w", err)
	}
	return silences, nil
}

func (c *alertingClient) GetAlertmanagerSilence(ctx context.Context, datasourceUID, silenceID string) (*models.GettableSilence, error) {
	resp, err := c.makeJSONRequest(ctx, http.MethodGet, alertmanagerProxyPath(datasourceUID, "/api/v2/silence/"+url.PathEscape(silenceID)), nil, nil, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("failed to get Alertmanager silence: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var silence models.GettableSilence
	if err := json.NewDecoder(resp.Body).Decode(&silence); err != nil {
		return nil, fmt.Errorf("failed to decode Alertmanager silence response: %w", err)
	}
	return &silence, nil
}

func (c *alertingClient) CreateOrUpdateAlertmanagerSilence(ctx context.Context, datasourceUID string, silence *models.PostableSilence) (map[string]string, error) {
	resp, err := c.makeJSONRequest(ctx, http.MethodPost, alertmanagerProxyPath(datasourceUID, "/api/v2/silences"), nil, silence, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	if err != nil {
		return nil, fmt.Errorf("failed to create or update Alertmanager silence: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Alertmanager silence response: %w", err)
	}
	return result, nil
}

func (c *alertingClient) DeleteAlertmanagerSilence(ctx context.Context, datasourceUID, silenceID string) (map[string]string, error) {
	resp, err := c.makeJSONRequest(ctx, http.MethodDelete, alertmanagerProxyPath(datasourceUID, "/api/v2/silence/"+url.PathEscape(silenceID)), nil, nil, http.StatusOK, http.StatusAccepted)
	if err != nil {
		return nil, fmt.Errorf("failed to delete Alertmanager silence: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Alertmanager delete silence response: %w", err)
	}
	if len(strings.TrimSpace(string(bodyBytes))) == 0 {
		return map[string]string{"status": "deleted", "silenceID": silenceID}, nil
	}
	var result map[string]string
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to decode Alertmanager delete silence response: %w", err)
	}
	if len(result) == 0 {
		result = map[string]string{"status": "deleted", "silenceID": silenceID}
	}
	return result, nil
}

// GetRuleVersions fetches the version history for a Grafana-managed alert rule.
// The endpoint may not exist on older Grafana versions; callers should handle errors.
func (c *alertingClient) GetRuleVersions(ctx context.Context, ruleUID string) ([]grafanaModels.GettableExtendedRuleNode, error) {
	path := fmt.Sprintf("/api/ruler/grafana/api/v1/rule/%s/versions", ruleUID)
	resp, err := c.makeRequest(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get rule versions: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	var versions []grafanaModels.GettableExtendedRuleNode
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("failed to decode rule versions response: %w", err)
	}

	return versions, nil
}
