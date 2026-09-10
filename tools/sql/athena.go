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

var (
	athenaTimeFilterRe = regexp.MustCompile(`\$__timeFilter\(([^)]+)\)`)
	athenaDateFilterRe = regexp.MustCompile(`\$__dateFilter\(([^)]+)\)`)
	athenaUnixFilterRe = regexp.MustCompile(`\$__unixEpochFilter\(([^)]+)\)`)
)

type athenaDialect struct{}

func init() {
	RegisterDialect(&athenaDialect{})
}

func (d *athenaDialect) DatasourceTypes() []string {
	return []string{AthenaDatasourceType}
}

func (d *athenaDialect) DefaultFormat() interface{} {
	return 1
}

func (d *athenaDialect) ValidateIdentifier(name, field string) error {
	return ValidateSQLIdentifier(name, field)
}

func (d *athenaDialect) ExtraQueryPayloadFields(params QuerySQLParams) map[string]interface{} {
	connectionArgs := map[string]interface{}{}
	if params.Region != "" {
		connectionArgs["region"] = params.Region
	}
	if params.Catalog != "" {
		connectionArgs["catalog"] = params.Catalog
	}
	if params.Database != "" {
		connectionArgs["database"] = params.Database
	}
	if params.ResultReuseEnabled {
		connectionArgs["resultReuseEnabled"] = true
		if params.ResultReuseMaxAgeInMinutes > 0 {
			connectionArgs["resultReuseMaxAgeInMinutes"] = params.ResultReuseMaxAgeInMinutes
		}
	}
	if len(connectionArgs) == 0 {
		return nil
	}
	return map[string]interface{}{
		"connectionArgs": connectionArgs,
	}
}

func (d *athenaDialect) SubstituteMacros(query string, from, to time.Time) string {
	fromTS := from.UTC().Format("2006-01-02 15:04:05")
	toTS := to.UTC().Format("2006-01-02 15:04:05")
	fromDate := from.UTC().Format("2006-01-02")
	toDate := to.UTC().Format("2006-01-02")
	fromUnix := from.UTC().Unix()
	toUnix := to.UTC().Unix()
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()
	rangeSeconds := toUnix - fromUnix
	intervalSeconds := rangeSeconds / 1000
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}

	query = athenaTimeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := athenaTimeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			col := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN TIMESTAMP '%s' AND TIMESTAMP '%s'", col, fromTS, toTS)
		}
		return match
	})

	query = athenaDateFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := athenaDateFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			col := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN date '%s' AND date '%s'", col, fromDate, toDate)
		}
		return match
	})

	query = athenaUnixFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := athenaUnixFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			col := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN %d AND %d", col, fromUnix, toUnix)
		}
		return match
	})

	query = strings.ReplaceAll(query, "$__timeFrom()", fmt.Sprintf("TIMESTAMP '%s'", fromTS))
	query = strings.ReplaceAll(query, "$__timeTo()", fmt.Sprintf("TIMESTAMP '%s'", toTS))
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))
	query = strings.ReplaceAll(query, "$__interval", strconv.FormatInt(intervalSeconds, 10))

	return query
}

func (d *athenaDialect) EnforceLimit(query string, limit int) string {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "SHOW") || strings.HasPrefix(upper, "DESCRIBE") {
		return query
	}
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

func (d *athenaDialect) ListDatabases(ctx context.Context, qr QueryRunner, _ string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	body := map[string]string{}
	if params.Region != "" {
		body["region"] = params.Region
	}

	if params.Catalog == "" {
		respBytes, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/catalogs", body)
		if err != nil {
			return nil, err
		}

		var names []string
		if err := json.Unmarshal(respBytes, &names); err != nil {
			return nil, err
		}

		dbs := make([]SQLDatabaseInfo, 0, len(names))
		for _, name := range names {
			dbs = append(dbs, SQLDatabaseInfo{Catalog: name})
		}
		return &ListSQLDatabasesResult{Databases: dbs}, nil
	}

	body["catalog"] = params.Catalog
	respBytes, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/databases", body)
	if err != nil {
		return nil, err
	}

	var names []string
	if err := json.Unmarshal(respBytes, &names); err != nil {
		return nil, err
	}

	dbs := make([]SQLDatabaseInfo, 0, len(names))
	for _, name := range names {
		dbs = append(dbs, SQLDatabaseInfo{
			Catalog:  params.Catalog,
			Database: name,
		})
	}
	return &ListSQLDatabasesResult{Databases: dbs}, nil
}

func (d *athenaDialect) ListTables(ctx context.Context, qr QueryRunner, _ string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
	body := map[string]string{}
	if params.Region != "" {
		body["region"] = params.Region
	}
	if params.Catalog != "" {
		body["catalog"] = params.Catalog
	}
	if params.Database != "" {
		body["database"] = params.Database
	}

	respBytes, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/tables", body)
	if err != nil {
		return nil, err
	}

	var names []string
	if err := json.Unmarshal(respBytes, &names); err != nil {
		return nil, err
	}

	tables := make([]SQLTableInfo, 0, len(names))
	for _, name := range names {
		tables = append(tables, SQLTableInfo{
			Database: params.Database,
			Schema:   params.Catalog,
			Name:     name,
		})
	}
	return &ListSQLTablesResult{Tables: tables}, nil
}

func (d *athenaDialect) DescribeTable(ctx context.Context, qr QueryRunner, _ string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	body := map[string]string{
		"table": params.Table,
	}
	if params.Region != "" {
		body["region"] = params.Region
	}
	if params.Catalog != "" {
		body["catalog"] = params.Catalog
	}
	if params.Database != "" {
		body["database"] = params.Database
	}

	respBytes, err := qr.ResourceRequest(ctx, params.DatasourceUID, "/columns", body)
	if err != nil {
		return nil, err
	}

	var names []string
	if err := json.Unmarshal(respBytes, &names); err != nil {
		return nil, err
	}

	columns := make([]SQLColumnInfo, 0, len(names))
	for _, name := range names {
		columns = append(columns, SQLColumnInfo{Name: name})
	}
	return &DescribeSQLTableResult{Columns: columns}, nil
}
