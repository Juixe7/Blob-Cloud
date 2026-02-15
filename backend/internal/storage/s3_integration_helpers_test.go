//go:build integration

package storage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// httpDefaultClient is the HTTP client used by the integration tests. It uses a
// short timeout so a misconfigured bucket fails fast rather than hanging.
var httpDefaultClient = &http.Client{Timeout: 30 * time.Second}

// newPresignedPutRequest builds the PUT request that mimics a browser doing a
// direct-to-S3 upload against a presigned URL.
func newPresignedPutRequest(presignedURL string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPut, presignedURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(len(body))
	return req, nil
}

// compile-time keep-alive so httptest is not flagged unused if we later add a
// local fake-S3 test server.
var _ = httptest.NewServer
