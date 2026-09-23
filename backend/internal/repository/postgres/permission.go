package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
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
	if perm.Status == "" {
		perm.Status = domain.PermissionStatusAccepted
	}
	const q = `
		INSERT INTO permissions (file_id, grantee_email, role, status, message, expires_at, invited_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, status, expires_at
	`
	row := r.db.QueryRowContext(ctx, q, perm.FileID, perm.GranteeEmail, perm.Role, perm.Status, perm.Message, perm.ExpiresAt, perm.InvitedBy)
	if err := row.Scan(&perm.ID, &perm.CreatedAt, &perm.Status, &perm.ExpiresAt); err != nil {
		return fmt.Errorf("grant permission: %w", err)
	}
	return nil
}

// GetPermissionsByFile lists all grants for a file (direct grants only; does
// not walk the hierarchy).
func (r *PermissionRepository) GetPermissionsByFile(ctx context.Context, fileID string) ([]*domain.Permission, error) {
	const q = `
		SELECT id, file_id, grantee_email, role, status, message, expires_at, responded_at, invited_by, created_at
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
		if err := rows.Scan(
			&p.ID, &p.FileID, &p.GranteeEmail, &p.Role, &p.Status, &p.Message, &p.ExpiresAt, &p.RespondedAt, &p.InvitedBy, &p.CreatedAt,
		); err != nil {
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
			  AND p.status = 'ACCEPTED'
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

// UpdatePermission updates the role of an existing permission.
func (r *PermissionRepository) UpdatePermission(ctx context.Context, fileID string, granteeEmail string, role string) error {
	const q = `
		UPDATE permissions
		SET role = $1
		WHERE file_id = $2 AND grantee_email = $3
	`
	res, err := r.db.ExecContext(ctx, q, role, fileID, granteeEmail)
	if err != nil {
		return fmt.Errorf("update permission: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update permission rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrPermissionNotFound
	}
	return nil
}

// RevokePermission deletes a permission grant.
func (r *PermissionRepository) RevokePermission(ctx context.Context, fileID string, granteeEmail string) error {
	const q = `
		DELETE FROM permissions
		WHERE file_id = $1 AND grantee_email = $2
	`
	res, err := r.db.ExecContext(ctx, q, fileID, granteeEmail)
	if err != nil {
		return fmt.Errorf("revoke permission: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke permission rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrPermissionNotFound
	}
	return nil
}

// ListPendingInvitations returns all active (unexpired) PENDING invitations for userEmail.
func (r *PermissionRepository) ListPendingInvitations(ctx context.Context, userEmail string) ([]*domain.ShareInvitation, error) {
	const q = `
		SELECT p.id, p.file_id, p.role, p.status, p.message, p.expires_at, p.created_at,
		       f.name, f.is_directory, f.size_bytes, COALESCE(f.mime_type, ''), f.summary, f.tags,
		       COALESCE(u.email, 'Someone') AS sender_email
		FROM permissions p
		JOIN files f ON p.file_id = f.id
		LEFT JOIN users u ON p.invited_by = u.id
		WHERE p.grantee_email = $1
		  AND p.status = 'PENDING'
		  AND p.expires_at > CURRENT_TIMESTAMP
		  AND f.deleted_at IS NULL
		ORDER BY p.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q, userEmail)
	if err != nil {
		return nil, fmt.Errorf("list pending invitations: %w", err)
	}
	defer rows.Close()

	var out []*domain.ShareInvitation
	for rows.Next() {
		var inv domain.ShareInvitation
		if err := rows.Scan(
			&inv.ID, &inv.FileID, &inv.Role, &inv.Status, &inv.Message, &inv.ExpiresAt, &inv.CreatedAt,
			&inv.FileName, &inv.IsDirectory, &inv.SizeBytes, &inv.MimeType, &inv.Summary, &inv.Tags,
			&inv.SenderEmail,
		); err != nil {
			return nil, fmt.Errorf("scan pending invitation: %w", err)
		}
		out = append(out, &inv)
	}
	return out, rows.Err()
}

// RespondToInvitation transitions a PENDING invitation to ACCEPTED or DECLINED.
func (r *PermissionRepository) RespondToInvitation(ctx context.Context, invitationID string, userEmail string, status string) (*domain.Permission, error) {
	const q = `
		UPDATE permissions
		SET status = $1, responded_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND grantee_email = $3 AND status = 'PENDING' AND expires_at > CURRENT_TIMESTAMP
		RETURNING id, file_id, grantee_email, role, status, message, expires_at, responded_at, invited_by, created_at
	`
	var p domain.Permission
	row := r.db.QueryRowContext(ctx, q, status, invitationID, userEmail)
	if err := row.Scan(
		&p.ID, &p.FileID, &p.GranteeEmail, &p.Role, &p.Status, &p.Message, &p.ExpiresAt, &p.RespondedAt, &p.InvitedBy, &p.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("invitation not found, expired, or already processed: %w", sql.ErrNoRows)
		}
		return nil, fmt.Errorf("respond to invitation: %w", err)
	}
	return &p, nil
}

// GetInvitationByID returns a single invitation by ID.
func (r *PermissionRepository) GetInvitationByID(ctx context.Context, invitationID string) (*domain.Permission, error) {
	const q = `
		SELECT id, file_id, grantee_email, role, status, message, expires_at, responded_at, invited_by, created_at
		FROM permissions
		WHERE id = $1
	`
	var p domain.Permission
	err := r.db.QueryRowContext(ctx, q, invitationID).Scan(
		&p.ID, &p.FileID, &p.GranteeEmail, &p.Role, &p.Status, &p.Message, &p.ExpiresAt, &p.RespondedAt, &p.InvitedBy, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PurgeStaleInvitations marks PENDING rows with expires_at <= NOW() as EXPIRED.
func (r *PermissionRepository) PurgeStaleInvitations(ctx context.Context) (int64, error) {
	const q = `
		UPDATE permissions
		SET status = 'EXPIRED'
		WHERE status = 'PENDING' AND expires_at <= CURRENT_TIMESTAMP
	`
	res, err := r.db.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("purge stale invitations: %w", err)
	}
	return res.RowsAffected()
}

// BlockUser blocks an email address from sharing files with userID.
func (r *PermissionRepository) BlockUser(ctx context.Context, userID string, blockedEmail string) error {
	const q = `
		INSERT INTO user_blocks (user_id, blocked_email)
		VALUES ($1, $2)
		ON CONFLICT (user_id, blocked_email) DO NOTHING
	`
	_, err := r.db.ExecContext(ctx, q, userID, blockedEmail)
	if err != nil {
		return fmt.Errorf("block user: %w", err)
	}
	return nil
}

// IsBlocked checks whether userID has blocked senderEmail.
func (r *PermissionRepository) IsBlocked(ctx context.Context, userID string, senderEmail string) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM user_blocks
			WHERE user_id = $1 AND blocked_email = $2
		)
	`
	var blocked bool
	if err := r.db.QueryRowContext(ctx, q, userID, senderEmail).Scan(&blocked); err != nil {
		return false, fmt.Errorf("check is blocked: %w", err)
	}
	return blocked, nil
}
