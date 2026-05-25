package service

import (
	"errors"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/dbexec"
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
	// SSL 证书（PEM 文本明文，服务端加密后存储）
	SSLCACert    string `json:"ssl_ca_cert,omitempty"`
	SSLClientCert string `json:"ssl_client_cert,omitempty"`
	SSLClientKey  string `json:"ssl_client_key,omitempty"`
	// SSH 隧道
	SSHEnabled    bool   `json:"ssh_enabled"`
	SSHHost       string `json:"ssh_host"`
	SSHPort       int    `json:"ssh_port"`
	SSHUser       string `json:"ssh_user"`
	SSHAuthMethod string `json:"ssh_auth_method"` // "password" | "privatekey"
	SSHPassword   string `json:"ssh_password,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	SSHPassphrase string `json:"ssh_passphrase,omitempty"`
	// HTTP/SOCKS5 代理（与 SSH 互斥）
	ProxyEnabled  bool   `json:"proxy_enabled"`
	ProxyType     string `json:"proxy_type"` // "http" | "socks5"
	ProxyHost     string `json:"proxy_host"`
	ProxyPort     int    `json:"proxy_port"`
	ProxyUsername string `json:"proxy_username"`
	ProxyPassword string `json:"proxy_password,omitempty"`
}

func encryptOptional(plain string, key []byte) (string, error) {
	if plain == "" {
		return "", nil
	}
	return cryptopkg.EncryptString(plain, key)
}

// CreateConnection creates a new connection in the group. Owner only.
func CreateConnection(actorID, groupID uint, in ConnectionInput) (*model.Connection, error) {
	if _, err := RequireRole(actorID, groupID, model.RoleOwner); err != nil {
		return nil, err
	}
	if in.Driver == "" {
		in.Driver = "postgres"
	}
	// Open 通过注册表验证驱动合法性，同时拿到 Capabilities 以填默认端口。
	// 把"支持哪些 driver"这一真相源收敛到 dbexec registry。
	driver, err := dbexec.Open(&model.Connection{Driver: in.Driver})
	if err != nil {
		return nil, err
	}
	if in.Name == "" || in.Host == "" || in.Database == "" || in.Username == "" {
		return nil, errors.New("name, host, database, username are required")
	}
	if in.Port == 0 {
		in.Port = driver.Capabilities().DefaultPort
	}
	if in.SSLMode == "" {
		in.SSLMode = "disable"
	}
	key := config.Get().CredentialKey
	enc, err := cryptopkg.EncryptString(in.Password, key)
	if err != nil {
		return nil, err
	}
	c := &model.Connection{
		GroupID:     groupID,
		Name:       in.Name,
		Driver:     in.Driver,
		Host:       in.Host,
		Port:       in.Port,
		Database:   in.Database,
		Username:   in.Username,
		PasswordEnc: enc,
		SSLMode:    in.SSLMode,
		CreatedByID: actorID,
		SSHEnabled: in.SSHEnabled,
		ProxyEnabled: in.ProxyEnabled,
	}
	// SSL 证书
	if c.SSLCACertEnc, err = encryptOptional(in.SSLCACert, key); err != nil {
		return nil, err
	}
	if c.SSLClientCertEnc, err = encryptOptional(in.SSLClientCert, key); err != nil {
		return nil, err
	}
	if c.SSLClientKeyEnc, err = encryptOptional(in.SSLClientKey, key); err != nil {
		return nil, err
	}
	// SSH
	if in.SSHEnabled {
		if in.SSHHost == "" || in.SSHUser == "" {
			return nil, errors.New("ssh_host and ssh_user are required when SSH is enabled")
		}
		c.SSHHost = in.SSHHost
		c.SSHPort = in.SSHPort
		if c.SSHPort == 0 {
			c.SSHPort = 22
		}
		c.SSHUser = in.SSHUser
		c.SSHAuthMethod = in.SSHAuthMethod
		if c.SSHAuthMethod == "" {
			c.SSHAuthMethod = "password"
		}
		if c.SSHPasswordEnc, err = encryptOptional(in.SSHPassword, key); err != nil {
			return nil, err
		}
		if c.SSHPrivateKeyEnc, err = encryptOptional(in.SSHPrivateKey, key); err != nil {
			return nil, err
		}
		if c.SSHPassphraseEnc, err = encryptOptional(in.SSHPassphrase, key); err != nil {
			return nil, err
		}
	}
	// 代理
	if in.ProxyEnabled {
		if in.ProxyHost == "" {
			return nil, errors.New("proxy_host is required when proxy is enabled")
		}
		c.ProxyType = in.ProxyType
		if c.ProxyType == "" {
			c.ProxyType = "http"
		}
		c.ProxyHost = in.ProxyHost
		c.ProxyPort = in.ProxyPort
		c.ProxyUsername = in.ProxyUsername
		if c.ProxyPasswordEnc, err = encryptOptional(in.ProxyPassword, key); err != nil {
			return nil, err
		}
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
	key := config.Get().CredentialKey
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
		enc, err := cryptopkg.EncryptString(in.Password, key)
		if err != nil {
			return nil, err
		}
		c.PasswordEnc = enc
	}
	// SSL 证书：如果前端传了值则更新（传空字符串也可以用于清空）
	if in.SSLCACert != "" {
		enc, err := encryptOptional(in.SSLCACert, key)
		if err != nil {
			return nil, err
		}
		c.SSLCACertEnc = enc
	}
	if in.SSLClientCert != "" {
		enc, err := encryptOptional(in.SSLClientCert, key)
		if err != nil {
			return nil, err
		}
		c.SSLClientCertEnc = enc
	}
	if in.SSLClientKey != "" {
		enc, err := encryptOptional(in.SSLClientKey, key)
		if err != nil {
			return nil, err
		}
		c.SSLClientKeyEnc = enc
	}
	// SSH 隧道
	c.SSHEnabled = in.SSHEnabled
	if in.SSHEnabled {
		if in.SSHHost != "" {
			c.SSHHost = in.SSHHost
		}
		if in.SSHPort != 0 {
			c.SSHPort = in.SSHPort
		}
		if in.SSHUser != "" {
			c.SSHUser = in.SSHUser
		}
		if in.SSHAuthMethod != "" {
			c.SSHAuthMethod = in.SSHAuthMethod
		}
		if in.SSHPassword != "" {
			enc, err := encryptOptional(in.SSHPassword, key)
			if err != nil {
				return nil, err
			}
			c.SSHPasswordEnc = enc
		}
		if in.SSHPrivateKey != "" {
			enc, err := encryptOptional(in.SSHPrivateKey, key)
			if err != nil {
				return nil, err
			}
			c.SSHPrivateKeyEnc = enc
		}
		if in.SSHPassphrase != "" {
			enc, err := encryptOptional(in.SSHPassphrase, key)
			if err != nil {
				return nil, err
			}
			c.SSHPassphraseEnc = enc
		}
	} else {
		c.SSHHost = ""
		c.SSHPort = 0
		c.SSHUser = ""
		c.SSHAuthMethod = ""
		c.SSHPasswordEnc = ""
		c.SSHPrivateKeyEnc = ""
		c.SSHPassphraseEnc = ""
	}
	// 代理
	c.ProxyEnabled = in.ProxyEnabled
	if in.ProxyEnabled {
		if in.ProxyType != "" {
			c.ProxyType = in.ProxyType
		}
		if c.ProxyType == "" {
			c.ProxyType = "http"
		}
		if in.ProxyHost != "" {
			c.ProxyHost = in.ProxyHost
		}
		if in.ProxyPort != 0 {
			c.ProxyPort = in.ProxyPort
		}
		// 用户名允许置空（关闭认证），故只要开启代理就同步
		c.ProxyUsername = in.ProxyUsername
		if in.ProxyPassword != "" {
			enc, err := encryptOptional(in.ProxyPassword, key)
			if err != nil {
				return nil, err
			}
			c.ProxyPasswordEnc = enc
		}
	} else {
		c.ProxyType = ""
		c.ProxyHost = ""
		c.ProxyPort = 0
		c.ProxyUsername = ""
		c.ProxyPasswordEnc = ""
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

// EncryptForTest encrypts a plaintext password for an in-memory connection
// when testing a draft connection form.
func EncryptForTest(plain string) (string, error) {
	return cryptopkg.EncryptString(plain, config.Get().CredentialKey)
}
