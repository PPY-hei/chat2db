package service

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
)

const (
	auditQueueCapacity = 1024
	auditPurgeInterval = 6 * time.Hour

	// QueryAudit 单页上限，避免一次拉太多拖慢 UI / 占 DB。
	maxAuditPageSize = 200
	defAuditPageSize = 50
)

var (
	auditQueue        chan model.AuditLog
	auditWG           sync.WaitGroup
	auditOnce         sync.Once
	auditCancel       context.CancelFunc
	auditDroppedTotal int64
)

// StartAuditWorker 在第一次调用时启动异步写入 worker 与定时清理 goroutine。
// retention <= 0 表示不清理（测试环境）。多次调用是 no-op。
func StartAuditWorker(retention time.Duration) {
	auditOnce.Do(func() {
		auditQueue = make(chan model.AuditLog, auditQueueCapacity)
		ctx, cancel := context.WithCancel(context.Background())
		auditCancel = cancel
		auditWG.Add(1)
		go runAuditWorker()
		if retention > 0 {
			auditWG.Add(1)
			go runAuditPurger(ctx, retention)
		}
	})
}

// StopAuditWorker 关闭队列并等 worker drain 完毕。
func StopAuditWorker() {
	if auditCancel == nil {
		return
	}
	close(auditQueue)
	auditCancel()
	auditWG.Wait()
}

// LogAudit 非阻塞 enqueue。队列满时丢弃并 log；worker 未启动时退化为 sync 写（便于单测）。
func LogAudit(entry model.AuditLog) {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if auditQueue == nil {
		if err := db.Meta().Create(&entry).Error; err != nil {
			slog.Error("audit sync write failed (worker not started)",
				slog.Any("error", err),
				slog.String("action", string(entry.Action)),
			)
		}
		return
	}
	select {
	case auditQueue <- entry:
	default:
		atomic.AddInt64(&auditDroppedTotal, 1)
		slog.Warn("audit queue full, event dropped",
			slog.String("action", string(entry.Action)),
			slog.String("user_email", entry.UserEmail),
		)
	}
}

// AuditDroppedTotal 暴露累计丢弃数，用于监控 / 观察。
func AuditDroppedTotal() int64 { return atomic.LoadInt64(&auditDroppedTotal) }

func runAuditWorker() {
	defer auditWG.Done()
	for entry := range auditQueue {
		if err := db.Meta().Create(&entry).Error; err != nil {
			slog.Error("audit write failed",
				slog.String("action", string(entry.Action)),
				slog.Any("error", err),
			)
		}
	}
}

func runAuditPurger(ctx context.Context, retention time.Duration) {
	defer auditWG.Done()
	t := time.NewTicker(auditPurgeInterval)
	defer t.Stop()
	purge := func() {
		cutoff := time.Now().Add(-retention)
		n, err := PurgeAuditBefore(cutoff)
		if err != nil {
			slog.Error("audit purge failed",
				slog.Time("cutoff", cutoff),
				slog.Any("error", err),
			)
			return
		}
		if n > 0 {
			slog.Info("audit purged",
				slog.Int64("rows", n),
				slog.Time("cutoff", cutoff),
			)
		}
	}
	purge()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purge()
		}
	}
}

// PurgeAuditBefore 删除 created_at < cutoff 的全部审计记录，返回行数。
func PurgeAuditBefore(cutoff time.Time) (int64, error) {
	res := db.Meta().Where("created_at < ?", cutoff).Delete(&model.AuditLog{})
	return res.RowsAffected, res.Error
}

// AuditFilter 描述查询条件。
//
// 可见范围由 GroupIDs 与 SelfUserID 共同决定：
//   - 仅 GroupIDs：     group_id IN (...)
//   - 仅 SelfUserID:    group_id IS NULL AND user_id = self（无组事件 + 仅自己）
//   - 两者都给：       group_id IN (...) OR (group_id IS NULL AND user_id = self)
//   - 两者都不给：     不限组（仅推荐内部 / 测试调用）
//
// 用于实现"按组隔离 + 自身的 auth.* 无组事件可见"的规则。
type AuditFilter struct {
	From    *time.Time
	To      *time.Time
	UserIDs []uint
	Actions []model.AuditAction
	Keyword string
	OnlyFail bool

	GroupIDs   []uint
	SelfUserID uint

	Page     int
	PageSize int
}

// AuditPage 分页响应。
type AuditPage struct {
	Items []model.AuditLog `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// QueryAudit 按条件分页查询，按 created_at 倒序。
func QueryAudit(f AuditFilter) (*AuditPage, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = defAuditPageSize
	}
	if f.PageSize > maxAuditPageSize {
		f.PageSize = maxAuditPageSize
	}

	q := db.Meta().Model(&model.AuditLog{})
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at < ?", *f.To)
	}
	if len(f.UserIDs) > 0 {
		q = q.Where("user_id IN ?", f.UserIDs)
	}
	switch {
	case len(f.GroupIDs) > 0 && f.SelfUserID > 0:
		q = q.Where("group_id IN ? OR (group_id IS NULL AND user_id = ?)", f.GroupIDs, f.SelfUserID)
	case len(f.GroupIDs) > 0:
		q = q.Where("group_id IN ?", f.GroupIDs)
	case f.SelfUserID > 0:
		q = q.Where("group_id IS NULL AND user_id = ?", f.SelfUserID)
	}
	if len(f.Actions) > 0 {
		q = q.Where("action IN ?", f.Actions)
	}
	if f.OnlyFail {
		q = q.Where("success = ?", false)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("user_email LIKE ? OR target LIKE ? OR detail LIKE ?", kw, kw, kw)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []model.AuditLog
	if err := q.Order("created_at DESC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, err
	}
	return &AuditPage{Items: items, Total: total, Page: f.Page, Size: f.PageSize}, nil
}

// VisibleGroupIDsForAudit 返回 user 在哪些组可见审计事件：
// 仅 admin/owner 角色的组进入返回列表。若用户不在任何 admin/owner 组，则返回空切片。
func VisibleGroupIDsForAudit(userID uint) ([]uint, error) {
	var members []model.GroupMember
	if err := db.Meta().Where("user_id = ?", userID).Find(&members).Error; err != nil {
		return nil, err
	}
	out := make([]uint, 0, len(members))
	for _, m := range members {
		if m.Role.CanDDL() { // owner / admin
			out = append(out, m.GroupID)
		}
	}
	return out, nil
}
