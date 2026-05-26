package dbexec

import (
	"context"
	"fmt"

	"github.com/chy/chat2db/server/internal/model"
)

// hiveListDatabases returns all databases in the Hive instance.
func hiveListDatabases(ctx context.Context, c *model.Connection) ([]DatabaseInfo, error) {
	result, err := hiveExec(ctx, c, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}

	var dbs []DatabaseInfo
	for _, row := range result.Rows {
		if len(row) > 0 {
			dbName := fmt.Sprintf("%v", row[0])
			dbs = append(dbs, DatabaseInfo{
				Name:    dbName,
				Current: dbName == c.Database,
			})
		}
	}
	return dbs, nil
}

// hiveListSchemas returns all schemas in the current database.
// Hive不区分database和schema，返回当前数据库作为唯一的schema。
func hiveListSchemas(ctx context.Context, c *model.Connection) ([]Schema, error) {
	dbName := c.Database
	if dbName == "" {
		dbName = "default"
	}
	return []Schema{{Name: dbName}}, nil
}

// hiveListTables returns all tables in the specified schema (database).
func hiveListTables(ctx context.Context, c *model.Connection, schema string) ([]TableInfo, error) {
	var sql string
	// schema 参数在 Hive 中对应 database
	if schema != "" {
		sql = fmt.Sprintf("SHOW TABLES IN %s", schema)
	} else if c.Database != "" {
		sql = fmt.Sprintf("SHOW TABLES IN %s", c.Database)
	} else {
		sql = "SHOW TABLES"
	}

	result, err := hiveExec(ctx, c, sql)
	if err != nil {
		return nil, err
	}

	var tables []TableInfo
	for _, row := range result.Rows {
		if len(row) > 0 {
			tableName := fmt.Sprintf("%v", row[0])
			// 使用传入的 schema，如果为空则使用连接的 database
			schemaName := schema
			if schemaName == "" {
				schemaName = c.Database
			}
			tables = append(tables, TableInfo{
				Name:   tableName,
				Kind:   "table",
				Schema: schemaName,
			})
		}
	}
	return tables, nil
}

// hiveListColumns returns all columns for the specified table.
func hiveListColumns(ctx context.Context, c *model.Connection, schema, table string) ([]ColumnInfo, error) {
	var sql string
	// 在 Hive 中，始终使用 database.table 格式更安全
	dbName := schema
	if dbName == "" {
		dbName = c.Database
	}
	if dbName == "" {
		dbName = "default"
	}
	sql = fmt.Sprintf("DESCRIBE %s.%s", dbName, table)

	result, err := hiveExec(ctx, c, sql)
	if err != nil {
		return nil, err
	}

	var columns []ColumnInfo
	for _, row := range result.Rows {
		if len(row) >= 2 {
			colName := fmt.Sprintf("%v", row[0])
			colType := fmt.Sprintf("%v", row[1])

			// 跳过分区信息和空行
			if colName == "" || colName == "# Partition Information" ||
				colName == "# col_name" || colName == "Partition Type:" {
				continue
			}

			comment := ""
			if len(row) >= 3 && row[2] != nil {
				comment = fmt.Sprintf("%v", row[2])
			}

			var commentPtr *string
			if comment != "" {
				commentPtr = &comment
			}

			columns = append(columns, ColumnInfo{
				Name:         colName,
				DataType:     colType,
				Nullable:     true,
				DefaultValue: nil,
				Comment:      commentPtr,
				IsPrimary:    false,
			})
		}
	}
	return columns, nil
}

// hiveListIndexes returns all indexes for the specified table.
// Hive不支持传统索引，返回空列表。
func hiveListIndexes(ctx context.Context, c *model.Connection, schema, table string) ([]IndexInfo, error) {
	return []IndexInfo{}, nil
}

// hiveGenerateTableDDL generates the DDL for the specified table.
func hiveGenerateTableDDL(ctx context.Context, c *model.Connection, schema, table string) (string, error) {
	// 在 Hive 中，始终使用 database.table 格式更安全
	dbName := schema
	if dbName == "" {
		dbName = c.Database
	}
	if dbName == "" {
		dbName = "default"
	}
	sql := fmt.Sprintf("SHOW CREATE TABLE %s.%s", dbName, table)

	result, err := hiveExec(ctx, c, sql)
	if err != nil {
		return "", err
	}

	if len(result.Rows) == 0 {
		return "", fmt.Errorf("no DDL returned for table %s", table)
	}

	// SHOW CREATE TABLE 返回的DDL通常在第一行第一列
	ddl := ""
	for _, row := range result.Rows {
		if len(row) > 0 {
			ddl += fmt.Sprintf("%v\n", row[0])
		}
	}
	return ddl, nil
}
