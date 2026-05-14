package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Supported metadata database drivers.
const (
	MetaDriverSQLite   = "sqlite"
	MetaDriverPostgres = "postgres"
	MetaDriverMySQL    = "mysql"
)

// Default values that are explicitly considered "development placeholders".
// In release mode these MUST be overridden via environment variables.
const (
	defaultJWTSecret     = "please-change-me-in-production"
	defaultCredentialKey = "0123456789abcdef0123456789abcdef"
)

type Config struct {
	ServerAddr string
	ServerMode string

	// Metadata database (the app's own DB: users, groups, connections, ...).
	MetaDBDriver          string
	MetaDBDSN             string
	MetaDBPath            string // deprecated: use MetaDBDSN; kept for sqlite backwards compatibility
	MetaDBMaxOpenConns    int
	MetaDBMaxIdleConns    int
	MetaDBConnMaxLifetime time.Duration
	// MetaDBAutoMigrate controls whether to run migrations on startup.
	// - sqlite: defaults to true (uses gorm AutoMigrate).
	// - postgres/mysql: defaults to true (runs golang-migrate up).
	//   Set META_DB_AUTO_MIGRATE=false to skip in production where migrations
	//   are applied out-of-band; startup will then only verify schema presence.
	MetaDBAutoMigrate bool

	JWTSecret      string
	JWTExpireHours int
	CredentialKey  []byte

	QueryMaxRows    int
	QueryTimeoutSec int

	// AuditRetention 审计日志保留时长。<= 0 表示永不清理（仅推荐测试环境）。
	// 默认 90 天，对应 env AUDIT_RETENTION=2160h。
	AuditRetention time.Duration
}

var cfg *Config

func Load() *Config {
	if cfg != nil {
		return cfg
	}
	c := &Config{
		ServerAddr:            getEnv("SERVER_ADDR", ":8080"),
		ServerMode:            getEnv("SERVER_MODE", "debug"),
		MetaDBDriver:          strings.ToLower(getEnv("META_DB_DRIVER", MetaDriverSQLite)),
		MetaDBDSN:             getEnv("META_DB_DSN", ""),
		MetaDBPath:            getEnv("META_DB_PATH", "./data/chat2db.db"),
		MetaDBMaxOpenConns:    getEnvInt("META_DB_MAX_OPEN_CONNS", 20),
		MetaDBMaxIdleConns:    getEnvInt("META_DB_MAX_IDLE_CONNS", 5),
		MetaDBConnMaxLifetime: getEnvDuration("META_DB_CONN_MAX_LIFETIME", time.Hour),
		MetaDBAutoMigrate:     getEnvBool("META_DB_AUTO_MIGRATE", true),
		JWTSecret:             getEnv("JWT_SECRET", defaultJWTSecret),
		JWTExpireHours:        getEnvInt("JWT_EXPIRE_HOURS", 72),
		QueryMaxRows:          getEnvInt("QUERY_MAX_ROWS", 1000),
		QueryTimeoutSec:       getEnvInt("QUERY_TIMEOUT_SECONDS", 30),
		AuditRetention:        getEnvDuration("AUDIT_RETENTION", 90*24*time.Hour),
	}

	keyStr := getEnv("CREDENTIAL_KEY", defaultCredentialKey)
	if len(keyStr) < 32 {
		log.Fatalf("CREDENTIAL_KEY must be at least 32 bytes, got %d", len(keyStr))
	}
	c.CredentialKey = []byte(keyStr[:32])

	// Validate driver and resolve DSN defaults / backwards compatibility.
	switch c.MetaDBDriver {
	case MetaDriverSQLite:
		// Backwards compatibility: META_DB_DSN > META_DB_PATH > default.
		if c.MetaDBDSN == "" {
			c.MetaDBDSN = c.MetaDBPath
		}
	case MetaDriverPostgres, MetaDriverMySQL:
		if c.MetaDBDSN == "" {
			log.Fatalf("META_DB_DSN is required when META_DB_DRIVER=%s", c.MetaDBDriver)
		}
	default:
		log.Fatalf("unsupported META_DB_DRIVER %q (expected sqlite|postgres|mysql)", c.MetaDBDriver)
	}

	// Release mode: refuse to start with development placeholder secrets.
	if strings.EqualFold(c.ServerMode, "release") {
		if c.JWTSecret == defaultJWTSecret {
			log.Fatalf("refusing to start in release mode: JWT_SECRET must be overridden")
		}
		if keyStr == defaultCredentialKey {
			log.Fatalf("refusing to start in release mode: CREDENTIAL_KEY must be overridden")
		}
	}

	cfg = c
	return cfg
}

func Get() *Config {
	if cfg == nil {
		return Load()
	}
	return cfg
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
