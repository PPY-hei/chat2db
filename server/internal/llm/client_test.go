package llm

import "testing"

func TestRewritePostgresSequenceSetval(t *testing.T) {
	in := "SELECT setval(pg_get_serial_sequence('public.mango_casbin_role', 'id'), 5000, false);"
	got := rewritePostgresSequenceSetval(in)
	want := "SELECT setval('mango_casbin_role_id_seq', 5000);"
	if got != want {
		t.Fatalf("unexpected SQL:\nwant %s\ngot  %s", want, got)
	}
}

func TestNormalizeGeneratedSQLOnlyForPostgres(t *testing.T) {
	in := "SELECT setval(pg_get_serial_sequence('public.t', 'id'), 5000, false);"
	if got := normalizeGeneratedSQL("mysql", in); got != in {
		t.Fatalf("mysql SQL should not be rewritten: %s", got)
	}
}
