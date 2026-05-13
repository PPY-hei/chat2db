package db

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/chy/chat2db/server/internal/config"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// runSQLMigrations applies embedded SQL migrations using golang-migrate.
//
// A *separate* *sql.DB is opened just for the migration run so that:
//   - MySQL can set multiStatements=true without leaking that flag to the
//     business GORM pool (reducing the blast radius of any future SQLi bug).
//   - The migration source can safely close its driver without tearing down
//     the main pool.
//
// The returned error wraps the underlying migrate/driver error.
func runSQLMigrations(driver, dsn string) error {
	srcFS, err := migrationsSubFS(driver)
	if err != nil {
		return err
	}
	src, err := iofs.New(srcFS, ".")
	if err != nil {
		return fmt.Errorf("init migration source: %w", err)
	}
	defer src.Close()

	migDB, err := openMigrationDB(driver, dsn)
	if err != nil {
		return err
	}
	defer migDB.Close()

	dbDriver, err := buildMigrateDriver(driver, migDB)
	if err != nil {
		return err
	}
	// dbDriver.Close() would also close migDB; we want to control that via
	// migDB's own defer above, so skip closing the driver explicitly.

	m, err := migrate.NewWithInstance("iofs", src, driver, dbDriver)
	if err != nil {
		return fmt.Errorf("build migrator: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// openMigrationDB opens a short-lived *sql.DB for migrations only.
// For MySQL, multiStatements=true is force-enabled on the DSN so that
// migration files containing multiple CREATE TABLE statements execute.
func openMigrationDB(driver, dsn string) (*sql.DB, error) {
	switch driver {
	case config.MetaDriverPostgres:
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			return db, nil
		}
		// Fall back to lib/pq if pgx stdlib is not registered in this build.
		return sql.Open("postgres", dsn)
	case config.MetaDriverMySQL:
		cfg, err := gomysql.ParseDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("parse mysql dsn: %w", err)
		}
		cfg.MultiStatements = true
		return sql.Open("mysql", cfg.FormatDSN())
	default:
		return nil, fmt.Errorf("openMigrationDB: unsupported driver %q", driver)
	}
}

func buildMigrateDriver(driver string, db *sql.DB) (database.Driver, error) {
	switch driver {
	case config.MetaDriverPostgres:
		d, err := migratepostgres.WithInstance(db, &migratepostgres.Config{})
		if err != nil {
			return nil, fmt.Errorf("init postgres migrator: %w", err)
		}
		return d, nil
	case config.MetaDriverMySQL:
		d, err := migratemysql.WithInstance(db, &migratemysql.Config{})
		if err != nil {
			return nil, fmt.Errorf("init mysql migrator: %w", err)
		}
		return d, nil
	}
	return nil, fmt.Errorf("buildMigrateDriver: unsupported driver %q", driver)
}
