package service

import (
	"errors"
	"time"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
	"gorm.io/gorm"
)

// ManageableGroupIDs 返回 userID 在哪些组拥有 admin/owner 角色。
// 用于"任务列表"等需要按可管理组聚合的场景；与 VisibleGroupIDsForAudit 同义，
// 抽出公共函数避免语义漂移。
func ManageableGroupIDs(userID uint) ([]uint, error) {
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

// MyGroupIDs 返回 userID 加入的所有组 ID（不论角色）。
func MyGroupIDs(userID uint) ([]uint, error) {
	var members []model.GroupMember
	if err := db.Meta().Where("user_id = ?", userID).Find(&members).Error; err != nil {
		return nil, err
	}
	out := make([]uint, 0, len(members))
	for _, m := range members {
		out = append(out, m.GroupID)
	}
	return out, nil
}

// CreateTaskParams 创建任务入参，handler 解析后传入。
type CreateTaskParams struct {
	GroupID        uint
	ConnID         uint
	Kind           model.TaskKind
	Scope          model.TaskScope
	TargetDatabase string
	TargetSchema   string
	TargetTable    string
}

// CreateTask 校验权限 + 入库 + 入队。返回已分配 ID 的 Task。
// 任务由调用方角色 ≥ editor 即可创建（与"导出"的最小权限对齐）。
func CreateTask(actorID uint, p CreateTaskParams) (*model.Task, error) {
	if !p.Kind.Valid() {
		return nil, errors.New("invalid task kind")
	}
	if !p.Scope.Valid() {
		return nil, errors.New("invalid task scope")
	}

	// 校验连接存在 + actor 至少是 editor。
	conn, _, err := GetConnection(actorID, p.ConnID)
	if err != nil {
		return nil, err
	}
	if conn.GroupID != p.GroupID {
		return nil, errors.New("connection does not belong to this group")
	}
	if _, err := RequireRole(actorID, p.GroupID, model.RoleEditor); err != nil {
		return nil, err
	}

	// 范围参数完整性校验
	switch p.Scope {
	case model.TaskScopeTable:
		if p.TargetDatabase == "" || p.TargetTable == "" {
			return nil, errors.New("scope=table requires target_database and target_table")
		}
	case model.TaskScopeDatabase:
		if p.TargetDatabase == "" {
			return nil, errors.New("scope=database requires target_database")
		}
	}

	var creatorName string
	var u model.User
	if err := db.Meta().Select("name").First(&u, actorID).Error; err == nil {
		creatorName = u.Name
	}

	t := &model.Task{
		GroupID:        p.GroupID,
		ConnID:         p.ConnID,
		Kind:           p.Kind,
		Scope:          p.Scope,
		TargetDatabase: p.TargetDatabase,
		TargetSchema:   p.TargetSchema,
		TargetTable:    p.TargetTable,
		Status:         model.TaskStatusPending,
		CreatedByID:    actorID,
		CreatorName:    creatorName,
		CreatedAt:      time.Now(),
	}
	if err := db.Meta().Create(t).Error; err != nil {
		return nil, err
	}
	enqueueTask(t.ID)
	return t, nil
}

// GetTask 读取单条；不做权限校验，handler 层负责。
func GetTask(id uint) (*model.Task, error) {
	var t model.Task
	if err := db.Meta().First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// TaskFilter 列表筛选。
//
// 可见性表达式：
//   - admin/owner 用户：传入 GroupIDs = 我能管理的组；OR SelfUserID 自己创建的。
//   - 普通用户：仅自己创建。
type TaskFilter struct {
	GroupIDs   []uint
	SelfUserID uint // 当 >0 时叠加 "OR created_by_id = SelfUserID"

	GroupID *uint // 指定单组（必须在 GroupIDs 中）
	ConnID  *uint
	Kind    string
	Scope   string
	Status  string
	Keyword string
	From    *time.Time
	To      *time.Time

	Page     int
	PageSize int
}

// TaskPage 分页响应。
type TaskPage struct {
	Items []model.Task `json:"items"`
	Total int64        `json:"total"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
}

const (
	maxTaskPageSize = 200
	defTaskPageSize = 20
)

// QueryTasks 按 filter 分页查询，按 created_at 倒序。
func QueryTasks(f TaskFilter) (*TaskPage, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = defTaskPageSize
	}
	if f.PageSize > maxTaskPageSize {
		f.PageSize = maxTaskPageSize
	}

	q := db.Meta().Model(&model.Task{})

	// 可见性：admin/owner 看自己管理组 + 自己创建的；普通用户仅看自己创建的。
	switch {
	case f.GroupID != nil:
		// 单组模式：调用方已校验 GroupID ∈ GroupIDs，直接限定该组
		q = q.Where("group_id = ?", *f.GroupID)
	case len(f.GroupIDs) > 0 && f.SelfUserID > 0:
		q = q.Where("group_id IN ? OR created_by_id = ?", f.GroupIDs, f.SelfUserID)
	case len(f.GroupIDs) > 0:
		q = q.Where("group_id IN ?", f.GroupIDs)
	case f.SelfUserID > 0:
		q = q.Where("created_by_id = ?", f.SelfUserID)
	default:
		// 无任何可见性约束 → 视作"什么都不能看"，避免泄漏
		return &TaskPage{Items: []model.Task{}, Total: 0, Page: f.Page, Size: f.PageSize}, nil
	}

	if f.ConnID != nil {
		q = q.Where("conn_id = ?", *f.ConnID)
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.Scope != "" {
		q = q.Where("scope = ?", f.Scope)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at < ?", *f.To)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("creator_name LIKE ? OR target_database LIKE ? OR target_table LIKE ? OR error_msg LIKE ?",
			kw, kw, kw, kw)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []model.Task
	if err := q.Order("created_at DESC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&items).Error; err != nil {
		return nil, err
	}
	return &TaskPage{Items: items, Total: total, Page: f.Page, Size: f.PageSize}, nil
}

// CancelTask 标记任务为待取消。worker 会在 row-loop 中检查到并停止。
// 已结束（succeeded/failed/canceled）的任务不允许取消。
func CancelTask(id uint) error {
	return db.Meta().Model(&model.Task{}).
		Where("id = ? AND status IN ?", id, []model.TaskStatus{
			model.TaskStatusPending, model.TaskStatusRunning,
		}).
		Update("cancel_requested", true).Error
}

// DeleteTask 删除任务记录（不联动删除产物文件，由 runner 清理）。
func DeleteTask(id uint) error {
	return db.Meta().Delete(&model.Task{}, id).Error
}

// CanViewTask 判断 user 是否可见某任务：admin/owner 看本组 + 任务创建者本人。
func CanViewTask(userID uint, t *model.Task) (bool, error) {
	if t.CreatedByID == userID {
		return true, nil
	}
	manageable, err := ManageableGroupIDs(userID)
	if err != nil {
		return false, err
	}
	for _, gid := range manageable {
		if gid == t.GroupID {
			return true, nil
		}
	}
	return false, nil
}

// updateTaskFields 是 worker 用的内部 helper：直接打字段 update，避免覆盖其它列。
func updateTaskFields(id uint, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return db.Meta().Model(&model.Task{}).Where("id = ?", id).Updates(fields).Error
}
