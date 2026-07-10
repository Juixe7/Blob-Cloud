// Package httpx contains the HTTP transport layer: chi router setup, middleware,
// and request handlers. Handlers depend on the domain.StorageProvider interface,
// never on a concrete storage driver.
package httpx

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
)

// Server bundles handler dependencies. It is passed to route registration so
// every handler shares the same injected storage provider and logger.
type Server struct {
	storage domain.StorageProvider
	log     *slog.Logger
	// Phase 3: orchestration + sharing. These are nil when the DB is
	// unavailable (dev, storage-only mode); the upload/share handlers then
	// return 503 Service Unavailable.
	uploads *service.UploadService
	perms   *postgresrepo.PermissionRepository
}

// NewServer constructs a Server with its dependencies injected.
func NewServer(storage domain.StorageProvider, log *slog.Logger) *Server {
	return &Server{storage: storage, log: log}
}

// WithUploads returns a copy of the Server wired with the upload service and
// permission repository. main.go calls this once the DB is ready.
func (s *Server) WithUploads(uploads *service.UploadService, perms *postgresrepo.PermissionRepository) *Server {
	return &Server{
		storage: s.storage,
		log:     s.log,
		uploads: uploads,
		perms:   perms,
	}
}

// HandleHealth responds with a simple JSON readiness check.
func (s *Server) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandlePutBlock simulates an S3 direct PUT upload. The client streams the raw
// block bytes in the request body; we persist them via the storage provider
// under the "blocks/<hash>" key derived from the route parameter.
func (s *Server) HandlePutBlock(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if hash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing block hash"})
		return
	}

	key := "blocks/" + hash
	// r.Body content type/length are honoured by the local driver; for S3 we
	// would pass them through as object metadata.
	contentType := r.Header.Get("Content-Type")
	if err := s.storage.PutObject(r.Context(), key, r.Body, r.ContentLength, contentType); err != nil {
		// A missing/invalid path (e.g. traversal) surfaces as a path error.
		if errors.Is(err, fs.ErrInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid block hash"})
			return
		}
		s.log.Error("put block failed", "hash", hash, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hash":   hash,
		"status": "stored",
	})
}

// writeJSON serializes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Nothing useful to do if encoding the error itself fails.
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
