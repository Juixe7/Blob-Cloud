package queue

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// fakeSQSSender captures the last SendMessageInput so the test can assert on
// the queue URL and serialised payload. err, when non-nil, is returned instead.
type fakeSQSSender struct {
	got   *sqs.SendMessageInput
	err   error
	calls int
}

func (f *fakeSQSSender) SendMessage(_ context.Context, params *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.calls++
	f.got = params
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.SendMessageOutput{MessageId: aws.String("msg-1")}, nil
}

func TestNoopPublisher_AlwaysSucceeds(t *testing.T) {
	if err := (NoopPublisher{}).PublishThumbnailJob(context.Background(), ThumbnailMessage{FileID: "x"}); err != nil {
		t.Fatalf("NoopPublisher returned error: %v", err)
	}
}

func TestSQSPublisher_SendsCorrectPayload(t *testing.T) {
	fake := &fakeSQSSender{}
	pub := NewSQSPublisher(fake, "https://sqs.example/123/blob-cloud-uploads", testLogger())

	msg := ThumbnailMessage{FileID: "file-42", UserID: "user-7"}
	if err := pub.PublishThumbnailJob(context.Background(), msg); err != nil {
		t.Fatalf("PublishThumbnailJob: %v", err)
	}

	if fake.calls != 1 {
		t.Fatalf("SendMessage calls = %d, want 1", fake.calls)
	}
	if got := *fake.got.QueueUrl; got != "https://sqs.example/123/blob-cloud-uploads" {
		t.Fatalf("QueueUrl = %q, want configured queue URL", got)
	}

	// The message body must be the JSON of ThumbnailMessage.
	var decoded ThumbnailMessage
	if err := json.Unmarshal([]byte(*fake.got.MessageBody), &decoded); err != nil {
		t.Fatalf("MessageBody is not valid ThumbnailMessage JSON: %v", err)
	}
	if decoded != msg {
		t.Fatalf("payload round-trip mismatch: got %+v, want %+v", decoded, msg)
	}
}

func TestSQSPublisher_PropagatesClientError(t *testing.T) {
	fake := &fakeSQSSender{err: errors.New("aws throttled")}
	pub := NewSQSPublisher(fake, "https://sqs.example/q", testLogger())

	err := pub.PublishThumbnailJob(context.Background(), ThumbnailMessage{FileID: "f1"})
	if err == nil {
		t.Fatal("expected error from failing SQS client, got nil")
	}
}

// TestNoopPublisher_satisfiesInterface / TestSQSPublisher_satisfiesInterface
// are compile-time guarantees that both types implement the Publisher
// interface that the upload service depends on.
func TestNoopPublisher_SatisfiesPublisherInterface(t *testing.T) {
	var _ Publisher = NoopPublisher{}
	var _ Publisher = (*SQSPublisher)(nil)
}

func init() {
	// Silence any logger output during tests by keeping a discard handler.
	_ = slog.New(slog.NewTextHandler(io.Discard, nil))
}
