package httpx

import (
	"context"
	"encoding/json"
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
	// wsAuthTimeout is the maximum duration an unauthenticated connection has
	// to transmit its {"type": "AUTH", "token": "..."} handshake frame.
	wsAuthTimeout = 5 * time.Second

	// Application-level WebSocket Close Codes (RFC 6455 4000-4999 range)
	WSCloseUnauthorized = 4401
	WSCloseAuthTimeout  = 4408
)

// wsAuthPayload defines the shape of the initial in-band authentication frame.
type wsAuthPayload struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

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

// HandleWSConnection upgrades an HTTP request to a WebSocket.
//
// Supports two authentication modes:
//  1. Legacy query parameter: ?token=<jwt> (fallback/backward-compatible).
//  2. In-band first-message handshake: Anonymous upgrade followed by an
//     {"type": "AUTH", "token": "<jwt>"} frame within 5 seconds.
//
// In-band authentication is preferred as it prevents token leakage into proxy/server
// access logs and yields deterministic RFC 4401 application close codes upon failure.
func (s *Server) HandleWSConnection(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "realtime layer unavailable",
		})
		return
	}

	// 1. Check if token was provided in query parameter (legacy / fallback)
	tokenStr := r.URL.Query().Get("token")
	if tokenStr != "" {
		claims, err := auth.ValidateToken(s.jwtSecret, tokenStr)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		if claims.SessionID != "" && s.sessions != nil {
			if _, err := s.sessions.GetSessionByID(r.Context(), claims.SessionID); err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session has been revoked or expired"})
				return
			}
		}

		conn, err := s.wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			s.log.Error("ws upgrade failed", "user_id", claims.UserID, "err", err)
			return
		}

		s.registerAndStartClient(conn, claims.UserID, claims.SessionID)
		return
	}

	// 2. In-Band First-Message Handshake
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("ws anonymous upgrade failed", "err", err)
		return
	}

	go s.handleInBandAuth(conn)
}

// handleInBandAuth reads the mandatory initial auth frame within wsAuthTimeout.
func (s *Server) handleInBandAuth(conn *websocket.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(wsAuthTimeout))

	_, msg, err := conn.ReadMessage()
	if err != nil {
		s.closeWSWithAuthError(conn, WSCloseAuthTimeout, "handshake timeout or connection closed")
		return
	}

	var authReq wsAuthPayload
	if err := json.Unmarshal(msg, &authReq); err != nil || authReq.Type != "AUTH" || authReq.Token == "" {
		s.closeWSWithAuthError(conn, WSCloseUnauthorized, "missing or invalid auth payload")
		return
	}

	claims, err := auth.ValidateToken(s.jwtSecret, authReq.Token)
	if err != nil {
		s.closeWSWithAuthError(conn, WSCloseUnauthorized, "invalid or expired token")
		return
	}

	if claims.SessionID != "" && s.sessions != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.sessions.GetSessionByID(ctx, claims.SessionID); err != nil {
			s.closeWSWithAuthError(conn, WSCloseUnauthorized, "session has been revoked or expired")
			return
		}
	}

	// Handshake successful: reset read deadline to normal ping/pong window
	_ = conn.SetReadDeadline(time.Time{})

	// Send confirmation AUTH_OK frame
	resp, _ := json.Marshal(map[string]any{
		"type":    "AUTH_OK",
		"user_id": claims.UserID,
	})
	if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
		_ = conn.Close()
		return
	}

	s.registerAndStartClient(conn, claims.UserID, claims.SessionID)
}

// closeWSWithAuthError sends an AUTH_ERROR payload, an RFC close control frame, and closes the connection.
func (s *Server) closeWSWithAuthError(conn *websocket.Conn, code int, reason string) {
	errPayload, _ := json.Marshal(map[string]any{
		"type":  "AUTH_ERROR",
		"error": reason,
	})
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = conn.WriteMessage(websocket.TextMessage, errPayload)
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
	_ = conn.Close()
}

// registerAndStartClient links the connection into the Hub and starts the pumps.
func (s *Server) registerAndStartClient(conn *websocket.Conn, userID, sessionID string) {
	client := &sync.Client{
		UserID:    userID,
		SessionID: sessionID,
		Conn:      conn,
		Send:      make(chan []byte, wsSendQueueDepth),
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
