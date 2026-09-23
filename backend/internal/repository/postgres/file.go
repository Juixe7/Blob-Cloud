package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go-drive-clone/internal/domain"
)

// EscapeSQLLike escapes special wildcard characters ('\', '%', '_') for SQL LIKE patterns.
func EscapeSQLLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

const fileSelectColumns = `id, user_id, name, parent_id, path, is_directory, size_bytes, created_at, updated_at, deleted_at, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, tags, summary, status`

func scanFileRow(scanner interface{ Scan(dest ...any) error }, f *domain.File) error {
	return scanner.Scan(
		&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.Path, &f.IsDirectory, &f.SizeBytes,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.TargetID, &f.MimeType,
		&f.ShortcutTargetID, &f.IsEncrypted, &f.EncryptionSalt, &f.Tags, &f.Summary, &f.Status,
	)
}

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

// Create inserts file, ensuring an authoritative materialized path is set, and reads back timestamps.
func (r *FileRepository) Create(ctx context.Context, file *domain.File) error {
	if file.ID == "" {
		file.ID = uuid.New().String()
	}

	if file.Path == "" {
		if file.ParentID == nil || *file.ParentID == "" {
			file.Path = "/" + file.ID + "/"
		} else {
			var parentPath string
			err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1", *file.ParentID).Scan(&parentPath)
			if err != nil {
				return fmt.Errorf("lookup parent path for %s: %w", *file.ParentID, err)
			}
			file.Path = parentPath + file.ID + "/"
		}
	}

	const q = `
		INSERT INTO files (id, user_id, name, parent_id, path, is_directory, size_bytes, target_id, mime_type, shortcut_target_id, is_encrypted, encryption_salt, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, q,
		file.ID, file.UserID, file.Name, file.ParentID, file.Path, file.IsDirectory, file.SizeBytes, file.TargetID, file.MimeType, file.ShortcutTargetID, file.IsEncrypted, file.EncryptionSalt, file.Status)
	if err := row.Scan(&file.CreatedAt, &file.UpdatedAt); err != nil {
		return fmt.Errorf("insert file: %w", err)
	}
	return nil
}

// GetByID returns the file with the given id.
func (r *FileRepository) GetByID(ctx context.Context, id string) (*domain.File, error) {
	q := fmt.Sprintf(`SELECT %s FROM files WHERE id = $1`, fileSelectColumns)
	var f domain.File
	err := scanFileRow(r.db.QueryRowContext(ctx, q, id), &f)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("file by id %q: %w", id, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query file by id: %w", err)
	}
	return &f, nil
}

// GetFolderByNameAndParent checks if an active (non-deleted) folder with exact name and parentID exists for userID.
func (r *FileRepository) GetFolderByNameAndParent(ctx context.Context, userID, name string, parentID *string) (*domain.File, error) {
	var (
		row *sql.Row
		f   domain.File
	)
	if parentID == nil {
		q := fmt.Sprintf(`
			SELECT %s
			FROM files
			WHERE user_id = $1 AND name = $2 AND parent_id IS NULL AND is_directory = TRUE AND deleted_at IS NULL
			LIMIT 1
		`, fileSelectColumns)
		row = r.db.QueryRowContext(ctx, q, userID, name)
	} else {
		q := fmt.Sprintf(`
			SELECT %s
			FROM files
			WHERE user_id = $1 AND name = $2 AND parent_id = $3 AND is_directory = TRUE AND deleted_at IS NULL
			LIMIT 1
		`, fileSelectColumns)
		row = r.db.QueryRowContext(ctx, q, userID, name, *parentID)
	}

	err := scanFileRow(row, &f)
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
		q := fmt.Sprintf(`
			SELECT %s
			FROM files
			WHERE user_id = $1 AND parent_id IS NULL AND deleted_at IS NULL
			ORDER BY is_directory DESC, name ASC
		`, fileSelectColumns)
		rows, err = r.db.QueryContext(ctx, q, userID)
	} else {
		q := fmt.Sprintf(`
			SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
			FROM files f
			WHERE f.parent_id = $1
			  AND (
			    (SELECT deleted_at FROM files WHERE id = $1) IS NOT NULL
			    OR f.deleted_at IS NULL
			  )
			ORDER BY f.is_directory DESC, f.name ASC
		`)
		rows, err = r.db.QueryContext(ctx, q, *parentID)
	}
	if err != nil {
		return nil, fmt.Errorf("query directory: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var f domain.File
		if err := scanFileRow(rows, &f); err != nil {
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
		SELECT dr.id, dr.user_id, dr.name, dr.parent_id, dr.path, dr.is_directory, dr.size_bytes, dr.created_at, dr.updated_at, dr.deleted_at,
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
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.Path, &f.IsDirectory, &f.SizeBytes,
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

// GetDescendants retrieves all non-deleted files and subfolders nested under rootID using materialized path prefix scan.
func (r *FileRepository) GetDescendants(ctx context.Context, rootID string) ([]*domain.File, error) {
	var rootPath string
	err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1", rootID).Scan(&rootPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup root path: %w", err)
	}

	escapedPrefix := EscapeSQLLike(rootPath) + "%"
	q := fmt.Sprintf(`
		SELECT %s
		FROM files
		WHERE path LIKE $1 ESCAPE '\' AND path != $2 AND deleted_at IS NULL
		ORDER BY path ASC
	`, fileSelectColumns)
	rows, err := r.db.QueryContext(ctx, q, escapedPrefix, rootPath)
	if err != nil {
		return nil, fmt.Errorf("query descendants: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var f domain.File
		if err := scanFileRow(rows, &f); err != nil {
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

// SoftDelete sets deleted_at = CURRENT_TIMESTAMP for rootID and all nested descendants in O(1) prefix match.
func (r *FileRepository) SoftDelete(ctx context.Context, rootID string, userID string) error {
	var rootPath string
	err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1 AND user_id = $2", rootID, userID).Scan(&rootPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup root path: %w", err)
	}

	escapedPrefix := EscapeSQLLike(rootPath) + "%"

	// 1. Orphan foreign files whose parent is inside this subtree
	const qOrphan = `
		UPDATE files
		SET parent_id = NULL
		WHERE parent_id IN (
			SELECT id FROM files WHERE user_id = $1 AND (path = $2 OR path LIKE $3 ESCAPE '\')
		) AND user_id != $1;
	`
	if _, err := r.db.ExecContext(ctx, qOrphan, userID, rootPath, escapedPrefix); err != nil {
		return fmt.Errorf("soft delete orphan foreign files: %w", err)
	}

	// 2. Soft delete the folder and all descendants
	const qDelete = `
		UPDATE files
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND (path = $2 OR path LIKE $3 ESCAPE '\');
	`
	if _, err := r.db.ExecContext(ctx, qDelete, userID, rootPath, escapedPrefix); err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}
	return nil
}

// Restore sets deleted_at = NULL for rootID and all nested descendants in O(1) prefix match.
func (r *FileRepository) Restore(ctx context.Context, rootID string, userID string) error {
	var rootPath string
	err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1", rootID).Scan(&rootPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup root path: %w", err)
	}

	escapedPrefix := EscapeSQLLike(rootPath) + "%"
	const q = `
		UPDATE files
		SET deleted_at = NULL
		WHERE (path = $1 OR path LIKE $2 ESCAPE '\');
	`
	if _, err := r.db.ExecContext(ctx, q, rootPath, escapedPrefix); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

// ListSharedWithUser returns all files/folders that have been shared with userEmail
// (excluding files owned by the user themselves and excluding soft-deleted files).
func (r *FileRepository) ListSharedWithUser(ctx context.Context, userEmail, userID string) ([]*domain.File, error) {
	const q = `
		SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, p.created_at, f.target_id, p.role
		FROM files f
		JOIN permissions p ON f.id = p.file_id
		WHERE p.grantee_email = $1 AND p.status = 'ACCEPTED' AND f.user_id != $2 AND f.deleted_at IS NULL
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
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.Path, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &sharedAt, &f.TargetID, &f.Role); err != nil {
			return nil, fmt.Errorf("scan shared file row: %w", err)
		}
		f.SharedAt = &sharedAt
		out = append(out, &f)
	}
	return out, rows.Err()
}

// MoveSubtree atomically moves a folder and all its nested descendants to a new parent,
// updating all materialized paths in O(1) via prefix substitution.
func (r *FileRepository) MoveSubtree(ctx context.Context, folderID string, newParentID *string, userID string) error {
	var (
		oldPath    string
		targetPath string
		newPrefix  string
	)

	// 1. Fetch current folder path
	err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1 AND user_id = $2", folderID, userID).Scan(&oldPath)
	if err != nil {
		return fmt.Errorf("lookup folder %s: %w", folderID, err)
	}

	// 2. Resolve new parent path
	if newParentID == nil || *newParentID == "" {
		newPrefix = "/" + folderID + "/"
	} else {
		if *newParentID == folderID {
			return fmt.Errorf("cannot move a folder into itself")
		}
		err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1", *newParentID).Scan(&targetPath)
		if err != nil {
			return fmt.Errorf("lookup new parent %s: %w", *newParentID, err)
		}

		// Cycle check: target parent path cannot start with oldPath
		if strings.HasPrefix(targetPath, oldPath) {
			return fmt.Errorf("cannot move directory inside its own descendant")
		}
		newPrefix = targetPath + folderID + "/"
	}

	if newPrefix == oldPath {
		// Nothing to move
		return nil
	}

	// 3. Subtree relocation
	// In PostgreSQL, SUBSTRING is 1-indexed. Starting at len(oldPath)+1 yields the remainder after oldPath.
	oldPrefixEscaped := EscapeSQLLike(oldPath) + "%"
	oldLenPlusOne := len(oldPath) + 1

	const q = `
		UPDATE files
		SET path = $1 || SUBSTRING(path FROM $2),
		    parent_id = CASE WHEN id = $6 THEN $7 ELSE parent_id END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $3 AND (path = $4 OR path LIKE $5 ESCAPE '\')
	`
	_, err = r.db.ExecContext(ctx, q,
		newPrefix,
		oldLenPlusOne,
		userID,
		oldPath,
		oldPrefixEscaped,
		folderID,
		newParentID,
	)
	if err != nil {
		return fmt.Errorf("relocate subtree %s: %w", folderID, err)
	}

	return nil
}

// Update mutates a file/folder's name and/or parent_id and refreshes
// updated_at. The file.ID must already be set; on return the struct is
// repopulated with the persisted row (including server-set path and updated_at).
func (r *FileRepository) Update(ctx context.Context, file *domain.File) error {
	// If parent_id changed, handle subtree relocation or single file move
	if file.ParentID != nil {
		var (
			currentParentID *string
			isDir           bool
			userID          string
		)
		err := r.db.QueryRowContext(ctx, "SELECT parent_id, is_directory, user_id FROM files WHERE id = $1", file.ID).Scan(
			&currentParentID, &isDir, &userID)
		if err != nil {
			return fmt.Errorf("lookup file for update %s: %w", file.ID, err)
		}

		var reqParentID *string
		if *file.ParentID != "" {
			reqParentID = file.ParentID
		}

		parentChanged := false
		if (currentParentID == nil && reqParentID != nil) || (currentParentID != nil && reqParentID == nil) {
			parentChanged = true
		} else if currentParentID != nil && reqParentID != nil && *currentParentID != *reqParentID {
			parentChanged = true
		}

		if parentChanged {
			if isDir {
				if err := r.MoveSubtree(ctx, file.ID, reqParentID, userID); err != nil {
					return err
				}
			} else {
				// Single file move
				var newPath string
				if reqParentID == nil {
					newPath = "/" + file.ID + "/"
				} else {
					var parentPath string
					if err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1", *reqParentID).Scan(&parentPath); err != nil {
						return fmt.Errorf("lookup parent path: %w", err)
					}
					newPath = parentPath + file.ID + "/"
				}
				_, err := r.db.ExecContext(ctx, "UPDATE files SET parent_id = $1, path = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $3",
					reqParentID, newPath, file.ID)
				if err != nil {
					return fmt.Errorf("update file parent/path %s: %w", file.ID, err)
				}
			}
		}
	}

	// Update mutable fields (name, size, mime_type, status, encryption)
	q := `
		UPDATE files 
		SET name = CASE WHEN $1 != '' THEN $1 ELSE name END,
		    size_bytes = $2,
		    mime_type = CASE WHEN $3 != '' THEN $3 ELSE mime_type END,
		    status = CASE WHEN $4 != '' THEN $4 ELSE status END,
		    is_encrypted = $5,
		    encryption_salt = $6,
		    updated_at = CURRENT_TIMESTAMP 
		WHERE id = $7
	`
	if _, err := r.db.ExecContext(ctx, q, file.Name, file.SizeBytes, file.MimeType, file.Status, file.IsEncrypted, file.EncryptionSalt, file.ID); err != nil {
		return fmt.Errorf("update file row %s: %w", file.ID, err)
	}

	// Read back the fresh state
	fresh, err := r.GetByID(ctx, file.ID)
	if err != nil {
		return fmt.Errorf("refresh file after update %s: %w", file.ID, err)
	}
	*file = *fresh
	return nil
}

// IsDescendant reports whether candidateID equals ancestorID or is reachable
// by walking DOWN the folder tree starting from ancestorID.
// It uses materialized path prefix matching.
func (r *FileRepository) IsDescendant(ctx context.Context, candidateID, ancestorID string) (bool, error) {
	if candidateID == ancestorID {
		return true, nil
	}
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM files c, files a
			WHERE c.id = $1 AND a.id = $2
			  AND (c.id = a.id OR c.path LIKE a.path || '%')
		)
	`
	var isDesc bool
	if err := r.db.QueryRowContext(ctx, q, candidateID, ancestorID).Scan(&isDesc); err != nil {
		return false, fmt.Errorf("is-descendant check (%s in %s): %w", candidateID, ancestorID, err)
	}
	return isDesc, nil
}

// DeleteRecursive removes the file/folder identified by rootID and, when it is
// a directory, every descendant using materialized path prefix matching.
func (r *FileRepository) DeleteRecursive(ctx context.Context, rootID string, userID string) (int64, []string, error) {
	var rootPath string
	err := r.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = $1 AND user_id = $2", rootID, userID).Scan(&rootPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("lookup root path %s: %w", rootID, err)
	}

	escapedPrefix := EscapeSQLLike(rootPath) + "%"

	// 1. Gather hashes of blocks referenced by files in this subtree
	const collectSubtreeHashes = `
		SELECT DISTINCT b.sha256
		FROM file_blocks fb
		JOIN blocks b ON b.id = fb.block_id
		WHERE fb.file_id IN (
			SELECT id FROM files
			WHERE user_id = $1 AND (path = $2 OR path LIKE $3 ESCAPE '\')
		)
	`
	rows, err := r.db.QueryContext(ctx, collectSubtreeHashes, userID, rootPath, escapedPrefix)
	if err != nil {
		return 0, nil, fmt.Errorf("collect subtree hashes: %w", err)
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("scan block hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	_ = rows.Close()

	// 2. Orphan files owned by other users in the subtree before deleting
	const orphanSubtree = `
		UPDATE files
		SET parent_id = NULL
		WHERE parent_id IN (
			SELECT id FROM files WHERE user_id = $1 AND (path = $2 OR path LIKE $3 ESCAPE '\')
		) AND user_id != $1
	`
	if _, err := r.db.ExecContext(ctx, orphanSubtree, userID, rootPath, escapedPrefix); err != nil {
		return 0, nil, fmt.Errorf("orphan foreign files under subtree %s: %w", rootID, err)
	}

	// 3. Delete the subtree
	const deleteSubtree = `
		DELETE FROM files
		WHERE user_id = $1 AND (path = $2 OR path LIKE $3 ESCAPE '\')
	`
	res, err := r.db.ExecContext(ctx, deleteSubtree, userID, rootPath, escapedPrefix)
	if err != nil {
		return 0, nil, fmt.Errorf("delete subtree %s: %w", rootID, err)
	}
	deleted, _ := res.RowsAffected()

	// 4. Find orphaned block hashes safe for GC
	var orphans []string
	if len(hashes) > 0 {
		orphans, err = r.findOrphanedHashes(ctx, hashes)
		if err != nil {
			return deleted, nil, err
		}
	}
	return deleted, orphans, nil
}

// GetUserStorageUsage calculates real total byte usage and category breakdown for a user,
// aggregating both active files and historical file revisions.
func (r *FileRepository) GetUserStorageUsage(ctx context.Context, userID string) (*domain.UserStorageUsage, error) {
	const q = `
		WITH active_files AS (
			SELECT name, size_bytes FROM files WHERE user_id = $1 AND is_directory = FALSE
		),
		version_files AS (
			SELECT f.name, fv.size_bytes
			FROM file_versions fv
			JOIN files f ON fv.file_id = f.id
			WHERE f.user_id = $1
		),
		all_files AS (
			SELECT name, size_bytes FROM active_files
			UNION ALL
			SELECT name, size_bytes FROM version_files
		)
		SELECT 
			COALESCE(SUM(size_bytes), 0) AS total_used,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('png','jpg','jpeg','webp','gif','svg','bmp') THEN size_bytes ELSE 0 END), 0) AS images,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('pdf','doc','docx','txt','rtf','xls','xlsx','csv') THEN size_bytes ELSE 0 END), 0) AS documents,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('mp3','wav','flac','mp4','webm','mov','mkv','avi') THEN size_bytes ELSE 0 END), 0) AS media,
			COALESCE(SUM(CASE WHEN LOWER(SUBSTRING(name FROM '\.([^\.]+)$')) IN ('js','ts','jsx','tsx','go','py','json','html','css','zip','tar','gz') THEN size_bytes ELSE 0 END), 0) AS code
		FROM all_files
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
// file_blocks references AND zero historical file_versions references
// (i.e. the physical object is safe to delete from CAS).
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
	sb.WriteString(`) AND NOT EXISTS (SELECT 1 FROM file_blocks fb WHERE fb.block_id = b.id) AND NOT EXISTS (SELECT 1 FROM file_versions fv WHERE b.sha256 = ANY(fv.chunk_hashes))`)

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
				isDesc, err := txRepo.IsDescendant(ctx, *parentID, file.ID)
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
	query := fmt.Sprintf(`
		SELECT %s
		FROM files
		WHERE deleted_at IS NULL AND user_id = $1 AND embedding IS NOT NULL
		ORDER BY embedding <=> $2
		LIMIT $3
	`, fileSelectColumns)

	rows, err := r.db.QueryContext(ctx, query, userID, embStr, limit)
	if err != nil {
		return nil, fmt.Errorf("semantic search query: %w", err)
	}
	defer rows.Close()

	var files []*domain.File
	for rows.Next() {
		f := &domain.File{}
		if err := scanFileRow(rows, f); err != nil {
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
		SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
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
		if err := scanFileRow(rows, &f); err != nil {
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
		SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, 
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
		SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, 
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
		if err := scanFileRow(rows, f); err != nil {
			return nil, fmt.Errorf("scan semantic search chunks result: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// HybridSearch performs a unified search matching filenames, tags, summaries, and vector chunk embeddings.
// It searches both files owned by the user and files explicitly shared with the user.
func (r *FileRepository) HybridSearch(ctx context.Context, userID, userEmail, query string, queryEmbedding []float32, limit int) ([]*domain.File, error) {
	if limit <= 0 {
		limit = 20
	}

	searchPattern := "%" + strings.TrimSpace(query) + "%"

	// If queryEmbedding is available, perform full hybrid search:
	// keyword match + semantic chunk vector distance match
	if len(queryEmbedding) > 0 {
		embStr := floatsToPgVectorStr(queryEmbedding)
		sqlQuery := `
			WITH AccessibleFiles AS (
				SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, 
					f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, 
					f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
				FROM files f
				WHERE f.deleted_at IS NULL AND (
					f.user_id = $1 OR 
					EXISTS (
						SELECT 1 FROM permissions p 
						WHERE p.file_id = f.id AND (p.grantee_email = $2 OR $2 = '')
					)
				)
			),
			KeywordMatches AS (
				SELECT id, 
					CASE 
						WHEN LOWER(name) = LOWER($3) THEN 1.0
						WHEN LOWER(name) LIKE LOWER($4) THEN 0.8
						WHEN tags IS NOT NULL AND LOWER(tags) LIKE LOWER($4) THEN 0.6
						WHEN summary IS NOT NULL AND LOWER(summary) LIKE LOWER($4) THEN 0.5
						ELSE 0.3
					END AS keyword_score
				FROM AccessibleFiles
				WHERE LOWER(name) LIKE LOWER($4)
				   OR (tags IS NOT NULL AND LOWER(tags) LIKE LOWER($4))
				   OR (summary IS NOT NULL AND LOWER(summary) LIKE LOWER($4))
			),
			SemanticMatches AS (
				SELECT fc.file_id AS id, 
					MIN(1.0 - (fc.embedding <=> $5)) AS semantic_score
				FROM file_chunks fc
				JOIN AccessibleFiles af ON fc.file_id = af.id
				GROUP BY fc.file_id
			),
			CombinedScores AS (
				SELECT af.id,
					COALESCE(km.keyword_score, 0.0) AS kw_score,
					COALESCE(sm.semantic_score, 0.0) AS sem_score,
					(COALESCE(km.keyword_score, 0.0) * 1.5 + COALESCE(sm.semantic_score, 0.0)) AS total_rank
				FROM AccessibleFiles af
				LEFT JOIN KeywordMatches km ON af.id = km.id
				LEFT JOIN SemanticMatches sm ON af.id = sm.id
				WHERE km.id IS NOT NULL OR (sm.id IS NOT NULL AND sm.semantic_score > 0.4)
			)
			SELECT af.id, af.user_id, af.name, af.parent_id, af.path, af.is_directory, af.size_bytes, 
				af.created_at, af.updated_at, af.deleted_at, af.target_id, af.mime_type, 
				af.shortcut_target_id, af.is_encrypted, af.encryption_salt, af.tags, af.summary, af.status
			FROM CombinedScores cs
			JOIN AccessibleFiles af ON af.id = cs.id
			ORDER BY cs.total_rank DESC, af.updated_at DESC
			LIMIT $6;
		`

		rows, err := r.db.QueryContext(ctx, sqlQuery, userID, userEmail, query, searchPattern, embStr, limit)
		if err == nil {
			defer rows.Close()
			var files []*domain.File
			for rows.Next() {
				f := &domain.File{}
				if err := scanFileRow(rows, f); err != nil {
					break
				}
				files = append(files, f)
			}
			if len(files) > 0 {
				return files, nil
			}
		}
	}

	// Fallback or when no queryEmbedding provided: Keyword/tag/summary search
	return r.keywordSearch(ctx, userID, userEmail, query, limit)
}

func (r *FileRepository) keywordSearch(ctx context.Context, userID, userEmail, query string, limit int) ([]*domain.File, error) {
	if limit <= 0 {
		limit = 20
	}
	searchPattern := "%" + strings.TrimSpace(query) + "%"
	sqlQuery := `
		SELECT f.id, f.user_id, f.name, f.parent_id, f.path, f.is_directory, f.size_bytes, 
			f.created_at, f.updated_at, f.deleted_at, f.target_id, f.mime_type, 
			f.shortcut_target_id, f.is_encrypted, f.encryption_salt, f.tags, f.summary, f.status
		FROM files f
		WHERE f.deleted_at IS NULL AND (
			f.user_id = $1 OR 
			EXISTS (
				SELECT 1 FROM permissions p 
				WHERE p.file_id = f.id AND (p.grantee_email = $2 OR $2 = '')
			)
		) AND (
			f.name ILIKE $3 OR 
			(f.tags IS NOT NULL AND f.tags ILIKE $3) OR 
			(f.summary IS NOT NULL AND f.summary ILIKE $3)
		)
		ORDER BY 
			CASE 
				WHEN LOWER(f.name) = LOWER($4) THEN 1
				WHEN LOWER(f.name) LIKE LOWER($3) THEN 2
				ELSE 3
			END ASC,
			f.updated_at DESC
		LIMIT $5;
	`

	rows, err := r.db.QueryContext(ctx, sqlQuery, userID, userEmail, searchPattern, query, limit)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()

	var files []*domain.File
	for rows.Next() {
		f := &domain.File{}
		if err := scanFileRow(rows, f); err != nil {
			return nil, fmt.Errorf("scan keyword search: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
