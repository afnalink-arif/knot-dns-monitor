package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/knot-dns-monitor/backend/internal/api"
	"github.com/knot-dns-monitor/backend/internal/config"
)

var version = "dev"

func main() {
	cfg := config.Load()
	cfg.Version = version

	router, cleanup, err := api.NewRouter(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize router: %v", err)
	}
	defer cleanup()

	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Backend API v%s listening on :%s", version, cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced shutdown: %v", err)
	}
	log.Println("Server exited")
}
