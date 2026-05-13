// Command sqlite2mysql is a one-off migration tool that copies Chat2DB
// metadata from a SQLite file into a MySQL database whose schema has
// already been provisioned by golang-migrate.
//
// Usage:
//
//	SQLITE_PATH=/path/to/chat2db.db \
//	MYSQL_DSN='user:pwd@tcp(host:3306)/chat2db?parseTime=true&loc=Local&charset=utf8mb4&collation=utf8mb4_bin' \
//	go run ./cmd/sqlite2mysql
//
// Behaviour:
//   - Refuses to run if any target table is non-empty (idempotent guard).
//   - Preserves primary keys and per-row timestamps verbatim.
//   - Runs each table in its own transaction, ordered by FK dependency.
//   - Resets AUTO_INCREMENT to MAX(id)+1 so later inserts do not collide.
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
)

type tableSpec struct {
	name    string
	columns []string
	// mysqlName is the table identifier used in the INSERT statement,
	// needed to quote reserved words like `groups`.
	mysqlName string
}

// Order matters: parents before children, children before their references.
var tables = []tableSpec{
	{
		name:      "users",
		mysqlName: "`users`",
		columns:   []string{"id", "email", "name", "password_hash", "created_at", "updated_at", "deleted_at", "llm_endpoint", "llm_model", "llm_api_key_enc"},
	},
	{
		name:      "groups",
		mysqlName: "`groups`",
		columns:   []string{"id", "name", "description", "owner_id", "created_at", "updated_at", "deleted_at", "share_llm"},
	},
	{
		name:      "group_members",
		mysqlName: "`group_members`",
		columns:   []string{"id", "group_id", "user_id", "role", "created_at", "updated_at"},
	},
	{
		name:      "connections",
		mysqlName: "`connections`",
		columns: []string{
			"id", "group_id", "name", "driver", "host", "port", "database", "username",
			"password_enc", "ssl_mode",
			"sslca_cert_enc", "ssl_client_cert_enc", "ssl_client_key_enc",
			"ssh_enabled", "ssh_host", "ssh_port", "ssh_user", "ssh_auth_method",
			"ssh_password_enc", "ssh_private_key_enc", "ssh_passphrase_enc",
			"created_by_id", "created_at", "updated_at", "deleted_at",
		},
	},
	{
		name:      "saved_queries",
		mysqlName: "`saved_queries`",
		columns:   []string{"id", "group_id", "connection_id", "title", "description", "sql", "created_by_id", "created_at", "updated_at", "deleted_at"},
	},
}

func main() {
	sqlitePath := mustEnv("SQLITE_PATH")
	mysqlDSN := mustEnv("MYSQL_DSN")

	// Force parseTime/loc to avoid scan surprises even if caller forgot.
	cfg, err := gomysql.ParseDSN(mysqlDSN)
	if err != nil {
		log.Fatalf("parse MYSQL_DSN: %v", err)
	}
	cfg.ParseTime = true
	if cfg.Loc == nil {
		cfg.Loc = time.Local
	}

	src, err := sql.Open("sqlite3", sqlitePath+"?_busy_timeout=3000")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer src.Close()
	if err := src.Ping(); err != nil {
		log.Fatalf("ping sqlite: %v", err)
	}

	dst, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer dst.Close()
	if err := dst.Ping(); err != nil {
		log.Fatalf("ping mysql: %v", err)
	}

	for _, t := range tables {
		var n int
		row := dst.QueryRow("SELECT COUNT(*) FROM " + t.mysqlName)
		if err := row.Scan(&n); err != nil {
			log.Fatalf("count %s: %v", t.name, err)
		}
		if n != 0 {
			log.Fatalf("target table %s is not empty (%d rows) — refuse to run", t.name, n)
		}
	}

	for _, t := range tables {
		if err := copyTable(src, dst, t); err != nil {
			log.Fatalf("copy %s: %v", t.name, err)
		}
	}

	for _, t := range tables {
		if err := fixAutoIncrement(dst, t); err != nil {
			log.Fatalf("fix AUTO_INCREMENT for %s: %v", t.name, err)
		}
	}

	log.Println("migration completed")
}

func copyTable(src, dst *sql.DB, t tableSpec) error {
	colList := quoteSQLiteCols(t.columns)
	rows, err := src.Query("SELECT " + colList + " FROM " + t.name)
	if err != nil {
		return fmt.Errorf("sqlite select: %w", err)
	}
	defer rows.Close()

	tx, err := dst.Begin()
	if err != nil {
		return fmt.Errorf("mysql begin: %w", err)
	}
	defer tx.Rollback()

	placeholders := ""
	for i := range t.columns {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}
	insertSQL := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		t.mysqlName, quoteMySQLCols(t.columns), placeholders,
	)
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("mysql prepare: %w", err)
	}
	defer stmt.Close()

	count := 0
	for rows.Next() {
		raw := make([]any, len(t.columns))
		ptrs := make([]any, len(t.columns))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		values := make([]any, len(t.columns))
		for i, v := range raw {
			values[i] = coerce(v, t.columns[i])
		}
		if _, err := stmt.Exec(values...); err != nil {
			return fmt.Errorf("insert row %d: %w", count+1, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite iterate: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mysql commit: %w", err)
	}

	log.Printf("copied %s: %d rows", t.name, count)
	return nil
}

// coerce adapts SQLite scan results to what MySQL wants.
//
//	- Booleans land as int64(0|1) from SQLite numeric; pass through.
//	- NULL stays NULL (target schema allows NULL for deleted_at / soft fields).
//	- Dates arrive as time.Time (parseTime for SQLite done implicitly by mattn driver)
//	  or as RFC3339 strings depending on how they were originally inserted.
func coerce(v any, column string) any {
	if v == nil {
		// Some columns are declared NOT NULL in MySQL; substitute sensible zero
		// values so NULLs from an older SQLite layout don't break the insert.
		switch column {
		case "description",
			"llm_endpoint", "llm_model", "llm_api_key_enc",
			"ssl_mode",
			"sslca_cert_enc", "ssl_client_cert_enc", "ssl_client_key_enc",
			"ssh_host", "ssh_user", "ssh_auth_method",
			"ssh_password_enc", "ssh_private_key_enc", "ssh_passphrase_enc":
			return ""
		case "ssh_port":
			return 22
		case "share_llm", "ssh_enabled":
			return 0
		}
		return nil
	}
	switch b := v.(type) {
	case []byte:
		// SQLite often returns TEXT as []byte; convert to string.
		return string(b)
	}
	return v
}

func fixAutoIncrement(dst *sql.DB, t tableSpec) error {
	var maxID sql.NullInt64
	if err := dst.QueryRow("SELECT MAX(id) FROM " + t.mysqlName).Scan(&maxID); err != nil {
		return err
	}
	if !maxID.Valid {
		return nil
	}
	// MySQL does not accept placeholders for AUTO_INCREMENT; inline the integer.
	next := maxID.Int64 + 1
	_, err := dst.Exec(fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT = %d", t.mysqlName, next))
	if err != nil {
		return err
	}
	log.Printf("set %s AUTO_INCREMENT=%d", t.name, next)
	return nil
}

func quoteSQLiteCols(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ","
		}
		out += "`" + c + "`"
	}
	return out
}

func quoteMySQLCols(cols []string) string {
	return quoteSQLiteCols(cols) // same quoting rules for this table set
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("required env %s not set", k)
	}
	return v
}
