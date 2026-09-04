package server

import (
	"crypto/hmac"
	"errors"
	"net/http"
	"strings"

	"varkiv/internal/catalog"
	storagex "varkiv/internal/storage"
)

var errManagedCleanupStale = errors.New("managed storage cleanup preview is stale")

type managedCleanupSnapshot struct {
	Fingerprint string                      `json:"fingerprint"`
	Candidates  []storagex.CleanupCandidate `json:"candidates"`
}

type managedCleanupPreview struct {
	PreviewToken string                      `json:"preview_token"`
	Fingerprint  string                      `json:"fingerprint"`
	Candidates   []storagex.CleanupCandidate `json:"candidates"`
	TotalBytes   int64                       `json:"total_bytes"`
}

type managedCleanupCommitRequest struct {
	PreviewToken string   `json:"preview_token"`
	SelectedIDs  []string `json:"selected_ids"`
}

func (s *Server) cleanupPreviewToken(plan storagex.CleanupPlan) (string, error) {
	return s.signPreviewValue(previewTokenDomainManagedCleanup, managedCleanupSnapshot{Fingerprint: plan.Fingerprint, Candidates: plan.Candidates})
}

func (s *Server) previewManagedStorageCleanup(w http.ResponseWriter, r *http.Request) {
	references, err := s.store.ManagedStorageReferences(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	plan, err := s.storage.PreviewManagedCleanup(r.Context(), references)
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := s.cleanupPreviewToken(plan)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedCleanupPreview{PreviewToken: token, Fingerprint: plan.Fingerprint, Candidates: plan.Candidates, TotalBytes: plan.TotalBytes})
}

func cleanupSelection(plan storagex.CleanupPlan, selectedIDs []string) ([]storagex.CleanupCandidate, error) {
	if len(selectedIDs) == 0 {
		return nil, errors.New("selected_ids must contain at least one cleanup candidate")
	}
	available := make(map[string]storagex.CleanupCandidate, len(plan.Candidates))
	for _, item := range plan.Candidates {
		available[item.ID] = item
	}
	selected := make([]storagex.CleanupCandidate, 0, len(selectedIDs))
	seen := map[string]bool{}
	for _, id := range selectedIDs {
		id = strings.TrimSpace(id)
		item, ok := available[id]
		if !ok {
			return nil, errManagedCleanupStale
		}
		if seen[id] {
			return nil, errors.New("selected_ids must not contain duplicates")
		}
		seen[id] = true
		selected = append(selected, item)
	}
	return selected, nil
}

func (s *Server) commitManagedStorageCleanup(w http.ResponseWriter, r *http.Request) {
	var request managedCleanupCommitRequest
	if !decode(w, r, &request) {
		return
	}
	var run storagex.CleanupRun
	err := s.store.WithLockedManagedStorageReferences(r.Context(), func(references catalog.ManagedStorageReferences) error {
		plan, scanErr := s.storage.PreviewManagedCleanup(r.Context(), references)
		if scanErr != nil {
			return scanErr
		}
		expected, tokenErr := s.cleanupPreviewToken(plan)
		if tokenErr != nil {
			return tokenErr
		}
		if request.PreviewToken == "" || !hmac.Equal([]byte(expected), []byte(request.PreviewToken)) {
			return errManagedCleanupStale
		}
		selected, selectionErr := cleanupSelection(plan, request.SelectedIDs)
		if selectionErr != nil {
			return selectionErr
		}
		run, scanErr = s.storage.QuarantineManagedCleanup(r.Context(), plan.Fingerprint, selected)
		return scanErr
	})
	if err != nil {
		if run.ID != "" {
			if _, restoreErr := s.storage.RestoreCleanupRun(r.Context(), run.ID); restoreErr != nil {
				writeAPIError(w, http.StatusInternalServerError, "managed_cleanup_recovery_required", "cleanup could not finalize; its private recovery manifest was retained")
				return
			}
		}
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "storage-cleanup/runs", run.ID))
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listManagedStorageCleanupRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.storage.ListCleanupRuns(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, runs)
}

func (s *Server) restoreManagedStorageCleanupRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.storage.RestoreCleanupRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
