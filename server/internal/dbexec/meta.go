package dbexec

import (
	"context"
	"fmt"

	"github.com/chy/chat2db/server/internal/model"
)

// Schema represents a database schema (PG namespace / MySQL database).
type Schema struct {
	Name string `json:"name"`
}

// DatabaseInfo represents a database on the instance.
type DatabaseInfo struct {
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	Current bool   `json:"current"` // 是否是连接配置里的默认数据库
}

// TableInfo represents a table or view.
type TableInfo struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // table | view | matview
}

// ColumnInfo describes a column.
type ColumnInfo struct {
	Name          string  `json:"name"`
	DataType      string  `json:"data_type"`
	Nullable      bool    `json:"nullable"`
	DefaultValue  *string `json:"default_value,omitempty"`
	IsPrimary     bool    `json:"is_primary"`
	Comment       *string `json:"comment,omitempty"`
	AutoIncrement bool    `json:"auto_increment"`
}

// IndexInfo describes a table index.
type IndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Method  string   `json:"method,omitempty"` // btree/hash (pg) | BTREE/... (mysql)
}

// WithDatabase 返回一个 connection 的浅拷贝，Database 字段被替换为指定值。
// 用于临时切换数据库浏览，不会修改持久化的连接配置。
// 使用原始 ID 不变，池管理器通过 connID+database 组合 key 来区分不同 database 的池。
func WithDatabase(c *model.Connection, database string) *model.Connection {
	if database == "" || database == c.Database {
		return c
	}
	cp := *c
	cp.Database = database
	return &cp
}

// --- 按 driver 分发的公共入口 ---
//
// 这些包级函数保留是为了向上层（service / api / cmd）提供稳定的入口；
// 内部实现全部走 Driver 接口（见 driver.go），每次调用用 Open(c) 拿一个
// 瘦 Driver 实例，方法透传到具体驱动适配器。池缓存仍由各驱动的全局 map
// 持有，与 Driver 实例的生命周期解耦。

// ListDatabases 列出实例上的所有数据库。
func ListDatabases(ctx context.Context, c *model.Connection) ([]DatabaseInfo, error) {
	d, err := Open(c)
	if err != nil {
		return nil, err
	}
	return d.ListDatabases(ctx)
}

// ListSchemas 返回当前数据库中的所有 schema。
func ListSchemas(ctx context.Context, c *model.Connection) ([]Schema, error) {
	d, err := Open(c)
	if err != nil {
		return nil, err
	}
	return d.ListSchemas(ctx)
}

// ListTables lists tables/views in a schema.
func ListTables(ctx context.Context, c *model.Connection, schema string) ([]TableInfo, error) {
	d, err := Open(c)
	if err != nil {
		return nil, err
	}
	return d.ListTables(ctx, schema)
}

// ListColumns returns columns for a specific table.
func ListColumns(ctx context.Context, c *model.Connection, schema, table string) ([]ColumnInfo, error) {
	d, err := Open(c)
	if err != nil {
		return nil, err
	}
	return d.ListColumns(ctx, schema, table)
}

// ListIndexes 返回指定表的索引信息。
func ListIndexes(ctx context.Context, c *model.Connection, schema, table string) ([]IndexInfo, error) {
	d, err := Open(c)
	if err != nil {
		return nil, err
	}
	return d.ListIndexes(ctx, schema, table)
}

// GenerateTableDDL 生成可读的表 DDL（列定义 + 主键 + 索引 + 注释）。
func GenerateTableDDL(ctx context.Context, c *model.Connection, schema, table string) (string, error) {
	d, err := Open(c)
	if err != nil {
		return "", err
	}
	return d.GenerateTableDDL(ctx, schema, table)
}

// Ping tests the connection without caching.
func Ping(ctx context.Context, c *model.Connection) error {
	d, err := Open(c)
	if err != nil {
		return err
	}
	return d.Ping(ctx)
}

// Exec runs one SQL statement and returns a result.
func Exec(ctx context.Context, c *model.Connection, sql string, args ...any) (*QueryResult, error) {
	d, err := Open(c)
	if err != nil {
		return nil, err
	}
	return d.Exec(ctx, sql, args...)
}

// InvalidatePool should be called after a connection is updated or deleted.
//
// 目前仍广播到所有已注册驱动（pg + mysql + ssh 隧道），而不是只调一个驱动，
// 因为调用方通常只有 connID 而没有 Driver 名；广播是幂等的、代价极低。
func InvalidatePool(connID uint) {
	pgInvalidatePool(connID)
	mysqlInvalidatePool(connID)
	invalidateSSHTunnel(connID)
}

// --- helpers ---

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case int64:
		return fmt.Sprintf("%d", val)
	case int32:
		return fmt.Sprintf("%d", val)
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case float32:
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func asBool(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	// MySQL 返回 int64
	if n, ok := v.(int64); ok {
		return n != 0
	}
	return false
}
