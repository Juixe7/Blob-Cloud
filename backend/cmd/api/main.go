// Package main is the entrypoint for the Blob-Cloud API server.
//
// It parses configuration from environment variables, initialises dependencies
// (Postgres connection pool, local/S3 storage provider, SQS worker pool, WebSocket
// hub), builds the chi HTTP router, and runs the server with graceful shutdown.
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
	"go-drive-clone/internal/antivirus"
	"go-drive-clone/internal/config"
	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/email"
	"go-drive-clone/internal/metrics"
	"go-drive-clone/internal/queue"
	"go-drive-clone/internal/ratelimit"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
	"go-drive-clone/internal/storage"
	wsSync "go-drive-clone/internal/sync"
	httpx "go-drive-clone/internal/transport/http"
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

	// Register all Prometheus metrics with the private registry before the
	// HTTP server starts accepting requests.
	metrics.Init()

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
	//
	// workerWg / workerCtx are declared here so the backplane goroutine can be
	// tracked alongside SQS workers for graceful shutdown.
	var workerWg sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	var hub *wsSync.Hub
	var notifier wsSync.Notifier = wsSync.NoopNotifier()
	if cfg.JWTSecret != "" {
		hub = wsSync.NewHub(log)
		go hub.Run()
		notifier = hub // single-node default: hub IS the notifier

		// Tier 2D: Redis backplane for horizontal scale.
		// When REDIS_URL is set, the backplane replaces the direct hub as the
		// Notifier — it still delivers locally via hub AND publishes to Redis so
		// peer nodes can deliver to their local connections.
		if cfg.RedisURL != "" {
			redisClient, err := wsSync.NewRedisClient(cfg.RedisURL)
			if err != nil {
				log.Error("redis client init failed, falling back to single-node hub",
					"redis_url", cfg.RedisURL, "err", err)
			} else if pingErr := wsSync.Ping(context.Background(), redisClient); pingErr != nil {
				log.Error("redis ping failed, falling back to single-node hub",
					"redis_url", cfg.RedisURL, "err", pingErr)
			} else {
				bp := wsSync.NewRedisBackplane(redisClient, hub, log)
				workerWg.Add(1)
				go func() {
					defer workerWg.Done()
					if err := bp.Run(workerCtx); err != nil {
						log.Error("backplane subscriber exited", "err", err)
					}
				}()
				notifier = bp
				log.Info("redis backplane started", "channel", "blobcloud:ws:events")
			}
		}

		srv = srv.WithRealtime(hub, cfg.JWTSecret, cfg.WSCORSOrigins)
		log.Info("websocket hub started")
	}

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
			auditRepo := postgresrepo.NewAuditRepository(db, log)
			srv = srv.WithAudit(auditRepo)
			journal := postgresrepo.NewJournalRepository(db)
			srv = srv.WithJournal(journal)

			// AI Client initialization (available for search, summarisation, and queue processing)
			var aiClient ai.AIClient
			if token := os.Getenv("GEMINI_API_KEY"); token != "" {
				aiClient = ai.NewGeminiClient(token)
				log.Info("Gemini AI client initialised")
			}
			srv = srv.WithAI(aiClient)

			clamAddress := os.Getenv("CLAMAV_ADDRESS")
			var clamClient *antivirus.ClamAVClient
			if clamAddress != "" {
				clamClient = antivirus.NewClamAVClient(clamAddress)
				log.Info("ClamAV antivirus client initialised", "address", clamAddress)
			}

			// Processor for thumbnail generation, virus scanning, and AI metadata extraction
			processor := queue.NewFileProcessor(files, blocks, storageProvider, notifier, aiClient, clamClient, log)

			// Queue & Worker Pool wiring:
			// If SQS_QUEUE_URL is provided, use AWS SQS Publisher and WorkerPool.
			// Otherwise, fall back to in-process ChannelQueue for local / single-node deployments.
			var publisher queue.Publisher
			if cfg.SQSQueueURL != "" {
				sqsClient := queue.NewSQSClient(cfg)
				publisher = queue.NewSQSPublisher(sqsClient, cfg.SQSQueueURL, log)
				log.Info("SQS publisher configured for decoupled worker service", "queue_url", cfg.SQSQueueURL)

				// Start in-process SQS worker pool so jobs are processed immediately without requiring a standalone daemon
				if os.Getenv("ENABLE_INPROCESS_WORKER") != "false" {
					numWorkers := cfg.SQSNumWorkers
					if numWorkers <= 0 {
						numWorkers = 2
					}
					pollTimeout := cfg.SQSPollTimeoutSec
					if pollTimeout <= 0 {
						pollTimeout = 10
					}
					wp := queue.NewWorkerPool(sqsClient, cfg.SQSQueueURL, processor, numWorkers, int32(pollTimeout), log)
					wp.Start(workerCtx, &workerWg)
					log.Info("in-process SQS worker pool started", "workers", numWorkers, "queue_url", cfg.SQSQueueURL)
				}
			} else {
				channelQueue := queue.NewChannelQueue(processor, 100, 2, log)
				channelQueue.Start(workerCtx, &workerWg)
				publisher = channelQueue
				log.Info("in-process channel worker pool started (SQS not configured)", "workers", 2)
			}

			uploadSvc := service.NewUploadService(db, users, files, blocks, sessions, perms, storageProvider, publisher, notifier, log).WithJournal(journal)
			srv = srv.WithUploads(uploadSvc, perms)
			srv = srv.WithUsers(users)
			srv = srv.WithSessions(userSessions)

			// Phase 7.4: file operations (rename, move, delete, download).
			fileSvc := service.NewFileService(db, users, files, blocks, perms, storageProvider, log).WithJournal(journal).WithNotifier(notifier)
			srv = srv.WithFileOperations(fileSvc, files, blocks)

			zipSvc := service.NewZipService(files, blocks, storageProvider)
			srv = srv.WithZipOperations(zipSvc)
			srv = srv.WithShareableLinks(shares)

			// Background periodic cleaner for stale/expired share invitations (Option A)
			go func() {
				ticker := time.NewTicker(1 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-workerCtx.Done():
						return
					case <-ticker.C:
						if purged, err := perms.PurgeStaleInvitations(workerCtx); err != nil {
							log.Error("failed to purge stale share invitations", "err", err)
						} else if purged > 0 {
							log.Info("purged stale share invitations", "count", purged)
						}
					}
				}
			}()

			log.Info("repositories and upload service initialised")
		}
	}

	// Build rate limiters from config. In-memory by default; for multi-node
	// deployments swap to ratelimit.NewRedisLimiter using the same Redis client
	// wired for the backplane.
	rl := httpx.RateLimiters{}
	if cfg.RateLimitAuthPerMin > 0 {
		rl.Auth = ratelimit.NewInMemoryLimiter(cfg.RateLimitAuthPerMin, time.Minute)
		rl.AuthCfg = ratelimit.NewZoneConfig(cfg.RateLimitAuthPerMin, time.Minute)
	}
	if cfg.RateLimitUploadPerMin > 0 {
		rl.Upload = ratelimit.NewInMemoryLimiter(cfg.RateLimitUploadPerMin, time.Minute)
		rl.UploadCfg = ratelimit.NewZoneConfig(cfg.RateLimitUploadPerMin, time.Minute)
	}
	if cfg.RateLimitAPIPerMin > 0 {
		rl.API = ratelimit.NewInMemoryLimiter(cfg.RateLimitAPIPerMin, time.Minute)
		rl.APICfg = ratelimit.NewZoneConfig(cfg.RateLimitAPIPerMin, time.Minute)
	}
	router := httpx.NewRouter(srv, rl)

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
		hub.Shutdown()
	}

	// 3. Drain in-flight HTTP requests.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed, forcing close", "err", err)
		_ = httpServer.Close()
	}
	log.Info("server stopped gracefully")
}

// newLogger builds a structured JSON logger for production or a human-friendly
// text logger for development.
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
