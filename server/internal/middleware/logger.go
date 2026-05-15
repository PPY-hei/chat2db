package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger 是 slog 请求日志中间件，替代 gin.Logger() 的非结构化输出。
//
// 输出策略：
//   - 5xx → slog.Error
//   - 4xx → slog.Warn
//   - 其它 → slog.Info
//
// 字段：method / path / status / latency_ms / client_ip / request_id / user_id（已登录时）。
//
// 注意：必须挂在 RequestID 之后，否则 request_id 字段为空。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
			slog.String("client_ip", c.ClientIP()),
			slog.String("request_id", GetRequestID(c)),
		}
		if uid := CurrentUserID(c); uid != 0 {
			attrs = append(attrs, slog.Uint64("user_id", uint64(uid)))
		}

		msg := c.Request.Method + " " + c.Request.URL.Path
		switch {
		case status >= 500:
			slog.LogAttrs(c.Request.Context(), slog.LevelError, msg, attrs...)
		case status >= 400:
			slog.LogAttrs(c.Request.Context(), slog.LevelWarn, msg, attrs...)
		default:
			slog.LogAttrs(c.Request.Context(), slog.LevelInfo, msg, attrs...)
		}
	}
}
