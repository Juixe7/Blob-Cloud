package queue

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Backoff defaults for the worker error path. When ReceiveMessage fails the
// worker sleeps before retrying, doubling the delay each consecutive failure
// (capped at maxBackoff). This protects SQS API rate limits and keeps the
// logs readable during an outage (e.g. a credential failure would otherwise
// produce ~3 requests/second forever).
const (
	defaultMinBackoff = 1 * time.Second
	defaultMaxBackoff = 30 * time.Second
	defaultBackoffFactor = 2.0
)

// messageProcessor is the contract the worker pool relies on to handle one
// job. *ThumbnailProcessor satisfies it, and tests can substitute a fake to
// verify the worker's retry/delete behaviour in isolation.
type messageProcessor interface {
	ProcessMessage(ctx context.Context, msg ThumbnailMessage) error
}

// sqsQueue is the subset of the SQS client API the worker pool uses.
type sqsQueue interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// WorkerPool manages a configurable number of goroutines that long-poll an SQS
// queue for thumbnail jobs and process them via a messageProcessor. On
// cancellation (graceful shutdown), workers finish their in-flight message
// and then exit.
type WorkerPool struct {
	client    sqsQueue
	queueURL  string
	processor messageProcessor
	numWorkers int
	pollTimeout int32 // WaitTimeSeconds for long-polling
	log       *slog.Logger
	// Exponential-backoff tuning for the ReceiveMessage error path.
	minBackoff    time.Duration
	maxBackoff    time.Duration
	backoffFactor float64
}

// NewWorkerPool builds a worker pool. It does not start consuming; call Start
// to begin.
func NewWorkerPool(
	client sqsQueue,
	queueURL string,
	processor messageProcessor,
	numWorkers int,
	pollTimeout int32,
	log *slog.Logger,
) *WorkerPool {
	return &WorkerPool{
		client:        client,
		queueURL:      queueURL,
		processor:     processor,
		numWorkers:    numWorkers,
		pollTimeout:   pollTimeout,
		log:           log,
		minBackoff:    defaultMinBackoff,
		maxBackoff:    defaultMaxBackoff,
		backoffFactor: defaultBackoffFactor,
	}
}

// Start launches all workers. Each worker runs in its own goroutine, long-polling
// SQS until ctx is cancelled. Call Start in a goroutine from main; cancel the
// context (or stop the wg) to signal shutdown.
func (wp *WorkerPool) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(wp.numWorkers)
	for i := range wp.numWorkers {
		go func(workerID int) {
			defer wg.Done()
			wp.log.Info("worker started", "worker_id", workerID)
			wp.run(ctx, workerID)
			wp.log.Info("worker stopped", "worker_id", workerID)
		}(i)
	}
}

// run is the main loop for a single worker. It long-polls SQS, processes
// messages, and applies exponential backoff on ReceiveMessage errors so a
// persistent failure (e.g. bad credentials) cannot hammer the SQS API.
func (wp *WorkerPool) run(ctx context.Context, workerID int) {
	currentBackoff := wp.minBackoff
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := wp.pollOnce(ctx, workerID)
		if err == nil {
			// Success (including an empty-but-valid response): reset backoff.
			currentBackoff = wp.minBackoff
			continue
		}

		// Failure — back off. The sleep is context-aware so SIGTERM during a
		// backoff still shuts the worker down promptly.
		wp.log.Warn("worker backing off",
			"worker_id", workerID,
			"backoff_seconds", currentBackoff.Seconds(),
			"err", err,
		)

		timer := time.NewTimer(currentBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		// Grow the delay for the next consecutive failure, capped at maxBackoff.
		currentBackoff = time.Duration(float64(currentBackoff) * wp.backoffFactor)
		if currentBackoff > wp.maxBackoff {
			currentBackoff = wp.maxBackoff
		}
	}
}

// pollOnce performs a single long-poll ReceiveMessage call and processes any
// returned messages. It returns nil on a successful poll (even with zero
// messages — that is a valid API transaction), or the error from
// ReceiveMessage so the caller can apply backoff.
func (wp *WorkerPool) pollOnce(ctx context.Context, workerID int) error {
	output, err := wp.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(wp.queueURL),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     wp.pollTimeout,
	})
	if err != nil {
		// Context cancellation during the in-flight ReceiveMessage is a
		// shutdown, not a failure — surface it so run returns promptly.
		return err
	}

	for _, msg := range output.Messages {
		wp.handleMessage(ctx, workerID, msg)
	}
	return nil
}

// handleMessage parses, processes, and deletes a single SQS message.
func (wp *WorkerPool) handleMessage(ctx context.Context, workerID int, msg types.Message) {
	var tm ThumbnailMessage
	if err := json.Unmarshal([]byte(*msg.Body), &tm); err != nil {
		wp.log.Error("invalid message body, deleting", "worker_id", workerID, "body", *msg.Body)
		wp.deleteMessage(ctx, msg)
		return
	}

	wp.log.Info("worker received job", "worker_id", workerID, "file_id", tm.FileID)

	if err := wp.processor.ProcessMessage(ctx, tm); err != nil {
		// Processing failed — do NOT delete the message. SQS will make it visible
		// again after the visibility timeout for retry.
		wp.log.Error("processing failed, message will retry",
			"worker_id", workerID,
			"file_id", tm.FileID,
			"err", err,
		)
		return
	}

	// Success — delete the message so it's not retried.
	wp.deleteMessage(ctx, msg)
}

// deleteMessage removes a processed message from the queue.
func (wp *WorkerPool) deleteMessage(ctx context.Context, msg types.Message) {
	_, err := wp.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(wp.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	if err != nil {
		wp.log.Error("failed to delete message",
			"message_id", *msg.MessageId,
			"err", err,
		)
	}
}
