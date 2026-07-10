// Package main is the API server entry point. It wires dependencies (config ->
// logger -> storage -> router -> HTTP server) and orchestrates a graceful
// shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"go-drive-clone/internal/config"
	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/queue"
	"go-drive-clone/internal/service"
	httpx "go-drive-clone/internal/transport/http"
	"go-drive-clone/internal/storage"
)

// shutdownTimeout is the maximum time allowed for in-flight requests to drain
// after a termination signal is received.
const shutdownTimeout = 10 * time.Second

func main() {
	// Load .env if present. A missing file is fine (e.g. production, where env
	// vars come from the runtime). A malformed file is a real error: fail loudly
	// so a typo in .env can't silently misconfigure the server.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		_, _ = os.Stderr.WriteString("failed to load .env: " + err.Error() + "\n")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		// Logger isn't set up yet; write to stderr directly.
		_, _ = os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	log := newLogger(cfg.ENV)

	log.Info("starting go-drive-clone",
		"env", cfg.ENV,
		"port", cfg.Port,
		"storage_provider", cfg.StorageProvider,
	)

	// Storage provider ------------------------------------------------------
	// STORAGE_PROVIDER selects the backend: "local" (Phase 1 filesystem) or
	// "s3" (Phase 4: AWS S3 / Cloudflare R2). The rest of the application
	// depends only on domain.StorageProvider, so swapping the implementation
	// requires zero handler/service changes.
	ctx := context.Background()
	var storageProvider domain.StorageProvider

	switch cfg.StorageProvider {
	case "s3":
		s3, err := storage.NewS3Storage(ctx, cfg, log)
		if err != nil {
			if cfg.ENV == "production" {
				log.Error("failed to initialise S3 storage (production: fatal)", "err", err)
				os.Exit(1)
			}
			log.Warn("S3 storage unavailable, falling back to local", "err", err)
			cfg.StorageProvider = "local"
			// fall through to local
		} else {
			storageProvider = s3
		}
	}

	if storageProvider == nil {
		// Default / fallback: local disk storage.
		local, err := storage.NewLocalStore(cfg.LocalStorageDir, cfg.BaseURL, log)
		if err != nil {
			log.Error("failed to initialise local storage", "err", err)
			os.Exit(1)
		}
		storageProvider = local
	}

	log.Info("storage provider active", "provider", cfg.StorageProvider)
	srv := httpx.NewServer(storageProvider, log)

	// workerWg tracks SQS worker goroutines for graceful shutdown. It is
	// populated inside the DB init block if SQS is configured.
	var workerWg sync.WaitGroup

	db, dbErr := database.New(ctx, cfg, log)
	if dbErr != nil {
		if cfg.ENV == "production" {
			log.Error("failed to connect to database (production: fatal)", "err", dbErr)
			os.Exit(1)
		}
		log.Warn("database unavailable, running in storage-only mode", "err", dbErr)
	} else {
		defer func() {
			if err := db.Close(); err != nil {
				log.Error("closing database pool", "err", err)
			}
		}()
		if err := database.RunMigrations(ctx, db, log); err != nil {
			if cfg.ENV == "production" {
				log.Error("failed to run migrations (production: fatal)", "err", err)
				os.Exit(1)
			}
			log.Warn("migrations failed, continuing without DB", "err", err)
		} else {
			// Repositories + upload service. The upload service owns the cross-
				// repository transaction for the complete flow, so it gets every
				// repo plus the storage provider and the pool.
				users := postgresrepo.NewUserRepository(db)
				files := postgresrepo.NewFileRepository(db)
				blocks := postgresrepo.NewBlockRepository(db)
				sessions := postgresrepo.NewUploadSessionRepository(db)
				perms := postgresrepo.NewPermissionRepository(db)

				// SQS publisher: publishes thumbnail jobs after upload completes.
				// Falls back to NoopPublisher if no queue URL is configured.
				var publisher queue.Publisher = queue.NoopPublisher{}
				if cfg.SQSQueueURL != "" {
					sqsClient := queue.NewSQSClient(cfg)
					publisher = queue.NewSQSPublisher(sqsClient, cfg.SQSQueueURL, log)
					log.Info("SQS publisher configured", "queue_url", cfg.SQSQueueURL)
				}

				uploadSvc := service.NewUploadService(db, users, files, blocks, sessions, perms, storageProvider, publisher, log)
				srv = srv.WithUploads(uploadSvc, perms)

				// Worker pool: background thumbnail generation.
				if cfg.SQSQueueURL != "" {
					processor := queue.NewThumbnailProcessor(files, blocks, storageProvider, log)
					wp := queue.NewWorkerPool(
						queue.NewSQSClient(cfg),
						cfg.SQSQueueURL,
						processor,
						cfg.SQSNumWorkers,
						int32(cfg.SQSPollTimeoutSec),
						log,
					)
					wp.Start(ctx, &workerWg)
					log.Info("SQS worker pool started", "workers", cfg.SQSNumWorkers)
				}

				log.Info("repositories and upload service initialised")
		}
	}

	router := httpx.NewRouter(srv)

	httpServer := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       0, // uploads may be large/streamed; rely on client.
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown ----------------------------------------------------
	// Run ListenAndServe in its own goroutine so the main goroutine can block
	// on the OS signal channel.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	stop := make(chan os.Signal, 1)
	// SIGINT = Ctrl+C; SIGTERM = the signal deploy systems send to stop a process.
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Error("http server crashed", "err", err)
		os.Exit(1)
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// Graceful shutdown: stop workers first (they may be mid-processing a
	// message), then drain the HTTP server.
	if cfg.SQSQueueURL != "" {
		log.Info("waiting for SQS workers to finish...")
		workerWg.Wait()
		log.Info("all workers stopped")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed, forcing exit", "err", err)
		os.Exit(1)
	}
	log.Info("server stopped cleanly")
}

// newLogger returns an slog.Logger configured for the environment: JSON for
// production (machine-parseable, ideal for log aggregation) and human-readable
// text for development.
func newLogger(env string) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
