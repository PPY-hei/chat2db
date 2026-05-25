package dbexec

import (
	"context"
	"fmt"
	"strings"

	"github.com/chy/chat2db/server/internal/model"
)

// mysqlListDatabases 列出 MySQL 实例上的所有数据库。
func mysqlListDatabases(ctx context.Context, c *model.Connection) ([]DatabaseInfo, error) {
	res, err := mysqlExec(ctx, c, `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, err
	}
	out := make([]DatabaseInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) < 1 {
			continue
		}
		name := asString(r[0])
		out = append(out, DatabaseInfo{
			Name:    name,
			Owner:   "",
			Current: name == c.Database,
		})
	}
	return out, nil
}

// mysqlListSchemas MySQL 没有独立于 database 的 schema 概念。
// 返回当前 database 名作为唯一 schema，让前端树结构保持一致。
func mysqlListSchemas(ctx context.Context, c *model.Connection) ([]Schema, error) {
	dbName := c.Database
	if dbName == "" {
		dbName = "information_schema"
	}
	return []Schema{{Name: dbName}}, nil
}

// mysqlListTables lists tables/views in the current database.
// schema 参数对 MySQL 来说被忽略（MySQL 的 schema == database）。
func mysqlListTables(ctx context.Context, c *model.Connection, schema string) ([]TableInfo, error) {
	q := `SELECT TABLE_SCHEMA, TABLE_NAME,
  CASE TABLE_TYPE WHEN 'BASE TABLE' THEN 'table' WHEN 'VIEW' THEN 'view' ELSE 'table' END AS kind
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME`
	// 对 MySQL，schema 就是 database
	dbName := c.Database
	if schema != "" {
		dbName = schema
	}
	res, err := mysqlExec(ctx, c, q, dbName)
	if err != nil {
		return nil, err
	}
	out := make([]TableInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) < 3 {
			continue
		}
		out = append(out, TableInfo{
			Schema: asString(r[0]),
			Name:   asString(r[1]),
			Kind:   asString(r[2]),
		})
	}
	return out, nil
}

// mysqlListColumns returns columns for a specific table.
func mysqlListColumns(ctx context.Context, c *model.Connection, schema, table string) ([]ColumnInfo, error) {
	dbName := c.Database
	if schema != "" {
		dbName = schema
	}
	q := `SELECT
  c.COLUMN_NAME,
  c.COLUMN_TYPE,
  CASE c.IS_NULLABLE WHEN 'YES' THEN 1 ELSE 0 END AS nullable,
  c.COLUMN_DEFAULT,
  CASE WHEN kcu.COLUMN_NAME IS NOT NULL THEN 1 ELSE 0 END AS is_primary,
  c.COLUMN_COMMENT,
  CASE WHEN c.EXTRA LIKE '%auto_increment%' THEN 1 ELSE 0 END AS auto_increment
FROM information_schema.COLUMNS c
LEFT JOIN information_schema.KEY_COLUMN_USAGE kcu
  ON kcu.TABLE_SCHEMA = c.TABLE_SCHEMA
  AND kcu.TABLE_NAME = c.TABLE_NAME
  AND kcu.COLUMN_NAME = c.COLUMN_NAME
  AND kcu.CONSTRAINT_NAME = 'PRIMARY'
WHERE c.TABLE_SCHEMA = ?
  AND c.TABLE_NAME = ?
ORDER BY c.ORDINAL_POSITION`
	res, err := mysqlExec(ctx, c, q, dbName, table)
	if err != nil {
		return nil, err
	}
	out := make([]ColumnInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) < 7 {
			continue
		}
		ci := ColumnInfo{
			Name:          asString(r[0]),
			DataType:      asString(r[1]),
			Nullable:      asBool(r[2]),
			IsPrimary:     asBool(r[4]),
			AutoIncrement: asBool(r[6]),
		}
		if r[3] != nil {
			s := asString(r[3])
			ci.DefaultValue = &s
		}
		if r[5] != nil {
			s := asString(r[5])
			if s != "" {
				ci.Comment = &s
			}
		}
		out = append(out, ci)
	}
	return out, nil
}

// mysqlListIndexes 返回 MySQL 表的索引（按 information_schema.STATISTICS 聚合）。
func mysqlListIndexes(ctx context.Context, c *model.Connection, schema, table string) ([]IndexInfo, error) {
	dbName := c.Database
	if schema != "" {
		dbName = schema
	}
	q := `SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, INDEX_TYPE
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX`
	res, err := mysqlExec(ctx, c, q, dbName, table)
	if err != nil {
		return nil, err
	}
	order := make([]string, 0)
	byName := make(map[string]*IndexInfo)
	for _, r := range res.Rows {
		if len(r) < 5 {
			continue
		}
		name := asString(r[0])
		idx, ok := byName[name]
		if !ok {
			idx = &IndexInfo{
				Name:    name,
				Unique:  !asBool(r[1]), // NON_UNIQUE=0 表示唯一
				Primary: name == "PRIMARY",
				Method:  asString(r[4]),
			}
			byName[name] = idx
			order = append(order, name)
		}
		idx.Columns = append(idx.Columns, asString(r[3]))
	}
	out := make([]IndexInfo, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

// mysqlGenerateTableDDL 使用 SHOW CREATE TABLE 获取 MySQL 表 DDL。
func mysqlGenerateTableDDL(ctx context.Context, c *model.Connection, schema, table string) (string, error) {
	dbName := c.Database
	if schema != "" {
		dbName = schema
	}

	// 先切到目标数据库再 SHOW CREATE TABLE
	fullName := fmt.Sprintf("`%s`.`%s`", dbName, table)
	res, err := mysqlExec(ctx, c, "SHOW CREATE TABLE "+fullName)
	if err != nil {
		return "", err
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) >= 2 {
		ddl := asString(res.Rows[0][1])
		var b strings.Builder
		fmt.Fprintf(&b, "-- mysql table: %s.%s\n", dbName, table)
		b.WriteString(ddl)
		if !strings.HasSuffix(ddl, ";") {
			b.WriteByte(';')
		}
		b.WriteByte('\n')
		return b.String(), nil
	}
	return "", nil
}
