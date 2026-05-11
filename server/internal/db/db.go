package db

import (
	"os"
	"path/filepath"

	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var metaDB *gorm.DB

// Init opens (and auto-migrates) the metadata SQLite database used by the app.
func Init() (*gorm.DB, error) {
	cfg := config.Get()
	if err := os.MkdirAll(filepath.Dir(cfg.MetaDBPath), 0o755); err != nil {
		return nil, err
	}
	g, err := gorm.Open(sqlite.Open(cfg.MetaDBPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}
	if err := g.AutoMigrate(
		&model.User{},
		&model.Group{},
		&model.GroupMember{},
		&model.Connection{},
		&model.SavedQuery{},
	); err != nil {
		return nil, err
	}
	metaDB = g
	return g, nil
}

func Meta() *gorm.DB { return metaDB }
