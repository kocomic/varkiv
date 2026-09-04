package server

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

	"varkiv/internal/catalog"
	"varkiv/internal/hashpack"
)

var errHashPackPreviewStale = errors.New("hash pack preview is stale")

const hashPackUploadOverhead = 2 << 20

type hashPackExportRequest struct {
	SourceID  string `json:"source_id"`
	Name      string `json:"name"`
	Publisher string `json:"publisher,omitempty"`
	License   string `json:"license"`
	Release   string `json:"release"`
}

type hashPackPreviewSnapshot struct {
	PackSHA256 string                  `json:"pack_sha256"`
	Preview    catalog.HashPackPreview `json:"preview"`
}

type hashPackPreviewResponse struct {
	catalog.HashPackPreview
	PackSHA256   string `json:"pack_sha256"`
	PreviewToken string `json:"preview_token"`
}

func readHashPackUpload(w http.ResponseWriter, r *http.Request) ([]byte, hashpack.Pack, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, hashpack.MaxPackBytes+hashPackUploadOverhead)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, hashpack.Pack{}, "", fmt.Errorf("invalid hash pack upload: %w", err)
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, hashpack.Pack{}, "", errors.New("file is required")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, hashpack.MaxPackBytes+1))
	if err != nil {
		return nil, hashpack.Pack{}, "", err
	}
	if len(data) > hashpack.MaxPackBytes {
		return nil, hashpack.Pack{}, "", fmt.Errorf("hash pack exceeds %d bytes", hashpack.MaxPackBytes)
	}
	pack, err := hashpack.Decode(data)
	if err != nil {
		return nil, hashpack.Pack{}, "", fmt.Errorf("invalid hash pack: %w", err)
	}
	return data, pack, hashpack.Digest(data), nil
}

func (s *Server) previewHashPack(w http.ResponseWriter, r *http.Request) {
	_, pack, digest, err := readHashPackUpload(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	preview, err := s.store.PreviewHashPack(r.Context(), pack, digest)
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := s.signPreviewValue(previewTokenDomainHashPack, hashPackPreviewSnapshot{PackSHA256: digest, Preview: preview})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hashPackPreviewResponse{HashPackPreview: preview, PackSHA256: digest, PreviewToken: token})
}

func (s *Server) importHashPack(w http.ResponseWriter, r *http.Request) {
	_, pack, digest, err := readHashPackUpload(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	preview, err := s.store.PreviewHashPack(r.Context(), pack, digest)
	if err != nil {
		writeError(w, err)
		return
	}
	expected, err := s.signPreviewValue(previewTokenDomainHashPack, hashPackPreviewSnapshot{PackSHA256: digest, Preview: preview})
	if err != nil {
		writeError(w, err)
		return
	}
	if supplied := strings.TrimSpace(r.FormValue("preview_token")); supplied == "" || !hmac.Equal([]byte(expected), []byte(supplied)) {
		writeError(w, errHashPackPreviewStale)
		return
	}
	if preview.ReleaseConflict {
		writeError(w, catalog.ErrHashReleaseConflict)
		return
	}
	result, err := s.store.ImportHashPack(r.Context(), pack, digest)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if result.ExistingRelease {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (s *Server) exportHashPack(w http.ResponseWriter, r *http.Request) {
	var in hashPackExportRequest
	if !decode(w, r, &in) {
		return
	}
	source := hashpack.Source{ID: in.SourceID, Name: in.Name, Publisher: in.Publisher, License: in.License}
	data, manifest, err := s.store.ExportHashPack(r.Context(), source, in.Release)
	if err != nil {
		writeError(w, err)
		return
	}
	releaseName := strings.Map(func(value rune) rune {
		if value > unicode.MaxASCII || !(unicode.IsLetter(value) || unicode.IsDigit(value) || value == '.' || value == '_' || value == '-') {
			return '-'
		}
		return value
	}, manifest.Release)
	releaseName = strings.Trim(releaseName, "-._")
	if releaseName == "" {
		releaseName = manifest.PackID[:12]
	}
	if len(releaseName) > 64 {
		releaseName = releaseName[:64]
	}
	filename := manifest.Source.ID + "-" + releaseName + ".hashpack"
	w.Header().Set("Content-Type", "application/vnd.varkiv.hashpack+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Varkiv-Pack-ID", manifest.PackID)
	w.Header().Set("X-Varkiv-Record-Count", fmt.Sprintf("%d", manifest.RecordCount))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) listHashSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListHashSources(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) resolveHashIdentity(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ResolveHashIdentities(r.Context(), r.PathValue("sha256"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}
