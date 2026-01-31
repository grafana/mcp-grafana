# Panel Information Features Contribution

## Summary

This contribution adds four new MCP tools to enhance panel inspection capabilities in the Grafana MCP server. These tools provide granular access to panel information, making it easier to analyze, search, and understand dashboard panels without retrieving the entire dashboard JSON.

## New Tools Added

### 1. `list_panels_by_type`

**Purpose**: Filter and retrieve all panels of a specific visualization type from a dashboard.

**Parameters**:
- `uid` (required): Dashboard UID
- `panelType` (required): Panel visualization type (e.g., 'timeseries', 'graph', 'table', 'stat', 'gauge', 'bargauge', 'piechart', 'heatmap')

**Returns**: Array of `PanelDetail` objects containing:
- Panel ID, title, type, description
- Targets (queries)
- Datasource information
- Grid position
- Panel options

**Use Cases**:
- Find all time series panels in a dashboard
- Identify all table panels for migration
- Audit panel types across dashboards

### 2. `get_panel_transformations`

**Purpose**: Retrieve data transformations applied to a specific panel.

**Parameters**:
- `uid` (required): Dashboard UID
- `panelId` (required): Panel ID

**Returns**: `PanelTransformationsResult` containing:
- Panel ID, title, and type
- Array of transformation configurations
- Boolean flag indicating if transformations exist

**Use Cases**:
- Understand data processing pipeline for a panel
- Debug transformation issues
- Document panel data manipulation

### 3. `search_dashboard_panels`

**Purpose**: Search for panels within a dashboard by matching text in titles, descriptions, or queries.

**Parameters**:
- `uid` (required): Dashboard UID
- `searchTerm` (required): Text to search for

**Returns**: Array of `PanelSearchResult` objects containing:
- Panel ID, title, type, description
- Match reasons (where the search term was found: title, description, or query)

**Use Cases**:
- Find panels querying specific metrics
- Locate panels by partial title
- Search for panels with specific query patterns

### 4. `get_panel_field_config`

**Purpose**: Retrieve field configuration for a specific panel, including units, thresholds, and overrides.

**Parameters**:
- `uid` (required): Dashboard UID
- `panelId` (required): Panel ID

**Returns**: `PanelFieldConfigResult` containing:
- Panel ID, title, and type
- Field configuration object with:
  - Default field settings
  - Field overrides
  - Units, decimals, thresholds
  - Value mappings

**Use Cases**:
- Understand how data is formatted and displayed
- Review threshold configurations
- Audit field override rules

## Implementation Details

### Files Modified

1. **`tools/dashboard.go`** (Main implementation)
   - Added 4 new tool functions with handlers
   - Added 6 new type definitions
   - Added 3 helper functions
   - Registered tools in `AddDashboardTools`

2. **`tools/dashboard_test.go`** (Comprehensive tests)
   - Added 8 integration tests covering:
     - Successful operations
     - Error handling (invalid panel IDs, non-existent types)
     - Edge cases (no matches, empty results)

3. **`README.md`** (Documentation)
   - Updated Features section with new panel tools
   - Added 4 rows to the Tools table with RBAC requirements

### Code Quality

- ✅ All tests passing (unit tests verified)
- ✅ No linter errors
- ✅ Follows existing code patterns and conventions
- ✅ Comprehensive error handling
- ✅ Support for nested panels in row panels
- ✅ Proper RBAC permissions documented

### Design Decisions

1. **Consistent with Existing Tools**: All new tools follow the same patterns as existing dashboard tools (e.g., `GetDashboardPanelQueries`, `GetDashboardSummary`)

2. **Context Window Optimization**: These tools provide targeted panel information without requiring the full dashboard JSON, reducing context window usage

3. **Nested Panel Support**: All tools properly handle panels nested within row panels, ensuring complete coverage

4. **Type Safety**: Strong typing with dedicated structs for requests and responses

5. **Read-Only Operations**: All tools are read-only with `dashboards:read` permission requirement

## Testing

### Integration Tests Added

```go
- list_panels_by_type (happy path)
- list_panels_by_type - no matches
- get_panel_transformations (happy path)
- get_panel_transformations - invalid panel
- search_dashboard_panels (happy path)
- search_dashboard_panels - no matches
- get_panel_field_config (happy path)
- get_panel_field_config - invalid panel
```

### Test Coverage

- ✅ Successful operations with valid inputs
- ✅ Error handling for invalid panel IDs
- ✅ Empty result sets (no matches)
- ✅ Edge cases and boundary conditions

## RBAC Requirements

All new tools require:
- **Permission**: `dashboards:read`
- **Scope**: `dashboards:uid:abc123` (or `dashboards:*` for all dashboards)

## Usage Examples

### Example 1: Find all time series panels

```json
{
  "tool": "list_panels_by_type",
  "arguments": {
    "uid": "dashboard-uid",
    "panelType": "timeseries"
  }
}
```

### Example 2: Search for panels with specific metric

```json
{
  "tool": "search_dashboard_panels",
  "arguments": {
    "uid": "dashboard-uid",
    "searchTerm": "cpu_usage"
  }
}
```

### Example 3: Get panel transformations

```json
{
  "tool": "get_panel_transformations",
  "arguments": {
    "uid": "dashboard-uid",
    "panelId": 5
  }
}
```

### Example 4: Get panel field configuration

```json
{
  "tool": "get_panel_field_config",
  "arguments": {
    "uid": "dashboard-uid",
    "panelId": 5
  }
}
```

## Benefits

1. **Enhanced Panel Discovery**: Easily find panels by type or content
2. **Better Debugging**: Inspect transformations and field configs
3. **Context Window Efficiency**: Get specific panel data without full dashboard
4. **Improved Observability**: Better understanding of dashboard structure
5. **Automation Friendly**: Programmatic access to panel metadata

## Next Steps

To contribute this to the official mcp-grafana repository:

1. ✅ Implementation complete
2. ✅ Tests written and passing
3. ✅ Documentation updated
4. ✅ Code linted and formatted
5. 🔄 Create a feature branch
6. 🔄 Commit changes with clear messages
7. 🔄 Push to GitHub
8. 🔄 Create Pull Request with this document

## Commit Message Template

```
feat(dashboard): add panel inspection tools

Add four new MCP tools for enhanced panel information retrieval:
- list_panels_by_type: Filter panels by visualization type
- get_panel_transformations: Retrieve panel transformations
- search_dashboard_panels: Search panels by content
- get_panel_field_config: Get panel field configuration

These tools provide granular access to panel metadata without
requiring full dashboard JSON, improving context window efficiency.

All tools support nested panels in rows and include comprehensive
integration tests.

Closes #<issue-number>
```

## Author

Contribution prepared for the official Grafana MCP server repository.

