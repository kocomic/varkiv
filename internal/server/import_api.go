package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"varkiv/internal/catalog"
	"varkiv/internal/importer"
	"varkiv/internal/scanner"
	storagex "varkiv/internal/storage"
)

func safeSegment(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

type importRequest struct {
	Format         string   `json:"format"`
	Source         string   `json:"source"`
	ContentRoot    string   `json:"content_root,omitempty"`
	RuntimeSource  string   `json:"runtime_source,omitempty"`
	Platform       string   `json:"platform"`
	Locale         string   `json:"locale"`
	ROMStorage     string   `json:"rom_storage,omitempty"`
	MediaStorage   string   `json:"media_storage,omitempty"`
	PreviewToken   string   `json:"preview_token,omitempty"`
	SelectedTokens []string `json:"selected_tokens,omitempty"`
}

type importCandidate struct {
	Index              int                  `json:"index"`
	Status             string               `json:"status"`
	Reason             string               `json:"reason,omitempty"`
	Availability       string               `json:"availability"`
	AvailableArtifacts int                  `json:"available_artifacts"`
	MissingArtifacts   int                  `json:"missing_artifacts"`
	Token              string               `json:"token"`
	Game               catalog.ImportedGame `json:"game"`
}

type romImportRequest struct {
	Source         string   `json:"source"`
	Platform       string   `json:"platform"`
	ROMStorage     string   `json:"rom_storage,omitempty"`
	PreviewToken   string   `json:"preview_token,omitempty"`
	SelectedTokens []string `json:"selected_tokens,omitempty"`
}

func (s *Server) parseImport(ctx context.Context, in importRequest) ([]catalog.ImportedGame, error) {
	if err := validateImportStorage(in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Source) == "" {
		return nil, errors.New("source is required")
	}
	source, err := s.libraryFile(in.Source)
	if err != nil {
		return nil, err
	}
	platform := strings.TrimSpace(in.Platform)
	registry, err := s.store.PlatformRegistry(ctx)
	if err != nil {
		return nil, err
	}
	if preset, ok := registry.Resolve(platform); ok {
		platform = preset.ID
	}
	var games []catalog.ImportedGame
	switch strings.ToLower(strings.TrimSpace(in.Format)) {
	case "pegasus":
		if strings.TrimSpace(in.Platform) == "" {
			return nil, errors.New("platform is required for Pegasus import")
		}
		if strings.TrimSpace(in.RuntimeSource) != "" {
			return nil, errors.New("runtime_source is only supported for ES-DE")
		}
		games, err = importer.PreviewPegasusWithContentRoot(s.libraryRoot, source, strings.TrimSpace(in.ContentRoot), platform, strings.TrimSpace(in.Locale))
	case "es-de", "esde":
		if strings.TrimSpace(in.Platform) == "" {
			return nil, errors.New("platform is required for ES-DE import")
		}
		games, err = importer.PreviewESDEWithContentRootAndRuntimeRegistry(s.libraryRoot, source, strings.TrimSpace(in.ContentRoot), strings.TrimSpace(in.RuntimeSource), platform, strings.TrimSpace(in.Locale), registry)
	case "varkiv", "library-manifest":
		if strings.TrimSpace(in.ContentRoot) != "" {
			return nil, errors.New("content_root is only supported for Pegasus and ES-DE imports")
		}
		if strings.TrimSpace(in.RuntimeSource) != "" {
			return nil, errors.New("runtime_source is not accepted for a neutral manifest; place the reviewed varkiv-launches.json beside it")
		}
		games, err = importer.PreviewLibraryManifest(s.libraryRoot, source)
	default:
		return nil, errors.New("format must be pegasus, es-de, or varkiv")
	}
	if err != nil {
		return nil, err
	}
	definitions, err := importer.CustomPlatformDefinitions(games)
	if err != nil {
		return nil, err
	}
	if _, err = s.store.ValidateCustomPlatformImports(ctx, definitions); err != nil {
		return nil, err
	}
	portableRuntime, err := catalog.MergePortableRuntimeCatalogs(games)
	if err != nil {
		return nil, err
	}
	if portableRuntime.PackageProfile != nil {
		if err = validatePackageProfile(*portableRuntime.PackageProfile); err != nil {
			return nil, fmt.Errorf("portable package profile: %w", err)
		}
	}
	if _, err = s.store.ValidatePortableRuntimeCatalogImports(ctx, portableRuntime); err != nil {
		return nil, err
	}
	return games, nil
}

func validateImportStorage(in importRequest) error {
	romMode := defaultValue(in.ROMStorage, "reference")
	if romMode != "reference" && romMode != "copy" {
		return errors.New("rom_storage must be reference or copy")
	}
	mediaMode := defaultValue(in.MediaStorage, "copy")
	if mediaMode != "reference" && mediaMode != "copy" && mediaMode != "ignore" {
		return errors.New("media_storage must be reference, copy, or ignore")
	}
	return nil
}

func (s *Server) previewImport(w http.ResponseWriter, r *http.Request) {
	var in importRequest
	if !decode(w, r, &in) {
		return
	}
	games, err := s.parseImport(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	candidates, err := s.importCandidates(r.Context(), games)
	if err != nil {
		writeError(w, err)
		return
	}
	previewToken, candidates, err := s.sealImportPreview(metadataTokenContext(in), candidates)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"parsed": len(games), "preview_token": previewToken, "candidates": candidates, "source_diagnostics": s.importSourceDiagnostics(r.Context(), in, candidates)})
}

func (s *Server) importCandidates(ctx context.Context, games []catalog.ImportedGame) ([]importCandidate, error) {
	candidates := make([]importCandidate, 0, len(games))
	for i, game := range games {
		candidate := importCandidate{Index: i, Status: "new", Availability: "ready", Game: game}
		for _, artifact := range game.Artifacts {
			if artifact.Missing {
				candidate.MissingArtifacts++
			} else {
				candidate.AvailableArtifacts++
			}
			if existing, lookupErr := s.store.ArtifactByPath(ctx, artifact.Path); lookupErr == nil {
				candidate.Status, candidate.Reason = "duplicate", "文件路径已被资料库收录："+existing.Path
				break
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, lookupErr
			}
			if existing, lookupErr := s.store.ArtifactBySourcePath(ctx, artifact.Path); lookupErr == nil {
				candidate.Status, candidate.Reason = "duplicate", "来源文件已经导入："+existing.SourcePath
				break
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, lookupErr
			}
			if artifact.SHA256 != "" {
				if existing, lookupErr := s.store.ArtifactBySHA256(ctx, artifact.SHA256); lookupErr == nil {
					candidate.Status, candidate.Reason = "duplicate", "相同内容已经收录："+existing.Path
					break
				} else if !errors.Is(lookupErr, sql.ErrNoRows) {
					return nil, lookupErr
				}
			}
		}
		if candidate.MissingArtifacts > 0 {
			candidate.Availability = "partial"
			if candidate.AvailableArtifacts == 0 {
				candidate.Availability = "missing"
			}
			if candidate.Status == "new" {
				candidate.Status = "missing"
				candidate.Reason = fmt.Sprintf("元数据引用的 %d 个 ROM 文件不存在；此条目会跳过，文件到位后可重新扫描", candidate.MissingArtifacts)
			}
		}
		if candidate.Status == "new" && game.EditionID != "" {
			if _, lookupErr := s.store.GetEdition(ctx, game.EditionID, ""); lookupErr == nil {
				candidate.Status, candidate.Reason = "conflict", "版本 ID 已存在，但文件路径不同"
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, lookupErr
			}
		}
		if candidate.Status == "new" && game.GameID != "" {
			if _, lookupErr := s.store.GetGame(ctx, game.GameID, ""); lookupErr == nil {
				candidate.Status = "append"
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, lookupErr
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s *Server) commitImport(w http.ResponseWriter, r *http.Request) {
	var in importRequest
	if !decode(w, r, &in) {
		return
	}
	games, err := s.parseImport(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	candidates, err := s.importCandidates(r.Context(), games)
	if err != nil {
		writeError(w, err)
		return
	}
	expectedPreview, candidates, err := s.sealImportPreview(metadataTokenContext(in), candidates)
	if err != nil {
		writeError(w, err)
		return
	}
	selected, err := verifyImportSelection(expectedPreview, in.PreviewToken, candidates, in.SelectedTokens)
	if err != nil {
		writeError(w, err)
		return
	}
	for index := range selected {
		if candidates[index].Status != "new" && candidates[index].Status != "append" {
			writeError(w, fmt.Errorf("selected import item %d must be importable (status %s)", index, candidates[index].Status))
			return
		}
	}
	result := importer.ImportResult{Parsed: len(games), Skipped: len(games) - len(selected)}
	romFiles, mediaFiles := 0, 0
	var romBytes, mediaBytes int64
	ingestedBatch := make([]storagex.IngestResult, 0, len(selected))
	for index, game := range games {
		if !selected[index] {
			continue
		}
		ingested, ingestErr := s.storage.IngestGame(r.Context(), game, in.ROMStorage, in.MediaStorage)
		if ingestErr != nil {
			cleanupIngested(ingestedBatch)
			writeError(w, ingestErr)
			return
		}
		ingestedBatch = append(ingestedBatch, ingested)
		romFiles += ingested.ROMFiles
		romBytes += ingested.ROMBytes
		mediaFiles += ingested.MediaFiles
		mediaBytes += ingested.MediaBytes
	}
	batchGames := make([]catalog.ImportedGame, len(ingestedBatch))
	for index := range ingestedBatch {
		batchGames[index] = ingestedBatch[index].Game
	}
	definitions, definitionErr := importer.CustomPlatformDefinitions(batchGames)
	if definitionErr != nil {
		cleanupIngested(ingestedBatch)
		writeError(w, definitionErr)
		return
	}
	if err = s.store.ImportGamesAndCustomPlatformsAtomic(r.Context(), batchGames, definitions); err != nil {
		cleanupIngested(ingestedBatch)
		writeError(w, err)
		return
	}
	result.Imported = len(batchGames)
	writeJSON(w, 200, map[string]any{"parsed": result.Parsed, "imported": result.Imported, "skipped": result.Skipped, "failure_policy": "atomic", "rom_storage": defaultValue(in.ROMStorage, "reference"), "media_storage": defaultValue(in.MediaStorage, "copy"), "rom_files_copied": romFiles, "rom_bytes_copied": romBytes, "media_files_copied": mediaFiles, "media_bytes_copied": mediaBytes})
}

func (s *Server) discoverROMImport(ctx context.Context, in romImportRequest) ([]scanner.Candidate, error) {
	if strings.TrimSpace(in.Source) == "" || strings.TrimSpace(in.Platform) == "" {
		return nil, errors.New("source and platform are required")
	}
	romMode := defaultValue(in.ROMStorage, "reference")
	if romMode != "reference" && romMode != "copy" {
		return nil, errors.New("rom_storage must be reference or copy")
	}
	source, err := s.libraryEntry(in.Source)
	if err != nil {
		return nil, err
	}
	platform := strings.TrimSpace(in.Platform)
	registry, err := s.store.PlatformRegistry(ctx)
	if err != nil {
		return nil, err
	}
	if preset, ok := registry.Resolve(platform); ok {
		platform = preset.ID
	}
	return scanner.DiscoverWithRegistry(ctx, s.store, s.libraryRoot, source, platform, registry)
}

func romImportCandidates(discovered []scanner.Candidate) []importCandidate {
	result := make([]importCandidate, 0, len(discovered))
	for index, item := range discovered {
		status := "new"
		available, missing, availability := 0, 0, "ready"
		for _, artifact := range item.Game.Artifacts {
			if artifact.Missing {
				missing++
			} else {
				available++
			}
		}
		reason := item.Reason
		if missing > 0 {
			status, availability = "missing", "partial"
			if available == 0 {
				availability = "missing"
			}
			reason = fmt.Sprintf("扫描到的入口还缺少 %d 个关联 ROM 文件；请先补齐再导入", missing)
		}
		if item.Duplicate {
			status = "duplicate"
		}
		result = append(result, importCandidate{Index: index, Status: status, Reason: reason, Availability: availability, AvailableArtifacts: available, MissingArtifacts: missing, Game: item.Game})
	}
	return result
}

func (s *Server) previewROMImport(w http.ResponseWriter, r *http.Request) {
	var in romImportRequest
	if !decode(w, r, &in) {
		return
	}
	discovered, err := s.discoverROMImport(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	previewToken, candidates, err := s.sealImportPreview(romTokenContext(in), romImportCandidates(discovered))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parsed": len(discovered), "preview_token": previewToken, "candidates": candidates, "source_diagnostics": []importSourceDiagnostic{}})
}

func (s *Server) commitROMImport(w http.ResponseWriter, r *http.Request) {
	var in romImportRequest
	if !decode(w, r, &in) {
		return
	}
	discovered, err := s.discoverROMImport(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	expectedPreview, candidates, err := s.sealImportPreview(romTokenContext(in), romImportCandidates(discovered))
	if err != nil {
		writeError(w, err)
		return
	}
	selected, err := verifyImportSelection(expectedPreview, in.PreviewToken, candidates, in.SelectedTokens)
	if err != nil {
		writeError(w, err)
		return
	}
	for index := range selected {
		candidate := candidates[index]
		if candidate.Status != "new" {
			writeError(w, fmt.Errorf("selected import item %d must be importable (status %s)", index, candidate.Status))
			return
		}
	}
	result := importer.ImportResult{Parsed: len(selected)}
	romFiles := 0
	var romBytes int64
	ingestedBatch := make([]storagex.IngestResult, 0, len(selected))
	for index, candidate := range discovered {
		if !selected[index] {
			continue
		}
		ingested, ingestErr := s.storage.IngestGame(r.Context(), candidate.Game, in.ROMStorage, "ignore")
		if ingestErr != nil {
			cleanupIngested(ingestedBatch)
			writeError(w, ingestErr)
			return
		}
		ingestedBatch = append(ingestedBatch, ingested)
		romFiles += ingested.ROMFiles
		romBytes += ingested.ROMBytes
	}
	batchGames := make([]catalog.ImportedGame, len(ingestedBatch))
	for index := range ingestedBatch {
		batchGames[index] = ingestedBatch[index].Game
	}
	if err = s.store.ImportGamesAtomic(r.Context(), batchGames); err != nil {
		cleanupIngested(ingestedBatch)
		writeError(w, err)
		return
	}
	result.Imported = len(batchGames)
	writeJSON(w, http.StatusOK, map[string]any{"parsed": result.Parsed, "imported": result.Imported, "skipped": result.Skipped, "failure_policy": "atomic", "rom_storage": defaultValue(in.ROMStorage, "reference"), "media_storage": "ignore", "rom_files_copied": romFiles, "rom_bytes_copied": romBytes, "media_files_copied": 0, "media_bytes_copied": 0})
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func cleanupIngested(results []storagex.IngestResult) {
	for _, result := range results {
		result.Cleanup()
	}
}

func (s *Server) libraryFile(value string) (string, error) {
	path, err := s.libraryEntry(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("source path must be an exact regular file, not a directory or symlink")
	}
	root, err := filepath.Abs(s.libraryRoot)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("source path resolves outside library root")
	}
	return path, nil
}

func (s *Server) libraryEntry(value string) (string, error) {
	root, err := filepath.Abs(s.libraryRoot)
	if err != nil {
		return "", err
	}
	path := filepath.FromSlash(strings.TrimSpace(value))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("source path must be inside library root")
	}
	if _, err = os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
