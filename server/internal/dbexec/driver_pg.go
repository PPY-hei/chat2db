package dbexec

import (
	"context"

	"github.com/chy/chat2db/server/internal/model"
)

// pgDriver 是 PostgreSQL 的 Driver 实现。
//
// 它是对现有自由函数（pgPing/pgExec/pgListXxx 等）的薄封装：
// 持有 *model.Connection，所有方法把 ctx 与 conn 透传给原实现。
// 这样做保证 PR #1 是纯重构——零行为变更，便于后续 PR 走查。
//
// 池缓存仍由 pg.go 中的 getPool/poolMu/pools 全局管理；驱动实例本身不持池，
// 避免在没有引入持久化生命周期管理时产生重复缓存层。
type pgDriver struct {
	conn *model.Connection
}

// newPGDriver 是注册到 registry 的 Factory。
// 当前实现没有需要立即校验的 Connection 字段，但保留 error 返回以保持 Factory 契约一致，
// 也便于未来在这里做 DSN/SSL 预校验。
func newPGDriver(c *model.Connection) (Driver, error) {
	return &pgDriver{conn: c}, nil
}

func init() {
	Register("postgres", newPGDriver)
}

func (d *pgDriver) Name() string { return "postgres" }

func (d *pgDriver) Capabilities() Capabilities {
	return Capabilities{
		DefaultPort:   5432,
		SSLModes:      []string{"disable", "require", "verify-ca", "verify-full"},
		SupportsSSH:   true,
		SupportsMTLS:  true,
		SchemaSupport: true,
	}
}

func (d *pgDriver) WithDatabase(database string) Driver {
	return &pgDriver{conn: WithDatabase(d.conn, database)}
}

func (d *pgDriver) Ping(ctx context.Context) error {
	return pgPing(ctx, d.conn)
}

func (d *pgDriver) Exec(ctx context.Context, sql string, args ...any) (*QueryResult, error) {
	return pgExec(ctx, d.conn, sql, args...)
}

func (d *pgDriver) ListDatabases(ctx context.Context) ([]DatabaseInfo, error) {
	return pgListDatabases(ctx, d.conn)
}

func (d *pgDriver) ListSchemas(ctx context.Context) ([]Schema, error) {
	return pgListSchemas(ctx, d.conn)
}

func (d *pgDriver) ListTables(ctx context.Context, schema string) ([]TableInfo, error) {
	return pgListTables(ctx, d.conn, schema)
}

func (d *pgDriver) ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error) {
	return pgListColumns(ctx, d.conn, schema, table)
}

func (d *pgDriver) GenerateTableDDL(ctx context.Context, schema, table string) (string, error) {
	return pgGenerateTableDDL(ctx, d.conn, schema, table)
}

// Invalidate 清理与本连接相关的所有 PG 池，并联动关闭 SSH 隧道。
// 与原 InvalidatePool(connID) 行为对齐，但不再触碰其它驱动的池——
// 这部分跨驱动联动放在 dbexec.InvalidatePool 中处理，
// Driver.Invalidate 只负责自己的资源。
func (d *pgDriver) Invalidate() {
	pgInvalidatePool(d.conn.ID)
	invalidateSSHTunnel(d.conn.ID)
}
