//go:build integration

// Package dbexec integration tests live behind the `integration` build tag.
//
// They are skipped by default (`go test ./...`). To run them, point
// PG_TEST_DSN / MYSQL_TEST_DSN at running databases and pass the tag:
//
//	docker compose --profile test up -d
//	PG_TEST_DSN="postgres://chat2db:chat2db@127.0.0.1:5433/chat2db?sslmode=disable" \
//	MYSQL_TEST_DSN="chat2db:chat2db@tcp(127.0.0.1:3307)/chat2db?parseTime=true&loc=Local&charset=utf8mb4" \
//	go test -tags=integration ./internal/dbexec/... -count=1 -v
//
// Either DSN may be left empty; the corresponding driver suite then skips.
//
// Tests share a single `*model.Connection` per driver. They write/drop
// schema-scoped fixture tables with names beginning `chat2db_it_` so they
// won't collide with developer data even if pointed at a non-disposable DB.

package dbexec

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// integrationKey is a fixed 32-byte CredentialKey used to encrypt the test
// connection's password. It is intentionally hard-coded — it never leaves
// the test process and is cleared from process state via os.Setenv before
// any production code reads config.Get().
const integrationKey = "0123456789abcdef0123456789abcdef"

// loadIntegrationConfig forces config.CredentialKey to a known value so we
// can encrypt+decrypt the test password without depending on the developer's
// env. It is safe to call repeatedly.
func loadIntegrationConfig(t *testing.T) {
	t.Helper()
	// We can't reach the unexported `cfg` cache directly, but config.Load
	// reads CREDENTIAL_KEY before caching, so setting it before the first
	// call wins. Subsequent calls return the cached config — which is fine
	// because all integration tests run in one process and want the same key.
	if err := os.Setenv("CREDENTIAL_KEY", integrationKey); err != nil {
		t.Fatalf("setenv CREDENTIAL_KEY: %v", err)
	}
	// QUERY_TIMEOUT_SECONDS is read by Exec — make it generous for slow CI.
	if os.Getenv("QUERY_TIMEOUT_SECONDS") == "" {
		_ = os.Setenv("QUERY_TIMEOUT_SECONDS", "30")
	}
	if os.Getenv("QUERY_MAX_ROWS") == "" {
		_ = os.Setenv("QUERY_MAX_ROWS", "1000")
	}
	_ = config.Load()
}

// encryptForTest encrypts a plaintext password under the integration key.
func encryptForTest(t *testing.T, plaintext string) string {
	t.Helper()
	enc, err := cryptopkg.EncryptString(plaintext, []byte(integrationKey))
	if err != nil {
		t.Fatalf("encrypt test password: %v", err)
	}
	return enc
}

// pgConnFromEnv parses PG_TEST_DSN into a *model.Connection.
// Returns (nil, true) when PG_TEST_DSN is unset — caller should skip.
func pgConnFromEnv(t *testing.T) (*model.Connection, bool) {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		return nil, true
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("PG_TEST_DSN parse: %v", err)
	}
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 5432
	}
	pwd, _ := u.User.Password()
	dbname := strings.TrimPrefix(u.Path, "/")
	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}
	return &model.Connection{
		ID:          1001,
		UpdatedAt:   time.Now(),
		Driver:      "postgres",
		Host:        host,
		Port:        port,
		Username:    u.User.Username(),
		PasswordEnc: encryptForTest(t, pwd),
		Database:    dbname,
		SSLMode:     sslMode,
	}, false
}

// mysqlConnFromEnv parses MYSQL_TEST_DSN into a *model.Connection.
// Returns (nil, true) when MYSQL_TEST_DSN is unset — caller should skip.
func mysqlConnFromEnv(t *testing.T) (*model.Connection, bool) {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		return nil, true
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("MYSQL_TEST_DSN parse: %v", err)
	}
	host, portStr, err := splitHostPort(cfg.Addr)
	if err != nil {
		t.Fatalf("MYSQL_TEST_DSN addr: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 3306
	}
	return &model.Connection{
		ID:          1002,
		UpdatedAt:   time.Now(),
		Driver:      "mysql",
		Host:        host,
		Port:        port,
		Username:    cfg.User,
		PasswordEnc: encryptForTest(t, cfg.Passwd),
		Database:    cfg.DBName,
		SSLMode:     "disable",
	}, false
}

// splitHostPort handles "host:port", returning ("host","port",nil).
// Falls back to the original string + "0" when there is no colon.
func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "0", nil
	}
	host, port := addr[:i], addr[i+1:]
	if host == "" || port == "" {
		return "", "", fmt.Errorf("invalid addr %q", addr)
	}
	return host, port, nil
}

// dropFixturesPG drops every chat2db_it_* table created by the suite.
func dropFixturesPG(t *testing.T, ctx context.Context, d Driver, schema string) {
	t.Helper()
	tables, err := d.ListTables(ctx, schema)
	if err != nil {
		t.Logf("dropFixturesPG ListTables: %v", err)
		return
	}
	for _, tbl := range tables {
		if strings.HasPrefix(tbl.Name, "chat2db_it_") {
			// PG identifier quoting uses double quotes, not pgQuote (which
			// produces SQL string literals).
			_, err := d.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"."%s"`, schema, tbl.Name))
			if err != nil {
				t.Logf("drop %s.%s: %v", schema, tbl.Name, err)
			}
		}
	}
}

// dropFixturesMySQL drops every chat2db_it_* table on the given (database) schema.
func dropFixturesMySQL(t *testing.T, ctx context.Context, d Driver, schema string) {
	t.Helper()
	tables, err := d.ListTables(ctx, schema)
	if err != nil {
		t.Logf("dropFixturesMySQL ListTables: %v", err)
		return
	}
	for _, tbl := range tables {
		if strings.HasPrefix(tbl.Name, "chat2db_it_") {
			_, err := d.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`.`%s`", schema, tbl.Name))
			if err != nil {
				t.Logf("drop %s.%s: %v", schema, tbl.Name, err)
			}
		}
	}
}
