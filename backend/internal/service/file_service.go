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
	"strings"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	wsSync "go-drive-clone/internal/sync"
)

// FileService orchestrates file-level operations: rename, move, delete, and
// download. It validates permissions before mutating metadata and handles
// garbage collection of orphaned S3 blocks after recursive deletes.
type FileService struct {
	db       *sql.DB
	users    *postgresrepo.UserRepository
	files    *postgresrepo.FileRepository
	blocks   *postgresrepo.BlockRepository
	perms    *postgresrepo.PermissionRepository
	storage  domain.StorageProvider
	journal  *postgresrepo.JournalRepository
	notifier wsSync.Notifier
	log      *slog.Logger
}

// NewFileService wires the service with all the repositories it needs.
func NewFileService(
	db *sql.DB,
	users *postgresrepo.UserRepository,
	files *postgresrepo.FileRepository,
	blocks *postgresrepo.BlockRepository,
	perms *postgresrepo.PermissionRepository,
	storage domain.StorageProvider,
	log *slog.Logger,
) *FileService {
	return &FileService{
		db: db, users: users, files: files, blocks: blocks,
		perms: perms, storage: storage, log: log,
	}
}

// WithJournal sets the journal repository for delta synchronization.
func (s *FileService) WithJournal(journal *postgresrepo.JournalRepository) *FileService {
	s.journal = journal
	return s
}

// WithNotifier sets the real-time event notifier.
func (s *FileService) WithNotifier(notifier wsSync.Notifier) *FileService {
	s.notifier = notifier
	return s
}

func (s *FileService) recordAndNotify(ctx context.Context, userID, fileID, action string, file *domain.File) {
	if s.journal == nil {
		return
	}
	name := ""
	var parentID *string
	isDir := false
	var sizeBytes int64 = 0
	status := "ACTIVE"
	var mimeType string
	if file != nil {
		name = file.Name
		parentID = file.ParentID
		isDir = file.IsDirectory
		sizeBytes = file.SizeBytes
		status = file.Status
		mimeType = file.MimeType
	}
	jEntry := &domain.JournalEntry{
		UserID:      userID,
		FileID:      fileID,
		Action:      action,
		ParentID:    parentID,
		Name:        name,
		IsDirectory: isDir,
		SizeBytes:   sizeBytes,
		MimeType:    mimeType,
		Status:      status,
	}
	cursor, err := s.journal.Record(ctx, jEntry)
	if err != nil {
		s.log.Error("failed to record journal entry", "file_id", fileID, "action", action, "err", err)
		return
	}
	if s.notifier != nil {
		s.notifier.NotifyUser(userID, wsSync.NotificationEvent{
			Type: wsSync.EventSyncDelta,
			Payload: map[string]any{
				"cursor":  cursor,
				"file_id": fileID,
				"action":  action,
			},
		})
	}
}

// RenameMoveRequest is the body of PATCH /api/files/{id}. Snake_case matches
// the backend's JSON decoder.
type RenameMoveRequest struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"` // nil = unchanged, empty-string means root
}

// GetFile exposes the file repository's GetByID method.
func (s *FileService) GetFile(ctx context.Context, id string) (*domain.File, error) {
	return s.files.GetByID(ctx, id)
}

// GetResolvedPermission exposes the file repository's GetResolvedPermission method.
func (s *FileService) GetResolvedPermission(ctx context.Context, fileID string, userEmail string) (string, error) {
	return s.files.GetResolvedPermission(ctx, fileID, userEmail)
}

// RenameMove changes a file/folder's name and/or parent_id after verifying the
// requesting user holds EDITOR or OWNER permissions. It also guards against
// parent-cycle corruption (moving a folder into itself or a descendant).
//
// Returns the updated File metadata on success.
func (s *FileService) RenameMove(ctx context.Context, userID, fileID string, req RenameMoveRequest) (*domain.File, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	// 3. Build the update target. We load the existing row first so we have a
	//    stable baseline and can apply partial changes.
	existing, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}

	// 2. Authorise and apply Golden Rule logic for moves.
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	role := domain.RoleOwner
	if existing.UserID != userID {
		role, err = s.files.GetResolvedPermission(ctx, fileID, user.Email)
		if err != nil {
			if errors.Is(err, domain.ErrPermissionNotFound) {
				return nil, fmt.Errorf("access denied: %s", user.Email)
			}
			return nil, fmt.Errorf("permission check: %w", err)
		}
	}

	isMove := req.ParentID != nil && ((existing.ParentID == nil && *req.ParentID != "") || (existing.ParentID != nil && *req.ParentID != *existing.ParentID))

	if isMove {
		if role == domain.RoleViewer || (role == domain.RoleEditor && existing.IsDirectory) {
			s.log.Info("golden rule fallback: creating shortcut instead of physical move", "file_id", fileID, "user_id", userID, "role", role)
			shortcut, err := s.CreateShortcut(ctx, userID, CreateShortcutRequest{
				FileID:   fileID,
				ParentID: req.ParentID,
			})
			return shortcut, err
		}
	}

	// If not fallback to shortcut, ensure they have at least EDITOR to proceed with rename/move
	if role != domain.RoleOwner && role != domain.RoleEditor {
		return nil, fmt.Errorf("access denied: %s", user.Email)
	}

	// Copy fields that the caller wants to change.
	updated := *existing
	if req.Name != "" {
		updated.Name = req.Name
	}
	if req.ParentID != nil {
		updated.ParentID = req.ParentID
		// Normalise: empty string -> nil pointer (root).
		if updated.ParentID != nil && *updated.ParentID == "" {
			updated.ParentID = nil
		}
	}

	// 4. Cycle guard: if parent_id is changing, the new parent must not be the
	//    file itself nor any of its own descendants (O(1) materialized path check).
	if req.ParentID != nil && existing.IsDirectory {
		if updated.ParentID != nil && *updated.ParentID != "" {
			if *updated.ParentID == existing.ID {
				return nil, fmt.Errorf("cannot move a folder into itself")
			}
			targetParent, err := s.files.GetByID(ctx, *updated.ParentID)
			if err != nil {
				return nil, fmt.Errorf("lookup target folder: %w", err)
			}
			if !targetParent.IsDirectory {
				return nil, fmt.Errorf("target parent is not a directory")
			}
			if strings.HasPrefix(targetParent.Path, existing.Path) {
				return nil, fmt.Errorf("cannot move directory inside its own descendant")
			}
		}
	}
	// Also guard the simple self-parent case.
	if updated.ParentID != nil && *updated.ParentID == existing.ID {
		return nil, fmt.Errorf("cannot move a folder into itself")
	}

	// 5. Persist.
	if err := s.files.Update(ctx, &updated); err != nil {
		return nil, fmt.Errorf("update file: %w", err)
	}

	s.log.Info("file renamed/moved",
		"file_id", fileID, "user_id", userID,
		"new_name", updated.Name,
		"new_parent_id", logNilStr(updated.ParentID))

	if updated.Name != existing.Name {
		s.recordAndNotify(ctx, userID, fileID, domain.ActionFileRenamed, &updated)
	}
	if isMove {
		s.recordAndNotify(ctx, userID, fileID, domain.ActionFileMoved, &updated)
	}

	return &updated, nil
}

// DeleteResult is returned by Delete after a successful recursive removal.
type DeleteResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	// Count of file rows removed (including descendants).
	DeletedCount int64 `json:"deleted_count"`
	// Number of orphaned physical blocks scheduled for GC.
	GCBlocks int `json:"gc_blocks,omitempty"`
}

// SoftDelete soft-deletes a file/folder and all descendants (setting deleted_at = CURRENT_TIMESTAMP)
// after verifying the requesting user holds OWNER permission.
func (s *FileService) SoftDelete(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	// OWNER or EDITOR required for soft deletion.
	allowed, err := s.perms.CheckUserPermission(ctx, fileID, user.Email,
		[]string{domain.RoleOwner, domain.RoleEditor})
	if err != nil {
		return nil, fmt.Errorf("permission check: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("access denied: %s", user.Email)
	}

	if err := s.files.SoftDelete(ctx, fileID, userID); err != nil {
		return nil, fmt.Errorf("soft delete: %w", err)
	}

	s.log.Info("file soft deleted", "file_id", fileID, "user_id", userID)
	s.recordAndNotify(ctx, userID, fileID, domain.ActionFileTrashed, nil)

	return &DeleteResult{
		Status:  "success",
		Message: "file moved to trash",
	}, nil
}

// Restore restores a soft-deleted file/folder and all descendants (setting deleted_at = NULL)
// after verifying the requesting user holds OWNER permission.
func (s *FileService) Restore(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	// OWNER or EDITOR required to restore.
	allowed, err := s.perms.CheckUserPermission(ctx, fileID, user.Email,
		[]string{domain.RoleOwner, domain.RoleEditor})
	if err != nil {
		return nil, fmt.Errorf("permission check: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("access denied: %s", user.Email)
	}

	if err := s.files.Restore(ctx, fileID, userID); err != nil {
		return nil, fmt.Errorf("restore file: %w", err)
	}

	s.log.Info("file restored", "file_id", fileID, "user_id", userID)
	restoredFile, _ := s.files.GetByID(ctx, fileID)
	s.recordAndNotify(ctx, userID, fileID, domain.ActionFileRestored, restoredFile)

	return &DeleteResult{
		Status:  "success",
		Message: "file restored successfully",
	}, nil
}

// ListTrash returns soft-deleted items owned by the user.
func (s *FileService) ListTrash(ctx context.Context, userID string) ([]*domain.File, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	return s.files.ListTrash(ctx, userID)
}

// Delete is an alias for SoftDelete to support standard delete routes.
func (s *FileService) Delete(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	return s.SoftDelete(ctx, userID, fileID)
}

// PermanentDelete recursively removes a file/folder and all descendants from DB and storage
// after verifying the requesting user holds OWNER permission.
func (s *FileService) PermanentDelete(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	// OWNER required for permanent deletion.
	allowed, err := s.perms.CheckUserPermission(ctx, fileID, user.Email,
		[]string{domain.RoleOwner})
	if err != nil {
		return nil, fmt.Errorf("permission check: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("access denied: %s", user.Email)
	}

	deletedCount, orphanHashes, err := s.files.DeleteRecursive(ctx, fileID, userID)
	if err != nil {
		return nil, fmt.Errorf("delete recursive: %w", err)
	}

	// Fire-and-forget GC: delete orphaned blocks from storage in the background.
	if len(orphanHashes) > 0 {
		go s.garbageCollectBlocks(context.Background(), orphanHashes)
	}

	s.log.Info("file permanently deleted",
		"file_id", fileID, "user_id", userID,
		"deleted_count", deletedCount,
		"orphaned_blocks", len(orphanHashes))
	s.recordAndNotify(ctx, userID, fileID, domain.ActionFileDeleted, nil)

	return &DeleteResult{
		Status:       "success",
		Message:      "file permanently deleted",
		DeletedCount: deletedCount,
		GCBlocks:     len(orphanHashes),
	}, nil
}

// CreateFolder inserts a new directory row or returns an existing one (idempotent).
// Returns (folder, created, error) where created is true if a new folder was inserted.
func (s *FileService) CreateFolder(ctx context.Context, userID, name string, parentID *string) (*domain.File, bool, error) {
	if userID == "" || name == "" {
		return nil, false, fmt.Errorf("user_id and name are required")
	}

	// 1. Idempotency check: if folder already exists, return 200 OK candidate
	existing, err := s.files.GetFolderByNameAndParent(ctx, userID, name, parentID)
	if err == nil && existing != nil {
		s.log.Info("folder creation skipped (already exists)", "folder_id", existing.ID, "user_id", userID, "name", name)
		return existing, false, nil
	}

	// 2. Resolve creator's email so the OWNER permission row can be written.
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("resolve user: %w", err)
	}

	var (
		folder   *domain.File
		wasNew   bool
	)
	err = postgresrepo.RunInTx(ctx, s.db, func(tx postgresrepo.DBTX) error {
		files := s.files.WithTx(tx)
		perms := s.perms.WithTx(tx)

		// Race-condition check inside Tx
		if existing, err := files.GetFolderByNameAndParent(ctx, userID, name, parentID); err == nil && existing != nil {
			folder = existing
			wasNew = false
			return nil
		}

		f := &domain.File{
			UserID:      userID,
			Name:        name,
			ParentID:    parentID,
			IsDirectory: true,
		}
		if err := files.Create(ctx, f); err != nil {
			return err
		}
		if err := perms.GrantPermission(ctx, &domain.Permission{
			FileID:       f.ID,
			GranteeEmail: user.Email,
			Role:         domain.RoleOwner,
		}); err != nil {
			return err
		}
		folder = f
		wasNew = true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("create folder: %w", err)
	}

	if wasNew {
		s.log.Info("folder created", "folder_id", folder.ID, "user_id", userID, "name", name)
		s.recordAndNotify(ctx, userID, folder.ID, domain.ActionFileCreated, folder)
	} else {
		s.log.Info("folder reused", "folder_id", folder.ID, "user_id", userID, "name", name)
	}
	return folder, wasNew, nil
}

// ListDirectory returns the immediate children of parentID (or the user's root
// when parentID is nil). Folders are returned first, then files, alphabetically
// — the ordering is enforced by the repository's ORDER BY clause.
func (s *FileService) ListDirectory(ctx context.Context, userID string, parentID *string) ([]*domain.File, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	var targetParentID = parentID
	if parentID != nil {
		parent, err := s.files.GetByID(ctx, *parentID)
		if err == nil && parent.TargetID != nil && *parent.TargetID != "" {
			targetParentID = parent.TargetID
		}
	}

	items, err := s.files.ListDirectory(ctx, userID, targetParentID)
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}

	// If parentID is provided, find the resolved permission of the parent folder to populate the role for items
	if targetParentID != nil {
		user, err := s.users.GetByID(ctx, userID)
		if err == nil {
			role, err := s.files.GetResolvedPermission(ctx, *targetParentID, user.Email)
			if err == nil {
				for _, item := range items {
					item.Role = role
				}
			}
		}
	}
	return items, nil
}

// ListSharedFiles returns all files/folders that have been shared with the user.
func (s *FileService) ListSharedFiles(ctx context.Context, userID string) ([]*domain.File, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}
	items, err := s.files.ListSharedWithUser(ctx, user.Email, userID)
	if err != nil {
		return nil, fmt.Errorf("list shared files: %w", err)
	}
	return items, nil
}

// GetUserStorage calculates real total byte usage and category breakdown for a user.
func (s *FileService) GetUserStorage(ctx context.Context, userID string) (*domain.UserStorageUsage, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	return s.files.GetUserStorageUsage(ctx, userID)
}

// GetDownloadInfo returns the ordered block hashes and the File metadata needed
// to stream a download. The caller verifies permissions before calling this.
func (s *FileService) GetDownloadInfo(ctx context.Context, fileID string) (*domain.File, []string, error) {
	f, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return nil, nil, fmt.Errorf("get file: %w", err)
	}
	if f.IsDirectory {
		return nil, nil, fmt.Errorf("cannot download a directory")
	}

	hashes, err := s.blocks.ListFileBlockHashes(ctx, fileID)
	if err != nil {
		return nil, nil, fmt.Errorf("get block hashes: %w", err)
	}
	return f, hashes, nil
}

// GetDownloadInfoWithBlocks returns the ordered domain.Block list and the File metadata needed
// to stream a download. The caller verifies permissions before calling this.
func (s *FileService) GetDownloadInfoWithBlocks(ctx context.Context, fileID string) (*domain.File, []*domain.Block, error) {
	f, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return nil, nil, fmt.Errorf("get file: %w", err)
	}
	if f.IsDirectory {
		return nil, nil, fmt.Errorf("cannot download a directory")
	}

	blocks, err := s.blocks.ListFileBlocks(ctx, fileID)
	if err != nil {
		return nil, nil, fmt.Errorf("get blocks: %w", err)
	}
	return f, blocks, nil
}

// garbageCollectBlocks deletes orphaned physical blocks from storage. Each
// deletion is best-effort; a failure is logged but not propagated.
func (s *FileService) garbageCollectBlocks(ctx context.Context, hashes []string) {
	for _, h := range hashes {
		key := "blocks/" + h
		if err := s.storage.DeleteObject(ctx, key); err != nil {
			s.log.Warn("GC: failed to delete orphaned block",
				"hash", h, "err", err)
		} else {
			s.log.Debug("GC: deleted orphaned block", "hash", h)
		}
	}
}

// BulkSoftDelete soft-deletes multiple file/folder roots for userID in a single atomic transaction.
func (s *FileService) BulkSoftDelete(ctx context.Context, userID string, req domain.BulkDeleteRequest) error {
	if userID == "" || len(req.IDs) == 0 {
		return nil
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}
	for _, id := range req.IDs {
		allowed, err := s.perms.CheckUserPermission(ctx, id, user.Email,
			[]string{domain.RoleOwner, domain.RoleEditor})
		if err != nil {
			return fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			return fmt.Errorf("access denied: %s", user.Email)
		}
	}
	return s.files.BulkSoftDelete(ctx, req.IDs, userID)
}

// BulkRestore restores multiple file/folder roots for userID in a single atomic transaction.
func (s *FileService) BulkRestore(ctx context.Context, userID string, req domain.BulkRestoreRequest) error {
	if userID == "" || len(req.IDs) == 0 {
		return nil
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}
	for _, id := range req.IDs {
		allowed, err := s.perms.CheckUserPermission(ctx, id, user.Email,
			[]string{domain.RoleOwner, domain.RoleEditor})
		if err != nil {
			return fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			return fmt.Errorf("access denied: %s", user.Email)
		}
	}
	return s.files.BulkRestore(ctx, req.IDs, userID)
}

// BulkMove updates parent_id for multiple file IDs for userID in a single atomic transaction.
func (s *FileService) BulkMove(ctx context.Context, userID string, req domain.BulkMoveRequest) error {
	if userID == "" || len(req.IDs) == 0 {
		return nil
	}
	return s.files.BulkMove(ctx, req.IDs, req.ParentID, userID)
}

// BulkHardDelete hard-deletes multiple file/folder roots for userID in a single transaction and triggers S3 block GC.
func (s *FileService) BulkHardDelete(ctx context.Context, userID string, req domain.BulkDeleteRequest) (int64, error) {
	if userID == "" || len(req.IDs) == 0 {
		return 0, nil
	}
	deletedCount, orphans, err := s.files.BulkHardDelete(ctx, req.IDs, userID)
	if err != nil {
		return 0, err
	}
	if len(orphans) > 0 {
		go s.garbageCollectBlocks(context.Background(), orphans)
	}
	return deletedCount, nil
}

// logNilStr converts a *string to a log-safe representation.
func logNilStr(s *string) string {
	if s == nil {
		return "<root>"
	}
	return *s
}

// readRoles is a helper for extracting user email from a JWT token and
// checking permissions. It is used by the HTTP handlers so the
// permission-check boilerplate stays DRY.

// AuthoriseRead checks that userID holds VIEWER, EDITOR, or OWNER on fileID (directly or via parent inheritance).
func (s *FileService) AuthoriseRead(ctx context.Context, userID, fileID string) error {
	// 1. If file is owned directly by the user, bypass check and grant access.
	file, err := s.files.GetByID(ctx, fileID)
	if err == nil && file.UserID == userID {
		return nil
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}

	role, err := s.files.GetResolvedPermission(ctx, fileID, user.Email)
	if err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			return fmt.Errorf("access denied: %s", user.Email)
		}
		return fmt.Errorf("permission check: %w", err)
	}

	if role == domain.RoleViewer || role == domain.RoleEditor || role == domain.RoleOwner {
		return nil
	}
	return fmt.Errorf("access denied: %s", user.Email)
}

// AuthoriseWrite checks that userID holds EDITOR or OWNER on fileID (directly or via parent inheritance).
func (s *FileService) AuthoriseWrite(ctx context.Context, userID, fileID string) error {
	// 1. If file is owned directly by the user, bypass check and grant access.
	file, err := s.files.GetByID(ctx, fileID)
	if err == nil && file.UserID == userID {
		return nil
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}

	role, err := s.files.GetResolvedPermission(ctx, fileID, user.Email)
	if err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			return fmt.Errorf("access denied: %s", user.Email)
		}
		return fmt.Errorf("permission check: %w", err)
	}

	if role == domain.RoleEditor || role == domain.RoleOwner {
		return nil
	}
	return fmt.Errorf("access denied: %s", user.Email)
}

// SanitiseFilename strips path separators and control characters from a
// filename so Content-Disposition can't be abused for header injection.
func SanitiseFilename(name string) string {
	// Fast path: nothing dangerous.
	if !strings.ContainsAny(name, `/\`) {
		return name
	}
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == '\\' {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CreateShortcutRequest represents the body of POST /api/files/shortcut
type CreateShortcutRequest struct {
	FileID   string  `json:"file_id"`
	ParentID *string `json:"parent_id"`
}

// CreateShortcut creates a shortcut to a file/folder in the user's drive.
func (s *FileService) CreateShortcut(ctx context.Context, userID string, req CreateShortcutRequest) (*domain.File, error) {
	if userID == "" || req.FileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	// 1. Resolve user to get their email
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	// 2. Authorize: USER must have at least VIEWER permission on the target file
	allowed, err := s.perms.CheckUserPermission(ctx, req.FileID, user.Email,
		[]string{domain.RoleOwner, domain.RoleEditor, domain.RoleViewer})
	if err != nil {
		return nil, fmt.Errorf("permission check: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("access denied: %s", user.Email)
	}

	// 3. Get target file metadata
	target, err := s.files.GetByID(ctx, req.FileID)
	if err != nil {
		return nil, fmt.Errorf("get target file: %w", err)
	}

	// 4. If target is already a shortcut, use the target's target_id instead
	var realTargetID string
	if target.TargetID != nil && *target.TargetID != "" {
		realTargetID = *target.TargetID
	} else {
		realTargetID = target.ID
	}

	// 5. Create new File entity
	shortcut := &domain.File{
		UserID:           userID,
		Name:             target.Name,
		ParentID:         req.ParentID,
		IsDirectory:      target.IsDirectory,
		SizeBytes:        0, // Shortcuts do not take storage bytes on their own
		TargetID:         &realTargetID,
		ShortcutTargetID: &realTargetID, // Also set the dedicated phase 8 column
		MimeType:         "application/vnd.google-apps.shortcut",
	}

	// Normalise parent_id
	if shortcut.ParentID != nil && *shortcut.ParentID == "" {
		shortcut.ParentID = nil
	}

	// 6. Persist
	if err := s.files.Create(ctx, shortcut); err != nil {
		return nil, fmt.Errorf("create shortcut: %w", err)
	}

	s.log.Info("shortcut created", "shortcut_id", shortcut.ID, "user_id", userID, "target_id", realTargetID)

	return shortcut, nil
}

// GetFileInfo fetches a file's metadata by ID.
func (s *FileService) GetFileInfo(ctx context.Context, fileID string) (*domain.File, error) {
	return s.files.GetByID(ctx, fileID)
}

const BlockSize int64 = 4194304

// CalculateRangeBlockOffset calculates the starting block index and offset inside that block
// given a global start byte offset.
// BlockSize is 4,194,304 bytes (4MB).
func (s *FileService) CalculateRangeBlockOffset(startByte int64) (startBlockIndex int64, blockOffset int64) {
	startBlockIndex = startByte / BlockSize
	blockOffset = startByte % BlockSize
	return startBlockIndex, blockOffset
}

// CalculateRangeBlockOffset calculates startBlockIndex and blockOffset from a startByte.
func CalculateRangeBlockOffset(startByte int64) (int64, int64) {
	return startByte / BlockSize, startByte % BlockSize
}

// CalculateDynamicRangeBlockOffset dynamically calculates the starting block index and intra-block offset
// given arbitrary variable-sized FastCDC blocks.
func CalculateDynamicRangeBlockOffset(blocks []*domain.Block, startByte int64) (startBlockIndex int, blockOffset int64) {
	if len(blocks) == 0 || startByte <= 0 {
		return 0, 0
	}
	var accumulated int64 = 0
	for i, b := range blocks {
		bSize := int64(b.SizeBytes)
		if startByte < accumulated+bSize {
			return i, startByte - accumulated
		}
		accumulated += bSize
	}
	return len(blocks) - 1, 0
}

// CalculateDynamicRangeBlockOffset dynamically calculates the starting block index and intra-block offset.
func (s *FileService) CalculateDynamicRangeBlockOffset(blocks []*domain.Block, startByte int64) (startBlockIndex int, blockOffset int64) {
	return CalculateDynamicRangeBlockOffset(blocks, startByte)
}


// CreateShortcutFromPublicLink creates a shortcut directly bypassing normal explicit database permissions,
// because authorization has already been established via the public link token.
func (s *FileService) CreateShortcutFromPublicLink(ctx context.Context, userID, targetID string, parentID *string) (*domain.File, error) {
	if userID == "" || targetID == "" {
		return nil, fmt.Errorf("user_id and target_id are required")
	}

	// 1. Get target file metadata
	target, err := s.files.GetByID(ctx, targetID)
	if err != nil {
		return nil, fmt.Errorf("get target file: %w", err)
	}

	// 2. If target is already a shortcut, use the target's target_id instead
	var realTargetID string
	if target.TargetID != nil && *target.TargetID != "" {
		realTargetID = *target.TargetID
	} else {
		realTargetID = target.ID
	}

	// 3. Create new File entity
	shortcut := &domain.File{
		UserID:           userID,
		Name:             target.Name,
		ParentID:         parentID,
		IsDirectory:      target.IsDirectory,
		SizeBytes:        0, // Shortcuts have no size
		TargetID:         &realTargetID,
		ShortcutTargetID: &realTargetID,
		MimeType:         "application/vnd.google-apps.shortcut",
		IsEncrypted:      target.IsEncrypted,
		EncryptionSalt:   target.EncryptionSalt,
		Status:           target.Status,
	}

	// 4. Save to db
	if err := s.files.Create(ctx, shortcut); err != nil {
		return nil, fmt.Errorf("create shortcut: %w", err)
	}

	return shortcut, nil
}
