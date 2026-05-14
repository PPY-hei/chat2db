package sqlguard

import (
	"strings"
	"testing"

	"github.com/chy/chat2db/server/internal/model"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "SELECT 1", []string{"SELECT 1"}},
		{"two", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"semicolon in string", "SELECT ';not end;'; SELECT 2", []string{"SELECT ';not end;'", "SELECT 2"}},
		{"line comment", "SELECT 1; -- comment;\nSELECT 2", []string{"SELECT 1", "-- comment;\nSELECT 2"}},
		{"block comment", "/* a;b */ SELECT 1;", []string{"/* a;b */ SELECT 1"}},
		{"dollar quoted", "DO $$BEGIN PERFORM 1; END$$;", []string{"DO $$BEGIN PERFORM 1; END$$"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Split(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len(got)=%d want=%d, got=%#v", len(got), len(c.want), got)
			}
			for i, g := range got {
				if strings.TrimSpace(g) != strings.TrimSpace(c.want[i]) {
					t.Errorf("idx %d: got %q want %q", i, g, c.want[i])
				}
			}
		})
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		sql  string
		want Category
	}{
		{"SELECT 1", CatRead},
		{"  -- hi\n SELECT 1", CatRead},
		{"INSERT INTO t VALUES (1)", CatWrite},
		{"UPDATE t SET a=1", CatWrite},
		{"DELETE FROM t", CatWrite},
		{"WITH x AS (SELECT 1) SELECT * FROM x", CatRead},
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", CatWrite},
		{"CREATE TABLE t(id int)", CatDDL},
		{"DROP TABLE t", CatDDL},
		{"BEGIN", CatTx},
		{"COMMIT", CatTx},
		{"GRANT SELECT ON t TO foo", CatAdmin},
		{"EXPLAIN SELECT 1", CatRead},
		{"EXPLAIN ANALYZE SELECT 1", CatAdmin},
	}
	for _, c := range cases {
		got := Classify(c.sql).Category
		if got != c.want {
			t.Errorf("Classify(%q) = %s, want %s", c.sql, got, c.want)
		}
	}
}

func TestCheckAllowed(t *testing.T) {
	type row struct {
		sql  string
		role model.Role
		ok   bool
	}
	rows := []row{
		{"SELECT 1", model.RoleViewer, true},
		{"INSERT INTO t VALUES (1)", model.RoleViewer, false},
		{"UPDATE t SET a=1", model.RoleViewer, false},
		{"DROP TABLE t", model.RoleViewer, false},

		{"SELECT 1; INSERT INTO t VALUES (1)", model.RoleEditor, true},
		{"DROP TABLE t", model.RoleEditor, false},
		{"GRANT SELECT ON t TO foo", model.RoleEditor, false},

		// Admin 在 SQL 层与 Owner 等价：DDL 与 Admin 语句都允许
		{"SELECT 1", model.RoleAdmin, true},
		{"INSERT INTO t VALUES (1)", model.RoleAdmin, true},
		{"CREATE TABLE t(id int)", model.RoleAdmin, true},
		{"ALTER TABLE t ADD COLUMN c int", model.RoleAdmin, true},
		{"DROP TABLE t", model.RoleAdmin, true},
		{"TRUNCATE TABLE t", model.RoleAdmin, true},
		{"GRANT SELECT ON t TO foo", model.RoleAdmin, true},

		{"DROP TABLE t", model.RoleOwner, true},
		{"GRANT SELECT ON t TO foo", model.RoleOwner, true},

		{"SELECT 1; SELECT 2; UPDATE t SET a=1;", model.RoleViewer, false},
	}
	for _, r := range rows {
		err := CheckAllowed(r.sql, r.role)
		if r.ok && err != nil {
			t.Errorf("CheckAllowed(%q,%s) unexpected error: %v", r.sql, r.role, err)
		}
		if !r.ok && err == nil {
			t.Errorf("CheckAllowed(%q,%s) expected denial, got nil", r.sql, r.role)
		}
	}
}
