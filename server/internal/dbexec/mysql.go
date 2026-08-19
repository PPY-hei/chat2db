package dbexec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// mysqlPoolKey uniquely identifies a MySQL connection pool by connID + database.
type mysqlPoolKey struct {
	connID   uint
	database string
}

type mysqlPoolEntry struct {
	db      *sql.DB
	version string
}

var (
	mysqlPoolMu sync.Mutex
	mysqlPools  = map[mysqlPoolKey]*mysqlPoolEntry{}
)

// mysqlDSN builds a MySQL DSN from the model.Connection using mysql.Config
// to properly escape special characters in username/password.
func mysqlDSN(c *model.Connection) (string, error) {
	pwd, err := decryptConnPassword(c)
	if err != nil {
		return "", err
	}

	host, port, err := getSSHTunnel(c)
	if err != nil {
		return "", err
	}

	dbName := c.Database
	if dbName == "" {
		dbName = "information_schema"
	}

	cfg := mysqldriver.NewConfig()
	cfg.User = c.Username
	cfg.Passwd = pwd
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)

	// 代理：注册自定义 net，让 go-sql-driver 经代理拨号到真实地址。
	// 与 SSH 互斥（proxyEnabled 内部已判断），用 connID 派生唯一注册名。
	if proxyEnabled(c) {
		netName := fmt.Sprintf("chat2db-proxy-%d", c.ID)
		target := cfg.Addr
		mysqldriver.RegisterDialContext(netName, func(ctx context.Context, _ string) (net.Conn, error) {
			return proxyDialContext(ctx, c, target)
		})
		cfg.Net = netName
	}
	cfg.DBName = dbName
	cfg.Params = map[string]string{
		"charset": "utf8mb4",
	}
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second

	sslMode := c.SSLMode
	if sslMode == "" || sslMode == "disable" {
		cfg.TLSConfig = "false"
	} else if sslMode == "require" || sslMode == "true" {
		cfg.TLSConfig = "skip-verify"
	} else if sslMode == "verify-ca" {
		// 构建自定义 TLS 配置并注册到 mysql driver
		tlsCfg, err := buildMySQLTLSConfig(c)
		if err != nil {
			return "", fmt.Errorf("mysql tls config: %w", err)
		}
		// 用 connID 生成唯一的注册名，避免不同连接互相覆盖
		tlsName := fmt.Sprintf("chat2db-mysql-%d", c.ID)
		if err := mysqldriver.RegisterTLSConfig(tlsName, tlsCfg); err != nil {
			return "", fmt.Errorf("register mysql tls: %w", err)
		}
		cfg.TLSConfig = tlsName
	}

	return cfg.FormatDSN(), nil
}

// buildMySQLTLSConfig creates a *tls.Config from the connection's SSL cert fields for MySQL.
func buildMySQLTLSConfig(c *model.Connection) (*tls.Config, error) {
	key := config.Get().CredentialKey

	tlsCfg := &tls.Config{
		ServerName: c.Host,
	}

	// CA 证书
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

	// 客户端证书 + 私钥（双向 TLS）
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

// mysqlGetPool returns a cached *sql.DB for the connection.
func mysqlGetPool(ctx context.Context, c *model.Connection) (*sql.DB, error) {
	version := c.UpdatedAt.Format(time.RFC3339Nano)
	key := mysqlPoolKey{connID: c.ID, database: c.Database}
	mysqlPoolMu.Lock()
	entry, ok := mysqlPools[key]
	if ok && entry.version == version {
		db := entry.db
		mysqlPoolMu.Unlock()
		return db, nil
	}
	if ok {
		entry.db.Close()
		delete(mysqlPools, key)
	}
	mysqlPoolMu.Unlock()

	dsn, err := mysqlDSN(c)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(10 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	mysqlPoolMu.Lock()
	mysqlPools[key] = &mysqlPoolEntry{db: db, version: version}
	mysqlPoolMu.Unlock()
	return db, nil
}

// mysqlPing tests the connection without caching.
func mysqlPing(ctx context.Context, c *model.Connection) error {
	dsn, err := mysqlDSN(c)
	if err != nil {
		return err
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return db.PingContext(ctx2)
}

// mysqlExec runs one SQL statement and returns a result.
func mysqlExec(ctx context.Context, c *model.Connection, sqlStr string, args ...any) (*QueryResult, error) {
	return mysqlExecWithRowLimit(ctx, c, config.Get().QueryMaxRows, sqlStr, args...)
}

// mysqlExecAllRows is reserved for bounded metadata queries such as listing
// tables. User SQL continues to use mysqlExec and QUERY_MAX_ROWS.
func mysqlExecAllRows(ctx context.Context, c *model.Connection, sqlStr string, args ...any) (*QueryResult, error) {
	return mysqlExecWithRowLimit(ctx, c, 0, sqlStr, args...)
}

func mysqlExecWithRowLimit(ctx context.Context, c *model.Connection, rowLimit int, sqlStr string, args ...any) (*QueryResult, error) {
	appCfg := config.Get()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(appCfg.QueryTimeoutSec)*time.Second)
	defer cancel()

	db, err := mysqlGetPool(ctx, c)
	if err != nil {
		return nil, err
	}

	start := time.Now()

	// 判断是否为返回结果集的语句（SELECT / SHOW / DESCRIBE / EXPLAIN / WITH）
	if !mysqlIsQuery(sqlStr) {
		// 非查询语句使用 ExecContext 获取正确的 RowsAffected
		result, err := db.ExecContext(ctx, sqlStr, args...)
		if err != nil {
			return nil, err
		}
		affected, _ := result.RowsAffected()
		out := &QueryResult{
			RowsAffected: affected,
			ElapsedMs:    time.Since(start).Milliseconds(),
			Tag:          "OK",
		}
		return out, nil
	}

	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	out := &QueryResult{}
	hasCols := len(cols) > 0

	if hasCols {
		out.Columns = cols
		colTypes, _ := rows.ColumnTypes()
		if colTypes != nil {
			out.Types = make([]string, len(colTypes))
			for i, ct := range colTypes {
				out.Types[i] = ct.DatabaseTypeName()
			}
		}

		count := 0
		for rows.Next() {
			if rowLimit > 0 && count >= rowLimit {
				out.Truncated = true
				break
			}
			// 扫描为 []any
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			// 转换 []byte -> string
			row := make([]any, len(cols))
			for i, v := range vals {
				switch vv := v.(type) {
				case []byte:
					row[i] = string(vv)
				default:
					row[i] = vv
				}
			}
			row = normalizeRowValuesForJSON(out.Types, row)
			out.Rows = append(out.Rows, row)
			count++
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out.RowsAffected = int64(len(out.Rows))
	}

	out.ElapsedMs = time.Since(start).Milliseconds()
	out.Tag = "OK"
	return out, nil
}

// mysqlIsQuery 判断 SQL 是否为返回结果集的查询语句。
func mysqlIsQuery(sqlStr string) bool {
	s := strings.TrimSpace(sqlStr)
	// 跳过前导注释
	for {
		if strings.HasPrefix(s, "--") {
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				return false
			}
			s = strings.TrimSpace(s[idx+1:])
			continue
		}
		if strings.HasPrefix(s, "/*") {
			idx := strings.Index(s, "*/")
			if idx < 0 {
				return false
			}
			s = strings.TrimSpace(s[idx+2:])
			continue
		}
		break
	}
	upper := strings.ToUpper(s)
	return strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC ") ||
		strings.HasPrefix(upper, "EXPLAIN") ||
		strings.HasPrefix(upper, "WITH") ||
		strings.HasPrefix(upper, "CALL") ||
		strings.HasPrefix(upper, "TABLE") ||
		strings.HasPrefix(upper, "HELP")
}

// mysqlInvalidatePool closes and removes the cached pool for a connection.
// mysqlInvalidatePool closes and removes all cached MySQL pools for a connection
// (including temporary pools created for different databases via WithDatabase).
func mysqlInvalidatePool(connID uint) {
	mysqlPoolMu.Lock()
	for key, e := range mysqlPools {
		if key.connID == connID {
			e.db.Close()
			delete(mysqlPools, key)
		}
	}
	mysqlPoolMu.Unlock()
}
