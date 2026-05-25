package dbexec

import (
	"context"

	"github.com/chy/chat2db/server/internal/model"
)

// mysqlDriver 是 MySQL 的 Driver 实现。
//
// 与 pgDriver 类似，它是对 mysql.go / mysql_meta.go 中现有自由函数的薄封装，
// 持有 *model.Connection，所有方法把 ctx 与 conn 透传。
//
// MySQL 池缓存由 mysql.go 中的 mysqlGetPool/mysqlPoolMu/mysqlPools 管理；
// 驱动实例本身不再额外缓存，避免重复的生命周期管理。
type mysqlDriver struct {
	conn *model.Connection
}

// newMySQLDriver 是注册到 registry 的 Factory。
// 当前无需提前校验，但保留 error 返回以保持 Factory 契约。
func newMySQLDriver(c *model.Connection) (Driver, error) {
	return &mysqlDriver{conn: c}, nil
}

func init() {
	Register("mysql", newMySQLDriver)
}

func (d *mysqlDriver) Name() string { return "mysql" }

func (d *mysqlDriver) Capabilities() Capabilities {
	return Capabilities{
		DefaultPort: 3306,
		// MySQL 目前实现映射：disable / require(=true,skip-verify) / verify-ca
		SSLModes:      []string{"disable", "require", "verify-ca"},
		SupportsSSH:   true,
		SupportsProxy: true,
		SupportsMTLS:  true,
		SchemaSupport: false, // MySQL 中 Database 即 Schema
	}
}

func (d *mysqlDriver) WithDatabase(database string) Driver {
	return &mysqlDriver{conn: WithDatabase(d.conn, database)}
}

func (d *mysqlDriver) Ping(ctx context.Context) error {
	return mysqlPing(ctx, d.conn)
}

func (d *mysqlDriver) Exec(ctx context.Context, sql string, args ...any) (*QueryResult, error) {
	return mysqlExec(ctx, d.conn, sql, args...)
}

func (d *mysqlDriver) ListDatabases(ctx context.Context) ([]DatabaseInfo, error) {
	return mysqlListDatabases(ctx, d.conn)
}

func (d *mysqlDriver) ListSchemas(ctx context.Context) ([]Schema, error) {
	return mysqlListSchemas(ctx, d.conn)
}

func (d *mysqlDriver) ListTables(ctx context.Context, schema string) ([]TableInfo, error) {
	return mysqlListTables(ctx, d.conn, schema)
}

func (d *mysqlDriver) ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error) {
	return mysqlListColumns(ctx, d.conn, schema, table)
}

func (d *mysqlDriver) ListIndexes(ctx context.Context, schema, table string) ([]IndexInfo, error) {
	return mysqlListIndexes(ctx, d.conn, schema, table)
}

func (d *mysqlDriver) GenerateTableDDL(ctx context.Context, schema, table string) (string, error) {
	return mysqlGenerateTableDDL(ctx, d.conn, schema, table)
}

// Invalidate 清理与本连接相关的所有 MySQL 池，并联动关闭 SSH 隧道。
func (d *mysqlDriver) Invalidate() {
	mysqlInvalidatePool(d.conn.ID)
	invalidateSSHTunnel(d.conn.ID)
}
