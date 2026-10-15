package sync

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	stdsync "sync"
	"testing"
	"time"
)

func newTestHub(t *testing.T) (*Hub, func()) {
	t.Helper()
	h := NewHub(discardLogger())
	go h.Run()
	return h, h.Shutdown
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRegisterAddsClient verifies a registered client appears in the hub's
// internal map.
func TestRegisterAddsClient(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c)

	// Give the run loop a moment to process.
	time.Sleep(10 * time.Millisecond)

	h.mu.RLock()
	conns := h.clie["u1"]
	h.mu.RUnlock()

	if conns == nil {
		t.Fatal("user u1 not found in hub registry after register")
	}
	if _, ok := conns[c]; !ok {
		t.Fatal("specific client not in u1's connection set")
	}
}

// TestRegisterMultipleTabs allows a single user to have many open connections.
func TestRegisterMultipleTabs(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c1 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	c2 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	c3 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c1)
	h.Register(c2)
	h.Register(c3)

	time.Sleep(10 * time.Millisecond)

	h.mu.RLock()
	count := len(h.clie["u1"])
	h.mu.RUnlock()

	if count != 3 {
		t.Fatalf("expected 3 connections for u1, got %d", count)
	}
}

// TestUnregisterRemovesClient verifies the client is removed and its Send
// channel is closed.
func TestUnregisterRemovesClient(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c)
	time.Sleep(10 * time.Millisecond)

	h.Unregister(c)
	time.Sleep(10 * time.Millisecond)

	h.mu.RLock()
	conns := h.clie["u1"]
	h.mu.RUnlock()

	if conns != nil {
		t.Fatal("user u1 should have been removed after unregistering only connection")
	}

	// Send channel must be closed.
	_, ok := <-c.Send
	if ok {
		t.Fatal("client Send channel was not closed after unregister")
	}
}

// TestUnregisterOneOfMany verifies removing one tab leaves the others intact.
func TestUnregisterOneOfMany(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c1 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	c2 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c1)
	h.Register(c2)
	time.Sleep(10 * time.Millisecond)

	h.Unregister(c1)
	time.Sleep(10 * time.Millisecond)

	// c2 should still be alive.
	select {
	case <-c2.Send:
		t.Fatal("c2 Send was closed, but only c1 was unregistered")
	default:
	}

	h.mu.RLock()
	count := len(h.clie["u1"])
	h.mu.RUnlock()

	if count != 1 {
		t.Fatalf("expected 1 remaining connection for u1, got %d", count)
	}
}

// TestNotifyUserDelivers verifies a notification reaches the right user's
// connections.
func TestNotifyUserDelivers(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c)
	time.Sleep(10 * time.Millisecond)

	h.NotifyUser("u1", NotificationEvent{
		Type:    "TEST_EVENT",
		Payload: map[string]string{"key": "val"},
	})

	select {
	case msg := <-c.Send:
		var event NotificationEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if event.Type != "TEST_EVENT" {
			t.Fatalf("event type = %q, want TEST_EVENT", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("notification did not arrive within 1s")
	}
}

// TestNotifyUserDeliversAllTabs verifies a notification reaches every tab for
// a user.
func TestNotifyUserDeliversAllTabs(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c1 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	c2 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c1)
	h.Register(c2)
	time.Sleep(10 * time.Millisecond)

	h.NotifyUser("u1", NotificationEvent{Type: "MULTI", Payload: nil})

	for i, c := range []*Client{c1, c2} {
		select {
		case <-c.Send:
			// ok
		case <-time.After(time.Second):
			t.Fatalf("tab %d did not receive notification", i)
		}
	}
}

// TestNotifySkipsWrongUser verifies a notification sent to u2 does not reach
// u1.
func TestNotifySkipsWrongUser(t *testing.T) {
	h, shutdown := newTestHub(t)
	defer shutdown()

	c1 := &Client{UserID: "u1", Send: make(chan []byte, 16)}
	h.Register(c1)
	time.Sleep(10 * time.Millisecond)

	h.NotifyUser("u2", NotificationEvent{Type: "WRONG", Payload: nil})

	select {
	case <-c1.Send:
		t.Fatal("u1 received a notification meant for u2")
	case <-time.After(50 * time.Millisecond):
		// correct: no message for u1
	}
}

// TestShutdownClosesDone verifies calling Shutdown stops the run loop and the
// second call is a no-op.
func TestShutdownIsIdempotent(t *testing.T) {
	h := NewHub(discardLogger())
	go h.Run()
	time.Sleep(10 * time.Millisecond)

	h.Shutdown()
	h.Shutdown() // must not panic or deadlock
}

// TestNoopNotifierSatisfiesInterface is a compile-time check.
func TestNoopNotifierSatisfiesInterface(t *testing.T) {
	var _ Notifier = NoopNotifier()
}

// TestHub_ConcurrentTortureAndGoroutineLeakCheck hammers the Actor-model Hub
// from 60 concurrent goroutines performing simultaneous Register, Unregister,
// KickSession, GetActiveConnCount, and NotifyUser calls (including slow consumers
// whose Send buffers saturate), then shuts down and verifies zero leaked goroutines.
func TestHub_ConcurrentTortureAndGoroutineLeakCheck(t *testing.T) {
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	h := NewHub(discardLogger())
	runExited := make(chan struct{})
	go func() {
		defer close(runExited)
		h.Run()
	}()

	const workers = 60
	var wg stdsync.WaitGroup
	startGate := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startGate

			userID := fmt.Sprintf("user-%d", id%10)
			sessionID := fmt.Sprintf("sess-%d", id)
			// Mix normal clients (cap 16) and slow clients (cap 1) to exercise full-buffer drop path
			bufCap := 16
			if id%3 == 0 {
				bufCap = 1
			}
			client := &Client{
				UserID:    userID,
				SessionID: sessionID,
				Send:      make(chan []byte, bufCap),
			}

			h.Register(client)
			for j := 0; j < 5; j++ {
				h.NotifyUser(userID, NotificationEvent{
					Type:    EventSyncDelta,
					Payload: map[string]int{"worker": id, "iter": j},
				})
				_ = h.GetActiveConnCount(userID)
			}
			if id%5 == 0 {
				h.KickSession(sessionID)
			}
			h.Unregister(client)
			// Double unregister must be a safe no-op
			h.Unregister(client)
		}(i)
	}

	// Release all 60 goroutines simultaneously
	close(startGate)
	wg.Wait()

	h.Shutdown()
	select {
	case <-runExited:
	case <-time.After(time.Second):
		t.Fatal("Hub.Run() goroutine did not exit after Shutdown()")
	}

	// Allow runtime scheduler to reap exited goroutines and verify no goroutine leak
	time.Sleep(20 * time.Millisecond)
	runtime.GC()
	afterGoroutines := runtime.NumGoroutine()
	if afterGoroutines > baselineGoroutines+2 {
		t.Fatalf("goroutine leak detected: baseline=%d, after=%d", baselineGoroutines, afterGoroutines)
	}
}

