package postgresrepo_test

import (
	"context"
	"testing"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

func TestGoldenRule_ViewerCreatesShortcutInsteadOfMove(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()

	userRepo := postgresrepo.NewUserRepository(db)
	fileRepo := postgresrepo.NewFileRepository(db)
	permRepo := postgresrepo.NewPermissionRepository(db)

	// User A (Owner)
	userA := &domain.User{Email: "usera@example.com"}
	if err := userRepo.Create(ctx, userA); err != nil {
		t.Fatalf("create userA: %v", err)
	}

	// User B (Viewer)
	userB := &domain.User{Email: "userb@example.com"}
	if err := userRepo.Create(ctx, userB); err != nil {
		t.Fatalf("create userB: %v", err)
	}

	// User A creates a file
	fileA := &domain.File{
		UserID:      userA.ID,
		Name:        "shared_file.txt",
		IsDirectory: false,
		MimeType:    "text/plain",
	}
	if err := fileRepo.Create(ctx, fileA); err != nil {
		t.Fatalf("create fileA: %v", err)
	}

	// User B creates a private folder
	folderB := &domain.File{
		UserID:      userB.ID,
		Name:        "private_folder",
		IsDirectory: true,
	}
	if err := fileRepo.Create(ctx, folderB); err != nil {
		t.Fatalf("create folderB: %v", err)
	}

	// User A shares fileA with User B as VIEWER
	perm := &domain.Permission{
		FileID:       fileA.ID,
		GranteeEmail: userB.Email,
		Role:         domain.RoleViewer,
	}
	if err := permRepo.GrantPermission(ctx, perm); err != nil {
		t.Fatalf("create permission: %v", err)
	}

	// Act: User B attempts to bulk move fileA into folderB
	err := fileRepo.BulkMove(ctx, []string{fileA.ID}, &folderB.ID, userB.ID)
	if err != nil {
		t.Fatalf("BulkMove failed unexpectedly: %v", err)
	}

	// Assert: Original file should be UNTOUCHED (still belongs to User A, parent_id is nil)
	afterA, err := fileRepo.GetByID(ctx, fileA.ID)
	if err != nil {
		t.Fatalf("get fileA: %v", err)
	}
	if afterA.ParentID != nil {
		t.Errorf("expected fileA.ParentID to be nil, got %v", *afterA.ParentID)
	}
	if afterA.UserID != userA.ID {
		t.Errorf("expected fileA.UserID to be userA (%s), got %s", userA.ID, afterA.UserID)
	}

	// Assert: A shortcut should have been created in folderB
	children, err := fileRepo.ListDirectory(ctx, userB.ID, &folderB.ID)
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("expected 1 child in folderB, got %d", len(children))
	}
	shortcut := children[0]
	if shortcut.MimeType != "application/vnd.google-apps.shortcut" {
		t.Errorf("expected mime_type application/vnd.google-apps.shortcut, got %s", shortcut.MimeType)
	}
	if shortcut.TargetID == nil || *shortcut.TargetID != fileA.ID {
		t.Errorf("expected shortcut to point to %s, got %v", fileA.ID, shortcut.TargetID)
	}
}

func TestGoldenRule_EditorMovesFilePreservesOwnership(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()

	userRepo := postgresrepo.NewUserRepository(db)
	fileRepo := postgresrepo.NewFileRepository(db)
	permRepo := postgresrepo.NewPermissionRepository(db)

	userA := &domain.User{Email: "usera@example.com"}
	_ = userRepo.Create(ctx, userA)
	userB := &domain.User{Email: "userb@example.com"}
	_ = userRepo.Create(ctx, userB)

	fileA := &domain.File{
		UserID:      userA.ID,
		Name:        "shared_file.txt",
		IsDirectory: false,
		MimeType:    "text/plain",
	}
	_ = fileRepo.Create(ctx, fileA)

	folderB := &domain.File{
		UserID:      userB.ID,
		Name:        "private_folder",
		IsDirectory: true,
	}
	_ = fileRepo.Create(ctx, folderB)

	// User A shares fileA with User B as EDITOR
	perm := &domain.Permission{
		FileID:       fileA.ID,
		GranteeEmail: userB.Email,
		Role:         domain.RoleEditor,
	}
	_ = permRepo.GrantPermission(ctx, perm)

	// Act: User B moves fileA into folderB
	err := fileRepo.BulkMove(ctx, []string{fileA.ID}, &folderB.ID, userB.ID)
	if err != nil {
		t.Fatalf("BulkMove failed unexpectedly: %v", err)
	}

	// Assert: The file's parent_id is updated, but user_id still belongs to User A
	after, err := fileRepo.GetByID(ctx, fileA.ID)
	if err != nil {
		t.Fatalf("get fileA: %v", err)
	}
	if after.ParentID == nil || *after.ParentID != folderB.ID {
		t.Errorf("expected file to be moved to %s, got %v", folderB.ID, after.ParentID)
	}
	if after.UserID != userA.ID {
		t.Errorf("expected user_id to remain %s, got %s", userA.ID, after.UserID)
	}
}

func TestOrphanedFiles_FolderDeleteOrphansNestedUserFiles(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()

	userRepo := postgresrepo.NewUserRepository(db)
	fileRepo := postgresrepo.NewFileRepository(db)
	permRepo := postgresrepo.NewPermissionRepository(db)

	userA := &domain.User{Email: "usera@example.com"}
	_ = userRepo.Create(ctx, userA)
	userB := &domain.User{Email: "userb@example.com"}
	_ = userRepo.Create(ctx, userB)

	// User A creates Folder X
	folderX := &domain.File{
		UserID:      userA.ID,
		Name:        "Folder X",
		IsDirectory: true,
	}
	_ = fileRepo.Create(ctx, folderX)

	// User A shares Folder X with User B as EDITOR
	perm := &domain.Permission{
		FileID:       folderX.ID,
		GranteeEmail: userB.Email,
		Role:         domain.RoleEditor,
	}
	_ = permRepo.GrantPermission(ctx, perm)

	// User B uploads File Y inside Folder X
	fileY := &domain.File{
		UserID:      userB.ID,
		Name:        "File Y",
		ParentID:    &folderX.ID,
		IsDirectory: false,
	}
	_ = fileRepo.Create(ctx, fileY)

	// Act: User A soft-deletes Folder X
	err := fileRepo.SoftDelete(ctx, folderX.ID, userA.ID)
	if err != nil {
		t.Fatalf("SoftDelete failed: %v", err)
	}

	// Assert: Folder X is deleted
	afterX, _ := fileRepo.GetByID(ctx, folderX.ID)
	if afterX.DeletedAt == nil {
		t.Errorf("expected Folder X to be soft deleted")
	}

	// Assert: File Y survives and is orphaned (parent_id = NULL)
	afterY, _ := fileRepo.GetByID(ctx, fileY.ID)
	if afterY.DeletedAt != nil {
		t.Errorf("expected File Y to NOT be deleted")
	}
	if afterY.ParentID != nil {
		t.Errorf("expected File Y to be orphaned (parent_id = NULL), got %v", *afterY.ParentID)
	}
}
