package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/prometheus/common/model"
)

type datasourceRuleCreated struct {
	DatasourceUID string `json:"datasource_uid"`
	Namespace     string `json:"namespace"`
	RuleGroup     string `json:"rule_group"`
	Title         string `json:"title"`
}

// Grafana's ruler proxy supports query placeholders for names containing path
// separators. Use them for dot segments too, to avoid URL path normalization.
func rulerPathSegment(value, kind string, query url.Values) string {
	tag := "QUERY_" + strings.ToUpper(kind)
	if strings.ContainsAny(value, `/\`) || value == "." || value == ".." || value == tag {
		query.Set(kind, value)
		return tag
	}
	return url.PathEscape(value)
}

func createDatasourceAlertRule(ctx context.Context, args ManageRulesReadWriteParams) (*datasourceRuleCreated, error) {
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	uid := ""
	if args.DatasourceUID != nil {
		uid = *args.DatasourceUID
	}
	ds, err := client.resolveExternalAlertingDatasource(ctx, uid, false)
	if err != nil {
		return nil, err
	}
	unlock, err := lockExternalAlertingWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	query := url.Values{}
	if ds.Type == "prometheus" {
		query.Set("subtype", strings.ToLower(ds.JSONData.PrometheusType))
	}
	namespacePath := "/api/ruler/" + url.PathEscape(ds.UID) + "/api/v1/rules/" + rulerPathSegment(args.Namespace, "namespace", query)
	groupPath := namespacePath + "/" + rulerPathSegment(args.RuleGroup, "group", query)
	body, status, err := client.externalAlertingRequest(ctx, http.MethodGet, groupPath, query, nil)
	if err != nil {
		return nil, err
	}
	var group externalAlertingObject
	if status == http.StatusNotFound && missingExternalRuleGroup(body) {
		interval := args.EvaluationInterval
		if interval == "" {
			interval = "1m"
		}
		group = externalAlertingObject{}
		if err := setExternalAlertingField(group, "name", args.RuleGroup); err != nil {
			return nil, err
		}
		if err := setExternalAlertingField(group, "interval", interval); err != nil {
			return nil, err
		}
	} else if status == http.StatusOK {
		group, err = decodeExternalAlertingObject(body)
		if err != nil {
			return nil, err
		}
		var name string
		if json.Unmarshal(group["name"], &name) != nil || name != args.RuleGroup || group["rules"] == nil {
			return nil, fmt.Errorf("unexpected rule group configuration, nothing was created")
		}
		if args.EvaluationInterval != "" {
			var interval string
			if json.Unmarshal(group["interval"], &interval) != nil {
				return nil, fmt.Errorf("cannot verify existing group evaluation_interval")
			}
			existing, parseErr := model.ParseDuration(interval)
			requested, _ := model.ParseDuration(args.EvaluationInterval)
			if parseErr != nil || existing != requested {
				return nil, fmt.Errorf("evaluation_interval must match the existing group, creating a rule does not change its interval")
			}
		}
	} else {
		return nil, fmt.Errorf("read external rule group: HTTP %d, verify the datasource has a writable ruler API", status)
	}

	var rules []json.RawMessage
	if raw := group["rules"]; raw != nil {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return nil, fmt.Errorf("invalid existing rule group rules")
		}
	}
	for _, raw := range rules {
		var rule struct {
			Alert string `json:"alert"`
		}
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, fmt.Errorf("invalid existing rule")
		}
		if rule.Alert == args.Title {
			return nil, fmt.Errorf("an alert with this title already exists in the group, nothing was created")
		}
	}
	rule := map[string]any{"alert": args.Title, "expr": args.Expression}
	if args.For != "" {
		rule["for"] = args.For
	}
	if args.KeepFiringFor != "" {
		rule["keep_firing_for"] = args.KeepFiringFor
	}
	if args.Labels != nil {
		rule["labels"] = args.Labels
	}
	if args.Annotations != nil {
		rule["annotations"] = args.Annotations
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		return nil, fmt.Errorf("encode datasource rule")
	}
	if err := setExternalAlertingField(group, "rules", append(rules, encoded)); err != nil {
		return nil, err
	}
	query.Del("group")
	if err := client.postExternalAlerting(ctx, namespacePath, query, group); err != nil {
		return nil, err
	}
	return &datasourceRuleCreated{DatasourceUID: ds.UID, Namespace: args.Namespace, RuleGroup: args.RuleGroup, Title: args.Title}, nil
}

func missingExternalRuleGroup(body []byte) bool {
	var response struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	return strings.Contains(response.Message, "group does not exist") || strings.Contains(response.Message, "no rule groups found")
}
