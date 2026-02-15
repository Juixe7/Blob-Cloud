package httpx

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/domain"
)

// HandleListFileVersions implements GET /api/files/{id}/versions.
func (s *Server) HandleListFileVersions(w http.ResponseWriter, r *http.Request) {
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

	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if file.IsDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot restore versions of a directory"})
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

	// Backup current state to a new version record before restoring
	oldBlocks, err := s.blocks.ListFileBlockHashes(r.Context(), fileID)
	if err != nil {
		s.log.Error("restore version failed to list current blocks", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error listing blocks"})
		return
	}

	versionsList, err := s.files.GetFileVersions(r.Context(), fileID)
	if err != nil {
		s.log.Error("restore version failed to list versions", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error listing versions"})
		return
	}
	nextVersion := len(versionsList) + 1

	backupVersion := &domain.FileVersion{
		FileID:        fileID,
		VersionNumber: nextVersion,
		SizeBytes:     file.SizeBytes,
		ChunkHashes:   oldBlocks,
	}
	if err := s.files.CreateFileVersion(r.Context(), backupVersion); err != nil {
		s.log.Error("restore version failed to backup current state", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error creating backup version"})
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

	// Update active file
	file.SizeBytes = version.SizeBytes
	if err := s.files.Update(r.Context(), file); err != nil {
		s.log.Error("restore version failed to update file", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error updating file"})
		return
	}

	if err := s.blocks.ReplaceBlocksForFile(r.Context(), fileID, seqs); err != nil {
		s.log.Error("restore version failed to replace blocks", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error replacing blocks"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "File version restored successfully.",
	})
}
