package service

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/dbexec"
	"github.com/chy/chat2db/server/internal/model"
)

var (
	errTaskCanceled         = errors.New("task canceled")
	errImportNotImplemented = errors.New("import task is not implemented yet")
	errUnknownKind          = errors.New("unknown task kind")
	errUnsupportedDriver    = errors.New("driver not supported for this operation")
)

const batchSize = 1000 // 数据同步批量大小

// runExportTask 是导出任务的统一入口：先列出"要导出的表清单"，
// 再循环逐表流式 dump 到 CSV；多张表时打 zip，单张表保留单 CSV。
func runExportTask(ctx context.Context, t *model.Task) error {
	conn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load connection: %w", err)
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

	csvFiles := make([]string, 0, len(tables))
	var totalRows int64
	for i, et := range tables {
		if err := checkCancel(ctx, t.ID); err != nil {
			return err
		}

		fname := safeFileName(fmt.Sprintf("%s.%s.%s.csv", et.Database, et.Schema, et.Table))
		fpath := filepath.Join(taskDir, fname)
		rows, err := dumpTableCSV(ctx, conn, et, fpath, t)
		if err != nil {
			return fmt.Errorf("dump %s.%s.%s: %w", et.Database, et.Schema, et.Table, err)
		}
		csvFiles = append(csvFiles, fpath)
		totalRows += rows

		// 进度按"已完成表数 / 总表数" 计算（粒度更稳）。
		prog := int(float64(i+1) / float64(len(tables)) * 100)
		_ = updateTaskFields(t.ID, map[string]any{
			"done_tables":    i + 1,
			"processed_rows": totalRows,
			"progress":       prog,
		})
	}

	// 多表时打 zip。单表保留单 CSV 即可。
	var finalPath string
	var finalSize int64
	if len(csvFiles) == 1 {
		finalPath = csvFiles[0]
		if info, err := os.Stat(finalPath); err == nil {
			finalSize = info.Size()
		}
	} else {
		zipPath := filepath.Join(taskDir, fmt.Sprintf("export-%d.zip", t.ID))
		if err := zipFiles(zipPath, csvFiles); err != nil {
			return fmt.Errorf("zip: %w", err)
		}
		// 打包后删除中间 CSV 以省盘。
		for _, f := range csvFiles {
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
func dumpTableCSV(ctx context.Context, conn *model.Connection, et exportTable, outPath string, t *model.Task) (int64, error) {
	f, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	switch conn.Driver {
	case "postgres":
		return dumpPGTable(ctx, conn, et, w, t)
	case "mysql":
		return dumpMySQLTable(ctx, conn, et, w, t)
	default:
		return 0, fmt.Errorf("driver %s not supported", conn.Driver)
	}
}

func dumpPGTable(ctx context.Context, conn *model.Connection, et exportTable, w *csv.Writer, t *model.Task) (int64, error) {
	c := dbexec.WithDatabase(conn, et.Database)
	pool, err := dbexec.AcquirePGPool(ctx, c)
	if err != nil {
		return 0, err
	}
	q := fmt.Sprintf(`SELECT * FROM %s.%s`, quotePGIdent(et.Schema), quotePGIdent(et.Table))
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

func dumpMySQLTable(ctx context.Context, conn *model.Connection, et exportTable, w *csv.Writer, t *model.Task) (int64, error) {
	c := dbexec.WithDatabase(conn, et.Database)
	pool, err := dbexec.AcquireMySQLPool(ctx, c)
	if err != nil {
		return 0, err
	}
	// MySQL 中 database == schema；用 backtick 限定。
	q := fmt.Sprintf("SELECT * FROM `%s`.`%s`", escapeMyIdent(et.Database), escapeMyIdent(et.Table))
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
func runDataSyncTask(ctx context.Context, t *model.Task) error {
	srcConn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load source connection: %w", err)
	}
	destConn, err := loadConnection(t.TargetConnID)
	if err != nil {
		return fmt.Errorf("load destination connection: %w", err)
	}

	// 验证驱动支持
	if srcConn.Driver != "postgres" && srcConn.Driver != "mysql" {
		return fmt.Errorf("source %w: %s", errUnsupportedDriver, srcConn.Driver)
	}
	if destConn.Driver != "postgres" && destConn.Driver != "mysql" {
		return fmt.Errorf("destination %w: %s", errUnsupportedDriver, destConn.Driver)
	}

	// 读取源表结构
	srcC := dbexec.WithDatabase(srcConn, t.TargetDatabase)
	srcCols, err := dbexec.ListColumns(ctx, srcC, t.TargetSchema, t.TargetTable)
	if err != nil {
		return fmt.Errorf("list source columns: %w", err)
	}
	if len(srcCols) == 0 {
		return errors.New("source table has no columns")
	}

	// 读取目标表结构
	destC := dbexec.WithDatabase(destConn, t.DestDatabase)
	destCols, err := dbexec.ListColumns(ctx, destC, t.DestSchema, t.DestTable)
	if err != nil {
		return fmt.Errorf("list destination columns: %w", err)
	}
	if len(destCols) == 0 {
		return errors.New("destination table has no columns")
	}

	// 匹配列名（按名称匹配）
	colMap := make(map[string]bool)
	for _, dc := range destCols {
		colMap[dc.Name] = true
	}
	var matchedCols []string
	for _, sc := range srcCols {
		if colMap[sc.Name] {
			matchedCols = append(matchedCols, sc.Name)
		}
	}
	if len(matchedCols) == 0 {
		return errors.New("no matching columns between source and destination")
	}

	// 开始同步数据
	var totalRows int64
	switch srcConn.Driver {
	case "postgres":
		totalRows, err = syncDataFromPG(ctx, srcConn, destConn, t, matchedCols)
	case "mysql":
		totalRows, err = syncDataFromMySQL(ctx, srcConn, destConn, t, matchedCols)
	default:
		return errUnsupportedDriver
	}

	if err != nil {
		return err
	}

	return updateTaskFields(t.ID, map[string]any{
		"processed_rows": totalRows,
		"total_rows":     totalRows,
	})
}

// syncDataFromPG 从 PostgreSQL 源表同步数据到目标表。
func syncDataFromPG(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, cols []string) (int64, error) {
	srcC := dbexec.WithDatabase(srcConn, t.TargetDatabase)
	pool, err := dbexec.AcquirePGPool(ctx, srcC)
	if err != nil {
		return 0, fmt.Errorf("acquire source pool: %w", err)
	}

	colList := strings.Join(quoteIdentifiers(cols, srcConn.Driver), ", ")
	q := fmt.Sprintf("SELECT %s FROM %s.%s", colList, quotePGIdent(t.TargetSchema), quotePGIdent(t.TargetTable))
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("query source: %w", err)
	}
	defer rows.Close()

	var totalRows int64
	batch := make([][]any, 0, batchSize)

	for rows.Next() {
		if totalRows%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return totalRows, err
			}
		}

		vals, err := rows.Values()
		if err != nil {
			return totalRows, fmt.Errorf("scan row: %w", err)
		}
		batch = append(batch, vals)

		if len(batch) >= batchSize {
			if err := insertBatch(ctx, destConn, t, cols, batch); err != nil {
				return totalRows, fmt.Errorf("insert batch: %w", err)
			}
			totalRows += int64(len(batch))
			batch = batch[:0]

			prog := int(float64(totalRows) / float64(totalRows+1) * 50) // 估算进度
			_ = updateTaskFields(t.ID, map[string]any{
				"processed_rows": totalRows,
				"progress":       prog,
			})
		}
	}

	// 插入剩余数据
	if len(batch) > 0 {
		if err := insertBatch(ctx, destConn, t, cols, batch); err != nil {
			return totalRows, fmt.Errorf("insert final batch: %w", err)
		}
		totalRows += int64(len(batch))
	}

	return totalRows, rows.Err()
}

// syncDataFromMySQL 从 MySQL 源表同步数据到目标表。
func syncDataFromMySQL(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, cols []string) (int64, error) {
	srcC := dbexec.WithDatabase(srcConn, t.TargetDatabase)
	pool, err := dbexec.AcquireMySQLPool(ctx, srcC)
	if err != nil {
		return 0, fmt.Errorf("acquire source pool: %w", err)
	}

	colList := strings.Join(quoteIdentifiers(cols, srcConn.Driver), ", ")
	q := fmt.Sprintf("SELECT %s FROM `%s`.`%s`", colList, escapeMyIdent(t.TargetDatabase), escapeMyIdent(t.TargetTable))
	rows, err := pool.QueryContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("query source: %w", err)
	}
	defer rows.Close()

	holders := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range holders {
		ptrs[i] = &holders[i]
	}

	var totalRows int64
	batch := make([][]any, 0, batchSize)

	for rows.Next() {
		if totalRows%500 == 0 {
			if err := checkCancel(ctx, t.ID); err != nil {
				return totalRows, err
			}
		}

		if err := rows.Scan(ptrs...); err != nil {
			return totalRows, fmt.Errorf("scan row: %w", err)
		}

		vals := make([]any, len(holders))
		copy(vals, holders)
		batch = append(batch, vals)

		if len(batch) >= batchSize {
			if err := insertBatch(ctx, destConn, t, cols, batch); err != nil {
				return totalRows, fmt.Errorf("insert batch: %w", err)
			}
			totalRows += int64(len(batch))
			batch = batch[:0]

			prog := int(float64(totalRows) / float64(totalRows+1) * 50)
			_ = updateTaskFields(t.ID, map[string]any{
				"processed_rows": totalRows,
				"progress":       prog,
			})
		}
	}

	if len(batch) > 0 {
		if err := insertBatch(ctx, destConn, t, cols, batch); err != nil {
			return totalRows, fmt.Errorf("insert final batch: %w", err)
		}
		totalRows += int64(len(batch))
	}

	return totalRows, rows.Err()
}

// insertBatch 批量插入数据到目标表。
func insertBatch(ctx context.Context, destConn *model.Connection, t *model.Task, cols []string, batch [][]any) error {
	if len(batch) == 0 {
		return nil
	}

	destC := dbexec.WithDatabase(destConn, t.DestDatabase)
	colList := strings.Join(quoteIdentifiers(cols, destConn.Driver), ", ")

	switch destConn.Driver {
	case "postgres":
		return insertBatchPG(ctx, destC, t, colList, batch, len(cols))
	case "mysql":
		return insertBatchMySQL(ctx, destC, t, colList, batch, len(cols))
	default:
		return errUnsupportedDriver
	}
}

// insertBatchPG 批量插入到 PostgreSQL。
func insertBatchPG(ctx context.Context, destC *model.Connection, t *model.Task, colList string, batch [][]any, colCount int) error {
	pool, err := dbexec.AcquirePGPool(ctx, destC)
	if err != nil {
		return err
	}

	placeholders := make([]string, len(batch))
	args := make([]any, 0, len(batch)*colCount)
	for i, row := range batch {
		rowPlaceholders := make([]string, colCount)
		for j := 0; j < colCount; j++ {
			rowPlaceholders[j] = fmt.Sprintf("$%d", i*colCount+j+1)
		}
		placeholders[i] = "(" + strings.Join(rowPlaceholders, ", ") + ")"
		args = append(args, row...)
	}

	q := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES %s",
		quotePGIdent(t.DestSchema), quotePGIdent(t.DestTable), colList, strings.Join(placeholders, ", "))
	_, err = pool.Exec(ctx, q, args...)
	return err
}

// insertBatchMySQL 批量插入到 MySQL。
func insertBatchMySQL(ctx context.Context, destC *model.Connection, t *model.Task, colList string, batch [][]any, colCount int) error {
	pool, err := dbexec.AcquireMySQLPool(ctx, destC)
	if err != nil {
		return err
	}

	placeholders := make([]string, len(batch))
	args := make([]any, 0, len(batch)*colCount)
	for i, row := range batch {
		rowPlaceholders := make([]string, colCount)
		for j := 0; j < colCount; j++ {
			rowPlaceholders[j] = "?"
		}
		placeholders[i] = "(" + strings.Join(rowPlaceholders, ", ") + ")"
		args = append(args, row...)
	}

	q := fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES %s",
		escapeMyIdent(t.DestDatabase), escapeMyIdent(t.DestTable), colList, strings.Join(placeholders, ", "))
	_, err = pool.ExecContext(ctx, q, args...)
	return err
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
// 对比源表和目标表的结构差异，生成并执行 DDL 语句。
func runSchemaSyncTask(ctx context.Context, t *model.Task) error {
	srcConn, err := loadConnection(t.ConnID)
	if err != nil {
		return fmt.Errorf("load source connection: %w", err)
	}
	destConn, err := loadConnection(t.TargetConnID)
	if err != nil {
		return fmt.Errorf("load destination connection: %w", err)
	}

	// 验证驱动支持
	if srcConn.Driver != "postgres" && srcConn.Driver != "mysql" {
		return fmt.Errorf("source %w: %s", errUnsupportedDriver, srcConn.Driver)
	}
	if destConn.Driver != "postgres" && destConn.Driver != "mysql" {
		return fmt.Errorf("destination %w: %s", errUnsupportedDriver, destConn.Driver)
	}

	// 读取源表结构
	srcC := dbexec.WithDatabase(srcConn, t.TargetDatabase)
	srcCols, err := dbexec.ListColumns(ctx, srcC, t.TargetSchema, t.TargetTable)
	if err != nil {
		return fmt.Errorf("list source columns: %w", err)
	}
	srcIndexes, err := dbexec.ListIndexes(ctx, srcC, t.TargetSchema, t.TargetTable)
	if err != nil {
		return fmt.Errorf("list source indexes: %w", err)
	}

	// 读取目标表结构
	destC := dbexec.WithDatabase(destConn, t.DestDatabase)
	destCols, err := dbexec.ListColumns(ctx, destC, t.DestSchema, t.DestTable)
	if err != nil {
		// 目标表不存在，创建整个表
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "doesn't exist") {
			return createTable(ctx, destConn, t, srcCols, srcIndexes)
		}
		return fmt.Errorf("list destination columns: %w", err)
	}
	destIndexes, err := dbexec.ListIndexes(ctx, destC, t.DestSchema, t.DestTable)
	if err != nil {
		return fmt.Errorf("list destination indexes: %w", err)
	}

	// 对比并同步结构
	return syncSchema(ctx, destConn, t, srcCols, srcIndexes, destCols, destIndexes)
}

// createTable 创建目标表（当目标表不存在时）。
func createTable(ctx context.Context, destConn *model.Connection, t *model.Task, cols []dbexec.ColumnInfo, indexes []dbexec.IndexInfo) error {
	var ddl string
	switch destConn.Driver {
	case "postgres":
		ddl = generateCreateTablePG(t, cols, indexes)
	case "mysql":
		ddl = generateCreateTableMySQL(t, cols, indexes)
	default:
		return errUnsupportedDriver
	}

	destC := dbexec.WithDatabase(destConn, t.DestDatabase)
	_, err := dbexec.Exec(ctx, destC, ddl)
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	return updateTaskFields(t.ID, map[string]any{
		"progress": 100,
	})
}

// syncSchema 同步表结构差异。
func syncSchema(ctx context.Context, destConn *model.Connection, t *model.Task,
	srcCols []dbexec.ColumnInfo, srcIndexes []dbexec.IndexInfo,
	destCols []dbexec.ColumnInfo, destIndexes []dbexec.IndexInfo) error {

	destC := dbexec.WithDatabase(destConn, t.DestDatabase)
	var ddls []string

	// 对比列
	destColMap := make(map[string]dbexec.ColumnInfo)
	for _, dc := range destCols {
		destColMap[dc.Name] = dc
	}

	for _, sc := range srcCols {
		if dc, exists := destColMap[sc.Name]; !exists {
			// 列不存在，添加列
			ddl := generateAddColumn(destConn.Driver, t, sc)
			ddls = append(ddls, ddl)
		} else if !columnsMatch(sc, dc) {
			// 列存在但类型不匹配，修改列
			ddl := generateModifyColumn(destConn.Driver, t, sc)
			ddls = append(ddls, ddl)
		}
	}

	// 对比索引
	destIdxMap := make(map[string]dbexec.IndexInfo)
	for _, di := range destIndexes {
		destIdxMap[di.Name] = di
	}

	for _, si := range srcIndexes {
		if si.Primary {
			continue // 主键通过列定义处理
		}
		if _, exists := destIdxMap[si.Name]; !exists {
			// 索引不存在，创建索引
			ddl := generateCreateIndex(destConn.Driver, t, si)
			ddls = append(ddls, ddl)
		}
	}

	// 执行所有 DDL
	for i, ddl := range ddls {
		if err := checkCancel(ctx, t.ID); err != nil {
			return err
		}
		if _, err := dbexec.Exec(ctx, destC, ddl); err != nil {
			return fmt.Errorf("execute DDL [%d/%d]: %w\nSQL: %s", i+1, len(ddls), err, ddl)
		}
		prog := int(float64(i+1) / float64(len(ddls)) * 100)
		_ = updateTaskFields(t.ID, map[string]any{"progress": prog})
	}

	return updateTaskFields(t.ID, map[string]any{"progress": 100})
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
func generateCreateTablePG(t *model.Task, cols []dbexec.ColumnInfo, indexes []dbexec.IndexInfo) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CREATE TABLE %s.%s (\n", quotePGIdent(t.DestSchema), quotePGIdent(t.DestTable)))

	for i, col := range cols {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  ")
		b.WriteString(quotePGIdent(col.Name))
		b.WriteString(" ")
		b.WriteString(col.DataType)
		if !col.Nullable {
			b.WriteString(" NOT NULL")
		}
		if col.DefaultValue != nil && *col.DefaultValue != "" {
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
func generateCreateTableMySQL(t *model.Task, cols []dbexec.ColumnInfo, indexes []dbexec.IndexInfo) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CREATE TABLE `%s`.`%s` (\n", escapeMyIdent(t.DestDatabase), escapeMyIdent(t.DestTable)))

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
func generateAddColumn(driver string, t *model.Task, col dbexec.ColumnInfo) string {
	if driver == "postgres" {
		return fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s%s",
			quotePGIdent(t.DestSchema), quotePGIdent(t.DestTable),
			quotePGIdent(col.Name), col.DataType,
			ternary(!col.Nullable, " NOT NULL", ""))
	}
	return fmt.Sprintf("ALTER TABLE `%s`.`%s` ADD COLUMN `%s` %s%s",
		escapeMyIdent(t.DestDatabase), escapeMyIdent(t.DestTable),
		escapeMyIdent(col.Name), col.DataType,
		ternary(!col.Nullable, " NOT NULL", ""))
}

// generateModifyColumn 生成修改列的 DDL。
func generateModifyColumn(driver string, t *model.Task, col dbexec.ColumnInfo) string {
	if driver == "postgres" {
		return fmt.Sprintf("ALTER TABLE %s.%s ALTER COLUMN %s TYPE %s",
			quotePGIdent(t.DestSchema), quotePGIdent(t.DestTable),
			quotePGIdent(col.Name), col.DataType)
	}
	return fmt.Sprintf("ALTER TABLE `%s`.`%s` MODIFY COLUMN `%s` %s%s",
		escapeMyIdent(t.DestDatabase), escapeMyIdent(t.DestTable),
		escapeMyIdent(col.Name), col.DataType,
		ternary(!col.Nullable, " NOT NULL", ""))
}

// generateCreateIndex 生成创建索引的 DDL。
func generateCreateIndex(driver string, t *model.Task, idx dbexec.IndexInfo) string {
	uniqueStr := ternary(idx.Unique, "UNIQUE ", "")
	if driver == "postgres" {
		cols := make([]string, len(idx.Columns))
		for i, c := range idx.Columns {
			cols[i] = quotePGIdent(c)
		}
		return fmt.Sprintf("CREATE %sINDEX %s ON %s.%s (%s)",
			uniqueStr, quotePGIdent(idx.Name),
			quotePGIdent(t.DestSchema), quotePGIdent(t.DestTable),
			strings.Join(cols, ", "))
	}
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = "`" + escapeMyIdent(c) + "`"
	}
	return fmt.Sprintf("CREATE %sINDEX `%s` ON `%s`.`%s` (%s)",
		uniqueStr, escapeMyIdent(idx.Name),
		escapeMyIdent(t.DestDatabase), escapeMyIdent(t.DestTable),
		strings.Join(cols, ", "))
}

// ternary 三元运算符辅助函数。
func ternary(cond bool, trueVal, falseVal string) string {
	if cond {
		return trueVal
	}
	return falseVal
}
