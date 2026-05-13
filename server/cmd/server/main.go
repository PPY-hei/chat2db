package main

import (
	"log"

	"github.com/chy/chat2db/server/internal/api"
	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/db"
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

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	api.RegisterRoutes(r)

	log.Printf("chat2db server listening on %s", cfg.ServerAddr)
	if err := r.Run(cfg.ServerAddr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
