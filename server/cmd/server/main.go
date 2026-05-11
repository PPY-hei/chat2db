package main

import (
	"log"

	"github.com/chy/chat2db/server/internal/api"
	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	gin.SetMode(cfg.ServerMode)

	if _, err := db.Init(); err != nil {
		log.Fatalf("failed to init metadata db: %v", err)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	api.RegisterRoutes(r)

	log.Printf("chat2db server listening on %s", cfg.ServerAddr)
	if err := r.Run(cfg.ServerAddr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
