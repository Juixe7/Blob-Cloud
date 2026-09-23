package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestShareInvitationEndpoints_Unauthenticated(t *testing.T) {
	srv := NewServer(nil, nil)

	r := chi.NewRouter()
	r.Route("/api/shares/invitations", func(r chi.Router) {
		r.Get("/", srv.HandleListInvitations)
		r.Post("/{id}/accept", srv.HandleAcceptInvitation)
		r.Post("/{id}/decline", srv.HandleDeclineInvitation)
		r.Post("/{id}/block", srv.HandleBlockSender)
		r.Get("/{id}/preview", srv.HandlePreviewInvitation)
	})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"ListInvitations", http.MethodGet, "/api/shares/invitations"},
		{"AcceptInvitation", http.MethodPost, "/api/shares/invitations/inv-123/accept"},
		{"DeclineInvitation", http.MethodPost, "/api/shares/invitations/inv-123/decline"},
		{"BlockSender", http.MethodPost, "/api/shares/invitations/inv-123/block"},
		{"PreviewInvitation", http.MethodGet, "/api/shares/invitations/inv-123/preview"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusUnauthorized {
				t.Fatalf("expected status 503 or 401, got %d", w.Code)
			}
		})
	}
}

func TestHandleShare_InvalidPayload(t *testing.T) {
	srv := NewServer(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/files/test-file/share", bytes.NewReader([]byte("{invalid json}")))
	w := httptest.NewRecorder()
	srv.HandleShare(w, req)

	// Since perms is nil, it safely returns 503
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", w.Code)
	}

	var res map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if res["error"] == "" {
		t.Fatalf("expected error message in response body")
	}
}
