package tools

import (
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/models"
)

type ListAlertRulesParams struct {
	Limit          int        `json:"limit,omitempty" jsonschema:"default=200,description=The maximum number of results to return"`
	Page           int        `json:"page,omitempty" jsonschema:"default=1,description=The page number to return"`
	DatasourceUID  *string    `json:"datasourceUid,omitempty" jsonschema:"description=Optional: UID of a Prometheus or Loki datasource to query for datasource-managed alert rules. If omitted\\, returns Grafana-managed rules."`
	LabelSelectors []Selector `json:"label_selectors,omitempty" jsonschema:"description=Optionally\\, a list of matchers to filter alert rules by labels"`
}

type alertRuleSummary struct {
	UID            string            `json:"uid"`
	Title          string            `json:"title"`
	State          string            `json:"state"`
	Health         string            `json:"health,omitempty"`
	FolderUID      string            `json:"folder_uid,omitempty"`
	RuleGroup      string            `json:"rule_group,omitempty"`
	For            string            `json:"for,omitempty"`
	LastEvaluation string            `json:"last_evaluation,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

// alertRuleDetail is the enriched response for a single rule, combining
// full configuration from the Provisioning API with runtime state from the
// Prometheus rules API.
type alertRuleDetail struct {
	UID          string            `json:"uid"`
	Title        string            `json:"title"`
	FolderUID    string            `json:"folder_uid"`
	RuleGroup    string            `json:"rule_group"`
	Condition    string            `json:"condition,omitempty"`
	NoDataState  string            `json:"no_data_state,omitempty"`
	ExecErrState string            `json:"exec_err_state,omitempty"`
	For          string            `json:"for,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`

	IsPaused             bool                                      `json:"is_paused"`
	NotificationSettings *models.AlertRuleNotificationSettings      `json:"notification_settings,omitempty"`
	Queries              []querySummary                             `json:"queries,omitempty"`

	State          string  `json:"state"`
	Health         string  `json:"health"`
	Type           string  `json:"type,omitempty"`
	LastEvaluation string  `json:"last_evaluation,omitempty"`
	LastError      string  `json:"last_error,omitempty"`
	Alerts         []alert `json:"alerts,omitempty"`
}

type querySummary struct {
	RefID         string `json:"ref_id"`
	DatasourceUID string `json:"datasource_uid"`
	Expression    string `json:"expression,omitempty"`
}

type CreateAlertRuleParams struct {
	Title             string               `json:"title" jsonschema:"required,description=The title of the alert rule"`
	RuleGroup         string               `json:"ruleGroup" jsonschema:"required,description=The rule group name"`
	FolderUID         string               `json:"folderUID" jsonschema:"required,description=The folder UID where the rule will be created"`
	Condition         string               `json:"condition" jsonschema:"required,description=The query condition identifier (e.g. 'A'\\, 'B')"`
	Data              []*models.AlertQuery `json:"data" jsonschema:"required,description=Array of query data objects"`
	NoDataState       string               `json:"noDataState" jsonschema:"required,description=State when no data (NoData\\, Alerting\\, OK)"`
	ExecErrState      string               `json:"execErrState" jsonschema:"required,description=State on execution error (NoData\\, Alerting\\, OK)"`
	For               string               `json:"for" jsonschema:"required,description=Duration before alert fires (e.g. '5m')"`
	Annotations       map[string]string    `json:"annotations,omitempty" jsonschema:"description=Optional annotations"`
	Labels            map[string]string    `json:"labels,omitempty" jsonschema:"description=Optional labels"`
	UID               *string              `json:"uid,omitempty" jsonschema:"description=Optional UID for the alert rule"`
	OrgID             int64                `json:"orgID" jsonschema:"required,description=The organization ID"`
	DisableProvenance *bool                `json:"disableProvenance,omitempty" jsonschema:"description=If true\\, the alert will remain editable in the Grafana UI (sets X-Disable-Provenance header). If false\\, the alert will be marked with provenance 'api' and locked from UI editing. Defaults to true."`
}

func (p CreateAlertRuleParams) validate() error {
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.RuleGroup == "" {
		return fmt.Errorf("rule_group is required")
	}
	if p.FolderUID == "" {
		return fmt.Errorf("folder_uid is required")
	}
	if p.Condition == "" {
		return fmt.Errorf("condition is required")
	}
	if p.Data == nil {
		return fmt.Errorf("data is required")
	}
	if p.NoDataState == "" {
		return fmt.Errorf("no_data_state is required")
	}
	if p.ExecErrState == "" {
		return fmt.Errorf("exec_err_state is required")
	}
	if p.For == "" {
		return fmt.Errorf("for duration is required")
	}
	if p.OrgID <= 0 {
		return fmt.Errorf("org_id is required and must be greater than 0")
	}
	return nil
}

type UpdateAlertRuleParams struct {
	UID               string               `json:"uid" jsonschema:"required,description=The UID of the alert rule to update"`
	Title             string               `json:"title" jsonschema:"required,description=The title of the alert rule"`
	RuleGroup         string               `json:"ruleGroup" jsonschema:"required,description=The rule group name"`
	FolderUID         string               `json:"folderUID" jsonschema:"required,description=The folder UID where the rule will be created"`
	Condition         string               `json:"condition" jsonschema:"required,description=The query condition identifier (e.g. 'A'\\, 'B')"`
	Data              []*models.AlertQuery `json:"data" jsonschema:"required,description=Array of query data objects"`
	NoDataState       string               `json:"noDataState" jsonschema:"required,description=State when no data (NoData\\, Alerting\\, OK)"`
	ExecErrState      string               `json:"execErrState" jsonschema:"required,description=State on execution error (NoData\\, Alerting\\, OK)"`
	For               string               `json:"for" jsonschema:"required,description=Duration before alert fires (e.g. '5m')"`
	Annotations       map[string]string    `json:"annotations,omitempty" jsonschema:"description=Optional annotations"`
	Labels            map[string]string    `json:"labels,omitempty" jsonschema:"description=Optional labels"`
	OrgID             int64                `json:"orgID" jsonschema:"required,description=The organization ID"`
	DisableProvenance *bool                `json:"disableProvenance,omitempty" jsonschema:"description=If true\\, the alert will remain editable in the Grafana UI (sets X-Disable-Provenance header). If false\\, the alert will be marked with provenance 'api' and locked from UI editing. Defaults to true."`
}

func (p UpdateAlertRuleParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("rule_uid is required")
	}
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.RuleGroup == "" {
		return fmt.Errorf("rule_group is required")
	}
	if p.FolderUID == "" {
		return fmt.Errorf("folder_uid is required")
	}
	if p.Condition == "" {
		return fmt.Errorf("condition is required")
	}
	if p.Data == nil {
		return fmt.Errorf("data is required")
	}
	if p.NoDataState == "" {
		return fmt.Errorf("no_data_state is required")
	}
	if p.ExecErrState == "" {
		return fmt.Errorf("exec_err_state is required")
	}
	if p.For == "" {
		return fmt.Errorf("for duration is required")
	}
	if p.OrgID <= 0 {
		return fmt.Errorf("org_id is required and must be greater than 0")
	}
	return nil
}

type DeleteAlertRuleParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the alert rule to delete"`
}

func (p DeleteAlertRuleParams) validate() error {
	if p.UID == "" {
		return fmt.Errorf("uid is required")
	}
	return nil
}

// ManageRulesReadParams is the param struct for the read-only version of alerting_manage_rules.
type ManageRulesReadParams struct {
	Operation      string     `json:"operation" jsonschema:"required,enum=list,enum=get,enum=versions,description=The operation to perform: 'list' to search/filter rules\\, 'get' to retrieve full rule details (state + configuration) by UID\\, or 'versions' to get the version history of a rule"`
	RuleUID        string     `json:"rule_uid,omitempty" jsonschema:"description=The UID of the alert rule (required for 'get' and 'versions' operations)"`
	RuleLimit      int        `json:"rule_limit,omitempty" jsonschema:"default=200,description=Maximum number of rules to return (default 200\\, max 200). Requires Grafana 12.4+ (for 'list' operation)"`
	DatasourceUID  *string    `json:"datasource_uid,omitempty" jsonschema:"description=Optional: UID of a Prometheus or Loki datasource to query for datasource-managed alert rules. If omitted\\, returns Grafana-managed rules."`
	LabelSelectors []Selector `json:"label_selectors,omitempty" jsonschema:"description=Label matchers to filter alert rules (for 'list' operation)"`
	LimitAlerts    int        `json:"limit_alerts,omitempty" jsonschema:"description=Limit alert instances per rule. For list: 0 omits alerts. For get: <=0 defaults to 200. Max 200."`
	FolderUID      string     `json:"folder_uid,omitempty" jsonschema:"description=Filter by exact folder UID (for 'list' operation). Mutually exclusive with search_folder."`
	SearchFolder   string     `json:"search_folder,omitempty" jsonschema:"description=Search folders by path using partial matching (for 'list' operation). Requires Grafana 12.4+. Mutually exclusive with folder_uid."`
}

func (p ManageRulesReadParams) validate() error {
	switch p.Operation {
	case "list":
		if p.RuleLimit < 0 {
			return fmt.Errorf("invalid rule_limit: %d, must be >= 0", p.RuleLimit)
		}
		if p.FolderUID != "" && p.SearchFolder != "" {
			return fmt.Errorf("folder_uid and search_folder are mutually exclusive")
		}
		return nil
	case "get":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'get' operation")
		}
		return nil
	case "versions":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'versions' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list, get, versions", p.Operation)
	}
}

// ManageRulesReadWriteParams is the param struct for the read-write version of alerting_manage_rules.
type ManageRulesReadWriteParams struct {
	Operation         string               `json:"operation" jsonschema:"required,enum=list,enum=get,enum=versions,enum=create,enum=update,enum=delete,description=The operation to perform: 'list'\\, 'get'\\, 'versions'\\, 'create'\\, 'update'\\, or 'delete'. To create a rule\\, use operation 'create' and provide all required fields in a single call. To update a rule\\, first use 'get' to retrieve its full configuration\\, then 'update' with all required fields plus your changes."`
	RuleUID           string               `json:"rule_uid,omitempty" jsonschema:"description=The UID of the alert rule (required for 'get'\\, 'versions'\\, 'update'\\, 'delete'; optional for 'create')"`
	RuleLimit         int                  `json:"rule_limit,omitempty" jsonschema:"default=200,description=Maximum number of rules to return (default 200\\, max 200). Requires Grafana 12.4+ (for 'list' operation)"`
	DatasourceUID     *string              `json:"datasource_uid,omitempty" jsonschema:"description=Optional: UID of a Prometheus or Loki datasource to query for datasource-managed alert rules (for 'list' operation)"`
	LabelSelectors    []Selector           `json:"label_selectors,omitempty" jsonschema:"description=Label matchers to filter alert rules (for 'list' operation)"`
	LimitAlerts       int                  `json:"limit_alerts,omitempty" jsonschema:"description=Limit alert instances per rule. For list: 0 omits alerts. For get: <=0 defaults to 200. Max 200."`
	SearchFolder      string               `json:"search_folder,omitempty" jsonschema:"description=Search folders by path using partial matching (for 'list' operation). Requires Grafana 12.4+. Mutually exclusive with folder_uid when used for filtering."`
	Title             string               `json:"title,omitempty" jsonschema:"description=The title of the alert rule (required for 'create'\\, 'update')"`
	RuleGroup         string               `json:"rule_group,omitempty" jsonschema:"description=The rule group name (required for 'create'\\, 'update')"`
	FolderUID         string               `json:"folder_uid,omitempty" jsonschema:"description=The folder UID. For 'list': filter by exact folder UID (mutually exclusive with search_folder). For 'create'/'update': the folder to store the rule in (required)."`
	Condition         string               `json:"condition,omitempty" jsonschema:"description=The query condition identifier\\, e.g. 'A'\\, 'B' (required for 'create'\\, 'update')"`
	Data              []*models.AlertQuery `json:"data,omitempty" jsonschema:"description=Array of query data objects (required for 'create'\\, 'update')"`
	NoDataState       string               `json:"no_data_state,omitempty" jsonschema:"description=State when no data: NoData\\, Alerting\\, OK (required for 'create'\\, 'update')"`
	ExecErrState      string               `json:"exec_err_state,omitempty" jsonschema:"description=State on execution error: NoData\\, Alerting\\, OK (required for 'create'\\, 'update')"`
	For               string               `json:"for,omitempty" jsonschema:"description=Duration before alert fires\\, e.g. '5m' (required for 'create'\\, 'update')"`
	Annotations       map[string]string    `json:"annotations,omitempty" jsonschema:"description=Optional annotations for the alert rule"`
	Labels            map[string]string    `json:"labels,omitempty" jsonschema:"description=Optional labels for the alert rule"`
	OrgID             int64                `json:"org_id,omitempty" jsonschema:"description=The organization ID (required for 'create'\\, 'update')"`
	DisableProvenance *bool                `json:"disable_provenance,omitempty" jsonschema:"description=If true\\, the alert remains editable in the Grafana UI (sets X-Disable-Provenance header). Defaults to true."`
}

func (p ManageRulesReadWriteParams) validate() error {
	switch p.Operation {
	case "list":
		if p.RuleLimit < 0 {
			return fmt.Errorf("invalid rule_limit: %d, must be >= 0", p.RuleLimit)
		}
		if p.FolderUID != "" && p.SearchFolder != "" {
			return fmt.Errorf("folder_uid and search_folder are mutually exclusive")
		}
		return nil
	case "get":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'get' operation")
		}
		return nil
	case "versions":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'versions' operation")
		}
		return nil
	case "create":
		return p.toCreateParams().validate()
	case "update":
		return p.toUpdateParams().validate()
	case "delete":
		if p.RuleUID == "" {
			return fmt.Errorf("rule_uid is required for 'delete' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list, get, versions, create, update, delete", p.Operation)
	}
}

func (p ManageRulesReadWriteParams) toCreateParams() CreateAlertRuleParams {
	params := CreateAlertRuleParams{
		Title:             p.Title,
		RuleGroup:         p.RuleGroup,
		FolderUID:         p.FolderUID,
		Condition:         p.Condition,
		Data:              p.Data,
		NoDataState:       p.NoDataState,
		ExecErrState:      p.ExecErrState,
		For:               p.For,
		Annotations:       p.Annotations,
		Labels:            p.Labels,
		OrgID:             p.OrgID,
		DisableProvenance: p.DisableProvenance,
	}
	if p.RuleUID != "" {
		params.UID = &p.RuleUID
	}
	return params
}

func (p ManageRulesReadWriteParams) toUpdateParams() UpdateAlertRuleParams {
	return UpdateAlertRuleParams{
		UID:               p.RuleUID,
		Title:             p.Title,
		RuleGroup:         p.RuleGroup,
		FolderUID:         p.FolderUID,
		Condition:         p.Condition,
		Data:              p.Data,
		NoDataState:       p.NoDataState,
		ExecErrState:      p.ExecErrState,
		For:               p.For,
		Annotations:       p.Annotations,
		Labels:            p.Labels,
		OrgID:             p.OrgID,
		DisableProvenance: p.DisableProvenance,
	}
}

