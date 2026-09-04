package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
)

const libraryManifestName = "library-manifest.json"
const maxLibraryManifestSize = 16 << 20

// ImportLibraryManifest restores a neutral, multi-platform manifest through
// the same missing-ROM filtering and atomic catalog commit used by frontend
// imports. The manifest and every referenced artifact must remain inside the
// explicitly configured library root.
func ImportLibraryManifest(ctx context.Context, store *catalog.Store, libraryRoot, manifestPath string) (ImportResult, error) {
	games, err := PreviewLibraryManifest(libraryRoot, manifestPath)
	if err != nil {
		return ImportResult{}, err
	}
	return Commit(ctx, store, games)
}

type importedLibraryManifest struct {
	FormatVersion   int                         `json:"format_version"`
	Frontend        string                      `json:"frontend"`
	CustomPlatforms []catalog.NewCustomPlatform `json:"custom_platforms"`
	Series          []struct {
		ID           string            `json:"id"`
		DefaultTitle string            `json:"default_title"`
		Description  string            `json:"description"`
		Titles       map[string]string `json:"titles"`
		Members      []struct {
			GameID       string `json:"game_id"`
			RelationType string `json:"relation_type"`
			SortOrder    int    `json:"sort_order"`
		} `json:"members"`
	} `json:"series"`
	Entries []importedManifestEntry `json:"entries"`
}

type importedManifestArtifact struct {
	Path         string `json:"path"`
	Role         string `json:"role"`
	DiscIndex    int    `json:"disc_index"`
	OriginalName string `json:"original_name"`
	Size         *int64 `json:"size"`
	SHA256       string `json:"sha256"`
}

type importedManifestMedia struct {
	OwnerType    string `json:"owner_type"`
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	OriginalName string `json:"original_name"`
	MIMEType     string `json:"mime_type"`
	Size         *int64 `json:"size"`
	SHA256       string `json:"sha256"`
	Locale       string `json:"locale"`
	SortOrder    int    `json:"sort_order"`
}

type importedManifestEntry struct {
	GameID              string                     `json:"game_id"`
	EditionID           string                     `json:"edition_id"`
	Platform            string                     `json:"platform"`
	GameDefaultTitle    string                     `json:"game_default_title"`
	GameTitles          map[string]string          `json:"game_titles"`
	EditionDefaultTitle string                     `json:"edition_default_title"`
	EditionTitles       map[string]string          `json:"edition_titles"`
	EditionType         string                     `json:"edition_type"`
	Version             string                     `json:"version"`
	Languages           []string                   `json:"languages"`
	Author              string                     `json:"author"`
	Serial              string                     `json:"serial"`
	ProductCode         string                     `json:"product_code"`
	TitleID             string                     `json:"title_id"`
	Artifacts           []string                   `json:"artifacts"`
	ArtifactRecords     []importedManifestArtifact `json:"artifact_records"`
	Media               []importedManifestMedia    `json:"media"`
}

// PreviewLibraryManifest parses the neutral recovery manifest directly. It is
// intentionally read-only: every ROM is resolved below libraryRoot and hashed
// before becoming a candidate; missing files remain explicit missing
// artifacts so the server cannot commit them.
func PreviewLibraryManifest(libraryRoot, manifestPath string) ([]catalog.ImportedGame, error) {
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return nil, err
	}
	manifestPath, err = filepath.Abs(manifestPath)
	if err != nil {
		return nil, err
	}
	if !pathInside(root, manifestPath) {
		return nil, errors.New("library manifest is outside the configured library root")
	}
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("library manifest must be an exact regular file, not a symlink")
	}
	if info.Size() > maxLibraryManifestSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", libraryManifestName, maxLibraryManifestSize)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolvedManifest, err := filepath.EvalSymlinks(manifestPath)
	if err != nil || !pathInside(resolvedRoot, resolvedManifest) {
		return nil, errors.New("library manifest resolves outside the configured library root")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest importedLibraryManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode library manifest: %w", err)
	}
	if manifest.FormatVersion != 4 && manifest.FormatVersion != 5 && manifest.FormatVersion != 6 {
		return nil, errors.New("direct library manifest import requires format_version 4, 5, or 6")
	}
	if len(manifest.Entries) > 50000 {
		return nil, errors.New("library manifest exceeds 50000 entries")
	}
	definitionByID := map[string]catalog.NewCustomPlatform{}
	definitionPlatforms := map[string]catalog.CustomPlatform{}
	registryItems := platforms.All()
	if manifest.FormatVersion < 6 && len(manifest.CustomPlatforms) > 0 {
		return nil, errors.New("custom_platforms requires library manifest format_version 6")
	}
	if len(manifest.CustomPlatforms) > 256 {
		return nil, errors.New("library manifest exceeds 256 custom platforms")
	}
	for index, definition := range manifest.CustomPlatforms {
		if definition.Enabled != nil && !*definition.Enabled {
			return nil, fmt.Errorf("library manifest custom platform %d must be enabled", index)
		}
		normalized, normalizeErr := catalog.NormalizeCustomPlatform(definition)
		if normalizeErr != nil {
			return nil, fmt.Errorf("library manifest custom platform %d: %w", index, normalizeErr)
		}
		if _, builtin := platforms.Resolve(normalized.ID); builtin {
			return nil, fmt.Errorf("library manifest custom platform %d shadows built-in platform %q", index, normalized.ID)
		}
		if _, repeated := definitionByID[normalized.ID]; repeated {
			return nil, fmt.Errorf("library manifest repeats custom platform %q", normalized.ID)
		}
		portable := normalized.PortableDefinition()
		definitionByID[normalized.ID] = portable
		definitionPlatforms[normalized.ID] = normalized
		registryItems = append(registryItems, normalized.Platform())
	}
	manifestRegistry, registryErr := platforms.NewRegistry(registryItems)
	if registryErr != nil {
		return nil, fmt.Errorf("library manifest custom platforms: %w", registryErr)
	}
	usedDefinitions := map[string]bool{}
	seriesByGame := map[string][]catalog.ImportedSeriesMembership{}
	for _, series := range manifest.Series {
		if strings.TrimSpace(series.ID) == "" || strings.TrimSpace(series.DefaultTitle) == "" {
			return nil, errors.New("library manifest series require stable id and default_title")
		}
		for _, member := range series.Members {
			seriesByGame[member.GameID] = append(seriesByGame[member.GameID], catalog.ImportedSeriesMembership{
				Series:       catalog.NewSeries{ID: series.ID, DefaultTitle: series.DefaultTitle, Description: series.Description, Titles: series.Titles},
				RelationType: member.RelationType,
				SortOrder:    member.SortOrder,
			})
		}
	}
	metadataDir := filepath.Dir(manifestPath)
	games := make([]catalog.ImportedGame, 0, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		if strings.TrimSpace(entry.GameID) == "" || strings.TrimSpace(entry.EditionID) == "" || strings.TrimSpace(entry.Platform) == "" {
			return nil, fmt.Errorf("library manifest entry %d requires game_id, edition_id, and platform", index)
		}
		if len(entry.Artifacts) == 0 || len(entry.Artifacts) > 64 {
			return nil, fmt.Errorf("library manifest entry %d must contain between 1 and 64 artifacts", index)
		}
		if manifest.FormatVersion >= 5 && len(entry.ArtifactRecords) != len(entry.Artifacts) {
			return nil, fmt.Errorf("library manifest entry %d must describe every artifact in artifact_records", index)
		}
		if len(entry.Media) > 256 {
			return nil, fmt.Errorf("library manifest entry %d exceeds 256 media items", index)
		}
		platformID := strings.ToLower(strings.TrimSpace(entry.Platform))
		if manifest.FormatVersion >= 6 {
			resolved, ok := manifestRegistry.Resolve(platformID)
			if !ok {
				return nil, fmt.Errorf("library manifest entry %d uses undefined platform %q", index, platformID)
			}
			platformID = resolved.ID
		}
		game := catalog.ImportedGame{
			GameID: entry.GameID, EditionID: entry.EditionID, Platform: platformID,
			DefaultTitle: entry.GameDefaultTitle, Titles: entry.GameTitles, EditionTitle: entry.EditionDefaultTitle, EditionTitles: entry.EditionTitles,
			EditionType: entry.EditionType, Version: entry.Version, Languages: entry.Languages, Author: entry.Author,
			Serial: entry.Serial, ProductCode: entry.ProductCode, TitleID: entry.TitleID,
			Artifacts: []catalog.NewArtifact{}, Media: []catalog.NewMediaAsset{}, SeriesMemberships: seriesByGame[entry.GameID],
		}
		if definition, ok := definitionByID[platformID]; ok {
			copy := definition
			game.PlatformDefinition = &copy
			usedDefinitions[platformID] = true
		}
		if strings.TrimSpace(game.DefaultTitle) == "" {
			game.DefaultTitle = strings.TrimSpace(game.EditionTitle)
		}
		if strings.TrimSpace(game.EditionTitle) == "" {
			game.EditionTitle = strings.TrimSpace(game.DefaultTitle)
		}
		if game.DefaultTitle == "" {
			return nil, fmt.Errorf("library manifest entry %d has no title", index)
		}
		if game.Titles == nil {
			game.Titles = map[string]string{}
		}
		if game.EditionTitles == nil {
			game.EditionTitles = map[string]string{}
		}
		if game.EditionType == "" {
			game.EditionType = "original"
		}
		artifacts, artifactErr := restoreManifestArtifacts(root, metadataDir, manifest.FormatVersion, entry.Artifacts, entry.ArtifactRecords)
		if artifactErr != nil {
			return nil, fmt.Errorf("library manifest entry %d: %w", index, artifactErr)
		}
		game.Artifacts = artifacts
		for mediaIndex, media := range entry.Media {
			if media.OwnerType != "game" && media.OwnerType != "edition" {
				return nil, fmt.Errorf("library manifest entry %d media %d has invalid owner_type", index, mediaIndex)
			}
			asset, exists, mediaErr := libraryMedia(root, metadataDir, media.Path, media.Kind)
			if mediaErr != nil {
				return nil, fmt.Errorf("library manifest entry %d media %d: %w", index, mediaIndex, mediaErr)
			}
			if !exists {
				continue
			}
			if manifest.FormatVersion >= 5 {
				if mediaErr = validateManifestMedia(asset, media); mediaErr != nil {
					return nil, fmt.Errorf("library manifest entry %d media %d: %w", index, mediaIndex, mediaErr)
				}
			}
			if media.OriginalName != "" {
				asset.OriginalName = media.OriginalName
			}
			if media.MIMEType != "" {
				asset.MIMEType = media.MIMEType
			}
			asset.Locale, asset.SortOrder = media.Locale, media.SortOrder
			if media.OwnerType == "game" {
				asset.GameID = entry.GameID
			} else {
				asset.EditionID = entry.EditionID
			}
			game.Media = upsertManifestMedia(game.Media, asset)
		}
		games = append(games, game)
	}
	for id := range definitionPlatforms {
		if !usedDefinitions[id] {
			return nil, fmt.Errorf("library manifest custom platform %q is not used by any entry", id)
		}
	}
	return attachStructuredLaunchHints(root, manifestPath, games)
}

func attachLibraryManifest(libraryRoot, metadataPath, frontend string, games []catalog.ImportedGame) ([]catalog.ImportedGame, error) {
	manifestPath, err := findAncestorFile(libraryRoot, metadataPath, libraryManifestName, maxLibraryManifestSize)
	if err != nil || manifestPath == "" {
		return games, err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest importedLibraryManifest
	if json.Unmarshal(data, &manifest) != nil || (manifest.FormatVersion != 3 && manifest.FormatVersion != 4 && manifest.FormatVersion != 5 && manifest.FormatVersion != 6) || (manifest.Frontend != "" && !strings.EqualFold(manifest.Frontend, frontend)) {
		return games, nil
	}
	portablePlatforms := map[string]catalog.NewCustomPlatform{}
	if manifest.FormatVersion >= 6 {
		registryItems := platforms.All()
		for index, definition := range manifest.CustomPlatforms {
			normalized, normalizeErr := catalog.NormalizeCustomPlatform(definition)
			if normalizeErr != nil {
				return nil, fmt.Errorf("library manifest custom platform %d: %w", index, normalizeErr)
			}
			if _, builtin := platforms.Resolve(normalized.ID); builtin {
				return nil, fmt.Errorf("library manifest custom platform %d shadows built-in platform %q", index, normalized.ID)
			}
			if _, exists := portablePlatforms[normalized.ID]; exists {
				return nil, fmt.Errorf("library manifest repeats custom platform %q", normalized.ID)
			}
			portablePlatforms[normalized.ID] = normalized.PortableDefinition()
			registryItems = append(registryItems, normalized.Platform())
		}
		if _, registryErr := platforms.NewRegistry(registryItems); registryErr != nil {
			return nil, fmt.Errorf("library manifest custom platforms: %w", registryErr)
		}
	}
	used := map[int]bool{}
	gameMediaAdded := map[string]bool{}
	for gameIndex := range games {
		entryIndex := -1
		for candidateIndex, entry := range manifest.Entries {
			if used[candidateIndex] || (entry.Platform != "" && !strings.EqualFold(entry.Platform, games[gameIndex].Platform)) {
				continue
			}
			if manifestEntryMatches(entry.Artifacts, games[gameIndex].Artifacts) {
				entryIndex = candidateIndex
				break
			}
		}
		if entryIndex < 0 {
			continue
		}
		entry := manifest.Entries[entryIndex]
		if (games[gameIndex].GameID != "" && games[gameIndex].GameID != entry.GameID) || (games[gameIndex].EditionID != "" && games[gameIndex].EditionID != entry.EditionID) {
			continue
		}
		used[entryIndex] = true
		if entry.GameID != "" {
			games[gameIndex].GameID = entry.GameID
		}
		if entry.EditionID != "" {
			games[gameIndex].EditionID = entry.EditionID
		}
		if definition, ok := portablePlatforms[strings.ToLower(strings.TrimSpace(entry.Platform))]; ok {
			copy := definition
			games[gameIndex].Platform = copy.ID
			games[gameIndex].PlatformDefinition = &copy
		}
		if manifest.FormatVersion >= 4 {
			if strings.TrimSpace(entry.GameDefaultTitle) != "" {
				games[gameIndex].DefaultTitle = entry.GameDefaultTitle
			}
			if entry.GameTitles != nil {
				games[gameIndex].Titles = entry.GameTitles
			}
			if strings.TrimSpace(entry.EditionDefaultTitle) != "" {
				games[gameIndex].EditionTitle = entry.EditionDefaultTitle
			}
			if entry.EditionTitles != nil {
				games[gameIndex].EditionTitles = entry.EditionTitles
			}
			if entry.EditionType != "" {
				games[gameIndex].EditionType = entry.EditionType
			}
			games[gameIndex].Version, games[gameIndex].Languages, games[gameIndex].Author = entry.Version, entry.Languages, entry.Author
			games[gameIndex].Serial, games[gameIndex].ProductCode, games[gameIndex].TitleID = entry.Serial, entry.ProductCode, entry.TitleID
			artifacts, artifactErr := restoreManifestArtifacts(libraryRoot, filepath.Dir(manifestPath), manifest.FormatVersion, entry.Artifacts, entry.ArtifactRecords)
			if artifactErr != nil {
				return nil, artifactErr
			}
			artifacts = availableManifestArtifacts(artifacts)
			if len(artifacts) > 0 {
				games[gameIndex].Artifacts = artifacts
			}
			manifestMedia := []catalog.NewMediaAsset{}
			manifestMediaPaths := map[string]bool{}
			for _, media := range entry.Media {
				asset, exists, mediaErr := libraryMedia(libraryRoot, filepath.Dir(manifestPath), media.Path, media.Kind)
				if mediaErr != nil || !exists {
					continue
				}
				if manifest.FormatVersion >= 5 {
					if mediaErr = validateManifestMedia(asset, media); mediaErr != nil {
						return nil, mediaErr
					}
				}
				if media.OriginalName != "" {
					asset.OriginalName = media.OriginalName
				}
				asset.Locale, asset.SortOrder = media.Locale, media.SortOrder
				if media.MIMEType != "" {
					asset.MIMEType = media.MIMEType
				}
				manifestMediaPaths[asset.Path] = true
				if media.OwnerType == "game" && gameMediaAdded[entry.GameID] {
					continue
				}
				if media.OwnerType == "game" {
					asset.GameID = entry.GameID
				} else {
					asset.EditionID = entry.EditionID
				}
				manifestMedia = append(manifestMedia, asset)
			}
			if len(manifestMediaPaths) > 0 {
				frontendMedia := make([]catalog.NewMediaAsset, 0, len(games[gameIndex].Media))
				for _, asset := range games[gameIndex].Media {
					if !manifestMediaPaths[asset.Path] {
						frontendMedia = append(frontendMedia, asset)
					}
				}
				games[gameIndex].Media = frontendMedia
			}
			for _, asset := range manifestMedia {
				games[gameIndex].Media = upsertManifestMedia(games[gameIndex].Media, asset)
			}
			gameMediaAdded[entry.GameID] = true
		}
		for _, series := range manifest.Series {
			for _, member := range series.Members {
				if member.GameID == games[gameIndex].GameID && series.ID != "" && series.DefaultTitle != "" {
					games[gameIndex].SeriesMemberships = append(games[gameIndex].SeriesMemberships, catalog.ImportedSeriesMembership{Series: catalog.NewSeries{ID: series.ID, DefaultTitle: series.DefaultTitle, Description: series.Description, Titles: series.Titles}, RelationType: member.RelationType, SortOrder: member.SortOrder})
				}
			}
		}
	}
	return games, nil
}

func restoreManifestArtifacts(libraryRoot, metadataDir string, formatVersion int, paths []string, records []importedManifestArtifact) ([]catalog.NewArtifact, error) {
	if formatVersion >= 5 && len(records) != len(paths) {
		return nil, errors.New("artifact_records must describe every artifact path")
	}
	artifacts := make([]catalog.NewArtifact, 0, len(paths))
	seen := map[string]bool{}
	for index, value := range paths {
		record := importedManifestArtifact{Path: value, Role: "rom", DiscIndex: index + 1}
		if formatVersion >= 5 {
			record = records[index]
			if cleanManifestDeclaredPath(record.Path) != cleanManifestDeclaredPath(value) {
				return nil, fmt.Errorf("artifact %d path disagrees with the legacy path list", index)
			}
			record.Role = strings.ToLower(strings.TrimSpace(record.Role))
			if !catalog.ValidArtifactRole(record.Role) {
				return nil, fmt.Errorf("artifact %d has unsupported role %q", index, record.Role)
			}
			if record.DiscIndex < 0 || record.DiscIndex > 64 {
				return nil, fmt.Errorf("artifact %d has invalid disc_index", index)
			}
			if record.OriginalName != "" && (len(record.OriginalName) > 255 || filepath.Base(record.OriginalName) != record.OriginalName || strings.ContainsAny(record.OriginalName, `/\\`)) {
				return nil, fmt.Errorf("artifact %d has invalid original_name", index)
			}
			if record.Size != nil && *record.Size < 0 {
				return nil, fmt.Errorf("artifact %d has invalid size", index)
			}
			if record.SHA256 != "" && !validSHA256(record.SHA256) {
				return nil, fmt.Errorf("artifact %d has invalid sha256", index)
			}
		}
		artifact, err := libraryArtifact(libraryRoot, metadataDir, value)
		if err != nil {
			return nil, fmt.Errorf("artifact %d: %w", index, err)
		}
		if seen[artifact.Path] {
			return nil, fmt.Errorf("artifact %d repeats path %q", index, artifact.Path)
		}
		seen[artifact.Path] = true
		artifact.Role = record.Role
		artifact.DiscIndex = record.DiscIndex
		if record.OriginalName != "" {
			artifact.OriginalName = record.OriginalName
		}
		if !artifact.Missing && record.Size != nil && artifact.Size != *record.Size {
			return nil, fmt.Errorf("artifact %d size changed since the manifest was generated", index)
		}
		if !artifact.Missing && record.SHA256 != "" && !strings.EqualFold(artifact.SHA256, record.SHA256) {
			return nil, fmt.Errorf("artifact %d hash changed since the manifest was generated", index)
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func cleanManifestDeclaredPath(value string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(value))))
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func availableManifestArtifacts(artifacts []catalog.NewArtifact) []catalog.NewArtifact {
	available := make([]catalog.NewArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !artifact.Missing {
			available = append(available, artifact)
		}
	}
	return available
}

func validateManifestMedia(asset catalog.NewMediaAsset, media importedManifestMedia) error {
	if media.Size != nil && *media.Size < 0 {
		return errors.New("media has invalid size")
	}
	if media.Size != nil && asset.Size != *media.Size {
		return errors.New("media size changed since the manifest was generated")
	}
	if media.SHA256 != "" {
		if !validSHA256(media.SHA256) {
			return errors.New("media has invalid sha256")
		}
		if !strings.EqualFold(asset.SHA256, media.SHA256) {
			return errors.New("media hash changed since the manifest was generated")
		}
	}
	return nil
}

func manifestEntryMatches(paths []string, artifacts []catalog.NewArtifact) bool {
	for _, left := range paths {
		left, leftOK := cleanManifestROMPath(left)
		if !leftOK {
			continue
		}
		for _, artifact := range artifacts {
			right, rightOK := cleanManifestROMPath(artifact.Path)
			if rightOK && left == right {
				return true
			}
		}
	}
	return false
}

func appendUniqueArtifact(items []catalog.NewArtifact, candidate catalog.NewArtifact) []catalog.NewArtifact {
	for _, item := range items {
		if item.Path == candidate.Path {
			return items
		}
	}
	return append(items, candidate)
}

func upsertManifestMedia(items []catalog.NewMediaAsset, candidate catalog.NewMediaAsset) []catalog.NewMediaAsset {
	for index, item := range items {
		if item.Path == candidate.Path && item.Kind == candidate.Kind {
			items[index] = candidate
			return items
		}
	}
	return append(items, candidate)
}
