package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/chy/chat2db/server/internal/auth"
	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/dbexec"
	"github.com/chy/chat2db/server/internal/llm"
	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/chy/chat2db/server/internal/sqlguard"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func findDB() *gorm.DB { return db.Meta() }

// --- auth ---

type registerReq struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func Register(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditAuthRegister, time.Now())
	defer rec.commit()

	var in registerReq
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.ac.UserEmail = in.Email
	rec.target = in.Email
	rec.detail = gin.H{"name": in.Name}

	u, err := service.Register(in.Email, in.Name, in.Password)
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.ac.UserID = u.ID

	token, err := auth.GenerateToken(u.ID, u.Email)
	if err != nil {
		rec.fail(err)
		internal(c, err)
		return
	}
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"token": token, "user": u})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Login(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditAuthLoginSuccess, time.Now())
	defer rec.commit()

	var in loginReq
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		rec.action = model.AuditAuthLoginFail
		badRequest(c, err)
		return
	}
	rec.ac.UserEmail = in.Email
	rec.target = in.Email

	u, err := service.Login(in.Email, in.Password)
	if err != nil {
		rec.fail(err)
		rec.action = model.AuditAuthLoginFail
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	rec.ac.UserID = u.ID

	token, err := auth.GenerateToken(u.ID, u.Email)
	if err != nil {
		rec.fail(err)
		rec.action = model.AuditAuthLoginFail
		internal(c, err)
		return
	}
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"token": token, "user": u})
}

func Me(c *gin.Context) {
	u, err := middleware.CurrentUser(c)
	if err != nil {
		unauthorized(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u, "llm_configured": u.LLMAPIKeyEnc != ""})
}

type llmReq struct {
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

func UpdateLLM(c *gin.Context) {
	u, err := middleware.CurrentUser(c)
	if err != nil {
		unauthorized(c, err)
		return
	}
	var in llmReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	if err := service.UpdateLLM(u.ID, in.Endpoint, in.Model, in.APIKey); err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- groups ---

type groupReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func CreateGroup(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	var in groupReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	g, err := service.CreateGroup(uid, in.Name, in.Description)
	if err != nil {
		badRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, g)
}

func ListGroups(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	gs, err := service.ListGroupsForUser(uid)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gs)
}

func UpdateGroup(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	var in service.UpdateGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	g, err := service.UpdateGroup(uid, gid, in)
	if err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, g)
}

type memberReq struct {
	Email string     `json:"email"`
	Role  model.Role `json:"role"`
}

func AddMember(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditMemberAdd, time.Now())
	defer rec.commit()

	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.groupID = uptr(gid)

	var in memberReq
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.target = in.Email
	rec.detail = gin.H{"role": in.Role}

	var target model.User
	if err := findDB().Where("email = ?", in.Email).First(&target).Error; err != nil {
		rec.errMsg = "user not found"
		badRequest(c, errors.New("user not found"))
		return
	}
	// 复用现有成员判断走 update 语义
	var existing model.GroupMember
	if err := findDB().Where("group_id = ? AND user_id = ?", gid, target.ID).First(&existing).Error; err == nil {
		rec.action = model.AuditMemberUpdate
	}
	if err := service.AddMember(uid, gid, target.ID, in.Role); err != nil {
		rec.fail(err)
		forbidden(c, err)
		return
	}
	rec.detail = gin.H{"role": in.Role, "target_user_id": target.ID}
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func RemoveMember(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditMemberRemove, time.Now())
	defer rec.commit()

	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.groupID = uptr(gid)

	memberID, err := uintParam(c, "userID")
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	// 提前取一次被移除用户的 email 作 target，让审计日志可读
	rec.target = fmt.Sprintf("#%d", memberID)
	var member model.User
	if err := findDB().Select("email").First(&member, memberID).Error; err == nil {
		rec.target = member.Email
	}
	rec.detail = gin.H{"target_user_id": memberID}

	if err := service.RemoveMember(uid, gid, memberID); err != nil {
		rec.fail(err)
		forbidden(c, err)
		return
	}
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func ListMembers(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	ms, err := service.ListMembers(uid, gid)
	if err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, ms)
}

// --- connections ---

func CreateConnection(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditConnectionCreate, time.Now())
	defer rec.commit()

	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.groupID = uptr(gid)

	var in service.ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.target = in.Name
	rec.detail = gin.H{"driver": in.Driver, "host": in.Host, "port": in.Port, "database": in.Database}

	conn, err := service.CreateConnection(uid, gid, in)
	if err != nil {
		rec.fail(err)
		forbidden(c, err)
		return
	}
	rec.connID = uptr(conn.ID)
	rec.target = conn.Name
	rec.detail = gin.H{"driver": conn.Driver, "host": conn.Host, "port": conn.Port, "database": conn.Database}
	rec.success = true
	c.JSON(http.StatusOK, conn)
}

func ListConnections(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	rows, err := service.ListConnections(uid, gid)
	if err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func UpdateConnection(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditConnectionUpdate, time.Now())
	defer rec.commit()

	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.connID = uptr(id)

	var in service.ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.target = in.Name
	rec.detail = gin.H{"driver": in.Driver, "host": in.Host, "port": in.Port, "database": in.Database}

	conn, err := service.UpdateConnection(uid, id, in)
	if err != nil {
		rec.fail(err)
		forbidden(c, err)
		return
	}
	dbexec.InvalidatePool(conn.ID)
	rec.groupID = uptr(conn.GroupID)
	rec.target = conn.Name
	rec.detail = gin.H{"driver": conn.Driver, "host": conn.Host, "port": conn.Port, "database": conn.Database}
	rec.success = true
	c.JSON(http.StatusOK, conn)
}

func DeleteConnection(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditConnectionDelete, time.Now())
	defer rec.commit()

	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.connID = uptr(id)
	// 提前取一次连接信息以便审计 target / groupID 字段；失败也走删除路径，不阻断。
	rec.target = fmt.Sprintf("#%d", id)
	if conn, _, err := service.GetConnection(uid, id); err == nil {
		rec.target = conn.Name
		rec.groupID = uptr(conn.GroupID)
	}

	if err := service.DeleteConnection(uid, id); err != nil {
		rec.fail(err)
		forbidden(c, err)
		return
	}
	dbexec.InvalidatePool(id)
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type testConnReq struct {
	ConnectionID uint                     `json:"connection_id,omitempty"`
	Draft        *service.ConnectionInput `json:"draft,omitempty"`
}

// ListDrivers 返回已注册的数据源驱动及其 Capabilities，供前端动态渲染
// 连接表单（端口占位、SSL 下拉、SSH/mTLS 显隐等）。
//
// 这是一个无状态、无用户维度数据的只读接口，因此挂在公开路径上，
// 便于登录页的"新建连接 / 测试连接"在未登录时也能拿到驱动列表。
func ListDrivers(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"drivers": dbexec.ListDrivers()})
}

func TestConnection(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditConnectionTest, time.Now())
	defer rec.commit()

	uid := middleware.CurrentUserID(c)
	var in testConnReq
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.connID = uptrOrNil(in.ConnectionID)

	var conn *model.Connection
	if in.ConnectionID != 0 {
		got, _, err := service.GetConnection(uid, in.ConnectionID)
		if err != nil {
			rec.fail(err)
			forbidden(c, err)
			return
		}
		conn = got
	} else if in.Draft != nil {
		key := config.Get().CredentialKey
		conn = &model.Connection{
			Name: in.Draft.Name, Driver: in.Draft.Driver, Host: in.Draft.Host, Port: in.Draft.Port,
			Database: in.Draft.Database, Username: in.Draft.Username, SSLMode: in.Draft.SSLMode,
			SSHEnabled: in.Draft.SSHEnabled, SSHHost: in.Draft.SSHHost, SSHPort: in.Draft.SSHPort,
			SSHUser: in.Draft.SSHUser, SSHAuthMethod: in.Draft.SSHAuthMethod,
		}
		// encrypt in-memory so DecryptPassword works
		enc, err := service.EncryptForTest(in.Draft.Password)
		if err != nil {
			rec.fail(err)
			badRequest(c, err)
			return
		}
		conn.PasswordEnc = enc
		// SSH 凭据
		if in.Draft.SSHPassword != "" {
			enc, err := cryptopkg.EncryptString(in.Draft.SSHPassword, key)
			if err != nil {
				rec.fail(err)
				badRequest(c, err)
				return
			}
			conn.SSHPasswordEnc = enc
		}
		if in.Draft.SSHPrivateKey != "" {
			enc, err := cryptopkg.EncryptString(in.Draft.SSHPrivateKey, key)
			if err != nil {
				rec.fail(err)
				badRequest(c, err)
				return
			}
			conn.SSHPrivateKeyEnc = enc
		}
		if in.Draft.SSHPassphrase != "" {
			enc, err := cryptopkg.EncryptString(in.Draft.SSHPassphrase, key)
			if err != nil {
				rec.fail(err)
				badRequest(c, err)
				return
			}
			conn.SSHPassphraseEnc = enc
		}
		// SSL 证书
		if in.Draft.SSLCACert != "" {
			enc, err := cryptopkg.EncryptString(in.Draft.SSLCACert, key)
			if err != nil {
				rec.fail(err)
				badRequest(c, err)
				return
			}
			conn.SSLCACertEnc = enc
		}
		if in.Draft.SSLClientCert != "" {
			enc, err := cryptopkg.EncryptString(in.Draft.SSLClientCert, key)
			if err != nil {
				rec.fail(err)
				badRequest(c, err)
				return
			}
			conn.SSLClientCertEnc = enc
		}
		if in.Draft.SSLClientKey != "" {
			enc, err := cryptopkg.EncryptString(in.Draft.SSLClientKey, key)
			if err != nil {
				rec.fail(err)
				badRequest(c, err)
				return
			}
			conn.SSLClientKeyEnc = enc
		}
		if conn.SSHPort == 0 {
			conn.SSHPort = 22
		}
	} else {
		err := errors.New("missing connection_id or draft")
		rec.fail(err)
		badRequest(c, err)
		return
	}
	rec.target = conn.Name
	rec.groupID = uptrOrNil(conn.GroupID)
	rec.detail = gin.H{"host": conn.Host, "port": conn.Port, "driver": conn.Driver}

	if err := dbexec.Ping(c.Request.Context(), conn); err != nil {
		rec.fail(err)
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- db operations ---

// resolveConn 获取连接并根据可选的 ?database= 参数临时切换数据库。
func resolveConn(c *gin.Context) (*model.Connection, model.Role, error) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		return nil, "", err
	}
	conn, role, err := service.GetConnection(uid, id)
	if err != nil {
		return nil, "", err
	}
	if db := c.Query("database"); db != "" {
		conn = dbexec.WithDatabase(conn, db)
	}
	return conn, role, nil
}

func ListDatabases(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	conn, _, err := service.GetConnection(uid, id)
	if err != nil {
		forbidden(c, err)
		return
	}
	rows, err := dbexec.ListDatabases(c.Request.Context(), conn)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func ListSchemas(c *gin.Context) {
	conn, _, err := resolveConn(c)
	if err != nil {
		forbidden(c, err)
		return
	}
	rows, err := dbexec.ListSchemas(c.Request.Context(), conn)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func ListTables(c *gin.Context) {
	conn, _, err := resolveConn(c)
	if err != nil {
		forbidden(c, err)
		return
	}
	schema := c.Query("schema")
	if schema == "" {
		badRequest(c, errors.New("schema is required"))
		return
	}
	rows, err := dbexec.ListTables(c.Request.Context(), conn, schema)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func ListColumns(c *gin.Context) {
	conn, _, err := resolveConn(c)
	if err != nil {
		forbidden(c, err)
		return
	}
	schema := c.Query("schema")
	table := c.Query("table")
	if schema == "" || table == "" {
		badRequest(c, errors.New("schema and table are required"))
		return
	}
	rows, err := dbexec.ListColumns(c.Request.Context(), conn, schema, table)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// TableDDL 返回给定表的可读 DDL（含列/主键/索引/注释），供前端查看和 AI 上下文。
func TableDDL(c *gin.Context) {
	conn, _, err := resolveConn(c)
	if err != nil {
		forbidden(c, err)
		return
	}
	schema := c.Query("schema")
	table := c.Query("table")
	if schema == "" || table == "" {
		badRequest(c, errors.New("schema and table are required"))
		return
	}
	ddl, err := dbexec.GenerateTableDDL(c.Request.Context(), conn, schema, table)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"schema": schema, "table": table, "ddl": ddl})
}

type executeReq struct {
	SQL string `json:"sql"`
}

func ExecuteSQL(c *gin.Context) {
	rec := newAuditRecorder(c, model.AuditSQLExecute, time.Now())
	defer rec.commit()

	conn, role, err := resolveConn(c)
	if err != nil {
		rec.fail(err)
		forbidden(c, err)
		return
	}
	rec.groupID = uptr(conn.GroupID)
	rec.connID = uptr(conn.ID)

	var in executeReq
	if err := c.ShouldBindJSON(&in); err != nil {
		rec.fail(err)
		badRequest(c, err)
		return
	}
	auditTarget := conn.Database
	if s := c.Query("database"); s != "" {
		auditTarget = s
	}
	rec.target = auditTarget
	rec.detail = gin.H{"sql": in.SQL, "role": role}

	if err := sqlguard.CheckAllowed(in.SQL, role); err != nil {
		rec.fail(err)
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	stmts := sqlguard.Split(in.SQL)
	results := make([]*dbexec.QueryResult, 0, len(stmts))
	for _, s := range stmts {
		res, err := dbexec.Exec(c.Request.Context(), conn, s)
		if err != nil {
			rec.fail(err)
			rec.detail = gin.H{"sql": in.SQL, "role": role, "failed_sql": s}
			c.JSON(http.StatusOK, gin.H{"results": results, "error": err.Error(), "failed_sql": s})
			return
		}
		results = append(results, res)
	}
	rec.detail = gin.H{"sql": in.SQL, "role": role, "stmt_count": len(stmts)}
	rec.success = true
	c.JSON(http.StatusOK, gin.H{"results": results, "role": role})
}

// --- saved queries ---

func CreateSavedQuery(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	var in service.SaveQueryInput
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	sq, err := service.CreateSavedQuery(uid, in)
	if err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, sq)
}

func DeleteSavedQuery(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "id")
	if err != nil {
		badRequest(c, err)
		return
	}
	if err := service.DeleteSavedQuery(uid, id); err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func ListGroupSavedQueries(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	rows, err := service.ListGroupSavedQueries(uid, gid)
	if err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func ListMySavedQueries(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	rows, err := service.ListMySavedQueries(uid)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// --- AI chat ---

func AIChat(c *gin.Context) {
	u, err := middleware.CurrentUser(c)
	if err != nil {
		unauthorized(c, err)
		return
	}
	var in llm.ChatRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	resp, err := llm.Chat(ctx, u, in)
	if err != nil {
		if errors.Is(err, llm.ErrNotConfigured) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// --- helpers ---

func uintParam(c *gin.Context, key string) (uint, error) {
	v := c.Param(key)
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, errors.New("invalid " + key)
	}
	return uint(n), nil
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, errorBody(c, err))
}

func forbidden(c *gin.Context, err error) {
	c.JSON(http.StatusForbidden, errorBody(c, err))
}

func internal(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, errorBody(c, err))
}

func unauthorized(c *gin.Context, err error) {
	c.JSON(http.StatusUnauthorized, errorBody(c, err))
}

// errorBody 是统一错误响应体：保留原有 "error" 字段（向后兼容），
// 同时附上 "request_id" 便于用户报错截图直接对应到 HTTP 日志 / 审计记录。
func errorBody(c *gin.Context, err error) gin.H {
	return gin.H{
		"error":      err.Error(),
		"request_id": middleware.GetRequestID(c),
	}
}
