package server

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"sort"
	"strings"

	"varkiv/internal/catalog"
)

type inventoryMatchCandidate struct {
	EditionID    string `json:"edition_id"`
	GameID       string `json:"game_id"`
	GameTitle    string `json:"game_title"`
	EditionTitle string `json:"edition_title"`
	EditionType  string `json:"edition_type"`
	PlatformID   string `json:"platform_id"`
}

type inventoryMatchReview struct {
	SessionID       string                    `json:"session_id"`
	InventoryItemID string                    `json:"inventory_item_id"`
	DeviceName      string                    `json:"device_name"`
	PlatformID      string                    `json:"platform_id"`
	MatchMethod     string                    `json:"match_method"`
	Size            int64                     `json:"size"`
	Candidates      []inventoryMatchCandidate `json:"candidates"`
}

type inventoryMatchRequest struct {
	SessionID       string `json:"session_id"`
	InventoryItemID string `json:"inventory_item_id"`
	EditionID       string `json:"edition_id"`
	PreviewToken    string `json:"preview_token,omitempty"`
}

type inventoryMatchSnapshot struct {
	SessionID       string   `json:"session_id"`
	InventoryItemID string   `json:"inventory_item_id"`
	DeviceID        string   `json:"device_id"`
	ClientItemID    string   `json:"client_item_id"`
	PlatformID      string   `json:"platform_id"`
	IdentityHash    string   `json:"identity_hash"`
	MatchMethod     string   `json:"match_method"`
	CandidateIDs    []string `json:"candidate_ids"`
	EditionID       string   `json:"edition_id"`
}

type inventoryMatchPreview struct {
	PreviewToken      string               `json:"preview_token"`
	Review            inventoryMatchReview `json:"review"`
	SelectedEditionID string               `json:"selected_edition_id"`
	AppliesToNextSync bool                 `json:"applies_to_next_sync"`
}

func (s *Server) buildInventoryMatchReview(ctx context.Context, sessionID, inventoryItemID, locale string) (inventoryMatchReview, inventoryMatchSnapshot, error) {
	item, candidateIDs, err := s.store.ReviewInventoryMatch(ctx, strings.TrimSpace(sessionID), strings.TrimSpace(inventoryItemID))
	if err != nil {
		return inventoryMatchReview{}, inventoryMatchSnapshot{}, err
	}
	review := inventoryMatchReview{
		SessionID: item.SessionID, InventoryItemID: item.ID, DeviceName: item.DeviceName,
		PlatformID: item.PlatformID, MatchMethod: item.MatchMethod, Size: item.Size,
		Candidates: []inventoryMatchCandidate{},
	}
	for _, editionID := range candidateIDs {
		edition, editionErr := s.store.GetEdition(ctx, editionID, locale)
		if editionErr != nil {
			return inventoryMatchReview{}, inventoryMatchSnapshot{}, catalog.ErrInventoryMatchStale
		}
		game, gameErr := s.store.GetGame(ctx, edition.GameID, locale)
		if gameErr != nil || game.Platform != item.PlatformID {
			return inventoryMatchReview{}, inventoryMatchSnapshot{}, catalog.ErrInventoryMatchStale
		}
		review.Candidates = append(review.Candidates, inventoryMatchCandidate{
			EditionID: edition.ID, GameID: game.ID, GameTitle: game.DisplayTitle,
			EditionTitle: edition.DisplayTitle, EditionType: edition.EditionType, PlatformID: game.Platform,
		})
	}
	identityHash, err := catalog.InventoryIdentityHash(catalog.NewInventoryItem{
		ClientItemID: item.ClientItemID, PlatformID: item.PlatformID, SHA256: item.SHA256,
		Serial: item.Serial, ProductCode: item.ProductCode, TitleID: item.TitleID, Size: item.Size,
	})
	if err != nil {
		return inventoryMatchReview{}, inventoryMatchSnapshot{}, err
	}
	snapshot := inventoryMatchSnapshot{
		SessionID: item.SessionID, InventoryItemID: item.ID, DeviceID: item.DeviceID,
		ClientItemID: item.ClientItemID, PlatformID: item.PlatformID, IdentityHash: identityHash,
		MatchMethod: item.MatchMethod, CandidateIDs: append([]string(nil), candidateIDs...),
	}
	return review, snapshot, nil
}

func (s *Server) listInventoryMatchReviews(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListAmbiguousInventoryReviewItems(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	locale := strings.TrimSpace(r.URL.Query().Get("locale"))
	reviews := make([]inventoryMatchReview, 0, len(items))
	for _, item := range items {
		review, _, reviewErr := s.buildInventoryMatchReview(r.Context(), item.SessionID, item.ID, locale)
		if errors.Is(reviewErr, catalog.ErrInventoryMatchStale) || errors.Is(reviewErr, catalog.ErrInventoryMatchNotAmbiguous) {
			continue
		}
		if reviewErr != nil {
			writeError(w, reviewErr)
			return
		}
		reviews = append(reviews, review)
	}
	writeCollection(w, r, reviews)
}

func selectedInventoryCandidate(review inventoryMatchReview, editionID string) bool {
	for _, candidate := range review.Candidates {
		if candidate.EditionID == editionID {
			return true
		}
	}
	return false
}

func (s *Server) previewInventoryMatch(w http.ResponseWriter, r *http.Request) {
	var in inventoryMatchRequest
	if !decode(w, r, &in) {
		return
	}
	in.SessionID, in.InventoryItemID, in.EditionID = strings.TrimSpace(in.SessionID), strings.TrimSpace(in.InventoryItemID), strings.TrimSpace(in.EditionID)
	review, snapshot, err := s.buildInventoryMatchReview(r.Context(), in.SessionID, in.InventoryItemID, strings.TrimSpace(r.URL.Query().Get("locale")))
	if err != nil {
		writeError(w, err)
		return
	}
	if !selectedInventoryCandidate(review, in.EditionID) {
		writeError(w, catalog.ErrInventoryMatchStale)
		return
	}
	snapshot.EditionID = in.EditionID
	sort.Strings(snapshot.CandidateIDs)
	token, err := s.signPreviewValue(previewTokenDomainInventoryMatch, snapshot)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inventoryMatchPreview{PreviewToken: token, Review: review, SelectedEditionID: in.EditionID, AppliesToNextSync: true})
}

func (s *Server) commitInventoryMatch(w http.ResponseWriter, r *http.Request) {
	var in inventoryMatchRequest
	if !decode(w, r, &in) {
		return
	}
	in.SessionID, in.InventoryItemID, in.EditionID = strings.TrimSpace(in.SessionID), strings.TrimSpace(in.InventoryItemID), strings.TrimSpace(in.EditionID)
	review, snapshot, err := s.buildInventoryMatchReview(r.Context(), in.SessionID, in.InventoryItemID, strings.TrimSpace(r.URL.Query().Get("locale")))
	if err != nil {
		writeError(w, err)
		return
	}
	if !selectedInventoryCandidate(review, in.EditionID) {
		writeError(w, catalog.ErrInventoryMatchStale)
		return
	}
	snapshot.EditionID = in.EditionID
	sort.Strings(snapshot.CandidateIDs)
	expected, err := s.signPreviewValue(previewTokenDomainInventoryMatch, snapshot)
	if err != nil {
		writeError(w, err)
		return
	}
	if in.PreviewToken == "" || !hmac.Equal([]byte(expected), []byte(in.PreviewToken)) {
		writeError(w, catalog.ErrInventoryMatchStale)
		return
	}
	override, err := s.store.ConfirmInventoryMatchOverride(r.Context(), catalog.NewInventoryMatchOverride{
		DeviceID: snapshot.DeviceID, ClientItemID: snapshot.ClientItemID, PlatformID: snapshot.PlatformID,
		IdentityHash: snapshot.IdentityHash, EditionID: snapshot.EditionID, MatchMethod: snapshot.MatchMethod,
		CandidateIDs:    append([]string(nil), snapshot.CandidateIDs...),
		SourceSessionID: snapshot.SessionID, SourceInventoryItemID: snapshot.InventoryItemID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"confirmed": true, "override_id": override.ID, "edition_id": override.EditionID,
		"match_method": override.MatchMethod, "applies_to_next_sync": true,
	})
}
