package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/PaesslerAG/gval"
	"github.com/PaesslerAG/jsonpath"
	"github.com/invopop/jsonschema"
)

const maxDashboardPropertyResponseBytes = 32 * 1024

// DashboardSelector identifies a dashboard object without exposing its
// schema-specific physical path.
type DashboardSelector struct {
	Kind    string `json:"kind" jsonschema:"required,enum=dashboard,enum=panel,enum=query,enum=variable,description=Object kind to select"`
	PanelID *int   `json:"panelId,omitempty" jsonschema:"description=Exact panel ID. Required for panel and query selectors"`
	RefID   string `json:"refId,omitempty" jsonschema:"description=Exact query ref ID. Required for query selectors"`
	Name    string `json:"name,omitempty" jsonschema:"description=Exact variable name. Required for variable selectors"`
}

func (DashboardSelector) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.AdditionalProperties = jsonschema.FalseSchema
}

func (selector *DashboardSelector) UnmarshalJSON(data []byte) error {
	type selectorAlias DashboardSelector
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for field := range fields {
		switch field {
		case "kind", "panelId", "refId", "name":
		default:
			return fmt.Errorf("unknown field %q in dashboard selector", field)
		}
	}

	var decoded selectorAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*selector = DashboardSelector(decoded)
	return nil
}

// DashboardPropertyResponse is returned when get_dashboard_property uses a
// semantic selector. Calls without a selector retain their legacy raw value.
type DashboardPropertyResponse struct {
	Value    interface{}       `json:"value"`
	Revision string            `json:"revision"`
	IsV2     bool              `json:"isV2"`
	Selector DashboardSelector `json:"selector"`
}

// readDashboardProperty applies a JSONPath either to the complete dashboard
// (legacy behavior) or relative to a semantically selected object.
func readDashboardProperty(ctx context.Context, res *dashboardResult, args GetDashboardPropertyParams) (interface{}, error) {
	root := interface{}(res.Spec)
	if args.Selector != nil {
		selected, err := resolveDashboardSelector(res.Spec, res.IsV2, *args.Selector)
		if err != nil {
			return nil, err
		}
		root = selected
	}

	value, err := evaluateDashboardJSONPath(ctx, root, args.JSONPath)
	if err != nil {
		return nil, err
	}
	if args.Selector == nil {
		return value, nil
	}

	response := &DashboardPropertyResponse{
		Value:    value,
		Revision: dashboardRevision(res),
		IsV2:     res.IsV2,
		Selector: *args.Selector,
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("marshal selected dashboard property: %w", err)
	}
	if len(encoded) > maxDashboardPropertyResponseBytes {
		return nil, fmt.Errorf(
			"selected dashboard property response is %d bytes and exceeds the %d-byte limit; use a narrower JSONPath",
			len(encoded),
			maxDashboardPropertyResponseBytes,
		)
	}
	return response, nil
}

func evaluateDashboardJSONPath(ctx context.Context, root interface{}, expression string) (interface{}, error) {
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal dashboard to JSON: %w", err)
	}

	var data interface{}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return nil, fmt.Errorf("unmarshal dashboard JSON: %w", err)
	}

	path, err := gval.Full(jsonpath.Language()).NewEvaluable(expression)
	if err != nil {
		return nil, fmt.Errorf("create JSONPath evaluable '%s': %w", expression, err)
	}
	value, err := path(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("apply JSONPath '%s': %w", expression, err)
	}
	return value, nil
}

func resolveDashboardSelector(spec map[string]interface{}, isV2 bool, selector DashboardSelector) (map[string]interface{}, error) {
	if err := validateDashboardSelector(selector); err != nil {
		return nil, err
	}

	switch selector.Kind {
	case "dashboard":
		return spec, nil
	case "panel":
		return resolveSelectedPanel(spec, isV2, *selector.PanelID)
	case "query":
		panel, err := resolveSelectedPanel(spec, isV2, *selector.PanelID)
		if err != nil {
			return nil, err
		}
		if isV2 {
			return findQueryByRefIDV2(panel, selector.RefID)
		}
		return findQueryByRefID(panel, selector.RefID)
	case "variable":
		if isV2 {
			return findVariableByNameV2(spec, selector.Name)
		}
		return findVariableByName(spec, selector.Name)
	default:
		panic("selector kind was validated")
	}
}

func validateDashboardSelector(selector DashboardSelector) error {
	switch selector.Kind {
	case "dashboard":
		if selector.PanelID != nil || selector.RefID != "" || selector.Name != "" {
			return fmt.Errorf("dashboard selector does not accept panelId, refId, or name")
		}
	case "panel":
		if selector.PanelID == nil {
			return fmt.Errorf("panelId is required for panel selector")
		}
		if selector.RefID != "" || selector.Name != "" {
			return fmt.Errorf("panel selector does not accept refId or name")
		}
	case "query":
		if selector.PanelID == nil {
			return fmt.Errorf("panelId is required for query selector")
		}
		if selector.RefID == "" {
			return fmt.Errorf("refId is required for query selector")
		}
		if selector.Name != "" {
			return fmt.Errorf("query selector does not accept name")
		}
	case "variable":
		if selector.Name == "" {
			return fmt.Errorf("name is required for variable selector")
		}
		if selector.PanelID != nil || selector.RefID != "" {
			return fmt.Errorf("variable selector does not accept panelId or refId")
		}
	default:
		return fmt.Errorf("unsupported selector kind %q; use dashboard, panel, query, or variable", selector.Kind)
	}
	return nil
}

func resolveSelectedPanel(spec map[string]interface{}, isV2 bool, panelID int) (map[string]interface{}, error) {
	if isV2 {
		return findSelectablePanelByIDV2(spec, panelID)
	}
	return findPanelByID(spec, panelID)
}

func findSelectablePanelByIDV2(spec map[string]interface{}, panelID int) (map[string]interface{}, error) {
	for _, element := range collectElementsV2(spec) {
		if (element.Kind == "Panel" || element.Kind == "LibraryPanel") && safeInt(element.Spec, "id") == panelID {
			return element.Spec, nil
		}
	}
	return nil, fmt.Errorf("panel with ID %d not found", panelID)
}

func findQueryByRefID(panel map[string]interface{}, refID string) (map[string]interface{}, error) {
	for _, raw := range safeArray(panel, "targets") {
		query, ok := raw.(map[string]interface{})
		if ok && safeString(query, "refId") == refID {
			return query, nil
		}
	}
	return nil, fmt.Errorf("query with refId %q not found in panel", refID)
}

// findQueryByRefIDV2 returns the datasource-specific query body. This makes
// relative paths such as $.expr and $.rawSql work for both classic and v2
// dashboards even though v2 wraps the body in PanelQuery/DataQuery objects.
func findQueryByRefIDV2(panel map[string]interface{}, refID string) (map[string]interface{}, error) {
	dataSpec := safeObject(safeObject(panel, "data"), "spec")
	for _, raw := range safeArray(dataSpec, "queries") {
		query, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		panelQuerySpec := safeObject(query, "spec")
		if safeString(panelQuerySpec, "refId") != refID {
			continue
		}
		dataQuery := safeObject(panelQuerySpec, "query")
		body := safeObject(dataQuery, "spec")
		if body == nil {
			return nil, fmt.Errorf("query with refId %q has no query body", refID)
		}
		return body, nil
	}
	return nil, fmt.Errorf("query with refId %q not found in panel", refID)
}

func findVariableByName(spec map[string]interface{}, name string) (map[string]interface{}, error) {
	templating := safeObject(spec, "templating")
	for _, raw := range safeArray(templating, "list") {
		variable, ok := raw.(map[string]interface{})
		if ok && safeString(variable, "name") == name {
			return variable, nil
		}
	}
	return nil, fmt.Errorf("variable with name %q not found", name)
}

// findVariableByNameV2 returns the variable spec rather than its kind/spec
// wrapper so relative paths such as $.name and $.current are schema-independent.
func findVariableByNameV2(spec map[string]interface{}, name string) (map[string]interface{}, error) {
	for _, raw := range safeArray(spec, "variables") {
		variable, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		variableSpec := safeObject(variable, "spec")
		if safeString(variableSpec, "name") == name {
			return variableSpec, nil
		}
	}
	return nil, fmt.Errorf("variable with name %q not found", name)
}

func dashboardRevision(res *dashboardResult) string {
	if revision := k8sNestedString(res.Object, "metadata", "resourceVersion"); revision != "" {
		return revision
	}
	if res.Meta != nil {
		return strconv.FormatInt(res.Meta.Version, 10)
	}
	return ""
}
