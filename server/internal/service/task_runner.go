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
	mysqldriver "github.com/go-sql-driver/mysql"
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

	var grandTotal int64
	for i, p := range pairs {
		if err := checkCancel(ctx, t.ID); err != nil {
			return err
		}
		rows, err := syncOneTableData(ctx, srcConn, destConn, t, p)
		if err != nil {
			return fmt.Errorf("sync %s.%s.%s -> %s.%s.%s: %w",
				p.SrcDatabase, p.SrcSchema, p.SrcTable,
				p.DestDatabase, p.DestSchema, p.DestTable, err)
		}
		grandTotal += rows
		prog := int(float64(i+1) / float64(len(pairs)) * 100)
		_ = updateTaskFields(t.ID, map[string]any{
			"done_tables":    i + 1,
			"processed_rows": grandTotal,
			"total_rows":     grandTotal,
			"progress":       prog,
		})
	}

	return updateTaskFields(t.ID, map[string]any{
		"processed_rows": grandTotal,
		"total_rows":     grandTotal,
	})
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
func syncOneTableData(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, p syncTablePair) (int64, error) {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	srcCols, err := dbexec.ListColumns(ctx, srcC, p.SrcSchema, p.SrcTable)
	if err != nil {
		return 0, fmt.Errorf("list source columns: %w", err)
	}
	if len(srcCols) == 0 {
		return 0, errors.New("source table has no columns")
	}

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	destCols, err := dbexec.ListColumns(ctx, destC, p.DestSchema, p.DestTable)
	if err != nil {
		// 目标表不存在 → 按源表结构建表，再继续同步数据
		if isMissingTableErr(err) {
			srcIdx, _ := dbexec.ListIndexes(ctx, srcC, p.SrcSchema, p.SrcTable)
			if cerr := createDestTableFromSrc(ctx, destConn, p, srcCols, srcIdx); cerr != nil {
				return 0, fmt.Errorf("auto-create dest table: %w", cerr)
			}
			destCols, err = dbexec.ListColumns(ctx, destC, p.DestSchema, p.DestTable)
			if err != nil {
				return 0, fmt.Errorf("list destination columns after create: %w", err)
			}
		} else {
			return 0, fmt.Errorf("list destination columns: %w", err)
		}
	}
	if len(destCols) == 0 {
		return 0, errors.New("destination table has no columns")
	}

	matched := matchColumnNames(srcCols, destCols)
	if len(matched) == 0 {
		return 0, errors.New("no matching columns between source and destination")
	}

	switch srcConn.Driver {
	case "postgres":
		return syncDataFromPG(ctx, srcConn, destConn, t, p, matched)
	case "mysql":
		return syncDataFromMySQL(ctx, srcConn, destConn, t, p, matched)
	default:
		return 0, errUnsupportedDriver
	}
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
func syncDataFromPG(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, p syncTablePair, cols []string) (int64, error) {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	pool, err := dbexec.AcquirePGPool(ctx, srcC)
	if err != nil {
		return 0, fmt.Errorf("acquire source pool: %w", err)
	}

	colList := strings.Join(quoteIdentifiers(cols, srcConn.Driver), ", ")
	q := fmt.Sprintf("SELECT %s FROM %s.%s", colList, quotePGIdent(p.SrcSchema), quotePGIdent(p.SrcTable))
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
			if err := insertBatch(ctx, destConn, p, cols, batch); err != nil {
				return totalRows, fmt.Errorf("insert batch: %w", err)
			}
			totalRows += int64(len(batch))
			batch = batch[:0]

			_ = updateTaskFields(t.ID, map[string]any{
				"processed_rows": totalRows,
			})
		}
	}

	// 插入剩余数据
	if len(batch) > 0 {
		if err := insertBatch(ctx, destConn, p, cols, batch); err != nil {
			return totalRows, fmt.Errorf("insert final batch: %w", err)
		}
		totalRows += int64(len(batch))
	}

	return totalRows, rows.Err()
}

// syncDataFromMySQL 从 MySQL 源表同步数据到目标表。
func syncDataFromMySQL(ctx context.Context, srcConn, destConn *model.Connection, t *model.Task, p syncTablePair, cols []string) (int64, error) {
	srcC := dbexec.WithDatabase(srcConn, p.SrcDatabase)
	pool, err := dbexec.AcquireMySQLPool(ctx, srcC)
	if err != nil {
		return 0, fmt.Errorf("acquire source pool: %w", err)
	}

	colList := strings.Join(quoteIdentifiers(cols, srcConn.Driver), ", ")
	q := fmt.Sprintf("SELECT %s FROM `%s`.`%s`", colList, escapeMyIdent(p.SrcDatabase), escapeMyIdent(p.SrcTable))
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
			if err := insertBatch(ctx, destConn, p, cols, batch); err != nil {
				return totalRows, fmt.Errorf("insert batch: %w", err)
			}
			totalRows += int64(len(batch))
			batch = batch[:0]

			_ = updateTaskFields(t.ID, map[string]any{
				"processed_rows": totalRows,
			})
		}
	}

	if len(batch) > 0 {
		if err := insertBatch(ctx, destConn, p, cols, batch); err != nil {
			return totalRows, fmt.Errorf("insert final batch: %w", err)
		}
		totalRows += int64(len(batch))
	}

	return totalRows, rows.Err()
}

// insertBatch 批量插入数据到目标表。
func insertBatch(ctx context.Context, destConn *model.Connection, p syncTablePair, cols []string, batch [][]any) error {
	if len(batch) == 0 {
		return nil
	}

	destC := dbexec.WithDatabase(destConn, p.DestDatabase)
	colList := strings.Join(quoteIdentifiers(cols, destConn.Driver), ", ")

	switch destConn.Driver {
	case "postgres":
		return insertBatchPG(ctx, destC, p, colList, batch, len(cols))
	case "mysql":
		return insertBatchMySQL(ctx, destC, p, colList, batch, len(cols))
	default:
		return errUnsupportedDriver
	}
}

// insertBatchPG 批量插入到 PostgreSQL。
func insertBatchPG(ctx context.Context, destC *model.Connection, p syncTablePair, colList string, batch [][]any, colCount int) error {
	pool, err := dbexec.AcquirePGPool(ctx, destC)
	if err != nil {
		return err
	}

	q, args := buildPGBatchInsert(p, colList, batch, colCount)
	_, err = pool.Exec(ctx, q, args...)
	return err
}

func buildPGBatchInsert(p syncTablePair, colList string, batch [][]any, colCount int) (string, []any) {
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

	// 使用 ON CONFLICT DO NOTHING 跳过主键/唯一约束冲突
	q := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES %s ON CONFLICT DO NOTHING",
		quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable), colList, strings.Join(placeholders, ", "))
	return q, args
}

// insertBatchMySQL 批量插入到 MySQL。
func insertBatchMySQL(ctx context.Context, destC *model.Connection, p syncTablePair, colList string, batch [][]any, colCount int) error {
	pool, err := dbexec.AcquireMySQLPool(ctx, destC)
	if err != nil {
		return err
	}

	q, args := buildMySQLBatchInsert(p, colList, batch, colCount)
	if _, err := pool.ExecContext(ctx, q, args...); err == nil {
		return nil
	} else if !isMySQLDuplicateKeyErr(err) {
		return err
	}

	singleQ := buildMySQLSingleInsert(p, colList, colCount)
	for _, row := range batch {
		if _, err := pool.ExecContext(ctx, singleQ, row...); err != nil {
			if isMySQLDuplicateKeyErr(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func buildMySQLBatchInsert(p syncTablePair, colList string, batch [][]any, colCount int) (string, []any) {
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
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable), colList, strings.Join(placeholders, ", "))
	return q, args
}

func buildMySQLSingleInsert(p syncTablePair, colList string, colCount int) string {
	rowPlaceholders := make([]string, colCount)
	for i := 0; i < colCount; i++ {
		rowPlaceholders[i] = "?"
	}
	return fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES (%s)",
		escapeMyIdent(p.DestDatabase), escapeMyIdent(p.DestTable), colList, strings.Join(rowPlaceholders, ", "))
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
		return fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s%s",
			quotePGIdent(p.DestSchema), quotePGIdent(p.DestTable),
			quotePGIdent(col.Name), col.DataType,
			ternary(!col.Nullable, " NOT NULL", ""))
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
