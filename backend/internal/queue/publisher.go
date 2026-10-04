// Package queue implements the event-driven thumbnail processing pipeline:
// an SQS publisher that emits jobs when uploads complete, and a worker pool
// that consumes and processes them asynchronously.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
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

// NewSQSClient creates an SQS client from the application config.
// Credential resolution order:
// 1. Dedicated SQS IAM static credentials (SQSAccessKeyID / SQSSecretAccessKey).
// 2. Standard AWS IAM credentials (AWSAccessKeyID starting with AKIA or ASIA).
// 3. AWS default credential chain (EC2 IAM Instance Profile Role / environment / metadata).
// Note: Cloudflare R2 tokens (32-character hex) are strictly filtered out to prevent InvalidClientTokenId.
func NewSQSClient(cfg appcfg.Config) *sqs.Client {
	var opts []func(*awscfg.LoadOptions) error
	if cfg.AWSRegion != "" {
		opts = append(opts, awscfg.WithRegion(cfg.AWSRegion))
	}

	if cfg.SQSAccessKeyID != "" && cfg.SQSSecretAccessKey != "" {
		opts = append(opts, awscfg.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.SQSAccessKeyID, cfg.SQSSecretAccessKey, "",
			),
		))
	} else if cfg.AWSAccessKeyID != "" && cfg.AWSSecretAccessKey != "" && isAWSKey(cfg.AWSAccessKeyID) {
		opts = append(opts, awscfg.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, "",
			),
		))
	}

	awsCfg, err := awscfg.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		awsCfg = aws.Config{Region: cfg.AWSRegion}
	}

	return sqs.NewFromConfig(awsCfg)
}

func isAWSKey(k string) bool {
	return strings.HasPrefix(k, "AKIA") || strings.HasPrefix(k, "ASIA")
}
