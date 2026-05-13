//go:build integration

package dbexec

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ----- L1 contract suite -------------------------------------------------
//
// These tests exercise the Driver interface through Open() only. Anything
// reachable from the interface should behave the same way across drivers,
// modulo Capabilities.SchemaSupport which tells tests which schema to use.
//
// The same test bodies run once per registered driver whose DSN env is set.

type driverCase struct {
	name   string
	setup  func(t *testing.T) (Driver, string /*schema*/, func())
}

func enabledDrivers(t *testing.T) []driverCase {
	t.Helper()
	loadIntegrationConfig(t)
	var cases []driverCase

	if c, skip := pgConnFromEnv(t); !skip {
		d, err := Open(c)
		if err != nil {
			t.Fatalf("Open pg: %v", err)
		}
		cases = append(cases, driverCase{
			name: "postgres",
			setup: func(t *testing.T) (Driver, string, func()) {
				return d, "public", func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					dropFixturesPG(t, ctx, d, "public")
					d.Invalidate()
				}
			},
		})
	}
	if c, skip := mysqlConnFromEnv(t); !skip {
		d, err := Open(c)
		if err != nil {
			t.Fatalf("Open mysql: %v", err)
		}
		cases = append(cases, driverCase{
			name: "mysql",
			setup: func(t *testing.T) (Driver, string, func()) {
				return d, c.Database, func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					dropFixturesMySQL(t, ctx, d, c.Database)
					d.Invalidate()
				}
			},
		})
	}

	if len(cases) == 0 {
		t.Skip("neither PG_TEST_DSN nor MYSQL_TEST_DSN is set; skipping integration suite")
	}
	return cases
}

// withCtx returns a fresh 15s context per sub-test.
func withCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func TestContract_Ping(t *testing.T) {
	for _, tc := range enabledDrivers(t) {
		t.Run(tc.name, func(t *testing.T) {
			d, _, cleanup := tc.setup(t)
			defer cleanup()

			ctx, cancel := withCtx(t)
			defer cancel()
			if err := d.Ping(ctx); err != nil {
				t.Fatalf("Ping: %v", err)
			}
		})
	}
}

func TestContract_Capabilities(t *testing.T) {
	for _, tc := range enabledDrivers(t) {
		t.Run(tc.name, func(t *testing.T) {
			d, _, cleanup := tc.setup(t)
			defer cleanup()

			caps := d.Capabilities()
			if caps.DefaultPort == 0 {
				t.Errorf("Capabilities.DefaultPort should be non-zero")
			}
			if len(caps.SSLModes) == 0 {
				t.Errorf("Capabilities.SSLModes should not be empty")
			}
			if !contains(caps.SSLModes, "disable") {
				t.Errorf("Capabilities.SSLModes should include 'disable', got %v", caps.SSLModes)
			}
			// Both current drivers claim SSH + mTLS. Lock that in as contract
			// so future regressions show up here instead of in prod.
			if !caps.SupportsSSH {
				t.Errorf("Capabilities.SupportsSSH should be true for %s", d.Name())
			}
			if !caps.SupportsMTLS {
				t.Errorf("Capabilities.SupportsMTLS should be true for %s", d.Name())
			}
		})
	}
}

func TestContract_ListDatabases_ContainsCurrent(t *testing.T) {
	for _, tc := range enabledDrivers(t) {
		t.Run(tc.name, func(t *testing.T) {
			d, _, cleanup := tc.setup(t)
			defer cleanup()

			ctx, cancel := withCtx(t)
			defer cancel()
			dbs, err := d.ListDatabases(ctx)
			if err != nil {
				t.Fatalf("ListDatabases: %v", err)
			}
			if len(dbs) == 0 {
				t.Fatalf("ListDatabases returned nothing")
			}
			var found bool
			for _, db := range dbs {
				if db.Current {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no database marked Current=true; dbs=%v", dbs)
			}
		})
	}
}

func TestContract_ListSchemas_NonEmpty(t *testing.T) {
	for _, tc := range enabledDrivers(t) {
		t.Run(tc.name, func(t *testing.T) {
			d, schema, cleanup := tc.setup(t)
			defer cleanup()

			ctx, cancel := withCtx(t)
			defer cancel()
			schemas, err := d.ListSchemas(ctx)
			if err != nil {
				t.Fatalf("ListSchemas: %v", err)
			}
			names := make([]string, 0, len(schemas))
			for _, s := range schemas {
				names = append(names, s.Name)
			}
			if !contains(names, schema) {
				t.Errorf("ListSchemas missing expected schema %q; got %v", schema, names)
			}
		})
	}
}

// TestContract_TablesRoundTrip creates a fixture table, verifies ListTables
// finds it, ListColumns returns the declared columns (with PK + nullability
// flags), and GenerateTableDDL returns a non-empty string. Then drops it.
//
// This is the most important L2 test: it asserts the two drivers surface
// user-visible metadata the same way, which is the whole point of the
// Driver interface.
func TestContract_TablesRoundTrip(t *testing.T) {
	for _, tc := range enabledDrivers(t) {
		t.Run(tc.name, func(t *testing.T) {
			d, schema, cleanup := tc.setup(t)
			defer cleanup()

			ctx, cancel := withCtx(t)
			defer cancel()

			table := "chat2db_it_users"
			createSQL := dialectCreate(d.Name(), schema, table)
			if _, err := d.Exec(ctx, createSQL); err != nil {
				t.Fatalf("create fixture: %v\nSQL=%s", err, createSQL)
			}

			// --- ListTables ---
			tables, err := d.ListTables(ctx, schema)
			if err != nil {
				t.Fatalf("ListTables: %v", err)
			}
			var foundTable *TableInfo
			for i := range tables {
				if tables[i].Name == table {
					foundTable = &tables[i]
					break
				}
			}
			if foundTable == nil {
				names := make([]string, 0, len(tables))
				for _, t := range tables {
					names = append(names, t.Name)
				}
				t.Fatalf("ListTables did not return %q; got %v", table, names)
			}
			if foundTable.Kind != "table" {
				t.Errorf("table.Kind = %q, want table", foundTable.Kind)
			}

			// --- ListColumns ---
			cols, err := d.ListColumns(ctx, schema, table)
			if err != nil {
				t.Fatalf("ListColumns: %v", err)
			}
			colByName := map[string]ColumnInfo{}
			for _, c := range cols {
				colByName[c.Name] = c
			}

			if id, ok := colByName["id"]; !ok {
				t.Errorf("missing column id")
			} else {
				if !id.IsPrimary {
					t.Errorf("column id should be primary key")
				}
				if id.Nullable {
					t.Errorf("column id should be NOT NULL")
				}
			}
			if email, ok := colByName["email"]; !ok {
				t.Errorf("missing column email")
			} else if email.Nullable {
				t.Errorf("column email should be NOT NULL")
			}
			if note, ok := colByName["note"]; !ok {
				t.Errorf("missing column note")
			} else if !note.Nullable {
				t.Errorf("column note should be nullable")
			}

			// --- GenerateTableDDL ---
			ddl, err := d.GenerateTableDDL(ctx, schema, table)
			if err != nil {
				t.Fatalf("GenerateTableDDL: %v", err)
			}
			if strings.TrimSpace(ddl) == "" {
				t.Errorf("GenerateTableDDL returned empty string")
			}
			// Weak but cross-driver assertion: DDL mentions each column name.
			for _, want := range []string{"id", "email", "note"} {
				if !strings.Contains(ddl, want) {
					t.Errorf("DDL missing column %q; ddl=\n%s", want, ddl)
				}
			}
		})
	}
}

// TestContract_WithDatabase_Isolated verifies WithDatabase returns a new
// Driver that scopes metadata lookups to a different database. We don't
// require the target database to exist; we only require the call itself
// to not mutate the original Driver's state.
func TestContract_WithDatabase_Isolated(t *testing.T) {
	for _, tc := range enabledDrivers(t) {
		t.Run(tc.name, func(t *testing.T) {
			d, schema, cleanup := tc.setup(t)
			defer cleanup()

			other := d.WithDatabase("definitely_not_a_real_db_" + tc.name)
			if other == d {
				t.Errorf("WithDatabase returned the same instance")
			}
			if other.Name() != d.Name() {
				t.Errorf("WithDatabase changed driver name")
			}

			// Original should still work after WithDatabase call.
			ctx, cancel := withCtx(t)
			defer cancel()
			if _, err := d.ListTables(ctx, schema); err != nil {
				t.Errorf("original driver broken after WithDatabase: %v", err)
			}
		})
	}
}

// ----- helpers -----------------------------------------------------------

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// dialectCreate returns a CREATE TABLE statement appropriate for the driver.
// Kept intentionally simple: id (PK, not null), email (unique, not null),
// note (nullable). Matches the assertions in TestContract_TablesRoundTrip.
//
// Note: pgQuote in production code produces SQL string literals ('x'); for
// identifier quoting tests use double quotes manually here.
func dialectCreate(driver, schema, table string) string {
	switch driver {
	case "postgres":
		return `CREATE TABLE "` + schema + `"."` + table + `" (
			id SERIAL PRIMARY KEY,
			email VARCHAR(255) NOT NULL UNIQUE,
			note TEXT NULL
		)`
	case "mysql":
		return "CREATE TABLE `" + schema + "`.`" + table + "` (\n" +
			"  id INT AUTO_INCREMENT PRIMARY KEY,\n" +
			"  email VARCHAR(255) NOT NULL UNIQUE,\n" +
			"  note TEXT NULL\n" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	default:
		return ""
	}
}
