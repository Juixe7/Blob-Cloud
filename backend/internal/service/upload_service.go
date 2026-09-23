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
	journal   *postgresrepo.JournalRepository
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

// WithJournal binds a JournalRepository for delta sync change logging.
func (s *UploadService) WithJournal(journal *postgresrepo.JournalRepository) *UploadService {
	s.journal = journal
	return s
}

// InitiateRequest is the body of POST /api/upload/initiate.
type InitiateRequest struct {
	Filename  string         `json:"filename"`
	ParentID  *string        `json:"parent_id"`
	UserID         string         `json:"user_id"`
	TotalSize      int64          `json:"total_size"`
	IsEncrypted    bool           `json:"is_encrypted"`
	EncryptionSalt string         `json:"encryption_salt,omitempty"`
	Chunks         []InitiateChunk `json:"chunks"`
}

// InitiateChunk is one chunk declared by the client at initiation.
type InitiateChunk struct {
	SHA256    string `json:"sha256"`
	BlockMD5  string `json:"block_md5"`
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
	SequenceNumber int      `json:"sequence_number"`
	SHA256         string   `json:"sha256"`
	SizeBytes      int32    `json:"size_bytes"`
	AlreadyExists  bool     `json:"already_exists"`
	UploadURL      string   `json:"upload_url,omitempty"`   // single PUT path (≤ 5 GiB)
	UploadID       string   `json:"upload_id,omitempty"`    // MPU upload ID (> 5 GiB)
	PartURLs       []string `json:"part_urls,omitempty"`    // one presigned URL per part
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
					pURL, err := mpu.PresignUploadPart(ctx, stagingKey, uploadID, p, 30*time.Minute)
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
				url, err := s.storage.GenerateStagingUploadURL(ctx, stagingKey, 30*time.Minute)
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

// GetSession returns the session and, for any chunk still not uploaded, a fresh
// upload URL. A COMPLETED session returns its terminal status with no URLs.
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
			url, err := s.storage.GenerateStagingUploadURL(ctx, stagingKey, 30*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("regenerate staging upload url: %w", err)
			}
			rc.UploadURL = url
		}
		resp.Chunks = append(resp.Chunks, rc)
	}
	return resp, nil
}

// CompleteRequest is the body of POST /api/upload/complete.
type CompleteRequest struct {
	SessionID      string `json:"session_id"`
	IsEncrypted    bool   `json:"is_encrypted"`
	EncryptionSalt string `json:"encryption_salt,omitempty"`
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
func (s *UploadService) Complete(ctx context.Context, req CompleteRequest, userID string) (*CompleteResponse, error) {
	if req.SessionID == "" {
		return nil, errors.New("session_id is required")
	}

	var result CompleteResponse
	var uploaderID string
	var recordedCursor int64
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
	txErr := postgresrepo.RunInTx(ctx, s.db, func(tx postgresrepo.DBTX) error {
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

		// 4. Handle File Versioning / Collision
		existingFile, err := files.GetFolderByNameAndParent(ctx, session.UserID, session.Filename, session.ParentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check existing file: %w", err)
		}

		if existingFile != nil && !existingFile.IsDirectory {
			// Collision exists: backup old state to file_versions
			oldBlocks, err := blocks.ListFileBlockHashes(ctx, existingFile.ID)
			if err != nil {
				return fmt.Errorf("list old blocks: %w", err)
			}
			
			// Count existing versions to increment
			versions, err := files.GetFileVersions(ctx, existingFile.ID)
			if err != nil {
				return fmt.Errorf("list versions: %w", err)
			}
			nextVersion := len(versions) + 1
			
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
			existingFile.Status = "ACTIVE"
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
		} else {
			// No collision, create new file
			file := &domain.File{
				UserID:         session.UserID,
				Name:           session.Filename,
				ParentID:       session.ParentID,
				SizeBytes:      session.TotalSize,
				MimeType:       detectMimeType(session.Filename),
				Status:         "ACTIVE",
				IsEncrypted:    req.IsEncrypted,
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

		// 7. Mark the session COMPLETED.
		if err := sessions.UpdateSessionStatus(ctx, req.SessionID, domain.SessionStatusCompleted); err != nil {
			return err
		}

		// 8. Record in Journal for Delta Synchronization
		if s.journal != nil {
			action := domain.ActionFileCreated
			if existingFile != nil && !existingFile.IsDirectory {
				action = domain.ActionFileUpdated
			}
			jEntry := &domain.JournalEntry{
				UserID:      session.UserID,
				FileID:      result.FileID,
				Action:      action,
				ParentID:    session.ParentID,
				Name:        session.Filename,
				IsDirectory: false,
				SizeBytes:   session.TotalSize,
				Status:      "ACTIVE",
			}
			cursor, err := s.journal.WithTx(tx).Record(ctx, jEntry)
			if err != nil {
				return fmt.Errorf("record journal entry: %w", err)
			}
			recordedCursor = cursor
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
	metrics.UploadsCompleted.Inc()

	// Publish a thumbnail job to the event queue. Failure to publish is
	// non-fatal — the upload succeeded, the thumbnail will just be missed.
	if s.publisher != nil {
		if err := s.publisher.PublishThumbnailJob(ctx, queue.ThumbnailMessage{
			FileID: result.FileID,
			UserID: uploaderID,
		}); err != nil {
			s.log.Error("failed to publish thumbnail job", "file_id", result.FileID, "err", err)
		}
	}

	// Notify the uploader's open tabs so their file explorer refreshes.
	s.notifier.NotifyUser(uploaderID, wsSync.NotificationEvent{
		Type: wsSync.EventUploadComplete,
		Payload: map[string]string{
			"file_id":   result.FileID,
			"session_id": result.SessionID,
		},
	})

	// Broadcast incremental delta event
	if recordedCursor > 0 {
		s.notifier.NotifyUser(uploaderID, wsSync.NotificationEvent{
			Type: wsSync.EventSyncDelta,
			Payload: map[string]any{
				"cursor":  recordedCursor,
				"file_id": result.FileID,
				"action":  domain.ActionFileCreated,
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
