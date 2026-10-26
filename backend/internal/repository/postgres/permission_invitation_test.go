package postgresrepo_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

func TestGetByIDForUpdateSerializesWithDeletion(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	owner := &domain.User{Email: fmt.Sprintf("row-lock-owner-%d@example.com", suffix), PasswordHash: "hash", IsVerified: true}
	if err := postgresrepo.NewUserRepository(db).Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	defer db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", owner.ID)

	file := &domain.File{UserID: owner.ID, Name: fmt.Sprintf("row-lock-file-%d.txt", suffix), Status: "ACTIVE"}
	files := postgresrepo.NewFileRepository(db)
	if err := files.Create(ctx, file); err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer db.ExecContext(context.Background(), "DELETE FROM files WHERE id = $1", file.ID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin lock transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := files.WithTx(tx).GetByIDForUpdate(ctx, file.ID); err != nil {
		t.Fatalf("lock file row: %v", err)
	}

	started := make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		close(started)
		_, err := db.ExecContext(ctx, "UPDATE files SET deleted_at = NOW() WHERE id = $1", file.ID)
		updated <- err
	}()
	<-started
	select {
	case err := <-updated:
		t.Fatalf("concurrent deletion passed row lock before transaction commit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit lock transaction: %v", err)
	}
	select {
	case err := <-updated:
		if err != nil {
			t.Fatalf("deletion after lock release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deletion remained blocked after lock transaction committed")
	}
	var deletedAt sql.NullTime
	if err := db.QueryRowContext(ctx, "SELECT deleted_at FROM files WHERE id = $1", file.ID).Scan(&deletedAt); err != nil || !deletedAt.Valid {
		t.Fatalf("expected deletion to commit after the lock: deleted=%v err=%v", deletedAt.Valid, err)
	}
}

func TestPermissionRowForUpdateSerializesRoleChanges(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	suffix := time.Now().UnixNano()
	owner := &domain.User{Email: fmt.Sprintf("role-lock-owner-%d@example.com", suffix), PasswordHash: "hash"}
	grantee := &domain.User{Email: fmt.Sprintf("role-lock-grantee-%d@example.com", suffix), PasswordHash: "hash"}
	for _, user := range []*domain.User{owner, grantee} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	file := &domain.File{UserID: owner.ID, Name: fmt.Sprintf("role-lock-%d.txt", suffix), Status: "ACTIVE"}
	if err := files.Create(ctx, file); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := perms.GrantPermission(ctx, &domain.Permission{
		FileID: file.ID, GranteeEmail: grantee.Email, Role: domain.RoleViewer,
		Status: domain.PermissionStatusAccepted,
	}); err != nil {
		t.Fatalf("grant permission: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer tx.Rollback()
	locked, err := perms.WithTx(tx).GetPermissionByFileAndGranteeForUpdate(ctx, file.ID, grantee.Email)
	if err != nil || locked.Role != domain.RoleViewer {
		t.Fatalf("lock grant: grant=%+v err=%v", locked, err)
	}
	started := make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		close(started)
		updated <- perms.UpdatePermission(ctx, file.ID, grantee.Email, domain.RoleOwner)
	}()
	<-started
	select {
	case err := <-updated:
		t.Fatalf("concurrent role change passed row lock before transaction commit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit lock transaction: %v", err)
	}
	select {
	case err := <-updated:
		if err != nil {
			t.Fatalf("role change after lock release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("role change remained blocked after transaction committed")
	}
	grant, err := perms.GetPermissionByFileAndGranteeForUpdate(ctx, file.ID, grantee.Email)
	if err != nil {
		t.Fatalf("load changed grant: %v", err)
	}
	if grant.Role != domain.RoleOwner {
		t.Fatalf("role = %q, want OWNER after waiting writer commits", grant.Role)
	}
}

func TestShareInvitationsWorkflow(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()

	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)

	suffix := time.Now().UnixNano()
	ownerEmail := fmt.Sprintf("inv-owner-%d@example.com", suffix)
	recipientEmail := fmt.Sprintf("inv-recipient-%d@example.com", suffix)

	owner := &domain.User{
		Email:        ownerEmail,
		PasswordHash: "hash",
		IsVerified:   true,
	}
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	recipient := &domain.User{
		Email:        recipientEmail,
		PasswordHash: "hash",
		IsVerified:   true,
	}
	if err := users.Create(ctx, recipient); err != nil {
		t.Fatalf("failed to create recipient: %v", err)
	}

	// Create test file owned by owner
	file := &domain.File{
		UserID:      owner.ID,
		Name:        fmt.Sprintf("confidential-doc-%d.pdf", suffix),
		SizeBytes:   1024,
		IsDirectory: false,
		Status:      "ACTIVE",
		MimeType:    "application/pdf",
	}
	if err := files.Create(ctx, file); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// 1. Check user permission initially: should be false
	hasAccess, err := perms.CheckUserPermission(ctx, file.ID, recipientEmail, []string{domain.RoleViewer, domain.RoleEditor})
	if err != nil {
		t.Fatalf("CheckUserPermission error: %v", err)
	}
	if hasAccess {
		t.Fatalf("expected recipient to NOT have access initially")
	}

	// 2. Grant permission in PENDING status (Share Invitation)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	note := "Please review this proposal."
	inv := &domain.Permission{
		FileID:       file.ID,
		GranteeEmail: recipientEmail,
		Role:         domain.RoleViewer,
		Status:       domain.PermissionStatusPending,
		Message:      &note,
		ExpiresAt:    &expiresAt,
		InvitedBy:    &owner.ID,
	}
	if err := perms.GrantPermission(ctx, inv); err != nil {
		t.Fatalf("failed to grant permission invitation: %v", err)
	}
	if inv.ID == "" {
		t.Fatalf("expected invitation ID to be populated")
	}

	// 3. CheckUserPermission: STILL false because status is PENDING!
	hasAccess, err = perms.CheckUserPermission(ctx, file.ID, recipientEmail, []string{domain.RoleViewer})
	if err != nil {
		t.Fatalf("CheckUserPermission error: %v", err)
	}
	if hasAccess {
		t.Fatalf("expected PENDING permission to not confer access")
	}

	// 4. List pending invitations for recipient
	pendingList, err := perms.ListPendingInvitations(ctx, recipientEmail)
	if err != nil {
		t.Fatalf("ListPendingInvitations error: %v", err)
	}
	if len(pendingList) == 0 {
		t.Fatalf("expected at least 1 pending invitation")
	}
	found := false
	for _, p := range pendingList {
		if p.ID == inv.ID {
			found = true
			if p.FileName != file.Name {
				t.Errorf("expected FileName %q, got %q", file.Name, p.FileName)
			}
			if p.SenderEmail != owner.Email {
				t.Errorf("expected SenderEmail %q, got %q", owner.Email, p.SenderEmail)
			}
			if p.Message == nil || *p.Message != note {
				t.Errorf("expected Message %q, got %v", note, p.Message)
			}
		}
	}
	if !found {
		t.Fatalf("newly created invitation not found in pending list")
	}

	// 5. Test blocking
	isBlocked, err := perms.IsBlocked(ctx, recipient.ID, owner.Email)
	if err != nil {
		t.Fatalf("IsBlocked error: %v", err)
	}
	if isBlocked {
		t.Fatalf("expected owner to not be blocked initially")
	}

	if err := perms.BlockUser(ctx, recipient.ID, owner.Email); err != nil {
		t.Fatalf("BlockUser error: %v", err)
	}

	isBlocked, err = perms.IsBlocked(ctx, recipient.ID, owner.Email)
	if err != nil {
		t.Fatalf("IsBlocked error: %v", err)
	}
	if !isBlocked {
		t.Fatalf("expected owner to be blocked after BlockUser")
	}

	// 6. Accept the invitation
	accepted, err := perms.RespondToInvitation(ctx, inv.ID, recipientEmail, domain.PermissionStatusAccepted)
	if err != nil {
		t.Fatalf("RespondToInvitation error: %v", err)
	}
	if accepted.Status != domain.PermissionStatusAccepted {
		t.Fatalf("expected status ACCEPTED, got %s", accepted.Status)
	}

	// 7. CheckUserPermission: NOW TRUE because it is ACCEPTED!
	hasAccess, err = perms.CheckUserPermission(ctx, file.ID, recipientEmail, []string{domain.RoleViewer})
	if err != nil {
		t.Fatalf("CheckUserPermission error: %v", err)
	}
	if !hasAccess {
		t.Fatalf("expected ACCEPTED permission to confer access")
	}

	// 8. FindCollaboratorUserIDs: should return both owner and recipient!
	collabs, err := perms.FindCollaboratorUserIDs(ctx, file.ID)
	if err != nil {
		t.Fatalf("FindCollaboratorUserIDs error: %v", err)
	}
	collabMap := make(map[string]bool)
	for _, c := range collabs {
		collabMap[c] = true
	}
	if !collabMap[owner.ID] {
		t.Errorf("expected owner %s in collaborators list, got %v", owner.ID, collabs)
	}
	if !collabMap[recipient.ID] {
		t.Errorf("expected recipient %s in collaborators list, got %v", recipient.ID, collabs)
	}

	// 9. Test folder inheritance: create folder shared with recipient, child file in folder
	folder := &domain.File{
		UserID:      owner.ID,
		Name:        fmt.Sprintf("shared-folder-%d", suffix),
		IsDirectory: true,
		Status:      "ACTIVE",
	}
	if err := files.Create(ctx, folder); err != nil {
		t.Fatalf("create folder error: %v", err)
	}
	if err := perms.GrantPermission(ctx, &domain.Permission{
		FileID:       folder.ID,
		GranteeEmail: recipientEmail,
		Role:         domain.RoleEditor,
		Status:       domain.PermissionStatusAccepted,
	}); err != nil {
		t.Fatalf("grant folder permission error: %v", err)
	}

	childFile := &domain.File{
		UserID:      owner.ID,
		ParentID:    &folder.ID,
		Name:        fmt.Sprintf("child-%d.txt", suffix),
		IsDirectory: false,
		Status:      "ACTIVE",
	}
	if err := files.Create(ctx, childFile); err != nil {
		t.Fatalf("create child file error: %v", err)
	}

	childCollabs, err := perms.FindCollaboratorUserIDs(ctx, childFile.ID)
	if err != nil {
		t.Fatalf("FindCollaboratorUserIDs for child error: %v", err)
	}
	childCollabMap := make(map[string]bool)
	for _, c := range childCollabs {
		childCollabMap[c] = true
	}
	if !childCollabMap[owner.ID] || !childCollabMap[recipient.ID] {
		t.Errorf("expected both owner and recipient for child file via inherited folder perm, got %v", childCollabs)
	}
}

func TestPermissionCheckShareLockSerializesWithRevoke(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	owner := &domain.User{Email: fmt.Sprintf("acl-lock-owner-%d@example.com", suffix), PasswordHash: "hash", IsVerified: true}
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	defer db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", owner.ID)

	grantee := &domain.User{Email: fmt.Sprintf("acl-lock-grantee-%d@example.com", suffix), PasswordHash: "hash", IsVerified: true}
	if err := users.Create(ctx, grantee); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	defer db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", grantee.ID)

	file := &domain.File{UserID: owner.ID, Name: fmt.Sprintf("acl-lock-file-%d.txt", suffix), Status: "ACTIVE"}
	if err := files.Create(ctx, file); err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer db.ExecContext(context.Background(), "DELETE FROM files WHERE id = $1", file.ID)
	if err := perms.GrantPermission(ctx, &domain.Permission{
		FileID: file.ID, GranteeEmail: grantee.Email, Role: domain.RoleEditor,
		Status: domain.PermissionStatusAccepted,
	}); err != nil {
		t.Fatalf("grant permission: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin mutation transaction: %v", err)
	}
	defer tx.Rollback()
	allowed, err := perms.WithTx(tx).CheckUserPermission(ctx, file.ID, grantee.Email, []string{domain.RoleEditor})
	if err != nil || !allowed {
		t.Fatalf("transactional permission check: allowed=%v err=%v", allowed, err)
	}

	started := make(chan struct{})
	revoked := make(chan error, 1)
	go func() {
		close(started)
		revoked <- perms.RevokePermission(ctx, file.ID, grantee.Email)
	}()
	<-started
	select {
	case err := <-revoked:
		t.Fatalf("revoke passed permission row lock before mutation commit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit mutation transaction: %v", err)
	}
	select {
	case err := <-revoked:
		if err != nil {
			t.Fatalf("revoke after mutation commit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoke remained blocked after mutation transaction committed")
	}
	allowed, err = perms.CheckUserPermission(ctx, file.ID, grantee.Email, []string{domain.RoleEditor})
	if err != nil || allowed {
		t.Fatalf("permission after revoke: allowed=%v err=%v", allowed, err)
	}
}
