// Package sync implements the real-time notification layer for Blob-Cloud.
//
// A central Hub maintains a registry of active WebSocket connections keyed by
// user id (one user may have many open tabs). All mutations to the registry —
// registrations, deregistrations, and broadcasts — are serialised through
// channels and processed by a single goroutine (the Hub's Run loop), so the
// map is never accessed concurrently.
package sync

import (
	"encoding/json"
	"log/slog"
	stdsync "sync"

	"github.com/gorilla/websocket"
)

// Event types broadcast over WebSocket. These are the string the browser client
// switches on to decide how to react (refresh the file list, show a toast, …).
const (
	EventThumbnailReady = "THUMBNAIL_READY"
	EventFileShared     = "FILE_SHARED"
	EventUploadComplete = "UPLOAD_COMPLETED"
)

// NotificationEvent is the JSON envelope pushed to every connected client.
//
//	{"type": "THUMBNAIL_READY", "payload": { ... }}
type NotificationEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Client is one live WebSocket connection belonging to a user. The Hub sends
// events to a client by writing to its Send channel; the per-connection write
// pump (see websocket_handlers.go) drains that channel.
type Client struct {
	UserID string
	Conn   *websocket.Conn
	// Send is buffered so a slow client doesn't block the Hub's run loop. If the
	// buffer fills, the Hub closes the connection (treated as a dead client).
	Send chan []byte
}

// register / unregister / notify are the internal messages exchanged with the
// Hub's single-threaded run loop. Using typed structs keeps the select arms
// self-documenting.
type register struct {
	client *Client
}

type unregister struct {
	client *Client
}

// notify routes an event to one or more users. It is the only message type
// that carries a payload.
type notify struct {
	userIDs []string
	event   NotificationEvent
}

// Notifier is the narrow inbound interface the rest of the app depends on. By
// depending on this rather than *Hub, callers (upload service, SQS processor,
// share handler) are decoupled from the Hub internals and trivially testable
// with a fake.
type Notifier interface {
	// NotifyUser sends an event to all active connections for the given user.
	NotifyUser(userID string, event NotificationEvent)
}

// noopNotifier is the default when WebSocket notifications aren't configured.
// It lets the SQS processor / upload service run unmodified in environments
// without a Hub (e.g. the unit-test harness).
type noopNotifier struct{}

func (noopNotifier) NotifyUser(string, NotificationEvent) {}

// NoopNotifier returns a Notifier that does nothing. Used when the Hub isn't
// wired (e.g. DB unavailable, or in unit tests).
func NoopNotifier() Notifier { return noopNotifier{} }

// Hub is the central registry of active WebSocket clients. It is safe for
// concurrent use because all state is owned by the Run goroutine; callers only
// touch the exported channel-based methods.
type Hub struct {
	log *slog.Logger

	register   chan register
	unregister chan unregister
	notify     chan notify
	done       chan struct{}

	mu   stdsync.RWMutex                   // protects clients only between Run and Shutdown
	clie map[string]map[*Client]struct{}   // user id -> set of connections
}

// NewHub constructs a Hub. It does not start processing; call Run (typically in
// its own goroutine) to begin handling register/unregister/notify messages.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:        log,
		register:   make(chan register),
		unregister: make(chan unregister),
		notify:     make(chan notify, 256), // buffered: bursts of events won't block senders
		done:       make(chan struct{}),
		clie:       map[string]map[*Client]struct{}{},
	}
}

// Run is the Hub's event loop. It must run in its own goroutine and is the
// single owner of the clients map. It exits when Shutdown is called.
func (h *Hub) Run() {
	for {
		select {
		case msg := <-h.register:
			h.handleRegister(msg.client)
		case msg := <-h.unregister:
			h.handleUnregister(msg.client)
		case msg := <-h.notify:
			h.handleNotify(msg)
		case <-h.done:
			return
		}
	}
}

func (h *Hub) handleRegister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clie[c.UserID] == nil {
		h.clie[c.UserID] = map[*Client]struct{}{}
	}
	h.clie[c.UserID][c] = struct{}{}
	h.log.Info("ws client registered", "user_id", c.UserID, "open_conns", len(h.clie[c.UserID]))
}

func (h *Hub) handleUnregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if conns, ok := h.clie[c.UserID]; ok {
		if _, exists := conns[c]; exists {
			delete(conns, c)
			close(c.Send)
			if len(conns) == 0 {
				delete(h.clie, c.UserID)
			}
			h.log.Info("ws client unregistered", "user_id", c.UserID, "open_conns", len(conns))
		}
	}
}

func (h *Hub) handleNotify(msg notify) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	payload, err := json.Marshal(msg.event)
	if err != nil {
		h.log.Error("failed to marshal notification event", "type", msg.event.Type, "err", err)
		return
	}

	delivered := 0
	for _, uid := range msg.userIDs {
		for c := range h.clie[uid] {
			select {
			case c.Send <- payload:
				delivered++
			default:
				// Client buffer full -> treat as dead. The write pump will close
				// the socket; we just stop pushing to it.
				h.log.Warn("ws client send buffer full, dropping", "user_id", uid)
			}
		}
	}
	h.log.Info("notification dispatched",
		"type", msg.event.Type, "targets", len(msg.userIDs), "delivered", delivered)
}

// Register, Unregister and the Notify methods are the public API callers use
// to talk to the run loop. They never touch the map directly.

// Register adds a client to the hub. Safe to call from any goroutine.
func (h *Hub) Register(c *Client) {
	h.register <- register{client: c}
}

// Unregister removes a client. Double-unregister is a no-op.
func (h *Hub) Unregister(c *Client) {
	select {
	case h.unregister <- unregister{client: c}:
	case <-h.done:
	}
}

// NotifyUser sends an event to every active connection for userID.
// Implements the Notifier interface.
func (h *Hub) NotifyUser(userID string, event NotificationEvent) {
	h.notify <- notify{userIDs: []string{userID}, event: event}
}

// Shutdown stops the run loop and sends a CloseGoingAway control frame to
// every connected client so browsers close their sockets cleanly.
func (h *Hub) Shutdown() {
	select {
	case <-h.done:
		return // already shut down
	default:
	}
	close(h.done)

	h.mu.Lock()
	defer h.mu.Unlock()
	for uid, conns := range h.clie {
		for c := range conns {
			// Guard against nil Conn (e.g. unit-test clients with no real socket).
			if c.Conn != nil {
				_ = c.Conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
				)
				_ = c.Conn.Close()
			}
		}
		delete(h.clie, uid)
	}
}
