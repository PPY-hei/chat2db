package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	ServerAddr      string
	ServerMode      string
	MetaDBPath      string
	JWTSecret       string
	JWTExpireHours  int
	CredentialKey   []byte
	QueryMaxRows    int
	QueryTimeoutSec int
}

var cfg *Config

func Load() *Config {
	if cfg != nil {
		return cfg
	}
	c := &Config{
		ServerAddr:      getEnv("SERVER_ADDR", ":8080"),
		ServerMode:      getEnv("SERVER_MODE", "debug"),
		MetaDBPath:      getEnv("META_DB_PATH", "./data/chat2db.db"),
		JWTSecret:       getEnv("JWT_SECRET", "please-change-me-in-production"),
		JWTExpireHours:  getEnvInt("JWT_EXPIRE_HOURS", 72),
		QueryMaxRows:    getEnvInt("QUERY_MAX_ROWS", 1000),
		QueryTimeoutSec: getEnvInt("QUERY_TIMEOUT_SECONDS", 30),
	}
	keyStr := getEnv("CREDENTIAL_KEY", "0123456789abcdef0123456789abcdef")
	if len(keyStr) < 32 {
		log.Fatalf("CREDENTIAL_KEY must be at least 32 bytes, got %d", len(keyStr))
	}
	c.CredentialKey = []byte(keyStr[:32])
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
