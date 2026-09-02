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
	"github.com/gorilla/websocket"

	"go-drive-clone/internal/audit"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/email"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
	"go-drive-clone/internal/sync"
)

// auditLogger is a local alias so handlers import only the httpx package, not
// the audit package directly (keeps the dependency graph clean).
type auditLogger = audit.Logger

// Server bundles handler dependencies. It is passed to route registration so
// every handler shares the same injected storage provider and logger.
type Server struct {
	storage domain.StorageProvider
	log     *slog.Logger
	// Phase 3: orchestration + sharing. These are nil when the DB is
	// unavailable (dev, storage-only mode); the upload/share handlers then
	// return 503 Service Unavailable.
	uploads  *service.UploadService
	perms    *postgresrepo.PermissionRepository
	users    *postgresrepo.UserRepository
	sessions *postgresrepo.SessionRepository
	// Phase 7.4: file operations (rename, move, delete, download). nil when
	// the DB is unavailable.
	fileOps *service.FileService
	zipOps  *service.ZipService
	// Phase 6: real-time layer. hub is nil when WS notifications aren't
	// configured; the WS handler then returns 503.
	hub            *sync.Hub
	jwtSecret      string
	wsUpgrader     websocket.Upgrader
	mailer         *email.Mailer
	googleClientID string
	// Tier 2F: structured audit log. Falls back to audit.NoopLogger{} when
	// the DB is absent so handlers always have a non-nil logger to call.
	auditLog auditLogger
}

// NewServer constructs a Server with its dependencies injected.
func NewServer(storage domain.StorageProvider, log *slog.Logger) *Server {
	return &Server{
		storage:  storage,
		log:      log,
		auditLog: audit.NoopLogger{},
	}
}

// WithUploads wires the upload service and permission repository. main.go calls
// this once the DB is ready.
func (s *Server) WithUploads(uploads *service.UploadService, perms *postgresrepo.PermissionRepository) *Server {
	s.uploads = uploads
	s.perms = perms
	return s
}

// WithRealtime wires the WebSocket hub and JWT secret used to authenticate WS
// connections. wsCORSOrigins controls the Upgrader's CheckOrigin.
func (s *Server) WithRealtime(hub *sync.Hub, jwtSecret string, wsCORSOrigins []string) *Server {
	s.hub = hub
	s.jwtSecret = jwtSecret
	s.wsUpgrader = newUpgrader(wsCORSOrigins)
	return s
}

// WithUsers wires the user repository, needed by the share handler to resolve
// grantee emails to user ids for real-time FILE_SHARED notifications.
func (s *Server) WithUsers(users *postgresrepo.UserRepository) *Server {
	s.users = users
	return s
}

// WithSessions wires the session repository for active login sessions tracking.
func (s *Server) WithSessions(sessions *postgresrepo.SessionRepository) *Server {
	s.sessions = sessions
	return s
}

// WithFileOperations wires the file operations service for rename, move,
// delete, download, and directory listing endpoints.
func (s *Server) WithFileOperations(fileOps *service.FileService) *Server {
	s.fileOps = fileOps
	return s
}

// WithZipOperations wires the zip service for on-the-fly streaming directories.
func (s *Server) WithZipOperations(zipOps *service.ZipService) *Server {
	s.zipOps = zipOps
	return s
}

// WithMailer wires the SMTP email mailer.
func (s *Server) WithMailer(mailer *email.Mailer) *Server {
	s.mailer = mailer
	return s
}

// WithGoogleOAuth wires the Google OAuth Client ID.
func (s *Server) WithGoogleOAuth(googleClientID string) *Server {
	s.googleClientID = googleClientID
	return s
}

// WithAudit wires the structured audit logger. If not called, the Server uses
// audit.NoopLogger{} which discards all events — safe for storage-only mode.
func (s *Server) WithAudit(l audit.Logger) *Server {
	s.auditLog = l
	return s
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
