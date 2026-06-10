package service

import (
	"strings"

	"github.com/chy/chat2db/server/internal/db"
)

func metaIdent(name string) string {
	quote := `"`
	if db.Meta() != nil && db.Meta().Dialector.Name() == "mysql" {
		quote = "`"
	}
	return quote + strings.ReplaceAll(name, quote, quote+quote) + quote
}

func metaTable(name, alias string) string {
	return metaIdent(name) + " AS " + alias
}

func metaCol(alias, name string) string {
	return alias + "." + metaIdent(name)
}
