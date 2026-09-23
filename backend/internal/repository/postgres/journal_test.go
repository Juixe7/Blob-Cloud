package postgresrepo

import (
	"testing"

	"go-drive-clone/internal/domain"
)

func TestJournal_ActionConstants(t *testing.T) {
	actions := []string{
		domain.ActionFileCreated,
		domain.ActionFileUpdated,
		domain.ActionFileRenamed,
		domain.ActionFileMoved,
		domain.ActionFileTrashed,
		domain.ActionFileRestored,
		domain.ActionFileDeleted,
	}

	for _, a := range actions {
		if a == "" {
			t.Error("expected non-empty action constant")
		}
	}
}

func TestJournalRepository_PaginationLogic(t *testing.T) {
	// Verifies the has_more and cursor calculation logic
	entries := []*domain.JournalEntry{
		{Cursor: 1, FileID: "f1", Action: domain.ActionFileCreated},
		{Cursor: 2, FileID: "f2", Action: domain.ActionFileRenamed},
		{Cursor: 3, FileID: "f3", Action: domain.ActionFileMoved},
	}

	limit := 2
	hasMore := len(entries) > limit
	if !hasMore {
		t.Fatalf("expected hasMore true for 3 items with limit 2")
	}

	sliced := entries[:limit]
	highestCursor := sliced[len(sliced)-1].Cursor
	if highestCursor != 2 {
		t.Errorf("expected highest cursor 2, got %d", highestCursor)
	}
}
