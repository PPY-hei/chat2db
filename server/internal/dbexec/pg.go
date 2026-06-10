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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolKey uniquely identifies a connection pool by connID + database.
type poolKey struct {
	connID   uint
	database string
}

// poolEntry caches a pgxpool.Pool keyed by poolKey (+ updated_at for staleness).
type poolEntry struct {
	pool    *pgxpool.Pool
	version string
}

var (
	poolMu sync.Mutex
	pools  = map[poolKey]*poolEntry{}
)

// dsnFor builds a pgx DSN from the model.Connection.
// host and port should already be resolved (e.g. via getSSHTunnel).
func dsnFor(c *model.Connection, host string, port int) (string, error) {
	pwd, err := decryptConnPassword(c)
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
	key := poolKey{connID: c.ID, database: c.Database}
	poolMu.Lock()
	entry, ok := pools[key]
	if ok && entry.version == version {
		p := entry.pool
		poolMu.Unlock()
		return p, nil
	}
	if ok {
		entry.pool.Close()
		delete(pools, key)
	}
	poolMu.Unlock()

	// Resolve SSH tunnel once for both DSN and DialFunc
	host, port, err := getSSHTunnel(c)
	if err != nil {
		return nil, err
	}

	dsn, err := dsnFor(c, host, port)
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

	// SSH 隧道需要自定义 DialFunc 让 pgx 直连本地隧道端口
	if c.SSHEnabled {
		localAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
		cfg.ConnConfig.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("tcp", localAddr, 10*time.Second)
		}
	} else if proxyEnabled(c) {
		// 经 HTTP/SOCKS5 代理拨号到真实目标地址（addr 即 DSN 解析出的真实 host:port）
		cfg.ConnConfig.DialFunc = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return proxyDialContext(ctx, c, addr)
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
	pools[key] = &poolEntry{pool: p, version: version}
	poolMu.Unlock()
	return p, nil
}

// pgPing tests the PG connection without caching.
func pgPing(ctx context.Context, c *model.Connection) error {
	// Resolve SSH tunnel once
	host, port, err := getSSHTunnel(c)
	if err != nil {
		return err
	}

	dsn, err := dsnFor(c, host, port)
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
		localAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
		cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("tcp", localAddr, 10*time.Second)
		}
	} else if proxyEnabled(c) {
		cfg.DialFunc = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return proxyDialContext(ctx, c, addr)
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

// pgExec runs one SQL statement on PG and returns a result.
func pgExec(ctx context.Context, c *model.Connection, sql string, args ...any) (*QueryResult, error) {
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
			out.Rows = append(out.Rows, normalizeRowValuesForJSON(out.Types, vals))
			count++
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tag := rows.CommandTag()
	out.Tag = tag.String()
	out.RowsAffected = tag.RowsAffected()
	out.ElapsedMs = time.Since(start).Milliseconds()
	return out, nil
}

// pgInvalidatePool closes and removes all cached PG pools for a connection
// (including temporary pools created for different databases via WithDatabase).
func pgInvalidatePool(connID uint) {
	poolMu.Lock()
	for key, e := range pools {
		if key.connID == connID {
			e.pool.Close()
			delete(pools, key)
		}
	}
	poolMu.Unlock()
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
