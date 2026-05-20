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
)

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
		return x
	case []byte:
		return string(x)
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
