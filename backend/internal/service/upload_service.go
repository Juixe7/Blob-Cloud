// Package service contains the application/orchestration layer: it coordinates
// repositories and storage providers to implement use cases that span multiple
// domain objects. Services hold the transactional boundaries that repositories
// individually cannot.
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/queue"
	wsSync "go-drive-clone/internal/sync"
)

// UploadService orchestrates resumable uploads: initiating a session with
// deduplication, regenerating URLs for resumption, and committing a completed
// upload as one atomic transaction. After a successful completion it publishes
// a thumbnail job to the event queue (if configured).
type UploadService struct {
	db        *sql.DB
	users     *postgresrepo.UserRepository
	files     *postgresrepo.FileRepository
	blocks    *postgresrepo.BlockRepository
	sessions  *postgresrepo.UploadSessionRepository
	perms     *postgresrepo.PermissionRepository
	storage   domain.StorageProvider
	publisher queue.Publisher
	notifier  wsSync.Notifier // optional; nil-safe (NoopNotifier)
	log       *slog.Logger
}

// NewUploadService wires the service with all the repositories it needs. The
// *sql.DB is retained so the service can open the cross-repository transaction
// for CompleteSession. publisher may be queue.NoopPublisher if SQS is not
// configured. notifier may be nil.
func NewUploadService(
	db *sql.DB,
	users *postgresrepo.UserRepository,
	files *postgresrepo.FileRepository,
	blocks *postgresrepo.BlockRepository,
	sessions *postgresrepo.UploadSessionRepository,
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

// InitiateRequest is the body of POST /api/upload/initiate.
type InitiateRequest struct {
	Filename  string         `json:"filename"`
	ParentID  *string        `json:"parent_id"`
	UserID    string         `json:"user_id"`
	TotalSize int64          `json:"total_size"`
	Chunks    []InitiateChunk `json:"chunks"`
}

// InitiateChunk is one chunk declared by the client at initiation.
type InitiateChunk struct {
	SHA256    string `json:"sha256"`
	SizeBytes int32  `json:"size_bytes"`
}

// InitiateResponse is returned to the client so it knows which chunks to upload
// and where. Already-existing chunks carry no upload URL (deduplication hit).
type InitiateResponse struct {
	SessionID string              `json:"session_id"`
	Status    string              `json:"status"`
	Chunks    []InitiateRespChunk `json:"chunks"`
}

// InitiateRespChunk is the per-chunk reply: its sequence, hash, whether it is
// already stored, and the upload URL when the client must PUT it.
type InitiateRespChunk struct {
	SequenceNumber int    `json:"sequence_number"`
	SHA256         string `json:"sha256"`
	SizeBytes      int32  `json:"size_bytes"`
	AlreadyExists  bool   `json:"already_exists"`
	UploadURL      string `json:"upload_url,omitempty"`
}

// Initiate performs deduplication-aware session creation. It returns the new
// session id plus, for each chunk, whether storage already has it and (if not)
// a presigned-style upload URL pointing at the local PUT endpoint.
func (s *UploadService) Initiate(ctx context.Context, req InitiateRequest) (*InitiateResponse, error) {
	if req.UserID == "" || req.Filename == "" {
		return nil, errors.New("user_id and filename are required")
	}
	if len(req.Chunks) == 0 {
		return nil, errors.New("at least one chunk is required")
	}

	// 1. Collect chunk hashes and ask the global blocks table which already
	//    exist. This is the deduplication check: hits skip both upload and
	//    storage of the physical block.
	hashes := make([]string, 0, len(req.Chunks))
	for _, c := range req.Chunks {
		if c.SHA256 == "" {
			return nil, errors.New("chunk sha256 must not be empty")
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

	// 2. Build the session + its blocks. Pre-existing chunks are marked
	//    is_uploaded=true so completion won't expect a fresh upload.
	session := &domain.UploadSession{
		UserID:    req.UserID,
		Filename:  req.Filename,
		ParentID:  req.ParentID,
		TotalSize: req.TotalSize,
		Status:    domain.SessionStatusInitiated,
	}
	respChunks := make([]InitiateRespChunk, 0, len(req.Chunks))
	blocks := make([]domain.SessionBlock, 0, len(req.Chunks))
	for i, c := range req.Chunks {
		alreadyExists := existingSet[c.SHA256]
		sb := domain.SessionBlock{
			BlockHash:      c.SHA256,
			SequenceNumber: i,
			SizeBytes:      c.SizeBytes,
			IsUploaded:     alreadyExists,
		}
		blocks = append(blocks, sb)

		rc := InitiateRespChunk{
			SequenceNumber: i,
			SHA256:         c.SHA256,
			SizeBytes:      c.SizeBytes,
			AlreadyExists:  alreadyExists,
		}
		// 3. For chunks the client must still upload, generate a URL. We use a
		//    generous lifetime; the local driver ignores it, S3 would honour it.
		if !alreadyExists {
			url, err := s.storage.GenerateUploadURL(ctx, c.SHA256, 30*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("generate upload url for %s: %w", c.SHA256, err)
			}
			rc.UploadURL = url
		}
		respChunks = append(respChunks, rc)
	}

	// 4. Persist session + blocks atomically.
	if err := s.sessions.CreateSession(ctx, session, blocks); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
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

// GetSession returns the session and, for any chunk still not uploaded, a fresh
// upload URL. A COMPLETED session returns its terminal status with no URLs.
func (s *UploadService) GetSession(ctx context.Context, id string) (*SessionStatusResponse, error) {
	session, blocks, err := s.sessions.GetSessionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
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
			url, err := s.storage.GenerateUploadURL(ctx, b.BlockHash, 30*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("regenerate upload url: %w", err)
			}
			rc.UploadURL = url
		}
		resp.Chunks = append(resp.Chunks, rc)
	}
	return resp, nil
}

// CompleteRequest is the body of POST /api/upload/complete.
type CompleteRequest struct {
	SessionID string `json:"session_id"`
}

// CompleteResponse confirms a finished upload and returns the new file id.
type CompleteResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	FileID    string `json:"file_id"`
}

// Complete finalises a session inside a SINGLE database transaction. Steps:
//  1. Load session + blocks; reject if not INITIATED.
//  2. For every block still marked not-uploaded, verify it physically exists in
//     storage; abort (no commit) if any is missing.
//  3. Upsert each block into the global blocks table (GetOrCreate = dedup).
//  4. Create the file metadata row.
//  5. Insert the ordered file_blocks mapping.
//  6. Grant the uploader a default OWNER permission.
//  7. Set session status COMPLETED.
//  8. Commit. Any error rolls the whole thing back.
func (s *UploadService) Complete(ctx context.Context, req CompleteRequest) (*CompleteResponse, error) {
	if req.SessionID == "" {
		return nil, errors.New("session_id is required")
	}

	var result CompleteResponse
	var uploaderID string
	txErr := postgresrepo.RunInTx(ctx, s.db, func(tx postgresrepo.DBTX) error {
		sessions := s.sessions.WithTx(tx)
		blocks := s.blocks.WithTx(tx)
		files := s.files.WithTx(tx)
		perms := s.perms.WithTx(tx)
		users := s.users.WithTx(tx)

		// 1. Load session + blocks (locks the session row for the tx).
			session, sessionBlocks, err := sessions.GetSessionByID(ctx, req.SessionID)
			if err != nil {
				return err
			}
			if session.Status != domain.SessionStatusInitiated {
				return fmt.Errorf("session %s is %s, cannot complete", req.SessionID, session.Status)
			}
			uploaderID = session.UserID

		// 2. Verify every declared block is physically present in storage. A
		//    missing block means the client never finished uploading it.
		for _, b := range sessionBlocks {
			if b.IsUploaded {
				continue
			}
			if err := s.verifyBlockExists(ctx, b.BlockHash); err != nil {
				return fmt.Errorf("block %s not in storage: %w", b.BlockHash, err)
			}
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

		// 4. Create the file metadata.
		file := &domain.File{
			UserID:    session.UserID,
			Name:      session.Filename,
			ParentID:  session.ParentID,
			SizeBytes: session.TotalSize,
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

		// 7. Mark the session COMPLETED.
		if err := sessions.UpdateSessionStatus(ctx, req.SessionID, domain.SessionStatusCompleted); err != nil {
			return err
		}

		result.SessionID = req.SessionID
		result.Status = domain.SessionStatusCompleted
		return nil
	})
	if txErr != nil {
		s.log.Error("complete upload failed", "session_id", req.SessionID, "err", txErr)
		return nil, txErr
	}

	s.log.Info("upload completed", "session_id", req.SessionID, "file_id", result.FileID)

	// Publish a thumbnail job to the event queue. Failure to publish is
	// non-fatal — the upload succeeded, the thumbnail will just be missed.
	if err := s.publisher.PublishThumbnailJob(ctx, queue.ThumbnailMessage{
		FileID: result.FileID,
		UserID: uploaderID,
	}); err != nil {
		s.log.Error("failed to publish thumbnail job", "file_id", result.FileID, "err", err)
	}

	// Notify the uploader's open tabs so their file explorer refreshes.
	s.notifier.NotifyUser(uploaderID, wsSync.NotificationEvent{
		Type: wsSync.EventUploadComplete,
		Payload: map[string]string{
			"file_id":   result.FileID,
			"session_id": result.SessionID,
		},
	})

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
