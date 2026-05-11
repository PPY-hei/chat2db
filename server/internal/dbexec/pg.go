package dbexec

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolEntry caches a pgxpool.Pool keyed by connection ID (+ updated_at).
type poolEntry struct {
	pool    *pgxpool.Pool
	version string // connection.UpdatedAt to invalidate cache on edit
}

var (
	poolMu sync.Mutex
	pools  = map[uint]*poolEntry{}
)

// dsnFor builds a pgx DSN from the model.Connection.
func dsnFor(c *model.Connection) (string, error) {
	pwd, err := service.DecryptPassword(c)
	if err != nil {
		return "", err
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Username, pwd),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Database,
	}
	q := u.Query()
	if c.SSLMode == "" {
		q.Set("sslmode", "disable")
	} else {
		q.Set("sslmode", c.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// getPool returns a cached pgxpool.Pool for the connection.
func getPool(ctx context.Context, c *model.Connection) (*pgxpool.Pool, error) {
	version := c.UpdatedAt.Format(time.RFC3339Nano)
	poolMu.Lock()
	entry, ok := pools[c.ID]
	if ok && entry.version == version {
		p := entry.pool
		poolMu.Unlock()
		return p, nil
	}
	if ok {
		entry.pool.Close()
		delete(pools, c.ID)
	}
	poolMu.Unlock()

	dsn, err := dsnFor(c)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 5
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 10 * time.Minute
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	poolMu.Lock()
	pools[c.ID] = &poolEntry{pool: p, version: version}
	poolMu.Unlock()
	return p, nil
}

// Ping tests the connection without caching.
func Ping(ctx context.Context, c *model.Connection) error {
	dsn, err := dsnFor(c)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return conn.Ping(ctx)
}

// QueryResult is the wire format returned by an executed query.
type QueryResult struct {
	Columns      []string          `json:"columns"`
	Rows         [][]any           `json:"rows"`
	RowsAffected int64             `json:"rows_affected"`
	Truncated    bool              `json:"truncated"`
	ElapsedMs    int64             `json:"elapsed_ms"`
	Types        []string          `json:"types,omitempty"`
	Message      string            `json:"message,omitempty"`
	Tag          string            `json:"tag,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// Exec runs one SQL statement and returns a result.
// For SELECT / RETURNING it fills Columns/Rows; otherwise RowsAffected.
func Exec(ctx context.Context, c *model.Connection, sql string, args ...any) (*QueryResult, error) {
	cfg := config.Get()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.QueryTimeoutSec)*time.Second)
	defer cancel()
	p, err := getPool(ctx, c)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	rows, err := p.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	hasCols := len(fields) > 0
	out := &QueryResult{}
	if hasCols {
		out.Columns = make([]string, len(fields))
		out.Types = make([]string, len(fields))
		for i, f := range fields {
			out.Columns[i] = string(f.Name)
			out.Types[i] = pgTypeName(f.DataTypeOID)
		}
		limit := cfg.QueryMaxRows
		count := 0
		for rows.Next() {
			if count >= limit {
				out.Truncated = true
				break
			}
			vals, err := rows.Values()
			if err != nil {
				return nil, err
			}
			out.Rows = append(out.Rows, vals)
			count++
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	tag := rows.CommandTag()
	out.Tag = tag.String()
	out.RowsAffected = tag.RowsAffected()
	out.ElapsedMs = time.Since(start).Milliseconds()
	return out, nil
}

// InvalidatePool should be called after a connection is updated or deleted.
func InvalidatePool(connID uint) {
	poolMu.Lock()
	defer poolMu.Unlock()
	if e, ok := pools[connID]; ok {
		e.pool.Close()
		delete(pools, connID)
	}
}

// Rudimentary PG type OID mapping for the most common cases.
func pgTypeName(oid uint32) string {
	switch oid {
	case 16:
		return "bool"
	case 20:
		return "int8"
	case 21:
		return "int2"
	case 23:
		return "int4"
	case 25, 1043:
		return "text"
	case 700:
		return "float4"
	case 701:
		return "float8"
	case 1082:
		return "date"
	case 1114:
		return "timestamp"
	case 1184:
		return "timestamptz"
	case 1700:
		return "numeric"
	case 2950:
		return "uuid"
	case 3802, 114:
		return "jsonb"
	}
	return fmt.Sprintf("oid:%d", oid)
}
