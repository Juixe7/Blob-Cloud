package httpx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
	"go-drive-clone/internal/storage"
)

func TestShareInvitationEndpoints_Unauthenticated(t *testing.T) {
	srv := NewServer(nil, nil)

	r := chi.NewRouter()
	r.Route("/api/shares/invitations", func(r chi.Router) {
		r.Get("/", srv.HandleListInvitations)
		r.Post("/{id}/accept", srv.HandleAcceptInvitation)
		r.Post("/{id}/decline", srv.HandleDeclineInvitation)
		r.Post("/{id}/block", srv.HandleBlockSender)
		r.Get("/{id}/preview", srv.HandlePreviewInvitation)
	})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"ListInvitations", http.MethodGet, "/api/shares/invitations"},
		{"AcceptInvitation", http.MethodPost, "/api/shares/invitations/inv-123/accept"},
		{"DeclineInvitation", http.MethodPost, "/api/shares/invitations/inv-123/decline"},
		{"BlockSender", http.MethodPost, "/api/shares/invitations/inv-123/block"},
		{"PreviewInvitation", http.MethodGet, "/api/shares/invitations/inv-123/preview"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusUnauthorized {
				t.Fatalf("expected status 503 or 401, got %d", w.Code)
			}
		})
	}
}

func TestReadMetadataEndpointsFailClosedWithoutAuthorizationService(t *testing.T) {
	srv := NewServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithUploads(nil, postgresrepo.NewPermissionRepository(nil))

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"ListPermissions", srv.HandleListPermissions},
		{"FileHistory", srv.HandleFileHistory},
		{"ListFileVersions", srv.HandleListFileVersions},
		{"RestoreFileVersion", srv.HandleRestoreFileVersion},
		{"CreateShare", srv.HandleShare},
		{"UpdateShare", srv.HandleUpdateShare},
		{"RevokeShare", srv.HandleRevokeShare},
		{"AcceptInvitation", srv.HandleAcceptInvitation},
		{"DeclineInvitation", srv.HandleDeclineInvitation},
		{"BlockInvitationSender", srv.HandleBlockSender},
		{"PreviewInvitation", srv.HandlePreviewInvitation},
		{"RemoveSharedWithMe", srv.HandleRemoveSharedWithMe},
		{"CreatePublicLink", srv.HandleCreatePublicLink},
		{"RevokePublicLink", srv.HandleRevokePublicLink},
		{"GetPublicShare", srv.HandleGetPublicShare},
		{"VerifyPublicShare", srv.HandleVerifyPublicShare},
		{"DownloadPublicShare", srv.HandlePublicDownload},
		{"RecordPublicView", srv.HandlePublicRecordView},
		{"SavePublicShortcut", srv.HandlePublicSaveShortcut},
		{"GetThumbnail", srv.HandleGetThumbnail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/files/file-id/"+strings.ToLower(tc.name), nil)
			routeCtx := chi.NewRouteContext()
			routeCtx.URLParams.Add("id", "file-id")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
			w := httptest.NewRecorder()
			tc.handler(w, req)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 when authorization service is unavailable; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleShare_InvalidPayload(t *testing.T) {
	srv := NewServer(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/files/test-file/share", bytes.NewReader([]byte("{invalid json}")))
	w := httptest.NewRecorder()
	srv.HandleShare(w, req)

	// Since perms is nil, it safely returns 503
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", w.Code)
	}

	var res map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if res["error"] == "" {
		t.Fatalf("expected error message in response body")
	}
}

func TestHandleShare_JournalIsAtomicAndVisiblePerUser(t *testing.T) {
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	if err := database.RunMigrations(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	journal := postgresrepo.NewJournalRepository(db)
	owner := &domain.User{Email: "share-owner-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	recipient := &domain.User{Email: "share-recipient-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	for _, user := range []*domain.User{owner, recipient} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	file := &domain.File{UserID: owner.ID, Name: "shared-" + suffix + ".txt", MimeType: "text/plain", Status: "ACTIVE"}
	if err := files.Create(ctx, file); err != nil {
		t.Fatal(err)
	}
	secret := "share-test-secret-key-with-sufficient-length"
	token, err := auth.CreateToken(secret, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	fileSvc := service.NewFileService(db, users, files, nil, perms, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithJournal(journal)
	srv := NewServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDatabase(db).WithUsers(users).WithUploads(nil, perms).
		WithFileOperations(fileSvc, files, nil).WithJournal(journal).
		WithRealtime(nil, secret, nil)

	request := func(email string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"grantee_email":%q,"role":"VIEWER"}`, email)
		req := httptest.NewRequest(http.MethodPost, "/api/files/"+file.ID+"/share", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", file.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		w := httptest.NewRecorder()
		srv.HandleShare(w, req)
		return w
	}

	response := request(recipient.Email)
	if response.Code != http.StatusCreated {
		t.Fatalf("share failed: status=%d body=%s", response.Code, response.Body.String())
	}
	ownerEvents, _, _, err := journal.ListSince(ctx, owner.ID, 0, 10)
	if err != nil || len(ownerEvents) != 1 || ownerEvents[0].Action != domain.ActionShareInvitation {
		t.Fatalf("owner did not receive share delta: entries=%+v err=%v", ownerEvents, err)
	}
	recipientEvents, _, _, err := journal.ListSince(ctx, recipient.ID, 0, 10)
	if err != nil || len(recipientEvents) != 1 || recipientEvents[0].Action != domain.ActionShareInvitation {
		t.Fatalf("recipient did not receive invitation delta: entries=%+v err=%v", recipientEvents, err)
	}

	triggerSuffix := suffix
	functionName := "reject_journal_" + triggerSuffix
	triggerName := "reject_journal_insert_" + triggerSuffix
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION `+functionName+` () RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected journal failure'; END; $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON journal_entries FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON journal_entries`)
		_, _ = db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`() `)
	}()

	failedRecipient := "share-failure-" + suffix + "@example.com"
	failed := request(failedRecipient)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("expected journal failure response, got %d: %s", failed.Code, failed.Body.String())
	}
	var permissionCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM permissions WHERE file_id=$1 AND LOWER(grantee_email)=LOWER($2)`, file.ID, failedRecipient).Scan(&permissionCount); err != nil {
		t.Fatal(err)
	}
	if permissionCount != 0 {
		t.Fatalf("permission mutation committed without journal event: count=%d", permissionCount)
	}
}

func TestHandleRevokePublicLink_IsAtomicWithJournal(t *testing.T) {
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	if err := database.RunMigrations(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	links := postgresrepo.NewShareableLinkRepository(db)
	journal := postgresrepo.NewJournalRepository(db)
	owner := &domain.User{Email: "link-owner-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	if err := users.Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	file := &domain.File{UserID: owner.ID, Name: "linked-" + suffix + ".txt", Status: "ACTIVE"}
	if err := files.Create(ctx, file); err != nil {
		t.Fatal(err)
	}
	link := &domain.ShareableLink{FileID: file.ID, LinkToken: "revoke-success-" + suffix, AccessTier: domain.RoleViewer}
	if err := links.Create(ctx, link); err != nil {
		t.Fatal(err)
	}
	secret := "public-link-test-secret-with-enough-entropy"
	token, err := auth.CreateToken(secret, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	fileSvc := service.NewFileService(db, users, files, nil, perms, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithJournal(journal)
	srv := NewServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDatabase(db).WithUsers(users).WithUploads(nil, perms).
		WithFileOperations(fileSvc, files, nil).WithShareableLinks(links).
		WithJournal(journal).WithRealtime(nil, secret, nil)
	call := func(linkToken string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/files/"+file.ID+"/links/"+linkToken, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", file.ID)
		routeCtx.URLParams.Add("token", linkToken)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		w := httptest.NewRecorder()
		srv.HandleRevokePublicLink(w, req)
		return w
	}
	response := call(link.LinkToken)
	if response.Code != http.StatusOK {
		t.Fatalf("revoke failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := links.GetByToken(ctx, link.LinkToken); err == nil {
		t.Fatal("revoked link still exists")
	}
	events, _, _, err := journal.ListSince(ctx, owner.ID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Action != domain.ActionPublicLinkRevoked {
		t.Fatalf("owner missing public-link revoke event: events=%+v err=%v", events, err)
	}

	failedLink := &domain.ShareableLink{FileID: file.ID, LinkToken: "revoke-failure-" + suffix, AccessTier: domain.RoleViewer}
	if err := links.Create(ctx, failedLink); err != nil {
		t.Fatal(err)
	}
	functionName := "reject_link_journal_" + suffix
	triggerName := "reject_link_journal_insert_" + suffix
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION `+functionName+` () RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected journal failure'; END; $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON journal_entries FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON journal_entries`)
		_, _ = db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`() `)
	}()
	failed := call(failedLink.LinkToken)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("expected journal failure, got %d: %s", failed.Code, failed.Body.String())
	}
	if _, err := links.GetByToken(ctx, failedLink.LinkToken); err != nil {
		t.Fatalf("link deletion committed despite journal failure: %v", err)
	}
}

func TestEditorCannotGrantOrModifyOwnerRole(t *testing.T) {
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	if err := database.RunMigrations(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	owner := &domain.User{Email: "role-owner-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	editor := &domain.User{Email: "role-editor-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	viewer := &domain.User{Email: "role-viewer-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	for _, user := range []*domain.User{owner, editor, viewer} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	file := &domain.File{UserID: owner.ID, Name: "role-test-" + suffix + ".txt", Status: "ACTIVE"}
	if err := files.Create(ctx, file); err != nil {
		t.Fatal(err)
	}
	for _, grant := range []*domain.Permission{
		{FileID: file.ID, GranteeEmail: editor.Email, Role: domain.RoleEditor, Status: domain.PermissionStatusAccepted},
		{FileID: file.ID, GranteeEmail: viewer.Email, Role: domain.RoleViewer, Status: domain.PermissionStatusPending},
		{FileID: file.ID, GranteeEmail: owner.Email, Role: domain.RoleOwner, Status: domain.PermissionStatusAccepted},
	} {
		if err := perms.GrantPermission(ctx, grant); err != nil {
			t.Fatal(err)
		}
	}
	secret := "role-escalation-test-secret-with-enough-length"
	token, err := auth.CreateToken(secret, editor.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal := postgresrepo.NewJournalRepository(db)
	fileSvc := service.NewFileService(db, users, files, nil, perms, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithJournal(journal)
	srv := NewServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDatabase(db).WithUsers(users).WithUploads(nil, perms).
		WithFileOperations(fileSvc, files, nil).WithJournal(journal).
		WithRealtime(nil, secret, nil)
	call := func(method, path, body string, params map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		routeCtx := chi.NewRouteContext()
		for key, value := range params {
			routeCtx.URLParams.Add(key, value)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		w := httptest.NewRecorder()
		if method == http.MethodPost {
			srv.HandleShare(w, req)
		} else if method == http.MethodPatch {
			srv.HandleUpdateShare(w, req)
		} else {
			srv.HandleRevokeShare(w, req)
		}
		return w
	}
	if response := call(http.MethodPost, "/api/files/"+file.ID+"/share", fmt.Sprintf(`{"grantee_email":%q,"role":"OWNER"}`, "new-owner-"+suffix+"@example.com"), map[string]string{"id": file.ID}); response.Code != http.StatusForbidden {
		t.Fatalf("editor OWNER invitation got status %d, want 403: %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPatch, "/api/files/"+file.ID+"/share/"+viewer.Email, `{"role":"OWNER"}`, map[string]string{"id": file.ID, "email": viewer.Email}); response.Code != http.StatusForbidden {
		t.Fatalf("editor promotion to OWNER got status %d, want 403: %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodDelete, "/api/files/"+file.ID+"/share/"+owner.Email, "", map[string]string{"id": file.ID, "email": owner.Email}); response.Code != http.StatusForbidden {
		t.Fatalf("editor revoking OWNER got status %d, want 403: %s", response.Code, response.Body.String())
	}
	grants, err := perms.GetPermissionsByFile(ctx, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if strings.EqualFold(grant.GranteeEmail, viewer.Email) && grant.Role != domain.RoleViewer {
			t.Fatalf("viewer role changed after denied escalation: %s", grant.Role)
		}
	}
}

func TestShortcutCannotBeSharedOrPublishedThroughPublicLink(t *testing.T) {
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	if err := database.RunMigrations(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	links := postgresrepo.NewShareableLinkRepository(db)
	owner := &domain.User{Email: "shortcut-owner-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	targetOwner := &domain.User{Email: "shortcut-target-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	recipient := &domain.User{Email: "shortcut-recipient-" + suffix + "@example.com", PasswordHash: "test", IsVerified: true}
	for _, user := range []*domain.User{owner, targetOwner, recipient} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	target := &domain.File{UserID: targetOwner.ID, Name: "private-target-" + suffix + ".txt", MimeType: "text/plain", Status: "ACTIVE"}
	if err := files.Create(ctx, target); err != nil {
		t.Fatal(err)
	}
	targetID := target.ID
	shortcut := &domain.File{UserID: owner.ID, Name: "target-shortcut-" + suffix + ".txt", MimeType: "text/plain", Status: "ACTIVE", TargetID: &targetID}
	if err := files.Create(ctx, shortcut); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	pendingInvitation := &domain.Permission{
		FileID: shortcut.ID, GranteeEmail: recipient.Email, Role: domain.RoleViewer,
		Status: domain.PermissionStatusPending, InvitedBy: &owner.ID, ExpiresAt: &expiresAt,
	}
	if err := perms.GrantPermission(ctx, pendingInvitation); err != nil {
		t.Fatal(err)
	}
	legacyLink := &domain.ShareableLink{FileID: shortcut.ID, LinkToken: "legacy-shortcut-" + suffix, AccessTier: domain.RoleViewer}
	if err := links.Create(ctx, legacyLink); err != nil {
		t.Fatal(err)
	}

	secret := "shortcut-public-link-test-secret-with-enough-entropy"
	token, err := auth.CreateToken(secret, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal := postgresrepo.NewJournalRepository(db)
	blocks := postgresrepo.NewBlockRepository(db)
	fileSvc := service.NewFileService(db, users, files, nil, perms, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithJournal(journal)
	localStore, err := storage.NewLocalStore(t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(localStore, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDatabase(db).WithUsers(users).WithUploads(nil, perms).
		WithFileOperations(fileSvc, files, blocks).WithShareableLinks(links).
		WithJournal(journal).WithRealtime(nil, secret, nil).
		WithZipOperations(service.NewZipService(files, postgresrepo.NewBlockRepository(db), nil))

	call := func(handler http.HandlerFunc, method, path, body, linkToken string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", shortcut.ID)
		routeCtx.URLParams.Add("token", linkToken)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		w := httptest.NewRecorder()
		handler(w, req)
		return w
	}
	if response := call(srv.HandleShare, http.MethodPost, "/api/files/"+shortcut.ID+"/share", fmt.Sprintf(`{"grantee_email":%q,"role":"VIEWER"}`, recipient.Email), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("sharing shortcut: status=%d body=%s, want 400", response.Code, response.Body.String())
	}
	if response := call(srv.HandleCreatePublicLink, http.MethodPost, "/api/files/"+shortcut.ID+"/links", `{"access_tier":"VIEWER"}`, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("creating public link for shortcut: status=%d body=%s, want 400", response.Code, response.Body.String())
	}
	recipientToken, err := auth.CreateToken(secret, recipient.ID)
	if err != nil {
		t.Fatal(err)
	}
	acceptReq := httptest.NewRequest(http.MethodPost, "/api/shares/invitations/"+pendingInvitation.ID+"/accept", nil)
	acceptReq.Header.Set("Authorization", "Bearer "+recipientToken)
	acceptCtx := chi.NewRouteContext()
	acceptCtx.URLParams.Add("id", pendingInvitation.ID)
	acceptReq = acceptReq.WithContext(context.WithValue(acceptReq.Context(), chi.RouteCtxKey, acceptCtx))
	acceptResp := httptest.NewRecorder()
	srv.HandleAcceptInvitation(acceptResp, acceptReq)
	if acceptResp.Code != http.StatusBadRequest {
		t.Fatalf("accepting legacy shortcut invitation: status=%d body=%s, want 400", acceptResp.Code, acceptResp.Body.String())
	}
	previewReq := httptest.NewRequest(http.MethodGet, "/api/shares/invitations/"+pendingInvitation.ID+"/preview?token="+recipientToken, nil)
	previewCtx := chi.NewRouteContext()
	previewCtx.URLParams.Add("id", pendingInvitation.ID)
	previewReq = previewReq.WithContext(context.WithValue(previewReq.Context(), chi.RouteCtxKey, previewCtx))
	previewResp := httptest.NewRecorder()
	srv.HandlePreviewInvitation(previewResp, previewReq)
	if previewResp.Code != http.StatusNotFound {
		t.Fatalf("previewing legacy shortcut invitation: status=%d body=%s, want 404", previewResp.Code, previewResp.Body.String())
	}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
	}{
		{"public metadata", srv.HandleGetPublicShare, http.MethodGet},
		{"public download", srv.HandlePublicDownload, http.MethodGet},
		{"save public shortcut", srv.HandlePublicSaveShortcut, http.MethodPost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := call(tc.handler, tc.method, "/api/public/shares/"+legacyLink.LinkToken, "", legacyLink.LinkToken)
			if response.Code != http.StatusNotFound {
				t.Fatalf("legacy public link to shortcut: status=%d body=%s, want 404", response.Code, response.Body.String())
			}
		})
	}
}

func TestListPermissionsRestrictsGrantRosterToShareManagers(t *testing.T) {
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	if err := database.RunMigrations(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	owner := &domain.User{Email: "acl-roster-owner-" + suffix + "@example.com", PasswordHash: "test"}
	editor := &domain.User{Email: "acl-roster-editor-" + suffix + "@example.com", PasswordHash: "test"}
	viewer := &domain.User{Email: "acl-roster-viewer-" + suffix + "@example.com", PasswordHash: "test"}
	foreign := &domain.User{Email: "acl-roster-foreign-" + suffix + "@example.com", PasswordHash: "test"}
	for _, user := range []*domain.User{owner, editor, viewer, foreign} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	file := &domain.File{UserID: owner.ID, Name: "acl-roster-" + suffix, Status: "ACTIVE"}
	if err := files.Create(ctx, file); err != nil {
		t.Fatal(err)
	}
	for _, grant := range []*domain.Permission{
		{FileID: file.ID, GranteeEmail: editor.Email, Role: domain.RoleEditor, Status: domain.PermissionStatusAccepted},
		{FileID: file.ID, GranteeEmail: viewer.Email, Role: domain.RoleViewer, Status: domain.PermissionStatusAccepted},
	} {
		if err := perms.GrantPermission(ctx, grant); err != nil {
			t.Fatal(err)
		}
	}
	secret := "acl-roster-test-secret-with-enough-entropy"
	fileSvc := service.NewFileService(db, users, files, nil, perms, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := NewServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDatabase(db).WithUsers(users).WithUploads(nil, perms).
		WithFileOperations(fileSvc, files, nil).WithRealtime(nil, secret, nil)
	for _, tc := range []struct {
		userID string
		want   int
	}{
		{owner.ID, http.StatusOK},
		{editor.ID, http.StatusOK},
		{viewer.ID, http.StatusForbidden},
		{foreign.ID, http.StatusForbidden},
	} {
		token, err := auth.CreateToken(secret, tc.userID)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/files/"+file.ID+"/permissions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", file.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		w := httptest.NewRecorder()
		srv.HandleListPermissions(w, req)
		if w.Code != tc.want {
			t.Errorf("caller %s got %d, want %d: %s", tc.userID, w.Code, tc.want, w.Body.String())
		}
	}
}
