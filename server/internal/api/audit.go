package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/gin-gonic/gin"
)

// QueryAuditLogs 列出当前用户可见的审计日志。
//
// 权限规则：
//   - 调用者需要在至少一个组里是 admin/owner，否则 403。
//   - 默认返回这些组里的事件，并叠加调用者自身的"无组事件"
//     （登录 / 注册 group_id IS NULL AND user_id = self）。
//   - 指定 group_id 时仅返回该组事件，且不叠加自身无组事件。
//
// Query 参数：
//   - from / to       RFC3339 时间窗口；默认最近 7 天
//   - actions         逗号分隔的 action 列表
//   - keyword         模糊匹配 user_email / target / detail
//   - only_fail       1/true 表示只看失败事件
//   - page / size     分页
//   - group_id        指定单一组（必须是 visible 之一）
func QueryAuditLogs(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	visible, err := service.VisibleGroupIDsForAudit(uid)
	if err != nil {
		internal(c, err)
		return
	}
	if len(visible) == 0 {
		forbidden(c, errors.New("you must be admin or owner of at least one group to view audit logs"))
		return
	}

	now := time.Now()
	from, to := parseTimeWindow(c.Query("from"), c.Query("to"), now, 7*24*time.Hour)

	f := service.AuditFilter{
		From:     &from,
		To:       &to,
		Page:     atoiOr(c.Query("page"), 1),
		PageSize: atoiOr(c.Query("size"), 50),
		OnlyFail: isTruthy(c.Query("only_fail")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Actions:  parseActions(c.Query("actions")),
	}

	if gidStr := strings.TrimSpace(c.Query("group_id")); gidStr != "" {
		n, err := strconv.ParseUint(gidStr, 10, 64)
		if err != nil {
			badRequest(c, errors.New("invalid group_id"))
			return
		}
		gid := uint(n)
		if !slices.Contains(visible, gid) {
			forbidden(c, errors.New("group not visible"))
			return
		}
		f.GroupIDs = []uint{gid}
		// 限定具体组：不再叠加自身无组事件，避免数据混杂。
	} else {
		f.GroupIDs = visible
		f.SelfUserID = uid
	}

	page, err := service.QueryAudit(f)
	if err != nil {
		internal(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":             page.Items,
		"total":             page.Total,
		"page":              page.Page,
		"size":              page.Size,
		"visible_group_ids": visible,
	})
}

// ListAuditActions 列出所有 action 枚举，供前端构造下拉。
func ListAuditActions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"actions": []model.AuditAction{
		model.AuditSQLExecute,
		model.AuditAuthLoginSuccess,
		model.AuditAuthLoginFail,
		model.AuditAuthRegister,
		model.AuditMemberAdd,
		model.AuditMemberRemove,
		model.AuditMemberUpdate,
		model.AuditConnectionCreate,
		model.AuditConnectionUpdate,
		model.AuditConnectionDelete,
		model.AuditConnectionTest,
	}})
}

func parseTimeWindow(fromStr, toStr string, now time.Time, defaultSpan time.Duration) (time.Time, time.Time) {
	to := now
	if t, err := time.Parse(time.RFC3339, toStr); err == nil {
		to = t
	}
	from := to.Add(-defaultSpan)
	if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
		from = t
	}
	return from, to
}

func parseActions(raw string) []model.AuditAction {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]model.AuditAction, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, model.AuditAction(v))
		}
	}
	return out
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y":
		return true
	}
	return false
}
