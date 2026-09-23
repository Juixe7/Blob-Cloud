package httpx

import (
	"net/http"
	"strconv"

	"go-drive-clone/internal/domain"
)

// DeltaSyncResponse is the JSON response payload for GET /api/sync/delta.
type DeltaSyncResponse struct {
	Entries    []*domain.JournalEntry `json:"entries"`
	NextCursor int64                  `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
}

// HandleSyncDelta retrieves incremental journal changes since a given cursor.
// GET /api/sync/delta?since=<cursor>&limit=<limit>
func (s *Server) HandleSyncDelta(w http.ResponseWriter, r *http.Request) {
	if s.journal == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sync service unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	var since int64 = 0
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if val, err := strconv.ParseInt(sinceStr, 10, 64); err == nil && val >= 0 {
			since = val
		}
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	entries, nextCursor, hasMore, err := s.journal.ListSince(r.Context(), userID, since, limit)
	if err != nil {
		s.log.Error("failed to list sync delta entries", "user_id", userID, "since", since, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve sync deltas"})
		return
	}

	if entries == nil {
		entries = []*domain.JournalEntry{}
	}

	writeJSON(w, http.StatusOK, DeltaSyncResponse{
		Entries:    entries,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

// HandleSyncCursor retrieves the highest journal cursor for the authenticated user.
// GET /api/sync/cursor
func (s *Server) HandleSyncCursor(w http.ResponseWriter, r *http.Request) {
	if s.journal == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sync service unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	cursor, err := s.journal.GetLatestCursor(r.Context(), userID)
	if err != nil {
		s.log.Error("failed to get latest sync cursor", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve sync cursor"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{
		"cursor": cursor,
	})
}
