// Package SQL implements connectors for SQL Databases
package sql

// Common aliases for columns to be used with meta queries.
const (
	TableNameColumn    = "table_name"
	DatabaseNameColumn = "database_name"

	// ColNameColumn is the alias for the column name in schema results.
	ColNameColumn = "column_name"
	// ColTypeColumn is the alias for the column data type in schema results.
	ColTypeColumn = "data_type"

	DBVersionColumn          = "db_version"
	TimeScaleDbVersionColumn = "timescaledb_version"
)
