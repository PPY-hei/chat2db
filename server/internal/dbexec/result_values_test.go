package dbexec

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNormalizeRowValuesForJSONTextifiesPrecisionRiskyTypes(t *testing.T) {
	row := []any{
		int64(4328326556395998000),
		"12345678901234567890.1234",
		float64(1.25),
		int64(42),
		"plain",
		[16]byte{0x31, 0x2a, 0x28, 0x00, 0x50, 0x23, 0x43, 0x2d, 0xb9, 0x5c, 0xa3, 0x94, 0x88, 0x95, 0x34, 0x10},
		pgtype.UUID{Bytes: [16]byte{0x33, 0x4d, 0xfb, 0x9e, 0x05, 0xa0, 0x49, 0xa7, 0x86, 0x54, 0x33, 0x10, 0x7f, 0x08, 0xa5, 0xce}, Valid: true},
	}
	got := normalizeRowValuesForJSON(
		[]string{"bigint", "numeric(38,4)", "double precision", "integer", "varchar", "uuid", "uuid"},
		row,
	)

	if got[0] != "4328326556395998000" {
		t.Fatalf("bigint should be stringified, got %#v (%T)", got[0], got[0])
	}
	if got[1] != "12345678901234567890.1234" {
		t.Fatalf("numeric text should be preserved, got %#v (%T)", got[1], got[1])
	}
	if got[2] != "1.25" {
		t.Fatalf("float should be stringified, got %#v (%T)", got[2], got[2])
	}
	if got[3] != int64(42) {
		t.Fatalf("non-risky integer should stay numeric, got %#v (%T)", got[3], got[3])
	}
	if got[4] != "plain" {
		t.Fatalf("text should stay text, got %#v (%T)", got[4], got[4])
	}
	if got[5] != "312a2800-5023-432d-b95c-a39488953410" {
		t.Fatalf("uuid bytes should be formatted, got %#v (%T)", got[5], got[5])
	}
	if got[6] != "334dfb9e-05a0-49a7-8654-33107f08a5ce" {
		t.Fatalf("pgtype uuid should be formatted, got %#v (%T)", got[6], got[6])
	}
}

func TestTypeNeedsTextForJSON(t *testing.T) {
	for _, typ := range []string{
		"bigint",
		"INT8",
		"decimal(20,0)",
		"numeric",
		"DOUBLE",
		"double precision",
		"float8",
		"real",
	} {
		if !typeNeedsTextForJSON(typ) {
			t.Fatalf("%q should be textified", typ)
		}
	}

	for _, typ := range []string{"integer", "int4", "smallint", "varchar", "timestamp", "bool"} {
		if typeNeedsTextForJSON(typ) {
			t.Fatalf("%q should not be textified", typ)
		}
	}
}
