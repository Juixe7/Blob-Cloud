// Package queue implements the event-driven thumbnail processing pipeline:
// an SQS publisher that emits jobs when uploads complete, and a worker pool
// that consumes and processes them asynchronously.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	appcfg "go-drive-clone/internal/config"
)

// ThumbnailMessage is the structured payload published to SQS when an upload
// completes. Workers consume this to know which file to thumbnail.
type ThumbnailMessage struct {
	FileID string `json:"file_id"`
	UserID string `json:"user_id"`
}

// Publisher abstracts the ability to enqueue a thumbnail job. The upload
// service depends on this interface (not the concrete SQS type) so it can be
// swapped for a no-op or in-memory publisher in tests.
type Publisher interface {
	PublishThumbnailJob(ctx context.Context, msg ThumbnailMessage) error
}

// NoopPublisher is a Publisher that does nothing. Used when SQS is not
// configured (e.g. local development) so the upload flow still works.
type NoopPublisher struct{}

// PublishThumbnailJob silently succeeds.
func (NoopPublisher) PublishThumbnailJob(_ context.Context, _ ThumbnailMessage) error {
	return nil
}

// sqsSender is the subset of the SQS client API the publisher uses. Declared
// locally so tests can supply a fake instead of hitting real AWS.
type sqsSender interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// SQSPublisher publishes thumbnail jobs to an AWS SQS queue.
type SQSPublisher struct {
	client   sqsSender
	queueURL string
	log      *slog.Logger
}

// NewSQSPublisher builds a publisher from an SQS client and the configured
// queue URL. If queueURL is empty, callers should use NoopPublisher instead.
func NewSQSPublisher(client sqsSender, queueURL string, log *slog.Logger) *SQSPublisher {
	return &SQSPublisher{client: client, queueURL: queueURL, log: log}
}

// PublishThumbnailJob serialises the message and sends it to SQS.
func (p *SQSPublisher) PublishThumbnailJob(ctx context.Context, msg ThumbnailMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal thumbnail message: %w", err)
	}

	_, err = p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.queueURL),
		MessageBody: aws.String(string(body)),
	})
	if err != nil {
		return fmt.Errorf("send sqs message: %w", err)
	}

	p.log.Info("thumbnail job published",
		"file_id", msg.FileID,
		"user_id", msg.UserID,
	)
	return nil
}

// Compile-time checks.
var _ Publisher = (*SQSPublisher)(nil)
var _ Publisher = NoopPublisher{}

// NewSQSClient creates an SQS client from the application config, sharing the
// same AWS credential chain and region as the S3 client.
func NewSQSClient(cfg appcfg.Config) *sqs.Client {
	awsCfg := aws.Config{Region: cfg.AWSRegion}

	// Static credentials override the default chain (same logic as S3 driver).
	if cfg.AWSAccessKeyID != "" && cfg.AWSSecretAccessKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentialsProvider(
			cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, "",
		)
	}

	return sqs.NewFromConfig(awsCfg)
}
