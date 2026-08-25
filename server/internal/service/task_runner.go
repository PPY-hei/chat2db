package service

import (
	"archive/zip"
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/dbexec"
	"github.com/chy/chat2db/server/internal/model"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	errTaskCanceled         = errors.New("task canceled")
	errImportNotImplemented = errors.New("import task is not implemented yet")
	errUnknownKind          = errors.New("unknown task kind")
	errUnsupportedDriver    = errors.New("driver not supported for this operation")
)

const batchSize = 1000 // 数据同步批量大小

type syncDataResult struct {
	SourceRows  int64
	SuccessRows int64
	FailedRows  int64
}

func (r syncDataResult) add(other syncDataResult) syncDataResult {
	r.SourceRows += other.SourceRows
	r.SuccessRows += other.SuccessRows
	r.FailedRows += other.FailedRows
	return r
}

// runExportTask 是导出任务的统一入口：先列出"要导出的表清单"，
// 再循环逐表流式 dump 到目标格式；多张表时打 zip，单张表保留原文件。
func runExportTask(ctx context.Context, t *model.Task) error {
	conn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load connection: %w", err)
	}
	opts, err := parseExportTaskOptions(t.Params)
	if err != nil {
		return err
	}
	if opts.Format == exportFormatInsertSQL && t.Scope != model.TaskScopeTable {
		return errors.New("insert_sql export is only supported for scope=table")
	}
	if opts.Where != "" && t.Scope != model.TaskScopeTable {
		return errors.New("export where condition is only supported for scope=table")
	}

	tables, err := resolveExportTables(ctx, conn, t)
	if err != nil {
		return fmt.Errorf("resolve target tables: %w", err)
	}
	if len(tables) == 0 {
		return errors.New("no tables to export")
	}

	// 准备产物目录：<artifactDir>/<task_id>/
	taskDir := filepath.Join(taskArtifactDir, fmt.Sprintf("%d", t.ID))
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return fmt.Errorf("mkdir artifact: %w", err)
	}

	if err := updateTaskFields(t.ID, map[string]any{
		"total_tables": len(tables),
		"done_tables":  0,
	}); err != nil {
		return err
	}

	outFiles := make([]string, 0, len(tables))
	var totalRows int64
	for i, et := range tables {
		if err := checkCancel(ctx, t.ID); err != nil {
			return err
		}

		ext := "csv"
		if opts.Format == exportFormatInsertSQL {
			ext = "sql"
		}
		fname := safeFileName(fmt.Sprintf("%s.%s.%s.%s", et.Database, et.Schema, et.Table, ext))
		fpath := filepath.Join(taskDir, fname)
		var rows int64
		if opts.Format == exportFormatInsertSQL {
			rows, err = dumpTableInsertSQL(ctx, conn, et, fpath, t, opts)
		} else {
			rows, err = dumpTableCSV(ctx, conn, et, fpath, t, opts)
		}
		if err != nil {
			return fmt.Errorf("dump %s.%s.%s: %w", et.Database, et.Schema, et.Table, err)
		}
		outFiles = append(outFiles, fpath)
		totalRows += rows

		// 进度按"已完成表数 / 总表数" 计算（粒度更稳）。
		prog := int(float64(i+1) / float64(len(tables)) * 100)
		_ = updateTaskFields(t.ID, map[string]any{
			"done_tables":    i + 1,
			"processed_rows": totalRows,
			"progress":       prog,
		})
	}

	// 多表时打 zip。单表保留原文件即可。
	var finalPath string
	var finalSize int64
	if len(outFiles) == 1 {
		finalPath = outFiles[0]
		if info, err := os.Stat(finalPath); err == nil {
			finalSize = info.Size()
		}
	} else {
		zipPath := filepath.Join(taskDir, fmt.Sprintf("export-%d.zip", t.ID))
		if err := zipFiles(zipPath, outFiles); err != nil {
			return fmt.Errorf("zip: %w", err)
		}
		// 打包后删除中间文件以省盘。
		for _, f := range outFiles {
			_ = os.Remove(f)
		}
		finalPath = zipPath
		if info, err := os.Stat(finalPath); err == nil {
			finalSize = info.Size()
		}
	}

	return updateTaskFields(t.ID, map[string]any{
		"file_path":      finalPath,
		"file_size":      finalSize,
		"processed_rows": totalRows,
	})
}

// exportTable 表示一张待导出的表的完整定位（含 database/schema/table）。
type exportTable struct {
	Database string
	Schema   string
	Table    string
}

// resolveExportTables 按 Scope 解析出实际要导出的表清单。
func resolveExportTables(ctx context.Context, conn *model.Connection, t *model.Task) ([]exportTable, error) {
	switch t.Scope {
	case model.TaskScopeTable:
		return []exportTable{{
			Database: t.TargetDatabase,
			Schema:   t.TargetSchema,
			Table:    t.TargetTable,
		}}, nil

	case model.TaskScopeDatabase:
		return listAllTablesInDatabase(ctx, conn, t.TargetDatabase)

	case model.TaskScopeConnection:
		dbs, err := dbexec.ListDatabases(ctx, conn)
		if err != nil {
			return nil, err
		}
		var all []exportTable
		for _, d := range dbs {
			ts, err := listAllTablesInDatabase(ctx, conn, d.Name)
			if err != nil {
				// 单库失败不影响其它库；记录并跳过。
				continue
			}
			all = append(all, ts...)
		}
		return all, nil

	default:
		return nil, fmt.Errorf("unsupported scope: %s", t.Scope)
	}
}

func runBackupTask(ctx context.Context, t *model.Task) error {
	if t.Scope != model.TaskScopeTable {
		return errors.New("backup task only supports scope=table")
	}
	conn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load connection: %w", err)
	}
	opts, err := parseBackupTaskOptions(t.Params)
	if err != nil {
		return err
	}
	src := exportTable{
		Database: t.TargetDatabase,
		Schema:   t.TargetSchema,
		Table:    t.TargetTable,
	}
	if src.Database == "" || src.Table == "" {
		return errors.New("backup task requires source database and table")
	}
	destTable := opts.BackupTable
	if destTable == "" {
		destTable = t.DestTable
	}
	if destTable == "" {
		return errors.New("backup task requires backup table")
	}
	if src.Table == destTable {
		return errors.New("backup table must be different from source table")
	}

	if err := updateTaskFields(t.ID, map[string]any{
		"total_tables":  1,
		"done_tables":   0,
		"dest_database": src.Database,
		"dest_schema":   src.Schema,
		"dest_table":    destTable,
	}); err != nil {
		return err
	}
	if err := checkCancel(ctx, t.ID); err != nil {
		return err
	}

	var rows int64
	switch conn.Driver {
	case "postgres":
		rows, err = backupPGTable(ctx, conn, src, destTable)
	case "mysql":
		rows, err = backupMySQLTable(ctx, conn, src, destTable)
	default:
		err = fmt.Errorf("driver %s not supported", conn.Driver)
	}
	if err != nil {
		return err
	}
	return updateTaskFields(t.ID, map[string]any{
		"done_tables":    1,
		"processed_rows": rows,
		"total_rows":     rows,
		"progress":       100,
	})
}

func backupPGTable(ctx context.Context, conn *model.Connection, src exportTable, destTable string) (int64, error) {
	c := dbexec.WithDatabase(conn, src.Database)
	pool, err := dbexec.AcquirePGPool(ctx, c)
	if err != nil {
		return 0, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	createSQL, insertSQL := buildPGBackupSQL(src.Schema, src.Table, destTable)
	if _, err := tx.Exec(ctx, createSQL); err != nil {
		return 0, fmt.Errorf("create backup table: %w", err)
	}
	tag, err := tx.Exec(ctx, insertSQL)
	if err != nil {
		return 0, fmt.Errorf("copy table data: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func backupMySQLTable(ctx context.Context, conn *model.Connection, src exportTable, destTable string) (int64, error) {
	c := dbexec.WithDatabase(conn, src.Database)
	pool, err := dbexec.AcquireMySQLPool(ctx, c)
	if err != nil {
		return 0, err
	}
	createSQL, insertSQL := buildMySQLBackupSQL(src.Database, src.Table, destTable)
	if _, err := pool.ExecContext(ctx, createSQL); err != nil {
		return 0, fmt.Errorf("create backup table: %w", err)
	}
	res, err := pool.ExecContext(ctx, insertSQL)
	if err != nil {
		_, _ = pool.ExecContext(ctx, fmt.Sprintf(
			"DROP TABLE `%s`.`%s`",
			escapeMyIdent(src.Database),
			escapeMyIdent(destTable),
		))
		return 0, fmt.Errorf("copy table data: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}

func buildPGBackupSQL(schema, table, destTable string) (string, string) {
	src := quotePGIdent(schema) + "." + quotePGIdent(table)
	dest := quotePGIdent(schema) + "." + quotePGIdent(destTable)
	return fmt.Sprintf("CREATE TABLE %s (LIKE %s INCLUDING ALL)", dest, src),
		fmt.Sprintf("INSERT INTO %s OVERRIDING SYSTEM VALUE SELECT * FROM %s", dest, src)
}

func buildMySQLBackupSQL(database, table, destTable string) (string, string) {
	src := fmt.Sprintf("`%s`.`%s`", escapeMyIdent(database), escapeMyIdent(table))
	dest := fmt.Sprintf("`%s`.`%s`", escapeMyIdent(database), escapeMyIdent(destTable))
	return fmt.Sprintf("CREATE TABLE %s LIKE %s", dest, src),
		fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", dest, src)
}

func listAllTablesInDatabase(ctx context.Context, conn *model.Connection, database string) ([]exportTable, error) {
	c := dbexec.WithDatabase(conn, database)
	schemas, err := dbexec.ListSchemas(ctx, c)
	if err != nil {
		return nil, err
	}
	var out []exportTable
	for _, s := range schemas {
		tables, err := dbexec.ListTables(ctx, c, s.Name)
		if err != nil {
			continue
		}
		for _, tbl := range tables {
			if tbl.Kind != "table" {
				continue // 视图等不导出
			}
			out = append(out, exportTable{Database: database, Schema: s.Name, Table: tbl.Name})
		}
	}
	return out, nil
}

// dumpTableCSV 流式导出单表为 CSV，返回行数。
// PG / MySQL 两条路径，避开了 QUERY_MAX_ROWS / QUERY_TIMEOUT 的限制。
func dumpTableCSV(ctx context.Context, conn *model.Connection, et exportTable, outPath string, t *model.Task, opts exportTaskOptions) (int64, error) {
	f, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	switch conn.Driver {
	case "postgres":
		return dumpPGTable(ctx, conn, et, w, t, opts)
	case "mysql":
		return dumpMySQLTable(ctx, conn, et, w, t, opts)
	default:
		return 0, fmt.Errorf("driver %s not supported", conn.Driver)
	}
}

func dumpPGTable(ctx context.Context, conn *model.Connection, et exportTable, w *csv.Writer, t *model.Task, opts exportTaskOptions) (int64, error) {
	c := dbexec.WithDatabase(conn, et.Database)
	pool, err := dbexec.AcquirePGPool(ctx, c)
	if err != nil {
		return 0, err
	}
	q := buildPGExportSelect(et, opts.Where)
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	header := make([]string, len(fields))
	for i, fd := range fields {
		header[i] = string(fd.Name)
	}
	if err := w.Write(header); err != nil {
		return 0, err
	}

	var n int64
	for rows.Next() {
		if n%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return n, err
			}
		}
		vals, err := rows.Values()
		if err != nil {
			return n, err
		}
		rec := make([]string, len(vals))
		for i, v := range vals {
			rec[i] = formatCSVCell(v)
		}
		if err := w.Write(rec); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func dumpMySQLTable(ctx context.Context, conn *model.Connection, et exportTable, w *csv.Writer, t *model.Task, opts exportTaskOptions) (int64, error) {
	c := dbexec.WithDatabase(conn, et.Database)
	pool, err := dbexec.AcquireMySQLPool(ctx, c)
	if err != nil {
		return 0, err
	}
	// MySQL 中 database == schema；用 backtick 限定。
	q := buildMySQLExportSelect(et, opts.Where)
	rows, err := pool.QueryContext(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if err := w.Write(cols); err != nil {
		return 0, err
	}

	holders := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range holders {
		ptrs[i] = &holders[i]
	}

	var n int64
	for rows.Next() {
		if n%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return n, err
			}
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		rec := make([]string, len(holders))
		for i, v := range holders {
			rec[i] = formatCSVCell(v)
		}
		if err := w.Write(rec); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// dumpTableInsertSQL 流式导出单表为逐行 INSERT SQL，返回行数。
func dumpTableInsertSQL(ctx context.Context, conn *model.Connection, et exportTable, outPath string, t *model.Task, opts exportTaskOptions) (int64, error) {
	f, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	switch conn.Driver {
	case "postgres":
		return dumpPGTableInsertSQL(ctx, conn, et, w, t, opts)
	case "mysql":
		return dumpMySQLTableInsertSQL(ctx, conn, et, w, t, opts)
	default:
		return 0, fmt.Errorf("driver %s not supported", conn.Driver)
	}
}

func dumpPGTableInsertSQL(ctx context.Context, conn *model.Connection, et exportTable, w *bufio.Writer, t *model.Task, opts exportTaskOptions) (int64, error) {
	c := dbexec.WithDatabase(conn, et.Database)
	pool, err := dbexec.AcquirePGPool(ctx, c)
	if err != nil {
		return 0, err
	}
	columnInfoByName, err := loadExportColumnInfoMap(ctx, c, et.Schema, et.Table)
	if err != nil {
		return 0, err
	}
	rows, err := pool.Query(ctx, buildPGExportSelect(et, opts.Where))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	pgInfos := make([]pgExportColumnInfo, len(fields))
	exportInfos := make([]exportColumnInfo, len(fields))
	for i, fd := range fields {
		name := string(fd.Name)
		cols[i] = name
		exportInfo := columnInfoByName[name]
		exportInfo.TypeOID = fd.DataTypeOID
		exportInfos[i] = exportInfo
		pgInfos[i] = pgExportColumnInfo{
			TypeOID:      fd.DataTypeOID,
			DefaultValue: exportInfo.DefaultValue,
		}
	}
	if err := validateExportValueReplacementColumns(cols, opts.ValueReplacements); err != nil {
		return 0, err
	}

	var n int64
	for rows.Next() {
		if n%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return n, err
			}
		}
		vals, err := rows.Values()
		if err != nil {
			return n, err
		}
		vals = applyExportValueReplacements(cols, vals, exportInfos, opts.ValueReplacements)
		stmt, err := buildExportInsertSQLWithPGColumnInfo(et, cols, vals, pgInfos, opts.OnConflictDoNothing)
		if err != nil {
			return n, err
		}
		if _, err := w.WriteString(stmt + "\n"); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func loadExportColumnInfoMap(ctx context.Context, conn *model.Connection, schema, table string) (map[string]exportColumnInfo, error) {
	cols, err := dbexec.ListColumns(ctx, conn, schema, table)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	out := make(map[string]exportColumnInfo, len(cols))
	for _, col := range cols {
		info := exportColumnInfo{
			DataType:      col.DataType,
			Nullable:      col.Nullable,
			AutoIncrement: col.AutoIncrement,
		}
		if col.DefaultValue != nil {
			info.DefaultValue = *col.DefaultValue
			info.HasDefault = strings.TrimSpace(*col.DefaultValue) != ""
		}
		out[col.Name] = info
	}
	return out, nil
}

func dumpMySQLTableInsertSQL(ctx context.Context, conn *model.Connection, et exportTable, w *bufio.Writer, t *model.Task, opts exportTaskOptions) (int64, error) {
	c := dbexec.WithDatabase(conn, et.Database)
	pool, err := dbexec.AcquireMySQLPool(ctx, c)
	if err != nil {
		return 0, err
	}
	rows, err := pool.QueryContext(ctx, buildMySQLExportSelect(et, opts.Where))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	columnInfoByName, err := loadExportColumnInfoMap(ctx, c, et.Schema, et.Table)
	if err != nil {
		return 0, err
	}
	if err := validateExportValueReplacementColumns(cols, opts.ValueReplacements); err != nil {
		return 0, err
	}
	exportInfos := exportColumnInfosForColumns(cols, columnInfoByName)
	holders := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range holders {
		ptrs[i] = &holders[i]
	}

	var n int64
	for rows.Next() {
		if n%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return n, err
			}
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		vals := applyExportValueReplacements(cols, holders, exportInfos, opts.ValueReplacements)
		stmt, err := buildExportInsertSQL(conn.Driver, et, cols, vals, opts.OnConflictDoNothing)
		if err != nil {
			return n, err
		}
		if _, err := w.WriteString(stmt + "\n"); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func validateExportValueReplacementColumns(cols []string, rules []ExportValueReplacement) error {
	if len(rules) == 0 {
		return nil
	}
	colSet := make(map[string]struct{}, len(cols))
	for _, col := range cols {
		colSet[col] = struct{}{}
	}
	for _, rule := range rules {
		if _, ok := colSet[rule.Column]; !ok {
			return fmt.Errorf("value replacement column %q not found in exported table", rule.Column)
		}
	}
	return nil
}

func exportColumnInfosForColumns(cols []string, columnInfoByName map[string]exportColumnInfo) []exportColumnInfo {
	if len(cols) == 0 {
		return nil
	}
	infos := make([]exportColumnInfo, len(cols))
	for i, col := range cols {
		infos[i] = columnInfoByName[col]
	}
	return infos
}

func applyExportValueReplacements(cols []string, vals []any, columnInfos []exportColumnInfo, rules []ExportValueReplacement) []any {
	if len(rules) == 0 || len(cols) == 0 || len(vals) == 0 {
		return vals
	}
	ruleByColumn := make(map[string]ExportValueReplacement, len(rules))
	for _, rule := range rules {
		ruleByColumn[rule.Column] = rule
	}

	var out []any
	for i, col := range cols {
		if i >= len(vals) {
			break
		}
		rule, ok := ruleByColumn[col]
		if !ok {
			continue
		}
		if out == nil {
			out = append([]any(nil), vals...)
		}
		key := exportReplacementKey(vals[i])
		if mapped, ok := rule.Mapping[key]; ok {
			out[i] = exportReplacementMappedValue(mapped, exportColumnInfoAt(columnInfos, i))
			continue
		}
		if rule.OnMissing == exportReplacementOnMissingEmpty {
			out[i] = exportMissingReplacementValue(exportColumnInfoAt(columnInfos, i))
		}
	}
	if out == nil {
		return vals
	}
	return out
}

func exportColumnInfoAt(infos []exportColumnInfo, i int) exportColumnInfo {
	if infos == nil || i < 0 || i >= len(infos) {
		return exportColumnInfo{}
	}
	return infos[i]
}

func exportMissingReplacementValue(info exportColumnInfo) any {
	if info.Nullable {
		return nil
	}
	if info.HasDefault || info.AutoIncrement {
		return exportSQLDefault{}
	}
	return exportZeroValue(info)
}

func exportReplacementMappedValue(raw string, info exportColumnInfo) any {
	if info.DataType == "" && info.TypeOID == 0 {
		return raw
	}

	t := strings.ToLower(strings.TrimSpace(info.DataType))
	base := normalizeExportDataType(t)
	switch {
	case isExportBoolType(t, base):
		if v, ok := parseExportBool(raw); ok {
			return v
		}
	case isExportFloatType(base):
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			return v
		}
	case isExportNumericType(base):
		if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			return v
		}
		if v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); err == nil {
			return v
		}
	}
	return raw
}

func parseExportBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "t", "1", "yes", "y", "on":
		return true, true
	case "false", "f", "0", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func exportZeroValue(info exportColumnInfo) any {
	t := strings.ToLower(strings.TrimSpace(info.DataType))
	base := normalizeExportDataType(t)
	switch {
	case info.TypeOID == pgtype.UUIDOID || strings.Contains(base, "uuid"):
		return "00000000-0000-0000-0000-000000000000"
	case isPGJSONOID(info.TypeOID) || strings.Contains(base, "json"):
		return "{}"
	case strings.HasSuffix(t, "[]"):
		return []any{}
	case isExportBoolType(t, base):
		return false
	case isExportFloatType(base):
		return float64(0)
	case isExportNumericType(base):
		return int64(0)
	case isExportTimeType(base):
		return time.Time{}
	default:
		return ""
	}
}

func normalizeExportDataType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if idx := strings.Index(t, "("); idx >= 0 {
		t = t[:idx]
	}
	return strings.TrimSpace(t)
}

func isExportBoolType(raw, base string) bool {
	return base == "bool" || base == "boolean" || raw == "tinyint(1)"
}

func isExportFloatType(t string) bool {
	return t == "real" ||
		t == "float" ||
		t == "float4" ||
		t == "float8" ||
		t == "double" ||
		t == "double precision"
}

func isExportNumericType(t string) bool {
	return strings.Contains(t, "int") ||
		t == "serial" ||
		t == "bigserial" ||
		t == "smallserial" ||
		t == "numeric" ||
		t == "decimal" ||
		t == "number"
}

func isExportTimeType(t string) bool {
	return strings.Contains(t, "date") ||
		strings.Contains(t, "time")
}

func exportReplacementKey(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int:
		return strconv.Itoa(x)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func buildPGExportSelect(et exportTable, where string) string {
	q := fmt.Sprintf(`SELECT * FROM %s.%s`, quotePGIdent(et.Schema), quotePGIdent(et.Table))
	if where != "" {
		q += " WHERE " + where
	}
	return q
}

func buildMySQLExportSelect(et exportTable, where string) string {
	q := fmt.Sprintf("SELECT * FROM `%s`.`%s`", escapeMyIdent(et.Database), escapeMyIdent(et.Table))
	if where != "" {
		q += " WHERE " + where
	}
	return q
}

func buildExportInsertSQL(driver string, et exportTable, cols []string, vals []any, onConflictDoNothing bool) (string, error) {
	return buildExportInsertSQLWithTypes(driver, et, cols, vals, nil, onConflictDoNothing)
}

func buildExportInsertSQLWithPGTypes(et exportTable, cols []string, vals []any, typeOIDs []uint32, onConflictDoNothing bool) (string, error) {
	return buildExportInsertSQLWithTypes("postgres", et, cols, vals, pgInfosFromTypeOIDs(typeOIDs), onConflictDoNothing)
}

func buildExportInsertSQLWithPGColumnInfo(et exportTable, cols []string, vals []any, pgInfos []pgExportColumnInfo, onConflictDoNothing bool) (string, error) {
	return buildExportInsertSQLWithTypes("postgres", et, cols, vals, pgInfos, onConflictDoNothing)
}

type pgExportColumnInfo struct {
	TypeOID      uint32
	DefaultValue string
}

type exportColumnInfo struct {
	TypeOID       uint32
	DataType      string
	Nullable      bool
	DefaultValue  string
	HasDefault    bool
	AutoIncrement bool
}

type exportSQLDefault struct{}

func buildExportInsertSQLWithTypes(driver string, et exportTable, cols []string, vals []any, pgInfos []pgExportColumnInfo, onConflictDoNothing bool) (string, error) {
	if len(cols) != len(vals) {
		return "", fmt.Errorf("column/value length mismatch: %d columns, %d values", len(cols), len(vals))
	}
	if pgInfos != nil && len(pgInfos) != len(vals) {
		return "", fmt.Errorf("type/value length mismatch: %d types, %d values", len(pgInfos), len(vals))
	}

	insertVerb := "INSERT INTO"
	if driver == "mysql" && onConflictDoNothing {
		insertVerb = "INSERT IGNORE INTO"
	}

	tableName := exportInsertTableName(driver, et)
	if len(cols) == 0 {
		if driver == "mysql" {
			return fmt.Sprintf("%s %s () VALUES ();", insertVerb, tableName), nil
		}
		stmt := fmt.Sprintf("%s %s DEFAULT VALUES", insertVerb, tableName)
		if driver == "postgres" && onConflictDoNothing {
			stmt += " ON CONFLICT DO NOTHING"
		}
		return stmt + ";", nil
	}

	colList := strings.Join(quoteIdentifiers(cols, driver), ", ")
	literals := make([]string, len(vals))
	for i, v := range vals {
		lit, err := sqlLiteralWithPGColumnInfo(v, driver, pgInfoAt(pgInfos, i))
		if err != nil {
			return "", err
		}
		literals[i] = lit
	}

	stmt := fmt.Sprintf("%s %s (%s) VALUES (%s)",
		insertVerb, tableName, colList, strings.Join(literals, ", "))
	if driver == "postgres" && onConflictDoNothing {
		stmt += " ON CONFLICT DO NOTHING"
	}
	return stmt + ";", nil
}

func pgInfosFromTypeOIDs(typeOIDs []uint32) []pgExportColumnInfo {
	if typeOIDs == nil {
		return nil
	}
	infos := make([]pgExportColumnInfo, len(typeOIDs))
	for i, oid := range typeOIDs {
		infos[i] = pgExportColumnInfo{TypeOID: oid}
	}
	return infos
}

func pgInfoAt(infos []pgExportColumnInfo, i int) pgExportColumnInfo {
	if infos == nil || i < 0 || i >= len(infos) {
		return pgExportColumnInfo{}
	}
	return infos[i]
}

func exportInsertTableName(driver string, et exportTable) string {
	if driver == "postgres" {
		return quotePGIdent(et.Schema) + "." + quotePGIdent(et.Table)
	}
	return "`" + escapeMyIdent(et.Database) + "`.`" + escapeMyIdent(et.Table) + "`"
}

func sqlLiteral(v any, driver string) (string, error) {
	return sqlLiteralWithPGColumnInfo(v, driver, pgExportColumnInfo{})
}

func sqlLiteralWithPGType(v any, driver string, pgTypeOID uint32) (string, error) {
	return sqlLiteralWithPGColumnInfo(v, driver, pgExportColumnInfo{TypeOID: pgTypeOID})
}

func sqlLiteralWithPGColumnInfo(v any, driver string, pgInfo pgExportColumnInfo) (string, error) {
	if v == nil {
		return "NULL", nil
	}
	if _, ok := v.(exportSQLDefault); ok {
		return "DEFAULT", nil
	}
	if driver == "postgres" && isPGJSONOID(pgInfo.TypeOID) {
		return pgJSONLiteral(v, pgInfo)
	}
	if driver == "postgres" && pgInfo.TypeOID == pgtype.UUIDOID {
		return pgUUIDLiteral(v)
	}
	switch x := v.(type) {
	case string:
		return quoteSQLString(x, driver)
	case []byte:
		return quoteSQLString(string(x), driver)
	case []any:
		return sqlArrayLiteral(x, driver)
	case int:
		return strconv.Itoa(x), nil
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case bool:
		if x {
			return "TRUE", nil
		}
		return "FALSE", nil
	case time.Time:
		return quoteSQLString(x.Format(time.RFC3339Nano), driver)
	default:
		rv := reflect.ValueOf(v)
		if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			return sqlArrayLiteralFromReflect(rv, driver)
		}
		return quoteSQLString(fmt.Sprintf("%v", x), driver)
	}
}

func isPGJSONOID(oid uint32) bool {
	return oid == pgtype.JSONOID || oid == pgtype.JSONBOID
}

func pgUUIDLiteral(v any) (string, error) {
	switch x := v.(type) {
	case string:
		lit, err := quoteSQLString(x, "postgres")
		if err != nil {
			return "", err
		}
		return lit + "::uuid", nil
	case []byte:
		if len(x) == 16 {
			return quoteUUIDBytes(x) + "::uuid", nil
		}
		lit, err := quoteSQLString(string(x), "postgres")
		if err != nil {
			return "", err
		}
		return lit + "::uuid", nil
	case [16]byte:
		return quoteUUIDBytes(x[:]) + "::uuid", nil
	case pgtype.UUID:
		if !x.Valid {
			return "NULL", nil
		}
		return quoteUUIDBytes(x.Bytes[:]) + "::uuid", nil
	default:
		rv := reflect.ValueOf(v)
		if rv.IsValid() && rv.Kind() == reflect.Array && rv.Len() == 16 && rv.Type().Elem().Kind() == reflect.Uint8 {
			buf := make([]byte, 16)
			for i := 0; i < 16; i++ {
				buf[i] = byte(rv.Index(i).Uint())
			}
			return quoteUUIDBytes(buf) + "::uuid", nil
		}
		lit, err := quoteSQLString(fmt.Sprintf("%v", x), "postgres")
		if err != nil {
			return "", err
		}
		return lit + "::uuid", nil
	}
}

func quoteUUIDBytes(b []byte) string {
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return "'" + string(buf[:]) + "'"
}

func pgJSONLiteral(v any, pgInfo pgExportColumnInfo) (string, error) {
	var raw string
	switch x := v.(type) {
	case string:
		raw = x
	case []byte:
		raw = string(x)
	default:
		if isEmptyJSONMap(v) {
			raw = jsonDefaultLiteral(pgInfo.DefaultValue)
			if raw != "" {
				break
			}
		}
		b, err := json.Marshal(x)
		if err != nil {
			return "", fmt.Errorf("marshal json literal: %w", err)
		}
		raw = string(b)
	}
	if strings.TrimSpace(raw) == "" {
		raw = jsonDefaultLiteral(pgInfo.DefaultValue)
		if raw == "" {
			raw = "{}"
		}
	}
	lit, err := quoteSQLString(raw, "postgres")
	if err != nil {
		return "", err
	}
	if pgInfo.TypeOID == pgtype.JSONBOID {
		return lit + "::jsonb", nil
	}
	return lit + "::json", nil
}

func isEmptyJSONMap(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.IsValid() && rv.Kind() == reflect.Map && rv.Len() == 0
}

func jsonDefaultLiteral(defaultValue string) string {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(defaultValue), " ", ""))
	switch {
	case strings.Contains(s, "'[]'::json") ||
		strings.Contains(s, "'[]'::jsonb") ||
		strings.Contains(s, "json_build_array(") ||
		strings.Contains(s, "jsonb_build_array("):
		return "[]"
	case strings.Contains(s, "'{}'::json") ||
		strings.Contains(s, "'{}'::jsonb") ||
		strings.Contains(s, "json_build_object(") ||
		strings.Contains(s, "jsonb_build_object("):
		return "{}"
	default:
		return ""
	}
}

func sqlArrayLiteral(values []any, driver string) (string, error) {
	if driver != "postgres" {
		return quoteSQLString(fmt.Sprintf("%v", values), driver)
	}
	if len(values) == 0 {
		return "'{}'", nil
	}
	parts := make([]string, len(values))
	for i, v := range values {
		lit, err := sqlLiteral(v, driver)
		if err != nil {
			return "", err
		}
		parts[i] = lit
	}
	return "ARRAY[" + strings.Join(parts, ", ") + "]", nil
}

func sqlArrayLiteralFromReflect(rv reflect.Value, driver string) (string, error) {
	if driver != "postgres" {
		return quoteSQLString(fmt.Sprintf("%v", rv.Interface()), driver)
	}
	if rv.Len() == 0 {
		return "'{}'", nil
	}
	parts := make([]string, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		lit, err := sqlLiteral(rv.Index(i).Interface(), driver)
		if err != nil {
			return "", err
		}
		parts[i] = lit
	}
	return "ARRAY[" + strings.Join(parts, ", ") + "]", nil
}

func quoteSQLString(s, driver string) (string, error) {
	if driver == "postgres" && strings.ContainsRune(s, 0) {
		return "", errors.New("postgres string literal cannot contain NUL byte")
	}
	if driver == "mysql" {
		replacer := strings.NewReplacer(
			"\\", "\\\\",
			"'", "\\'",
			"\x00", `\0`,
			"\n", `\n`,
			"\r", `\r`,
			"\x1a", `\Z`,
		)
		return "'" + replacer.Replace(s) + "'", nil
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}

// loadConnection 解 connID → *model.Connection（不做权限校验，handler 已校验）。
func loadConnection(connID uint) (*model.Connection, error) {
	var c model.Connection
	if err := db.Meta().First(&c, connID).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// checkCancel 检查 cancel_requested；命中或 ctx 已取消 → 返回 errTaskCanceled。
func checkCancel(ctx context.Context, taskID uint) error {
	if ctx.Err() != nil {
		return errTaskCanceled
	}
	var t model.Task
	if err := db.Meta().Select("cancel_requested").First(&t, taskID).Error; err != nil {
		return nil // 读失败不阻塞 worker
	}
	if t.CancelRequested {
		return errTaskCanceled
	}
	return nil
}

func formatCSVCell(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		// 防止 CSV 注入：如果以公式字符开头，添加单引号前缀
		if len(x) > 0 && (x[0] == '=' || x[0] == '+' || x[0] == '-' || x[0] == '@') {
			return "'" + x
		}
		return x
	case []byte:
		s := string(x)
		// 同样防护 []byte 转换后的字符串
		if len(s) > 0 && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
			return "'" + s
		}
		return s
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func safeFileName(s string) string {
	bad := []string{"/", "\\", "..", string(os.PathSeparator)}
	for _, b := range bad {
		s = strings.ReplaceAll(s, b, "_")
	}
	if s == "" {
		return "out.csv"
	}
	return s
}

// quotePGIdent 用双引号包裹 PG 标识符并转义内部的双引号。
func quotePGIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// escapeMyIdent 仅转义 backtick 字符（外层 backtick 由调用方加）。
func escapeMyIdent(name string) string {
	return strings.ReplaceAll(name, "`", "``")
}

// zipFiles 把多个文件打到一个 zip 中（不保留绝对路径）。
func zipFiles(zipPath string, files []string) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()
	z := zip.NewWriter(out)
	defer z.Close()

	for _, p := range files {
		if err := addFileToZip(z, p); err != nil {
			return err
		}
	}
	return nil
}

func addFileToZip(z *zip.Writer, fpath string) error {
	f, err := os.Open(fpath)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := z.Create(filepath.Base(fpath))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// 防止编译器抱怨 sql 包未使用（dumpMySQLTable 通过 *sql.DB 用到了，
// 但部分编辑器静态分析有时识别不到，留个显式 var 兜底，无运行时副作用）。
var _ = (*sql.DB)(nil)

// runDataSyncTask 执行表数据同步任务。
// 从源表读取所有数据，批量插入到目标表。
// scope=table  时仅同步单张表；
// scope=schema 时遍历源 database+schema 下的所有表，按同名映射到目标 schema，目标表不存在则先建表。
func runDataSyncTask(ctx context.Context, t *model.Task) error {
	srcConn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load source connection: %w", err)
	}
	destConn, err := loadConnection(t.TargetConnID)
	if err != nil {
		return fmt.Errorf("load destination connection: %w", err)
	}

	if err := assertSyncDriversSupported(srcConn, destConn); err != nil {
		return err
	}
	opts, err := parseDataSyncTaskOptions(t.Params)
	if err != nil {
		return err
	}
	if (opts.Where != "" || len(opts.ValueReplacements) > 0) && t.Scope != model.TaskScopeTable {
		return errors.New("data sync filters and value replacements are only supported for scope=table")
	}

	pairs, err := resolveSyncTablePairs(ctx, srcConn, t)
	if err != nil {
		return fmt.Errorf("resolve table pairs: %w", err)
	}
	if len(pairs) == 0 {
		return errors.New("no tables to sync")
	}

	taskDir := filepath.Join(taskArtifactDir, fmt.Sprintf("%d", t.ID))
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return fmt.Errorf("mkdir artifact: %w", err)
	}

	if err := updateTaskFields(t.ID, map[string]any{
		"total_tables": len(pairs),
		"done_tables":  0,
	}); err != nil {
		return err
	}

	outFiles := make([]string, 0, len(pairs))
	var grand syncDataResult
	for i, p := range pairs {
		if err := checkCancel(ctx, t.ID); err != nil {
			return err
		}
		fname := safeFileName(fmt.Sprintf("%s.%s.%s-to-%s.%s.%s.sql",
			p.SrcDatabase, p.SrcSchema, p.SrcTable,
			p.DestDatabase, p.DestSchema, p.DestTable))
		fpath := filepath.Join(taskDir, fname)
		result, err := syncOneTableData(ctx, srcConn, destConn, t, p, opts, fpath)
		if err != nil {
			return fmt.Errorf("sync %s.%s.%s -> %s.%s.%s: %w",
				p.SrcDatabase, p.SrcSchema, p.SrcTable,
				p.DestDatabase, p.DestSchema, p.DestTable, err)
		}
		outFiles = append(outFiles, fpath)
		grand = grand.add(result)
		prog := int(float64(i+1) / float64(len(pairs)) * 100)
		_ = updateTaskFields(t.ID, map[string]any{
			"done_tables":    i + 1,
			"processed_rows": grand.SuccessRows,
			"failed_rows":    grand.FailedRows,
			"total_rows":     grand.SourceRows,
			"progress":       prog,
		})
	}

	finalPath, finalSize, err := finalizeTaskArtifacts(taskDir, fmt.Sprintf("data-sync-%d.zip", t.ID), outFiles)
	if err != nil {
		return err
	}

	return updateTaskFields(t.ID, map[string]any{
		"file_path":      finalPath,
		"file_size":      finalSize,
		"processed_rows": grand.SuccessRows,
		"failed_rows":    grand.FailedRows,
		"total_rows":     grand.SourceRows,
	})
}

func finalizeTaskArtifacts(taskDir, zipName string, outFiles []string) (string, int64, error) {
	if len(outFiles) == 0 {
		return "", 0, nil
	}
	if len(outFiles) == 1 {
		info, err := os.Stat(outFiles[0])
		if err != nil {
			return "", 0, fmt.Errorf("stat artifact: %w", err)
		}
		return outFiles[0], info.Size(), nil
	}

	zipPath := filepath.Join(taskDir, zipName)
	if err := zipFiles(zipPath, outFiles); err != nil {
		return "", 0, fmt.Errorf("zip artifacts: %w", err)
	}
	for _, f := range outFiles {
		_ = os.Remove(f)
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		return "", 0, fmt.Errorf("stat artifact zip: %w", err)
	}
	return zipPath, info.Size(), nil
}

// syncTablePair 描述一次"源表 → 目标表"的对应关系。
type syncTablePair struct {
	SrcDatabase, SrcSchema, SrcTable    string
	DestDatabase, DestSchema, DestTable string
}

// resolveSyncTablePairs 根据 task scope 计算源 → 目标的表映射。
//   - scope=table  ：只产出 1 对，按 task 字段直接映射
//   - scope=schema ：列出源 database+schema 下所有 table（不含 view），按同名映射到目标 schema
func resolveSyncTablePairs(ctx context.Context, srcConn *model.Connection, t *model.Task) ([]syncTablePair, error) {
	switch t.Scope {
	case model.TaskScopeTable:
		return []syncTablePair{{
			SrcDatabase: t.TargetDatabase, SrcSchema: t.TargetSchema, SrcTable: t.TargetTable,
			DestDatabase: t.DestDatabase, DestSchema: t.DestSchema, DestTable: t.DestTable,
		}}, nil
	case model.TaskScopeSchema:
		c := dbexec.WithDatabase(srcConn, t.TargetDatabase)
		tables, err := dbexec.ListTables(ctx, c, t.TargetSchema)
		if err != nil {
			return nil, err
		}
		pairs := make([]syncTablePair, 0, len(tables))
		for _, tb := range tables {
			if tb.Kind != "table" {
				continue
			}
			pairs = append(pairs, syncTablePair{
				SrcDatabase: t.TargetDatabase, SrcSchema: t.TargetSchema, SrcTable: tb.Name,
				DestDatabase: t.DestDatabase, DestSchema: t.DestSchema, DestTable: tb.Name,
			})
		}
		return pairs, nil
	default:
		return nil, fmt.Errorf("unsupported scope for sync task: %s", t.Scope)
	}
}

// assertSyncDriversSupported 校验源/目标驱动是否在白名单内。
func assertSyncDriversSupported(srcConn, destConn *model.Connection) error {
	if srcConn.Driver != "postgres" && srcConn.Driver != "mysql" {
		return fmt.Errorf("source %w: %s", errUnsupportedDriver, srcConn.Driver)
	}
	if destConn.Driver != "postgres" && destConn.Driver != "mysql" {
		return fmt.Errorf("destination %w: %s", errUnsupportedDriver, destConn.Driver)
	}
	return nil
}

// syncOneTableData 同步单张表的数据：
//   - 读取源/目标列；目标表不存在时先按源表结构建表（含主键 + 索引）
//   - 按列名取交集做 INSERT
func syncOneTableData(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, p syncTablePair, opts dataSyncTaskOptions, artifactPath string) (syncDataResult, error) {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	srcCols, err := dbexec.ListColumns(ctx, srcC, p.SrcSchema, p.SrcTable)
	if err != nil {
		return syncDataResult{}, fmt.Errorf("list source columns: %w", err)
	}
	if len(srcCols) == 0 {
		return syncDataResult{}, errors.New("source table has no columns")
	}

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	destCols, err := dbexec.ListColumns(ctx, destC, p.DestSchema, p.DestTable)
	if err != nil {
		// 目标表不存在 → 按源表结构建表，再继续同步数据
		if isMissingTableErr(err) {
			srcIdx, _ := dbexec.ListIndexes(ctx, srcC, p.SrcSchema, p.SrcTable)
			if cerr := createDestTableFromSrc(ctx, destConn, p, srcCols, srcIdx); cerr != nil {
				return syncDataResult{}, fmt.Errorf("auto-create dest table: %w", cerr)
			}
			destCols, err = dbexec.ListColumns(ctx, destC, p.DestSchema, p.DestTable)
			if err != nil {
				return syncDataResult{}, fmt.Errorf("list destination columns after create: %w", err)
			}
		} else {
			return syncDataResult{}, fmt.Errorf("list destination columns: %w", err)
		}
	}
	if len(destCols) == 0 {
		return syncDataResult{}, errors.New("destination table has no columns")
	}

	matched := matchColumnNames(srcCols, destCols)
	if len(matched) == 0 {
		return syncDataResult{}, errors.New("no matching columns between source and destination")
	}
	if err := validateExportValueReplacementColumns(matched, opts.ValueReplacements); err != nil {
		return syncDataResult{}, err
	}
	destInfoByName := exportColumnInfoMapFromDBColumns(destCols)
	destInfos := exportColumnInfosForColumns(matched, destInfoByName)
	artifact, err := newSyncSQLArtifactWriter(destConn.Driver, p, matched, destInfos, artifactPath)
	if err != nil {
		return syncDataResult{}, err
	}
	defer artifact.Close()

	switch srcConn.Driver {
	case "postgres":
		return syncDataFromPG(ctx, srcConn, destConn, t, p, matched, destInfos, opts, artifact)
	case "mysql":
		return syncDataFromMySQL(ctx, srcConn, destConn, t, p, matched, destInfos, opts, artifact)
	default:
		return syncDataResult{}, errUnsupportedDriver
	}
}

func exportColumnInfoMapFromDBColumns(cols []dbexec.ColumnInfo) map[string]exportColumnInfo {
	out := make(map[string]exportColumnInfo, len(cols))
	for _, col := range cols {
		info := exportColumnInfo{
			DataType:      col.DataType,
			Nullable:      col.Nullable,
			AutoIncrement: col.AutoIncrement,
		}
		if col.DefaultValue != nil {
			info.DefaultValue = *col.DefaultValue
			info.HasDefault = strings.TrimSpace(*col.DefaultValue) != ""
		}
		out[col.Name] = info
	}
	return out
}

type syncSQLArtifactWriter struct {
	driver string
	table  exportTable
	cols   []string
	infos  []exportColumnInfo
	file   *os.File
	writer *bufio.Writer
}

func newSyncSQLArtifactWriter(driver string, p syncTablePair, cols []string, infos []exportColumnInfo, outPath string) (*syncSQLArtifactWriter, error) {
	f, err := os.Create(outPath)
	if err != nil {
		return nil, fmt.Errorf("create sync sql artifact: %w", err)
	}
	return &syncSQLArtifactWriter{
		driver: driver,
		table: exportTable{
			Database: p.DestDatabase,
			Schema:   p.DestSchema,
			Table:    p.DestTable,
		},
		cols:   append([]string(nil), cols...),
		infos:  append([]exportColumnInfo(nil), infos...),
		file:   f,
		writer: bufio.NewWriter(f),
	}, nil
}

func (w *syncSQLArtifactWriter) WriteRow(vals []any) error {
	if w == nil {
		return nil
	}
	stmt, err := buildSyncArtifactInsertSQL(w.driver, w.table, w.cols, vals, w.infos)
	if err != nil {
		return err
	}
	_, err = w.writer.WriteString(stmt + "\n")
	return err
}

func (w *syncSQLArtifactWriter) Close() error {
	if w == nil {
		return nil
	}
	flushErr := w.writer.Flush()
	closeErr := w.file.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

func buildSyncArtifactInsertSQL(driver string, et exportTable, cols []string, vals []any, infos []exportColumnInfo) (string, error) {
	if len(cols) != len(vals) {
		return "", fmt.Errorf("column/value length mismatch: %d columns, %d values", len(cols), len(vals))
	}

	insertVerb := "INSERT INTO"
	if driver == "mysql" {
		insertVerb = "INSERT IGNORE INTO"
	}
	colList := strings.Join(quoteIdentifiers(cols, driver), ", ")
	literals := make([]string, len(vals))
	for i, v := range vals {
		lit, err := syncSQLLiteral(v, driver, exportColumnInfoAt(infos, i))
		if err != nil {
			return "", err
		}
		literals[i] = lit
	}

	stmt := fmt.Sprintf("%s %s (%s) VALUES (%s)",
		insertVerb, exportInsertTableName(driver, et), colList, strings.Join(literals, ", "))
	if driver == "postgres" {
		stmt += " ON CONFLICT DO NOTHING"
	}
	return stmt + ";", nil
}

func syncSQLLiteral(v any, driver string, info exportColumnInfo) (string, error) {
	if driver == "postgres" {
		t := normalizeExportDataType(info.DataType)
		switch {
		case info.TypeOID == pgtype.UUIDOID || strings.Contains(t, "uuid"):
			return pgUUIDLiteral(v)
		case isPGJSONOID(info.TypeOID) || strings.Contains(t, "json"):
			return pgJSONLiteral(v, pgExportColumnInfo{
				TypeOID:      inferredPGJSONOID(info),
				DefaultValue: info.DefaultValue,
			})
		}
	}
	return sqlLiteral(v, driver)
}

func inferredPGJSONOID(info exportColumnInfo) uint32 {
	if isPGJSONOID(info.TypeOID) {
		return info.TypeOID
	}
	if strings.Contains(normalizeExportDataType(info.DataType), "jsonb") {
		return pgtype.JSONBOID
	}
	return pgtype.JSONOID
}

// matchColumnNames 取源列与目标列的交集，保留源列顺序。
func matchColumnNames(src, dest []dbexec.ColumnInfo) []string {
	destSet := make(map[string]struct{}, len(dest))
	for _, c := range dest {
		destSet[c.Name] = struct{}{}
	}
	out := make([]string, 0, len(src))
	for _, c := range src {
		if _, ok := destSet[c.Name]; ok {
			out = append(out, c.Name)
		}
	}
	return out
}

// isMissingTableErr 判断错误是否是"目标表/关系不存在"。
// 不同驱动消息不同，做关键字匹配兜底。
func isMissingTableErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "unknown table")
}

// syncDataFromPG 从 PostgreSQL 源表同步数据到目标表。
func syncDataFromPG(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, p syncTablePair, cols []string, destInfos []exportColumnInfo, opts dataSyncTaskOptions, artifact *syncSQLArtifactWriter) (syncDataResult, error) {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	pool, err := dbexec.AcquirePGPool(ctx, srcC)
	if err != nil {
		return syncDataResult{}, fmt.Errorf("acquire source pool: %w", err)
	}

	colList := strings.Join(quoteIdentifiers(cols, srcConn.Driver), ", ")
	q := fmt.Sprintf("SELECT %s FROM %s.%s", colList, quotePGIdent(p.SrcSchema), quotePGIdent(p.SrcTable))
	if opts.Where != "" {
		q += " WHERE " + opts.Where
	}
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return syncDataResult{}, fmt.Errorf("query source: %w", err)
	}
	defer rows.Close()

	var result syncDataResult
	batch := make([][]any, 0, batchSize)

	for rows.Next() {
		if result.SourceRows%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return result, err
			}
		}

		vals, err := rows.Values()
		if err != nil {
			return result, fmt.Errorf("scan row: %w", err)
		}
		vals = applyExportValueReplacements(cols, vals, destInfos, opts.ValueReplacements)
		if err := artifact.WriteRow(vals); err != nil {
			return result, fmt.Errorf("write sync sql artifact: %w", err)
		}
		batch = append(batch, vals)
		result.SourceRows++

		if len(batch) >= batchSize {
			affected, err := insertBatch(ctx, destConn, p, cols, batch)
			if err != nil {
				return result, fmt.Errorf("insert batch: %w", err)
			}
			result.SuccessRows += affected
			result.FailedRows += int64(len(batch)) - affected
			batch = batch[:0]

			_ = updateTaskFields(t.ID, map[string]any{
				"processed_rows": result.SuccessRows,
				"failed_rows":    result.FailedRows,
				"total_rows":     result.SourceRows,
			})
		}
	}

	// 插入剩余数据
	if len(batch) > 0 {
		affected, err := insertBatch(ctx, destConn, p, cols, batch)
		if err != nil {
			return result, fmt.Errorf("insert final batch: %w", err)
		}
		result.SuccessRows += affected
		result.FailedRows += int64(len(batch)) - affected
	}

	return result, rows.Err()
}

// syncDataFromMySQL 从 MySQL 源表同步数据到目标表。
func syncDataFromMySQL(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, p syncTablePair, cols []string, destInfos []exportColumnInfo, opts dataSyncTaskOptions, artifact *syncSQLArtifactWriter) (syncDataResult, error) {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	pool, err := dbexec.AcquireMySQLPool(ctx, srcC)
	if err != nil {
		return syncDataResult{}, fmt.Errorf("acquire source pool: %w", err)
	}

	colList := strings.Join(quoteIdentifiers(cols, srcConn.Driver), ", ")
	q := fmt.Sprintf("SELECT %s FROM `%s`.`%s`", colList, escapeMyIdent(p.SrcDatabase), escapeMyIdent(p.SrcTable))
	if opts.Where != "" {
		q += " WHERE " + opts.Where
	}
	rows, err := pool.QueryContext(ctx, q)
	if err != nil {
		return syncDataResult{}, fmt.Errorf("query source: %w", err)
	}
	defer rows.Close()

	holders := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range holders {
		ptrs[i] = &holders[i]
	}

	var result syncDataResult
	batch := make([][]any, 0, batchSize)

	for rows.Next() {
		if result.SourceRows%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return result, err
			}
		}

		if err := rows.Scan(ptrs...); err != nil {
			return result, fmt.Errorf("scan row: %w", err)
		}

		vals := make([]any, len(holders))
		copy(vals, holders)
		vals = applyExportValueReplacements(cols, vals, destInfos, opts.ValueReplacements)
		if err := artifact.WriteRow(vals); err != nil {
			return result, fmt.Errorf("write sync sql artifact: %w", err)
		}
		batch = append(batch, vals)
		result.SourceRows++

		if len(batch) >= batchSize {
			affected, err := insertBatch(ctx, destConn, p, cols, batch)
			if err != nil {
				return result, fmt.Errorf("insert batch: %w", err)
			}
			result.SuccessRows += affected
			result.FailedRows += int64(len(batch)) - affected
			batch = batch[:0]

			_ = updateTaskFields(t.ID, map[string]any{
				"processed_rows": result.SuccessRows,
				"failed_rows":    result.FailedRows,
				"total_rows":     result.SourceRows,
			})
		}
	}

	if len(batch) > 0 {
		affected, err := insertBatch(ctx, destConn, p, cols, batch)
		if err != nil {
			return result, fmt.Errorf("insert final batch: %w", err)
		}
		result.SuccessRows += affected
		result.FailedRows += int64(len(batch)) - affected
	}

	return result, rows.Err()
}

// insertBatch 批量插入数据到目标表，返回目标库实际写入的行数。
func insertBatch(ctx context.Context, destConn *model.Connection, p syncTablePair, cols []string, batch [][]any) (int64, error) {
	if len(batch) == 0 {
		return 0, nil
	}

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	colList := strings.Join(quoteIdentifiers(cols, destConn.Driver), ", ")

	switch destConn.Driver {
	case "postgres":
		return insertBatchPG(ctx, destC, p, colList, batch, len(cols))
	case "mysql":
		return insertBatchMySQL(ctx, destC, p, colList, batch, len(cols))
	default:
		return 0, errUnsupportedDriver
	}
}

// insertBatchPG 批量插入到 PostgreSQL。
func insertBatchPG(ctx context.Context, destC *model.Connection, p syncTablePair, colList string, batch [][]any, colCount int) (int64, error) {
	pool, err := dbexec.AcquirePGPool(ctx, destC)
	if err != nil {
		return 0, err
	}

	q, args := buildPGBatchInsert(p, colList, batch, colCount)
	tag, err := pool.Exec(ctx, q, args...)
	if err == nil {
		return tag.RowsAffected(), nil
	}
	return insertRowsPGIndividually(ctx, pool, p, colList, batch, colCount)
}

func buildPGBatchInsert(p syncTablePair, colList string, batch [][]any, colCount int) (string, []any) {
	placeholders := make([]string, len(batch))
	args := make([]any, 0, len(batch)*colCount)
	for i, row := range batch {
		rowPlaceholders := make([]string, colCount)
		for j := 0; j < colCount; j++ {
			if _, ok := row[j].(exportSQLDefault); ok {
				rowPlaceholders[j] = "DEFAULT"
				continue
			}
			args = append(args, row[j])
			rowPlaceholders[j] = fmt.Sprintf("$%d", len(args))
		}
		placeholders[i] = "(" + strings.Join(rowPlaceholders, ", ") + ")"
	}

	// 使用 ON CONFLICT DO NOTHING 跳过主键/唯一约束冲突
	q := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES %s ON CONFLICT DO NOTHING",
		quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable), colList, strings.Join(placeholders, ", "))
	return q, args
}

func insertRowsPGIndividually(ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, p syncTablePair, colList string, batch [][]any, colCount int) (int64, error) {
	var affected int64
	for _, row := range batch {
		q, args := buildPGBatchInsert(p, colList, [][]any{row}, colCount)
		tag, err := pool.Exec(ctx, q, args...)
		if err != nil {
			continue
		}
		affected += tag.RowsAffected()
	}
	return affected, nil
}

// insertBatchMySQL 批量插入到 MySQL。
func insertBatchMySQL(ctx context.Context, destC *model.Connection, p syncTablePair, colList string, batch [][]any, colCount int) (int64, error) {
	pool, err := dbexec.AcquireMySQLPool(ctx, destC)
	if err != nil {
		return 0, err
	}

	q, args := buildMySQLBatchInsert(p, colList, batch, colCount)
	if res, err := pool.ExecContext(ctx, q, args...); err == nil {
		return res.RowsAffected()
	}

	var affected int64
	for _, row := range batch {
		singleQ, singleArgs := buildMySQLSingleInsert(p, colList, row, colCount)
		res, err := pool.ExecContext(ctx, singleQ, singleArgs...)
		if err != nil {
			if isMySQLDuplicateKeyErr(err) {
				continue
			}
			continue
		}
		n, err := res.RowsAffected()
		if err != nil {
			return affected, err
		}
		affected += n
	}
	return affected, nil
}

func buildMySQLBatchInsert(p syncTablePair, colList string, batch [][]any, colCount int) (string, []any) {
	placeholders := make([]string, len(batch))
	args := make([]any, 0, len(batch)*colCount)
	for i, row := range batch {
		rowPlaceholders := make([]string, colCount)
		for j := 0; j < colCount; j++ {
			if _, ok := row[j].(exportSQLDefault); ok {
				rowPlaceholders[j] = "DEFAULT"
				continue
			}
			rowPlaceholders[j] = "?"
			args = append(args, row[j])
		}
		placeholders[i] = "(" + strings.Join(rowPlaceholders, ", ") + ")"
	}

	q := fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES %s",
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable), colList, strings.Join(placeholders, ", "))
	return q, args
}

func buildMySQLSingleInsert(p syncTablePair, colList string, row []any, colCount int) (string, []any) {
	rowPlaceholders := make([]string, colCount)
	args := make([]any, 0, colCount)
	for i := 0; i < colCount; i++ {
		if _, ok := row[i].(exportSQLDefault); ok {
			rowPlaceholders[i] = "DEFAULT"
			continue
		}
		rowPlaceholders[i] = "?"
		args = append(args, row[i])
	}
	return fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES (%s)",
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable), colList, strings.Join(rowPlaceholders, ", ")), args
}

func isMySQLDuplicateKeyErr(err error) bool {
	var myErr *mysqldriver.MySQLError
	return errors.As(err, &myErr) && myErr.Number == 1062
}

// quoteIdentifiers 根据驱动类型引用标识符列表。
func quoteIdentifiers(names []string, driver string) []string {
	quoted := make([]string, len(names))
	for i, name := range names {
		if driver == "postgres" {
			quoted[i] = quotePGIdent(name)
		} else {
			quoted[i] = "`" + escapeMyIdent(name) + "`"
		}
	}
	return quoted
}

// runSchemaSyncTask 执行表结构同步任务。
// scope=table  时仅对单表对比 + 同步；
// scope=schema 时遍历源 database+schema 下的所有表，按同名映射到目标 schema。
func runSchemaSyncTask(ctx context.Context, t *model.Task) error {
	srcConn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load source connection: %w", err)
	}
	destConn, err := loadConnection(t.TargetConnID)
	if err != nil {
		return fmt.Errorf("load destination connection: %w", err)
	}

	if err := assertSyncDriversSupported(srcConn, destConn); err != nil {
		return err
	}

	pairs, err := resolveSyncTablePairs(ctx, srcConn, t)
	if err != nil {
		return fmt.Errorf("resolve table pairs: %w", err)
	}
	if len(pairs) == 0 {
		return errors.New("no tables to sync")
	}

	if err := updateTaskFields(t.ID, map[string]any{
		"total_tables": len(pairs),
		"done_tables":  0,
	}); err != nil {
		return err
	}

	for i, p := range pairs {
		if err := checkCancel(ctx, t.ID); err != nil {
			return err
		}
		if err := syncOneTableSchema(ctx, srcConn, destConn, p); err != nil {
			return fmt.Errorf("schema sync %s.%s.%s -> %s.%s.%s: %w",
				p.SrcDatabase, p.SrcSchema, p.SrcTable,
				p.DestDatabase, p.DestSchema, p.DestTable, err)
		}
		prog := int(float64(i+1) / float64(len(pairs)) * 100)
		_ = updateTaskFields(t.ID, map[string]any{
			"done_tables": i + 1,
			"progress":    prog,
		})
	}

	return updateTaskFields(t.ID, map[string]any{"progress": 100})
}

// syncOneTableSchema 对比并同步单张表的结构差异。目标表不存在时直接按源表建表。
func syncOneTableSchema(ctx context.Context, srcConn, destConn *model.Connection, p syncTablePair) error {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	srcCols, err := dbexec.ListColumns(ctx, srcC, p.SrcSchema, p.SrcTable)
	if err != nil {
		return fmt.Errorf("list source columns: %w", err)
	}
	srcIndexes, err := dbexec.ListIndexes(ctx, srcC, p.SrcSchema, p.SrcTable)
	if err != nil {
		return fmt.Errorf("list source indexes: %w", err)
	}

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	destCols, err := dbexec.ListColumns(ctx, destC, p.DestSchema, p.DestTable)
	if err != nil {
		if isMissingTableErr(err) {
			return createDestTableFromSrc(ctx, destConn, p, srcCols, srcIndexes)
		}
		return fmt.Errorf("list destination columns: %w", err)
	}
	// Metadata queries for PostgreSQL/MySQL return an empty result rather than
	// an error when the relation does not exist. Treat that case as a missing
	// destination table; otherwise diffAndApplySchema would emit ALTER TABLE
	// against a relation that cannot exist.
	if len(destCols) == 0 {
		return createDestTableFromSrc(ctx, destConn, p, srcCols, srcIndexes)
	}
	destIndexes, err := dbexec.ListIndexes(ctx, destC, p.DestSchema, p.DestTable)
	if err != nil {
		return fmt.Errorf("list destination indexes: %w", err)
	}

	return diffAndApplySchema(ctx, destConn, p, srcCols, srcIndexes, destCols, destIndexes)
}

// createDestTableFromSrc 按源表结构在目标库创建同名表（含主键 + 普通索引）。
func createDestTableFromSrc(ctx context.Context, destConn *model.Connection, p syncTablePair,
	cols []dbexec.ColumnInfo, indexes []dbexec.IndexInfo) error {
	var ddl string
	switch destConn.Driver {
	case "postgres":
		ddl = generateCreateTablePG(p, cols)
	case "mysql":
		ddl = generateCreateTableMySQL(p, cols)
	default:
		return errUnsupportedDriver
	}

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	if _, err := dbexec.Exec(ctx, destC, ddl); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	// 建表后追加非主键索引（建表语句里没带）
	for _, idx := range indexes {
		if idx.Primary {
			continue
		}
		idxDDL := generateCreateIndex(destConn.Driver, p, idx)
		if _, err := dbexec.Exec(ctx, destC, idxDDL); err != nil {
			// 索引失败不阻塞建表流程；记到错误链尾。
			return fmt.Errorf("create index %s: %w", idx.Name, err)
		}
	}
	return nil
}

// diffAndApplySchema 把源表与目标表结构差异翻译成 DDL 并执行。
func diffAndApplySchema(ctx context.Context, destConn *model.Connection, p syncTablePair,
	srcCols []dbexec.ColumnInfo, srcIndexes []dbexec.IndexInfo,
	destCols []dbexec.ColumnInfo, destIndexes []dbexec.IndexInfo) error {

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	var ddls []string

	destColMap := make(map[string]dbexec.ColumnInfo, len(destCols))
	for _, dc := range destCols {
		destColMap[dc.Name] = dc
	}
	for _, sc := range srcCols {
		if dc, exists := destColMap[sc.Name]; !exists {
			ddls = append(ddls, generateAddColumn(destConn.Driver, p, sc))
		} else if !columnsMatch(sc, dc) {
			ddls = append(ddls, generateModifyColumn(destConn.Driver, p, sc))
		}
	}

	destIdxMap := make(map[string]dbexec.IndexInfo, len(destIndexes))
	for _, di := range destIndexes {
		destIdxMap[di.Name] = di
	}
	for _, si := range srcIndexes {
		if si.Primary {
			continue // 主键通过列定义处理
		}
		if _, exists := destIdxMap[si.Name]; !exists {
			ddls = append(ddls, generateCreateIndex(destConn.Driver, p, si))
		}
	}

	for i, ddl := range ddls {
		if _, err := dbexec.Exec(ctx, destC, ddl); err != nil {
			return fmt.Errorf("execute DDL [%d/%d]: %w\nSQL: %s", i+1, len(ddls), err, ddl)
		}
	}
	return nil
}

// columnsMatch 检查两列是否匹配。
func columnsMatch(src, dest dbexec.ColumnInfo) bool {
	return normalizeType(src.DataType) == normalizeType(dest.DataType) &&
		src.Nullable == dest.Nullable
}

// normalizeType 标准化数据类型（简化比较）。
func normalizeType(t string) string {
	t = strings.ToLower(t)
	// 移除长度和精度
	if idx := strings.Index(t, "("); idx > 0 {
		t = t[:idx]
	}
	return strings.TrimSpace(t)
}

// generateCreateTablePG 生成 PostgreSQL 建表语句。
func generateCreateTablePG(p syncTablePair, cols []dbexec.ColumnInfo) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CREATE TABLE %s.%s (\n", quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable)))

	for i, col := range cols {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  ")
		b.WriteString(quotePGIdent(col.Name))
		b.WriteString(" ")
		b.WriteString(col.DataType)
		if col.AutoIncrement {
			b.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
		}
		if !col.Nullable {
			b.WriteString(" NOT NULL")
		}
		if !col.AutoIncrement && col.DefaultValue != nil && *col.DefaultValue != "" {
			b.WriteString(" DEFAULT ")
			b.WriteString(*col.DefaultValue)
		}
	}

	// 添加主键
	var pkCols []string
	for _, col := range cols {
		if col.IsPrimary {
			pkCols = append(pkCols, quotePGIdent(col.Name))
		}
	}
	if len(pkCols) > 0 {
		b.WriteString(",\n  PRIMARY KEY (")
		b.WriteString(strings.Join(pkCols, ", "))
		b.WriteString(")")
	}

	b.WriteString("\n)")
	return b.String()
}

// generateCreateTableMySQL 生成 MySQL 建表语句。
func generateCreateTableMySQL(p syncTablePair, cols []dbexec.ColumnInfo) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CREATE TABLE `%s`.`%s` (\n", escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable)))

	for i, col := range cols {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  `")
		b.WriteString(escapeMyIdent(col.Name))
		b.WriteString("` ")
		b.WriteString(col.DataType)
		if !col.Nullable {
			b.WriteString(" NOT NULL")
		}
		if col.AutoIncrement {
			b.WriteString(" AUTO_INCREMENT")
		}
		if col.DefaultValue != nil && *col.DefaultValue != "" {
			b.WriteString(" DEFAULT ")
			b.WriteString(*col.DefaultValue)
		}
		if col.Comment != nil && *col.Comment != "" {
			b.WriteString(" COMMENT '")
			b.WriteString(strings.ReplaceAll(*col.Comment, "'", "''"))
			b.WriteString("'")
		}
	}

	// 添加主键
	var pkCols []string
	for _, col := range cols {
		if col.IsPrimary {
			pkCols = append(pkCols, "`"+escapeMyIdent(col.Name)+"`")
		}
	}
	if len(pkCols) > 0 {
		b.WriteString(",\n  PRIMARY KEY (")
		b.WriteString(strings.Join(pkCols, ", "))
		b.WriteString(")")
	}

	b.WriteString("\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	return b.String()
}

// generateAddColumn 生成添加列的 DDL。
func generateAddColumn(driver string, p syncTablePair, col dbexec.ColumnInfo) string {
	if driver == "postgres" {
		identity := ternary(col.AutoIncrement, " GENERATED BY DEFAULT AS IDENTITY", "")
		defaultValue := ""
		if !col.AutoIncrement && col.DefaultValue != nil && *col.DefaultValue != "" {
			defaultValue = " DEFAULT " + *col.DefaultValue
		}
		return fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s%s%s%s",
			quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable),
			quotePGIdent(col.Name), col.DataType,
			identity, defaultValue, ternary(!col.Nullable, " NOT NULL", ""))
	}
	return fmt.Sprintf("ALTER TABLE `%s`.`%s` ADD COLUMN `%s` %s%s",
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable),
		escapeMyIdent(col.Name), col.DataType,
		ternary(!col.Nullable, " NOT NULL", ""))
}

// generateModifyColumn 生成修改列的 DDL。
func generateModifyColumn(driver string, p syncTablePair, col dbexec.ColumnInfo) string {
	if driver == "postgres" {
		return fmt.Sprintf("ALTER TABLE %s.%s ALTER COLUMN %s TYPE %s",
			quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable),
			quotePGIdent(col.Name), col.DataType)
	}
	return fmt.Sprintf("ALTER TABLE `%s`.`%s` MODIFY COLUMN `%s` %s%s",
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable),
		escapeMyIdent(col.Name), col.DataType,
		ternary(!col.Nullable, " NOT NULL", ""))
}

// generateCreateIndex 生成创建索引的 DDL。
func generateCreateIndex(driver string, p syncTablePair, idx dbexec.IndexInfo) string {
	uniqueStr := ternary(idx.Unique, "UNIQUE ", "")
	if driver == "postgres" {
		cols := make([]string, len(idx.Columns))
		for i, c := range idx.Columns {
			cols[i] = quotePGIdent(c)
		}
		return fmt.Sprintf("CREATE %sINDEX %s ON %s.%s (%s)",
			uniqueStr, quotePGIdent(idx.Name),
			quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable),
			strings.Join(cols, ", "))
	}
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = "`" + escapeMyIdent(c) + "`"
	}
	return fmt.Sprintf("CREATE %sINDEX `%s` ON `%s`.`%s` (%s)",
		uniqueStr, escapeMyIdent(idx.Name),
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable),
		strings.Join(cols, ", "))
}

// ternary 三元运算符辅助函数。
func ternary(cond bool, trueVal, falseVal string) string {
	if cond {
		return trueVal
	}
	return falseVal
}
