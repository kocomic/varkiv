package server

import (
	"crypto/hmac"
	"errors"
	"net/http"
	"strings"

	"varkiv/internal/catalog"
)

type gameMergeRequest struct {
	SourceGameID        string `json:"source_game_id"`
	PreviewToken        string `json:"preview_token,omitempty"`
	SnapshotFingerprint string `json:"snapshot_fingerprint,omitempty"`
}

type gameMergeToken struct {
	TargetGameID        string `json:"target_game_id"`
	SourceGameID        string `json:"source_game_id"`
	SnapshotFingerprint string `json:"snapshot_fingerprint"`
}

type gameMergePreviewResponse struct {
	catalog.GameMergePlan
	PreviewToken              string `json:"preview_token"`
	FailurePolicy             string `json:"failure_policy"`
	ROMFilesMoved             bool   `json:"rom_files_moved"`
	SaveNamespacesChanged     bool   `json:"save_namespaces_changed"`
	SourceGameMetadataRemoved bool   `json:"source_game_metadata_removed"`
}

func (in gameMergeRequest) sourceID() string {
	return strings.TrimSpace(in.SourceGameID)
}

func (s *Server) gameMergePreviewToken(targetGameID, sourceGameID, fingerprint string) (string, error) {
	return s.signPreviewValue(previewTokenDomainGameMerge, gameMergeToken{TargetGameID: targetGameID, SourceGameID: sourceGameID, SnapshotFingerprint: fingerprint})
}

func (s *Server) previewGameMerge(w http.ResponseWriter, r *http.Request) {
	var in gameMergeRequest
	if !decode(w, r, &in) {
		return
	}
	targetGameID, sourceGameID := r.PathValue("id"), in.sourceID()
	plan, err := s.store.PreviewGameMerge(r.Context(), targetGameID, sourceGameID, r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := s.gameMergePreviewToken(targetGameID, sourceGameID, plan.SnapshotFingerprint)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gameMergePreviewResponse{
		GameMergePlan: plan, PreviewToken: token, FailurePolicy: "atomic",
		ROMFilesMoved: false, SaveNamespacesChanged: false, SourceGameMetadataRemoved: true,
	})
}

func (s *Server) mergeGame(w http.ResponseWriter, r *http.Request) {
	var in gameMergeRequest
	if !decode(w, r, &in) {
		return
	}
	targetGameID, sourceGameID := r.PathValue("id"), in.sourceID()
	var item catalog.Game
	var err error
	if in.PreviewToken == "" && in.SnapshotFingerprint == "" {
		// Compatibility for pre-preview API clients. The first-party web UI always
		// uses the signed preview flow below.
		item, err = s.store.MergeGames(r.Context(), targetGameID, sourceGameID)
	} else {
		if in.PreviewToken == "" || in.SnapshotFingerprint == "" {
			writeError(w, catalog.ErrGameMergeStale)
			return
		}
		expected, tokenErr := s.gameMergePreviewToken(targetGameID, sourceGameID, in.SnapshotFingerprint)
		if tokenErr != nil {
			writeError(w, tokenErr)
			return
		}
		if !hmac.Equal([]byte(expected), []byte(in.PreviewToken)) {
			writeError(w, catalog.ErrGameMergeStale)
			return
		}
		item, err = s.store.MergeGamesIfFingerprint(r.Context(), targetGameID, sourceGameID, in.SnapshotFingerprint)
	}
	if err != nil {
		if errors.Is(err, catalog.ErrGameMergeStale) {
			writeError(w, catalog.ErrGameMergeStale)
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
