package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/gin-gonic/gin"
)

// createTaskRequest 创建任务请求体。
type createTaskRequest struct {
	GroupID        uint            `json:"group_id"`
	ConnID         uint            `json:"conn_id"`
	TargetConnID   uint            `json:"target_conn_id"` // 目标连接（仅同步任务使用）
	Kind           model.TaskKind  `json:"kind"`
	Scope          model.TaskScope `json:"scope"`
	TargetDatabase string          `json:"target_database"`
	TargetSchema   string          `json:"target_schema"`
	TargetTable    string          `json:"target_table"`
	DestDatabase   string          `json:"dest_database"` // 目标数据库（仅同步任务使用）
	DestSchema     string          `json:"dest_schema"`   // 目标 schema（仅同步任务使用）
	DestTable      string          `json:"dest_table"`    // 目标表（仅同步任务使用）

	ExportFormat        string                           `json:"export_format"`          // csv | insert_sql（仅导出任务使用）
	WhereCondition      string                           `json:"where_condition"`        // 单表导出/数据同步的 WHERE 条件片段
	OnConflictDoNothing bool                             `json:"on_conflict_do_nothing"` // INSERT SQL 冲突时忽略
	ValueReplacements   []service.ExportValueReplacement `json:"value_replacements"`     // INSERT SQL/单表数据同步列值替换
	BackupTable         string                           `json:"backup_table"`           // 单表备份的目标表名
}

// CreateTask 新建任务（创建者需要在目标组中至少为 editor）。
func CreateTask(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	t, err := service.CreateTask(uid, service.CreateTaskParams{
		GroupID:             req.GroupID,
		ConnID:              req.ConnID,
		TargetConnID:        req.TargetConnID,
		Kind:                req.Kind,
		Scope:               req.Scope,
		TargetDatabase:      strings.TrimSpace(req.TargetDatabase),
		TargetSchema:        strings.TrimSpace(req.TargetSchema),
		TargetTable:         strings.TrimSpace(req.TargetTable),
		DestDatabase:        strings.TrimSpace(req.DestDatabase),
		DestSchema:          strings.TrimSpace(req.DestSchema),
		DestTable:           strings.TrimSpace(req.DestTable),
		ExportFormat:        strings.TrimSpace(req.ExportFormat),
		ExportWhere:         strings.TrimSpace(req.WhereCondition),
		OnConflictDoNothing: req.OnConflictDoNothing,
		ValueReplacements:   req.ValueReplacements,
		BackupTable:         strings.TrimSpace(req.BackupTable),
	})
	if err != nil {
		badRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

// ListTasks 列任务。可见性：admin/owner 看自己管理的组 + 自己创建的；普通用户仅看自己创建的。
func ListTasks(c *gin.Context) {
	uid := middleware.CurrentUserID(c)

	manageable, err := service.ManageableGroupIDs(uid)
	if err != nil {
		internal(c, err)
		return
	}

	now := time.Now()
	from, to := parseTimeWindow(c.Query("from"), c.Query("to"), now, 30*24*time.Hour)

	f := service.TaskFilter{
		GroupIDs:   manageable,
		SelfUserID: uid,
		Page:       atoiOr(c.Query("page"), 1),
		PageSize:   atoiOr(c.Query("size"), 20),
		Kind:       strings.TrimSpace(c.Query("kind")),
		Scope:      strings.TrimSpace(c.Query("scope")),
		Status:     strings.TrimSpace(c.Query("status")),
		Keyword:    strings.TrimSpace(c.Query("keyword")),
		From:       &from,
		To:         &to,
	}

	if gidStr := strings.TrimSpace(c.Query("group_id")); gidStr != "" {
		n, err := strconv.ParseUint(gidStr, 10, 64)
		if err != nil {
			badRequest(c, errors.New("invalid group_id"))
			return
		}
		gid := uint(n)
		// 必须是我能管理的组之一；普通用户只能看自己创建的，group_id 二次过滤交给 service
		if !slices.Contains(manageable, gid) {
			forbidden(c, errors.New("group not visible"))
			return
		}
		f.GroupID = &gid
	}
	if cidStr := strings.TrimSpace(c.Query("conn_id")); cidStr != "" {
		n, err := strconv.ParseUint(cidStr, 10, 64)
		if err == nil {
			cid := uint(n)
			f.ConnID = &cid
		}
	}

	page, err := service.QueryTasks(f)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":             page.Items,
		"total":             page.Total,
		"page":              page.Page,
		"size":              page.Size,
		"visible_group_ids": manageable,
	})
}

// GetTaskByID 单条任务（前端轮询用）。
func GetTaskByID(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "id")
	if err != nil {
		badRequest(c, err)
		return
	}
	t, err := service.GetTask(id)
	if err != nil {
		internal(c, err)
		return
	}
	if t == nil {
		c.JSON(http.StatusNotFound, errorBody(c, errors.New("task not found")))
		return
	}
	ok, err := service.CanViewTask(uid, t)
	if err != nil {
		internal(c, err)
		return
	}
	if !ok {
		forbidden(c, errors.New("permission denied"))
		return
	}
	c.JSON(http.StatusOK, t)
}

// CancelTaskByID 取消任务。仅 admin/owner 本组 或 创建者本人可操作。
func CancelTaskByID(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "id")
	if err != nil {
		badRequest(c, err)
		return
	}
	t, err := service.GetTask(id)
	if err != nil {
		internal(c, err)
		return
	}
	if t == nil {
		c.JSON(http.StatusNotFound, errorBody(c, errors.New("task not found")))
		return
	}
	ok, err := service.CanViewTask(uid, t)
	if err != nil {
		internal(c, err)
		return
	}
	if !ok {
		forbidden(c, errors.New("permission denied"))
		return
	}
	if err := service.CancelTask(id); err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteTaskByID 删除任务记录（与可见性一致：admin/owner 本组 或 创建者本人）。
func DeleteTaskByID(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "id")
	if err != nil {
		badRequest(c, err)
		return
	}
	t, err := service.GetTask(id)
	if err != nil {
		internal(c, err)
		return
	}
	if t == nil {
		c.JSON(http.StatusNotFound, errorBody(c, errors.New("task not found")))
		return
	}
	ok, err := service.CanViewTask(uid, t)
	if err != nil {
		internal(c, err)
		return
	}
	if !ok {
		forbidden(c, errors.New("permission denied"))
		return
	}
	// 删除产物目录（best-effort）
	if t.FilePath != "" {
		artDir := service.TaskArtifactDir()
		taskDir := filepath.Join(artDir, strconv.FormatUint(uint64(id), 10))
		_ = os.RemoveAll(taskDir)
	}
	if err := service.DeleteTask(id); err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DownloadTaskArtifact 下载任务产物（仅 succeeded 且可见）。
func DownloadTaskArtifact(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	id, err := uintParam(c, "id")
	if err != nil {
		badRequest(c, err)
		return
	}
	t, err := service.GetTask(id)
	if err != nil {
		internal(c, err)
		return
	}
	if t == nil {
		c.JSON(http.StatusNotFound, errorBody(c, errors.New("task not found")))
		return
	}
	ok, err := service.CanViewTask(uid, t)
	if err != nil {
		internal(c, err)
		return
	}
	if !ok {
		forbidden(c, errors.New("permission denied"))
		return
	}
	if t.Status != model.TaskStatusSucceeded || t.FilePath == "" {
		badRequest(c, errors.New("task artifact is not available"))
		return
	}
	// 路径越界防护：必须位于 artifactDir 之下
	artDir := service.TaskArtifactDir()
	absArt, err := filepath.Abs(artDir)
	if err != nil {
		internal(c, fmt.Errorf("resolve artifact dir: %w", err))
		return
	}
	absFile, err := filepath.Abs(t.FilePath)
	if err != nil {
		internal(c, fmt.Errorf("resolve file path: %w", err))
		return
	}
	// 确保文件路径在产物目录内（加上路径分隔符防止前缀绕过）
	if !strings.HasPrefix(absFile, absArt+string(os.PathSeparator)) {
		forbidden(c, errors.New("invalid artifact path"))
		return
	}
	c.FileAttachment(t.FilePath, filepath.Base(t.FilePath))
}
