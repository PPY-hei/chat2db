package service

import (
	"errors"
	"strings"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
)

// SaveQueryInput is the request body for saving a query.
type SaveQueryInput struct {
	ConnectionID uint   `json:"connection_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	SQL          string `json:"sql"`
}

// SavedQueryView enriches SavedQuery with connection/group metadata and author.
type SavedQueryView struct {
	model.SavedQuery
	GroupName      string `json:"group_name"`
	ConnectionName string `json:"connection_name"`
	Database       string `json:"database"`
	CreatedByName  string `json:"created_by_name"`
}

// CreateSavedQuery creates a group-scoped saved SQL. Role at least Viewer.
func CreateSavedQuery(actorID uint, in SaveQueryInput) (*model.SavedQuery, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return nil, errors.New("title is required")
	}
	if strings.TrimSpace(in.SQL) == "" {
		return nil, errors.New("sql is required")
	}
	var conn model.Connection
	if err := db.Meta().First(&conn, in.ConnectionID).Error; err != nil {
		return nil, err
	}
	if _, err := RequireRole(actorID, conn.GroupID, model.RoleViewer); err != nil {
		return nil, err
	}
	sq := &model.SavedQuery{
		GroupID:      conn.GroupID,
		ConnectionID: conn.ID,
		Title:        in.Title,
		Description:  in.Description,
		SQL:          in.SQL,
		CreatedByID:  actorID,
	}
	if err := db.Meta().Create(sq).Error; err != nil {
		return nil, err
	}
	return sq, nil
}

// DeleteSavedQuery removes a saved query. Author or group owner only.
func DeleteSavedQuery(actorID, id uint) error {
	var sq model.SavedQuery
	if err := db.Meta().First(&sq, id).Error; err != nil {
		return err
	}
	if sq.CreatedByID != actorID {
		if _, err := RequireRole(actorID, sq.GroupID, model.RoleOwner); err != nil {
			return err
		}
	}
	return db.Meta().Delete(&sq).Error
}

// ListGroupSavedQueries lists saved queries in a group. Viewer+.
func ListGroupSavedQueries(actorID, groupID uint) ([]SavedQueryView, error) {
	if _, err := RequireRole(actorID, groupID, model.RoleViewer); err != nil {
		return nil, err
	}
	return querySavedQueries("sq.group_id = ?", []any{groupID})
}

// ListMySavedQueries lists every saved query the user can see — which means every
// saved query inside any group the user is a member of.
func ListMySavedQueries(actorID uint) ([]SavedQueryView, error) {
	var gm []model.GroupMember
	if err := db.Meta().Where("user_id = ?", actorID).Find(&gm).Error; err != nil {
		return nil, err
	}
	if len(gm) == 0 {
		return []SavedQueryView{}, nil
	}
	ids := make([]uint, 0, len(gm))
	for _, m := range gm {
		ids = append(ids, m.GroupID)
	}
	return querySavedQueries("sq.group_id IN ?", []any{ids})
}

func querySavedQueries(where string, args []any) ([]SavedQueryView, error) {
	var rows []struct {
		ID             uint
		GroupID        uint
		ConnectionID   uint
		Title          string
		Description    string
		SQL            string
		CreatedByID    uint
		CreatedAt      string
		UpdatedAt      string
		GroupName      string
		ConnectionName string
		Database       string `gorm:"column:connection_database"`
		CreatedByName  string
	}
	q := db.Meta().Table(metaTable("saved_queries", "sq")).
		Select("sq.id, sq.group_id, sq.connection_id, sq.title, sq.description, sq.sql, sq.created_by_id, sq.created_at, sq.updated_at, g.name AS group_name, c.name AS connection_name, "+metaCol("c", "database")+" AS connection_database, u.name AS created_by_name").
		Joins("JOIN "+metaTable("groups", "g")+" ON g.id = sq.group_id").
		Joins("JOIN "+metaTable("connections", "c")+" ON c.id = sq.connection_id").
		Joins("JOIN "+metaTable("users", "u")+" ON u.id = sq.created_by_id").
		Where(where, args...).
		Order("sq.created_at DESC")
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]SavedQueryView, 0, len(rows))
	for _, r := range rows {
		sv := SavedQueryView{
			SavedQuery: model.SavedQuery{
				ID:           r.ID,
				GroupID:      r.GroupID,
				ConnectionID: r.ConnectionID,
				Title:        r.Title,
				Description:  r.Description,
				SQL:          r.SQL,
				CreatedByID:  r.CreatedByID,
			},
			GroupName:      r.GroupName,
			ConnectionName: r.ConnectionName,
			Database:       r.Database,
			CreatedByName:  r.CreatedByName,
		}
		out = append(out, sv)
	}
	return out, nil
}
