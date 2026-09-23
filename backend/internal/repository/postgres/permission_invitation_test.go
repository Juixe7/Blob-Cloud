package postgresrepo_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

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
}
