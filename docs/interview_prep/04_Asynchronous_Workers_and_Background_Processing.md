# 04. Asynchronous Workers & Background Processing

## 1. Asynchronous Architecture & Decoupling Rationale

To maintain sub-100ms API response times for file uploads, heavy post-processing operations—specifically **Image Thumbnail Generation**—are entirely decoupled from the synchronous HTTP request lifecycle.

```
+-------------------+           +-------------------+           +-------------------+
|  HTTP API Handler |           |  AWS SQS Queue    |           |  Go Worker Pool   |
| (upload_complete) |           |  (Event Stream)   |           |  (Concurrent)     |
+---------+---------+           +---------+---------+           +---------+---------+
          |                               ^                               |
          | 1. Publish Event Payload      |                               |
          +-------------------------------+                               |
                                          | 2. Long-Poll Receive (20s)    |
                                          +-------------------------------+
                                                                          |
                                                                          | 3. Process Image
                                                                          v
                                                                +-------------------+
                                                                | In-Memory Resizer |
                                                                | (200x200 PNG)     |
                                                                +---------+---------+
                                                                          |
                                                                          | 4. Put S3 Thumbnail
                                                                          v
                                                                +-------------------+
                                                                | AWS S3 Bucket     |
                                                                | /thumbnails/*.png |
                                                                +-------------------+
```

---

## 2. Queue Producer & Worker Pipeline (`backend/internal/queue/`)

### A. Event Publisher ([publisher.go](file:///c:/Users/Asus/Desktop/z/backend/internal/queue/publisher.go))
When `POST /api/upload/complete` finishes successfully for an image file (MIME type `image/png`, `image/jpeg`), the Go backend publishes a JSON event payload to AWS SQS:

```json
{
  "event_type": "FILE_UPLOADED",
  "file_id": "c56a4180-65aa-42ec-a945-5fd21dec0538",
  "mime_type": "image/jpeg",
  "owner_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
}
```

---

### B. Worker Pool Engine & Long Polling ([worker.go](file:///c:/Users/Asus/Desktop/z/backend/internal/queue/worker.go))
- **Long-Polling**: SQS consumer uses a 20-second wait time (`WaitTimeSeconds: 20`). Long polling eliminates empty responses, reducing AWS SQS API billing costs by up to 95%.
- **Worker Pool**: Spins up a configurable number of worker goroutines (e.g. `SQS_NUM_WORKERS=3`) listening on an SQS message channel.
- **Graceful Shutdown**: Listens for OS signal interrupts (`SIGINT`, `SIGTERM`). When triggered, workers complete active message processing before closing.

---

### C. In-Memory Image Thumbnail Processor ([processor.go](file:///c:/Users/Asus/Desktop/z/backend/internal/queue/processor.go))
1. Fetches block streams associated with `file_id` from S3.
2. Decodes image in-memory using Go standard library (`image.Decode`).
3. Resizes image to a **200x200 pixel square thumbnail** using bi-linear interpolation.
4. Encodes thumbnail as PNG into an `bytes.Buffer`.
5. Uploads PNG binary to S3 object path `/thumbnails/{file_id}.png`.
6. Sends `DeleteMessage` to SQS to acknowledge job completion.

---

## 3. Trade-Offs & Architectural Alternatives

| Approach | Implemented in Blob-Cloud | Alternative Approach | Why We Chose Our Approach |
| :--- | :--- | :--- | :--- |
| **Queue Technology** | **AWS SQS** | RabbitMQ / Kafka / Redis Streams | AWS SQS is serverless, fully managed, highly durable, and operates within AWS Free Tier. |
| **Execution Host** | **In-House Go Goroutine Worker Pool** | AWS Lambda Serverless Functions | Running workers inside the Go container eliminates cold start latencies and avoids additional cloud invocation fees. |
| **Processing Mode** | **Asynchronous Post-Upload Queue** | Synchronous Inline Upload Processing | Inline processing blocks the user's upload finish API call by 1-3 seconds per image; async completes in background instantly. |

---

## 4. Interviewer Deep-Dive Q&A

### Q1: How do you handle job failures or poisonous messages in SQS?
**Answer**:
If an image binary is corrupted and causes `processor.go` to error out:
1. The worker logs the failure and does **not** call `DeleteMessage`.
2. After the SQS **Visibility Timeout** (e.g., 30 seconds) expires, SQS makes the message visible again.
3. SQS tracks `ReceiveCount`. We configure an **SQS Dead Letter Queue (DLQ)** with `maxReceiveCount = 3`. If a message fails 3 times, SQS routes it to DLQ for developer investigation without blocking the queue.

### Q2: How do worker goroutines prevent memory spikes when processing huge images (e.g., 50MB 4K photos)?
**Answer**:
We limit worker concurrency using a bounded worker pool (semaphore channel pattern). If `SQS_NUM_WORKERS=3`, at most 3 images are processed concurrently in memory. Furthermore, we inspect image metadata headers (`image.DecodeConfig`) to reject absurd dimensions (>10,000px) before allocating pixel buffers.
