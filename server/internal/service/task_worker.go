package service

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
)

const taskQueueCapacity = 256

var (
	taskQueue           chan uint
	taskWG              sync.WaitGroup
	taskOnce            sync.Once
	taskCancel          context.CancelFunc
	taskDroppedTotal    int64

	// taskRunningCancels 记录运行中任务的 ctx cancel，供未来扩展硬取消。
	// 当前 worker 是单 goroutine 串行执行，最多 1 个条目，但用 map 保留扩展性。
	taskRunningCancels   = make(map[uint]context.CancelFunc)
	taskRunningCancelsMu sync.Mutex

	// taskArtifactDir 是产物根目录；StartTaskWorker 时初始化，handler 下载时复用。
	taskArtifactDir string
)

// StartTaskWorker 启动任务 worker，串行处理队列。artifactDir 是产物落地目录。
// 多次调用是 no-op。
func StartTaskWorker(artifactDir string) {
	taskOnce.Do(func() {
		taskArtifactDir = artifactDir
		taskQueue = make(chan uint, taskQueueCapacity)
		_, cancel := context.WithCancel(context.Background())
		taskCancel = cancel

		// 启动时把残留的 pending/running（上次 crash）重置为 failed。
		// 重启场景下 worker 没有它们的 cancel 句柄，无法续跑。
		if err := db.Meta().Model(&model.Task{}).
			Where("status IN ?", []model.TaskStatus{model.TaskStatusPending, model.TaskStatusRunning}).
			Updates(map[string]any{
				"status":    model.TaskStatusFailed,
				"error_msg": "server restarted before task finished",
			}).Error; err != nil {
			slog.Warn("task worker: failed to reset stale tasks", slog.Any("error", err))
		}

		taskWG.Add(1)
		go runTaskWorker()
	})
}

// StopTaskWorker 关闭队列，等 worker 退出。
func StopTaskWorker() {
	if taskCancel == nil {
		return
	}
	close(taskQueue)
	taskCancel()
	taskWG.Wait()
}

// TaskArtifactDir 返回当前任务产物根目录（供 handler 拼下载路径）。
func TaskArtifactDir() string { return taskArtifactDir }

// TaskDroppedTotal 暴露累计丢弃数。
func TaskDroppedTotal() int64 { return atomic.LoadInt64(&taskDroppedTotal) }

func enqueueTask(id uint) {
	if taskQueue == nil {
		slog.Warn("task queue not started, task pending forever",
			slog.Uint64("task_id", uint64(id)))
		return
	}
	select {
	case taskQueue <- id:
	default:
		atomic.AddInt64(&taskDroppedTotal, 1)
		// 标记任务直接失败，避免 pending 永久挂起
		_ = updateTaskFields(id, map[string]any{
			"status":    model.TaskStatusFailed,
			"error_msg": "task queue full, dropped",
		})
		slog.Warn("task queue full, dropped",
			slog.Uint64("task_id", uint64(id)))
	}
}

func runTaskWorker() {
	defer taskWG.Done()
	for id := range taskQueue {
		processTask(id)
	}
}

func processTask(id uint) {
	t, err := GetTask(id)
	if err != nil || t == nil {
		slog.Warn("task worker: task missing",
			slog.Uint64("task_id", uint64(id)), slog.Any("error", err))
		return
	}
	if t.Status != model.TaskStatusPending {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	taskRunningCancelsMu.Lock()
	taskRunningCancels[id] = cancel
	taskRunningCancelsMu.Unlock()
	defer func() {
		taskRunningCancelsMu.Lock()
		delete(taskRunningCancels, id)
		taskRunningCancelsMu.Unlock()
		cancel()
	}()

	now := time.Now()
	if err := updateTaskFields(id, map[string]any{
		"status":     model.TaskStatusRunning,
		"started_at": now,
		"progress":   0,
	}); err != nil {
		slog.Error("task worker: mark running failed",
			slog.Uint64("task_id", uint64(id)), slog.Any("error", err))
		return
	}

	slog.Info("task started",
		slog.Uint64("task_id", uint64(id)),
		slog.String("kind", string(t.Kind)),
		slog.String("scope", string(t.Scope)),
		slog.Uint64("conn_id", uint64(t.ConnID)),
	)

	var runErr error
	switch t.Kind {
	case model.TaskKindExport:
		runErr = runExportTask(ctx, t)
	case model.TaskKindImport:
		runErr = errImportNotImplemented
	default:
		runErr = errUnknownKind
	}

	finished := time.Now()
	fields := map[string]any{
		"finished_at": finished,
		"updated_at":  finished,
	}
	switch {
	case runErr == errTaskCanceled:
		fields["status"] = model.TaskStatusCanceled
		fields["error_msg"] = "canceled by user"
	case runErr != nil:
		fields["status"] = model.TaskStatusFailed
		fields["error_msg"] = truncStr(runErr.Error(), 1023)
		slog.Error("task failed",
			slog.Uint64("task_id", uint64(id)),
			slog.Any("error", runErr))
	default:
		fields["status"] = model.TaskStatusSucceeded
		fields["progress"] = 100
		slog.Info("task succeeded",
			slog.Uint64("task_id", uint64(id)),
			slog.Duration("elapsed", time.Since(now)))
	}
	if err := updateTaskFields(id, fields); err != nil {
		slog.Error("task worker: mark finished failed",
			slog.Uint64("task_id", uint64(id)), slog.Any("error", err))
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
