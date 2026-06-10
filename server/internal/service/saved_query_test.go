package service

import (
	"testing"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
)

func TestListMySavedQueriesIncludesConnectionDatabase(t *testing.T) {
	setupTestDB(t)

	mustCreate := func(m any) {
		t.Helper()
		if err := db.Meta().Create(m).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	mustCreate(&model.User{ID: 1, Email: "viewer@example.com", Name: "Viewer", PasswordHash: "x"})
	mustCreate(&model.User{ID: 2, Email: "author@example.com", Name: "Author", PasswordHash: "x"})
	mustCreate(&model.Group{ID: 10, Name: "G", OwnerID: 2})
	mustCreate(&model.GroupMember{GroupID: 10, UserID: 1, Role: model.RoleViewer})
	mustCreate(&model.Connection{
		ID:            20,
		GroupID:       10,
		Name:          "pg-main",
		Driver:        "postgres",
		Host:          "127.0.0.1",
		Port:          5432,
		Database:      "aml",
		Username:      "chat2db",
		PasswordEnc:   "enc",
		CreatedByID:   2,
		SSLCACertEnc:  "",
		SSHPort:       22,
		ProxyUsername: "",
	})
	mustCreate(&model.SavedQuery{
		ID:           30,
		GroupID:      10,
		ConnectionID: 20,
		Title:        "Q",
		SQL:          "select 1",
		CreatedByID:  2,
	})

	rows, err := ListMySavedQueries(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.Database != "aml" {
		t.Fatalf("want database aml, got %q", got.Database)
	}
	if got.GroupName != "G" || got.ConnectionName != "pg-main" || got.CreatedByName != "Author" {
		t.Fatalf("unexpected saved query metadata: %+v", got)
	}
}
