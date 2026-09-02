package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	appcfg "go-drive-clone/internal/config"
	"go-drive-clone/internal/domain"
)

// S3 block key prefix. Defined as a var (not const) to avoid colliding with
// local.go's BlockPrefix since they live in the same package and have the same
// value.
var s3BlockPrefix = "blocks"

// S3Storage implements domain.StorageProvider using AWS S3 (or any S3-compatible
// service like Cloudflare R2). It generates presigned PUT URLs for direct
// client-to-S3 uploads and optionally rewrites the hostname to a CDN domain.
type S3Storage struct {
	client       *s3.Client
	presigner    *s3.PresignClient
	bucket       string
	cdndomain    string // empty = no rewrite; set = replace bucket host with this
	log          *slog.Logger
}

// Compile-time check.
var _ domain.StorageProvider = (*S3Storage)(nil)

// NewS3Storage initialises the S3 client and presigner from cfg.
//
// Credential strategy:
//   - If AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are both set, static
//     credentials are used (ideal for Cloudflare R2 or CI/dev).
//   - Otherwise the AWS SDK default credential chain is consulted (env vars,
//     IAM role, ~/.aws/credentials, etc.).
//
// Custom endpoint:
//   - If AWS_S3_ENDPOINT is set the client routes to that URL instead of the
//     default regional endpoint. This is required for Cloudflare R2.
func NewS3Storage(ctx context.Context, cfg appcfg.Config, log *slog.Logger) (*S3Storage, error) {
	var opts []func(*awscfg.LoadOptions) error

	// Region is always required (even for R2; it just isn't used for routing).
	opts = append(opts, awscfg.WithRegion(cfg.AWSRegion))

	// Static credentials override the default chain.
	if cfg.AWSAccessKeyID != "" && cfg.AWSSecretAccessKey != "" {
		opts = append(opts, awscfg.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, "",
			),
		))
	}

	awsCfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	s3Opts := []func(*s3.Options){}

	// Custom endpoint for R2 or minio.
	if cfg.AWSS3Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.AWSS3Endpoint)
			// R2 uses path-style addressing.
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	s := &S3Storage{
		client:    client,
		presigner: s3.NewPresignClient(client),
		bucket:    cfg.AWSS3Bucket,
		cdndomain: cfg.CloudFrontDomain,
		log:       log,
	}

	// Verify we can actually reach the bucket.
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	}); err != nil {
		return nil, fmt.Errorf("head bucket %q: %w", s.bucket, err)
	}

	log.Info("s3 storage driver ready",
		"bucket", s.bucket,
		"region", cfg.AWSRegion,
		"endpoint", cfg.AWSS3Endpoint,
		"cdn", cfg.CloudFrontDomain,
	)
	return s, nil
}

// GenerateUploadURL returns a presigned PUT URL the client uses to upload a
// block directly to S3 (or R2). If a CDN domain is configured the hostname in
// the returned URL is rewritten while the signature query parameters are left
// untouched.
func (s *S3Storage) GenerateUploadURL(ctx context.Context, blockHash string, expires time.Duration) (string, error) {
	if blockHash == "" {
		return "", fmt.Errorf("blockHash must not be empty")
	}
	key := s3BlockPrefix + "/" + blockHash

	presigned, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(po *s3.PresignOptions) {
		po.Expires = expires
	})
	if err != nil {
		return "", fmt.Errorf("presign put object: %w", err)
	}

	rawURL := presigned.URL
	if s.cdndomain != "" {
		rawURL = rewriteHost(rawURL, s.cdndomain)
	}
	return rawURL, nil
}

// PutObject uploads a stream to S3 under the given key.
func (s *S3Storage) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   reader,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("s3 put object %q: %w", key, err)
	}
	return nil
}

// GetObject opens an S3 object for streaming reads. The caller must close the
// returned ReadCloser.
func (s *S3Storage) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get object %q: %w", key, err)
	}
	return out.Body, nil
}

// GetObjectRange opens the stored S3 object starting at offset for up to length bytes.
func (s *S3Storage) GetObjectRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	var rangeHeader string
	if length >= 0 {
		rangeHeader = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	} else {
		rangeHeader = fmt.Sprintf("bytes=%d-", offset)
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get object range %q: %w", key, err)
	}
	return out.Body, nil
}

// HeadObject fetches object metadata directly from S3.
func (s *S3Storage) HeadObject(ctx context.Context, key string) (*domain.ObjectMetadata, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 head object %q: %w", key, err)
	}
	var contentLength int64
	if out.ContentLength != nil {
		contentLength = *out.ContentLength
	}
	var etag string
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, "\"")
	}
	return &domain.ObjectMetadata{
		ContentLength: contentLength,
		ETag:          etag,
	}, nil
}

// DeleteObject removes an object from S3. Missing objects are not an error
// (idempotent, matching S3 semantics).
func (s *S3Storage) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 delete object %q: %w", key, err)
	}
	return nil
}

// rewriteHost replaces the hostname in a presigned S3 URL with the CDN domain.
// Only the scheme+host portion of the URL is rewritten; path, query (signature),
// and fragment are preserved verbatim.
func rewriteHost(rawURL, cdnDomain string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, return the original URL rather than losing it.
		return rawURL
	}
	u.Host = strings.TrimPrefix(cdnDomain, "https://")
	u.Host = strings.TrimPrefix(u.Host, "http://")
	// Preserve https if the CDN domain specified it.
	if strings.HasPrefix(cdnDomain, "https://") {
		u.Scheme = "https"
	}
	return u.String()
}

// ListBlockKeys pages through S3 ListObjectsV2 under the "blocks/" prefix and
// returns every key found. Satisfies the gc.BlockLister interface used by the
// orphaned-block garbage collector.
func (s *S3Storage) ListBlockKeys(ctx context.Context) ([]string, error) {
	prefix := s3BlockPrefix + "/"
	var keys []string
	var continuationToken *string

	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("s3 list objects: %w", err)
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated || out.NextContinuationToken == nil {
			break
		}
		continuationToken = out.NextContinuationToken
	}
	return keys, nil
}

// ── MultipartUploadProvider implementation (Tier 3G) ─────────────────────────
//
// S3Storage satisfies domain.MultipartUploadProvider in addition to
// domain.StorageProvider. The four methods below implement the S3 Multipart
// Upload protocol, required for objects > 5 GiB.

// Compile-time check: S3Storage implements both interfaces.
var _ domain.MultipartUploadProvider = (*S3Storage)(nil)

// CreateMultipartUpload initiates an S3 MPU and returns its upload ID.
// The returned ID must be threaded through all subsequent part calls and the
// final CompleteMultipartUpload / AbortMultipartUpload.
func (s *S3Storage) CreateMultipartUpload(ctx context.Context, key string) (string, error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return "", fmt.Errorf("s3 create multipart upload key=%q: %w", key, err)
	}
	if out.UploadId == nil {
		return "", fmt.Errorf("s3 create multipart upload: nil upload ID returned")
	}
	return *out.UploadId, nil
}

// PresignUploadPart returns a presigned PUT URL for one MPU part.
// partNumber is 1-indexed per the S3 spec (valid range 1–10 000).
func (s *S3Storage) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, expires time.Duration) (string, error) {
	presigned, err := s.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, func(po *s3.PresignOptions) {
		po.Expires = expires
	})
	if err != nil {
		return "", fmt.Errorf("presign upload part %d key=%q: %w", partNumber, key, err)
	}
	rawURL := presigned.URL
	if s.cdndomain != "" {
		rawURL = rewriteHost(rawURL, s.cdndomain)
	}
	return rawURL, nil
}

// CompleteMultipartUpload assembles the previously uploaded parts into the
// final S3 object. etags must be the ETag values returned by each part PUT
// response (surrounding quotes are normalised automatically).
func (s *S3Storage) CompleteMultipartUpload(ctx context.Context, key, uploadID string, etags []string) error {
	parts := make([]s3types.CompletedPart, len(etags))
	for i, etag := range etags {
		e := strings.Trim(etag, "\"")
		parts[i] = s3types.CompletedPart{
			ETag:       aws.String(e),
			PartNumber: aws.Int32(int32(i + 1)), // 1-indexed
		}
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		return fmt.Errorf("s3 complete multipart upload key=%q upload=%q: %w", key, uploadID, err)
	}
	return nil
}

// AbortMultipartUpload cancels an in-progress MPU and frees partial-part
// storage. Always call this on any error path after CreateMultipartUpload.
func (s *S3Storage) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("s3 abort multipart upload key=%q upload=%q: %w", key, uploadID, err)
	}
	return nil
}
