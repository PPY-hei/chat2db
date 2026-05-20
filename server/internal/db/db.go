package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// migrationsFS embeds the golang-migrate files so the binary is self-contained.
//
//go:embed migrations/postgres/*.sql migrations/mysql/*.sql
var migrationsFS embed.FS

var metaDB *gorm.DB

// Init opens (and migrates) the metadata database used by the app.
//
// Driver is selected via META_DB_DRIVER (sqlite | postgres | mysql).
//   - sqlite  : GORM AutoMigrate (dev-friendly, zero config).
//   - postgres/mysql : golang-migrate runs embedded SQL under migrations/<driver>/.
//     Can be disabled by META_DB_AUTO_MIGRATE=false, in which case Init only
//     verifies that the expected tables already exist.
func Init() (*gorm.DB, error) {
	cfg := config.Get()
	dialector, err := buildDialector(cfg)
	if err != nil {
		return nil, err
	}
	g, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := g.DB()
	if err != nil {
		return nil, err
	}
	applyPoolConfig(sqlDB, cfg)

	if err := migrateOrVerify(g, cfg); err != nil {
		return nil, err
	}

	metaDB = g
	return g, nil
}

// Meta returns the shared metadata DB handle. Must be called after Init.
func Meta() *gorm.DB { return metaDB }

// buildDialector maps META_DB_DRIVER to the appropriate GORM dialector.
// Also prepares the SQLite parent directory when needed.
func buildDialector(cfg *config.Config) (gorm.Dialector, error) {
	switch cfg.MetaDBDriver {
	case config.MetaDriverSQLite:
		if err := os.MkdirAll(filepath.Dir(cfg.MetaDBDSN), 0o755); err != nil {
			return nil, fmt.Errorf("create sqlite dir: %w", err)
		}
		return sqlite.Open(cfg.MetaDBDSN), nil
	case config.MetaDriverPostgres:
		return postgres.Open(cfg.MetaDBDSN), nil
	case config.MetaDriverMySQL:
		return mysql.Open(cfg.MetaDBDSN), nil
	default:
		return nil, fmt.Errorf("unsupported driver %q", cfg.MetaDBDriver)
	}
}

// applyPoolConfig configures *sql.DB according to env-driven limits.
// SQLite largely ignores these, which is fine.
func applyPoolConfig(db *sql.DB, cfg *config.Config) {
	if cfg.MetaDBMaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MetaDBMaxOpenConns)
	}
	if cfg.MetaDBMaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MetaDBMaxIdleConns)
	}
	if cfg.MetaDBConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.MetaDBConnMaxLifetime)
	}
}

// migrateOrVerify runs the driver-appropriate migration path.
func migrateOrVerify(g *gorm.DB, cfg *config.Config) error {
	switch cfg.MetaDBDriver {
	case config.MetaDriverSQLite:
		return g.AutoMigrate(metaModels()...)
	case config.MetaDriverPostgres, config.MetaDriverMySQL:
		if cfg.MetaDBAutoMigrate {
			return runSQLMigrations(cfg.MetaDBDriver, cfg.MetaDBDSN)
		}
		return verifySchema(g)
	default:
		return fmt.Errorf("unsupported driver %q", cfg.MetaDBDriver)
	}
}

func metaModels() []any {
	return []any{
		&model.User{},
		&model.Group{},
		&model.GroupMember{},
		&model.Connection{},
		&model.SavedQuery{},
		&model.AuditLog{},
		&model.Task{},
	}
}

// verifySchema is used when META_DB_AUTO_MIGRATE=false: we require that the
// DBA has already applied the expected schema out-of-band.
func verifySchema(g *gorm.DB) error {
	mig := g.Migrator()
	var missing []string
	for _, m := range metaModels() {
		if !mig.HasTable(m) {
			// Derive the table name via GORM's statement for a clearer error.
			stmt := &gorm.Statement{DB: g}
			_ = stmt.Parse(m)
			missing = append(missing, stmt.Schema.Table)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("meta db schema verification failed, missing tables: %s; "+
			"run migrations or set META_DB_AUTO_MIGRATE=true",
			strings.Join(missing, ", "))
	}
	return nil
}

// --- migration helpers kept separate from public surface ---

// migrationsSubFS returns an fs.FS rooted at the driver-specific folder.
func migrationsSubFS(driver string) (fs.FS, error) {
	sub, err := fs.Sub(migrationsFS, "migrations/"+driver)
	if err != nil {
		return nil, fmt.Errorf("locate migrations for %s: %w", driver, err)
	}
	// Guard against an empty dir which would silently no-op.
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no migration files found")
	}
	return sub, nil
}
