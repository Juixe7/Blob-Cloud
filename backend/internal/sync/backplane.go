package sync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisChannel is the Pub/Sub channel name every node subscribes to and
// publishes on. A single channel is sufficient because events are already
// user-scoped inside the envelope.
const redisChannel = "blobcloud:ws:events"

// backplaneEnvelope wraps a NotificationEvent with the target user ID so that
// any node receiving the message can route it to the correct local connections.
// NodeID identifies the publishing node for echo suppression.
type backplaneEnvelope struct {
	NodeID string            `json:"node_id,omitempty"`
	UserID string            `json:"user_id"`
	Event  NotificationEvent `json:"event"`
}

// RedisBackplane implements Notifier by publishing events to a Redis Pub/Sub
// channel and delivering incoming messages from other nodes to the local Hub.
//
// Architecture (horizontal scale with echo suppression):
//
//	Node A                        Node B
//	────────────────────          ────────────────────
//	UploadService.NotifyUser()    (no local connections for this user)
//	  → backplane.NotifyUser()
//	       ├─ hub.NotifyUser()    ← delivered to tabs on this node instantly
//	       └─ Redis PUBLISH ────→ backplane subscriber goroutine
//	                                  └─ hub.NotifyUser() ← tabs on Node B
//
// The Hub itself is unchanged; it remains the single owner of local WebSocket
// connections. The backplane is purely an inter-node routing layer.
type RedisBackplane struct {
	nodeID string
	client *redis.Client
	hub    *Hub
	log    *slog.Logger
}

func generateNodeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// NewRedisBackplane creates a backplane that publishes to Redis and delivers
// remote events to hub. Call Run() in a goroutine to start the subscriber.
func NewRedisBackplane(client *redis.Client, hub *Hub, log *slog.Logger) *RedisBackplane {
	return &RedisBackplane{
		nodeID: generateNodeID(),
		client: client,
		hub:    hub,
		log:    log,
	}
}

// NodeID returns this backplane instance's unique cluster node identifier.
func (b *RedisBackplane) NodeID() string {
	return b.nodeID
}

// NotifyUser delivers event to all local connections for userID and publishes
// it to Redis so peer nodes can also deliver it to their local connections.
// Implements the Notifier interface — drop-in replacement for *Hub.
func (b *RedisBackplane) NotifyUser(userID string, event NotificationEvent) {
	// 1. Local delivery: serve tabs connected to this node immediately.
	b.hub.NotifyUser(userID, event)

	// 2. Remote delivery: publish so other nodes can serve their local tabs.
	env := backplaneEnvelope{NodeID: b.nodeID, UserID: userID, Event: event}
	payload, err := json.Marshal(env)
	if err != nil {
		b.log.Error("backplane: marshal envelope failed", "user_id", userID, "err", err)
		return
	}
	if err := b.client.Publish(context.Background(), redisChannel, payload).Err(); err != nil {
		// Non-fatal: the local delivery above already succeeded. Log and continue.
		b.log.Warn("backplane: redis publish failed (local delivery was successful)",
			"user_id", userID, "err", err)
	}
}

// Run subscribes to the Redis Pub/Sub channel and fans inbound messages out to
// the local Hub. It blocks until ctx is cancelled, making it suitable for a
// goroutine started from main.
//
// Messages published by THIS node are tagged with NodeID and dropped on arrival
// to prevent duplicate delivery to local sockets.
func (b *RedisBackplane) Run(ctx context.Context) error {
	sub := b.client.Subscribe(ctx, redisChannel)
	defer func() { _ = sub.Close() }()

	b.log.Info("backplane: subscribed to redis pub/sub channel", "channel", redisChannel, "node_id", b.nodeID)

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			b.log.Info("backplane: context cancelled, stopping subscriber")
			return nil
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("backplane: redis subscription channel closed")
			}
			b.dispatch(msg.Payload)
		}
	}
}

// dispatch decodes one Redis message and routes it to the local Hub if not from this node.
func (b *RedisBackplane) dispatch(payload string) {
	var env backplaneEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		b.log.Error("backplane: malformed envelope, skipping", "payload", payload, "err", err)
		return
	}

	// Echo suppression: If this message was published by THIS node, skip delivering to
	// local connections again since NotifyUser already delivered it locally on publication.
	if env.NodeID != "" && env.NodeID == b.nodeID {
		return
	}

	b.hub.NotifyUser(env.UserID, env.Event)
}

// Ping checks the Redis connection. Used by main.go to fail fast on bad config.
func Ping(ctx context.Context, client *redis.Client) error {
	return client.Ping(ctx).Err()
}

// NewRedisClient creates a Redis client from a redis:// URL.
// Example: redis://localhost:6379/0
func NewRedisClient(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return redis.NewClient(opts), nil
}
