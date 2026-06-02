package model

import "time"

// TaskKind 任务种类：导入 / 导出 / 表结构同步 / 数据同步。
type TaskKind string

const (
	TaskKindExport     TaskKind = "export"
	TaskKindImport     TaskKind = "import"
	TaskKindSchemaSync TaskKind = "schema_sync"
	TaskKindDataSync   TaskKind = "data_sync"
)

func (k TaskKind) Valid() bool {
	return k == TaskKindExport || k == TaskKindImport || k == TaskKindSchemaSync || k == TaskKindDataSync
}

// TaskScope 任务作用范围：整连接 / 单库 / 单 schema / 单表。
//
//   - TaskScopeConnection：作用于连接下所有数据库的所有表
//   - TaskScopeDatabase  ：作用于指定数据库下的所有表（含其下全部 schema）
//   - TaskScopeSchema    ：作用于指定 database+schema 下的所有表
//   - TaskScopeTable     ：仅作用于指定表
type TaskScope string

const (
	TaskScopeConnection TaskScope = "connection"
	TaskScopeDatabase   TaskScope = "database"
	TaskScopeSchema     TaskScope = "schema"
	TaskScopeTable      TaskScope = "table"
)

func (s TaskScope) Valid() bool {
	return s == TaskScopeConnection || s == TaskScopeDatabase ||
		s == TaskScopeSchema || s == TaskScopeTable
}

// TaskStatus 任务生命周期。
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"   // 已入队，未开始
	TaskStatusRunning   TaskStatus = "running"   // worker 处理中
	TaskStatusSucceeded TaskStatus = "succeeded" // 成功
	TaskStatusFailed    TaskStatus = "failed"    // 失败（含部分失败）
	TaskStatusCanceled  TaskStatus = "canceled"  // 用户取消
)

// Task 异步任务记录。
//
// 设计要点：
//   - 仅追踪元信息和状态/进度；产物（CSV / zip）落地到磁盘，FilePath 记录路径。
//   - 进度采用 0-100 整数，避免 float 累计误差。
//   - CancelRequested 是软取消信号：worker 在 row-loop 中周期性检查。
//   - 对于同步任务（schema_sync/data_sync），ConnID 是源连接，TargetConnID 是目标连接。
type Task struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	GroupID      uint   `gorm:"not null;index" json:"group_id"`
	ConnID       uint   `gorm:"not null;index" json:"conn_id"`
	TargetConnID uint   `gorm:"not null;default:0;index" json:"target_conn_id"` // 目标连接（仅同步任务使用）

	Kind  TaskKind  `gorm:"size:16;not null;index" json:"kind"`
	Scope TaskScope `gorm:"size:16;not null" json:"scope"`

	// 作用对象。Scope=connection 时三者均空；Scope=database 时仅 TargetDatabase；
	// Scope=table 时 TargetDatabase + TargetSchema + TargetTable 都给。
	TargetDatabase string `gorm:"size:128;not null;default:''" json:"target_database"`
	TargetSchema   string `gorm:"size:128;not null;default:''" json:"target_schema"`
	TargetTable    string `gorm:"size:128;not null;default:''" json:"target_table"`

	// 目标连接的数据库/schema/表（仅同步任务使用）
	DestDatabase string `gorm:"size:128;not null;default:''" json:"dest_database"`
	DestSchema   string `gorm:"size:128;not null;default:''" json:"dest_schema"`
	DestTable    string `gorm:"size:128;not null;default:''" json:"dest_table"`

	Status   TaskStatus `gorm:"size:16;not null;index" json:"status"`
	Progress int        `gorm:"not null;default:0" json:"progress"`

	ProcessedRows int64 `gorm:"not null;default:0" json:"processed_rows"`
	TotalRows     int64 `gorm:"not null;default:0" json:"total_rows"`
	TotalTables   int   `gorm:"not null;default:0" json:"total_tables"`
	DoneTables    int   `gorm:"not null;default:0" json:"done_tables"`

	// 产物路径（相对应用工作目录）。仅 succeeded 时有效。
	FilePath string `gorm:"size:512;not null;default:''" json:"file_path"`
	FileSize int64  `gorm:"not null;default:0" json:"file_size"`

	ErrorMsg string `gorm:"size:1024;not null;default:''" json:"error_msg"`

	CancelRequested bool `gorm:"not null;default:false" json:"cancel_requested"`

	CreatedByID uint   `gorm:"not null;index" json:"created_by_id"`
	CreatorName string `gorm:"size:128;not null;default:''" json:"creator_name"`

	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Task) TableName() string { return "tasks" }
