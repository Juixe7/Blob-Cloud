package postgresrepo

import (
	"context"
	"fmt"
	"strings"

	"go-drive-clone/internal/domain"
)

// PermissionRepository persists sharing grants and answers access checks.
type PermissionRepository struct {
	db DBTX
}

// NewPermissionRepository constructs a PermissionRepository bound to db.
func NewPermissionRepository(db DBTX) *PermissionRepository {
	return &PermissionRepository{db: db}
}

// WithTx returns a copy bound to tx.
func (r *PermissionRepository) WithTx(tx DBTX) *PermissionRepository {
	return &PermissionRepository{db: tx}
}

// GrantPermission inserts a permission row. The grantee is identified by email
// (so a share can be issued before the recipient has signed in). A duplicate
// (file_id, grantee_email) violates the unique constraint and errors out.
func (r *PermissionRepository) GrantPermission(ctx context.Context, perm *domain.Permission) error {
	const q = `
		INSERT INTO permissions (file_id, grantee_email, role)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q, perm.FileID, perm.GranteeEmail, perm.Role)
	if err := row.Scan(&perm.ID, &perm.CreatedAt); err != nil {
		return fmt.Errorf("grant permission: %w", err)
	}
	return nil
}

// GetPermissionsByFile lists all grants for a file (direct grants only; does
// not walk the hierarchy).
func (r *PermissionRepository) GetPermissionsByFile(ctx context.Context, fileID string) ([]*domain.Permission, error) {
	const q = `
		SELECT id, file_id, grantee_email, role, created_at
		FROM permissions
		WHERE file_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("query permissions by file: %w", err)
	}
	defer rows.Close()

	var out []*domain.Permission
	for rows.Next() {
		var p domain.Permission
		if err := rows.Scan(&p.ID, &p.FileID, &p.GranteeEmail, &p.Role, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// CheckUserPermission reports whether userEmail holds one of requiredRoles on
// fileID, walking up the folder hierarchy.
//
// The query uses a recursive CTE to climb the parent_id chain from the target
// file, collecting every file/folder id from the file itself up to the root.
// Any direct permission on any node in that chain, whose role is in the
// required set, satisfies the check — so granting VIEWER on a folder grants
// VIEWER on everything inside it.
//
// Role rank is handled in Go (OWNER > EDITOR > VIEWER) rather than in SQL so
// the caller controls exactly which roles satisfy a requirement: pass the full
// set of acceptable roles (e.g. ["VIEWER","EDITOR","OWNER"] for a read check,
// ["EDITOR","OWNER"] for a write check).
func (r *PermissionRepository) CheckUserPermission(ctx context.Context, fileID string, userEmail string, requiredRoles []string) (bool, error) {
	if len(requiredRoles) == 0 {
		return false, nil
	}

	// Build an IN-list of acceptable roles for the permissions join.
	roleArgs := make([]any, 0, len(requiredRoles))
	var rolePlaceholders strings.Builder
	for i, role := range requiredRoles {
		if i > 0 {
			rolePlaceholders.WriteByte(',')
		}
		// fileID is $1, userEmail is $2; roles start at $3.
		rolePlaceholders.WriteString(fmt.Sprintf("$%d", i+3))
		roleArgs = append(roleArgs, role)
	}

	q := fmt.Sprintf(`
		WITH RECURSIVE chain AS (
			-- Anchor: the target file itself.
			SELECT id, parent_id FROM files WHERE id = $1
			UNION ALL
			-- Recurse: walk up to each ancestor folder.
			SELECT f.id, f.parent_id
			FROM files f
			JOIN chain c ON f.id = c.parent_id
		)
		SELECT EXISTS (
			SELECT 1
			FROM permissions p
			JOIN chain c ON c.id = p.file_id
			WHERE p.grantee_email = $2
			  AND p.role IN (%s)
		) AS allowed
	`, rolePlaceholders.String())

	args := append([]any{fileID, userEmail}, roleArgs...)
	var allowed bool
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check user permission: %w", err)
	}
	return allowed, nil
}

// Compile-time assertion that PermissionRepository satisfies the interface.
var _ domain.PermissionRepository = (*PermissionRepository)(nil)
