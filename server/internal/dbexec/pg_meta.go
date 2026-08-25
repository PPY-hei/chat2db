package dbexec

import (
	"context"
	"fmt"
	"strings"

	"github.com/chy/chat2db/server/internal/model"
)

// pgListDatabases 列出当前 PG 实例上的所有非模板数据库。
func pgListDatabases(ctx context.Context, c *model.Connection) ([]DatabaseInfo, error) {
	res, err := Exec(ctx, c, `SELECT datname, pg_catalog.pg_get_userbyid(datdba) AS owner
FROM pg_catalog.pg_database
WHERE datistemplate = false
ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	out := make([]DatabaseInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) < 2 {
			continue
		}
		name := asString(r[0])
		owner := asString(r[1])
		out = append(out, DatabaseInfo{
			Name:    name,
			Owner:   owner,
			Current: name == c.Database,
		})
	}
	return out, nil
}

// pgListSchemas 返回当前数据库中的所有 schema。
func pgListSchemas(ctx context.Context, c *model.Connection) ([]Schema, error) {
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

// pgListTables lists tables/views in a schema.
func pgListTables(ctx context.Context, c *model.Connection, schema string) ([]TableInfo, error) {
	q := `SELECT n.nspname, cls.relname,
  CASE cls.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'matview' WHEN 'p' THEN 'table' ELSE cls.relkind::text END
FROM pg_class cls
JOIN pg_namespace n ON n.oid = cls.relnamespace
WHERE n.nspname = $1
AND cls.relkind IN ('r','v','m','p')
ORDER BY cls.relname`
	res, err := pgExecAllRows(ctx, c, q, schema)
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

func pgListTablesFiltered(ctx context.Context, c *model.Connection, schema, search string) ([]TableInfo, error) {
	q := `SELECT n.nspname, cls.relname,
  CASE cls.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'matview' WHEN 'p' THEN 'table' ELSE cls.relkind::text END
FROM pg_class cls
JOIN pg_namespace n ON n.oid = cls.relnamespace
WHERE n.nspname = $1
  AND cls.relkind IN ('r','v','m','p')
  AND cls.relname ILIKE '%' || $2 || '%'
ORDER BY cls.relname`
	res, err := pgExecAllRows(ctx, c, q, schema, search)
	if err != nil {
		return nil, err
	}
	return pgTableInfoRows(res.Rows), nil
}

func pgTableInfoRows(rows [][]any) []TableInfo {
	out := make([]TableInfo, 0, len(rows))
	for _, r := range rows {
		if len(r) >= 3 {
			out = append(out, TableInfo{Schema: asString(r[0]), Name: asString(r[1]), Kind: asString(r[2])})
		}
	}
	return out
}

// pgListColumns returns columns for a specific table.
func pgListColumns(ctx context.Context, c *model.Connection, schema, table string) ([]ColumnInfo, error) {
	q := `SELECT a.attname,
  pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
  NOT a.attnotnull AS nullable,
  pg_get_expr(ad.adbin, ad.adrelid) AS default_value,
  COALESCE(pk.is_primary, false) AS is_primary,
  col_description(cls.oid, a.attnum) AS comment,
  (a.attidentity <> '' OR COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') LIKE 'nextval(%') AS auto_increment
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

// pgListIndexes 返回 PG 表的索引（结构化：名/列/唯一/主键/方法）。
func pgListIndexes(ctx context.Context, c *model.Connection, schema, table string) ([]IndexInfo, error) {
	q := `SELECT i.relname AS index_name,
  ix.indisunique AS is_unique,
  ix.indisprimary AS is_primary,
  am.amname AS method,
  a.attname AS column_name
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN pg_am am ON am.oid = i.relam
JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE n.nspname = $1 AND t.relname = $2
ORDER BY i.relname, k.ord`
	res, err := Exec(ctx, c, q, schema, table)
	if err != nil {
		return nil, err
	}
	// 按 index 名聚合列（结果已按 index_name, ord 排序）
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
				Unique:  asBool(r[1]),
				Primary: asBool(r[2]),
				Method:  asString(r[3]),
			}
			byName[name] = idx
			order = append(order, name)
		}
		idx.Columns = append(idx.Columns, asString(r[4]))
	}
	out := make([]IndexInfo, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

// pgGenerateTableDDL 生成 PG 表/视图 DDL。
func pgGenerateTableDDL(ctx context.Context, c *model.Connection, schema, table string) (string, error) {
	// Views expose columns through pg_attribute just like tables, but those
	// columns are not enough to reconstruct the object. Fetch the stored view
	// definition first so the DDL can recreate the view itself.
	objRes, err := Exec(ctx, c, `SELECT c.relkind::text, CASE WHEN c.relkind IN ('v', 'm') THEN pg_get_viewdef(c.oid, true) END
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, schema, table)
	if err != nil {
		return "", err
	}
	if len(objRes.Rows) > 0 && len(objRes.Rows[0]) > 0 {
		relkind := asString(objRes.Rows[0][0])
		if relkind == "v" || relkind == "m" {
			definition := ""
			if len(objRes.Rows[0]) > 1 {
				definition = asString(objRes.Rows[0][1])
			}
			return pgRenderViewDDL(schema, table, relkind == "m", definition), nil
		}
	}

	cols, err := pgListColumns(ctx, c, schema, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", nil
	}

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

// pgRenderViewDDL renders the definition returned by pg_get_viewdef.
// pg_get_viewdef(..., true) returns a complete SELECT body without a trailing
// semicolon, so normalize it here before appending the statement terminator.
func pgRenderViewDDL(schema, table string, materialized bool, definition string) string {
	definition = strings.TrimSpace(definition)
	definition = strings.TrimSpace(strings.TrimSuffix(definition, ";"))
	kind := "VIEW"
	if materialized {
		kind = "MATERIALIZED VIEW"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "-- postgres %s: %q.%q\n", strings.ToLower(kind), schema, table)
	fmt.Fprintf(&b, "CREATE %s %q.%q AS\n", kind, schema, table)
	b.WriteString(definition)
	b.WriteString(";\n")
	return b.String()
}

// pgQuote 返回 PG 风格的单引号字符串字面量。
func pgQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
