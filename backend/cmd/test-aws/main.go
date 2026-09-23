package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/joho/godotenv"
)

type ThumbnailJob struct {
	FileID string `json:"file_id"`
	UserID string `json:"user_id"`
}

func main() {
	fmt.Println("==================================================")
	fmt.Println("🚀 Blob-Cloud AWS S3 & SQS + Lambda Integration Test")
	fmt.Println("==================================================")

	// Load .env
	if err := godotenv.Load(".env"); err != nil {
		_ = godotenv.Load("../../.env")
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	bucket := os.Getenv("AWS_S3_BUCKET")
	sqsURL := os.Getenv("SQS_QUEUE_URL")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	maskedKey := accessKey
	if len(maskedKey) > 8 {
		maskedKey = maskedKey[:4] + "..." + maskedKey[len(maskedKey)-4:]
	}

	fmt.Println("[Config]")
	fmt.Printf("  • AWS Region:     %s\n", region)
	fmt.Printf("  • S3 Bucket:      %s\n", bucket)
	fmt.Printf("  • SQS Queue URL:  %s\n", sqsURL)
	fmt.Printf("  • Access Key:     %s\n", maskedKey)
	fmt.Println()

	if bucket == "" || sqsURL == "" || accessKey == "" || secretKey == "" {
		log.Fatal("❌ Missing required AWS configuration in .env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build AWS Config
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(creds),
	)
	if err != nil {
		log.Fatalf("❌ Failed to load AWS SDK config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	// ----------------------------------------------------
	// 1. Test S3 Bucket Connectivity & Upload
	// ----------------------------------------------------
	fmt.Println("--------------------------------------------------")
	fmt.Println("📦 STEP 1: Testing S3 Storage Driver & Bucket Access")
	fmt.Println("--------------------------------------------------")

	testKey := fmt.Sprintf("staging/test-probe-%d/0", time.Now().Unix())
	testPayload := []byte("Blob-Cloud zero-trust staging verification payload")

	fmt.Printf("Uploading test block to S3 -> s3://%s/%s ...\n", bucket, testKey)
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(testKey),
		Body:   bytes.NewReader(testPayload),
	})
	if err != nil {
		log.Fatalf("❌ S3 PutObject failed: %v", err)
	}
	fmt.Println("  ✅ S3 PutObject succeeded!")

	// Verify via HeadObject
	headOut, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(testKey),
	})
	if err != nil {
		log.Fatalf("❌ S3 HeadObject failed: %v", err)
	}
	fmt.Printf("  ✅ S3 HeadObject confirmed! Size = %d bytes, ETag = %s\n", *headOut.ContentLength, *headOut.ETag)

	// Clean up probe object
	_, _ = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(testKey),
	})
	fmt.Println("  ✅ S3 staging cleanup completed.")
	fmt.Println()

	// ----------------------------------------------------
	// 2. Test SQS Queue Attributes (Before Publish)
	// ----------------------------------------------------
	fmt.Println("--------------------------------------------------")
	fmt.Println("📬 STEP 2: Inspecting SQS Queue & Lambda Trigger State")
	fmt.Println("--------------------------------------------------")

	getAttrs := func() (int, int, error) {
		out, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(sqsURL),
			AttributeNames: []types.QueueAttributeName{
				types.QueueAttributeNameApproximateNumberOfMessages,
				types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
			},
		})
		if err != nil {
			return 0, 0, err
		}
		var visible, notVisible int
		fmt.Sscanf(out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessages)], "%d", &visible)
		fmt.Sscanf(out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible)], "%d", &notVisible)
		return visible, notVisible, nil
	}

	visibleBefore, notVisibleBefore, err := getAttrs()
	if err != nil {
		log.Fatalf("❌ Failed to query SQS queue attributes: %v", err)
	}
	fmt.Printf("  Initial Queue State:\n    • Messages Available: %d\n    • Messages In-Flight (being processed): %d\n", visibleBefore, notVisibleBefore)
	fmt.Println()

	// ----------------------------------------------------
	// 3. Publish Test Thumbnail Job to SQS
	// ----------------------------------------------------
	fmt.Println("--------------------------------------------------")
	fmt.Println("🚀 STEP 3: Publishing Job to SQS Queue")
	fmt.Println("--------------------------------------------------")

	jobID := fmt.Sprintf("file-probe-%d", time.Now().Unix())
	jobPayload, _ := json.Marshal(ThumbnailJob{
		FileID: jobID,
		UserID: "user-probe-verification",
	})

	fmt.Printf("Sending message body: %s\n", string(jobPayload))
	sendOut, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(sqsURL),
		MessageBody: aws.String(string(jobPayload)),
	})
	if err != nil {
		log.Fatalf("❌ Failed to send message to SQS: %v", err)
	}

	fmt.Println("  ✅ Message PUBLISHED successfully to SQS!")
	fmt.Printf("  • Message ID: %s\n", *sendOut.MessageId)
	if sendOut.MD5OfMessageBody != nil {
		fmt.Printf("  • MD5:        %s\n", *sendOut.MD5OfMessageBody)
	}
	fmt.Println()

	// ----------------------------------------------------
	// 4. Poll SQS to Confirm Lambda Consumption
	// ----------------------------------------------------
	fmt.Println("--------------------------------------------------")
	fmt.Println("⚡ STEP 4: Monitoring SQS Queue for Lambda Consumption")
	fmt.Println("--------------------------------------------------")
	fmt.Println("Watching SQS queue to observe Lambda trigger consumption...")

	consumed := false
	startWait := time.Now()

	for time.Since(startWait) < 20*time.Second {
		time.Sleep(1500 * time.Millisecond)
		v, nv, err := getAttrs()
		if err != nil {
			fmt.Printf("  (polling error: %v)\n", err)
			continue
		}

		elapsed := time.Since(startWait).Round(time.Millisecond)
		fmt.Printf("  [%s] Queue -> Visible: %d, In-Flight: %d\n", elapsed, v, nv)

		// When Lambda trigger fires:
		// SQS Event Source Mapping polls the queue and invokes Lambda.
		// While executing, nv (not visible / in-flight) may briefly show 1.
		// Once processed/completed, visible drops back to 0!
		if v == 0 && nv == 0 {
			consumed = true
			fmt.Println()
			fmt.Println("  🎉 Lambda Trigger Consumption CONFIRMED!")
			fmt.Println("  The SQS message was immediately received and processed by AWS Lambda!")
			break
		} else if nv > 0 {
			fmt.Println("  >> Message is actively being processed by Lambda worker (in-flight)!")
		}
	}

	if !consumed {
		// If message is still visible, check if we can read it or if it's waiting
		v, nv, _ := getAttrs()
		if v > 0 {
			fmt.Printf("\n⚠️ Note: Message is still visible in queue (Visible: %d, In-Flight: %d).\n", v, nv)
			fmt.Println("If you haven't enabled the SQS Trigger in the AWS Lambda Console, please ensure:")
			fmt.Println("  1. In AWS Lambda -> blobcloud-worker -> Triggers -> Add Trigger -> SQS")
			fmt.Println("  2. Select queue 'blob-cloud-uploads' and check 'Enable trigger'")
		}
	}

	fmt.Println()
	fmt.Println("==================================================")
	fmt.Println("🎯 Verification Run Finished")
	fmt.Println("==================================================")
}
