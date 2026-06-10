package service

import (
	"testing"

	"github.com/chy/chat2db/server/internal/db"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMetaIdentUsesMySQLQuoting(t *testing.T) {
	g, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "chat2db:chat2db@tcp(127.0.0.1:1)/chat2db?parseTime=true",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		DryRun:               true,
	})
	if err != nil {
		t.Fatalf("open dry-run mysql db: %v", err)
	}
	db.SetMetaForTest(g)
	t.Cleanup(func() { db.SetMetaForTest(nil) })

	if got := metaTable("groups", "g"); got != "`groups` AS g" {
		t.Fatalf("unexpected mysql table quote: %q", got)
	}
	if got := metaCol("c", "database"); got != "c.`database`" {
		t.Fatalf("unexpected mysql column quote: %q", got)
	}
}
