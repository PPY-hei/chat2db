package dbexec

import (
	"context"
	"fmt"
	"strings"

	"github.com/chy/chat2db/server/internal/model"
)

// Schema represents a PG schema.
type Schema struct {
	Name string `json:"name"`
}

// TableInfo represents a table or view.
type TableInfo struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // table | view | matview
}

// ColumnInfo describes a column.
type ColumnInfo struct {
	Name         string  `json:"name"`
	DataType     string  `json:"data_type"`
	Nullable     bool    `json:"nullable"`
	DefaultValue *string `json:"default_value,omitempty"`
	IsPrimary    bool    `json:"is_primary"`
}

// ListSchemas 返回当前数据库中的所有 schema（含系统 schema，仅排除临时 schema）。
// 使用 pg_catalog.pg_namespace，避免 information_schema.schemata 的权限过滤漏掉
// 当前用户没有 USAGE 权限但依然可见的命名空间。
func ListSchemas(ctx context.Context, c *model.Connection) ([]Schema, error) {
	res, err := Exec(ctx, c, `SELECT nspname FROM pg_catalog.pg_namespace
WHERE nspname NOT LIKE 'pg_temp_%'
  AND nspname NOT LIKE 'pg_toast_temp_%'
ORDER BY
  CASE
    WHEN nspname IN ('pg_catalog','information_schema','pg_toast') THEN 2
    ELSE 1
  END,
  nspname`)
	if err != nil {
		return nil, err
	}
	out := make([]Schema, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 {
			if s, ok := r[0].(string); ok {
				out = append(out, Schema{Name: s})
			}
		}
	}
	return out, nil
}

// ListTables lists tables/views in a schema.
func ListTables(ctx context.Context, c *model.Connection, schema string) ([]TableInfo, error) {
	q := `SELECT n.nspname, cls.relname,
  CASE cls.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'matview' WHEN 'p' THEN 'table' ELSE cls.relkind::text END
FROM pg_class cls
JOIN pg_namespace n ON n.oid = cls.relnamespace
WHERE n.nspname = $1
AND cls.relkind IN ('r','v','m','p')
ORDER BY cls.relname`
	res, err := Exec(ctx, c, q, schema)
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

// ListColumns returns columns for a specific table.
func ListColumns(ctx context.Context, c *model.Connection, schema, table string) ([]ColumnInfo, error) {
	q := `SELECT a.attname,
  pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
  NOT a.attnotnull AS nullable,
  pg_get_expr(ad.adbin, ad.adrelid) AS default_value,
  COALESCE(pk.is_primary, false) AS is_primary
FROM pg_attribute a
JOIN pg_class cls ON cls.oid = a.attrelid
JOIN pg_namespace n ON n.oid = cls.relnamespace
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
LEFT JOIN (
  SELECT conrelid, unnest(conkey) AS attnum, true AS is_primary
  FROM pg_constraint WHERE contype = 'p'
) pk ON pk.conrelid = a.attrelid AND pk.attnum = a.attnum
WHERE n.nspname = $1
AND cls.relname = $2
AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`
	res, err := Exec(ctx, c, q, schema, table)
	if err != nil {
		return nil, err
	}
	out := make([]ColumnInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) < 5 {
			continue
		}
		ci := ColumnInfo{
			Name:      asString(r[0]),
			DataType:  asString(r[1]),
			Nullable:  asBool(r[2]),
			IsPrimary: asBool(r[4]),
		}
		if r[3] != nil {
			s := asString(r[3])
			ci.DefaultValue = &s
		}
		out = append(out, ci)
	}
	return out, nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return ""
}

func asBool(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// GenerateTableDDL 生成一段便于阅读的表 DDL（列定义 + 主键 + 索引 + 表/列注释）。
// 用于 AI 对话场景向大模型提供表结构上下文。
func GenerateTableDDL(ctx context.Context, c *model.Connection, schema, table string) (string, error) {
	cols, err := ListColumns(ctx, c, schema, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", nil
	}

	// 列定义
	var b strings.Builder
	fmt.Fprintf(&b, "-- postgres table: %q.%q\n", schema, table)
	fmt.Fprintf(&b, "CREATE TABLE %q.%q (\n", schema, table)
	pkCols := make([]string, 0)
	for i, col := range cols {
		line := fmt.Sprintf("  %q %s", col.Name, col.DataType)
		if !col.Nullable {
			line += " NOT NULL"
		}
		if col.DefaultValue != nil && *col.DefaultValue != "" {
			line += " DEFAULT " + *col.DefaultValue
		}
		if i < len(cols)-1 {
			line += ","
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if col.IsPrimary {
			pkCols = append(pkCols, fmt.Sprintf("%q", col.Name))
		}
	}
	if len(pkCols) > 0 {
		// 若已有列名以逗号结尾则需要保持 SQL 合法；重写最后一行
		fmt.Fprintf(&b, "  , PRIMARY KEY (%s)\n", strings.Join(pkCols, ", "))
	}
	b.WriteString(");\n")

	// 索引
	idxRes, err := Exec(ctx, c, `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2 ORDER BY indexname`, schema, table)
	if err == nil {
		for _, r := range idxRes.Rows {
			if len(r) < 2 {
				continue
			}
			def := asString(r[1])
			if def != "" {
				b.WriteString(def)
				if !strings.HasSuffix(def, ";") {
					b.WriteByte(';')
				}
				b.WriteByte('\n')
			}
		}
	}

	// 表 / 列注释
	tblComment, err := Exec(ctx, c, `SELECT obj_description(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2`, schema, table)
	if err == nil && len(tblComment.Rows) > 0 && len(tblComment.Rows[0]) > 0 && tblComment.Rows[0][0] != nil {
		desc := asString(tblComment.Rows[0][0])
		if desc != "" {
			fmt.Fprintf(&b, "COMMENT ON TABLE %q.%q IS %s;\n", schema, table, pgQuote(desc))
		}
	}

	colComments, err := Exec(ctx, c, `SELECT a.attname, col_description(c.oid, a.attnum)
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
  AND col_description(c.oid, a.attnum) IS NOT NULL
ORDER BY a.attnum`, schema, table)
	if err == nil {
		for _, r := range colComments.Rows {
			if len(r) < 2 {
				continue
			}
			name := asString(r[0])
			cmt := asString(r[1])
			if name != "" && cmt != "" {
				fmt.Fprintf(&b, "COMMENT ON COLUMN %q.%q.%q IS %s;\n", schema, table, name, pgQuote(cmt))
			}
		}
	}

	return b.String(), nil
}

// pgQuote 返回 PG 风格的单引号字符串字面量。
func pgQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
