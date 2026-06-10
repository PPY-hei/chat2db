package service

import (
	"errors"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgtype"
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
	gotSingle, gotSingleArgs := buildMySQLSingleInsert(p, "`id`, `name`", []any{1, "alice"}, 2)
	if gotSingle != "INSERT INTO `app`.`users` (`id`, `name`) VALUES (?, ?)" {
		t.Fatalf("unexpected single insert SQL: %s", gotSingle)
	}
	if len(gotSingleArgs) != 2 {
		t.Fatalf("want 2 single insert args, got %d", len(gotSingleArgs))
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

func TestNormalizeExportWhereCondition(t *testing.T) {
	got, err := normalizeExportWhereCondition(" WHERE tenant_id = 1001 AND deleted_at IS NULL ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant_id = 1001 AND deleted_at IS NULL" {
		t.Fatalf("unexpected where: %q", got)
	}

	for _, raw := range []string{
		"id = 1; DROP TABLE users",
		"id = 1 -- hide rest",
		"id = 1 /* hide rest */",
		"sn IN ('WA3302551)",
	} {
		if _, err := normalizeExportWhereCondition(raw); err == nil {
			t.Fatalf("expected invalid where for %q", raw)
		}
	}
}

func TestNormalizeExportWhereConditionAllowsLongINList(t *testing.T) {
	values := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		values = append(values, "'WA2002614003B622382'")
	}
	raw := "sn IN (" + strings.Join(values, ",") + ")"
	got, err := normalizeExportWhereCondition(raw)
	if err != nil {
		t.Fatalf("unexpected error for %d byte where: %v", len(raw), err)
	}
	if got != raw {
		t.Fatalf("where was changed")
	}
}

func TestParseExportTaskOptionsDefaultsToCSV(t *testing.T) {
	opts, err := parseExportTaskOptions("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Format != exportFormatCSV {
		t.Fatalf("format=%q want %q", opts.Format, exportFormatCSV)
	}
}

func TestParseExportTaskOptionsKeepsValueReplacements(t *testing.T) {
	raw, err := buildExportTaskParams(exportFormatInsertSQL, "", false, []ExportValueReplacement{
		{
			Column:    "shop_id",
			Mapping:   map[string]string{"old": "new"},
			OnMissing: exportReplacementOnMissingEmpty,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts, err := parseExportTaskOptions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opts.ValueReplacements) != 1 {
		t.Fatalf("expected one replacement rule, got %d", len(opts.ValueReplacements))
	}
	rule := opts.ValueReplacements[0]
	if rule.Column != "shop_id" || rule.Mapping["old"] != "new" || rule.OnMissing != exportReplacementOnMissingEmpty {
		t.Fatalf("unexpected replacement rule: %#v", rule)
	}
}

func TestParseDataSyncTaskOptionsKeepsWhereAndValueReplacements(t *testing.T) {
	raw, err := buildDataSyncTaskParams(" tenant_id = 1001 ", []ExportValueReplacement{
		{
			Column:    "shop_id",
			Mapping:   map[string]string{"old": "new"},
			OnMissing: exportReplacementOnMissingEmpty,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == "" {
		t.Fatal("expected non-empty data sync params")
	}

	opts, err := parseDataSyncTaskOptions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Where != "tenant_id = 1001" {
		t.Fatalf("unexpected where: %q", opts.Where)
	}
	if len(opts.ValueReplacements) != 1 {
		t.Fatalf("expected one replacement rule, got %d", len(opts.ValueReplacements))
	}
	rule := opts.ValueReplacements[0]
	if rule.Column != "shop_id" || rule.Mapping["old"] != "new" || rule.OnMissing != exportReplacementOnMissingEmpty {
		t.Fatalf("unexpected replacement rule: %#v", rule)
	}
}

func TestParseBackupTaskOptionsKeepsBackupTable(t *testing.T) {
	raw, err := buildBackupTaskParams(" users_bak ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts, err := parseBackupTaskOptions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.BackupTable != "users_bak" {
		t.Fatalf("backup_table=%q want users_bak", opts.BackupTable)
	}
}

func TestBuildExportSelectWithWhere(t *testing.T) {
	et := exportTable{Database: "app", Schema: "public", Table: "users"}
	got := buildPGExportSelect(et, "tenant_id = 1001")
	want := `SELECT * FROM "public"."users" WHERE tenant_id = 1001`
	if got != want {
		t.Fatalf("unexpected select:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildBackupSQL(t *testing.T) {
	pgCreate, pgInsert := buildPGBackupSQL(`pub"lic`, "users", "users_bak")
	if pgCreate != `CREATE TABLE "pub""lic"."users_bak" (LIKE "pub""lic"."users" INCLUDING ALL)` {
		t.Fatalf("unexpected pg create: %s", pgCreate)
	}
	if pgInsert != `INSERT INTO "pub""lic"."users_bak" OVERRIDING SYSTEM VALUE SELECT * FROM "pub""lic"."users"` {
		t.Fatalf("unexpected pg insert: %s", pgInsert)
	}

	myCreate, myInsert := buildMySQLBackupSQL("app`db", "users", "users_bak")
	if myCreate != "CREATE TABLE `app``db`.`users_bak` LIKE `app``db`.`users`" {
		t.Fatalf("unexpected mysql create: %s", myCreate)
	}
	if myInsert != "INSERT INTO `app``db`.`users_bak` SELECT * FROM `app``db`.`users`" {
		t.Fatalf("unexpected mysql insert: %s", myInsert)
	}
}

func TestApplyExportValueReplacementsMapsConfiguredColumns(t *testing.T) {
	et := exportTable{Database: "projection", Schema: "public", Table: "orders"}
	cols := []string{"id", "shop_id", "company_id"}
	vals := []any{1, "shop-old", "company-old"}
	replaced := applyExportValueReplacements(cols, vals, nil, []ExportValueReplacement{
		{
			Column:    "shop_id",
			Mapping:   map[string]string{"shop-old": "shop-new"},
			OnMissing: exportReplacementOnMissingKeep,
		},
		{
			Column:    "company_id",
			Mapping:   map[string]string{"company-old": "company-new"},
			OnMissing: exportReplacementOnMissingKeep,
		},
	})
	got, err := buildExportInsertSQL("postgres", et, cols, replaced, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `INSERT INTO "public"."orders" ("id", "shop_id", "company_id") VALUES (1, 'shop-new', 'company-new') ON CONFLICT DO NOTHING;`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
	if vals[1] != "shop-old" {
		t.Fatalf("original vals should not be mutated: %#v", vals)
	}
}

func TestApplyExportValueReplacementsCastsMappedValuesByColumnType(t *testing.T) {
	cols := []string{"supplier_id", "enabled"}
	vals := []any{"old_supplier", "no"}
	replaced := applyExportValueReplacements(cols, vals, []exportColumnInfo{
		{DataType: "bigint"},
		{DataType: "boolean"},
	}, []ExportValueReplacement{
		{
			Column:    "supplier_id",
			Mapping:   map[string]string{"old_supplier": "123"},
			OnMissing: exportReplacementOnMissingKeep,
		},
		{
			Column:    "enabled",
			Mapping:   map[string]string{"no": "true"},
			OnMissing: exportReplacementOnMissingKeep,
		},
	})
	if replaced[0] != int64(123) {
		t.Fatalf("expected mapped bigint to become int64, got %#v", replaced[0])
	}
	if replaced[1] != true {
		t.Fatalf("expected mapped boolean to become true, got %#v", replaced[1])
	}
}

func TestApplyExportValueReplacementsCanEmptyUnmatchedValue(t *testing.T) {
	cols := []string{"id", "shop_id"}
	vals := []any{1, "shop-old"}
	replaced := applyExportValueReplacements(cols, vals, []exportColumnInfo{
		{DataType: "bigint"},
		{DataType: "bigint"},
	}, []ExportValueReplacement{
		{
			Column:    "shop_id",
			Mapping:   map[string]string{"other": "shop-new"},
			OnMissing: exportReplacementOnMissingEmpty,
		},
	})
	if replaced[1] != int64(0) {
		t.Fatalf("expected unmatched bigint to become zero value, got %#v", replaced[1])
	}
}

func TestApplyExportValueReplacementsUsesNullForNullableUnmatchedValue(t *testing.T) {
	cols := []string{"id", "supplier_id"}
	vals := []any{1, "whale"}
	replaced := applyExportValueReplacements(cols, vals, []exportColumnInfo{
		{DataType: "bigint"},
		{DataType: "bigint", Nullable: true},
	}, []ExportValueReplacement{
		{
			Column:    "supplier_id",
			Mapping:   map[string]string{"other": "1"},
			OnMissing: exportReplacementOnMissingEmpty,
		},
	})
	if replaced[1] != nil {
		t.Fatalf("expected nullable unmatched value to become NULL, got %#v", replaced[1])
	}
}

func TestApplyExportValueReplacementsUsesDefaultForDefaultedUnmatchedValue(t *testing.T) {
	cols := []string{"id", "sequence"}
	vals := []any{1, "missing"}
	replaced := applyExportValueReplacements(cols, vals, []exportColumnInfo{
		{DataType: "bigint"},
		{DataType: "bigint", HasDefault: true, DefaultValue: "0"},
	}, []ExportValueReplacement{
		{
			Column:    "sequence",
			Mapping:   map[string]string{"other": "1"},
			OnMissing: exportReplacementOnMissingEmpty,
		},
	})
	if _, ok := replaced[1].(exportSQLDefault); !ok {
		t.Fatalf("expected defaulted unmatched value to become DEFAULT sentinel, got %#v", replaced[1])
	}
	got, err := buildExportInsertSQL("postgres", exportTable{Schema: "public", Table: "mango_device"}, cols, replaced, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `INSERT INTO "public"."mango_device" ("id", "sequence") VALUES (1, DEFAULT);`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildPGBatchInsertSupportsDefaultSentinel(t *testing.T) {
	p := syncTablePair{DestSchema: "public", DestTable: "users"}
	q, args := buildPGBatchInsert(p, `"id", "sequence"`, [][]any{
		{1, exportSQLDefault{}},
		{2, int64(10)},
	}, 2)

	want := `INSERT INTO "public"."users" ("id", "sequence") VALUES ($1, DEFAULT), ($2, $3) ON CONFLICT DO NOTHING`
	if q != want {
		t.Fatalf("unexpected SQL:\nwant %s\ngot  %s", want, q)
	}
	if len(args) != 3 {
		t.Fatalf("want 3 args, got %d", len(args))
	}
}

func TestBuildMySQLBatchInsertSupportsDefaultSentinel(t *testing.T) {
	p := syncTablePair{DestDatabase: "app", DestTable: "users"}
	q, args := buildMySQLBatchInsert(p, "`id`, `sequence`", [][]any{
		{1, exportSQLDefault{}},
		{2, int64(10)},
	}, 2)

	want := "INSERT INTO `app`.`users` (`id`, `sequence`) VALUES (?, DEFAULT), (?, ?)"
	if q != want {
		t.Fatalf("unexpected SQL:\nwant %s\ngot  %s", want, q)
	}
	if len(args) != 3 {
		t.Fatalf("want 3 args, got %d", len(args))
	}

	singleQ, singleArgs := buildMySQLSingleInsert(p, "`id`, `sequence`", []any{1, exportSQLDefault{}}, 2)
	if singleQ != "INSERT INTO `app`.`users` (`id`, `sequence`) VALUES (?, DEFAULT)" {
		t.Fatalf("unexpected single insert SQL: %s", singleQ)
	}
	if len(singleArgs) != 1 {
		t.Fatalf("want 1 single insert arg, got %d", len(singleArgs))
	}
}

func TestBuildSyncArtifactInsertSQLPostgres(t *testing.T) {
	et := exportTable{Database: "app", Schema: "public", Table: "devices"}
	got, err := buildSyncArtifactInsertSQL("postgres", et,
		[]string{"id", "meta", "name"},
		[]any{"57f6f812-871c-4813-9e9f-27bd36c696d7", map[string]any{}, "camera"},
		[]exportColumnInfo{
			{DataType: "uuid"},
			{DataType: "jsonb", DefaultValue: "'[]'::jsonb"},
			{DataType: "text"},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `INSERT INTO "public"."devices" ("id", "meta", "name") VALUES ('57f6f812-871c-4813-9e9f-27bd36c696d7'::uuid, '[]'::jsonb, 'camera') ON CONFLICT DO NOTHING;`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildSyncArtifactInsertSQLMySQL(t *testing.T) {
	et := exportTable{Database: "app", Schema: "app", Table: "users"}
	got, err := buildSyncArtifactInsertSQL("mysql", et,
		[]string{"id", "name"},
		[]any{1, "bob"},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "INSERT IGNORE INTO `app`.`users` (`id`, `name`) VALUES (1, 'bob');"
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildExportInsertSQLPostgresWithConflict(t *testing.T) {
	et := exportTable{Database: "app", Schema: "public", Table: "users"}
	got, err := buildExportInsertSQL("postgres", et,
		[]string{"id", "name", "note", "active"},
		[]any{1, "O'Reilly", nil, true},
		true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `INSERT INTO "public"."users" ("id", "name", "note", "active") VALUES (1, 'O''Reilly', NULL, TRUE) ON CONFLICT DO NOTHING;`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildExportInsertSQLPostgresArrayLiteral(t *testing.T) {
	et := exportTable{Database: "projection", Schema: "public", Table: "ptop_config"}
	got, err := buildExportInsertSQL("postgres", et,
		[]string{"id", "hk_channel_id"},
		[]any{1, []int64{1, 2, 3, 4, 5}},
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `INSERT INTO "public"."ptop_config" ("id", "hk_channel_id") VALUES (1, ARRAY[1, 2, 3, 4, 5]);`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildExportInsertSQLPostgresUUIDLiteral(t *testing.T) {
	et := exportTable{Database: "mango_production", Schema: "public", Table: "mango_device"}
	got, err := buildExportInsertSQLWithPGTypes(et,
		[]string{"id", "sn"},
		[]any{[16]byte{87, 246, 248, 18, 135, 28, 72, 19, 158, 159, 39, 189, 54, 198, 150, 215}, "WMT202614003B60ED3E"},
		[]uint32{pgtype.UUIDOID, pgtype.TextOID},
		true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `INSERT INTO "public"."mango_device" ("id", "sn") VALUES ('57f6f812-871c-4813-9e9f-27bd36c696d7'::uuid, 'WMT202614003B60ED3E') ON CONFLICT DO NOTHING;`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildExportInsertSQLPostgresJSONLiteral(t *testing.T) {
	et := exportTable{Database: "stream", Schema: "public", Table: "stream_ability"}
	got, err := buildExportInsertSQLWithPGTypes(et,
		[]string{"id", "sp_config"},
		[]any{86638, map[string]any{}},
		[]uint32{pgtype.Int8OID, pgtype.JSONBOID},
		true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `INSERT INTO "public"."stream_ability" ("id", "sp_config") VALUES (86638, '{}'::jsonb) ON CONFLICT DO NOTHING;`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildExportInsertSQLPostgresJSONLiteralUsesArrayDefault(t *testing.T) {
	et := exportTable{Database: "stream", Schema: "public", Table: "stream_ability"}
	got, err := buildExportInsertSQLWithPGColumnInfo(et,
		[]string{"id", "sp_config"},
		[]any{86638, map[string]any{}},
		[]pgExportColumnInfo{
			{TypeOID: pgtype.Int8OID},
			{TypeOID: pgtype.JSONBOID, DefaultValue: "'[]'::jsonb"},
		},
		true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `INSERT INTO "public"."stream_ability" ("id", "sp_config") VALUES (86638, '[]'::jsonb) ON CONFLICT DO NOTHING;`
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}

func TestBuildExportInsertSQLMySQLIgnore(t *testing.T) {
	et := exportTable{Database: "app", Schema: "app", Table: "users"}
	got, err := buildExportInsertSQL("mysql", et,
		[]string{"id", "name"},
		[]any{1, "bob"},
		true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "INSERT IGNORE INTO `app`.`users` (`id`, `name`) VALUES (1, 'bob');"
	if got != want {
		t.Fatalf("unexpected insert:\nwant %s\ngot  %s", want, got)
	}
}
