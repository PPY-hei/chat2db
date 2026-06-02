package service

import (
	"errors"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestBuildPGBatchInsertSkipsConflicts(t *testing.T) {
	p := syncTablePair{DestSchema: "public", DestTable: "users"}
	q, args := buildPGBatchInsert(p, `"id", "name"`, [][]any{
		{1, "alice"},
		{2, "bob"},
	}, 2)

	want := `INSERT INTO "public"."users" ("id", "name") VALUES ($1, $2), ($3, $4) ON CONFLICT DO NOTHING`
	if q != want {
		t.Fatalf("unexpected SQL:\nwant %s\ngot  %s", want, q)
	}
	if len(args) != 4 {
		t.Fatalf("want 4 args, got %d", len(args))
	}
}

func TestBuildMySQLBatchInsertDoesNotUseIgnore(t *testing.T) {
	p := syncTablePair{DestDatabase: "app", DestTable: "users"}
	q, args := buildMySQLBatchInsert(p, "`id`, `name`", [][]any{
		{1, "alice"},
		{2, "bob"},
	}, 2)

	want := "INSERT INTO `app`.`users` (`id`, `name`) VALUES (?, ?), (?, ?)"
	if q != want {
		t.Fatalf("unexpected SQL:\nwant %s\ngot  %s", want, q)
	}
	if len(args) != 4 {
		t.Fatalf("want 4 args, got %d", len(args))
	}
	if got := buildMySQLSingleInsert(p, "`id`, `name`", 2); got != "INSERT INTO `app`.`users` (`id`, `name`) VALUES (?, ?)" {
		t.Fatalf("unexpected single insert SQL: %s", got)
	}
}

func TestIsMySQLDuplicateKeyErr(t *testing.T) {
	if !isMySQLDuplicateKeyErr(&mysqldriver.MySQLError{Number: 1062}) {
		t.Fatal("expected duplicate key error")
	}
	if isMySQLDuplicateKeyErr(&mysqldriver.MySQLError{Number: 1048}) {
		t.Fatal("did not expect non-duplicate MySQL error")
	}
	if isMySQLDuplicateKeyErr(errors.New("Duplicate entry '1' for key 'PRIMARY'")) {
		t.Fatal("plain string errors should not be treated as duplicate key errors")
	}
}
