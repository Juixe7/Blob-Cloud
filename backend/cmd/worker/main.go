// Package main is the entrypoint for the Blob-Cloud standalone background worker daemon.
//
// It runs independently from the HTTP API Gateway, consuming thumbnail, virus scanning,
// and AI embedding jobs asynchronously from an AWS SQS queue (or fallback queue).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"go-drive-clone/internal/ai"
	"go-drive-clone/internal/antivirus"
	"go-drive-clone/internal/config"
	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/metrics"
	"go-drive-clone/internal/queue"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/storage"
	wsSync "go-drive-clone/internal/sync"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		_, _ = os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	metrics.Init()

	log.Info("starting blob-cloud standalone worker daemon",
		"env", cfg.ENV,
		"workers", cfg.SQSNumWorkers,
		"queue_url", cfg.SQSQueueURL,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Storage Provider --------------------------------------------------
	var storageProvider domain.StorageProvider
	switch cfg.StorageProvider {
	case "s3":
		s3, err := storage.NewS3Storage(ctx, cfg, log)
		if err != nil {
			log.Error("S3 storage initialisation failed", "err", err)
			os.Exit(1)
		}
		storageProvider = s3
	default:
		local, err := storage.NewLocalStore(cfg.LocalStorageDir, cfg.BaseURL, log)
		if err != nil {
			log.Error("failed to initialise local storage", "err", err)
			os.Exit(1)
		}
		storageProvider = local
	}

	// 2. Database ----------------------------------------------------------
	db, err := database.New(ctx, cfg, log)
	if err != nil {
		log.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	files := postgresrepo.NewFileRepository(db)
	blocks := postgresrepo.NewBlockRepository(db)

	// 3. Notifier (Redis Backplane) ----------------------------------------
	var notifier wsSync.Notifier = wsSync.NoopNotifier()
	var backplaneWg sync.WaitGroup
	if cfg.RedisURL != "" {
		redisClient, err := wsSync.NewRedisClient(cfg.RedisURL)
		if err != nil {
			log.Warn("redis client init failed, continuing with noop notifier", "err", err)
		} else if pingErr := wsSync.Ping(ctx, redisClient); pingErr != nil {
			log.Warn("redis ping failed, continuing with noop notifier", "err", pingErr)
		} else {
			// Workers do not have local WebSocket connections, so hub can be nil.
			// RedisBackplane handles publishing events to the shared Redis Pub/Sub channel.
			bp := wsSync.NewRedisBackplane(redisClient, nil, log)
			backplaneWg.Add(1)
			go func() {
				defer backplaneWg.Done()
				_ = bp.Run(ctx)
			}()
			notifier = bp
			log.Info("redis notifier configured for cross-service events")
		}
	}

	// 4. AI & Antivirus ----------------------------------------------------
	var aiClient ai.AIClient
	if token := os.Getenv("GEMINI_API_KEY"); token != "" {
		aiClient = ai.NewGeminiClient(token)
		log.Info("gemini AI client initialised")
	}

	var clamClient *antivirus.ClamAVClient
	if clamAddr := os.Getenv("CLAMAV_ADDRESS"); clamAddr != "" {
		clamClient = antivirus.NewClamAVClient(clamAddr)
		log.Info("clamav antivirus client initialised", "address", clamAddr)
	}

	// 5. File Processor ----------------------------------------------------
	processor := queue.NewFileProcessor(files, blocks, storageProvider, notifier, aiClient, clamClient, log)

	// 6. Worker Pool -------------------------------------------------------
	var workerWg sync.WaitGroup
	if cfg.SQSQueueURL != "" {
		sqsClient := queue.NewSQSClient(cfg)
		wp := queue.NewWorkerPool(
			sqsClient,
			cfg.SQSQueueURL,
			processor,
			cfg.SQSNumWorkers,
			int32(cfg.SQSPollTimeoutSec),
			log,
		)
		wp.Start(ctx, &workerWg)
		log.Info("SQS worker pool started", "workers", cfg.SQSNumWorkers, "queue_url", cfg.SQSQueueURL)
	} else {
		log.Warn("SQS_QUEUE_URL not configured. Standalone worker requires SQS in distributed mode. Waiting for shutdown.")
	}

	// 7. Graceful Shutdown -------------------------------------------------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutdown signal received; draining worker queue...")
	cancel()

	done := make(chan struct{})
	go func() {
		workerWg.Wait()
		backplaneWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("all workers stopped cleanly")
	case <-time.After(15 * time.Second):
		log.Warn("shutdown timed out after 15s; force exiting")
	}
}
