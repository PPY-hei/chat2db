package service

import (
	"errors"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
)

// ConnectionInput is the payload for creating/updating a connection.
type ConnectionInput struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"ssl_mode"`
}

// CreateConnection creates a new connection in the group. Owner only.
func CreateConnection(actorID, groupID uint, in ConnectionInput) (*model.Connection, error) {
	if _, err := RequireRole(actorID, groupID, model.RoleOwner); err != nil {
		return nil, err
	}
	if in.Driver == "" {
		in.Driver = "postgres"
	}
	if in.Driver != "postgres" {
		return nil, errors.New("only postgres is supported for now")
	}
	if in.Name == "" || in.Host == "" || in.Database == "" || in.Username == "" {
		return nil, errors.New("name, host, database, username are required")
	}
	if in.Port == 0 {
		in.Port = 5432
	}
	if in.SSLMode == "" {
		in.SSLMode = "disable"
	}
	enc, err := cryptopkg.EncryptString(in.Password, config.Get().CredentialKey)
	if err != nil {
		return nil, err
	}
	c := &model.Connection{
		GroupID:     groupID,
		Name:        in.Name,
		Driver:      in.Driver,
		Host:        in.Host,
		Port:        in.Port,
		Database:    in.Database,
		Username:    in.Username,
		PasswordEnc: enc,
		SSLMode:     in.SSLMode,
		CreatedByID: actorID,
	}
	if err := db.Meta().Create(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateConnection updates a connection. Owner only.
func UpdateConnection(actorID, connID uint, in ConnectionInput) (*model.Connection, error) {
	var c model.Connection
	if err := db.Meta().First(&c, connID).Error; err != nil {
		return nil, err
	}
	if _, err := RequireRole(actorID, c.GroupID, model.RoleOwner); err != nil {
		return nil, err
	}
	if in.Name != "" {
		c.Name = in.Name
	}
	if in.Host != "" {
		c.Host = in.Host
	}
	if in.Port != 0 {
		c.Port = in.Port
	}
	if in.Database != "" {
		c.Database = in.Database
	}
	if in.Username != "" {
		c.Username = in.Username
	}
	if in.SSLMode != "" {
		c.SSLMode = in.SSLMode
	}
	if in.Password != "" {
		enc, err := cryptopkg.EncryptString(in.Password, config.Get().CredentialKey)
		if err != nil {
			return nil, err
		}
		c.PasswordEnc = enc
	}
	if err := db.Meta().Save(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteConnection removes a connection. Owner only.
func DeleteConnection(actorID, connID uint) error {
	var c model.Connection
	if err := db.Meta().First(&c, connID).Error; err != nil {
		return err
	}
	if _, err := RequireRole(actorID, c.GroupID, model.RoleOwner); err != nil {
		return err
	}
	return db.Meta().Delete(&c).Error
}

// ListConnections returns the connections accessible to user inside group.
func ListConnections(actorID, groupID uint) ([]model.Connection, error) {
	if _, err := RequireRole(actorID, groupID, model.RoleViewer); err != nil {
		return nil, err
	}
	var rows []model.Connection
	if err := db.Meta().Where("group_id = ?", groupID).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetConnection returns a single connection and the user's role inside its group.
func GetConnection(actorID, connID uint) (*model.Connection, model.Role, error) {
	var c model.Connection
	if err := db.Meta().First(&c, connID).Error; err != nil {
		return nil, "", err
	}
	role, err := RequireRole(actorID, c.GroupID, model.RoleViewer)
	if err != nil {
		return nil, "", err
	}
	return &c, role, nil
}

// DecryptPassword decrypts the stored credential password.
func DecryptPassword(c *model.Connection) (string, error) {
	return cryptopkg.DecryptString(c.PasswordEnc, config.Get().CredentialKey)
}

// EncryptForTest encrypts a plaintext password for an in-memory connection
// when testing a draft connection form.
func EncryptForTest(plain string) (string, error) {
	return cryptopkg.EncryptString(plain, config.Get().CredentialKey)
}
