package httpx

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/service"
)

// requireUploads is a small guard that short-circuits handlers when the upload
// service isn't wired (DB unavailable in dev/storage-only mode).
func (s *Server) requireUploads(w http.ResponseWriter) bool {
	if s.uploads == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "upload service unavailable (database not configured)",
		})
		return false
	}
	return true
}

// HandleInitiateUpload implements POST /api/upload/initiate.
//
// The client declares a file's chunks (by sha256) up front; we return a session
// id and, for each chunk, whether it already exists (dedup hit) plus an upload
// URL when the client still needs to PUT it.
func (s *Server) HandleInitiateUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireUploads(w) {
		return
	}
	var req service.InitiateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	resp, err := s.uploads.Initiate(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// HandleGetSession implements GET /api/upload/session/{id}.
//
// Returns the session state with fresh upload URLs for any chunks the client
// still needs to upload. A COMPLETED/ABORTED session returns its terminal state
// with no URLs.
func (s *Server) HandleGetSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireUploads(w) {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	resp, err := s.uploads.GetSession(r.Context(), id)
	if err != nil {
		// Missing session rows surface as sql.ErrNoRows -> 404.
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no rows") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleCompleteUpload implements POST /api/upload/complete.
//
// Runs the full finalisation flow (verify blocks present, upsert blocks, create
// file, link blocks, grant OWNER, mark COMPLETED) in a single transaction.
func (s *Server) HandleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireUploads(w) {
		return
	}
	var req service.CompleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	resp, err := s.uploads.Complete(r.Context(), req)
	if err != nil {
		// Missing blocks / wrong session state -> 400; anything else -> 500.
		code := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "not in storage") ||
			strings.Contains(err.Error(), "cannot complete") {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// decodeJSON decodes a JSON request body into dst using a strict decoder.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
