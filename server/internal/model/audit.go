package model

import "time"

// AuditAction 表示一次审计事件的动作类型。
// 采用 dotted notation 以便前端按前缀过滤（例如 sql.* / auth.* / member.*）。
type AuditAction string

const (
	// SQL 执行
	AuditSQLExecute AuditAction = "sql.execute"

	// 鉴权 / 会话
	AuditAuthLoginSuccess AuditAction = "auth.login.success"
	AuditAuthLoginFail    AuditAction = "auth.login.fail"
	AuditAuthRegister     AuditAction = "auth.register"

	// 成员 / 角色
	AuditMemberAdd    AuditAction = "member.add"
	AuditMemberRemove AuditAction = "member.remove"
	AuditMemberUpdate AuditAction = "member.update" // 角色变更走 update

	// 连接 CRUD
	AuditConnectionCreate AuditAction = "connection.create"
	AuditConnectionUpdate AuditAction = "connection.update"
	AuditConnectionDelete AuditAction = "connection.delete"
	AuditConnectionTest   AuditAction = "connection.test"
)

// AuditLog 是一条审计记录。
// 设计原则：
//   - 面向"事后追溯"，不参与业务事务。GORM 钩子无 cascade。
//   - GroupID 可空：登录 / 注册等无组上下文事件 GroupID = NULL。
//   - UserID 可空：登录失败时若邮箱不存在则为 NULL，但 UserEmail 必填。
//   - Detail 是 JSON 文本（原样存储），由调用方 marshal；查询时不做解码。
type AuditLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`

	UserID    *uint  `gorm:"index" json:"user_id,omitempty"`
	UserEmail string `gorm:"size:191;not null" json:"user_email"`

	Action AuditAction `gorm:"size:64;not null;index" json:"action"`

	GroupID *uint `gorm:"index" json:"group_id,omitempty"`
	ConnID  *uint `gorm:"index" json:"conn_id,omitempty"`

	// Target 是被操作对象的简短描述（如成员邮箱、连接名、表名），
	// 便于 UI 直接显示而不必反序列化 Detail。
	Target string `gorm:"size:255;not null;default:''" json:"target"`

	// Detail 是 JSON 字符串（如 SQL 文本、字段 diff、错误堆栈）。
	Detail string `gorm:"type:longtext;not null" json:"detail"`

	Success    bool  `gorm:"not null;index" json:"success"`
	DurationMS int64 `gorm:"not null;default:0" json:"duration_ms"`

	// 失败原因；Success=true 时为空。
	ErrorMsg string `gorm:"size:512;not null;default:''" json:"error_msg"`

	// 客户端信息
	IP        string `gorm:"size:64;not null;default:''" json:"ip"`
	UserAgent string `gorm:"size:255;not null;default:''" json:"user_agent"`
}

// TableName 显式指定，避免 GORM 默认 audit_logs 与某些 SQL 关键字冲突。
func (AuditLog) TableName() string { return "audit_logs" }
