package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/importer"
	storagex "varkiv/internal/storage"
)

const sourceScanLifetime = 15 * time.Minute

type sourceScanCommitRequest struct {
	PreviewToken   string   `json:"preview_token"`
	SelectedTokens []string `json:"selected_tokens"`
}

func sourceEnabled(value bool) *bool { return &value }

func (s *Server) validateLibrarySource(ctx context.Context, in catalog.NewLibrarySource) (catalog.NewLibrarySource, error) {
	var err error
	in.Platform, err = s.canonicalPlatform(ctx, in.Platform)
	if err != nil {
		return in, err
	}
	if in.Enabled == nil {
		in.Enabled = sourceEnabled(true)
	}
	var path string
	err = nil
	if strings.EqualFold(strings.TrimSpace(in.Kind), "rom_directory") {
		path, err = s.libraryEntry(in.RootPath)
	} else {
		path, err = s.libraryFile(in.MetadataPath)
	}
	if err != nil {
		return in, err
	}
	if strings.EqualFold(strings.TrimSpace(in.Kind), "rom_directory") {
		if _, err = os.Stat(path); err != nil {
			return in, err
		}
	} else if strings.TrimSpace(in.RootPath) != "" {
		contentRoot, rootErr := s.libraryEntry(in.RootPath)
		if rootErr != nil {
			return in, fmt.Errorf("root_path: %w", rootErr)
		}
		info, rootErr := os.Lstat(contentRoot)
		if rootErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return in, errors.New("root_path must be an existing real directory inside the library root")
		}
	}
	if strings.EqualFold(strings.TrimSpace(in.Kind), "esde") && strings.TrimSpace(in.RuntimeMetadataPath) != "" {
		if _, err = s.libraryFile(in.RuntimeMetadataPath); err != nil {
			return in, fmt.Errorf("runtime_metadata_path: %w", err)
		}
	}
	return in, nil
}

func (s *Server) listLibrarySources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListLibrarySources(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) createLibrarySource(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewLibrarySource
	if !decode(w, r, &in) {
		return
	}
	var err error
	if in, err = s.validateLibrarySource(r.Context(), in); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.CreateLibrarySource(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getLibrarySource(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetLibrarySource(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateLibrarySource(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewLibrarySource
	if !decode(w, r, &in) {
		return
	}
	var err error
	if in, err = s.validateLibrarySource(r.Context(), in); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.UpdateLibrarySource(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteLibrarySource(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteLibrarySource(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sourceRequests(source catalog.LibrarySource) (importRequest, romImportRequest) {
	if source.Kind == "rom_directory" {
		return importRequest{}, romImportRequest{Source: source.RootPath, Platform: source.Platform, ROMStorage: source.ROMStoragePolicy}
	}
	format := source.Kind
	if format == "esde" {
		format = "es-de"
	}
	contentRoot := source.RootPath
	if contentRoot == filepath.ToSlash(filepath.Dir(filepath.FromSlash(source.MetadataPath))) {
		contentRoot = ""
	}
	return importRequest{Format: format, Source: source.MetadataPath, ContentRoot: contentRoot, RuntimeSource: source.RuntimeMetadataPath, Platform: source.Platform, Locale: source.MetadataLocale, ROMStorage: source.ROMStoragePolicy, MediaStorage: source.MediaStoragePolicy}, romImportRequest{}
}

func (s *Server) validateSourceAdapterForScan(ctx context.Context, source catalog.LibrarySource) error {
	adapter, err := s.store.GetSourceAdapter(ctx, source.SourceAdapterID)
	if err != nil {
		return fmt.Errorf("source adapter is unavailable: %w", err)
	}
	if !adapter.Enabled {
		return errors.New("source adapter is disabled; enable it or choose another compatible adapter")
	}
	if adapter.Handler != source.Kind {
		return errors.New("source adapter handler no longer matches this source")
	}
	return nil
}

func (s *Server) previewLibrarySource(ctx context.Context, source catalog.LibrarySource) (string, []importCandidate, error) {
	if err := s.validateSourceAdapterForScan(ctx, source); err != nil {
		return "", nil, err
	}
	metadata, rom := sourceRequests(source)
	if source.Kind == "rom_directory" {
		discovered, err := s.discoverROMImport(ctx, rom)
		if err != nil {
			return "", nil, err
		}
		return s.sealImportPreview(romTokenContext(rom), romImportCandidates(discovered))
	}
	games, err := s.parseImport(ctx, metadata)
	if err != nil {
		return "", nil, err
	}
	candidates, err := s.importCandidates(ctx, games)
	if err != nil {
		return "", nil, err
	}
	return s.sealImportPreview(metadataTokenContext(metadata), candidates)
}

func candidateCounts(candidates []importCandidate) (importable, missing, duplicate, conflict int) {
	for _, candidate := range candidates {
		switch candidate.Status {
		case "new", "append":
			importable++
		case "missing":
			missing++
		case "duplicate":
			duplicate++
		case "conflict":
			conflict++
		}
	}
	return
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) createSourceScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	source, err := s.store.GetLibrarySource(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if !source.Enabled {
		writeError(w, errors.New("library source must be enabled before scanning"))
		return
	}
	started := time.Now().UTC()
	previewToken, candidates, err := s.previewLibrarySource(ctx, source)
	if err != nil {
		_, _ = s.store.CreateSourceScan(ctx, catalog.NewSourceScan{SourceID: source.ID, Status: "failed", StartedAt: started, FinishedAt: time.Now().UTC(), FailureCode: "scan_failed", FailureDetail: err.Error()})
		writeError(w, err)
		return
	}
	importable, missing, duplicate, conflict := candidateCounts(candidates)
	scan, err := s.store.CreateSourceScan(ctx, catalog.NewSourceScan{
		SourceID: source.ID, Status: "ready", StartedAt: started, FinishedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(sourceScanLifetime),
		CandidateCount: len(candidates), ImportableCount: importable, MissingCount: missing, DuplicateCount: duplicate, ConflictCount: conflict, PreviewTokenHash: tokenDigest(previewToken),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	metadata, _ := sourceRequests(source)
	writeJSON(w, http.StatusCreated, map[string]any{"scan": scan, "preview_token": previewToken, "candidates": candidates, "source_diagnostics": s.importSourceDiagnostics(ctx, metadata, candidates)})
}

func (s *Server) listSourceScans(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSourceScans(r.Context(), strings.TrimSpace(r.URL.Query().Get("source_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) getSourceScan(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetSourceScan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) commitSourceScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scan, err := s.store.GetSourceScan(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if scan.Status != "ready" {
		writeError(w, fmt.Errorf("source scan must be ready, current status is %s", scan.Status))
		return
	}
	var in sourceScanCommitRequest
	if !decode(w, r, &in) {
		return
	}
	expectedHash, err := s.store.SourceScanTokenHash(ctx, scan.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if time.Now().UTC().After(scan.ExpiresAt) || expectedHash == "" || tokenDigest(in.PreviewToken) != expectedHash {
		_, _ = s.store.UpdateSourceScanStatus(ctx, scan.ID, "stale", "import_preview_stale", "the preview expired or does not belong to this scan")
		writeError(w, errImportPreviewStale)
		return
	}
	source, err := s.store.GetLibrarySource(ctx, scan.SourceID)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.commitLibrarySource(ctx, source, in.PreviewToken, in.SelectedTokens)
	if err != nil {
		status, code := "failed", "import_failed"
		if errors.Is(err, errImportPreviewStale) {
			status, code = "stale", "import_preview_stale"
		}
		_, _ = s.store.UpdateSourceScanStatus(ctx, scan.ID, status, code, err.Error())
		writeError(w, err)
		return
	}
	completed, err := s.store.UpdateSourceScanStatus(ctx, scan.ID, "committed", "", "")
	if err != nil {
		writeError(w, err)
		return
	}
	result["scan"] = completed
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) commitLibrarySource(ctx context.Context, source catalog.LibrarySource, previewToken string, selectedTokens []string) (map[string]any, error) {
	if err := s.validateSourceAdapterForScan(ctx, source); err != nil {
		return nil, err
	}
	metadata, rom := sourceRequests(source)
	var games []catalog.ImportedGame
	var candidates []importCandidate
	var expectedPreview string
	var err error
	if source.Kind == "rom_directory" {
		discovered, discoverErr := s.discoverROMImport(ctx, rom)
		if discoverErr != nil {
			return nil, discoverErr
		}
		candidates = romImportCandidates(discovered)
		expectedPreview, candidates, err = s.sealImportPreview(romTokenContext(rom), candidates)
		if err != nil {
			return nil, err
		}
		games = make([]catalog.ImportedGame, len(discovered))
		for index := range discovered {
			games[index] = discovered[index].Game
		}
	} else {
		games, err = s.parseImport(ctx, metadata)
		if err != nil {
			return nil, err
		}
		candidates, err = s.importCandidates(ctx, games)
		if err != nil {
			return nil, err
		}
		expectedPreview, candidates, err = s.sealImportPreview(metadataTokenContext(metadata), candidates)
		if err != nil {
			return nil, err
		}
	}
	selected, err := verifyImportSelection(expectedPreview, previewToken, candidates, selectedTokens)
	if err != nil {
		return nil, err
	}
	for index := range selected {
		if candidates[index].Status != "new" && candidates[index].Status != "append" {
			return nil, fmt.Errorf("selected import item %d must be importable (status %s)", index, candidates[index].Status)
		}
	}
	ingestedBatch := make([]storagex.IngestResult, 0, len(selected))
	romFiles, mediaFiles := 0, 0
	var romBytes, mediaBytes int64
	for index, game := range games {
		if !selected[index] {
			continue
		}
		mediaPolicy := source.MediaStoragePolicy
		if source.Kind == "rom_directory" {
			mediaPolicy = "ignore"
		}
		ingested, ingestErr := s.storage.IngestGame(ctx, game, source.ROMStoragePolicy, mediaPolicy)
		if ingestErr != nil {
			cleanupIngested(ingestedBatch)
			return nil, ingestErr
		}
		ingestedBatch = append(ingestedBatch, ingested)
		romFiles += ingested.ROMFiles
		romBytes += ingested.ROMBytes
		mediaFiles += ingested.MediaFiles
		mediaBytes += ingested.MediaBytes
	}
	batch := make([]catalog.ImportedGame, len(ingestedBatch))
	for index := range ingestedBatch {
		batch[index] = ingestedBatch[index].Game
	}
	definitions, definitionErr := importer.CustomPlatformDefinitions(batch)
	if definitionErr != nil {
		cleanupIngested(ingestedBatch)
		return nil, definitionErr
	}
	if err = s.store.ImportGamesAndCustomPlatformsAtomic(ctx, batch, definitions); err != nil {
		cleanupIngested(ingestedBatch)
		return nil, err
	}
	return map[string]any{"parsed": len(games), "imported": len(batch), "skipped": len(games) - len(batch), "failure_policy": "atomic", "rom_storage": source.ROMStoragePolicy, "media_storage": source.MediaStoragePolicy, "rom_files_copied": romFiles, "rom_bytes_copied": romBytes, "media_files_copied": mediaFiles, "media_bytes_copied": mediaBytes}, nil
}
