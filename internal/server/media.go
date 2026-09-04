package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"varkiv/internal/catalog"
	storagex "varkiv/internal/storage"
)

func (s *Server) listMedia(w http.ResponseWriter, r *http.Request) {
	gameID := strings.TrimSpace(r.URL.Query().Get("game_id"))
	items, err := s.store.ListMedia(r.Context(), gameID, strings.TrimSpace(r.URL.Query().Get("edition_id")), strings.TrimSpace(r.URL.Query().Get("kind")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) getMedia(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetMedia(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateMedia(w http.ResponseWriter, r *http.Request) {
	var in catalog.MediaMetadataUpdate
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateMediaMetadata(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) recheckMedia(w http.ResponseWriter, r *http.Request) {
	gameID := strings.TrimSpace(r.URL.Query().Get("game_id"))
	editionID := strings.TrimSpace(r.URL.Query().Get("edition_id"))
	if gameID != "" && editionID != "" {
		writeError(w, errors.New("game_id and edition_id cannot be combined"))
		return
	}
	items, err := s.store.ListMedia(r.Context(), gameID, editionID, "")
	if err != nil {
		writeError(w, err)
		return
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	updates := make([]catalog.MediaContentStatusUpdate, 0, len(items))
	result := struct {
		Checked    int `json:"checked"`
		Available  int `json:"available"`
		Missing    int `json:"missing"`
		Changed    int `json:"changed"`
		Unsafe     int `json:"unsafe"`
		Unverified int `json:"unverified"`
	}{Checked: len(items)}
	for _, item := range items {
		status := s.storage.InspectMedia(item)
		updates = append(updates, catalog.MediaContentStatusUpdate{ID: item.ID, ContentStatus: status, ContentCheckedAt: checkedAt})
		switch status {
		case "available":
			result.Available++
		case "missing":
			result.Missing++
		case "changed":
			result.Changed++
		case "unsafe":
			result.Unsafe++
		default:
			result.Unverified++
		}
	}
	if err = s.store.UpdateMediaContentStatuses(r.Context(), updates); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, fmt.Errorf("invalid media upload: %w", err))
		return
	}
	gameID := strings.TrimSpace(r.FormValue("game_id"))
	editionID := strings.TrimSpace(r.FormValue("edition_id"))
	kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
	if (gameID == "") == (editionID == "") {
		writeError(w, errors.New("exactly one of game_id or edition_id is required"))
		return
	}
	if !catalog.ValidMediaKind(kind) {
		writeError(w, errors.New("invalid media kind"))
		return
	}
	order, err := strconv.Atoi(defaultValue(r.FormValue("sort_order"), "0"))
	if err != nil || order < 0 {
		writeError(w, errors.New("sort_order must be zero or greater"))
		return
	}
	if gameID != "" {
		_, err = s.store.GetGame(r.Context(), gameID, "")
	} else {
		_, err = s.store.GetEdition(r.Context(), editionID, "")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, errors.New("media file is required"))
		return
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	peek, _ := reader.Peek(512)
	detected := http.DetectContentType(peek)
	if !allowedMediaType(detected) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "media must be a safe image, audio, video, or PDF file")
		return
	}
	storedPath, size, hash, mimeType, err := s.storage.StoreMedia(r.Context(), header.Filename, detected, reader)
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.AddMedia(r.Context(), catalog.NewMediaAsset{
		GameID: gameID, EditionID: editionID, Kind: kind, StorageKind: "managed", Path: storedPath, OriginalName: filepath.Base(header.Filename), MIMEType: mimeType, Size: size, SHA256: hash, Locale: strings.TrimSpace(r.FormValue("locale")), SourceType: "upload", SortOrder: order, ContentStatus: "available",
	})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "media", item.ID))
	writeJSON(w, http.StatusCreated, item)
}

func allowedMediaType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	if value == "application/pdf" {
		return true
	}
	return (strings.HasPrefix(value, "image/") && value != "image/svg+xml") || strings.HasPrefix(value, "audio/") || strings.HasPrefix(value, "video/")
}

func (s *Server) downloadMedia(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetMedia(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	file, err := s.storage.OpenVerifiedMedia(item)
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, storagex.ErrMediaUnavailable)
		return
	}
	etag := `"sha256:` + item.SHA256 + `"`
	if item.SHA256 != "" && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", item.MIMEType)
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, item.OriginalName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if item.SHA256 != "" {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (s *Server) deleteMedia(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteMedia(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	// Content-addressed blobs are intentionally retained for later mark-and-sweep GC.
	w.WriteHeader(http.StatusNoContent)
}
