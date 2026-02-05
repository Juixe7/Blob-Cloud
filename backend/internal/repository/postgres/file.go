package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"go-drive-clone/internal/domain"
)

// FileRepository is the Postgres implementation of domain.FileRepository.
type FileRepository struct {
	db DBTX
}

// NewFileRepository constructs a FileRepository bound to the given pool.
func NewFileRepository(db *sql.DB) *FileRepository {
	return &FileRepository{db: db}
}

// WithTx returns a copy of the repository bound to tx.
func (r *FileRepository) WithTx(tx DBTX) *FileRepository {
	return &FileRepository{db: tx}
}

// Create inserts file and reads back the DB-generated id/timestamps.
func (r *FileRepository) Create(ctx context.Context, file *domain.File) error {
	const q = `
		INSERT INTO files (user_id, name, parent_id, is_directory, size_bytes, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, q,
		file.UserID, file.Name, file.ParentID, file.IsDirectory, file.SizeBytes, file.TargetID, file.MimeType, file.ShortcutTargetID, file.IsEncrypted, file.EncryptionSalt, file.Status)
	if err := row.Scan(&file.ID, &file.CreatedAt, &file.UpdatedAt); err != nil {
		return fmt.Errorf("insert file: %w", err)
	}
	return nil
}

// GetByID returns the file with the given id.
func (r *FileRepository) GetByID(ctx context.Context, id string) (*domain.File, error) {
	const q = `
		SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
		FROM files
		WHERE id = $1
	`
	var f domain.File
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType, &f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("file by id %q: %w", id, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query file by id: %w", err)
	}
	return &f, nil
}

// GetFolderByNameAndParent checks if a active (non-deleted) folder with exact name and parentID exists for userID.
func (r *FileRepository) GetFolderByNameAndParent(ctx context.Context, userID, name string, parentID *string) (*domain.File, error) {
	var (
		row *sql.Row
		f   domain.File
	)
	if parentID == nil {
		const q = `
			SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
			FROM files
			WHERE user_id = $1 AND name = $2 AND parent_id IS NULL AND is_directory = TRUE AND deleted_at IS NULL
			LIMIT 1
		`
		row = r.db.QueryRowContext(ctx, q, userID, name)
	} else {
		const q = `
			SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
			FROM files
			WHERE user_id = $1 AND name = $2 AND parent_id = $3 AND is_directory = TRUE AND deleted_at IS NULL
			LIMIT 1
		`
		row = r.db.QueryRowContext(ctx, q, userID, name, *parentID)
	}

	err := row.Scan(
		&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType, &f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, sql.ErrNoRows
	case err != nil:
		return nil, fmt.Errorf("query folder by name and parent: %w", err)
	}
	return &f, nil
}

// ListDirectory returns the immediate children of parentID for a user. A nil
// parentID lists the user's top-level entries (parent_id IS NULL). If parentID is provided,
// returns children even if the parent folder itself is soft-deleted.
func (r *FileRepository) ListDirectory(ctx context.Context, userID string, parentID *string) ([]*domain.File, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if parentID == nil {
		const q = `
			SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
			FROM files
			WHERE user_id = $1 AND parent_id IS NULL AND deleted_at IS NULL
			ORDER BY is_directory DESC, name ASC
		`
		rows, err = r.db.QueryContext(ctx, q, userID)
	} else {
		const q = `
			SELECT f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
			FROM files f
			WHERE f.parent_id = $1
			  AND (
			    (SELECT deleted_at FROM files WHERE id = $1) IS NOT NULL
			    OR f.deleted_at IS NULL
			  )
			ORDER BY f.is_directory DESC, f.name ASC
		`
		rows, err = r.db.QueryContext(ctx, q, *parentID)
	}
	if err != nil {
		return nil, fmt.Errorf("query directory: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var f domain.File
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType, &f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// ListTrash returns only top-level deleted files/directories for a user with aggregated size, item count, and original location.
func (r *FileRepository) ListTrash(ctx context.Context, userID string) ([]*domain.File, error) {
	const q = `
		WITH RECURSIVE deleted_roots AS (
		    SELECT f.*
		    FROM files f
		    WHERE f.deleted_at IS NOT NULL
		      AND f.user_id = $1
		      AND (
		          f.parent_id IS NULL 
		          OR (SELECT deleted_at FROM files WHERE id = f.parent_id) IS NULL
		      )
		),
		subtree_stats AS (
		    SELECT dr.id AS root_id, f.id AS child_id, f.size_bytes
		    FROM deleted_roots dr
		    INNER JOIN files f ON f.id = dr.id
		    
		    UNION ALL
		    
		    SELECT ss.root_id, f.id, f.size_bytes
		    FROM subtree_stats ss
		    INNER JOIN files f ON f.parent_id = ss.child_id
		),
		aggregated_stats AS (
		    SELECT root_id, 
		           COALESCE(SUM(size_bytes), 0) AS total_size,
		           COUNT(child_id) - 1 AS nested_item_count
		    FROM subtree_stats
		    GROUP BY root_id
		)
		SELECT dr.id, dr.user_id, dr.name, dr.parent_id, dr.is_directory, dr.size_bytes, dr.created_at, dr.updated_at, dr.deleted_at,
		       COALESCE(ast.total_size, 0) AS aggregate_size,
		       COALESCE(ast.nested_item_count, 0) AS item_count,
		       COALESCE((SELECT name FROM files WHERE id = dr.parent_id), 'My Drive') AS original_location,
		       dr.target_id, dr.mime_type, dr.shortcut_target_id, dr.is_encrypted, dr.encryption_salt, dr.tags, dr.summary
		FROM deleted_roots dr
		LEFT JOIN aggregated_stats ast ON dr.id = ast.root_id
		ORDER BY dr.deleted_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("query trash: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var (
			f                domain.File
			aggregateSize    int64
			itemCount        int64
			originalLocation string
		)
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt,
			&aggregateSize, &itemCount, &originalLocation,
			&f.TargetID, &f.MimeType, &f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary,
		); err != nil {
			return nil, fmt.Errorf("scan trash file row: %w", err)
		}
		f.AggregateSize = &aggregateSize
		f.ItemCount = &itemCount
		f.OriginalLocation = &originalLocation
		out = append(out, &f)
	}
	return out, rows.Err()
}

// GetDescendants recursively retrieves all non-deleted files and subfolders nested under rootID.
func (r *FileRepository) GetDescendants(ctx context.Context, rootID string) ([]*domain.File, error) {
	const q = `
		WITH RECURSIVE subtree AS (
			SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
			FROM files
			WHERE parent_id = $1 AND deleted_at IS NULL
			UNION ALL
			SELECT f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
			FROM files f
			INNER JOIN subtree s ON f.parent_id = s.id
			WHERE f.deleted_at IS NULL
		)
		SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
		FROM subtree
	`
	rows, err := r.db.QueryContext(ctx, q, rootID)
	if err != nil {
		return nil, fmt.Errorf("query descendants: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var f domain.File
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType, &f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status); err != nil {
			return nil, fmt.Errorf("scan descendant file row: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// GetSubtreeForItems recursively finds all nested files inside multiple file/folder roots and calculates their relative path.
func (r *FileRepository) GetSubtreeForItems(ctx context.Context, ids []string, userID string) ([]*domain.ZippableItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Resolve target IDs for shortcuts
	resolvedIDs := make([]string, len(ids))
	for i, id := range ids {
		f, err := r.GetByID(ctx, id)
		if err == nil && f.TargetID != nil && *f.TargetID != "" {
			resolvedIDs[i] = *f.TargetID
		} else {
			resolvedIDs[i] = id
		}
	}

	q := `
		WITH RECURSIVE selected_hierarchy AS (
			-- 1. Base Case: Start with all selected target items (files or folders)
			SELECT f.id, f.parent_id, f.name, f.mime_type, f.target_id, 1 AS depth, 
				CASE WHEN array_length($1::uuid[], 1) > 1 AND f.parent_id IS NOT NULL THEN
					COALESCE((SELECT p.name FROM files p WHERE p.id = f.parent_id), 'Root') || '/' || f.name
				ELSE
					f.name
				END::VARCHAR(1024) AS relative_path
			FROM files f
			WHERE f.id = ANY($1::uuid[]) AND f.user_id = $2 AND f.deleted_at IS NULL
			
			UNION ALL
			
			-- 2. Recursive Step: Walk down the tree to collect nested children
			SELECT f.id, f.parent_id, f.name, f.mime_type, f.target_id, sh.depth + 1,
				CAST(sh.relative_path || '/' || f.name AS VARCHAR(1024)) AS relative_path
			FROM files f
			INNER JOIN selected_hierarchy sh ON f.parent_id = sh.id
			WHERE f.deleted_at IS NULL
		)
		-- 3. Return both directories (for empty folder headers) and files
		SELECT id, parent_id, name, mime_type, relative_path, target_id
		FROM selected_hierarchy
		ORDER BY depth ASC;
	`

	rows, err := r.db.QueryContext(ctx, q, pq.Array(resolvedIDs), userID)
	if err != nil {
		return nil, fmt.Errorf("query subtree for items: %w", err)
	}
	defer rows.Close()

	var items []*domain.ZippableItem
	for rows.Next() {
		var item domain.ZippableItem
		if err := rows.Scan(
			&item.ID,
			&item.ParentID,
			&item.Name,
			&item.MimeType,
			&item.RelativePath,
			&item.TargetID,
		); err != nil {
			return nil, fmt.Errorf("scan subtree item: %w", err)
		}
		items = append(items, &item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return items, nil
}

// SoftDelete recursively sets deleted_at = CURRENT_TIMESTAMP for rootID and all nested descendants.
func (r *FileRepository) SoftDelete(ctx context.Context, rootID string, userID string) error {
	const qOrphan = `
		WITH RECURSIVE subtree AS (
			SELECT id, user_id FROM files WHERE id = $1 AND user_id = $2
			UNION ALL
			SELECT f.id, f.user_id FROM files f JOIN subtree s ON f.parent_id = s.id
		)
		UPDATE files
		SET parent_id = NULL
		WHERE parent_id IN (SELECT id FROM subtree WHERE user_id = $2)
		  AND user_id != $2;
	`
	if _, err := r.db.ExecContext(ctx, qOrphan, rootID, userID); err != nil {
		return fmt.Errorf("soft delete orphan foreign files: %w", err)
	}

	const qDelete = `
		WITH RECURSIVE subtree AS (
			SELECT id FROM files WHERE id = $1 AND user_id = $2
			UNION ALL
			SELECT f.id FROM files f JOIN subtree s ON f.parent_id = s.id
		)
		UPDATE files
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id IN (SELECT id FROM subtree) AND user_id = $2;
	`
	if _, err := r.db.ExecContext(ctx, qDelete, rootID, userID); err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}
	return nil
}

// Restore recursively sets deleted_at = NULL for rootID and all nested descendants.
func (r *FileRepository) Restore(ctx context.Context, rootID string, userID string) error {
	const q = `
		WITH RECURSIVE subtree AS (
			SELECT id FROM files WHERE id = $1
			UNION ALL
			SELECT f.id FROM files f JOIN subtree s ON f.parent_id = s.id
		)
		UPDATE files
		SET deleted_at = NULL
		WHERE id IN (SELECT id FROM subtree);
	`
	_, err := r.db.ExecContext(ctx, q, rootID)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

// ListSharedWithUser returns all files/folders that have been shared with userEmail
// (excluding files owned by the user themselves and excluding soft-deleted files).
func (r *FileRepository) ListSharedWithUser(ctx context.Context, userEmail, userID string) ([]*domain.File, error) {
	const q = `
		SELECT f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, p.created_at, f.target_id, p.role
		FROM files f
		JOIN permissions p ON f.id = p.file_id
		WHERE p.grantee_email = $1 AND f.user_id != $2 AND f.deleted_at IS NULL
		ORDER BY p.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q, userEmail, userID)
	if err != nil {
		return nil, fmt.Errorf("query shared files: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var f domain.File
		var sharedAt time.Time
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &sharedAt, &f.TargetID, &f.Role); err != nil {
			return nil, fmt.Errorf("scan shared file row: %w", err)
		}
		f.SharedAt = &sharedAt
		out = append(out, &f)
	}
	return out, rows.Err()
}

// Update mutates a file/folder's name and/or parent_id and refreshes
// updated_at. The file.ID must already be set; on return the struct is
// repopulated with the persisted row (including a server-set updated_at).
//
// Parent semantics:
//   - nil *string   -> leave parent_id UNCHANGED
//   - non-nil ""    -> move to root (parent_id = NULL)
//   - non-nil value -> set parent_id to that value
//
// Name semantics: an empty name leaves the column unchanged; this lets the
// caller update only parent_id (a pure move) in one round trip.
func (r *FileRepository) Update(ctx context.Context, file *domain.File) error {
	// Build the SET clause dynamically from whichever fields are present, so a
	// pure move (no name) or a pure rename (no parent change) each send a
	// minimal UPDATE.
	setParts := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{file.ID}
	argIdx := 2 // $1 is the file id used in WHERE

	if file.Name != "" {
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, file.Name)
		argIdx++
	}
	if file.ParentID != nil {
		// Non-nil pointer (possibly empty string). Empty string => root.
		if *file.ParentID == "" {
			setParts = append(setParts, "parent_id = NULL")
		} else {
			setParts = append(setParts, fmt.Sprintf("parent_id = $%d", argIdx))
			args = append(args, *file.ParentID)
			argIdx++
		}
	}

	// If nothing changed besides updated_at, still touch the row so updated_at
	// advances and the client gets a consistent response. RETURNING gives us
	// the authoritative post-update values.
		q := fmt.Sprintf(`
		UPDATE files
		SET %s
		WHERE id = $1
		RETURNING id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at
	`, strings.Join(setParts, ", "))

	if err := r.db.QueryRowContext(ctx, q, args...).Scan(
		&file.ID, &file.UserID, &file.Name, &file.ParentID, &file.IsDirectory,
		&file.SizeBytes, &file.CreatedAt, &file.UpdatedAt); err != nil {
		return fmt.Errorf("update file %s: %w", file.ID, err)
	}
	return nil
}

// IsDescendant reports whether candidateID equals ancestorID or is reachable
// by walking DOWN the parent_id tree starting from ancestorID. It uses a
// recursive CTE so the full subtree is examined in a single query.
//
// Use this before reparenting a folder to reject moves that would create a
// cycle: moving /Docs into /Docs/Sub would make /Docs its own grandparent.
func (r *FileRepository) IsDescendant(ctx context.Context, candidateID, ancestorID string) (bool, error) {
	if candidateID == ancestorID {
		return true, nil
	}
	const q = `
		WITH RECURSIVE subtree AS (
			-- Anchor: the proposed new parent (the "ancestor" we descend from).
			SELECT id FROM files WHERE id = $1
			UNION ALL
			-- Recurse: every direct child of a node already in the subtree.
			SELECT f.id
			FROM files f
			JOIN subtree s ON f.parent_id = s.id
		)
		SELECT EXISTS (SELECT 1 FROM subtree WHERE id = $2) AS is_desc
	`
	var isDesc bool
	if err := r.db.QueryRowContext(ctx, q, ancestorID, candidateID).Scan(&isDesc); err != nil {
		return false, fmt.Errorf("is-descendant check (%s in %s): %w", candidateID, ancestorID, err)
	}
	return isDesc, nil
}

// DeleteRecursive removes the file/folder identified by rootID and, when it is
// a directory, every descendant. file_blocks and permissions rows are removed
// automatically by the schema's ON DELETE CASCADE; we still gather the hashes
// of blocks that became orphaned (no remaining file_blocks references) so the
// caller can delete the physical storage objects as garbage collection.
//
// The collection + delete runs as a single statement sequence. When the
// repository is bound to a caller-owned transaction (via WithTx) the whole
// operation participates in that tx; otherwise it runs against the pool.
func (r *FileRepository) DeleteRecursive(ctx context.Context, rootID string, userID string) (int64, []string, error) {
	// 1. Gather the sha256 of every block referenced by any file in the
	//    subtree being deleted. We collect the FULL set (even blocks still
	//    referenced elsewhere) and then, after the delete, filter to those
	//    that no longer have any file_blocks row — those are safe to GC.
	const collectSubtreeHashes = `
		WITH RECURSIVE subtree AS (
			SELECT id FROM files WHERE id = $1
			UNION ALL
			SELECT f.id FROM files f JOIN subtree s ON f.parent_id = s.id
		)
		SELECT DISTINCT b.sha256
		FROM file_blocks fb
		JOIN blocks b ON b.id = fb.block_id
		WHERE fb.file_id IN (SELECT id FROM subtree)
	`
	hashes := make([]string, 0)
	// Edge case: a directory with no files (only empty subfolders) has no
	// blocks at all; we must still perform the delete, so don't bail early.
	rows, err := r.db.QueryContext(ctx, collectSubtreeHashes, rootID)
	if err != nil {
		return 0, nil, fmt.Errorf("collect subtree hashes: %w", err)
	}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("scan block hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, nil, fmt.Errorf("iterate subtree hashes: %w", err)
	}
	_ = rows.Close()

	// 2. Orphan files owned by other users in the subtree before deleting
	const orphanSubtree = `
		WITH RECURSIVE subtree AS (
			SELECT id FROM files WHERE id = $1
			UNION ALL
			SELECT f.id FROM files f JOIN subtree s ON f.parent_id = s.id
		)
		UPDATE files SET parent_id = NULL 
		WHERE id IN (SELECT id FROM subtree) AND user_id != $2
	`
	if _, err := r.db.ExecContext(ctx, orphanSubtree, rootID, userID); err != nil {
		return 0, nil, fmt.Errorf("orphan subtree %s: %w", rootID, err)
	}

	// 2b. Delete the subtree. The recursive CTE + single DELETE cascades
	//     through file_blocks and permissions via the FK triggers.
	const deleteSubtree = `
		WITH RECURSIVE subtree AS (
			SELECT id FROM files WHERE id = $1
			UNION ALL
			SELECT f.id FROM files f JOIN subtree s ON f.parent_id = s.id
		)
		DELETE FROM files WHERE id IN (SELECT id FROM subtree) AND user_id = $2
	`
	res, err := r.db.ExecContext(ctx, deleteSubtree, rootID, userID)
	if err != nil {
		return 0, nil, fmt.Errorf("delete subtree %s: %w", rootID, err)
	}
	deleted, _ := res.RowsAffected()

	// 3. Of the blocks the subtree referenced, find which are now orphaned
	//    (no file anywhere still needs them). Those are GC candidates. We do
	//    this AFTER the delete so the result reflects post-delete state.
	var orphans []string
	if len(hashes) > 0 {
		orphans, err = r.findOrphanedHashes(ctx, hashes)
		if err != nil {
			// Non-fatal: the metadata delete already succeeded. Surface the
			// count but return the GC error so the caller can log it.
			return deleted, nil, err
		}
	}
	return deleted, orphans, nil
}

// GetUserStorageUsage calculates real total byte usage and category breakdown for a user.
func (r *FileRepository) GetUserStorageUsage(ctx context.Context, userID string) (*domain.UserStorageUsage, error) {
	const q = `
		SELECT 
			COALESCE(SUM(size_bytes), 0) AS total_used,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('png','jpg','jpeg','webp','gif','svg','bmp') THEN size_bytes ELSE 0 END), 0) AS images,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('pdf','doc','docx','txt','rtf','xls','xlsx','csv') THEN size_bytes ELSE 0 END), 0) AS documents,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('mp3','wav','flac','mp4','webm','mov','mkv','avi') THEN size_bytes ELSE 0 END), 0) AS media,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('js','ts','jsx','tsx','go','py','json','html','css','zip','tar','gz') THEN size_bytes ELSE 0 END), 0) AS code
		FROM files
		WHERE user_id = $1 AND is_directory = FALSE
	`
	var usage domain.UserStorageUsage
	var images, docs, media, code int64

	err := r.db.QueryRowContext(ctx, q, userID).Scan(
		&usage.TotalUsedBytes,
		&images,
		&docs,
		&media,
		&code,
	)
	if err != nil {
		return nil, fmt.Errorf("calculate storage usage: %w", err)
	}

	usage.StorageLimit = 15 * 1073741824 // 15 GB
	usage.Categories.Images = images
	usage.Categories.Documents = docs
	usage.Categories.Media = media
	usage.Categories.Code = code
	other := usage.TotalUsedBytes - (images + docs + media + code)
	if other < 0 {
		other = 0
	}
	usage.Categories.Other = other

	return &usage, nil
}

// findOrphanedHashes returns the subset of hashes that have zero remaining
// file_blocks references (i.e. the physical object is safe to delete).
func (r *FileRepository) findOrphanedHashes(ctx context.Context, hashes []string) ([]string, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	sb.WriteString(`
		SELECT b.sha256
		FROM blocks b
		WHERE b.sha256 IN (`)
	args := make([]any, 0, len(hashes))
	for i, h := range hashes {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("$%d", i+1))
		args = append(args, h)
	}
	sb.WriteString(`) AND NOT EXISTS (SELECT 1 FROM file_blocks fb WHERE fb.block_id = b.id)`)

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("find orphaned blocks: %w", err)
	}
	defer rows.Close()

	var orphans []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan orphan hash: %w", err)
		}
		orphans = append(orphans, h)
	}
		return orphans, rows.Err()
	}

// GetResolvedPermission checks permissions recursively up the folder tree
func (r *FileRepository) GetResolvedPermission(ctx context.Context, fileID string, userEmail string) (string, error) {
	const q = `
		WITH RECURSIVE file_hierarchy AS (
			-- 1. Base Case: Start with the target file/folder being checked
			SELECT id, parent_id, 1 AS depth
			FROM files
			WHERE id = $1
			
			UNION ALL
			
			-- 2. Recursive Step: Walk up the tree to the parent folder
			SELECT f.id, f.parent_id, fh.depth + 1
			FROM files f
			INNER JOIN file_hierarchy fh ON f.id = fh.parent_id
		)
		-- 3. Join the resolved hierarchy with the permissions table
		SELECT p.role
		FROM file_hierarchy fh
		INNER JOIN permissions p ON fh.id = p.file_id
		WHERE p.grantee_email = $2
		ORDER BY fh.depth ASC
		LIMIT 1;
	`
	var role string
	err := r.db.QueryRowContext(ctx, q, fileID, userEmail).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrPermissionNotFound
		}
		return "", fmt.Errorf("get resolved permission: %w", err)
	}
	return role, nil
}

// BulkSoftDelete soft-deletes multiple file/folder roots in a single transaction.
func (r *FileRepository) BulkSoftDelete(ctx context.Context, ids []string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		for _, id := range ids {
			if err := r.SoftDelete(ctx, id, userID); err != nil {
				return err
			}
		}
		return nil
	}

	return RunInTx(ctx, db, func(tx DBTX) error {
		txRepo := r.WithTx(tx)
		for _, id := range ids {
			if err := txRepo.SoftDelete(ctx, id, userID); err != nil {
				return fmt.Errorf("soft delete %s: %w", id, err)
			}
		}
		return nil
	})
}

// BulkRestore restores multiple file/folder roots in a single transaction.
func (r *FileRepository) BulkRestore(ctx context.Context, ids []string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		for _, id := range ids {
			if err := r.Restore(ctx, id, userID); err != nil {
				return err
			}
		}
		return nil
	}

	return RunInTx(ctx, db, func(tx DBTX) error {
		txRepo := r.WithTx(tx)
		for _, id := range ids {
			if err := txRepo.Restore(ctx, id, userID); err != nil {
				return fmt.Errorf("restore %s: %w", id, err)
			}
		}
		return nil
	})
}

// BulkMove updates parent_id for multiple specified file IDs in a single transaction.
func (r *FileRepository) BulkMove(ctx context.Context, ids []string, parentID *string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("BulkMove must be called outside of a transaction")
	}

	return RunInTx(ctx, db, func(tx DBTX) error {
		txRepo := r.WithTx(tx)
		
		var userEmail string
		if err := txRepo.db.QueryRowContext(ctx, "SELECT email FROM users WHERE id = $1", userID).Scan(&userEmail); err != nil {
			return fmt.Errorf("resolve user email for golden rule: %w", err)
		}

		for _, id := range ids {
			file, err := txRepo.GetByID(ctx, id)
			if err != nil {
				return fmt.Errorf("get file %s for move: %w", id, err)
			}
			
			// 1. Cycle guard
			if parentID != nil && file.IsDirectory {
				if *parentID == file.ID {
					return fmt.Errorf("cannot move a folder into itself")
				}
				isDesc, err := txRepo.IsDescendant(ctx, file.ID, *parentID)
				if err != nil {
					return fmt.Errorf("cycle check: %w", err)
				}
				if isDesc {
					return fmt.Errorf("cannot move directory inside its own descendant")
				}
			}

			// 2. Permissions
			role := domain.RoleOwner
			if file.UserID != userID {
				role, err = txRepo.GetResolvedPermission(ctx, file.ID, userEmail)
				if err != nil {
					if errors.Is(err, domain.ErrPermissionNotFound) {
						return fmt.Errorf("access denied to move file %s", id)
					}
					return fmt.Errorf("permission check %s: %w", id, err)
				}
			}

			// 3. Golden Rule fallback
			isMove := parentID != nil && ((file.ParentID == nil && *parentID != "") || (file.ParentID != nil && *parentID != *file.ParentID))
			
			if isMove && (role == domain.RoleViewer || (role == domain.RoleEditor && file.IsDirectory)) {
				realTargetID := file.ID
				if file.TargetID != nil && *file.TargetID != "" {
					realTargetID = *file.TargetID
				}
				
				shortcut := &domain.File{
					UserID: userID,
					Name: file.Name,
					ParentID: parentID,
					IsDirectory: file.IsDirectory,
					SizeBytes: file.SizeBytes,
					TargetID: &realTargetID,
					MimeType: "application/vnd.google-apps.shortcut",
					ShortcutTargetID: &realTargetID,
				}
				if err := txRepo.Create(ctx, shortcut); err != nil {
					return fmt.Errorf("create shortcut for %s: %w", id, err)
				}
			} else {
				if role != domain.RoleOwner && role != domain.RoleEditor {
					return fmt.Errorf("access denied to move file %s", id)
				}
				file.ParentID = parentID
				if err := txRepo.Update(ctx, file); err != nil {
					return fmt.Errorf("update parent_id for %s: %w", id, err)
				}
			}
		}
		return nil
	})
}

// BulkHardDelete recursively hard-deletes multiple file/folder roots and compiles orphaned block hashes in a single transaction.
func (r *FileRepository) BulkHardDelete(ctx context.Context, ids []string, userID string) (int64, []string, error) {
	if len(ids) == 0 {
		return 0, nil, nil
	}

	var totalDeleted int64
	var allOrphans []string

	execBulk := func(repo *FileRepository) error {
		for _, id := range ids {
			file, err := repo.GetByID(ctx, id)
			if err != nil {
				return fmt.Errorf("get file %s: %w", id, err)
			}
			if file.UserID != userID {
				return fmt.Errorf("access denied to delete %s", id)
			}
			count, orphans, err := repo.DeleteRecursive(ctx, id, userID)
			if err != nil {
				return fmt.Errorf("hard delete %s: %w", id, err)
			}
			totalDeleted += count
			allOrphans = append(allOrphans, orphans...)
		}
		return nil
	}

	db, ok := r.db.(*sql.DB)
	if !ok {
		if err := execBulk(r); err != nil {
			return 0, nil, err
		}
		return totalDeleted, allOrphans, nil
	}

	err := RunInTx(ctx, db, func(tx DBTX) error {
		return execBulk(r.WithTx(tx))
	})
	if err != nil {
		return 0, nil, err
	}
	return totalDeleted, allOrphans, nil
}

// CreateFileVersion inserts a new file_versions record.
func (r *FileRepository) CreateFileVersion(ctx context.Context, version *domain.FileVersion) error {
	query := `
		INSERT INTO file_versions (file_id, version_number, size_bytes, chunk_hashes)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`
	err := r.db.QueryRowContext(ctx, query,
		version.FileID,
		version.VersionNumber,
		version.SizeBytes,
		pq.Array(version.ChunkHashes),
	).Scan(&version.ID, &version.CreatedAt)
	if err != nil {
		return fmt.Errorf("create file version: %w", err)
	}
	return nil
}

// GetFileVersions returns all historical versions for a file ordered by version_number desc.
func (r *FileRepository) GetFileVersions(ctx context.Context, fileID string) ([]*domain.FileVersion, error) {
	query := `
		SELECT id, file_id, version_number, size_bytes, chunk_hashes, created_at
		FROM file_versions
		WHERE file_id = $1
		ORDER BY version_number DESC
	`
	rows, err := r.db.QueryContext(ctx, query, fileID)
	if err != nil {
		return nil, fmt.Errorf("list file versions: %w", err)
	}
	defer rows.Close()

	var versions []*domain.FileVersion
	for rows.Next() {
		v := &domain.FileVersion{}
		var hashes pq.StringArray
		if err := rows.Scan(&v.ID, &v.FileID, &v.VersionNumber, &v.SizeBytes, &hashes, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan file version: %w", err)
		}
		v.ChunkHashes = []string(hashes)
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// GetFileVersion returns a specific version by ID.
func (r *FileRepository) GetFileVersion(ctx context.Context, versionID string) (*domain.FileVersion, error) {
	query := `
		SELECT id, file_id, version_number, size_bytes, chunk_hashes, created_at
		FROM file_versions
		WHERE id = $1
	`
	v := &domain.FileVersion{}
	var hashes pq.StringArray
	err := r.db.QueryRowContext(ctx, query, versionID).Scan(
		&v.ID, &v.FileID, &v.VersionNumber, &v.SizeBytes, &hashes, &v.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("file version not found: %w", err)
		}
		return nil, fmt.Errorf("get file version: %w", err)
	}
	v.ChunkHashes = []string(hashes)
	return v, nil
}

func floatsToPgVectorStr(v []float32) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i, val := range v {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%f", val))
	}
	sb.WriteString("]")
	return sb.String()
}

// UpdateAIMetadata saves the generated semantic tags, summary, and embedding (vector).
func (r *FileRepository) UpdateAIMetadata(ctx context.Context, id string, tags, summary *string, embedding []float32) error {
	var embStr *string
	if len(embedding) > 0 {
		s := floatsToPgVectorStr(embedding)
		embStr = &s
	}

	query := `
		UPDATE files
		SET tags = $2, summary = $3, embedding = $4
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, id, tags, summary, embStr)
	if err != nil {
		return fmt.Errorf("update ai metadata: %w", err)
	}
	return nil
}

// SemanticSearch performs a cosine similarity search using pgvector on the user's files.
func (r *FileRepository) SemanticSearch(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*domain.File, error) {
	embStr := floatsToPgVectorStr(queryEmbedding)
	query := `
		SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status
		FROM files
		WHERE deleted_at IS NULL AND user_id = $1 AND embedding IS NOT NULL
		ORDER BY embedding <=> $2
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, userID, embStr, limit)
	if err != nil {
		return nil, fmt.Errorf("semantic search query: %w", err)
	}
	defer rows.Close()

	var files []*domain.File
	for rows.Next() {
		f := &domain.File{}
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType, &f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary,
		); err != nil {
			return nil, fmt.Errorf("scan semantic search result: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// Compile-time assertion that FileRepository satisfies the interface.
var _ domain.FileRepository = (*FileRepository)(nil)


func (r *FileRepository) UpdateStatus(ctx context.Context, id string, status string) error {
	q := `UPDATE files SET status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`
	_, err := r.db.ExecContext(ctx, q, status, id)
	return err
}

// RecordView upserts a view record for the user and file, updating the viewed_at timestamp.
func (r *FileRepository) RecordView(ctx context.Context, userID, fileID string) error {
	const q = `
		INSERT INTO user_file_views (user_id, file_id, viewed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, file_id) DO UPDATE SET viewed_at = NOW()
	`
	_, err := r.db.ExecContext(ctx, q, userID, fileID)
	if err != nil {
		return fmt.Errorf("record view: %w", err)
	}
	return nil
}

// GetRecentViews returns the user's recently viewed files, ordered by most recent first.
func (r *FileRepository) GetRecentViews(ctx context.Context, userID string, limit int) ([]*domain.File, error) {
	const q = `
		SELECT f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
		FROM files f
		JOIN user_file_views v ON f.id = v.file_id
		WHERE v.user_id = $1 AND f.deleted_at IS NULL
		ORDER BY v.viewed_at DESC
		LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent views: %w", err)
	}
	defer rows.Close()

	var files []*domain.File
	for rows.Next() {
		var f domain.File
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType,
			&f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status,
		); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		files = append(files, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}

	return files, nil
}

// InsertFileChunks bulk inserts file chunks for semantic search.
func (r *FileRepository) InsertFileChunks(ctx context.Context, chunks []*domain.FileChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	query := `
		INSERT INTO file_chunks (file_id, chunk_index, chunk_text, embedding)
		VALUES ($1, $2, $3, $4)
	`

	// DBTX doesn't have BeginTx, so if we're not wrapped in a Tx, we might just run multiple statements.
	// We check if we can get a transaction, otherwise we just loop.
	db, ok := r.db.(*sql.DB)
	if ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx for file chunks: %w", err)
		}
		defer tx.Rollback()

		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("prepare chunk insert: %w", err)
		}
		defer stmt.Close()

		for _, c := range chunks {
			embStr := floatsToPgVectorStr(c.Embedding)
			_, err := stmt.ExecContext(ctx, c.FileID, c.ChunkIndex, c.ChunkText, embStr)
			if err != nil {
				return fmt.Errorf("exec chunk insert: %w", err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit chunk insert: %w", err)
		}
		return nil
	}

	// Fallback if r.db is already a Tx
	for _, c := range chunks {
		embStr := floatsToPgVectorStr(c.Embedding)
		_, err := r.db.ExecContext(ctx, query, c.FileID, c.ChunkIndex, c.ChunkText, embStr)
		if err != nil {
			return fmt.Errorf("exec chunk insert: %w", err)
		}
	}

	return nil
}

// SemanticSearchChunks searches across all chunks and returns the matching files.
// We join with the files table and deduplicate by file id.
func (r *FileRepository) SemanticSearchChunks(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*domain.File, error) {
	embStr := floatsToPgVectorStr(queryEmbedding)
	// We want to find the top matching chunks, and then return their corresponding files.
	// Since one file can have multiple chunks matching, we group by file fields or use DISTINCT ON.
	query := `
		SELECT DISTINCT ON (f.id)
			f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, 
			f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, 
			f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
		FROM file_chunks fc
		JOIN files f ON f.id = fc.file_id
		WHERE f.deleted_at IS NULL AND f.user_id = $1
		ORDER BY f.id, fc.embedding <=> $2
	`
	
	// Actually, DISTINCT ON requires the ORDER BY to start with the DISTINCT ON expression (f.id).
	// But we want to order by cosine similarity overall, not just within each file.
	// A better query is to find top N chunks, then get the files for those chunks, preserving order.
	
	query = `
		WITH RankedChunks AS (
			SELECT file_id, embedding <=> $2 AS distance
			FROM file_chunks
			ORDER BY distance ASC
			LIMIT $3
		),
		RankedFiles AS (
			SELECT file_id, MIN(distance) as min_distance
			FROM RankedChunks
			GROUP BY file_id
			ORDER BY min_distance ASC
		)
		SELECT f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, 
			f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, 
			f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
		FROM RankedFiles rf
		JOIN files f ON f.id = rf.file_id
		WHERE f.deleted_at IS NULL AND f.user_id = $1
		ORDER BY rf.min_distance ASC
	`

	// Wait, the limit applies to chunks. If limit=20 chunks belong to 2 files, it returns 2 files. That's fine.
	// But we must also ensure we only search chunks of the user's files to prevent data leak, 
	// or we just filter by user_id at the end (which is safe since f.user_id = $1).
	// But filtering at the end means if the top 20 chunks belong to other users, we might get 0 results.
	// So we should filter by user_id in the chunk search CTE:

	query = `
		WITH UserFiles AS (
			SELECT id FROM files WHERE user_id = $1 AND deleted_at IS NULL
		),
		RankedChunks AS (
			SELECT fc.file_id, fc.embedding <=> $2 AS distance
			FROM file_chunks fc
			JOIN UserFiles uf ON fc.file_id = uf.id
			ORDER BY distance ASC
			LIMIT $3
		),
		RankedFiles AS (
			SELECT file_id, MIN(distance) as min_distance
			FROM RankedChunks
			GROUP BY file_id
			ORDER BY min_distance ASC
		)
		SELECT f.id, f.user_id, f.name, f.parent_id, f.is_directory, f.size_bytes, 
			f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, 
			f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
		FROM RankedFiles rf
		JOIN files f ON f.id = rf.file_id
		ORDER BY rf.min_distance ASC
	`

	rows, err := r.db.QueryContext(ctx, query, userID, embStr, limit)
	if err != nil {
		return nil, fmt.Errorf("semantic search chunks query: %w", err)
	}
	defer rows.Close()

	var files []*domain.File
	for rows.Next() {
		f := &domain.File{}
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType, 
			&f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status,
		); err != nil {
			return nil, fmt.Errorf("scan semantic search chunks result: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
