package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// ChannelQueue implements Publisher using an in-process buffered Go channel.
// It is used as a fallback when AWS SQS is not configured, providing seamless
// asynchronous background processing (thumbnailing, virus scanning, AI summarisation)
// on single-node and local development environments.
type ChannelQueue struct {
	ch        chan ThumbnailMessage
	processor messageProcessor
	workers   int
	log       *slog.Logger
}

// NewChannelQueue constructs an in-process channel queue.
func NewChannelQueue(
	processor messageProcessor,
	bufferSize int,
	workers int,
	log *slog.Logger,
) *ChannelQueue {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	if workers <= 0 {
		workers = 2
	}
	return &ChannelQueue{
		ch:        make(chan ThumbnailMessage, bufferSize),
		processor: processor,
		workers:   workers,
		log:       log,
	}
}

// PublishThumbnailJob enqueues a thumbnail/AI processing job.
func (q *ChannelQueue) PublishThumbnailJob(ctx context.Context, msg ThumbnailMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case q.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Channel buffer is full; log warning and attempt blocking send respecting ctx
		q.log.Warn("channel queue buffer full; blocking until slot available", "file_id", msg.FileID)
		select {
		case q.ch <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Start launches worker goroutines consuming jobs from the internal channel.
func (q *ChannelQueue) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(q.workers)
	for i := range q.workers {
		go func(workerID int) {
			defer wg.Done()
			q.log.Info("channel queue worker started", "worker_id", workerID)
			for {
				select {
				case <-ctx.Done():
					q.log.Info("channel queue worker stopping due to context cancellation", "worker_id", workerID)
					return
				case msg, ok := <-q.ch:
					if !ok {
						return
					}
					if err := q.processor.ProcessMessage(ctx, msg); err != nil {
						if !errors.Is(err, context.Canceled) {
							q.log.Error("channel worker failed to process message",
								"worker_id", workerID,
								"file_id", msg.FileID,
								"err", err,
							)
						}
					}
				}
			}
		}(i)
	}
}
