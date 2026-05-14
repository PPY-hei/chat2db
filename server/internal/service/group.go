package service

import (
	"errors"
	"fmt"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
	"gorm.io/gorm"
)

// CreateGroup creates a group and makes the creator its Owner.
func CreateGroup(userID uint, name, description string) (*model.Group, error) {
	if name == "" {
		return nil, errors.New("group name is required")
	}
	g := &model.Group{Name: name, Description: description, OwnerID: userID}
	if err := db.Meta().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(g).Error; err != nil {
			return err
		}
		return tx.Create(&model.GroupMember{GroupID: g.ID, UserID: userID, Role: model.RoleOwner}).Error
	}); err != nil {
		return nil, err
	}
	return g, nil
}

// UpdateGroupInput 是更新组信息的可选字段。非 nil 字段才会被写入。
type UpdateGroupInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	ShareLLM    *bool   `json:"share_llm,omitempty"`
}

// UpdateGroup 更新组的元数据，仅 Owner 有权。
func UpdateGroup(actorID, groupID uint, in UpdateGroupInput) (*model.Group, error) {
	if _, err := RequireRole(actorID, groupID, model.RoleOwner); err != nil {
		return nil, err
	}
	var g model.Group
	if err := db.Meta().First(&g, groupID).Error; err != nil {
		return nil, err
	}
	updates := map[string]any{}
	if in.Name != nil {
		if *in.Name == "" {
			return nil, errors.New("name cannot be empty")
		}
		updates["name"] = *in.Name
	}
	if in.Description != nil {
		updates["description"] = *in.Description
	}
	if in.ShareLLM != nil {
		updates["share_llm"] = *in.ShareLLM
	}
	if len(updates) == 0 {
		return &g, nil
	}
	if err := db.Meta().Model(&g).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := db.Meta().First(&g, groupID).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// ListGroupsForUser returns every group the user is a member of, along with the role.
type GroupListItem struct {
	model.Group
	Role        model.Role `json:"role"`
	MemberCount int64      `json:"member_count"`
}

func ListGroupsForUser(userID uint) ([]GroupListItem, error) {
	var members []model.GroupMember
	if err := db.Meta().Where("user_id = ?", userID).Find(&members).Error; err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return []GroupListItem{}, nil
	}
	ids := make([]uint, 0, len(members))
	roleByGroup := make(map[uint]model.Role, len(members))
	for _, m := range members {
		ids = append(ids, m.GroupID)
		roleByGroup[m.GroupID] = m.Role
	}
	var groups []model.Group
	if err := db.Meta().Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return nil, err
	}
	out := make([]GroupListItem, 0, len(groups))
	for _, g := range groups {
		var cnt int64
		_ = db.Meta().Model(&model.GroupMember{}).Where("group_id = ?", g.ID).Count(&cnt).Error
		out = append(out, GroupListItem{Group: g, Role: roleByGroup[g.ID], MemberCount: cnt})
	}
	return out, nil
}

// RoleInGroup returns the role of user in group, or empty string if not a member.
func RoleInGroup(userID, groupID uint) (model.Role, error) {
	var m model.GroupMember
	if err := db.Meta().Where("group_id = ? AND user_id = ?", groupID, userID).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return m.Role, nil
}

// RequireRole returns an error if the user's role in the group is missing or
// below the required threshold.
func RequireRole(userID, groupID uint, minimum model.Role) (model.Role, error) {
	role, err := RoleInGroup(userID, groupID)
	if err != nil {
		return "", err
	}
	if role == "" {
		return "", errors.New("not a member of this group")
	}
	if !roleAtLeast(role, minimum) {
		return role, errors.New("permission denied")
	}
	return role, nil
}

// roleRank 维护角色等级：owner > admin > editor > viewer。
// 与前端 web/src/utils/role.ts 的 RANK 保持一致。
var roleRank = map[model.Role]int{
	model.RoleViewer: 1,
	model.RoleEditor: 2,
	model.RoleAdmin:  3,
	model.RoleOwner:  4,
}

func roleAtLeast(have, want model.Role) bool {
	return roleRank[have] >= roleRank[want]
}

// AddMember 添加（或更新）成员。
// 权限规则：
//   - Owner 可随意添加任意角色；修改任意成员角色。
//   - Admin / Editor 可**邀请**新成员（仅 viewer / editor），不可邀请 owner / admin；
//     不可对已存在的成员降/提权。
//   - Viewer 不可操作。
func AddMember(actorID, groupID, userID uint, role model.Role) error {
	if !role.Valid() {
		return errors.New("invalid role")
	}
	actorRole, err := RequireRole(actorID, groupID, model.RoleEditor)
	if err != nil {
		return err
	}
	// 只有 Owner 可以授予 Owner / Admin 角色
	if (role == model.RoleOwner || role == model.RoleAdmin) && !actorRole.CanManage() {
		return fmt.Errorf("only owner can grant %s role", role)
	}
	if userID == actorID && role != model.RoleOwner {
		return errors.New("owner cannot demote themselves; transfer ownership first")
	}
	var existing model.GroupMember
	err = db.Meta().Where("group_id = ? AND user_id = ?", groupID, userID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Meta().Create(&model.GroupMember{GroupID: groupID, UserID: userID, Role: role}).Error
	}
	if err != nil {
		return err
	}
	// 非 Owner 不能修改已有成员的角色（避免 Admin/Editor 改现有成员的角色绕过上面的检查）。
	if !actorRole.CanManage() && existing.Role != role {
		return errors.New("only owner can modify existing member role")
	}
	existing.Role = role
	return db.Meta().Save(&existing).Error
}

// RemoveMember removes a member; only Owner can do so. Owners cannot remove themselves
// while they are the sole owner.
func RemoveMember(actorID, groupID, userID uint) error {
	if _, err := RequireRole(actorID, groupID, model.RoleOwner); err != nil {
		return err
	}
	if actorID == userID {
		var owners int64
		if err := db.Meta().Model(&model.GroupMember{}).
			Where("group_id = ? AND role = ?", groupID, model.RoleOwner).Count(&owners).Error; err != nil {
			return err
		}
		if owners <= 1 {
			return errors.New("group must have at least one owner")
		}
	}
	return db.Meta().Where("group_id = ? AND user_id = ?", groupID, userID).Delete(&model.GroupMember{}).Error
}

// MemberView is a denormalized member listing.
type MemberView struct {
	UserID uint       `json:"user_id"`
	Email  string     `json:"email"`
	Name   string     `json:"name"`
	Role   model.Role `json:"role"`
}

// ListMembers returns the members of a group; the caller must be a member.
func ListMembers(actorID, groupID uint) ([]MemberView, error) {
	if _, err := RequireRole(actorID, groupID, model.RoleViewer); err != nil {
		return nil, err
	}
	var rows []struct {
		UserID uint
		Email  string
		Name   string
		Role   model.Role
	}
	err := db.Meta().Table("group_members AS gm").
		Select("gm.user_id, u.email, u.name, gm.role").
		Joins("JOIN users u ON u.id = gm.user_id").
		Where("gm.group_id = ?", groupID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]MemberView, 0, len(rows))
	for _, r := range rows {
		out = append(out, MemberView{UserID: r.UserID, Email: r.Email, Name: r.Name, Role: r.Role})
	}
	return out, nil
}
