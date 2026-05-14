package sqlguard

import (
	"errors"
	"strings"
	"unicode"

	"github.com/chy/chat2db/server/internal/model"
)

// Category classifies a single SQL statement at a coarse level.
type Category string

const (
	CatRead    Category = "read"    // SELECT / SHOW / EXPLAIN (without ANALYZE) / WITH ... SELECT
	CatWrite   Category = "write"   // INSERT / UPDATE / DELETE / MERGE / UPSERT
	CatDDL     Category = "ddl"     // CREATE / ALTER / DROP / TRUNCATE / RENAME / COMMENT
	CatTx      Category = "tx"      // BEGIN / COMMIT / ROLLBACK / SAVEPOINT
	CatAdmin   Category = "admin"   // GRANT / REVOKE / SET ROLE / VACUUM / ANALYZE / REINDEX / CLUSTER
	CatUnknown Category = "unknown" // could not classify
)

// Statement is a sanitized SQL chunk with its category.
type Statement struct {
	SQL      string
	Category Category
	Verb     string // first SQL keyword, uppercased
}

// Split splits raw SQL by semicolons while respecting string/identifier quoting
// and line/block comments. Empty statements are dropped.
func Split(raw string) []string {
	var out []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	inDollar := false
	dollarTag := ""
	inLineComment := false
	inBlockComment := false

	i := 0
	for i < len(raw) {
		c := raw[i]
		next := byte(0)
		if i+1 < len(raw) {
			next = raw[i+1]
		}

		if inLineComment {
			b.WriteByte(c)
			if c == '\n' {
				inLineComment = false
			}
			i++
			continue
		}
		if inBlockComment {
			b.WriteByte(c)
			if c == '*' && next == '/' {
				b.WriteByte(next)
				i += 2
				inBlockComment = false
				continue
			}
			i++
			continue
		}
		if inSingle {
			b.WriteByte(c)
			if c == '\'' {
				if next == '\'' { // escaped quote
					b.WriteByte(next)
					i += 2
					continue
				}
				inSingle = false
			}
			i++
			continue
		}
		if inDouble {
			b.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
			i++
			continue
		}
		if inDollar {
			b.WriteByte(c)
			if c == '$' && strings.HasPrefix(raw[i:], dollarTag) {
				b.WriteString(raw[i+1 : i+len(dollarTag)])
				i += len(dollarTag)
				inDollar = false
				dollarTag = ""
				continue
			}
			i++
			continue
		}

		// Not inside any quoted region
		if c == '-' && next == '-' {
			inLineComment = true
			b.WriteByte(c)
			i++
			continue
		}
		if c == '/' && next == '*' {
			inBlockComment = true
			b.WriteByte(c)
			i++
			continue
		}
		if c == '\'' {
			inSingle = true
			b.WriteByte(c)
			i++
			continue
		}
		if c == '"' {
			inDouble = true
			b.WriteByte(c)
			i++
			continue
		}
		if c == '$' {
			// Detect dollar-quoted strings: $tag$...$tag$
			end := i + 1
			for end < len(raw) && (raw[end] == '_' || isAlphaNum(raw[end])) {
				end++
			}
			if end < len(raw) && raw[end] == '$' {
				dollarTag = raw[i : end+1]
				inDollar = true
				b.WriteString(dollarTag)
				i = end + 1
				continue
			}
		}
		if c == ';' {
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	if rest := strings.TrimSpace(b.String()); rest != "" {
		out = append(out, rest)
	}

	// Drop empty entries.
	cleaned := out[:0]
	for _, s := range out {
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	return cleaned
}

func isAlphaNum(b byte) bool {
	r := rune(b)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// stripLeading removes leading comments and whitespace to find the first verb.
func stripLeading(s string) string {
	i := 0
	for i < len(s) {
		// skip whitespace
		for i < len(s) && unicode.IsSpace(rune(s[i])) {
			i++
		}
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
			continue
		}
		break
	}
	return s[i:]
}

// firstVerb returns the first SQL keyword of statement, uppercased.
func firstVerb(s string) string {
	s = stripLeading(s)
	// Some statements begin with "(": peel off leading parens for verbs like (SELECT ...)
	for len(s) > 0 && (s[0] == '(' || unicode.IsSpace(rune(s[0]))) {
		s = s[1:]
	}
	end := 0
	for end < len(s) && (unicode.IsLetter(rune(s[end])) || s[end] == '_') {
		end++
	}
	return strings.ToUpper(s[:end])
}

// secondVerb returns the second keyword (used to detect EXPLAIN ANALYZE).
func secondVerb(s string) string {
	s = stripLeading(s)
	// skip first word
	i := 0
	for i < len(s) && (unicode.IsLetter(rune(s[i])) || s[i] == '_') {
		i++
	}
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	end := i
	for end < len(s) && (unicode.IsLetter(rune(s[end])) || s[end] == '_') {
		end++
	}
	return strings.ToUpper(s[i:end])
}

// Classify computes the Category for a single statement.
func Classify(stmt string) Statement {
	verb := firstVerb(stmt)
	cat := CatUnknown
	switch verb {
	case "SELECT", "VALUES", "TABLE", "SHOW", "DESCRIBE", "DESC":
		cat = CatRead
	case "WITH":
		// WITH ... SELECT vs WITH ... INSERT/UPDATE/DELETE
		// Walk past the CTE list to find the trailing top-level keyword.
		cat = classifyWith(stmt)
	case "EXPLAIN":
		// EXPLAIN [ANALYZE] ... — treat ANALYZE as write-ish since it executes.
		sv := secondVerb(stmt)
		if sv == "ANALYZE" {
			cat = CatAdmin
		} else {
			cat = CatRead
		}
	case "INSERT", "UPDATE", "DELETE", "MERGE", "UPSERT", "COPY":
		cat = CatWrite
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "COMMENT":
		cat = CatDDL
	case "BEGIN", "COMMIT", "ROLLBACK", "START", "SAVEPOINT", "RELEASE":
		cat = CatTx
	case "GRANT", "REVOKE", "SET", "RESET", "VACUUM", "ANALYZE", "REINDEX", "CLUSTER", "LISTEN", "NOTIFY":
		cat = CatAdmin
	}
	return Statement{SQL: stmt, Category: cat, Verb: verb}
}

func classifyWith(stmt string) Category {
	// Conservative rule: if the stripped body contains any DML verb as a
	// standalone keyword, treat the whole CTE as a write — this preserves
	// the "deny by default" stance for Viewer/Editor permission checks.
	upper := strings.ToUpper(stmt)
	cleaned := stripQuotesAndComments(upper)
	writeKeywords := []string{" INSERT ", " UPDATE ", " DELETE ", " MERGE "}
	for _, k := range writeKeywords {
		if strings.Contains(cleaned, k) {
			return CatWrite
		}
	}
	return CatRead
}

func stripQuotesAndComments(s string) string {
	var b strings.Builder
	inSingle, inDouble, inLine, inBlock := false, false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		var n byte
		if i+1 < len(s) {
			n = s[i+1]
		}
		if inLine {
			if c == '\n' {
				inLine = false
				b.WriteByte(' ')
			}
			continue
		}
		if inBlock {
			if c == '*' && n == '/' {
				inBlock = false
				i++
				b.WriteByte(' ')
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			b.WriteByte(' ')
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			b.WriteByte(' ')
			continue
		}
		if c == '-' && n == '-' {
			inLine = true
			i++
			continue
		}
		if c == '/' && n == '*' {
			inBlock = true
			i++
			continue
		}
		if c == '\'' {
			inSingle = true
			b.WriteByte(' ')
			continue
		}
		if c == '"' {
			inDouble = true
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	return " " + b.String() + " "
}

// CheckAllowed returns nil if every statement in raw SQL is allowed for the given role.
// Viewer: only CatRead.
// Editor: CatRead + CatWrite + CatTx (commit/rollback to undo accidents).
// Admin / Owner: everything (including DDL/Admin).
func CheckAllowed(raw string, role model.Role) error {
	stmts := Split(raw)
	if len(stmts) == 0 {
		return errors.New("empty SQL")
	}
	for _, s := range stmts {
		st := Classify(s)
		if !allowed(st.Category, role) {
			return &PermissionError{
				Verb:     st.Verb,
				Category: string(st.Category),
				Role:     string(role),
			}
		}
	}
	return nil
}

func allowed(c Category, r model.Role) bool {
	if c == CatUnknown {
		return false
	}
	switch c {
	case CatRead:
		return r.Valid()
	case CatWrite, CatTx:
		return r.CanWrite()
	case CatDDL, CatAdmin:
		return r.CanDDL()
	}
	return false
}

// PermissionError describes a denied statement.
type PermissionError struct {
	Verb     string
	Category string
	Role     string
}

func (e *PermissionError) Error() string {
	return "statement " + e.Verb + " (" + e.Category + ") is not allowed for role " + e.Role
}
