package queue

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockMessageProcessor struct {
	processed atomic.Int32
	processFn func(ctx context.Context, msg ThumbnailMessage) error
}

func (m *mockMessageProcessor) ProcessMessage(ctx context.Context, msg ThumbnailMessage) error {
	m.processed.Add(1)
	if m.processFn != nil {
		return m.processFn(ctx, msg)
	}
	return nil
}

func TestChannelQueue_PublishAndProcess(t *testing.T) {
	proc := &mockMessageProcessor{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewChannelQueue(proc, 10, 2, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	q.Start(ctx, &wg)

	msgs := []ThumbnailMessage{
		{FileID: "file-1", UserID: "user-1"},
		{FileID: "file-2", UserID: "user-1"},
		{FileID: "file-3", UserID: "user-2"},
	}

	for _, msg := range msgs {
		if err := q.PublishThumbnailJob(ctx, msg); err != nil {
			t.Fatalf("unexpected publish error: %v", err)
		}
	}

	// Wait briefly for workers to consume messages
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if proc.processed.Load() == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := proc.processed.Load(); got != 3 {
		t.Errorf("expected 3 processed messages, got %d", got)
	}

	cancel()
	wg.Wait()
}

func TestChannelQueue_ContextCancellation(t *testing.T) {
	proc := &mockMessageProcessor{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewChannelQueue(proc, 1, 1, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled

	err := q.PublishThumbnailJob(ctx, ThumbnailMessage{FileID: "file-canceled"})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}
