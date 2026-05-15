package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery 是 panic 恢复中间件，替代 gin.Recovery()：
//   - panic 时用 slog.Error 输出完整 stack。
//   - 响应 500 + JSON {error, request_id}，便于前端展示给用户反查。
//
// 必须挂在 RequestID 之后，否则 stack 无法关联到具体请求。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				rid := GetRequestID(c)
				slog.Error("panic recovered",
					slog.String("request_id", rid),
					slog.String("method", c.Request.Method),
					slog.String("path", c.Request.URL.Path),
					slog.String("error", fmt.Sprint(r)),
					slog.String("stack", string(debug.Stack())),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error":      "Internal Server Error",
					"request_id": rid,
				})
			}
		}()
		c.Next()
	}
}
