// Integration tests for the Postgres repository layer. These run against a real
// database (configured via DB_DSN, defaulting to the local Docker container).
//
// Run with:   go test ./internal/repository/postgres/...
// They are skipped automatically when the database is unreachable, so the rest
// of the suite still passes in DB-less environments.
package postgresrepo_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	// Register the pgx driver under the name "pgx" for database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

// dsn returns the connection string to use: DB_DSN env var or the default that
// matches the Docker container we start in the test instructions.
func dsn() string {
	if v := os.Getenv("DB_DSN"); v != "" {
		return v
	}
	return "postgres://postgres:postgres@localhost:5432/godrive?sslmode=disable"
}

// openDB returns a connected pool or skips the test if Postgres is unavailable.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use the registered "pgx" driver via the standard library, exactly like
	// the production database.New function does.
	db, err := sql.Open("pgx", dsn())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(5)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		t.Skipf("postgres unavailable, skipping integration test: %v", err)
	}
	return db
}

// freshSchema runs migrations and truncates all tables so each subtest starts
// from a clean slate.
func freshSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.RunMigrations(ctx, db, log); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	for _, tbl := range []string{
		"session_blocks", "upload_sessions", "permissions",
		"file_blocks", "blocks", "files", "users",
	} {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", tbl)); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

func TestMigrations_CreateAllTables(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	for _, tbl := range []string{"users", "files", "blocks", "file_blocks"} {
		var exists bool
		err := db.QueryRowContext(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, "public."+tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("expected table %s to exist", tbl)
		}
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Running migrations a second time must be a no-op, not an error.
	if err := database.RunMigrations(context.Background(), db, log); err != nil {
		t.Fatalf("second RunMigrations should be idempotent: %v", err)
	}
}

func TestUserRepository_CreateAndGetByEmail(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	repo := postgresrepo.NewUserRepository(db)

	u := &domain.User{Email: "alice@example.com", PasswordHash: "hashed-secret"}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == "" || u.CreatedAt.IsZero() {
		t.Fatalf("Create did not populate id/created_at: %+v", u)
	}

	got, err := repo.GetByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != u.ID || got.PasswordHash != "hashed-secret" {
		t.Fatalf("round-trip mismatch: got %+v", got)
	}
}

func TestUserRepository_GetByEmail_NotFound(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	repo := postgresrepo.NewUserRepository(db)
	_, err := repo.GetByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestFileRepository_CreateGetList(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)

	u := &domain.User{Email: "bob@example.com", PasswordHash: "x"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	// Root-level entries (parent_id NULL).
	doc := &domain.File{UserID: u.ID, Name: "report.pdf", IsDirectory: false, SizeBytes: 1024}
	folder := &domain.File{UserID: u.ID, Name: "Photos", IsDirectory: true}
	if err := files.Create(ctx, doc); err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if err := files.Create(ctx, folder); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if doc.ID == "" {
		t.Fatal("file id not populated")
	}

	got, err := files.GetByID(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "report.pdf" || got.SizeBytes != 1024 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// List root: should contain both entries.
	rootEntries, err := files.ListDirectory(ctx, u.ID, nil)
	if err != nil {
		t.Fatalf("ListDirectory root: %v", err)
	}
	if len(rootEntries) != 2 {
		t.Fatalf("expected 2 root entries, got %d", len(rootEntries))
	}

	// Put a file inside the folder, then list the folder.
	inner := &domain.File{UserID: u.ID, Name: "pic.png", ParentID: &folder.ID, SizeBytes: 50}
	if err := files.Create(ctx, inner); err != nil {
		t.Fatalf("create inner: %v", err)
	}
	innerEntries, err := files.ListDirectory(ctx, u.ID, &folder.ID)
	if err != nil {
		t.Fatalf("ListDirectory folder: %v", err)
	}
	if len(innerEntries) != 1 || innerEntries[0].Name != "pic.png" {
		t.Fatalf("expected 1 inner entry 'pic.png', got %+v", innerEntries)
	}
}

func TestBlockRepository_CreateGetAndLink(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	blocks := postgresrepo.NewBlockRepository(db)

	u := &domain.User{Email: "carol@example.com", PasswordHash: "x"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	f := &domain.File{UserID: u.ID, Name: "big.bin", SizeBytes: 8}
	if err := files.Create(ctx, f); err != nil {
		t.Fatal(err)
	}

	// Create two distinct blocks.
	b1 := &domain.Block{SHA256: "a" + strings_64a, SizeBytes: 4}
	b2 := &domain.Block{SHA256: "b" + strings_64b, SizeBytes: 4}
	for _, b := range []*domain.Block{b1, b2} {
		if err := blocks.Create(ctx, b); err != nil {
			t.Fatalf("create block: %v", err)
		}
	}

	// GetByHash round-trip.
	got, err := blocks.GetByHash(ctx, b1.SHA256)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if got.ID != b1.ID {
		t.Fatalf("id mismatch: %s vs %s", got.ID, b1.ID)
	}

	// GetMultipleByHashes returns both.
	many, err := blocks.GetMultipleByHashes(ctx, []string{b1.SHA256, b2.SHA256})
	if err != nil {
		t.Fatalf("GetMultipleByHashes: %v", err)
	}
	if len(many) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(many))
	}

	// LinkBlocksToFile wires the ordered sequence atomically.
	seq := []domain.BlockSequence{
		{BlockID: b1.ID, SequenceNumber: 0},
		{BlockID: b2.ID, SequenceNumber: 1},
	}
	if err := blocks.LinkBlocksToFile(ctx, f.ID, seq); err != nil {
		t.Fatalf("LinkBlocksToFile: %v", err)
	}

	// Verify the links landed in order.
	rows, err := db.QueryContext(ctx,
		`SELECT block_id, sequence_number FROM file_blocks WHERE file_id=$1 ORDER BY sequence_number`, f.ID)
	if err != nil {
		t.Fatalf("query file_blocks: %v", err)
	}
	defer rows.Close()
	var (
		gotSeq []int
		gotIDs []string
	)
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		gotIDs = append(gotIDs, id)
		gotSeq = append(gotSeq, n)
	}
	if len(gotSeq) != 2 || gotSeq[0] != 0 || gotSeq[1] != 1 {
		t.Fatalf("sequence mismatch: %v", gotSeq)
	}
	if gotIDs[0] != b1.ID || gotIDs[1] != b2.ID {
		t.Fatalf("block order mismatch: %v", gotIDs)
	}
}

// strings_64a/b are 63-char padders so SHA256 is exactly 64 chars (matches the
// column width and exercises the unique index).
const (
	strings_64a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	strings_64b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// ===========================================================================
// Phase 3: upload sessions, permissions, and the recursive permission CTE.
// ===========================================================================

func TestUploadSession_CreateAndGet(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	sessions := postgresrepo.NewUploadSessionRepository(db)

	u := &domain.User{Email: "uploader@example.com", PasswordHash: "x"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	session := &domain.UploadSession{
		UserID: u.ID, Filename: "doc.pdf", TotalSize: 8,
		Status: domain.SessionStatusInitiated,
	}
	blocks := []domain.SessionBlock{
		{BlockHash: "1" + strings_64a, SequenceNumber: 0, SizeBytes: 4, IsUploaded: false},
		{BlockHash: "2" + strings_64b, SequenceNumber: 1, SizeBytes: 4, IsUploaded: true},
	}
	if err := sessions.CreateSession(ctx, session, blocks); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ID == "" {
		t.Fatal("session id not populated")
	}

	got, gotBlocks, err := sessions.GetSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSessionByID: %v", err)
	}
	if got.Status != domain.SessionStatusInitiated {
		t.Fatalf("status = %s", got.Status)
	}
	if len(gotBlocks) != 2 || gotBlocks[0].SequenceNumber != 0 || gotBlocks[1].SequenceNumber != 1 {
		t.Fatalf("blocks out of order / count: %+v", gotBlocks)
	}
	if !gotBlocks[1].IsUploaded || gotBlocks[0].IsUploaded {
		t.Fatalf("is_uploaded flags wrong: %+v", gotBlocks)
	}
}

func TestUploadSession_MarkBlockUploadedAndStatus(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	sessions := postgresrepo.NewUploadSessionRepository(db)

	u := &domain.User{Email: "u@example.com", PasswordHash: "x"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	session := &domain.UploadSession{UserID: u.ID, Filename: "f", TotalSize: 4, Status: domain.SessionStatusInitiated}
	if err := sessions.CreateSession(ctx, session, []domain.SessionBlock{
		{BlockHash: "z" + strings_64a, SequenceNumber: 0, SizeBytes: 4, IsUploaded: false},
	}); err != nil {
		t.Fatal(err)
	}

	if err := sessions.MarkBlockUploaded(ctx, session.ID, 0); err != nil {
		t.Fatalf("MarkBlockUploaded: %v", err)
	}
	_, blocks, _ := sessions.GetSessionByID(ctx, session.ID)
	if !blocks[0].IsUploaded {
		t.Fatal("block should be marked uploaded")
	}

	if err := sessions.UpdateSessionStatus(ctx, session.ID, domain.SessionStatusCompleted); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	got, _, _ := sessions.GetSessionByID(ctx, session.ID)
	if got.Status != domain.SessionStatusCompleted {
		t.Fatalf("status = %s, want COMPLETED", got.Status)
	}
}

func TestBlockRepository_GetOrCreate_Dedup(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	blocks := postgresrepo.NewBlockRepository(db)

	// First GetOrCreate inserts.
	b1 := &domain.Block{SHA256: "g" + strings_64a, SizeBytes: 4}
	if err := blocks.GetOrCreate(ctx, b1); err != nil {
		t.Fatalf("GetOrCreate insert: %v", err)
	}
	// Second call with the same hash returns the SAME id (deduplication).
	b2 := &domain.Block{SHA256: "g" + strings_64a, SizeBytes: 4}
	if err := blocks.GetOrCreate(ctx, b2); err != nil {
		t.Fatalf("GetOrCreate dedup: %v", err)
	}
	if b1.ID != b2.ID {
		t.Fatalf("dedup returned different ids: %s vs %s", b1.ID, b2.ID)
	}

	// And the table still has exactly one row for that hash.
	count := 0
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks WHERE sha256=$1`, b1.SHA256).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 block row, got %d", count)
	}
}

func TestPermission_GrantListAndDirectCheck(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)

	u := &domain.User{Email: "owner@example.com", PasswordHash: "x"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	f := &domain.File{UserID: u.ID, Name: "shared.txt", SizeBytes: 1}
	if err := files.Create(ctx, f); err != nil {
		t.Fatal(err)
	}

	if err := perms.GrantPermission(ctx, &domain.Permission{
		FileID: f.ID, GranteeEmail: "friend@example.com", Role: domain.RoleViewer,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	list, err := perms.GetPermissionsByFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetPermissionsByFile: %v", err)
	}
	if len(list) != 1 || list[0].GranteeEmail != "friend@example.com" {
		t.Fatalf("permission list wrong: %+v", list)
	}

	allowed, err := perms.CheckUserPermission(ctx, f.ID, "friend@example.com", []string{domain.RoleViewer})
	if err != nil {
		t.Fatalf("CheckUserPermission: %v", err)
	}
	if !allowed {
		t.Fatal("viewer should be allowed for a VIEWER check")
	}

	// A different email is not allowed.
	allowed, _ = perms.CheckUserPermission(ctx, f.ID, "stranger@example.com", []string{domain.RoleViewer})
	if allowed {
		t.Fatal("stranger should not be allowed")
	}
}

// TestPermission_RecursiveCTE verifies the headline behaviour: a permission on
// an ancestor folder satisfies a check on a descendant file.
func TestPermission_RecursiveCTE(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)

	u := &domain.User{Email: "rootowner@example.com", PasswordHash: "x"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	// folder -> innerFolder -> file : a three-level hierarchy.
	folder := &domain.File{UserID: u.ID, Name: "Docs", IsDirectory: true}
	if err := files.Create(ctx, folder); err != nil {
		t.Fatal(err)
	}
	inner := &domain.File{UserID: u.ID, Name: "Work", IsDirectory: true, ParentID: &folder.ID}
	if err := files.Create(ctx, inner); err != nil {
		t.Fatal(err)
	}
	leaf := &domain.File{UserID: u.ID, Name: "resume.pdf", ParentID: &inner.ID, SizeBytes: 10}
	if err := files.Create(ctx, leaf); err != nil {
		t.Fatal(err)
	}

	// Grant VIEWER on the TOP folder to someone.
	if err := perms.GrantPermission(ctx, &domain.Permission{
		FileID: folder.ID, GranteeEmail: "coworker@example.com", Role: domain.RoleViewer,
	}); err != nil {
		t.Fatal(err)
	}

	// The coworker should be allowed on the deeply-nested leaf via the CTE.
	allowed, err := perms.CheckUserPermission(ctx, leaf.ID, "coworker@example.com", []string{domain.RoleViewer})
	if err != nil {
		t.Fatalf("CheckUserPermission: %v", err)
	}
	if !allowed {
		t.Fatal("recursive CTE failed: permission on ancestor folder should cover descendant file")
	}

	// An EDITOR requirement is NOT satisfied by a VIEWER grant (rank check).
	allowed, _ = perms.CheckUserPermission(ctx, leaf.ID, "coworker@example.com", []string{domain.RoleEditor})
	if allowed {
		t.Fatal("VIEWER grant must not satisfy an EDITOR requirement")
	}
	// ...but it IS satisfied if EDITOR+OWNER are both acceptable.
	allowed, _ = perms.CheckUserPermission(ctx, leaf.ID, "coworker@example.com", []string{domain.RoleViewer, domain.RoleEditor})
	if !allowed {
		t.Fatal("VIEWER should satisfy a [VIEWER,EDITOR] requirement")
	}
}

