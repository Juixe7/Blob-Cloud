// Package main is the entrypoint for the Blob-Cloud serverless worker on AWS Lambda.
//
// When SQS receives a post-upload job, AWS automatically invokes this Lambda function
// via Event Source Mapping. It supports SQS Partial Batch Responses so transient failures
// cause only the failed message to be retried by SQS without duplicating work.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
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

// LambdaWorker encapsulates dependencies reused across warm Lambda invocations.
type LambdaWorker struct {
	processor *queue.FileProcessor
	log       *slog.Logger
}

var (
	instance *LambdaWorker
	initOnce sync.Once
	initErr  error
)

// getWorker initializes the LambdaWorker singleton on cold start.
func getWorker(ctx context.Context) (*LambdaWorker, error) {
	initOnce.Do(func() {
		log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))

		metrics.Init()

		cfg, err := config.Load()
		if err != nil {
			initErr = fmt.Errorf("load config: %w", err)
			return
		}

		// 1. Storage Provider
		var storageProvider domain.StorageProvider
		switch cfg.StorageProvider {
		case "s3":
			s3, err := storage.NewS3Storage(ctx, cfg, log)
			if err != nil {
				initErr = fmt.Errorf("init s3 storage: %w", err)
				return
			}
			storageProvider = s3
		default:
			local, err := storage.NewLocalStore(cfg.LocalStorageDir, cfg.BaseURL, log)
			if err != nil {
				initErr = fmt.Errorf("init local storage: %w", err)
				return
			}
			storageProvider = local
		}

		// 2. Database
		db, err := database.New(ctx, cfg, log)
		if err != nil {
			initErr = fmt.Errorf("init db pool: %w", err)
			return
		}

		files := postgresrepo.NewFileRepository(db)
		blocks := postgresrepo.NewBlockRepository(db)

		// 3. Notifier (Redis Pub/Sub for cross-service WebSocket alerts)
		var notifier wsSync.Notifier = wsSync.NoopNotifier()
		if cfg.RedisURL != "" {
			redisClient, err := wsSync.NewRedisClient(cfg.RedisURL)
			if err == nil && wsSync.Ping(ctx, redisClient) == nil {
				notifier = wsSync.NewRedisBackplane(redisClient, nil, log)
			}
		}

		// 4. AI & ClamAV
		var aiClient ai.AIClient
		if token := os.Getenv("GEMINI_API_KEY"); token != "" {
			aiClient = ai.NewGeminiClient(token)
		}

		var clamClient *antivirus.ClamAVClient
		if clamAddr := os.Getenv("CLAMAV_ADDRESS"); clamAddr != "" {
			clamClient = antivirus.NewClamAVClient(clamAddr)
		}

		processor := queue.NewFileProcessor(files, blocks, storageProvider, notifier, aiClient, clamClient, log)

		instance = &LambdaWorker{
			processor: processor,
			log:       log,
		}
	})

	return instance, initErr
}

// Handler processes a batch of SQS messages delivered by AWS Lambda Event Source Mapping.
// It returns SQSEventResponse so SQS only retries failed messages within the batch.
func Handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	worker, err := getWorker(ctx)
	if err != nil {
		// Initialization failed; report all messages as failed
		var failures []events.SQSBatchItemFailure
		for _, record := range sqsEvent.Records {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
		return events.SQSEventResponse{BatchItemFailures: failures}, err
	}

	return processSQSEvent(ctx, worker.processor, worker.log, sqsEvent)
}

// processSQSEvent handles each message in the batch and tracks individual item failures.
func processSQSEvent(
	ctx context.Context,
	processor interface{ ProcessMessage(ctx context.Context, msg queue.ThumbnailMessage) error },
	log *slog.Logger,
	sqsEvent events.SQSEvent,
) (events.SQSEventResponse, error) {
	var batchFailures []events.SQSBatchItemFailure

	for _, record := range sqsEvent.Records {
		var msg queue.ThumbnailMessage
		if err := json.Unmarshal([]byte(record.Body), &msg); err != nil {
			log.Error("malformed SQS message body; dropping message to prevent poison pill loop",
				"message_id", record.MessageId,
				"err", err,
			)
			// Do NOT add to batchFailures: poison pills should not be re-queued indefinitely
			continue
		}

		if msg.FileID == "" {
			log.Warn("SQS message missing file_id; skipping", "message_id", record.MessageId)
			continue
		}

		log.Info("lambda processing message", "file_id", msg.FileID, "message_id", record.MessageId)
		if err := processor.ProcessMessage(ctx, msg); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Error("lambda failed to process file",
					"file_id", msg.FileID,
					"message_id", record.MessageId,
					"err", err,
				)
				// Record item failure so SQS retries only this message
				batchFailures = append(batchFailures, events.SQSBatchItemFailure{
					ItemIdentifier: record.MessageId,
				})
			}
		}
	}

	return events.SQSEventResponse{
		BatchItemFailures: batchFailures,
	}, nil
}

func main() {
	lambda.Start(Handler)
}
