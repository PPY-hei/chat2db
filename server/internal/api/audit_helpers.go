package api

import (
	"encoding/json"
	"time"

	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/gin-gonic/gin"
)

// auditCtx 抽取 gin.Context 中的请求侧上下文（user/ip/ua），用于填充 AuditLog。
type auditCtx struct {
	UserID    uint
	UserEmail string
	IP        string
	UserAgent string
}

func extractAuditCtx(c *gin.Context) auditCtx {
	a := auditCtx{
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	}
	a.UserID = middleware.CurrentUserID(c)
	if u, err := middleware.CurrentUser(c); err == nil {
		a.UserEmail = u.Email
	}
	return a
}

// auditEvent 是一次审计入参的全量描述。
// 使用 struct 而非位置参数，避免 9 个位置参数容易把 GroupID/ConnID、Target/ErrorMsg 写反。
type auditEvent struct {
	Ctx      auditCtx
	Action   model.AuditAction
	GroupID  *uint
	ConnID   *uint
	Target   string
	Detail   any
	Success  bool
	ErrorMsg string
	Started  time.Time
}

// auditRecorder 在 handler 顶部创建，在所有返回路径之前用 defer 提交。
//
// 用法：
//
//	rec := newAuditRecorder(c, model.AuditXxx, started)
//	defer rec.commit()
//	// ... 业务逻辑 ...
//	rec.target = "foo"
//	rec.detail = gin.H{...}
//	rec.success = true   // 仅在 happy path 末尾置 true
//
// 这样消除每个 handler "失败一处 log + 成功一处 log" 的重复。
type auditRecorder struct {
	ac        auditCtx
	action    model.AuditAction
	groupID   *uint
	connID    *uint
	target    string
	detail    any
	success   bool
	errMsg    string
	started   time.Time
	requestID string
}

func newAuditRecorder(c *gin.Context, action model.AuditAction, started time.Time) *auditRecorder {
	return &auditRecorder{
		ac:        extractAuditCtx(c),
		action:    action,
		started:   started,
		requestID: middleware.GetRequestID(c),
	}
}

func (r *auditRecorder) commit() {
	detail := r.detail
	if r.requestID != "" {
		// 把 request_id 写入 detail JSON，便于审计与 HTTP 日志通过同一 ID 串联。
		// 仅当 detail 是 gin.H / nil 时无侵入注入；其它复合类型保持原样。
		switch m := detail.(type) {
		case gin.H:
			m["request_id"] = r.requestID
			detail = m
		case nil:
			detail = gin.H{"request_id": r.requestID}
		}
	}
	logAuditEvent(auditEvent{
		Ctx:      r.ac,
		Action:   r.action,
		GroupID:  r.groupID,
		ConnID:   r.connID,
		Target:   r.target,
		Detail:   detail,
		Success:  r.success,
		ErrorMsg: r.errMsg,
		Started:  r.started,
	})
}

// fail 把 err.Error() 写入 errMsg；调用方仍需 return 终止后续逻辑。
func (r *auditRecorder) fail(err error) {
	if err != nil {
		r.errMsg = err.Error()
	}
}

// logAuditEvent 是埋点最低公共封装。Detail 为任意可序列化 map / struct。
func logAuditEvent(e auditEvent) {
	var detailJSON string
	if e.Detail != nil {
		if b, err := json.Marshal(e.Detail); err == nil {
			detailJSON = string(b)
		} else {
			detailJSON = `{"_marshal_error":` + jsonQuote(err.Error()) + `}`
		}
	} else {
		detailJSON = "{}"
	}

	entry := model.AuditLog{
		UserEmail:  e.Ctx.UserEmail,
		Action:     e.Action,
		GroupID:    e.GroupID,
		ConnID:     e.ConnID,
		Target:     e.Target,
		Detail:     detailJSON,
		Success:    e.Success,
		DurationMS: time.Since(e.Started).Milliseconds(),
		ErrorMsg:   truncate(e.ErrorMsg, 512),
		IP:         e.Ctx.IP,
		UserAgent:  truncate(e.Ctx.UserAgent, 255),
	}
	if e.Ctx.UserID != 0 {
		uid := e.Ctx.UserID
		entry.UserID = &uid
	}
	service.LogAudit(entry)
}

// uptr 把 uint 包成 *uint，便于 GroupID / ConnID 字段赋值。
func uptr(v uint) *uint { return &v }

// uptrOrNil 把 0 视作"无值"返回 nil；用于 draft 连接没有 ID 时填 ConnID。
func uptrOrNil(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// jsonQuote 用于 fallback 错误信息的 JSON 转义，避免引入额外依赖。
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
