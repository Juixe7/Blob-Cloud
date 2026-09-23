# Phase 1 Walkthrough: Distributed Service Decoupling & Serverless Workers

We have completed **Phase 1: Distributed Service Decoupling**. Blob-Cloud has been restructured into an **Event-Driven Distributed Microservices Architecture** with a stateless HTTP API Gateway, a standalone worker daemon, a serverless AWS Lambda worker, Docker Compose orchestration, and production Kubernetes manifests with KEDA autoscaling.

---

## 1. What Was Built & Modified

| Component | Path | Description |
| :--- | :--- | :--- |
| **Stateless API Gateway** | `backend/cmd/api/main.go` | Removed in-process SQS consumer loop. The API Gateway strictly handles REST endpoints, WebSockets, rate limiting, and publishes jobs to SQS. |
| **Standalone Worker Daemon** | `backend/cmd/worker/main.go` | Standalone background service that connects to PostgreSQL, S3, Redis, ClamAV, and Gemini, long-polling SQS for background processing jobs. |
| **Serverless Lambda Worker** | `backend/cmd/worker-lambda/main.go` | Compiled with `aws-lambda-go` and `events.SQSEvent`. Implements **SQS Partial Batch Responses** (`SQSEventResponse`) so only failed messages are retried. Designed for AWS Student Free Tier ($0 idle cost). |
| **Lambda Unit Tests** | `backend/cmd/worker-lambda/main_test.go` | Unit tests verifying batch processing, partial failure reporting, and malformed JSON poison-pill filtering. |
| **Multi-Stage Dockerfile** | `backend/Dockerfile` | Production Dockerfile with 3 optimized minimal build targets: `api`, `worker`, and `gc`. |
| **Microservice Compose** | `docker-compose.yml` | Orchestrates `postgres` (with `pgvector`), `redis`, `api`, and `worker` in an isolated network. |
| **Kubernetes Manifests & KEDA** | `deploy/k8s/` | Contains `api-deployment.yaml`, `worker-deployment.yaml`, `configmap.yaml`, `secrets.yaml`, and `keda-sqs-autoscaler.yaml` (scales worker pods dynamically 0 $\rightarrow$ 15 based on SQS queue backlog). |

---

## 2. Automated Test & Build Results

```powershell
# 1. Lambda Worker unit test suite
cd backend
go test -v ./cmd/worker-lambda/...
# Result: PASS (All 3 tests passed)
# === RUN   TestProcessSQSEvent_AllSuccess         --- PASS (0.00s)
# === RUN   TestProcessSQSEvent_PartialFailure     --- PASS (0.00s)
# === RUN   TestProcessSQSEvent_MalformedJSONDropped --- PASS (0.00s)

# 2. Multi-Binary compilation test
go build ./cmd/...
# Result: Exit 0 (cmd/api, cmd/worker, cmd/worker-lambda, cmd/gc compiled cleanly)

# 3. Full regression test suite
go test -v ./...
# Result: PASS across all packages (internal/queue, internal/ratelimit, internal/gc, internal/audit, internal/sync, internal/storage, internal/transport/http)
```

---

## 3. Beginner-Friendly Guide: Setting Up AWS SQS + Lambda on AWS Student Free Tier

### Step A: Create the SQS Queue (Free Tier: 1 Million Free Requests/Month)
1. Log into your **AWS Management Console**.
2. In the top search bar, type **SQS** and click **Simple Queue Service**.
3. Click the orange **Create queue** button.
4. Select **Standard** (default).
5. In **Name**, enter: `blobcloud-jobs`.
6. Leave the configuration defaults (Visibility timeout: 30 seconds, Message retention: 4 days).
7. Scroll down to the bottom and click **Create queue**.
8. Copy the **URL** shown on the details page (e.g. `https://sqs.us-east-1.amazonaws.com/123456789012/blobcloud-jobs`). This will be your `SQS_QUEUE_URL`.

---

### Step B: Build the Go Lambda Binary for AWS (Linux ARM64/AMD64)
AWS Lambda runs on Amazon Linux. On your Windows machine, build the Go binary targeting Linux:

```powershell
cd c:\Users\Asus\Desktop\z\backend

# Set target OS to Linux and architecture to amd64 (x86_64)
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# Build bootstrap binary (AWS Lambda custom runtime convention)
go build -tags lambda.norpc -ldflags="-s -w" -o bootstrap ./cmd/worker-lambda

# Create a zip file containing the bootstrap binary
Compress-Archive -Path bootstrap -DestinationPath lambda_worker.zip -Force

# Reset environment variables
$env:GOOS = ""
$env:GOARCH = ""
```

---

### Step C: Create the Lambda Function in AWS Console (Free Tier: 1M Invocations/Month)
1. In the AWS Console search bar, type **Lambda** and click **Lambda**.
2. Click **Create function**.
3. Select **Author from scratch**.
4. Set:
   * **Function name**: `blobcloud-worker`
   * **Runtime**: Select **Amazon Linux 2023** (under Custom runtime) or **Provide your own bootstrap on Amazon Linux 2023**.
   * **Architecture**: **x86_64**.
5. Under **Permissions**, select **Create a new role with basic Lambda permissions**.
6. Click **Create function**.

---

### Step D: Upload the Code & Configure SQS Trigger
1. On the function page, under the **Code** tab, click **Upload from** $\rightarrow$ **.zip file**.
2. Choose the `lambda_worker.zip` file you created in Step B and click **Save**.
3. Go to the **Configuration** tab:
   * Click **Environment variables** $\rightarrow$ **Edit** $\rightarrow$ add:
     - `STORAGE_PROVIDER`: `s3`
     - `S3_BUCKET`: your bucket name
     - `AWS_REGION`: your region (e.g. `us-east-1`)
     - `DB_URL`: your RDS/Postgres connection string
     - `GEMINI_API_KEY`: your Gemini API key
   * Click **General configuration** $\rightarrow$ **Edit**:
     - Change **Timeout** to `1 min 0 sec`.
     - Change **Memory** to `256 MB` or `512 MB`.
4. Go to **Triggers**:
   * Click **Add trigger**.
   * Select **SQS**.
   * In **SQS queue**, choose `blobcloud-jobs`.
   * Check the box: **Report batch item failures** (our Go code supports this natively!).
   * Click **Add**.

---

### Step E: Grant Lambda Permission to Read SQS & S3
1. Under **Configuration** $\rightarrow$ **Permissions**, click the **Role name** link (opens AWS IAM in a new tab).
2. Click **Add permissions** $\rightarrow$ **Attach policies**.
3. Search and attach:
   * `AmazonSQSFullAccess` (or `AWSLambdaSQSQueueExecutionRole`)
   * `AmazonS3FullAccess`
4. Click **Add permissions**.

**That’s it!** Now, whenever your API server receives an upload and puts a message into SQS, AWS will automatically wake up your Lambda function, process the thumbnail and Gemini embeddings, and terminate. When no uploads are happening, you pay **$0.00**.
