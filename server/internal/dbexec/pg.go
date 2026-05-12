package dbexec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolEntry caches a pgxpool.Pool keyed by connection ID (+ updated_at).
type poolEntry struct {
	pool    *pgxpool.Pool
	version string
}

var (
	poolMu sync.Mutex
	pools  = map[uint]*poolEntry{}
)

// dsnFor builds a pgx DSN from the model.Connection.
// If SSH tunnel is enabled, host:port will be the local tunnel endpoint.
func dsnFor(c *model.Connection) (string, error) {
	pwd, err := service.DecryptPassword(c)
	if err != nil {
		return "", err
	}

	host, port, err := getSSHTunnel(c)
	if err != nil {
		return "", err
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Username, pwd),
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/" + c.Database,
	}
	q := u.Query()
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// buildTLSConfig creates a *tls.Config from the connection's SSL cert fields.
// Returns nil if no certs are configured (pgx will use sslmode param only).
func buildTLSConfig(c *model.Connection) (*tls.Config, error) {
	key := config.Get().CredentialKey
	hasCerts := c.SSLCACertEnc != "" || c.SSLClientCertEnc != "" || c.SSLClientKeyEnc != ""
	if !hasCerts {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		ServerName: c.Host,
	}

	if c.SSLCACertEnc != "" {
		caPEM, err := cryptopkg.DecryptString(c.SSLCACertEnc, key)
		if err != nil {
			return nil, fmt.Errorf("decrypt ssl ca cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, fmt.Errorf("invalid CA certificate PEM")
		}
		tlsCfg.RootCAs = pool
	}

	if c.SSLClientCertEnc != "" && c.SSLClientKeyEnc != "" {
		certPEM, err := cryptopkg.DecryptString(c.SSLClientCertEnc, key)
		if err != nil {
			return nil, fmt.Errorf("decrypt ssl client cert: %w", err)
		}
		keyPEM, err := cryptopkg.DecryptString(c.SSLClientKeyEnc, key)
		if err != nil {
			return nil, fmt.Errorf("decrypt ssl client key: %w", err)
		}
		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			return nil, fmt.Errorf("parse ssl client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
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

	tlsCfg, err := buildTLSConfig(c)
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}

	// SSH 隧道需要自定义 DialFunc 连到本地隧道端口
	if c.SSHEnabled {
		tunnelHost, tunnelPort, err := getSSHTunnel(c)
		if err != nil {
			return nil, err
		}
		localAddr := fmt.Sprintf("%s:%d", tunnelHost, tunnelPort)
		cfg.ConnConfig.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("tcp", localAddr, 10*time.Second)
		}
	}

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
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	tlsCfg, err := buildTLSConfig(c)
	if err != nil {
		return err
	}
	if tlsCfg != nil {
		cfg.TLSConfig = tlsCfg
	}
	if c.SSHEnabled {
		tunnelHost, tunnelPort, err2 := getSSHTunnel(c)
		if err2 != nil {
			return err2
		}
		localAddr := fmt.Sprintf("%s:%d", tunnelHost, tunnelPort)
		cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("tcp", localAddr, 10*time.Second)
		}
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
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
func Exec(ctx context.Context, c *model.Connection, sql string, args ...any) (*QueryResult, error) {
	appCfg := config.Get()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(appCfg.QueryTimeoutSec)*time.Second)
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
		limit := appCfg.QueryMaxRows
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
	if e, ok := pools[connID]; ok {
		e.pool.Close()
		delete(pools, connID)
	}
	poolMu.Unlock()
	invalidateSSHTunnel(connID)
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
