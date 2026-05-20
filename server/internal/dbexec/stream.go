// Package dbexec —— 暴露给 service 层的"原生池"入口，用于绕开
// QUERY_MAX_ROWS / QUERY_TIMEOUT_SECONDS 的限制，做长时流式扫表（导出任务）。
//
// 注意：拿到的池/句柄仍由 dbexec 内部缓存并按连接 UpdatedAt 版本失效。
// 调用方不要对返回值调用 Close()，统一交给 InvalidatePool 管理生命周期。
package dbexec

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chy/chat2db/server/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUnsupportedDriverForStream 在调用方传入非 pg/mysql 驱动时返回。
var ErrUnsupportedDriverForStream = errors.New("dbexec: driver does not support streaming pool access")

// AcquirePGPool 返回该 PG 连接对应的 pgxpool 实例。
// 仅适用于 c.Driver == "postgres"。
func AcquirePGPool(ctx context.Context, c *model.Connection) (*pgxpool.Pool, error) {
	if c.Driver != "postgres" {
		return nil, ErrUnsupportedDriverForStream
	}
	return getPool(ctx, c)
}

// AcquireMySQLPool 返回该 MySQL 连接对应的 *sql.DB。
// 仅适用于 c.Driver == "mysql"。
func AcquireMySQLPool(ctx context.Context, c *model.Connection) (*sql.DB, error) {
	if c.Driver != "mysql" {
		return nil, ErrUnsupportedDriverForStream
	}
	return mysqlGetPool(ctx, c)
}
