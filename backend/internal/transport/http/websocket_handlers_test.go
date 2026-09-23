package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/sync"
)

func TestHandleWSConnection_InBandAuth(t *testing.T) {
	jwtSecret := "test-ws-secret-12345678901234567890"
	hub := sync.NewHub(slog.Default())
	go hub.Run()

	srv := NewServer(&testMemStorage{}, slog.Default()).WithRealtime(hub, jwtSecret, nil)

	s := httptest.NewServer(http.HandlerFunc(srv.HandleWSConnection))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")

	// 1. In-band Auth Success
	token, err := auth.CreateToken(jwtSecret, "user-123")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Send AUTH frame
	authReq := map[string]string{
		"type":  "AUTH",
		"token": token,
	}
	if err := conn.WriteJSON(authReq); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	// Expect AUTH_OK frame
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if resp["type"] != "AUTH_OK" {
		t.Fatalf("expected AUTH_OK, got %v", resp)
	}
	if resp["user_id"] != "user-123" {
		t.Fatalf("expected user_id user-123, got %v", resp["user_id"])
	}

	// 2. In-band Auth Failure (Invalid Token) -> expect AUTH_ERROR and code 4401
	connBad, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial bad: %v", err)
	}
	defer connBad.Close()

	if err := connBad.WriteJSON(map[string]string{
		"type":  "AUTH",
		"token": "invalid.jwt.token",
	}); err != nil {
		t.Fatalf("WriteJSON bad: %v", err)
	}

	// Expect AUTH_ERROR message
	var errResp map[string]any
	_ = connBad.ReadJSON(&errResp)
	if errResp["type"] != "AUTH_ERROR" {
		t.Fatalf("expected AUTH_ERROR, got %v", errResp)
	}

	// Expect close with 4401
	_, _, err = connBad.ReadMessage()
	if !websocket.IsCloseError(err, WSCloseUnauthorized) {
		t.Fatalf("expected close error 4401, got %v", err)
	}

	// 3. Legacy Query Param Auth Success
	connLegacy, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+token, nil)
	if err != nil {
		t.Fatalf("Dial legacy: %v", err)
	}
	defer connLegacy.Close()
	time.Sleep(50 * time.Millisecond)
}
