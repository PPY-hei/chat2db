package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chy/chat2db/server/internal/api"
	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/chy/chat2db/server/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env files from conventional locations (optional).
	// Priority: existing OS env > .env.local > .env. godotenv does NOT override
	// variables that already exist in the environment, so explicit exports and
	// container-provided env still win.
	//
	// godotenv 在 slog 初始化之前调用，所以加载提示先用临时 stderr handler 打 INFO，
	// 等 cfg.LogLevel 解析后再 SetDefault 切换正式 handler。
	loadedEnvFiles := make([]string, 0, 2)
	for _, f := range []string{".env.local", ".env"} {
		if err := godotenv.Load(f); err == nil {
			loadedEnvFiles = append(loadedEnvFiles, f)
		}
	}

	cfg := config.Load()

	// 初始化 slog 全局 logger：JSON 输出到 stdout，级别由 LOG_LEVEL 控制。
	// 之后所有 slog.Info/Warn/Error 都直接走全局 default。
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})))
	for _, f := range loadedEnvFiles {
		slog.Info("loaded env file", slog.String("file", f))
	}

	gin.SetMode(cfg.ServerMode)

	if _, err := db.Init(); err != nil {
		slog.Error("failed to init metadata db", slog.Any("error", err))
		os.Exit(1)
	}

	// 启动审计日志异步 worker。retention 来自 AUDIT_RETENTION，默认 90d。
	service.StartAuditWorker(cfg.AuditRetention)
	defer service.StopAuditWorker()

	r := gin.New()
	// 中间件顺序：RequestID（最先，让后续都能拿到 ID）→ Logger（请求结束写日志）
	// → Recovery（panic 时仍能输出 ID + stack）。
	r.Use(middleware.RequestID(), middleware.Logger(), middleware.Recovery())
	api.RegisterRoutes(r)

	srv := &http.Server{Addr: cfg.ServerAddr, Handler: r}

	// 异步启动 + 信号驱动的优雅停机：收到 SIGINT/SIGTERM 后给 5s 排空 HTTP，
	// 再 close 审计 channel 让 worker 把队列里剩余事件 flush 进 DB。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("chat2db server listening", slog.String("addr", cfg.ServerAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server exited", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", slog.Any("error", err))
	}
}
