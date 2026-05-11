package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/chy/chat2db/server/internal/auth"
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
	var in registerReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	u, err := service.Register(in.Email, in.Name, in.Password)
	if err != nil {
		badRequest(c, err)
		return
	}
	token, err := auth.GenerateToken(u.ID, u.Email)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user": u})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Login(c *gin.Context) {
	var in loginReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	u, err := service.Login(in.Email, in.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	token, err := auth.GenerateToken(u.ID, u.Email)
	if err != nil {
		internal(c, err)
		return
	}
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
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	var in memberReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	var target model.User
	if err := findDB().Where("email = ?", in.Email).First(&target).Error; err != nil {
		badRequest(c, errors.New("user not found"))
		return
	}
	if err := service.AddMember(uid, gid, target.ID, in.Role); err != nil {
		forbidden(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func RemoveMember(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	memberID, err := uintParam(c, "userID")
	if err != nil {
		badRequest(c, err)
		return
	}
	if err := service.RemoveMember(uid, gid, memberID); err != nil {
		forbidden(c, err)
		return
	}
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
	uid := middleware.CurrentUserID(c)
	gid, err := uintParam(c, "groupID")
	if err != nil {
		badRequest(c, err)
		return
	}
	var in service.ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	conn, err := service.CreateConnection(uid, gid, in)
	if err != nil {
		forbidden(c, err)
		return
	}
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
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	var in service.ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	conn, err := service.UpdateConnection(uid, id, in)
	if err != nil {
		forbidden(c, err)
		return
	}
	dbexec.InvalidatePool(conn.ID)
	c.JSON(http.StatusOK, conn)
}

func DeleteConnection(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	if err := service.DeleteConnection(uid, id); err != nil {
		forbidden(c, err)
		return
	}
	dbexec.InvalidatePool(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type testConnReq struct {
	ConnectionID uint                    `json:"connection_id,omitempty"`
	Draft        *service.ConnectionInput `json:"draft,omitempty"`
}

func TestConnection(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	var in testConnReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	var conn *model.Connection
	if in.ConnectionID != 0 {
		got, _, err := service.GetConnection(uid, in.ConnectionID)
		if err != nil {
			forbidden(c, err)
			return
		}
		conn = got
	} else if in.Draft != nil {
		conn = &model.Connection{
			Name: in.Draft.Name, Driver: in.Draft.Driver, Host: in.Draft.Host, Port: in.Draft.Port,
			Database: in.Draft.Database, Username: in.Draft.Username, SSLMode: in.Draft.SSLMode,
		}
		// encrypt in-memory so DecryptPassword works
		enc, err := service.EncryptForTest(in.Draft.Password)
		if err != nil {
			badRequest(c, err)
			return
		}
		conn.PasswordEnc = enc
	} else {
		badRequest(c, errors.New("missing connection_id or draft"))
		return
	}
	if err := dbexec.Ping(c.Request.Context(), conn); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- db operations ---

func ListSchemas(c *gin.Context) {
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
	rows, err := dbexec.ListSchemas(c.Request.Context(), conn)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func ListTables(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	schema := c.Query("schema")
	if schema == "" {
		badRequest(c, errors.New("schema is required"))
		return
	}
	conn, _, err := service.GetConnection(uid, id)
	if err != nil {
		forbidden(c, err)
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
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	schema := c.Query("schema")
	table := c.Query("table")
	if schema == "" || table == "" {
		badRequest(c, errors.New("schema and table are required"))
		return
	}
	conn, _, err := service.GetConnection(uid, id)
	if err != nil {
		forbidden(c, err)
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
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	schema := c.Query("schema")
	table := c.Query("table")
	if schema == "" || table == "" {
		badRequest(c, errors.New("schema and table are required"))
		return
	}
	conn, _, err := service.GetConnection(uid, id)
	if err != nil {
		forbidden(c, err)
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
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "connID")
	if err != nil {
		badRequest(c, err)
		return
	}
	var in executeReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}
	conn, role, err := service.GetConnection(uid, id)
	if err != nil {
		forbidden(c, err)
		return
	}
	if err := sqlguard.CheckAllowed(in.SQL, role); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	stmts := sqlguard.Split(in.SQL)
	results := make([]*dbexec.QueryResult, 0, len(stmts))
	for _, s := range stmts {
		res, err := dbexec.Exec(c.Request.Context(), conn, s)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"results": results, "error": err.Error(), "failed_sql": s})
			return
		}
		results = append(results, res)
	}
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
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func forbidden(c *gin.Context, err error) {
	c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
}

func internal(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func unauthorized(c *gin.Context, err error) {
	c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
}
