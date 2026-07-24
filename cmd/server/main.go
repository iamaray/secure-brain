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

	"github.com/jackc/pgx/v5/pgxpool"

	"secure-brain/internal/config"
	"secure-brain/internal/httpapi"
	openaiapi "secure-brain/internal/openai"
	"secure-brain/internal/storage"
	"secure-brain/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("DATABASE_URL is invalid")
		os.Exit(1)
	}
	poolConfig.MaxConns, poolConfig.MinConns, poolConfig.MaxConnLifetime = 10, 1, 30*time.Minute
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		logger.Error("database pool could not be created")
		os.Exit(1)
	}
	defer pool.Close()
	db := store.New(pool)
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.Ping(startup); err != nil {
		logger.Error("database is unreachable")
		os.Exit(1)
	}
	if err := db.CheckSchemaVersion(startup); err != nil {
		logger.Error("database schema version 1 is missing; run db/schema.sql")
		os.Exit(1)
	}
	objects, err := storage.NewClient(cfg.SupabaseURL, cfg.StorageBucket, cfg.SupabaseServiceRoleKey, &http.Client{Timeout: 60 * time.Second}, cfg.Limits.MaxObjectBytes())
	if err != nil {
		logger.Error("Storage configuration is invalid")
		os.Exit(1)
	}
	var chat openaiapi.ChatClient
	if !cfg.ChatDisabled {
		chat, err = openaiapi.NewClient(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, &http.Client{Timeout: 45 * time.Second})
		if err != nil {
			logger.Error("OpenAI configuration is invalid")
			os.Exit(1)
		}
	}
	handler := httpapi.New(db, objects, httpapi.Options{SessionSecret: []byte(cfg.SessionSecret), FrontendOrigin: cfg.FrontendOrigin, Logger: logger, Limits: cfg.Limits, Chat: chat, ChatModel: cfg.OpenAIModel, ChatDisabled: cfg.ChatDisabled})
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		logger.Info("server started", "address", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()
	stop, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	<-stop.Done()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
