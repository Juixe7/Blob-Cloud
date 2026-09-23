package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"go-drive-clone/internal/queue"
)

type mockProcessor struct {
	failFileID string
	processed  []string
}

func (m *mockProcessor) ProcessMessage(_ context.Context, msg queue.ThumbnailMessage) error {
	m.processed = append(m.processed, msg.FileID)
	if m.failFileID != "" && msg.FileID == m.failFileID {
		return errors.New("simulated processing failure")
	}
	return nil
}

func TestProcessSQSEvent_AllSuccess(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	proc := &mockProcessor{}

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "msg-1", Body: `{"file_id":"f1","user_id":"u1"}`},
			{MessageId: "msg-2", Body: `{"file_id":"f2","user_id":"u1"}`},
		},
	}

	resp, err := processSQSEvent(context.Background(), proc, log, sqsEvent)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}

	if len(resp.BatchItemFailures) != 0 {
		t.Errorf("expected 0 batch failures, got %d", len(resp.BatchItemFailures))
	}
	if len(proc.processed) != 2 {
		t.Errorf("expected 2 processed messages, got %d", len(proc.processed))
	}
}

func TestProcessSQSEvent_PartialFailure(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	proc := &mockProcessor{failFileID: "f2"}

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "msg-1", Body: `{"file_id":"f1","user_id":"u1"}`},
			{MessageId: "msg-2", Body: `{"file_id":"f2","user_id":"u1"}`},
			{MessageId: "msg-3", Body: `{"file_id":"f3","user_id":"u1"}`},
		},
	}

	resp, err := processSQSEvent(context.Background(), proc, log, sqsEvent)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}

	if len(resp.BatchItemFailures) != 1 {
		t.Fatalf("expected exactly 1 batch failure, got %d", len(resp.BatchItemFailures))
	}
	if resp.BatchItemFailures[0].ItemIdentifier != "msg-2" {
		t.Errorf("expected msg-2 to fail, got %s", resp.BatchItemFailures[0].ItemIdentifier)
	}
}

func TestProcessSQSEvent_MalformedJSONDropped(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	proc := &mockProcessor{}

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "msg-bad", Body: `not-valid-json`},
			{MessageId: "msg-good", Body: `{"file_id":"f100","user_id":"u1"}`},
		},
	}

	resp, err := processSQSEvent(context.Background(), proc, log, sqsEvent)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}

	// Poison pill must not be in BatchItemFailures (it should be dropped, not retried forever)
	if len(resp.BatchItemFailures) != 0 {
		t.Errorf("expected 0 batch failures for malformed JSON, got %d", len(resp.BatchItemFailures))
	}
	if len(proc.processed) != 1 || proc.processed[0] != "f100" {
		t.Errorf("expected only f100 processed, got %v", proc.processed)
	}
}
