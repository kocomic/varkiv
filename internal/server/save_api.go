package server

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/portablepath"
	"varkiv/internal/saves"
)

func (s *Server) saveRevisionForRequest(r *http.Request, id string) (catalog.SaveRevision, error) {
	revision, err := s.store.GetSaveRevision(r.Context(), id)
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	identity, paired := clientIdentity(r)
	if !paired {
		return revision, nil
	}
	device, err := s.store.GetDevice(r.Context(), identity.DeviceID)
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	bindings, err := s.store.ListSaveBindings(r.Context(), "", device.DeviceProfileID)
	if err != nil {
		return catalog.SaveRevision{}, err
	}
	for _, binding := range bindings {
		if device.DeviceProfileID == "" && binding.DeviceProfileID != "" {
			continue
		}
		authorized, authErr := s.store.SaveBindingRuntimeAuthorized(r.Context(), device, binding)
		if authErr != nil {
			return catalog.SaveRevision{}, authErr
		}
		if authorized && binding.StreamID == revision.StreamID {
			return revision, nil
		}
	}
	return catalog.SaveRevision{}, catalog.ErrSaveRevisionNotBound
}

func (s *Server) listSaveStreams(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSaveStreams(r.Context(), strings.TrimSpace(r.URL.Query().Get("edition_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) listSaveCompatibilityGroups(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSaveCompatibilityGroups(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) listRuntimeAttestations(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRuntimeAttestations(r.Context(), strings.TrimSpace(r.URL.Query().Get("device_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) createSaveStream(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSaveStream
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateSaveStream(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "save-streams", item.ID))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getSaveStream(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetSaveStream(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listStreamRevisions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListStreamRevisions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

type saveUploadManifestFile struct {
	LogicalPath string `json:"logical_path"`
	MTimeNS     int64  `json:"mtime_ns,omitempty"`
	Mode        int64  `json:"mode,omitempty"`
}

func (s *Server) uploadStreamRevision(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, fmt.Errorf("invalid multi-file save upload: %w", err))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	stream, err := s.store.GetSaveStream(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var manifest []saveUploadManifestFile
	if err = json.Unmarshal([]byte(r.FormValue("manifest")), &manifest); err != nil || len(manifest) == 0 {
		writeError(w, errors.New("manifest must be a non-empty JSON file list"))
		return
	}
	headers := r.MultipartForm.File["files"]
	if len(headers) != len(manifest) {
		writeError(w, errors.New("manifest and files must have the same item count"))
		return
	}
	editionID := strings.TrimSpace(r.FormValue("edition_id"))
	if editionID == "" && len(stream.Editions) > 0 {
		editionID = stream.Editions[0].EditionID
	}
	linked := false
	for _, relation := range stream.Editions {
		if relation.EditionID == editionID {
			linked = true
		}
	}
	if !linked {
		writeError(w, errors.New("edition_id is not linked to this save stream"))
		return
	}
	files := make([]saves.IncomingFile, 0, len(headers))
	opened := make([]io.Closer, 0, len(headers))
	defer func() {
		for _, closer := range opened {
			_ = closer.Close()
		}
	}()
	for index, header := range headers {
		part, openErr := header.Open()
		if openErr != nil {
			writeError(w, openErr)
			return
		}
		opened = append(opened, part)
		files = append(files, saves.IncomingFile{LogicalPath: manifest[index].LogicalPath, Reader: part, MTimeNS: manifest[index].MTimeNS, Mode: manifest[index].Mode})
	}
	scopeType, scopeKey := stream.OwnerType, stream.OwnerKey
	if stream.OwnerType == "edition" {
		scopeType, scopeKey = "game", ""
	}
	deviceID := r.FormValue("device_id")
	if identity, ok := clientIdentity(r); ok {
		deviceID = identity.DeviceID
	}
	result, err := s.saves.PushSet(r.Context(), saves.PushSetInput{
		EditionID: editionID, DeviceID: deviceID, DriverID: stream.DriverID, ScopeType: scopeType, ScopeKey: scopeKey,
		BaseRevisionID: r.FormValue("base_revision_id"), Files: files,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "save-revisions", result.Revision.ID))
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) downloadSaveRevisionFile(w http.ResponseWriter, r *http.Request) {
	file, metadata, err := s.saves.OpenRevisionFile(r.Context(), r.PathValue("id"), r.PathValue("file_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	name := filepath.Base(filepath.FromSlash(metadata.LogicalPath))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("Content-Length", strconv.FormatInt(metadata.Size, 10))
	w.Header().Set("ETag", `"`+metadata.Checksum+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func saveArchiveLogicalPaths(files []catalog.SaveFile) (map[string]string, error) {
	logicalPaths := make(map[string]string, len(files))
	seenPaths := make(map[string]bool, len(files))
	for _, metadata := range files {
		logical, err := portablepath.CleanSaveLogical(metadata.LogicalPath)
		if err != nil || logical != metadata.LogicalPath || seenPaths[logical] {
			return nil, errors.New("save revision contains an unsafe logical path")
		}
		seenPaths[logical] = true
		logicalPaths[metadata.ID] = logical
	}
	return logicalPaths, nil
}

func (s *Server) downloadSaveRevisionArchive(w http.ResponseWriter, r *http.Request) {
	revision, err := s.store.GetSaveRevision(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	// Verify every referenced blob before committing a successful response.
	// OpenRevisionFile also repeats verification while streaming, so a race or
	// post-preflight corruption produces an unusable truncated ZIP rather than
	// silently returning bad bytes.
	logicalPaths, err := saveArchiveLogicalPaths(revision.Files)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, metadata := range revision.Files {
		handle, _, openErr := s.saves.OpenRevisionFile(r.Context(), revision.ID, metadata.ID)
		if openErr != nil {
			writeError(w, openErr)
			return
		}
		if closeErr := handle.Close(); closeErr != nil {
			writeError(w, closeErr)
			return
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, "save-revision-"+revision.ID[:min(8, len(revision.ID))]+".zip"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+revision.ContentHash+`-zip-v1"`)
	w.WriteHeader(http.StatusOK)
	archive := zip.NewWriter(w)
	for _, metadata := range revision.Files {
		handle, _, openErr := s.saves.OpenRevisionFile(r.Context(), revision.ID, metadata.ID)
		if openErr != nil {
			_ = archive.Close()
			return
		}
		header := &zip.FileHeader{Name: logicalPaths[metadata.ID], Method: zip.Deflate}
		mode := os.FileMode(metadata.Mode).Perm()
		if mode == 0 {
			mode = 0o600
		}
		header.SetMode(mode)
		entry, createErr := archive.CreateHeader(header)
		if createErr == nil {
			_, createErr = io.Copy(entry, handle)
		}
		closeErr := handle.Close()
		if createErr != nil || closeErr != nil {
			_ = archive.Close()
			return
		}
	}
	_ = archive.Close()
}
