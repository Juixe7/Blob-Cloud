package httpx

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

// HandleListFileVersions implements GET /api/files/{id}/versions.
func (s *Server) HandleListFileVersions(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil || s.files == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "version service unavailable"})
		return
	}
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file id is required"})
		return
	}

	// Verify the user has access to the file (Viewer or Editor or Owner)
	if err := s.fileOps.AuthoriseRead(r.Context(), userID, fileID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}

	versions, err := s.files.GetFileVersions(r.Context(), fileID)
	if err != nil {
		s.log.Error("list file versions failed", "file_id", fileID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error listing versions"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"versions": versions,
	})
}

// HandleRestoreFileVersion implements POST /api/files/{id}/versions/{version_id}/restore.
func (s *Server) HandleRestoreFileVersion(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil || s.files == nil || s.blocks == nil || s.perms == nil || s.users == nil || s.db == nil || s.journalWriter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "version restore service unavailable"})
		return
	}
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	fileID := chi.URLParam(r, "id")
	versionID := chi.URLParam(r, "version_id")
	if fileID == "" || versionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file id and version id are required"})
		return
	}

	// Must be Editor or Owner to restore
	if err := s.fileOps.AuthoriseWrite(r.Context(), userID, fileID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "must be an editor to restore file versions"})
		return
	}
	actor, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if file.IsDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot restore versions of a directory"})
		return
	}
	if file.TargetID != nil || file.ShortcutTargetID != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot restore versions through a shortcut"})
		return
	}

	version, err := s.files.GetFileVersion(r.Context(), versionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "version not found"})
		return
	}
	if version.FileID != fileID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "version does not belong to this file"})
		return
	}

	// Resolve hashes to block sequences
	blocksList, err := s.blocks.GetMultipleByHashes(r.Context(), version.ChunkHashes)
	if err != nil {
		s.log.Error("restore version failed to get blocks", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error resolving blocks"})
		return
	}

	blockMap := make(map[string]string)
	for _, b := range blocksList {
		blockMap[b.SHA256] = b.ID
	}

	var seqs []domain.BlockSequence
	for i, hash := range version.ChunkHashes {
		id, ok := blockMap[hash]
		if !ok {
			s.log.Error("missing block for version restore", "hash", hash)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("missing block data for %s", hash)})
			return
		}
		seqs = append(seqs, domain.BlockSequence{
			BlockID:        id,
			SequenceNumber: i,
		})
	}

	// Back up the current version, restore metadata and block associations, and
	// append the sync journal event atomically.
	file.SizeBytes = version.SizeBytes
	entries, err := s.journalEntriesForFile(r.Context(), file, domain.ActionFileUpdated, userID)
	if err != nil {
		s.log.Error("restore version failed to resolve event recipients", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error restoring file version"})
		return
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		// Serialize with file lifecycle changes and keep the effective grant row
		// locked until the restore commits. The earlier request-level check is
		// useful for a quick response, but cannot authorize this mutation alone.
		currentFile, err := s.requireSharePermissionInTx(r.Context(), tx, fileID, userID, actor.Email)
		if err != nil {
			return fmt.Errorf("restore permission changed: %w", err)
		}
		if currentFile.IsDirectory || currentFile.TargetID != nil || currentFile.ShortcutTargetID != nil {
			return fmt.Errorf("cannot restore versions of a directory or shortcut")
		}
		if currentFile.DeletedAt != nil || currentFile.Status != "ACTIVE" {
			return fmt.Errorf("file is unavailable")
		}
		oldBlocks, err := s.blocks.WithTx(tx).ListFileBlockHashes(r.Context(), fileID)
		if err != nil {
			return fmt.Errorf("list current blocks: %w", err)
		}
		versionsList, err := s.files.WithTx(tx).GetFileVersions(r.Context(), fileID)
		if err != nil {
			return fmt.Errorf("list current versions: %w", err)
		}
		maxVersion := 0
		for _, v := range versionsList {
			if v.VersionNumber > maxVersion {
				maxVersion = v.VersionNumber
			}
		}
		backupVersion := &domain.FileVersion{
			FileID: fileID, VersionNumber: maxVersion + 1,
			SizeBytes:   currentFile.SizeBytes,
			ChunkHashes: oldBlocks,
		}
		if err := s.files.WithTx(tx).CreateFileVersion(r.Context(), backupVersion); err != nil {
			return fmt.Errorf("backup current file version: %w", err)
		}
		currentFile.SizeBytes = version.SizeBytes
		if err := s.files.WithTx(tx).Update(r.Context(), currentFile); err != nil {
			return fmt.Errorf("update restored file metadata: %w", err)
		}
		if err := s.blocks.WithTx(tx).ReplaceBlocksForFile(r.Context(), fileID, seqs); err != nil {
			return fmt.Errorf("replace restored file blocks: %w", err)
		}
		return nil
	}, entries); err != nil {
		s.log.Error("restore version failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error restoring file version"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "File version restored successfully.",
	})
}
