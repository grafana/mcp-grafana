package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type bigqueryDialect struct{}

func init() { RegisterDialect(&bigqueryDialect{}) }

func (d *bigqueryDialect) DatasourceTypes() []string  { return []string{BigQueryDatasourceType} }
func (d *bigqueryDialect) DefaultFormat() interface{} { return 1 }

var bigqueryIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`)

func (d *bigqueryDialect) ValidateIdentifier(name, field string) error {
	if name != "" && !bigqueryIdentifier.MatchString(name) {
		return fmt.Errorf("invalid %s: use letters, numbers, underscores, or hyphens", field)
	}
	return nil
}

func (d *bigqueryDialect) ExtraQueryPayloadFields(params QuerySQLParams) map[string]interface{} {
	// The plugin reads these from connectionArgs, not the top-level query.
	// Its configured defaultProject remains the execution/billing project.
	return map[string]interface{}{
		"connectionArgs": map[string]interface{}{"location": params.Location},
		"intervalMs":     1000,
		"maxDataPoints":  1000,
	}
}

func (d *bigqueryDialect) SubstituteMacros(query string, from, to time.Time) string {
	interval := max(int64(1), int64(to.Sub(from).Seconds())/1000)
	// Leave BigQuery's native timeFilter/timeFrom/timeTo/timeGroup macros to
	// the plugin, which handles expressions and its own interval semantics.
	return replaceWholeMacros(query, map[string]string{
		"from":        strconv.FormatInt(from.UnixMilli(), 10),
		"to":          strconv.FormatInt(to.UnixMilli(), 10),
		"interval":    fmt.Sprintf("%ds", interval),
		"interval_ms": strconv.FormatInt(interval*1000, 10),
	})
}

func (d *bigqueryDialect) EnforceLimit(query string, limit int) string {
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

// Handlers already apply explicit/configured projects. Let the plugin discover
// the remaining default (e.g. GCE credentials), without accessing GCP from MCP.
func (d *bigqueryDialect) resolveMetadataProject(ctx context.Context, qr QueryRunner, uid, project string) (string, error) {
	if project == "" {
		body, err := qr.ResourceRequest(ctx, uid, "/defaultProjects", map[string]string{})
		if err != nil {
			return "", fmt.Errorf("resolving BigQuery default project: %w", err)
		}
		// Despite the endpoint's plural name, the response is one JSON string.
		if err := json.Unmarshal(body, &project); err != nil {
			return "", fmt.Errorf("decoding BigQuery default project: %w", err)
		}
	}
	if project == "" {
		return "", fmt.Errorf("database (BigQuery project) is required; set it or configure defaultProject on the datasource")
	}
	if err := d.ValidateIdentifier(project, "database"); err != nil {
		return "", err
	}
	return project, nil
}

func (d *bigqueryDialect) validateMetadataScope(project, dataset string) error {
	if dataset == "" {
		return fmt.Errorf("schema (BigQuery dataset) is required")
	}
	for field, value := range map[string]string{"database": project, "schema": dataset} {
		if err := d.ValidateIdentifier(value, field); err != nil {
			return err
		}
	}
	return nil
}

func (d *bigqueryDialect) ListDatabases(ctx context.Context, qr QueryRunner, _ string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	project, err := d.resolveMetadataProject(ctx, qr, params.DatasourceUID, params.Database)
	if err != nil {
		return nil, err
	}
	body, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/datasets", map[string]string{"project": project})
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(body, &names); err != nil {
		return nil, fmt.Errorf("decoding BigQuery datasets: %w", err)
	}
	result := &ListSQLDatabasesResult{Databases: make([]SQLDatabaseInfo, 0, len(names))}
	for _, name := range names {
		result.Databases = append(result.Databases, SQLDatabaseInfo{Database: project, Schema: name})
	}
	return result, nil
}

func (d *bigqueryDialect) ListTables(ctx context.Context, qr QueryRunner, _ string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
	if err := d.validateMetadataScope(params.Database, params.Schema); err != nil {
		return nil, err
	}
	project, err := d.resolveMetadataProject(ctx, qr, params.DatasourceUID, params.Database)
	if err != nil {
		return nil, err
	}
	// The plugin's completion endpoint uses BigQuery tables.list. It returns
	// names only, including views. Never enrich these with billable SQL jobs.
	body, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/tables", map[string]string{
		"project": project, "dataset": params.Schema,
	})
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(body, &names); err != nil {
		return nil, fmt.Errorf("decoding BigQuery tables: %w", err)
	}
	result := &ListSQLTablesResult{Tables: make([]SQLTableInfo, 0, len(names))}
	for _, name := range names {
		result.Tables = append(result.Tables, SQLTableInfo{Database: project, Schema: params.Schema, Name: name})
	}
	return result, nil
}

// These fields match the plugin's /dataset/table/schema response, not the
// BigQuery REST schema. The plugin omits table descriptions, required/default
// flags, and table types; unknown metadata must stay unset, not be guessed.
type bigquerySchemaField struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Type        string                `json:"type"`
	Repeated    bool                  `json:"repeated"`
	Schema      []bigquerySchemaField `json:"schema"`
}

func (f bigquerySchemaField) sqlType() string {
	typ := f.Type
	switch typ {
	case "INTEGER":
		typ = "INT64"
	case "FLOAT":
		typ = "FLOAT64"
	case "BOOLEAN":
		typ = "BOOL"
	case "RECORD":
		fields := make([]string, 0, len(f.Schema))
		for _, child := range f.Schema {
			fields = append(fields, child.Name+" "+child.sqlType())
		}
		typ = "STRUCT<" + strings.Join(fields, ", ") + ">"
	}
	if f.Repeated {
		typ = "ARRAY<" + typ + ">"
	}
	return typ
}

func appendBigQueryColumns(columns []SQLColumnInfo, fields []bigquerySchemaField, prefix string) []SQLColumnInfo {
	for _, field := range fields {
		name := prefix + field.Name
		columns = append(columns, SQLColumnInfo{Name: name, Type: field.sqlType(), Comment: field.Description})
		columns = appendBigQueryColumns(columns, field.Schema, name+".")
	}
	return columns
}

func (d *bigqueryDialect) DescribeTable(ctx context.Context, qr QueryRunner, _ string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if err := d.validateMetadataScope(params.Database, params.Schema); err != nil {
		return nil, err
	}
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if err := d.ValidateIdentifier(params.Table, "table"); err != nil {
		return nil, err
	}
	project, err := d.resolveMetadataProject(ctx, qr, params.DatasourceUID, params.Database)
	if err != nil {
		return nil, err
	}
	// This resource is backed by BigQuery tables.get, not a query job. Do not
	// fall back to INFORMATION_SCHEMA, sampling, or any other SQL on failure.
	body, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/dataset/table/schema", map[string]string{
		"project": project, "dataset": params.Schema, "table": params.Table,
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		Schema []bigquerySchemaField `json:"schema"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decoding BigQuery table schema: %w", err)
	}
	return &DescribeSQLTableResult{Columns: appendBigQueryColumns(make([]SQLColumnInfo, 0), response.Schema, "")}, nil
}
