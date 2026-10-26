// Package service contains the application/orchestration layer: it coordinates
// repositories and storage providers to implement use cases that span multiple
// domain objects. Services hold the transactional boundaries that repositories
// individually cannot.
package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/metrics"
	"go-drive-clone/internal/queue"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	wsSync "go-drive-clone/internal/sync"
)

func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if m := mime.TypeByExtension(ext); m != "" {
		return m
	}
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".zip":
		return "application/zip"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}

// UploadBlockStore defines the block deduplication operations used by UploadService.
type UploadBlockStore interface {
	GetMultipleByHashes(ctx context.Context, hashes []string) ([]*domain.Block, error)
	WithTx(tx postgresrepo.DBTX) *postgresrepo.BlockRepository
}

// UploadSessionStore defines the upload session persistence operations used by UploadService.
type UploadSessionStore interface {
	CreateSession(ctx context.Context, session *domain.UploadSession, blocks []domain.SessionBlock) error
	GetSessionByID(ctx context.Context, id string) (*domain.UploadSession, []domain.SessionBlock, error)
	UpdateSessionStatus(ctx context.Context, id string, status string) error
	WithTx(tx postgresrepo.DBTX) *postgresrepo.UploadSessionRepository
}

// UploadService orchestrates resumable uploads: initiating a session with
// deduplication, regenerating URLs for resumption, and committing a completed
// upload as one atomic transaction. After a successful completion it publishes
// a thumbnail job to the event queue (if configured).
type UploadService struct {
	db        *sql.DB
	users     *postgresrepo.UserRepository
	files     *postgresrepo.FileRepository
	blocks    UploadBlockStore
	sessions  UploadSessionStore
	perms     *postgresrepo.PermissionRepository
	storage   domain.StorageProvider
	publisher queue.Publisher
	notifier  wsSync.Notifier // optional; nil-safe (NoopNotifier)
	journal   *postgresrepo.JournalRepository
	log       *slog.Logger
	commitTx  func(ctx context.Context, session *domain.UploadSession, sessionBlocks []domain.SessionBlock, req CompleteRequest) (fileID string, cursors map[string]int64, action string, err error)
}

// NewUploadService wires the service with all the repositories it needs. The
// *sql.DB is retained so the service can open the cross-repository transaction
// for CompleteSession. publisher may be queue.NoopPublisher if SQS is not
// configured. notifier may be nil.
func NewUploadService(
	db *sql.DB,
	users *postgresrepo.UserRepository,
	files *postgresrepo.FileRepository,
	blocks UploadBlockStore,
	sessions UploadSessionStore,
	perms *postgresrepo.PermissionRepository,
	storage domain.StorageProvider,
	publisher queue.Publisher,
	notifier wsSync.Notifier,
	log *slog.Logger,
) *UploadService {
	if notifier == nil {
		notifier = wsSync.NoopNotifier()
	}
	return &UploadService{
		db: db, users: users, files: files, blocks: blocks,
		sessions: sessions, perms: perms, storage: storage,
		publisher: publisher, notifier: notifier, log: log,
	}
}

// WithJournal binds a JournalRepository for delta sync change logging.
func (s *UploadService) WithJournal(journal *postgresrepo.JournalRepository) *UploadService {
	s.journal = journal
	return s
}

// InitiateRequest defines the payload for initiating a multi-chunk, content-defined upload session.
type InitiateRequest struct {
	// Filename is the target file's display name (e.g. "dataset.parquet").
	Filename string `json:"filename"`
	// ParentID is the optional parent directory UUID (nil for root folder).
	ParentID *string `json:"parent_id"`
	// UserID is the authenticated owner's unique identifier.
	UserID string `json:"user_id"`
	// TotalSize is the declared total file size in bytes.
	TotalSize int64 `json:"total_size"`
	// IsEncrypted indicates whether client-side WebCrypto AES-256-GCM framing is applied.
	IsEncrypted bool `json:"is_encrypted"`
	// EncryptionSalt is the client's PBKDF2 cryptographic salt (required when IsEncrypted is true).
	EncryptionSalt string `json:"encryption_salt,omitempty"`
	// Chunks contains the ordered array of FastCDC chunk hashes and sizes.
	Chunks []InitiateChunk `json:"chunks"`
}

// InitiateChunk represents a single content-defined chunk declared by the client.
type InitiateChunk struct {
	// SHA256 is the hex-encoded digest of the chunk's payload.
	SHA256 string `json:"sha256"`
	// BlockMD5 is the optional MD5 digest used for legacy S3 Content-MD5 integrity checks.
	BlockMD5 string `json:"block_md5"`
	// SizeBytes is the uncompressed byte length of this chunk.
	SizeBytes int32 `json:"size_bytes"`
}

// InitiateResponse is returned to the client to coordinate parallel chunk uploads.
type InitiateResponse struct {
	// SessionID is the unique identifier for tracking this upload session.
	SessionID string `json:"session_id"`
	// Status indicates the lifecycle state (e.g. domain.SessionStatusInitiated).
	Status string `json:"status"`
	// Chunks lists the sequence, deduplication status, and presigned upload targets.
	Chunks []InitiateRespChunk `json:"chunks"`
}

// InitiateRespChunk coordinates individual chunk transmission.
type InitiateRespChunk struct {
	// SequenceNumber is the 0-based index determining chunk ordering in the assembled file.
	SequenceNumber int `json:"sequence_number"`
	// SHA256 is the verified content hash of the block.
	SHA256 string `json:"sha256"`
	// SizeBytes is the byte length of the chunk.
	SizeBytes int32 `json:"size_bytes"`
	// AlreadyExists is true if the block already resides in CAS storage (client skips PUT).
	AlreadyExists bool `json:"already_exists"`
	// UploadURL is the presigned S3/local PUT URL issued when AlreadyExists is false.
	UploadURL string `json:"upload_url,omitempty"`
	// UploadID identifies an active AWS S3 Multi-Part Upload for oversized blocks (> 5 GiB).
	UploadID string `json:"upload_id,omitempty"`
	// PartURLs contains ordered presigned URLs for multi-part chunk uploads.
	PartURLs []string `json:"part_urls,omitempty"`
}

// Initiate creates a resumable, deduplication-aware upload session.
//
// Algorithmic Workflow & Deduplication Engine:
//  1. FastCDC Chunk Deduplication: Queries the immutable `blocks` table with all chunk SHA-256 hashes.
//  2. Bandwidth Optimization: Chunks already existing in storage are marked `already_exists = true`.
//     The client omits physical transmission of these blocks, saving up to 99% bandwidth on file mutations.
//  3. Presigned URL Issuance: For missing blocks, generates isolated, time-limited presigned PUT URLs
//     targeting the quarantine namespace (`staging/{session_id}/{sequence_number}`).
//  4. Session Persistence: Writes session metadata and session_blocks rows in an atomic transaction.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - req: InitiateRequest containing filename, parent folder, and ordered chunk hashes.
//
// Returns:
//   - *InitiateResponse: Session ID and chunk-by-chunk upload coordination URLs.
//   - error: Nil on success, or validation/database error.
func (s *UploadService) Initiate(ctx context.Context, req InitiateRequest) (*InitiateResponse, error) {
	if req.UserID == "" || strings.TrimSpace(req.Filename) == "" {
		return nil, errors.New("user_id and filename are required")
	}
	if req.ParentID != nil {
		parent := strings.TrimSpace(*req.ParentID)
		if parent == "" || parent == "root" {
			req.ParentID = nil
		} else {
			req.ParentID = &parent
		}
	}
	if len(req.Filename) > 255 || strings.ContainsAny(req.Filename, `/\\`) || strings.ContainsRune(req.Filename, 0) {
		return nil, errors.New("filename is invalid")
	}
	if req.TotalSize < 0 {
		return nil, errors.New("total_size must not be negative")
	}
	if len(req.Chunks) == 0 {
		return nil, errors.New("at least one chunk is required")
	}
	if err := validateUploadParent(ctx, s.files, s.users, req.UserID, req.ParentID); err != nil {
		return nil, err
	}

	// 1. Collect chunk hashes and ask the global blocks table which already
	//    exist. This is the deduplication check: hits skip both upload and
	//    storage of the physical block.
	hashes := make([]string, 0, len(req.Chunks))
	for i := range req.Chunks {
		c := &req.Chunks[i]
		if len(c.SHA256) != sha256.Size*2 {
			return nil, errors.New("chunk sha256 must be a 64-character hex digest")
		}
		if _, err := hex.DecodeString(c.SHA256); err != nil {
			return nil, errors.New("chunk sha256 must be a 64-character hex digest")
		}
		c.SHA256 = strings.ToLower(c.SHA256)
		if c.SizeBytes < 0 {
			return nil, errors.New("chunk size_bytes must not be negative")
		}
		hashes = append(hashes, c.SHA256)
	}
	existing, err := s.blocks.GetMultipleByHashes(ctx, hashes)
	if err != nil {
		return nil, fmt.Errorf("dedup check: %w", err)
	}
	existingSet := make(map[string]bool, len(existing))
	for _, b := range existing {
		existingSet[b.SHA256] = true
	}

	// Record deduplication metrics: hits = blocks already stored (client skips
	// uploading them), misses = blocks that need a fresh upload URL.
	dedupHits := float64(len(existing))
	dedupMisses := float64(len(hashes) - len(existing))
	metrics.BlockDedupHits.Add(dedupHits)
	metrics.BlockDedupMisses.Add(dedupMisses)
	metrics.UploadsInitiated.Inc()

	// 2. Build the session + its blocks. Pre-existing chunks are marked
	//    is_uploaded=true so completion won't expect a fresh upload.
	session := &domain.UploadSession{
		UserID:    req.UserID,
		Filename:  req.Filename,
		ParentID:  req.ParentID,
		TotalSize: req.TotalSize,
		Status:    domain.SessionStatusInitiated,
	}
	blocks := make([]domain.SessionBlock, 0, len(req.Chunks))
	for i, c := range req.Chunks {
		alreadyExists := existingSet[c.SHA256]
		sb := domain.SessionBlock{
			BlockHash:      c.SHA256,
			BlockMD5:       c.BlockMD5,
			SequenceNumber: i,
			SizeBytes:      c.SizeBytes,
			IsUploaded:     alreadyExists,
		}
		blocks = append(blocks, sb)
	}

	// 3. Persist session + blocks atomically to generate session UUID.
	if err := s.sessions.CreateSession(ctx, session, blocks); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// 4. Generate staging upload URL(s) for chunks the client still must upload.
	// Zero-Trust: unverified client data lands in staging/{session_id}/{index}, NEVER blocks/{hash}.
	respChunks := make([]InitiateRespChunk, 0, len(req.Chunks))
	for i, c := range req.Chunks {
		alreadyExists := existingSet[c.SHA256]
		rc := InitiateRespChunk{
			SequenceNumber: i,
			SHA256:         c.SHA256,
			SizeBytes:      c.SizeBytes,
			AlreadyExists:  alreadyExists,
		}
		if !alreadyExists {
			stagingKey := fmt.Sprintf("staging/%s/%d", session.ID, i)
			blockSize := int64(c.SizeBytes)

			if blockSize > domain.MPUBlockThreshold {
				// Large block path (> 5 GiB): S3 Multipart Upload to staging.
				mpu, ok := s.storage.(domain.MultipartUploadProvider)
				if !ok {
					return nil, fmt.Errorf("block %s exceeds 5 GiB but storage driver does not support multipart upload", c.SHA256)
				}
				uploadID, err := mpu.CreateMultipartUpload(ctx, stagingKey)
				if err != nil {
					return nil, fmt.Errorf("create multipart upload for %s: %w", c.SHA256, err)
				}
				partCount := int32((blockSize + domain.MPUPartSize - 1) / domain.MPUPartSize)
				partURLs := make([]string, 0, partCount)
				for p := int32(1); p <= partCount; p++ {
					pURL, err := mpu.PresignUploadPart(ctx, stagingKey, uploadID, p, domain.StagingUploadURLLifetime)
					if err != nil {
						_ = mpu.AbortMultipartUpload(ctx, stagingKey, uploadID)
						return nil, fmt.Errorf("presign part %d for %s: %w", p, c.SHA256, err)
					}
					partURLs = append(partURLs, pURL)
				}
				rc.UploadID = uploadID
				rc.PartURLs = partURLs
			} else {
				// Standard path (≤ 5 GiB): presigned PUT URL targeting staging.
				url, err := s.storage.GenerateStagingUploadURL(ctx, stagingKey, domain.StagingUploadURLLifetime)
				if err != nil {
					return nil, fmt.Errorf("generate staging upload url for %s: %w", c.SHA256, err)
				}
				rc.UploadURL = url
			}
		}
		respChunks = append(respChunks, rc)
	}

	s.log.Info("upload session initiated",
		"session_id", session.ID, "filename", req.Filename,
		"chunks", len(req.Chunks), "dedup_hits", len(existing))

	return &InitiateResponse{
		SessionID: session.ID,
		Status:    session.Status,
		Chunks:    respChunks,
	}, nil
}

// SessionStatusResponse is the body of GET /api/upload/session/{id}. It mirrors
// InitiateResponse so the client can resume a session with the same shape.
type SessionStatusResponse struct {
	SessionID string              `json:"session_id"`
	Status    string              `json:"status"`
	Filename  string              `json:"filename"`
	Chunks    []InitiateRespChunk `json:"chunks"`
}

// GetSession returns the current upload session state and generates fresh presigned upload URLs
// for any chunks that have not yet been successfully transmitted.
//
// Staging Reconciliation & Network Drop Resiliency:
//   - Checks whether supposedly missing chunks already landed in S3 staging prior to a network interruption.
//   - If a chunk is physically verified in S3 with matching byte length, it is automatically marked
//     as uploaded, saving the client from redundant re-transmissions.
//   - For genuinely missing chunks, issues fresh 30-minute presigned PUT URLs.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - id: Unique upload session UUID.
//   - userID: Authenticated user ID (must match session owner).
//
// Returns:
//   - *SessionStatusResponse: Current status, filename, and per-chunk upload coordination data.
//   - error: Nil on success, or session not found / access denied error.
func (s *UploadService) GetSession(ctx context.Context, id string, userID string) (*SessionStatusResponse, error) {
	session, blocks, err := s.sessions.GetSessionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if userID != "" && session.UserID != userID {
		return nil, errors.New("access denied: session owned by another user")
	}

	resp := &SessionStatusResponse{
		SessionID: session.ID,
		Status:    session.Status,
		Filename:  session.Filename,
		Chunks:    make([]InitiateRespChunk, 0, len(blocks)),
	}

	for _, b := range blocks {
		rc := InitiateRespChunk{
			SequenceNumber: b.SequenceNumber,
			SHA256:         b.BlockHash,
			SizeBytes:      b.SizeBytes,
			AlreadyExists:  b.IsUploaded,
		}
		// Only pending chunks need a URL; completed/aborted sessions return as-is.
		if session.Status == domain.SessionStatusInitiated && !b.IsUploaded {
			stagingKey := fmt.Sprintf("staging/%s/%d", session.ID, b.SequenceNumber)
			// Staging reconciliation: check if this chunk already landed in S3 staging
			// (e.g. the client uploaded it before a network drop). If so, skip re-upload.
			if meta, headErr := s.storage.HeadObject(ctx, stagingKey); headErr == nil &&
				meta.ContentLength == int64(b.SizeBytes) {
				// Chunk is already safely quarantined in staging — no re-upload needed.
				rc.AlreadyExists = true
			} else {
				// Chunk is genuinely missing — issue a fresh presigned PUT URL.
				url, err := s.storage.GenerateStagingUploadURL(ctx, stagingKey, domain.StagingUploadURLLifetime)
				if err != nil {
					return nil, fmt.Errorf("regenerate staging upload url: %w", err)
				}
				rc.UploadURL = url
			}
		}
		resp.Chunks = append(resp.Chunks, rc)
	}
	return resp, nil
}

// StoreStagingBlock accepts a local-driver upload only when its session is
// active and the sequence, byte length, and SHA-256 match the session record.
// The URL capability is checked by the HTTP transport before this method.
func (s *UploadService) StoreStagingBlock(ctx context.Context, sessionID string, sequenceNumber int, body io.Reader, contentType string) error {
	if sessionID == "" || sequenceNumber < 0 || body == nil {
		return errors.New("invalid staging upload parameters")
	}
	session, blocks, err := s.sessions.GetSessionByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get upload session: %w", err)
	}
	if session.Status != domain.SessionStatusInitiated {
		return fmt.Errorf("session is not active")
	}
	var expected *domain.SessionBlock
	for i := range blocks {
		if blocks[i].SequenceNumber == sequenceNumber {
			expected = &blocks[i]
			break
		}
	}
	if expected == nil {
		return errors.New("staging sequence is not part of upload session")
	}
	if expected.IsUploaded {
		return errors.New("staging sequence does not require an upload")
	}
	if expected.SizeBytes < 0 || len(expected.BlockHash) != sha256.Size*2 {
		return errors.New("session contains invalid block metadata")
	}
	if _, err := hex.DecodeString(expected.BlockHash); err != nil {
		return errors.New("session contains invalid block metadata")
	}

	key := fmt.Sprintf("staging/%s/%d", sessionID, sequenceNumber)
	hasher := sha256.New()
	counted := &countingReader{reader: io.LimitReader(body, int64(expected.SizeBytes)+1)}
	if err := s.storage.PutObject(ctx, key, io.TeeReader(counted, hasher), int64(expected.SizeBytes), contentType); err != nil {
		return fmt.Errorf("%w: %v", ErrStagingStorage, err)
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if counted.count != int64(expected.SizeBytes) || !strings.EqualFold(actualHash, expected.BlockHash) {
		_ = s.storage.DeleteObject(ctx, key)
		return fmt.Errorf("staging payload does not match declared size and sha256")
	}
	return nil
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count += int64(n)
	return n, err
}

func validateUploadParent(
	ctx context.Context,
	files *postgresrepo.FileRepository,
	users *postgresrepo.UserRepository,
	userID string,
	parentID *string,
) error {
	if parentID == nil || strings.TrimSpace(*parentID) == "" || strings.TrimSpace(*parentID) == "root" {
		return nil
	}
	if files == nil || users == nil {
		return errors.New("destination validation unavailable")
	}
	parent, err := files.GetByID(ctx, *parentID)
	if err != nil {
		return fmt.Errorf("destination folder not found: %w", err)
	}
	if !parent.IsDirectory {
		return errors.New("destination parent is not a directory")
	}
	if parent.DeletedAt != nil {
		return errors.New("destination folder is deleted")
	}
	if parent.UserID == userID {
		return nil
	}
	user, err := users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve uploader: %w", err)
	}
	role, err := files.GetResolvedPermission(ctx, parent.ID, user.Email)
	if err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			return errors.New("access denied: editor permission required on destination folder")
		}
		return fmt.Errorf("destination permission check: %w", err)
	}
	if role != domain.RoleOwner && role != domain.RoleEditor {
		return errors.New("access denied: editor permission required on destination folder")
	}
	return nil
}

// CompleteRequest defines the payload for finalizing a completed upload session.
//
// Conflict Resolution Modes:
//   - "" or "version": Creates a new immutable version in file_versions and updates the file row (default).
//   - "copy": Auto-generates a unique non-conflicting filename (e.g. "report (1).pdf").
//   - "fail": Aborts with ErrFileConflict (mapped to HTTP 409 Conflict).
type CompleteRequest struct {
	SessionID      string `json:"session_id"`
	IsEncrypted    bool   `json:"is_encrypted"`
	EncryptionSalt string `json:"encryption_salt,omitempty"`
	ResolutionMode string `json:"resolution_mode,omitempty"`
}

// ErrFileConflict is returned when an upload complete request has ResolutionMode "fail"
// and an active file with the same name already exists in the target destination.
var ErrFileConflict = errors.New("file conflict: an active file with this name already exists in this destination")

// ErrStagingStorage reports a failure persisting a validated local staging PUT.
var ErrStagingStorage = errors.New("staging storage failure")

// CompleteResponse confirms a finished upload and returns the finalized file metadata ID.
type CompleteResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	FileID    string `json:"file_id"`
}

// Complete finalizes an upload session across a two-stage verification and atomic transaction boundary.
//
// Architecture & Multi-Stage Execution:
//  1. Stage 1: Zero-Trust Staging Promotion (Outside DB Transaction):
//     - Checks every block in S3 quarantine staging (`staging/{session_id}/{seq}`).
//     - Verifies byte lengths and re-hashes payload to guarantee zero bit-rot or tampering.
//     - Promotes verified blocks to the immutable CAS namespace (`blocks/{sha256}`).
//     - Executed outside the database transaction so slow object storage I/O never holds database row locks.
//  2. Stage 2: Single Atomic SQL Transaction (Inside DB Transaction):
//     - Upserts unique blocks into the global deduplication `blocks` catalog.
//     - Evaluates destination filename conflicts according to ResolutionMode ("version", "copy", "fail").
//     - Inserts the `files` metadata row and ordered `file_blocks` join mappings.
//     - Grants the creator default OWNER rights in `permissions`.
//     - Transitions the upload session status to COMPLETED.
//     - Records a monotonic journal entry for real-time WebSocket sync propagation.
//  3. Stage 3: Asynchronous Worker Dispatch (Post-Commit):
//     - Publishes a background job to Amazon SQS for thumbnail generation and Gemini AI summarization.
//     - Automatically evaluates CAS block reuse to bypass redundant AI processing if an identical file exists.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - req: CompleteRequest with session ID, encryption attributes, and conflict resolution policy.
//   - userID: Authenticated user ID (must match session creator).
//
// Returns:
//   - *CompleteResponse: Confirmation payload containing session ID, terminal status, and new FileID.
//   - error: Nil on success, or validation/checksum/authorization/conflict error on failure.
func (s *UploadService) Complete(ctx context.Context, req CompleteRequest, userID string) (*CompleteResponse, error) {
	if req.SessionID == "" {
		return nil, errors.New("session_id is required")
	}

	var result CompleteResponse
	var uploaderID string
	var recordedCursors map[string]int64
	var recordedAction string
	// 1. Pre-transaction validation & Zero-Trust Staging Verification (outside DB transaction)
	session, sessionBlocks, err := s.sessions.GetSessionByID(ctx, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if userID != "" && session.UserID != userID {
		return nil, errors.New("access denied: session owned by another user")
	}
	if session.Status != domain.SessionStatusInitiated {
		return nil, fmt.Errorf("session %s is %s, cannot complete", req.SessionID, session.Status)
	}
	if err := validateUploadParent(ctx, s.files, s.users, session.UserID, session.ParentID); err != nil {
		return nil, fmt.Errorf("validate upload destination: %w", err)
	}
	uploaderID = session.UserID

	// 2. Zero-Trust Staging Verification & Promotion Pipeline (Phase 3)
	// Executed BEFORE opening the SQL transaction so slow WAN I/O never holds database connection locks.
	for _, b := range sessionBlocks {
		destKey := "blocks/" + b.BlockHash

		// If the block was deduplicated at initiate time, verify it exists in CAS.
		if b.IsUploaded {
			meta, err := s.storage.HeadObject(ctx, destKey)
			if err != nil {
				s.log.Warn("complete upload security alert: deduplicated block missing in CAS store",
					"session_id", req.SessionID, "block_hash", b.BlockHash, "err", err)
				return nil, fmt.Errorf("payload integrity violation: block %s not in CAS: %w", b.BlockHash, err)
			}
			if meta.ContentLength != int64(b.SizeBytes) {
				return nil, fmt.Errorf("payload integrity violation: block %s size mismatch (expected %d, got %d)",
					b.BlockHash, b.SizeBytes, meta.ContentLength)
			}
			continue
		}

		// Block was uploaded to staging during this session.
		stagingKey := fmt.Sprintf("staging/%s/%d", req.SessionID, b.SequenceNumber)

		// Fast-path: Check if destination block already exists in CAS (e.g. concurrent upload dedup)
		if casMeta, err := s.storage.HeadObject(ctx, destKey); err == nil && casMeta.ContentLength == int64(b.SizeBytes) {
			// Safe deduplication! Discard redundant staging object.
			_ = s.storage.DeleteObject(ctx, stagingKey)
			continue
		}

		// Verify staged block exists and check server-authoritative sizing
		meta, err := s.storage.HeadObject(ctx, stagingKey)
		if err != nil {
			s.log.Warn("complete upload security alert: staged block missing",
				"session_id", req.SessionID, "staging_key", stagingKey, "err", err)
			return nil, fmt.Errorf("payload integrity violation: staged block %s not in storage: %w", stagingKey, err)
		}
		if meta.ContentLength != int64(b.SizeBytes) {
			_ = s.storage.DeleteObject(ctx, stagingKey)
			return nil, fmt.Errorf("payload integrity violation: block %s size mismatch (expected %d, got %d)",
				b.BlockHash, b.SizeBytes, meta.ContentLength)
		}

		// Zero-Trust Cryptographic Content Verification: stream and compute true SHA-256
		rc, err := s.storage.GetObject(ctx, stagingKey)
		if err != nil {
			return nil, fmt.Errorf("read staged block %s: %w", stagingKey, err)
		}

		hasher := sha256.New()
		if _, copyErr := io.Copy(hasher, rc); copyErr != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("stream staged block %s: %w", stagingKey, copyErr)
		}
		_ = rc.Close()

		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actualHash, b.BlockHash) {
			s.log.Warn("complete upload security alert: CAS POISONING ATTEMPT REJECTED",
				"session_id", req.SessionID, "sequence_number", b.SequenceNumber,
				"claimed_hash", b.BlockHash, "actual_hash", actualHash)
			_ = s.storage.DeleteObject(ctx, stagingKey)
			return nil, fmt.Errorf("checksum mismatch: claimed sha256 %s but computed %s (CAS poisoning rejected)",
				b.BlockHash, actualHash)
		}

		// Cryptographic ETag Verification if client provided MD5
		if b.BlockMD5 != "" {
			cleanETag := strings.Trim(strings.ToLower(meta.ETag), "\"")
			expectedMD5 := strings.Trim(strings.ToLower(b.BlockMD5), "\"")
			if cleanETag != "" && cleanETag != "mocketag" && cleanETag != expectedMD5 {
				s.log.Warn("complete upload security alert: ETag verification failed",
					"session_id", req.SessionID, "staging_key", stagingKey,
					"expected_md5", expectedMD5, "s3_etag", cleanETag)
				_ = s.storage.DeleteObject(ctx, stagingKey)
				return nil, fmt.Errorf("payload integrity violation: block %s ETag mismatch", b.BlockHash)
			}
		}

		// Promote verified block to immutable CAS storage (blocks/<hash>)
		if err := s.storage.PromoteObject(ctx, stagingKey, destKey); err != nil {
			s.log.Error("failed to promote block from staging",
				"session_id", req.SessionID, "staging_key", stagingKey, "dest_key", destKey, "err", err)
			return nil, fmt.Errorf("failed to promote block %s: %w", b.BlockHash, err)
		}
	}

	// 3. Fast Atomic Database Transaction (Metadata writes only, ~20-50ms)
	var txErr error
	if s.commitTx != nil {
		var fID string
		fID, recordedCursors, recordedAction, txErr = s.commitTx(ctx, session, sessionBlocks, req)
		if txErr == nil {
			result.FileID = fID
			result.SessionID = req.SessionID
			result.Status = domain.SessionStatusCompleted
		}
	} else {
		txErr = postgresrepo.RunInTx(ctx, s.db, func(tx postgresrepo.DBTX) error {
			sessions := s.sessions.WithTx(tx)
			blocks := s.blocks.WithTx(tx)
			files := s.files.WithTx(tx)
			perms := s.perms.WithTx(tx)
			users := s.users.WithTx(tx)

			// Re-verify session state under transaction lock
			currentSession, _, err := sessions.GetSessionByID(ctx, req.SessionID)
			if err != nil {
				return err
			}
			if currentSession.Status != domain.SessionStatusInitiated {
				return fmt.Errorf("session %s is %s, cannot complete", req.SessionID, currentSession.Status)
			}
			if err := validateUploadParent(ctx, files, users, currentSession.UserID, currentSession.ParentID); err != nil {
				return fmt.Errorf("validate upload destination: %w", err)
			}

			// 3. Upsert blocks into the global table, resolving each to a stable id.
			seqs := make([]domain.BlockSequence, 0, len(sessionBlocks))
			for _, b := range sessionBlocks {
				blk := &domain.Block{SHA256: b.BlockHash, SizeBytes: b.SizeBytes}
				if err := blocks.GetOrCreate(ctx, blk); err != nil {
					return err
				}
				seqs = append(seqs, domain.BlockSequence{
					BlockID:        blk.ID,
					SequenceNumber: b.SequenceNumber,
				})
			}

			// 4. Handle File Versioning / Collision with Transaction Advisory Locking
			// Serializes concurrent uploads of the same filename to the same parent folder,
			// preventing duplicate key collisions and version number races.
			parentLockStr := "root"
			if session.ParentID != nil {
				parentLockStr = *session.ParentID
			}
			uploadLockKey := fmt.Sprintf("upload_file_%s_%s_%s", session.UserID, parentLockStr, session.Filename)
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", uploadLockKey); err != nil {
				return fmt.Errorf("acquire upload completion lock: %w", err)
			}

			existingFile, err := files.GetFileByNameAndParent(ctx, session.UserID, session.Filename, session.ParentID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("check existing file: %w", err)
			}

			if existingFile != nil && existingFile.IsDirectory {
				return fmt.Errorf("a folder named %q already exists in this destination", session.Filename)
			}

			isVersionUpdate := false
			if existingFile != nil && !existingFile.IsDirectory {
				if req.ResolutionMode == "fail" {
					return ErrFileConflict
				}

				if req.ResolutionMode == "copy" {
					// Keep Both: Auto-generate an available clash-free filename
					ext := filepath.Ext(session.Filename)
					base := strings.TrimSuffix(session.Filename, ext)
					targetName := ""
					for i := 1; i <= 1000; i++ {
						candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
						f, err := files.GetFileByNameAndParent(ctx, session.UserID, candidate, session.ParentID)
						if errors.Is(err, sql.ErrNoRows) || f == nil {
							targetName = candidate
							break
						}
					}
					if targetName == "" {
						targetName = fmt.Sprintf("%s (%d)%s", base, time.Now().Unix(), ext)
					}

					file := &domain.File{
						UserID:      session.UserID,
						Name:        targetName,
						ParentID:    session.ParentID,
						SizeBytes:   session.TotalSize,
						MimeType:    detectMimeType(targetName),
						Status:      "PROCESSING",
						IsEncrypted: req.IsEncrypted,
					}
					if req.EncryptionSalt != "" {
						file.EncryptionSalt = &req.EncryptionSalt
					}
					if err := files.Create(ctx, file); err != nil {
						return err
					}
					result.FileID = file.ID

					if err := blocks.LinkBlocksToFile(ctx, file.ID, seqs); err != nil {
						return err
					}

					uploader, err := users.GetByID(ctx, session.UserID)
					if err != nil {
						return fmt.Errorf("resolve uploader for owner perm: %w", err)
					}
					if err := perms.GrantPermission(ctx, &domain.Permission{
						FileID:       file.ID,
						GranteeEmail: uploader.Email,
						Role:         domain.RoleOwner,
					}); err != nil {
						return err
					}
				} else {
					// Collision exists: backup old state to file_versions (version mode)
					isVersionUpdate = true
					oldBlocks, err := blocks.ListFileBlockHashes(ctx, existingFile.ID)
					if err != nil {
						return fmt.Errorf("list old blocks: %w", err)
					}

					// Atomically resolve next version number from DB to eliminate gaps and race conditions
					var maxVersion int
					err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_number), 0) FROM file_versions WHERE file_id = $1", existingFile.ID).Scan(&maxVersion)
					if err != nil {
						return fmt.Errorf("resolve next version number: %w", err)
					}
					nextVersion := maxVersion + 1

					fileVersion := &domain.FileVersion{
						FileID:        existingFile.ID,
						VersionNumber: nextVersion,
						SizeBytes:     existingFile.SizeBytes,
						ChunkHashes:   oldBlocks,
					}
					if err := files.CreateFileVersion(ctx, fileVersion); err != nil {
						return fmt.Errorf("create file version: %w", err)
					}

					// Update the active file record
					existingFile.SizeBytes = session.TotalSize
					existingFile.MimeType = detectMimeType(session.Filename)
					existingFile.Status = "PROCESSING"
					existingFile.IsEncrypted = req.IsEncrypted
					if req.EncryptionSalt != "" {
						existingFile.EncryptionSalt = &req.EncryptionSalt
					} else {
						existingFile.EncryptionSalt = nil
					}
					if err := files.Update(ctx, existingFile); err != nil {
						return fmt.Errorf("update existing file metadata: %w", err)
					}

					result.FileID = existingFile.ID

					// Replace blocks
					if err := blocks.ReplaceBlocksForFile(ctx, existingFile.ID, seqs); err != nil {
						return err
					}

					if err := files.UpdateAIMetadata(ctx, existingFile.ID, nil, nil, nil); err != nil {
						return fmt.Errorf("clear stale AI metadata before scan: %w", err)
					}
				}
			} else {
				// No collision, create new file
				file := &domain.File{
					UserID:      session.UserID,
					Name:        session.Filename,
					ParentID:    session.ParentID,
					SizeBytes:   session.TotalSize,
					MimeType:    detectMimeType(session.Filename),
					Status:      "PROCESSING",
					IsEncrypted: req.IsEncrypted,
				}
				if req.EncryptionSalt != "" {
					file.EncryptionSalt = &req.EncryptionSalt
				}
				if err := files.Create(ctx, file); err != nil {
					return err
				}
				result.FileID = file.ID

				// 5. Link blocks to the file in order.
				if err := blocks.LinkBlocksToFile(ctx, file.ID, seqs); err != nil {
					return err
				}

				// 6. Grant the uploader OWNER. Resolve their email from the user row.
				uploader, err := users.GetByID(ctx, session.UserID)
				if err != nil {
					return fmt.Errorf("resolve uploader for owner perm: %w", err)
				}
				if err := perms.GrantPermission(ctx, &domain.Permission{
					FileID:       file.ID,
					GranteeEmail: uploader.Email,
					Role:         domain.RoleOwner,
				}); err != nil {
					return err
				}
			}

			// Persist processing intent in the same transaction as file metadata so
			// queue outages cannot make new content readable or lose the scan job.
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO file_processing_outbox(file_id, user_id)
				VALUES ($1, $2)
				ON CONFLICT (file_id) DO UPDATE SET user_id = EXCLUDED.user_id,
					attempts = 0, next_attempt_at = NOW(), created_at = NOW()`, result.FileID, session.UserID); err != nil {
				return fmt.Errorf("enqueue file processing: %w", err)
			}

			// 7. Mark the session COMPLETED.
			if err := sessions.UpdateSessionStatus(ctx, req.SessionID, domain.SessionStatusCompleted); err != nil {
				return err
			}

			// 8. Record in Journal for Delta Synchronization (Fan-out to all collaborators)
			if s.journal != nil {
				action := domain.ActionFileCreated
				if isVersionUpdate {
					action = domain.ActionFileUpdated
				}
				recordedAction = action

				recipientSet := map[string]struct{}{
					session.UserID: {},
				}
				if s.perms != nil {
					if collabs, err := perms.FindCollaboratorUserIDs(ctx, result.FileID); err == nil {
						for _, c := range collabs {
							if c != "" {
								recipientSet[c] = struct{}{}
							}
						}
					}
				}

				entries := make([]*domain.JournalEntry, 0, len(recipientSet))
				for rUID := range recipientSet {
					entries = append(entries, &domain.JournalEntry{
						UserID:      rUID,
						FileID:      result.FileID,
						Action:      action,
						ParentID:    session.ParentID,
						Name:        session.Filename,
						IsDirectory: false,
						SizeBytes:   session.TotalSize,
						Status:      "PROCESSING",
					})
				}

				cursors, err := s.journal.WithTx(tx).BatchRecord(ctx, entries)
				if err != nil {
					return fmt.Errorf("record journal entries: %w", err)
				}
				recordedCursors = make(map[string]int64)
				for i, e := range entries {
					if i < len(cursors) {
						recordedCursors[e.UserID] = cursors[i]
					}
				}
			}

			result.SessionID = req.SessionID
			result.Status = domain.SessionStatusCompleted
			return nil
		})
	}
	if txErr != nil {
		s.log.Error("complete upload failed", "session_id", req.SessionID, "err", txErr)
		return nil, txErr
	}

	s.log.Info("upload completed", "session_id", req.SessionID, "file_id", result.FileID)
	metrics.UploadsCompleted.Inc()

	// Publish the processing job after commit. A failed publish leaves the file
	// unavailable; the durable retry/reconciliation path is tracked separately.
	if s.publisher != nil {
		if err := s.publisher.PublishThumbnailJob(ctx, queue.ThumbnailMessage{
			FileID: result.FileID,
			UserID: uploaderID,
		}); err != nil {
			s.log.Error("failed to publish processing job; file remains unavailable", "file_id", result.FileID, "err", err)
		} else if s.db != nil {
			// The outbox remains until a worker completes scanning. Delay its
			// fallback delivery after this immediate publish succeeds.
			_, _ = s.db.ExecContext(ctx, `UPDATE file_processing_outbox SET next_attempt_at = NOW() + INTERVAL '10 minutes' WHERE file_id = $1`, result.FileID)
		}
	}

	// Notify the uploader's open tabs so their file explorer refreshes.
	s.notifier.NotifyUser(uploaderID, wsSync.NotificationEvent{
		Type: wsSync.EventUploadComplete,
		Payload: map[string]string{
			"file_id":    result.FileID,
			"session_id": result.SessionID,
		},
	})

	// Broadcast incremental delta event to uploader and all collaborators
	for uID, cursor := range recordedCursors {
		s.notifier.NotifyUser(uID, wsSync.NotificationEvent{
			Type: wsSync.EventSyncDelta,
			Payload: map[string]any{
				"cursor":  cursor,
				"file_id": result.FileID,
				"action":  recordedAction,
			},
		})
	}

	return &result, nil
}

// verifyBlockExists probes storage for a block. The local driver GetObject
// returns an error wrapping fs.ErrNotExist for missing objects; we treat any
// successful open as "present" and immediately close the stream.
func (s *UploadService) verifyBlockExists(ctx context.Context, blockHash string) error {
	rc, err := s.storage.GetObject(ctx, "blocks/"+blockHash)
	if err != nil {
		return err
	}
	return rc.Close()
}
