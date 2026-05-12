package model

import (
	"time"

	"gorm.io/gorm"
)

// User represents an application account (NOT a database account).
type User struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	Email        string         `gorm:"uniqueIndex;size:191;not null" json:"email"`
	Name         string         `gorm:"size:64;not null" json:"name"`
	PasswordHash string         `gorm:"size:255;not null" json:"-"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`

	LLMEndpoint  string `gorm:"size:255" json:"llm_endpoint,omitempty"`
	LLMModel     string `gorm:"size:128" json:"llm_model,omitempty"`
	LLMAPIKeyEnc string `gorm:"size:1024" json:"-"`
}

// Role within a connection group.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleEditor, RoleViewer:
		return true
	}
	return false
}

// CanWrite returns true if the role can run INSERT/UPDATE/DELETE.
func (r Role) CanWrite() bool { return r == RoleOwner || r == RoleEditor }

// CanManage returns true if the role can manage members and connections.
func (r Role) CanManage() bool { return r == RoleOwner }

// Group is a sharable collection of database connections.
type Group struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:128;not null" json:"name"`
	Description string         `gorm:"size:512" json:"description"`
	OwnerID     uint           `gorm:"not null;index" json:"owner_id"`
	// ShareLLM 表示该组的 Owner 是否把自己的 LLM 配置共享给组内其他成员使用。
	// 组员若自身未配置 LLM，则会优先使用任意一个开启了分享的所属组 Owner 的配置。
	ShareLLM    bool           `gorm:"not null;default:false" json:"share_llm"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// GroupMember binds a user to a group with a role.
type GroupMember struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   uint      `gorm:"not null;uniqueIndex:idx_group_user" json:"group_id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_group_user" json:"user_id"`
	Role      Role      `gorm:"size:16;not null" json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Connection is a database connection shared inside a group.
type Connection struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	GroupID     uint           `gorm:"not null;index" json:"group_id"`
	Name        string         `gorm:"size:128;not null" json:"name"`
	Driver      string         `gorm:"size:32;not null" json:"driver"` // postgres for now
	Host        string         `gorm:"size:255;not null" json:"host"`
	Port        int            `gorm:"not null" json:"port"`
	Database    string         `gorm:"size:128;not null" json:"database"`
	Username    string         `gorm:"size:128;not null" json:"username"`
	PasswordEnc string         `gorm:"size:1024;not null" json:"-"`
	SSLMode     string         `gorm:"size:32;default:'disable'" json:"ssl_mode"`
	// SSL 证书（PEM 文本，加密存储）
	SSLCACertEnc    string `gorm:"type:text" json:"-"` // CA 根证书
	SSLClientCertEnc string `gorm:"type:text" json:"-"` // 客户端证书
	SSLClientKeyEnc  string `gorm:"type:text" json:"-"` // 客户端私钥
	// SSH 隧道
	SSHEnabled       bool   `gorm:"not null;default:false" json:"ssh_enabled"`
	SSHHost          string `gorm:"size:255" json:"ssh_host"`
	SSHPort          int    `gorm:"default:22" json:"ssh_port"`
	SSHUser          string `gorm:"size:128" json:"ssh_user"`
	SSHAuthMethod    string `gorm:"size:32" json:"ssh_auth_method"` // "password" | "privatekey"
	SSHPasswordEnc   string `gorm:"size:1024" json:"-"`
	SSHPrivateKeyEnc string `gorm:"type:text" json:"-"`
	SSHPassphraseEnc string `gorm:"size:1024" json:"-"` // 私钥的 passphrase
	CreatedByID uint           `gorm:"not null" json:"created_by_id"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// SavedQuery is a SQL snippet saved at group level (shared with everyone in group).
type SavedQuery struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	GroupID      uint           `gorm:"not null;index" json:"group_id"`
	ConnectionID uint           `gorm:"not null;index" json:"connection_id"`
	Title        string         `gorm:"size:255;not null" json:"title"`
	Description  string         `gorm:"size:1024" json:"description"`
	SQL          string         `gorm:"type:text;not null" json:"sql"`
	CreatedByID  uint           `gorm:"not null;index" json:"created_by_id"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}
