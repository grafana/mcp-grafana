package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/alertmanager/config"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type ListContactPointsParams struct {
	DatasourceUID *string `json:"datasourceUid,omitempty" jsonschema:"description=Optional: UID of an Alertmanager-compatible datasource to query for receivers. If omitted\\, returns Grafana-managed contact points."`
	Limit         int     `json:"limit,omitempty" jsonschema:"description=The maximum number of results to return. Default is 100."`
	Name          *string `json:"name,omitempty" jsonschema:"description=Filter contact points by name"`
}

func (p ListContactPointsParams) validate() error {
	if p.Limit < 0 {
		return fmt.Errorf("invalid limit: %d, must be greater than 0", p.Limit)
	}
	return nil
}

type contactPointSummary struct {
	UID  string  `json:"uid"`
	Name string  `json:"name"`
	Type *string `json:"type,omitempty"`
}

func listContactPoints(ctx context.Context, args ListContactPointsParams) ([]contactPointSummary, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	if args.DatasourceUID != nil && *args.DatasourceUID != "" {
		return listAlertmanagerReceivers(ctx, args)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)

	params := provisioning.NewGetContactpointsParams().WithContext(ctx)
	if args.Name != nil {
		params.Name = args.Name
	}

	response, err := c.Provisioning.GetContactpoints(params)
	if err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	filteredContactPoints, err := applyLimitToContactPoints(response.Payload, args.Limit)
	if err != nil {
		return nil, fmt.Errorf("list contact points: %w", err)
	}

	return summarizeContactPoints(filteredContactPoints), nil
}

func summarizeContactPoints(contactPoints []*models.EmbeddedContactPoint) []contactPointSummary {
	result := make([]contactPointSummary, 0, len(contactPoints))
	for _, cp := range contactPoints {
		result = append(result, contactPointSummary{
			UID:  cp.UID,
			Name: cp.Name,
			Type: cp.Type,
		})
	}
	return result
}

func applyLimitToContactPoints(items []*models.EmbeddedContactPoint, limit int) ([]*models.EmbeddedContactPoint, error) {
	if limit == 0 {
		limit = DefaultListContactPointsLimit
	}

	if limit > len(items) {
		return items, nil
	}

	return items[:limit], nil
}

func listAlertmanagerReceivers(ctx context.Context, args ListContactPointsParams) ([]contactPointSummary, error) {
	dsUID := *args.DatasourceUID

	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: dsUID})
	if err != nil {
		return nil, fmt.Errorf("datasource %s: %w", dsUID, err)
	}

	if !isAlertmanagerDatasource(ds.Type) {
		return nil, fmt.Errorf("datasource %s (type: %s) is not an Alertmanager datasource", dsUID, ds.Type)
	}

	implementation := "prometheus"
	if ds.JSONData != nil {
		if jsonDataMap, ok := ds.JSONData.(map[string]interface{}); ok {
			if impl, ok := jsonDataMap["implementation"].(string); ok && impl != "" {
				implementation = impl
			}
		}
	}

	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}

	cfg, err := client.GetAlertmanagerConfig(ctx, dsUID, implementation)
	if err != nil {
		return nil, fmt.Errorf("querying Alertmanager config: %w", err)
	}

	receivers := convertReceiversToContactPoints(cfg.Receivers)

	if args.Name != nil && *args.Name != "" {
		receivers = filterContactPointsByName(receivers, *args.Name)
	}

	if args.Limit > 0 && len(receivers) > args.Limit {
		receivers = receivers[:args.Limit]
	} else if args.Limit == 0 && len(receivers) > DefaultListContactPointsLimit {
		receivers = receivers[:DefaultListContactPointsLimit]
	}

	return receivers, nil
}

func isAlertmanagerDatasource(dsType string) bool {
	dsType = strings.ToLower(dsType)
	return strings.Contains(dsType, "alertmanager")
}

func convertReceiversToContactPoints(receivers []config.Receiver) []contactPointSummary {
	result := make([]contactPointSummary, 0, len(receivers))
	for _, r := range receivers {
		result = append(result, contactPointSummary{
			Name: r.Name,
		})
	}
	return result
}

func filterContactPointsByName(cps []contactPointSummary, name string) []contactPointSummary {
	var filtered []contactPointSummary
	for _, cp := range cps {
		if cp.Name == name {
			filtered = append(filtered, cp)
		}
	}
	return filtered
}
