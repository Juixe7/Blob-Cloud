package httpx

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/sync"
)

// wsReadBufferSize / wsWriteBufferSize tune the gorilla buffers. 1 KiB is ample
// for JSON notification events and keeps per-connection memory low.
const (
	wsReadBufferSize  = 1024
	wsWriteBufferSize = 1024
	// wsSendQueueDepth is the per-client outbound buffer. A slow client fills
	// this before the Hub drops it as unresponsive.
	wsSendQueueDepth = 64
	// wsPingInterval governs the keepalive ping sent by the write pump. Must be
	// less than the gorilla default pong deadline.
	wsPingInterval = 30 * time.Second
)

// newUpgrader builds a websocket.Upgrader whose CheckOrigin accepts the
// configured CORS origins. A "*" entry (or empty list) allows all origins,
// which is the development default.
func newUpgrader(allowedOrigins []string) websocket.Upgrader {
	allowAll := len(allowedOrigins) == 0
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = struct{}{}
	}
	return websocket.Upgrader{
		ReadBufferSize:  wsReadBufferSize,
		WriteBufferSize: wsWriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			if allowAll {
				return true
			}
			_, ok := allowed[r.Header.Get("Origin")]
			return ok
		},
	}
}

// HandleWSConnection upgrades an HTTP request to a WebSocket and wires the
// connection into the Hub under the authenticated user's id.
//
// Authentication is via a JWT passed as the "token" query parameter, because
// browsers cannot attach custom headers to the WebSocket handshake. A missing
// or invalid token yields 401 and the connection is refused.
func (s *Server) HandleWSConnection(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "realtime layer unavailable",
		})
		return
	}

	// 1. Authenticate via ?token=<jwt>.
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
		return
	}
	claims, err := auth.ValidateToken(s.jwtSecret, tokenStr)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}

	// 2. Upgrade to WebSocket.
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response; nothing more to do.
		s.log.Error("ws upgrade failed", "user_id", claims.UserID, "err", err)
		return
	}

	// 3. Register the client with the Hub and start the read/write pumps.
	client := &sync.Client{
		UserID: claims.UserID,
		Conn:   conn,
		Send:   make(chan []byte, wsSendQueueDepth),
	}
	s.hub.Register(client)

	go s.wsWritePump(client)
	go s.wsReadPump(client)
}

// wsReadPump drains incoming frames for a connection. Clients normally send
// nothing meaningful (this server pushes notifications), but we must read to
// process ping/pong and to detect a dropped connection. When the read loop
// ends (close, error, or disconnect), the client is unregistered.
func (s *Server) wsReadPump(c *sync.Client) {
	defer func() {
		s.hub.Unregister(c)
		_ = c.Conn.Close()
	}()
	for {
		// We don't care about the message contents; we read purely to keep the
		// connection alive and detect closure. Set a generous read deadline that
		// the pong handler resets.
		_ = c.Conn.SetReadDeadline(time.Now().Add(wsPingInterval * 3))
		c.Conn.SetPongHandler(func(string) error {
			_ = c.Conn.SetReadDeadline(time.Now().Add(wsPingInterval * 3))
			return nil
		})

		if _, _, err := c.Conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Info("ws read loop ended", "user_id", c.UserID, "err", err)
			}
			return
		}
	}
}

// wsWritePump forwards events from the Hub to the WebSocket. It also sends a
// periodic ping to keep proxies/load balancers from idle-closing the socket.
// When the Send channel closes (Hub unregistered the client), it shuts down.
func (s *Server) wsWritePump(c *sync.Client) {
	ticker := time.NewTicker(wsPingInterval)
	defer func() {
		ticker.Stop()
		_ = c.Conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Channel closed by Hub (unregister). Send a close frame.
				_ = c.Conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, ""),
				)
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
