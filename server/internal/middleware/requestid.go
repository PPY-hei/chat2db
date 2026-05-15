package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// HeaderXRequestID 是 request_id 在 HTTP 头中的标准位置。
// 如果上游网关 / 反向代理已经注入，则透传；否则本地生成。
const HeaderXRequestID = "X-Request-ID"

// CtxRequestID 是 gin.Context 中存放 request_id 的 key。
const CtxRequestID = "requestID"

// RequestID 中间件：写入 gin.Context 与响应头，供后续中间件 / handler / 审计层使用。
//
// 取值优先级：
//  1. 请求头 X-Request-ID（允许上游统一分配，便于跨服务追溯）。
//  2. 本地随机生成 32 字符 hex（128 bit），冲突概率可忽略。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderXRequestID)
		if id == "" {
			id = generateRequestID()
		}
		c.Set(CtxRequestID, id)
		c.Header(HeaderXRequestID, id)
		c.Next()
	}
}

// GetRequestID 从 gin.Context 取出 request_id；未设置时返回空串。
func GetRequestID(c *gin.Context) string {
	v, _ := c.Get(CtxRequestID)
	s, _ := v.(string)
	return s
}

// generateRequestID 用 crypto/rand 生成 16 字节随机 → 32 字符 hex。
// 故意不依赖 google/uuid，避免引入仅为 ID 生成的第三方依赖。
func generateRequestID() string {
	b := make([]byte, 16)
	// crypto/rand.Read 在系统熵源可用时不会失败；极端情况下退化为零值不影响功能。
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
