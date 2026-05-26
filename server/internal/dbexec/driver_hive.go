package dbexec

import (
	"context"

	"github.com/chy/chat2db/server/internal/model"
)

// hiveDriver 是 Hive 的 Driver 实现。
//
// 与 pgDriver/mysqlDriver 类似，它是对 hive.go / hive_meta.go 中现有自由函数的薄封装，
// 持有 *model.Connection，所有方法把 ctx 与 conn 透传。
//
// Hive 池缓存由 hive.go 中的 hiveGetPool/hivePoolMu/hivePools 管理；
// 驱动实例本身不再额外缓存，避免重复的生命周期管理。
type hiveDriver struct {
	conn *model.Connection
}

// newHiveDriver 是注册到 registry 的 Factory。
// 当前无需提前校验，但保留 error 返回以保持 Factory 契约。
func newHiveDriver(c *model.Connection) (Driver, error) {
	return &hiveDriver{conn: c}, nil
}

func init() {
	Register("hive", newHiveDriver)
}

func (d *hiveDriver) Name() string { return "hive" }

func (d *hiveDriver) Capabilities() Capabilities {
	return Capabilities{
		DefaultPort:   10000, // HiveServer2 默认端口
		SSLModes:      []string{"disable"},
		SupportsSSH:   true,
		SupportsProxy: false,
		SupportsMTLS:  false,
		SchemaSupport: false, // Hive 中 Database 即 Schema
	}
}

func (d *hiveDriver) WithDatabase(database string) Driver {
	return &hiveDriver{conn: WithDatabase(d.conn, database)}
}

func (d *hiveDriver) Ping(ctx context.Context) error {
	return hivePing(ctx, d.conn)
}

func (d *hiveDriver) Exec(ctx context.Context, sql string, args ...any) (*QueryResult, error) {
	return hiveExec(ctx, d.conn, sql, args...)
}

func (d *hiveDriver) ListDatabases(ctx context.Context) ([]DatabaseInfo, error) {
	return hiveListDatabases(ctx, d.conn)
}

func (d *hiveDriver) ListSchemas(ctx context.Context) ([]Schema, error) {
	return hiveListSchemas(ctx, d.conn)
}

func (d *hiveDriver) ListTables(ctx context.Context, schema string) ([]TableInfo, error) {
	return hiveListTables(ctx, d.conn, schema)
}

func (d *hiveDriver) ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error) {
	return hiveListColumns(ctx, d.conn, schema, table)
}

func (d *hiveDriver) ListIndexes(ctx context.Context, schema, table string) ([]IndexInfo, error) {
	return hiveListIndexes(ctx, d.conn, schema, table)
}

func (d *hiveDriver) GenerateTableDDL(ctx context.Context, schema, table string) (string, error) {
	return hiveGenerateTableDDL(ctx, d.conn, schema, table)
}

// Invalidate 清理与本连接相关的所有 Hive 池，并联动关闭 SSH 隧道。
func (d *hiveDriver) Invalidate() {
	hiveInvalidatePool(d.conn.ID)
	invalidateSSHTunnel(d.conn.ID)
}
