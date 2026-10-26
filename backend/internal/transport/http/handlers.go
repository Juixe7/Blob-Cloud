// Package httpx contains the HTTP transport layer: chi router setup, middleware,
// and request handlers. Handlers depend on the domain.StorageProvider interface,
// never on a concrete storage driver.
package httpx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"go-drive-clone/internal/ai"
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

// JournalStore abstracts the delta-sync journal repository for HTTP handlers.
// *postgresrepo.JournalRepository satisfies this interface directly.
type JournalStore interface {
	GetLatestCursor(ctx context.Context, userID string) (int64, error)
	ListSince(ctx context.Context, userID string, sinceCursor int64, limit int) ([]*domain.JournalEntry, int64, bool, error)
}

// Server bundles handler dependencies. It is passed to route registration so
// every handler shares the same injected storage provider and logger.
type Server struct {
	db      *sql.DB
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
	files   *postgresrepo.FileRepository
	blocks  *postgresrepo.BlockRepository
	zipOps  *service.ZipService
	shares  *postgresrepo.ShareableLinkRepository
	// Phase 6: real-time layer. hub is nil when WS notifications aren't
	// configured; the WS handler then returns 503.
	hub            *sync.Hub
	notifier       sync.Notifier
	jwtSecret      string
	wsUpgrader     websocket.Upgrader
	mailer         *email.Mailer
	googleClientID string
	aiClient       ai.AIClient
	// Tier 2F: structured audit log. Falls back to audit.NoopLogger{} when
	// the DB is absent so handlers always have a non-nil logger to call.
	auditLog auditLogger
	// Phase 2: Delta Sync Engine journal store
	journal       JournalStore
	journalWriter *postgresrepo.JournalRepository
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
	if s.notifier == nil && hub != nil {
		s.notifier = hub
	}
	return s
}

// WithNotifier wires an external or cluster-aware Notifier (e.g. RedisBackplane)
// so events published from HTTP handlers route to peer nodes in a multi-instance deployment.
func (s *Server) WithNotifier(notifier sync.Notifier) *Server {
	s.notifier = notifier
	return s
}

// notifyUser routes a notification event through the cluster-aware notifier if present,
// or directly to the local hub.
func (s *Server) notifyUser(userID string, event sync.NotificationEvent) {
	if s.notifier != nil {
		s.notifier.NotifyUser(userID, event)
	} else if s.hub != nil {
		s.hub.NotifyUser(userID, event)
	}
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
func (s *Server) WithFileOperations(fileOps *service.FileService, files *postgresrepo.FileRepository, blocks *postgresrepo.BlockRepository) *Server {
	s.fileOps = fileOps
	s.files = files
	s.blocks = blocks
	return s
}

// WithZipOperations wires the zip service for on-the-fly streaming directories.
func (s *Server) WithZipOperations(zipOps *service.ZipService) *Server {
	s.zipOps = zipOps
	return s
}

// WithAI wires the HuggingFace client for semantic search functionality.
func (s *Server) WithAI(aiClient ai.AIClient) *Server {
	s.aiClient = aiClient
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

// WithShareableLinks wires the public links repository.
func (s *Server) WithShareableLinks(shares *postgresrepo.ShareableLinkRepository) *Server {
	s.shares = shares
	return s
}

// WithAudit wires the structured audit logger. If not called, the Server uses
// audit.NoopLogger{} which discards all events — safe for storage-only mode.
func (s *Server) WithAudit(l audit.Logger) *Server {
	s.auditLog = l
	return s
}

// WithJournal wires the delta sync journal repository.
func (s *Server) WithJournal(journal JournalStore) *Server {
	s.journal = journal
	if writer, ok := journal.(*postgresrepo.JournalRepository); ok {
		s.journalWriter = writer
	}
	return s
}

func (s *Server) WithDatabase(db *sql.DB) *Server {
	s.db = db
	return s
}

func (s *Server) recordMutationEvents(ctx context.Context, mutate func(postgresrepo.DBTX) error, entries []*domain.JournalEntry) error {
	if s.db == nil || s.journalWriter == nil {
		return errors.New("transactional journal is unavailable")
	}
	err := postgresrepo.RunInTx(ctx, s.db, func(tx postgresrepo.DBTX) error {
		if err := mutate(tx); err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		cursors, err := s.journalWriter.WithTx(tx).BatchRecord(ctx, entries)
		if err != nil {
			return fmt.Errorf("record mutation journal: %w", err)
		}
		for i, cursor := range cursors {
			entries[i].Cursor = cursor
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, entry := range entries {
		s.notifyUser(entry.UserID, sync.NotificationEvent{Type: sync.EventSyncDelta, Payload: map[string]any{
			"cursor": entry.Cursor, "file_id": entry.FileID, "action": entry.Action,
		}})
	}
	return nil
}

func (s *Server) requireSharePermissionInTx(ctx context.Context, tx postgresrepo.DBTX, fileID, userID, userEmail string) (*domain.File, error) {
	file, err := s.files.WithTx(tx).GetByID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if file.UserID != userID {
		allowed, err := s.perms.WithTx(tx).CheckUserPermission(ctx, fileID, userEmail, []string{domain.RoleOwner, domain.RoleEditor})
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, errors.New("share permission revoked")
		}
	}
	// The effective grant is locked above for collaborators; lock the file row
	// afterward so lifecycle changes cannot race the authorized mutation. Keep
	// this grant-then-file order consistent across ACL mutation handlers.
	return s.files.WithTx(tx).GetByIDForUpdate(ctx, fileID)
}

func isShortcutFile(file *domain.File) bool {
	if file == nil {
		return false
	}
	return (file.TargetID != nil && *file.TargetID != "") ||
		(file.ShortcutTargetID != nil && *file.ShortcutTargetID != "")
}

func (s *Server) journalEntriesForFile(ctx context.Context, file *domain.File, action string, actorID string) ([]*domain.JournalEntry, error) {
	ids := map[string]struct{}{}
	if file != nil && file.UserID != "" {
		ids[file.UserID] = struct{}{}
	}
	if actorID != "" {
		ids[actorID] = struct{}{}
	}
	if s.perms != nil && file != nil {
		collaborators, err := s.perms.FindCollaboratorUserIDs(ctx, file.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve journal recipients: %w", err)
		}
		for _, id := range collaborators {
			if id != "" {
				ids[id] = struct{}{}
			}
		}
	}
	entries := make([]*domain.JournalEntry, 0, len(ids))
	for id := range ids {
		entries = append(entries, shareJournalEntries(file, action, id)...)
	}
	return entries, nil
}

// HandleHealth serves a 200 OK text response. Standard endpoint for ECS ALBss check.
func (s *Server) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandlePutStagingBlock handles PUT /local-storage/staging/{session_id}/{index}.
// Stores unverified upload chunks in the staging area until cryptographic verification.
func (s *Server) HandlePutStagingBlock(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	indexText := chi.URLParam(r, "index")
	if sessionID == "" || indexText == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing staging parameters"})
		return
	}
	index, err := strconv.Atoi(indexText)
	if err != nil || index < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid staging index"})
		return
	}

	validator, ok := s.storage.(domain.StagingUploadURLValidator)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "local staging uploads are unavailable"})
		return
	}
	expiresAt, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	key := fmt.Sprintf("staging/%s/%d", sessionID, index)
	if err != nil || !validator.ValidateStagingUploadURL(key, expiresAt, r.URL.Query().Get("signature")) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or expired staging upload URL"})
		return
	}
	if s.uploads == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "upload service unavailable"})
		return
	}
	if err := s.uploads.StoreStagingBlock(r.Context(), sessionID, index, r.Body, r.Header.Get("Content-Type")); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrStagingStorage) {
			status = http.StatusInternalServerError
		} else if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "session is not active") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key":    key,
		"status": "staged",
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

// getAppBaseURL returns the configured base URL for user-facing links (invitations,
// password resets, public shares). It prioritises APP_BASE_URL, then BASE_URL,
// and defaults safely to the production domain https://blobcloud.dev.
func getAppBaseURL() string {
	if val := strings.TrimSpace(os.Getenv("APP_BASE_URL")); val != "" {
		return strings.TrimRight(val, "/")
	}
	if val := strings.TrimSpace(os.Getenv("BASE_URL")); val != "" {
		return strings.TrimRight(val, "/")
	}
	return "https://blobcloud.dev"
}
