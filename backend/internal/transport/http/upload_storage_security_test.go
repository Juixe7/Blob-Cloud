package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/storage"
)

func TestLocalStorageRoutesRejectUnsignedAndCanonicalBlockWrites(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://localhost", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	router := NewRouter(NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil))), RateLimiters{})

	stagingURL, err := store.GenerateStagingUploadURL(context.Background(), "staging/session-id/0", time.Minute)
	if err != nil {
		t.Fatalf("GenerateStagingUploadURL: %v", err)
	}
	parsed, err := url.Parse(stagingURL)
	if err != nil {
		t.Fatalf("parse staging URL: %v", err)
	}
	query := parsed.Query()
	query.Set("signature", "invalid-signature")
	parsed.RawQuery = query.Encode()
	put := httptest.NewRequest(http.MethodPut, parsed.String(), strings.NewReader("untrusted"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, put)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unsigned staging write returned %d, want %d: %s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if _, err := store.HeadObject(context.Background(), "staging/session-id/0"); err == nil {
		t.Fatal("unsigned request wrote to staging")
	}

	put = httptest.NewRequest(http.MethodPut, "/local-storage/blocks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.NewReader("unverified"))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, put)
	if response.Code != http.StatusNotFound {
		t.Fatalf("direct CAS write returned %d, want %d", response.Code, http.StatusNotFound)
	}
	if _, err := store.HeadObject(context.Background(), "blocks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("direct request wrote a canonical block")
	}
}

var _ domain.StagingUploadURLValidator = (*storage.LocalStore)(nil)
