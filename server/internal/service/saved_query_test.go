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

func TestListMySavedQueriesSkipsSoftDeletedSavedQueries(t *testing.T) {
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
		ID:          20,
		GroupID:     10,
		Name:        "pg-main",
		Driver:      "postgres",
		Host:        "127.0.0.1",
		Port:        5432,
		Database:    "aml",
		Username:    "chat2db",
		PasswordEnc: "enc",
		CreatedByID: 2,
	})
	deleted := &model.SavedQuery{
		ID:           30,
		GroupID:      10,
		ConnectionID: 20,
		Title:        "deleted",
		SQL:          "select deleted",
		CreatedByID:  2,
	}
	mustCreate(deleted)
	mustCreate(&model.SavedQuery{
		ID:           31,
		GroupID:      10,
		ConnectionID: 20,
		Title:        "active",
		SQL:          "select active",
		CreatedByID:  2,
	})
	if err := db.Meta().Delete(deleted).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	rows, err := ListMySavedQueries(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 active row, got %d rows=%+v", len(rows), rows)
	}
	if rows[0].ID != 31 || rows[0].Title != "active" {
		t.Fatalf("unexpected row after soft delete: %+v", rows[0])
	}
}

func TestDeleteSavedQueryIsIdempotentWhenAlreadyGone(t *testing.T) {
	setupTestDB(t)

	mustCreate := func(m any) {
		t.Helper()
		if err := db.Meta().Create(m).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	mustCreate(&model.User{ID: 1, Email: "author@example.com", Name: "Author", PasswordHash: "x"})
	mustCreate(&model.Group{ID: 10, Name: "G", OwnerID: 1})
	mustCreate(&model.GroupMember{GroupID: 10, UserID: 1, Role: model.RoleOwner})
	mustCreate(&model.SavedQuery{
		ID:           30,
		GroupID:      10,
		ConnectionID: 20,
		Title:        "Q",
		SQL:          "select 1",
		CreatedByID:  1,
	})

	if err := DeleteSavedQuery(1, 30); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := DeleteSavedQuery(1, 30); err != nil {
		t.Fatalf("second delete should be idempotent: %v", err)
	}
	if err := DeleteSavedQuery(1, 999); err != nil {
		t.Fatalf("missing delete should be idempotent: %v", err)
	}
}
