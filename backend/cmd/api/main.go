// Package main is the API server entry point. It wires dependencies (config ->
// logger -> storage -> hub -> router -> HTTP server) and orchestrates a graceful
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
	"go-drive-clone/internal/ai"
	"go-drive-clone/internal/config"
	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/email"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/queue"
	"go-drive-clone/internal/antivirus"
	wsSync "go-drive-clone/internal/sync"
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
	ctx := context.Background()
	var storageProvider domain.StorageProvider

	switch cfg.StorageProvider {
	case "s3":
		s3, err := storage.NewS3Storage(ctx, cfg, log)
		if err != nil {
			// S3 was explicitly requested: a failure here means bad keys, a
			// missing bucket, or a network timeout. Falling back silently to
			// local storage would mask the misconfiguration (uploads would
			// appear to work but vanish into ./tmp/storage/). Fail loudly.
			log.Error("S3 storage initialisation failed (STORAGE_PROVIDER=s3)", "err", err)
			os.Exit(1)
		}
		storageProvider = s3
	}

	if storageProvider == nil {
		local, err := storage.NewLocalStore(cfg.LocalStorageDir, cfg.BaseURL, log)
		if err != nil {
			log.Error("failed to initialise local storage", "err", err)
			os.Exit(1)
		}
		storageProvider = local
	}

	log.Info("storage provider active", "provider", cfg.StorageProvider)
	srv := httpx.NewServer(storageProvider, log)

	mailer := email.NewMailer(log)
	srv = srv.WithMailer(mailer)
	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	if googleClientID == "" {
		googleClientID = os.Getenv("VITE_GOOGLE_CLIENT_ID")
	}
	srv = srv.WithGoogleOAuth(googleClientID)

	// Phase 6: WebSocket Hub — started unconditionally so WS connections
	// can be accepted even if the DB is unavailable (auth only requires JWT).
	var hub *wsSync.Hub
	if cfg.JWTSecret != "" {
		hub = wsSync.NewHub(log)
		go hub.Run()
		srv = srv.WithRealtime(hub, cfg.JWTSecret, cfg.WSCORSOrigins)
		log.Info("websocket hub started")
	}

	// workerWg tracks SQS worker goroutines for graceful shutdown.
	var workerWg sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

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
			// Repositories + upload service.
			users := postgresrepo.NewUserRepository(db)
			files := postgresrepo.NewFileRepository(db)
			blocks := postgresrepo.NewBlockRepository(db)
			sessions := postgresrepo.NewUploadSessionRepository(db)
			perms := postgresrepo.NewPermissionRepository(db)
			userSessions := postgresrepo.NewSessionRepository(db)
			shares := postgresrepo.NewShareableLinkRepository(db)

			// Build the notifier that integration points will use.
			// Falls back to a no-op if the Hub isn't configured.
			var notifier wsSync.Notifier = wsSync.NoopNotifier()
			if hub != nil {
				notifier = hub
			}

			// SQS publisher: publishes thumbnail jobs after upload completes.
			var publisher queue.Publisher = queue.NoopPublisher{}
			if cfg.SQSQueueURL != "" {
				sqsClient := queue.NewSQSClient(cfg)
				publisher = queue.NewSQSPublisher(sqsClient, cfg.SQSQueueURL, log)
				log.Info("SQS publisher configured", "queue_url", cfg.SQSQueueURL)
			}

			uploadSvc := service.NewUploadService(db, users, files, blocks, sessions, perms, storageProvider, publisher, notifier, log)
				srv = srv.WithUploads(uploadSvc, perms)
				srv = srv.WithUsers(users)
				srv = srv.WithSessions(userSessions)

				// Phase 7.4: file operations (rename, move, delete, download).
				fileSvc := service.NewFileService(db, users, files, blocks, perms, storageProvider, log)
				srv = srv.WithFileOperations(fileSvc, files, blocks)

				zipSvc := service.NewZipService(files, blocks, storageProvider)
				srv = srv.WithZipOperations(zipSvc)
				srv = srv.WithShareableLinks(shares)

			// Worker pool: background thumbnail and AI processing.
			if cfg.SQSQueueURL != "" {
				var aiClient ai.AIClient
				if token := os.Getenv("GEMINI_API_KEY"); token != "" {
					aiClient = ai.NewGeminiClient(token)
				}
				srv = srv.WithAI(aiClient)
				clamAddress := os.Getenv("CLAMAV_ADDRESS")
				var clamClient *antivirus.ClamAVClient
				if clamAddress != "" {
					clamClient = antivirus.NewClamAVClient(clamAddress)
				}
				processor := queue.NewFileProcessor(files, blocks, storageProvider, notifier, aiClient, clamClient, log)
				wp := queue.NewWorkerPool(
					queue.NewSQSClient(cfg),
					cfg.SQSQueueURL,
					processor,
					cfg.SQSNumWorkers,
					int32(cfg.SQSPollTimeoutSec),
					log,
				)
				wp.Start(workerCtx, &workerWg)
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
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown ----------------------------------------------------
	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Error("http server crashed", "err", err)
		os.Exit(1)
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// 1. Stop SQS workers (they may be mid-processing a message).
	if cfg.SQSQueueURL != "" {
		log.Info("cancelling worker context and waiting for SQS workers to finish...")
		workerCancel()
		workerWg.Wait()
		log.Info("all workers stopped")
	}

	// 2. Close WebSocket hub: send CloseGoingAway to all connected clients.
	if hub != nil {
		log.Info("shutting down websocket hub...")
		hub.Shutdown()
		log.Info("websocket hub stopped")
	}

	// 3. Drain the HTTP server.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed, forcing exit", "err", err)
		os.Exit(1)
	}
	log.Info("server stopped cleanly")
}

// newLogger returns an slog.Logger configured for the environment: JSON for
// production (machine-parseable) and human-readable text for development.
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
