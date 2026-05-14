package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/chy/chat2db/server/internal/api"
	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/db"
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
	// Each file is attempted independently — godotenv.Load returns on the first
	// missing file when given a list, which is not what we want here.
	for _, f := range []string{".env.local", ".env"} {
		if err := godotenv.Load(f); err == nil {
			log.Printf("loaded env file: %s", f)
		}
	}

	cfg := config.Load()
	gin.SetMode(cfg.ServerMode)

	if _, err := db.Init(); err != nil {
		log.Fatalf("failed to init metadata db: %v", err)
	}

	// 启动审计日志异步 worker。retention 来自 AUDIT_RETENTION，默认 90d。
	service.StartAuditWorker(cfg.AuditRetention)
	defer service.StopAuditWorker()

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	api.RegisterRoutes(r)

	srv := &http.Server{Addr: cfg.ServerAddr, Handler: r}

	// 异步启动 + 信号驱动的优雅停机：收到 SIGINT/SIGTERM 后给 5s 排空 HTTP，
	// 再 close 审计 channel 让 worker 把队列里剩余事件 flush 进 DB。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("chat2db server listening on %s", cfg.ServerAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server exited: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
}
