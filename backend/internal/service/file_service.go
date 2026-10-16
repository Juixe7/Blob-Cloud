// Package service contains the application/orchestration layer: it coordinates
// repositories and storage providers to implement use cases that span multiple
// domain objects. Services hold the transactional boundaries that repositories
// individually cannot.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	wsSync "go-drive-clone/internal/sync"
)

// UserStore defines the user lookup operations used by FileService.
type UserStore interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// FileStore defines the file metadata persistence operations used by FileService.
type FileStore interface {
	GetByID(ctx context.Context, id string) (*domain.File, error)
	GetResolvedPermission(ctx context.Context, fileID string, userEmail string) (string, error)
	GetFileByNameAndParent(ctx context.Context, userID, name string, parentID *string) (*domain.File, error)
	GetFolderByNameAndParent(ctx context.Context, userID, name string, parentID *string) (*domain.File, error)
	Create(ctx context.Context, file *domain.File) error
	Update(ctx context.Context, file *domain.File) error
	SoftDelete(ctx context.Context, id, userID string) error
	Restore(ctx context.Context, id, userID string) error
	ListTrash(ctx context.Context, userID string) ([]*domain.File, error)
	DeleteRecursive(ctx context.Context, rootID, userID string) (int64, []string, error)
	ListDirectory(ctx context.Context, userID string, parentID *string) ([]*domain.File, error)
	ListSharedWithUser(ctx context.Context, userEmail string, userID string) ([]*domain.File, error)
	GetUserStorageUsage(ctx context.Context, userID string) (*domain.UserStorageUsage, error)
	BulkSoftDelete(ctx context.Context, ids []string, userID string) error
	BulkRestore(ctx context.Context, ids []string, userID string) error
	BulkMove(ctx context.Context, ids []string, parentID *string, userID string) error
	BulkHardDelete(ctx context.Context, ids []string, userID string) (int64, []string, error)
	WithTx(tx postgresrepo.DBTX) *postgresrepo.FileRepository
}

// BlockStore defines the block listing operations used by FileService.
type BlockStore interface {
	ListFileBlockHashes(ctx context.Context, fileID string) ([]string, error)
	ListFileBlocks(ctx context.Context, fileID string) ([]*domain.Block, error)
}

// PermissionStore defines the permission verification operations used by FileService.
type PermissionStore interface {
	CheckUserPermission(ctx context.Context, fileID, userEmail string, requiredRoles []string) (bool, error)
	FindCollaboratorUserIDs(ctx context.Context, fileID string) ([]string, error)
	WithTx(tx postgresrepo.DBTX) *postgresrepo.PermissionRepository
}

// FileService orchestrates file-level operations: rename, move, delete, and
// download. It validates permissions before mutating metadata and handles
// garbage collection of orphaned S3 blocks after recursive deletes.
type FileService struct {
	db       *sql.DB
	users    UserStore
	files    FileStore
	blocks   BlockStore
	perms    PermissionStore
	storage  domain.StorageProvider
	journal  *postgresrepo.JournalRepository
	notifier wsSync.Notifier
	log      *slog.Logger
}

// NewFileService wires the service with all the repositories it needs.
func NewFileService(
	db *sql.DB,
	users UserStore,
	files FileStore,
	blocks BlockStore,
	perms PermissionStore,
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

func (s *FileService) recordAndNotify(ctx context.Context, userID, fileID, action string, file *domain.File, preResolvedRecipients ...string) {
	if s.journal == nil && s.notifier == nil {
		return
	}

	// 1. Resolve all recipients (actor + pre-resolved + collaborators & owners via DB)
	recipientSet := make(map[string]struct{})
	if userID != "" {
		recipientSet[userID] = struct{}{}
	}
	for _, r := range preResolvedRecipients {
		if r != "" {
			recipientSet[r] = struct{}{}
		}
	}

	if len(preResolvedRecipients) == 0 && s.perms != nil && fileID != "" {
		if collabs, err := s.perms.FindCollaboratorUserIDs(ctx, fileID); err == nil {
			for _, c := range collabs {
				if c != "" {
					recipientSet[c] = struct{}{}
				}
			}
		} else {
			s.log.Warn("failed to resolve collaborators for sync fanout", "file_id", fileID, "err", err)
		}
	}

	if len(recipientSet) == 0 {
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

	// 2. Prepare journal entries for batch insert
	entries := make([]*domain.JournalEntry, 0, len(recipientSet))
	for rUID := range recipientSet {
		entries = append(entries, &domain.JournalEntry{
			UserID:      rUID,
			FileID:      fileID,
			Action:      action,
			ParentID:    parentID,
			Name:        name,
			IsDirectory: isDir,
			SizeBytes:   sizeBytes,
			MimeType:    mimeType,
			Status:      status,
		})
	}

	if s.journal != nil {
		cursors, err := s.journal.BatchRecord(ctx, entries)
		if err != nil {
			s.log.Error("failed to record journal entries", "file_id", fileID, "action", action, "err", err)
		} else if s.notifier != nil {
			for i, e := range entries {
				cursor := int64(0)
				if i < len(cursors) {
					cursor = cursors[i]
				}
				s.notifier.NotifyUser(e.UserID, wsSync.NotificationEvent{
					Type: wsSync.EventSyncDelta,
					Payload: map[string]any{
						"cursor":  cursor,
						"file_id": fileID,
						"action":  action,
					},
				})
			}
		}
	} else if s.notifier != nil {
		for rUID := range recipientSet {
			s.notifier.NotifyUser(rUID, wsSync.NotificationEvent{
				Type: wsSync.EventSyncDelta,
				Payload: map[string]any{
					"cursor":  0,
					"file_id": fileID,
					"action":  action,
				},
			})
		}
	}
}

// RenameMoveRequest is the body of PATCH /api/files/{id}. Snake_case matches
// the backend's JSON decoder.
type RenameMoveRequest struct {
	Name      string  `json:"name"`
	ParentID  *string `json:"parent_id"` // nil or empty-string means root when hasParent is true
	hasParent bool    // true if "parent_id" was explicitly specified in the JSON body
}

// UnmarshalJSON detects whether "parent_id" was explicitly supplied in JSON,
// distinguishing between rename-only (omitted) and move-to-root (null or "").
func (r *RenameMoveRequest) UnmarshalJSON(data []byte) error {
	type Alias RenameMoveRequest
	aux := struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if _, ok := raw["parent_id"]; ok {
		r.hasParent = true
	}
	return nil
}

// GetFile fetches a single file or folder metadata record by its unique ID.
// Note: This is an internal lookup primitive that bypasses permission checks.
// Callers must use AuthoriseRead or AuthoriseWrite before exposing data to clients.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - id: Primary key UUID of the target file or directory.
//
// Returns:
//   - *domain.File: Populated file domain model.
//   - error: Nil on success, or domain.ErrFileNotFound if the record does not exist.
func (s *FileService) GetFile(ctx context.Context, id string) (*domain.File, error) {
	return s.files.GetByID(ctx, id)
}

// GetResolvedPermission traverses the directory tree upward to determine the effective ACL role
// granted to a user on a given file or folder (evaluating direct grants, parent inheritance, and ownership).
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - fileID: Target file or folder UUID.
//   - userEmail: Normalized email address of the user whose permissions are evaluated.
//
// Returns:
//   - string: Effective role constant (domain.RoleOwner, domain.RoleEditor, or domain.RoleViewer).
//   - error: Nil on success, or domain.ErrPermissionNotFound if no access has been granted.
func (s *FileService) GetResolvedPermission(ctx context.Context, fileID string, userEmail string) (string, error) {
	return s.files.GetResolvedPermission(ctx, fileID, userEmail)
}

// RenameMove changes a file/folder's display name and/or relocates it to a new parent folder.
//
// Architecture & Business Rules:
//  1. Permissions: Validates that the requesting user holds OWNER or EDITOR permissions.
//  2. Golden Rule Fallback: If a non-owner (or EDITOR moving a folder) attempts a physical move
//     on a shared file, the system creates a personalized shortcut in the user's destination
//     rather than disrupting the canonical hierarchy for collaborators.
//  3. Cycle Rejection: Disallows moving a directory into itself or any of its descendants ($O(1)$ prefix check).
//  4. Materialized Path Updates: Relocating a folder atomically updates the path prefix of all
//     descendants via a single substring replacement SQL query without recursive locks.
//  5. Transactional Outbox: Commits the metadata mutation and emits a monotonic journal event
//     with real-time WebSocket notification to all active collaborators.
//
// Parameters:
//   - ctx: Context carrying request deadlines and cancellation signals.
//   - userID: Authenticated user ID executing the request.
//   - fileID: Target file or directory UUID.
//   - req: RenameMoveRequest specifying new name and/or parent folder target.
//
// Returns:
//   - *domain.File: The modified file entity (or newly created shortcut in Golden Rule fallback).
//   - error: Nil on success, or validation/authorization/conflict error on failure.
func (s *FileService) RenameMove(ctx context.Context, userID, fileID string, req RenameMoveRequest) (*domain.File, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	// 1. Build the update target. We load the existing row first so we have a
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

	// Determine whether a move was requested (explicit parent_id present in request)
	var isMove bool
	var newParentTarget *string
	if req.hasParent || req.ParentID != nil {
		if req.ParentID != nil && *req.ParentID != "" && *req.ParentID != "root" {
			newParentTarget = req.ParentID
		} else {
			newParentTarget = nil
		}

		if (existing.ParentID == nil && newParentTarget != nil) ||
			(existing.ParentID != nil && newParentTarget == nil) ||
			(existing.ParentID != nil && newParentTarget != nil && *existing.ParentID != *newParentTarget) {
			isMove = true
		}
	}

	if isMove {
		if role == domain.RoleViewer || (role == domain.RoleEditor && existing.IsDirectory) {
			s.log.Info("golden rule fallback: creating shortcut instead of physical move", "file_id", fileID, "user_id", userID, "role", role)
			shortcut, err := s.CreateShortcut(ctx, userID, CreateShortcutRequest{
				FileID:   fileID,
				ParentID: newParentTarget,
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
	if isMove {
		updated.ParentID = newParentTarget
	}

	// 4. Cycle guard: if parent_id is changing, the new parent must not be the
	//    file itself nor any of its own descendants (O(1) materialized path check).
	if isMove && existing.IsDirectory {
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

	// Collision guard: ensure no active item with the target name exists in destination
	clash, err := s.files.GetFileByNameAndParent(ctx, existing.UserID, updated.Name, updated.ParentID)
	if err == nil && clash != nil && clash.ID != existing.ID {
		return nil, fmt.Errorf("an item named %q already exists in the destination folder", updated.Name)
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

// SoftDelete moves a file, directory, or shortcut (and all its descendants) to the Trash
// by stamping `deleted_at = CURRENT_TIMESTAMP`. Physical storage blocks in S3 remain intact.
//
// Authorization:
//   - If the caller is the direct owner (file.UserID == userID), soft delete is granted immediately.
//   - If the file is shared, the caller must hold OWNER or EDITOR permissions.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID requesting the soft delete.
//   - fileID: Target file or directory UUID.
//
// Returns:
//   - *DeleteResult: Operation outcome payload.
//   - error: Nil on success, or authorization/not-found error.
func (s *FileService) SoftDelete(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	file, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}

	// If requesting user does not own this record, check shared permissions
	if file.UserID != userID {
		user, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("resolve user: %w", err)
		}

		allowed, err := s.perms.CheckUserPermission(ctx, fileID, user.Email,
			[]string{domain.RoleOwner, domain.RoleEditor})
		if err != nil {
			return nil, fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			return nil, fmt.Errorf("access denied: %s", user.Email)
		}
	}

	if err := s.files.SoftDelete(ctx, fileID, file.UserID); err != nil {
		return nil, fmt.Errorf("soft delete: %w", err)
	}

	s.log.Info("file soft deleted", "file_id", fileID, "user_id", userID)
	s.recordAndNotify(ctx, userID, fileID, domain.ActionFileTrashed, nil)

	return &DeleteResult{
		Status:  "success",
		Message: "file moved to trash",
	}, nil
}

// Restore recovers a soft-deleted file, folder, or shortcut (and all its descendants)
// from the Trash by resetting `deleted_at = NULL`.
//
// Authorization:
//   - If the caller owns the file, restore is granted immediately.
//   - For shared files, the caller must hold OWNER or EDITOR permissions.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID executing the restoration.
//   - fileID: Target file or folder UUID to restore.
//
// Returns:
//   - *DeleteResult: Confirmation status payload.
//   - error: Nil on success, or domain.ErrFileNotFound / permission error.
func (s *FileService) Restore(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	file, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}

	if file.UserID != userID {
		user, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("resolve user: %w", err)
		}

		allowed, err := s.perms.CheckUserPermission(ctx, fileID, user.Email,
			[]string{domain.RoleOwner, domain.RoleEditor})
		if err != nil {
			return nil, fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			return nil, fmt.Errorf("access denied: %s", user.Email)
		}
	}

	if err := s.files.Restore(ctx, fileID, file.UserID); err != nil {
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

// ListTrash returns all active soft-deleted items (files, folders, shortcuts) owned by userID.
//
// Parameters:
//   - ctx: Context carrying request tracing.
//   - userID: Authenticated user ID whose trash contents are retrieved.
//
// Returns:
//   - []*domain.File: Slices of soft-deleted file entities ordered by deleted_at DESC.
//   - error: Nil on success, or database query error.
func (s *FileService) ListTrash(ctx context.Context, userID string) ([]*domain.File, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	return s.files.ListTrash(ctx, userID)
}

// Delete is a semantic alias for SoftDelete supporting standard HTTP DELETE routes.
func (s *FileService) Delete(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	return s.SoftDelete(ctx, userID, fileID)
}

// PermanentDelete permanently purges a file or folder and all descendants from PostgreSQL
// and schedules physical S3 block garbage collection for any content hashes that are no longer
// referenced by any other file version or user in the system.
//
// Authorization & Safety:
//   - Requires direct ownership or explicit OWNER permission.
//   - Emits asynchronous outbox events before triggering non-blocking background S3 block deletions.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID executing the purge.
//   - fileID: Target file or directory UUID to be destroyed.
//
// Returns:
//   - *DeleteResult: Count of purged file rows and number of unreferenced blocks scheduled for GC.
//   - error: Nil on success, or authorization/database error on failure.
func (s *FileService) PermanentDelete(ctx context.Context, userID, fileID string) (*DeleteResult, error) {
	if userID == "" || fileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	file, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}

	if file.UserID != userID {
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
	}

	var preResolvedRecipients []string
	if s.perms != nil {
		if collabs, err := s.perms.FindCollaboratorUserIDs(ctx, fileID); err == nil {
			preResolvedRecipients = collabs
		}
	}

	deletedCount, orphanHashes, err := s.files.DeleteRecursive(ctx, fileID, file.UserID)
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
	s.recordAndNotify(ctx, userID, fileID, domain.ActionFileDeleted, nil, preResolvedRecipients...)

	return &DeleteResult{
		Status:       "success",
		Message:      "file permanently deleted",
		DeletedCount: deletedCount,
		GCBlocks:     len(orphanHashes),
	}, nil
}

// CreateFolder creates a directory under parentID or idempotently returns an existing
// folder with the same name and parent for the user.
//
// Concurrency & Atomicity:
//   - Executes inside an explicit PostgreSQL transaction (RunInTx).
//   - Employs a double-checked locking idiom (checks before and inside the transaction)
//     to prevent duplicate directory creation races under concurrent requests.
//   - Automatically creates an OWNER permission row for the creator.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID creating the folder.
//   - name: Folder display name.
//   - parentID: Optional pointer to the parent folder UUID (nil for drive root).
//
// Returns:
//   - *domain.File: The newly created or existing directory entity.
//   - bool: wasNew flag (true if a new row was inserted, false if reused).
//   - error: Nil on success, or validation/database error.
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

// ListDirectory returns the immediate children of parentID (or the user's root folder when parentID is nil).
// Folders are returned first, then files, sorted alphabetically.
//
// Features & Concurrency Guarantees:
//   - Shortcut Transparency: If parentID points to a shortcut, transparently lists the canonical target folder.
//   - Authorization: Verifies the caller holds VIEWER, EDITOR, or OWNER rights on the folder.
//   - Role Inheritance: Populates the effective ACL role for all returned items.
//   - Thread-Safe Memory Isolation: Shallow-clones all returned *domain.File pointers before mutating
//     their Role field, guaranteeing zero data races when mock stores or concurrent requests share memory.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID querying the directory.
//   - parentID: Optional parent folder UUID (nil queries the root directory).
//
// Returns:
//   - []*domain.File: Isolated slice of child file/folder models.
//   - error: Nil on success, or domain.ErrPermissionDenied / database error.
func (s *FileService) ListDirectory(ctx context.Context, userID string, parentID *string) ([]*domain.File, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	var targetParentID = parentID
	if parentID != nil {
		if err := s.AuthoriseRead(ctx, userID, *parentID); err != nil {
			return nil, err
		}
		parent, err := s.files.GetByID(ctx, *parentID)
		if err == nil {
			if parent.TargetID != nil && *parent.TargetID != "" {
				targetParentID = parent.TargetID
			} else if parent.ShortcutTargetID != nil && *parent.ShortcutTargetID != "" {
				targetParentID = parent.ShortcutTargetID
			}
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
			if err != nil && errors.Is(err, domain.ErrPermissionNotFound) {
				role = domain.RoleViewer
				err = nil
			}
			if err == nil {
				cloned := make([]*domain.File, len(items))
				for i, item := range items {
					c := *item
					c.Role = role
					cloned[i] = &c
				}
				items = cloned
			}
		}
	}
	return items, nil
}

// ListSharedFiles returns all files and folders that have been directly shared with the user
// or inherited through shared parent directories.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID querying their shared file inventory.
//
// Returns:
//   - []*domain.File: Slice of shared file records populated with their inherited roles.
//   - error: Nil on success, or user resolution / repository query error.
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

// GetUserStorage calculates accurate aggregate storage consumption and category breakdown
// (images, videos, documents, audio, archives, and others) for the specified user account.
//
// Parameters:
//   - ctx: Context carrying request tracing.
//   - userID: Authenticated user ID whose quota and usage are queried.
//
// Returns:
//   - *domain.UserStorageUsage: DTO containing TotalBytesUsed, TotalFiles, QuotaBytes, and CategoryBytes.
//   - error: Nil on success, or database aggregation error.
func (s *FileService) GetUserStorage(ctx context.Context, userID string) (*domain.UserStorageUsage, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	return s.files.GetUserStorageUsage(ctx, userID)
}

// GetDownloadInfo returns the ordered physical SHA-256 block hashes and the File metadata
// needed by transport handlers to stream a full file download.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - fileID: Target file UUID.
//
// Returns:
//   - *domain.File: File metadata model.
//   - []string: Sequentially ordered block content SHA-256 hashes.
//   - error: Nil on success, or domain.ErrFileNotFound / validation error.
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

// GetDownloadInfoWithBlocks returns the ordered domain.Block entities (with individual byte lengths)
// and File metadata needed for dynamic byte-range streaming (RFC 7233).
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - fileID: Target file UUID.
//
// Returns:
//   - *domain.File: File metadata model.
//   - []*domain.Block: Ordered slice of Block models with sequence number, hash, and byte length.
//   - error: Nil on success, or domain.ErrFileNotFound / database error.
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

// garbageCollectBlocks performs best-effort asynchronous deletion of orphaned physical blocks from S3/storage.
// An orphan block is one whose reference count in the file_blocks join table has dropped to zero.
//
// Parameters:
//   - ctx: Detached context (typically context.Background()).
//   - hashes: List of SHA-256 block hashes to delete from the blocks/ namespace.
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

// BulkSoftDelete moves an arbitrary array of files and folders to the Trash in a single atomic SQL transaction.
// For every item in req.IDs, verifies that the user holds OWNER or EDITOR permissions.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID executing the batch operation.
//   - req: BulkDeleteRequest containing slice of target UUIDs.
//
// Returns:
//   - error: Nil on success, or validation/authorization/DB error.
func (s *FileService) BulkSoftDelete(ctx context.Context, userID string, req domain.BulkDeleteRequest) error {
	if userID == "" || len(req.IDs) == 0 {
		return nil
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}
	for _, id := range req.IDs {
		file, err := s.files.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("file not found: %w", err)
		}
		if file.UserID != userID {
			allowed, err := s.perms.CheckUserPermission(ctx, id, user.Email,
				[]string{domain.RoleOwner, domain.RoleEditor})
			if err != nil {
				return fmt.Errorf("permission check: %w", err)
			}
			if !allowed {
				return fmt.Errorf("access denied: %s", user.Email)
			}
		}
	}
	return s.files.BulkSoftDelete(ctx, req.IDs, userID)
}

// BulkRestore recovers multiple soft-deleted files and folders from the Trash in an atomic transaction.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID executing the batch restore.
//   - req: BulkRestoreRequest containing array of target UUIDs.
//
// Returns:
//   - error: Nil on success, or authorization/database error.
func (s *FileService) BulkRestore(ctx context.Context, userID string, req domain.BulkRestoreRequest) error {
	if userID == "" || len(req.IDs) == 0 {
		return nil
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}
	for _, id := range req.IDs {
		file, err := s.files.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("file not found: %w", err)
		}
		if file.UserID != userID {
			allowed, err := s.perms.CheckUserPermission(ctx, id, user.Email,
				[]string{domain.RoleOwner, domain.RoleEditor})
			if err != nil {
				return fmt.Errorf("permission check: %w", err)
			}
			if !allowed {
				return fmt.Errorf("access denied: %s", user.Email)
			}
		}
	}
	return s.files.BulkRestore(ctx, req.IDs, userID)
}

// BulkMove updates the parent folder pointer for multiple files/directories in a single transaction.
//
// Parameters:
//   - ctx: Context carrying cancellation signals.
//   - userID: Authenticated user ID owning the items.
//   - req: BulkMoveRequest containing list of file IDs and destination parentID.
//
// Returns:
//   - error: Nil on success, or database update error.
func (s *FileService) BulkMove(ctx context.Context, userID string, req domain.BulkMoveRequest) error {
	if userID == "" || len(req.IDs) == 0 {
		return nil
	}
	return s.files.BulkMove(ctx, req.IDs, req.ParentID, userID)
}

// BulkHardDelete permanently removes multiple files/directories and cascades through their subtrees,
// calculating orphaned physical CAS blocks and triggering background garbage collection in storage.
//
// Parameters:
//   - ctx: Context carrying cancellation signals.
//   - userID: Authenticated user ID executing the purge.
//   - req: BulkDeleteRequest containing slice of target UUIDs.
//
// Returns:
//   - int64: Total count of database file rows purged.
//   - error: Nil on success, or database error.
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

// AuthoriseRead verifies that the requesting user holds VIEWER, EDITOR, or OWNER rights
// on fileID.
//
// Resolution Rules:
//  1. Recursive Shortcut Dereferencing: If fileID is a shortcut, recursively checks permissions
//     on the target file or folder entity.
//  2. Direct Ownership: If file.UserID == userID, access is granted immediately.
//  3. Status Checks: Denies read access if non-owners attempt to access soft-deleted or quarantined files.
//  4. Materialized ACL Traversal: Traverses up the directory materialized path to check inherited permissions.
//  5. Public Link Shortcuts: Grants access if the user has created or accessed a valid public link shortcut.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID requesting read access.
//   - fileID: Target file, directory, or shortcut UUID.
//
// Returns:
//   - error: Nil if authorized, or domain.ErrPermissionDenied / access denied error.
func (s *FileService) AuthoriseRead(ctx context.Context, userID, fileID string) error {
	file, err := s.files.GetByID(ctx, fileID)
	if err == nil {
		// If file is a shortcut, recursively authorize read access on the underlying target
		if file.TargetID != nil && *file.TargetID != "" && *file.TargetID != fileID {
			return s.AuthoriseRead(ctx, userID, *file.TargetID)
		}
		if file.ShortcutTargetID != nil && *file.ShortcutTargetID != "" && *file.ShortcutTargetID != fileID {
			return s.AuthoriseRead(ctx, userID, *file.ShortcutTargetID)
		}
		// 1. If file is owned directly by the user, bypass check and grant access.
		if file.UserID == userID {
			return nil
		}
		// If a non-owner tries to access a soft-deleted or quarantined target file, deny immediately.
		if file.DeletedAt != nil || file.Status == "TRASHED" || file.Status == "QUARANTINED" {
			return fmt.Errorf("access denied: file is unavailable")
		}
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve user: %w", err)
	}

	role, err := s.files.GetResolvedPermission(ctx, fileID, user.Email)
	if err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			if linkChecker, ok := s.files.(interface {
				HasPublicLinkOrShortcutAccess(ctx context.Context, userID, fileID string) (bool, error)
			}); ok {
				if allowed, lErr := linkChecker.HasPublicLinkOrShortcutAccess(ctx, userID, fileID); lErr == nil && allowed {
					return nil
				}
			}
			return fmt.Errorf("access denied: %s", user.Email)
		}
		return fmt.Errorf("permission check: %w", err)
	}

	if role == domain.RoleViewer || role == domain.RoleEditor || role == domain.RoleOwner {
		return nil
	}
	return fmt.Errorf("access denied: %s", user.Email)
}

// AuthoriseWrite verifies that the requesting user holds EDITOR or OWNER permissions on fileID
// (directly or inherited via parent directories).
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID requesting write access.
//   - fileID: Target file or directory UUID.
//
// Returns:
//   - error: Nil if authorized, or access denied error.
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

// SanitiseFilename cleans a filename by replacing path separators ('/' and '\') with underscores,
// mitigating HTTP header injection and path traversal vulnerabilities in Content-Disposition headers.
//
// Parameters:
//   - name: Raw untrusted filename string.
//
// Returns:
//   - string: Sanitized filename safe for HTTP headers and filesystem operations.
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

// CreateShortcutRequest defines the payload for creating a virtual pointer to a target file or folder.
type CreateShortcutRequest struct {
	// FileID is the unique identifier of the target file or directory being referenced.
	FileID string `json:"file_id"`
	// ParentID is the destination folder where the shortcut will reside (nil for root).
	ParentID *string `json:"parent_id"`
}

// CreateShortcut generates a lightweight virtual pointer entity pointing to an existing file or directory.
//
// Business Logic:
//   - Verification: Validates that the user holds at least VIEWER rights on the target file.
//   - Pointer Flattening: If the target is already a shortcut, dereferences it to point directly
//     to the canonical target file, preventing unbounded pointer chaining.
//   - Zero Byte Consumption: Shortcuts have size_bytes = 0 and consume no user storage quota.
//
// Parameters:
//   - ctx: Context carrying cancellation signals and request tracing.
//   - userID: Authenticated user ID creating the shortcut.
//   - req: CreateShortcutRequest specifying target file ID and destination parent folder.
//
// Returns:
//   - *domain.File: Populated shortcut file domain entity.
//   - error: Nil on success, or validation/authorization error.
func (s *FileService) CreateShortcut(ctx context.Context, userID string, req CreateShortcutRequest) (*domain.File, error) {
	if userID == "" || req.FileID == "" {
		return nil, fmt.Errorf("user_id and file_id are required")
	}

	// 1. Get target file metadata first
	target, err := s.files.GetByID(ctx, req.FileID)
	if err != nil {
		return nil, fmt.Errorf("get target file: %w", err)
	}

	// 2. Authorize: If not owned by requesting user, must have at least VIEWER permission
	if target.UserID != userID {
		user, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("resolve user: %w", err)
		}

		allowed, err := s.perms.CheckUserPermission(ctx, req.FileID, user.Email,
			[]string{domain.RoleOwner, domain.RoleEditor, domain.RoleViewer})
		if err != nil {
			return nil, fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			return nil, fmt.Errorf("access denied: %s", user.Email)
		}
	}

	// 3. If target is already a shortcut, use the target's target_id instead
	var realTargetID string
	if target.TargetID != nil && *target.TargetID != "" {
		realTargetID = *target.TargetID
	} else {
		realTargetID = target.ID
	}

	// 4. Create new File entity
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

	// 5. Persist
	if err := s.files.Create(ctx, shortcut); err != nil {
		return nil, fmt.Errorf("create shortcut: %w", err)
	}

	s.log.Info("shortcut created", "shortcut_id", shortcut.ID, "user_id", userID, "target_id", realTargetID)

	return shortcut, nil
}

// GetFileInfo fetches a file's metadata entity by its primary key UUID.
//
// Parameters:
//   - ctx: Context carrying cancellation signals.
//   - fileID: Target file UUID.
//
// Returns:
//   - *domain.File: File domain model.
//   - error: Nil on success, or domain.ErrFileNotFound if absent.
func (s *FileService) GetFileInfo(ctx context.Context, fileID string) (*domain.File, error) {
	return s.files.GetByID(ctx, fileID)
}

// BlockSize defines the legacy fixed 4 MiB block boundary constant (4,194,304 bytes).
const BlockSize int64 = 4194304

// CalculateRangeBlockOffset computes the 0-based starting block index and internal byte offset
// for legacy fixed 4 MiB blocks given a global file seek position.
//
// Parameters:
//   - startByte: Absolute seek offset in bytes.
//
// Returns:
//   - startBlockIndex: 0-based index of the first block containing the range.
//   - blockOffset: Intra-block byte offset where streaming should begin.
func (s *FileService) CalculateRangeBlockOffset(startByte int64) (startBlockIndex int64, blockOffset int64) {
	startBlockIndex = startByte / BlockSize
	blockOffset = startByte % BlockSize
	return startBlockIndex, blockOffset
}

// CalculateRangeBlockOffset computes startBlockIndex and blockOffset from startByte using fixed 4 MiB blocks.
func CalculateRangeBlockOffset(startByte int64) (int64, int64) {
	return startByte / BlockSize, startByte % BlockSize
}

// CalculateDynamicRangeBlockOffset dynamically calculates the starting block index and intra-block offset
// given arbitrary variable-sized FastCDC content-defined blocks.
//
// Complexity & Algorithmic Design:
//   - Iterates through the ordered slice of variable-sized blocks, accumulating block sizes.
//   - Determines the exact boundary block and offset in $O(N)$ where $N$ is block count (typically < 100).
//
// Parameters:
//   - blocks: Ordered slice of Block models representing the sequential file stream.
//   - startByte: Absolute 0-based byte offset requested in the HTTP Range header.
//
// Returns:
//   - startBlockIndex: 0-based index of the first block containing startByte.
//   - blockOffset: Intra-block offset in bytes where reading begins.
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

// CalculateDynamicRangeBlockOffset delegates to the standalone CalculateDynamicRangeBlockOffset function.
func (s *FileService) CalculateDynamicRangeBlockOffset(blocks []*domain.Block, startByte int64) (startBlockIndex int, blockOffset int64) {
	return CalculateDynamicRangeBlockOffset(blocks, startByte)
}

// CreateShortcutFromPublicLink creates a shortcut directly for a user from an authorized public share token,
// bypassing standard ACL checks because access has been cryptographically validated via the public share token.
//
// Parameters:
//   - ctx: Context carrying cancellation signals.
//   - userID: Authenticated user ID adding the shared file to their drive.
//   - targetID: Canonical target file or folder UUID.
//   - parentID: Optional destination folder in the user's drive (nil for root).
//
// Returns:
//   - *domain.File: The newly created shortcut entity.
//   - error: Nil on success, or database error.
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
