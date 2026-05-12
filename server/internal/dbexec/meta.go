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
	Name         string  `json:"name"`
	DataType     string  `json:"data_type"`
	Nullable     bool    `json:"nullable"`
	DefaultValue *string `json:"default_value,omitempty"`
	IsPrimary    bool    `json:"is_primary"`
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

// ListDatabases 列出实例上的所有数据库。
func ListDatabases(ctx context.Context, c *model.Connection) ([]DatabaseInfo, error) {
	switch c.Driver {
	case "postgres":
		return pgListDatabases(ctx, c)
	case "mysql":
		return mysqlListDatabases(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// ListSchemas 返回当前数据库中的所有 schema。
func ListSchemas(ctx context.Context, c *model.Connection) ([]Schema, error) {
	switch c.Driver {
	case "postgres":
		return pgListSchemas(ctx, c)
	case "mysql":
		return mysqlListSchemas(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// ListTables lists tables/views in a schema.
func ListTables(ctx context.Context, c *model.Connection, schema string) ([]TableInfo, error) {
	switch c.Driver {
	case "postgres":
		return pgListTables(ctx, c, schema)
	case "mysql":
		return mysqlListTables(ctx, c, schema)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// ListColumns returns columns for a specific table.
func ListColumns(ctx context.Context, c *model.Connection, schema, table string) ([]ColumnInfo, error) {
	switch c.Driver {
	case "postgres":
		return pgListColumns(ctx, c, schema, table)
	case "mysql":
		return mysqlListColumns(ctx, c, schema, table)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// GenerateTableDDL 生成可读的表 DDL（列定义 + 主键 + 索引 + 注释）。
func GenerateTableDDL(ctx context.Context, c *model.Connection, schema, table string) (string, error) {
	switch c.Driver {
	case "postgres":
		return pgGenerateTableDDL(ctx, c, schema, table)
	case "mysql":
		return mysqlGenerateTableDDL(ctx, c, schema, table)
	default:
		return "", fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// Ping tests the connection without caching.
func Ping(ctx context.Context, c *model.Connection) error {
	switch c.Driver {
	case "postgres":
		return pgPing(ctx, c)
	case "mysql":
		return mysqlPing(ctx, c)
	default:
		return fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// Exec runs one SQL statement and returns a result.
func Exec(ctx context.Context, c *model.Connection, sql string, args ...any) (*QueryResult, error) {
	switch c.Driver {
	case "postgres":
		return pgExec(ctx, c, sql, args...)
	case "mysql":
		return mysqlExec(ctx, c, sql, args...)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", c.Driver)
	}
}

// InvalidatePool should be called after a connection is updated or deleted.
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
