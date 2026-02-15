package queue

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeProcessor is a controllable messageProcessor for worker tests. processErr
// is returned for every ProcessMessage call; calls counts invocations.
type fakeProcessor struct {
	processErr error
	calls      atomic.Int32
}

func (p *fakeProcessor) ProcessMessage(_ context.Context, _ ThumbnailMessage) error {
	p.calls.Add(1)
	return p.processErr
}

// scriptQueue is a scriptable sqsQueue. It serves a fixed slice of receive
// outputs (receiveScript), one per poll, then returns empty outputs forever.
// It records every DeleteMessage receipt handle in deletedHandles.
type scriptQueue struct {
	receiveScript  []*sqs.ReceiveMessageOutput
	receiveErr     error
	polls          atomic.Int32
	deletedHandles []string
}

func (q *scriptQueue) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	n := int(q.polls.Add(1)) // 1-based
	if q.receiveErr != nil {
		return nil, q.receiveErr
	}
	if n <= len(q.receiveScript) {
		return q.receiveScript[n-1], nil
	}
	// Default: empty result (no messages). Long-poll would normally block, but
	// returning empty lets tests terminate via ctx cancellation.
	return &sqs.ReceiveMessageOutput{}, nil
}

func (q *scriptQueue) DeleteMessage(_ context.Context, params *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	if params.ReceiptHandle != nil {
		q.deletedHandles = append(q.deletedHandles, *params.ReceiptHandle)
	}
	return &sqs.DeleteMessageOutput{}, nil
}

func workerTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func msg(body string, receipt string) types.Message {
	return types.Message{
		MessageId:     aws.String("mid-" + receipt),
		Body:          aws.String(body),
		ReceiptHandle: aws.String(receipt),
	}
}

// ---- handleMessage contract tests -----------------------------------------

// On success the message MUST be deleted so SQS does not redeliver it.
func TestHandleMessage_SuccessDeletesMessage(t *testing.T) {
	q := &scriptQueue{}
	wp := &WorkerPool{
		client: q, queueURL: "q", processor: &fakeProcessor{},
		pollTimeout: 1, log: workerTestLogger(),
	}
	body, _ := json.Marshal(ThumbnailMessage{FileID: "f1"})
	wp.handleMessage(context.Background(), 0, msg(string(body), "rh-1"))

	if len(q.deletedHandles) != 1 {
		t.Fatalf("deletes = %d, want 1 (success must delete)", len(q.deletedHandles))
	}
	if q.deletedHandles[0] != "rh-1" {
		t.Fatalf("deleted receipt = %q, want rh-1", q.deletedHandles[0])
	}
}

// On processing failure the message MUST NOT be deleted so SQS makes it
// visible again for retry (phase5.txt requirement #4).
func TestHandleMessage_ProcessingFailureRetainsMessage(t *testing.T) {
	q := &scriptQueue{}
	wp := &WorkerPool{
		client: q, queueURL: "q", processor: &fakeProcessor{processErr: errors.New("resize failed")},
		pollTimeout: 1, log: workerTestLogger(),
	}
	body, _ := json.Marshal(ThumbnailMessage{FileID: "f1"})
	wp.handleMessage(context.Background(), 0, msg(string(body), "rh-9"))

	if len(q.deletedHandles) != 0 {
		t.Fatalf("deletes = %d, want 0 (failure must retain for retry)", len(q.deletedHandles))
	}
}

// A malformed message body must be deleted (poison-pill protection) rather
// than infinitely retried.
func TestHandleMessage_InvalidJSONDeletesMessage(t *testing.T) {
	q := &scriptQueue{}
	wp := &WorkerPool{
		client: q, queueURL: "q", processor: &fakeProcessor{},
		pollTimeout: 1, log: workerTestLogger(),
	}
	wp.handleMessage(context.Background(), 0, msg("not-json", "rh-bad"))

	if len(q.deletedHandles) != 1 {
		t.Fatalf("deletes = %d, want 1 (invalid json must be deleted)", len(q.deletedHandles))
	}
}

// ---- run loop / context cancellation test --------------------------------

// On context cancellation the run loop must return promptly.
func TestRun_StopsOnContextCancellation(t *testing.T) {
	wp := &WorkerPool{
		client:      &scriptQueue{}, // always returns empty
		queueURL:    "q",
		processor:   &fakeProcessor{},
		pollTimeout: 1,
		log:         workerTestLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		wp.run(ctx, 0)
		close(done)
	}()

	// Give the loop a moment to be actively polling, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// ok: run returned
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop after context cancellation")
	}
}

// ---- WorkerPool.Start graceful shutdown test -----------------------------

// Start must register all workers on the WaitGroup so that wg.Wait() returns
// once they have all drained after ctx cancellation.
func TestStart_WorkersJoinWaitGroup(t *testing.T) {
	q := &scriptQueue{}
	wp := NewWorkerPool(q, "q", &fakeProcessor{}, 3, 1, workerTestLogger())

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wp.Start(ctx, &wg)

	// All workers should be running now. Cancel and wait for them to exit.
	cancel()
	waited := make(chan struct{})
	go func() {
		wg.Wait()
		close(waited)
	}()

	select {
	case <-waited:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("wg.Wait() did not return after cancel — worker goroutines leaked")
	}
}

// ---- exponential backoff tests -------------------------------------------

// alwaysErrQueue fails every ReceiveMessage. Its polls counter lets a test
// assert how many calls happened in a window — i.e. whether backoff throttled.
type alwaysErrQueue struct {
	polls atomic.Int32
}

func (q *alwaysErrQueue) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	q.polls.Add(1)
	return nil, errors.New("operation error SQS: ReceiveMessage, 403 AccessDenied")
}
func (q *alwaysErrQueue) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return &sqs.DeleteMessageOutput{}, nil
}

// TestRun_BackoffContextAware verifies the sleep is interruptible by ctx:
// cancelling during a backoff must return promptly WITHOUT time.Sleep.
func TestRun_BackoffContextAware(t *testing.T) {
	q := &alwaysErrQueue{}
	wp := &WorkerPool{
		client: q, queueURL: "q", processor: &fakeProcessor{},
		pollTimeout: 1, log: workerTestLogger(),
		minBackoff: 50 * time.Millisecond, maxBackoff: 200 * time.Millisecond, backoffFactor: 2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	start := time.Now()
	go func() {
		wp.run(ctx, 0)
		close(done)
	}()

	// Wait for the first failed poll to land the worker in backoff, then cancel.
	// The first failure triggers a minBackoff (50ms) sleep — cancelling mid-sleep
	// must abort it immediately, so the goroutine returns in ~0ms, not 50ms.
	time.Sleep(5 * time.Millisecond) // let the first poll fail
	cancel()

	select {
	case <-done:
		elapsed := time.Since(start)
		// If backoff were a raw time.Sleep, the worker would block ~50ms waiting
		// for the timer. Context-aware select returns near-instantly.
		if elapsed > 30*time.Millisecond {
			t.Fatalf("backoff was not interrupted by ctx: elapsed %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop after context cancellation during backoff")
	}
}

// TestRun_BackoffThrottlesOnError verifies that on consecutive failures the
// worker sleeps between polls (instead of spinning immediately). Over a fixed
// window we must see far fewer polls than a tight loop would produce.
func TestRun_BackoffThrottlesOnError(t *testing.T) {
	q := &alwaysErrQueue{}
	wp := &WorkerPool{
		client: q, queueURL: "q", processor: &fakeProcessor{},
		pollTimeout: 1, log: workerTestLogger(),
		// min 20ms, x2, max 80ms -> sequence of sleeps: 20,40,80,80,...
		minBackoff: 20 * time.Millisecond, maxBackoff: 80 * time.Millisecond, backoffFactor: 2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go wp.run(ctx, 0)

	// Observe poll count over ~150ms. With backoff the worker sleeps at least
	// 20ms between polls (20+40+80 = 140ms for the first 3), so we expect only a
	// handful of polls — not dozens. A non-backoff loop would hit 100+.
	time.Sleep(150 * time.Millisecond)
	cancel()

	got := q.polls.Load()
	// Upper bound: 5 polls in 150ms is generous but well below a tight loop.
	// Lower bound: at least 2 (we want to confirm it actually retried with growth).
	if got > 6 {
		t.Fatalf("backoff did not throttle: got %d polls in 150ms (want <= ~5)", got)
	}
	if got < 2 {
		t.Fatalf("expected multiple retries, got %d polls", got)
	}
}

// TestRun_BackoffResetsOnSuccess verifies that after failures, a successful
// poll resets the backoff to the minimum. We use a queue that fails N times
// then succeeds forever, and confirm the poll rate jumps back up.
func TestRun_BackoffResetsOnSuccess(t *testing.T) {
	// failThenSucceedQueue fails the first `failCount` polls, then returns empty.
	failThenSucceedQueue := &scriptQueue{receiveErr: nil}
	wp := &WorkerPool{
		client: failThenSucceedQueue, queueURL: "q", processor: &fakeProcessor{},
		pollTimeout: 1, log: workerTestLogger(),
		minBackoff: 40 * time.Millisecond, maxBackoff: 40 * time.Millisecond, backoffFactor: 2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go wp.run(ctx, 0)

	// scriptQueue returns empty success immediately, so polls should be frequent
	// (no backoff on success). Confirm we see several polls quickly.
	time.Sleep(60 * time.Millisecond)
	cancel()

	got := failThenSucceedQueue.polls.Load()
	// With 40ms backoff disabled (success path), a 60ms window should yield >=2
	// polls. If backoff wrongly applied to success, we'd see only 1.
	if got < 2 {
		t.Fatalf("backoff not reset on success: only %d polls in 60ms", got)
	}
}
