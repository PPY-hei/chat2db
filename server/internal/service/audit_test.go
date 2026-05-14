package service

import (
	"testing"
	"time"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB 用 sqlite 内存库 + AutoMigrate 替换全局 metaDB，
// 让 service 包内基于 db.Meta() 的逻辑可在单测中运行。
//
// 通过反射或暴露 setter 都过于侵入；这里直接用同包路径访问 unexported
// 的 metaDB 变量。如果未来 db.metaDB 改名/改包，本测试需要同步更新。
func setupTestDB(t *testing.T) {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := g.AutoMigrate(
		&model.User{},
		&model.Group{},
		&model.GroupMember{},
		&model.Connection{},
		&model.SavedQuery{},
		&model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db.SetMetaForTest(g)
	t.Cleanup(func() { db.SetMetaForTest(nil) })
}

func TestVisibleGroupIDsForAudit(t *testing.T) {
	setupTestDB(t)

	// 4 个组，user 1 在不同角色下
	mustCreate := func(m any) {
		if err := db.Meta().Create(m).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	mustCreate(&model.User{ID: 1, Email: "a@x.com", Name: "A", PasswordHash: "x"})
	mustCreate(&model.Group{ID: 10, Name: "G-owner", OwnerID: 1})
	mustCreate(&model.Group{ID: 11, Name: "G-admin", OwnerID: 2})
	mustCreate(&model.Group{ID: 12, Name: "G-editor", OwnerID: 2})
	mustCreate(&model.Group{ID: 13, Name: "G-viewer", OwnerID: 2})

	mustCreate(&model.GroupMember{GroupID: 10, UserID: 1, Role: model.RoleOwner})
	mustCreate(&model.GroupMember{GroupID: 11, UserID: 1, Role: model.RoleAdmin})
	mustCreate(&model.GroupMember{GroupID: 12, UserID: 1, Role: model.RoleEditor})
	mustCreate(&model.GroupMember{GroupID: 13, UserID: 1, Role: model.RoleViewer})

	ids, err := VisibleGroupIDsForAudit(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 visible groups (owner + admin), got %v", ids)
	}
	seen := map[uint]bool{}
	for _, g := range ids {
		seen[g] = true
	}
	if !seen[10] || !seen[11] {
		t.Fatalf("expected groups 10 & 11 visible, got %v", ids)
	}
}

func TestQueryAudit_GroupIsolation(t *testing.T) {
	setupTestDB(t)

	now := time.Now()
	mustCreate := func(m any) {
		if err := db.Meta().Create(m).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	// 三条事件：
	//   - g=10 admin/owner 视角应能看到
	//   - g=20 admin/owner 不在该组，应看不到
	//   - g=NULL, user=1     自身无组事件，应能看到
	mustCreate(&model.AuditLog{CreatedAt: now, UserEmail: "a", Action: model.AuditSQLExecute, GroupID: uintPtr(10), Detail: "{}"})
	mustCreate(&model.AuditLog{CreatedAt: now, UserEmail: "b", Action: model.AuditSQLExecute, GroupID: uintPtr(20), Detail: "{}"})
	mustCreate(&model.AuditLog{CreatedAt: now, UserEmail: "a", Action: model.AuditAuthLoginSuccess, UserID: uintPtr(1), Detail: "{}"})

	// 别人的无组事件，不应被自己看到
	mustCreate(&model.AuditLog{CreatedAt: now, UserEmail: "x", Action: model.AuditAuthLoginFail, UserID: uintPtr(99), Detail: "{}"})

	from := now.Add(-time.Hour)
	to := now.Add(time.Hour)
	page, err := QueryAudit(AuditFilter{
		From: &from, To: &to,
		GroupIDs:   []uint{10},
		SelfUserID: 1,
		PageSize:   50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("want 2 visible logs, got %d items=%+v", page.Total, page.Items)
	}
}

func TestPurgeAuditBefore(t *testing.T) {
	setupTestDB(t)

	now := time.Now()
	old := now.Add(-100 * 24 * time.Hour)
	mustCreate := func(m any) {
		if err := db.Meta().Create(m).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	mustCreate(&model.AuditLog{CreatedAt: old, UserEmail: "old", Action: model.AuditSQLExecute, Detail: "{}"})
	mustCreate(&model.AuditLog{CreatedAt: now, UserEmail: "new", Action: model.AuditSQLExecute, Detail: "{}"})

	n, err := PurgeAuditBefore(now.Add(-90 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want purge 1 row, got %d", n)
	}
	var remain int64
	if err := db.Meta().Model(&model.AuditLog{}).Count(&remain).Error; err != nil {
		t.Fatal(err)
	}
	if remain != 1 {
		t.Fatalf("want 1 remaining, got %d", remain)
	}
}

func uintPtr(v uint) *uint { return &v }
