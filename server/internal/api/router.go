package api

import (
	"net/http"

	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes wires all HTTP routes onto the gin engine.
func RegisterRoutes(r *gin.Engine) {
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	r.GET("/api/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	api := r.Group("/api")
	api.POST("/auth/register", Register)
	api.POST("/auth/login", Login)
	// 驱动能力描述：无状态只读，不需要鉴权；前端登录页构造连接表单会用到。
	api.GET("/drivers", ListDrivers)

	authed := api.Group("/")
	authed.Use(middleware.AuthRequired())
	{
		authed.GET("/me", Me)
		authed.PUT("/me/llm", UpdateLLM)

		// groups
		authed.GET("/groups", ListGroups)
		authed.POST("/groups", CreateGroup)
		authed.PUT("/groups/:groupID", UpdateGroup)
		authed.GET("/groups/:groupID/members", ListMembers)
		authed.POST("/groups/:groupID/members", AddMember)
		authed.DELETE("/groups/:groupID/members/:userID", RemoveMember)

		// connections under a group
		authed.GET("/groups/:groupID/connections", ListConnections)
		authed.POST("/groups/:groupID/connections", CreateConnection)
		authed.PUT("/connections/:connID", UpdateConnection)
		authed.DELETE("/connections/:connID", DeleteConnection)
		authed.POST("/connections/test", TestConnection)

		// schema browsing & execution
		authed.GET("/connections/:connID/databases", ListDatabases)
		authed.GET("/connections/:connID/schemas", ListSchemas)
		authed.GET("/connections/:connID/tables", ListTables)
		authed.GET("/connections/:connID/columns", ListColumns)
		authed.GET("/connections/:connID/ddl", TableDDL)
		authed.POST("/connections/:connID/execute", ExecuteSQL)

		// saved queries
		authed.GET("/groups/:groupID/saved-queries", ListGroupSavedQueries)
		authed.GET("/me/saved-queries", ListMySavedQueries)
		authed.POST("/saved-queries", CreateSavedQuery)
		authed.DELETE("/saved-queries/:id", DeleteSavedQuery)

		// AI
		authed.POST("/ai/chat", AIChat)
	}
}
