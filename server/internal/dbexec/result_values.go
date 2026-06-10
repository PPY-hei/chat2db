package dbexec

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

func normalizeRowValuesForJSON(types []string, row []any) []any {
	if len(types) == 0 || len(row) == 0 {
		return row
	}
	out := make([]any, len(row))
	copy(out, row)
	for i := range out {
		if i >= len(types) || out[i] == nil {
			continue
		}
		out[i] = normalizeValueForJSON(types[i], out[i])
	}
	return out
}

func normalizeValueForJSON(dbType string, v any) any {
	s := normalizedDBType(dbType)
	if s == "uuid" {
		if text, ok := uuidValueToString(v); ok {
			return text
		}
		return valueToDisplayText(v)
	}
	if typeNeedsTextForJSON(dbType) {
		return valueToDisplayText(v)
	}
	return v
}

func typeNeedsTextForJSON(dbType string) bool {
	s := normalizedDBType(dbType)
	if s == "" {
		return false
	}
	switch s {
	case "bigint", "int8", "bigserial", "serial8",
		"decimal", "numeric", "number", "money",
		"float", "float4", "float8", "real", "double", "double precision",
		"binary_float", "binary_double":
		return true
	}
	return strings.Contains(s, "decimal") ||
		strings.Contains(s, "numeric") ||
		strings.Contains(s, "double") ||
		strings.Contains(s, "float")
}

func normalizedDBType(dbType string) string {
	s := strings.ToLower(strings.TrimSpace(dbType))
	if idx := strings.IndexAny(s, "( "); idx >= 0 {
		s = s[:idx]
	}
	return s
}

func uuidValueToString(v any) (string, bool) {
	switch x := v.(type) {
	case pgtype.UUID:
		if !x.Valid {
			return "", false
		}
		return formatUUIDBytes(x.Bytes[:]), true
	case *pgtype.UUID:
		if x == nil || !x.Valid {
			return "", false
		}
		return formatUUIDBytes(x.Bytes[:]), true
	case [16]byte:
		return formatUUIDBytes(x[:]), true
	case []byte:
		if len(x) == 16 {
			return formatUUIDBytes(x), true
		}
		return string(x), true
	case string:
		return x, true
	default:
		return "", false
	}
}

func formatUUIDBytes(b []byte) string {
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

func valueToDisplayText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		if math.IsInf(x, 1) {
			return "Infinity"
		}
		if math.IsInf(x, -1) {
			return "-Infinity"
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		if math.IsNaN(float64(x)) {
			return "NaN"
		}
		if math.IsInf(float64(x), 1) {
			return "Infinity"
		}
		if math.IsInf(float64(x), -1) {
			return "-Infinity"
		}
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case fmt.Stringer:
		return x.String()
	default:
		if b, err := json.Marshal(x); err == nil && string(b) != "null" {
			return strings.Trim(string(b), `"`)
		}
		return fmt.Sprintf("%v", x)
	}
}
