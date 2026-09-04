package exporter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

type manifestEntry struct {
	GameID              string             `json:"game_id"`
	EditionID           string             `json:"edition_id"`
	Platform            string             `json:"platform"`
	GameDefaultTitle    string             `json:"game_default_title"`
	GameTitles          map[string]string  `json:"game_titles"`
	EditionDefaultTitle string             `json:"edition_default_title"`
	EditionTitles       map[string]string  `json:"edition_titles"`
	EditionType         string             `json:"edition_type"`
	Version             string             `json:"version,omitempty"`
	Languages           []string           `json:"languages"`
	Author              string             `json:"author,omitempty"`
	Serial              string             `json:"serial,omitempty"`
	ProductCode         string             `json:"product_code,omitempty"`
	TitleID             string             `json:"title_id,omitempty"`
	Artifacts           []string           `json:"artifacts"`
	ArtifactRecords     []manifestArtifact `json:"artifact_records"`
	Media               []manifestMedia    `json:"media"`
}

// manifestArtifact keeps the library's resource semantics recoverable. The
// legacy Artifacts path array remains alongside it so older readers can still
// locate ROM files without understanding the v5 fields.
type manifestArtifact struct {
	Path         string `json:"path"`
	Role         string `json:"role"`
	DiscIndex    int    `json:"disc_index,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	Size         int64  `json:"size,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
}

type manifestMedia struct {
	OwnerType    string `json:"owner_type"`
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	OriginalName string `json:"original_name"`
	MIMEType     string `json:"mime_type"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256,omitempty"`
	Locale       string `json:"locale,omitempty"`
	SortOrder    int    `json:"sort_order"`
}

type manifestSeriesMember struct {
	GameID       string `json:"game_id"`
	RelationType string `json:"relation_type"`
	SortOrder    int    `json:"sort_order"`
}

type manifestSeries struct {
	ID           string                 `json:"id"`
	DefaultTitle string                 `json:"default_title"`
	DisplayTitle string                 `json:"display_title"`
	Description  string                 `json:"description,omitempty"`
	Titles       map[string]string      `json:"titles"`
	Members      []manifestSeriesMember `json:"members"`
}

type exportManifest struct {
	FormatVersion   int                         `json:"format_version"`
	Frontend        string                      `json:"frontend"`
	Locale          string                      `json:"locale"`
	CustomPlatforms []catalog.NewCustomPlatform `json:"custom_platforms"`
	Series          []manifestSeries            `json:"series"`
	Entries         []manifestEntry             `json:"entries"`
}

func writeLibraryManifest(ctx context.Context, store *catalog.Store, outRoot, frontend, locale string, entries []manifestEntry) error {
	series, err := store.ListSeries(ctx, locale)
	if err != nil {
		return err
	}
	outSeries := make([]manifestSeries, 0, len(series))
	for _, item := range series {
		members := make([]manifestSeriesMember, 0, len(item.Members))
		for _, member := range item.Members {
			members = append(members, manifestSeriesMember{GameID: member.GameID, RelationType: member.RelationType, SortOrder: member.SortOrder})
		}
		outSeries = append(outSeries, manifestSeries{ID: item.ID, DefaultTitle: item.DefaultTitle, DisplayTitle: item.DisplayTitle, Description: item.Description, Titles: item.Titles, Members: members})
	}
	referencedPlatforms := map[string]bool{}
	for _, entry := range entries {
		referencedPlatforms[entry.Platform] = true
	}
	custom, err := store.ListCustomPlatforms(ctx, true)
	if err != nil {
		return err
	}
	portablePlatforms := make([]catalog.NewCustomPlatform, 0, len(custom))
	for _, item := range custom {
		if referencedPlatforms[item.ID] {
			portablePlatforms = append(portablePlatforms, item.PortableDefinition())
		}
	}
	manifest := exportManifest{FormatVersion: 6, Frontend: frontend, Locale: locale, CustomPlatforms: portablePlatforms, Series: outSeries, Entries: entries}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err = os.MkdirAll(outRoot, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outRoot, "library-manifest.json"), data, 0o644)
}

func makeManifestEntry(game catalog.Game, edition catalog.Edition, artifacts []string, libraryRoot, managedMediaRoot, outRoot string, portable bool) manifestEntry {
	entry := manifestEntry{GameID: game.ID, EditionID: edition.ID, Platform: game.Platform, GameDefaultTitle: game.DefaultTitle, GameTitles: game.Titles, EditionDefaultTitle: edition.DefaultTitle, EditionTitles: edition.Titles, EditionType: edition.EditionType, Version: edition.Version, Languages: edition.Languages, Author: edition.Author, Serial: edition.Serial, ProductCode: edition.ProductCode, TitleID: edition.TitleID, Artifacts: artifacts, ArtifactRecords: []manifestArtifact{}, Media: []manifestMedia{}}
	for index, path := range artifacts {
		record := manifestArtifact{Path: path, Role: "rom"}
		var stored catalog.Artifact
		if index < len(edition.Artifacts) {
			stored = edition.Artifacts[index]
			record.Role = stored.Role
			if record.Role == "" {
				record.Role = "rom"
			}
			record.DiscIndex = stored.DiscIndex
			record.OriginalName = stored.OriginalName
		}
		if record.OriginalName == "" {
			record.OriginalName = filepath.Base(filepath.FromSlash(path))
		}
		if digest, size, ok := currentArtifactIntegrity(outRoot, path); ok {
			record.Size, record.SHA256 = size, digest
		} else if validManifestSHA256(stored.SHA256) {
			record.Size, record.SHA256 = stored.Size, stored.SHA256
		}
		entry.ArtifactRecords = append(entry.ArtifactRecords, record)
	}
	for _, asset := range append(append([]catalog.MediaAsset{}, game.Media...), edition.Media...) {
		root := libraryRoot
		if asset.StorageKind == "managed" {
			root = managedMediaRoot
		}
		path := asset.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		exported := filepath.ToSlash(abs)
		if portable {
			if rel, relErr := filepath.Rel(outRoot, abs); relErr == nil {
				exported = filepath.ToSlash(rel)
			}
		}
		ownerType := "edition"
		if asset.GameID != "" {
			ownerType = "game"
		}
		record := manifestMedia{OwnerType: ownerType, Kind: asset.Kind, Path: exported, OriginalName: asset.OriginalName, MIMEType: asset.MIMEType, Size: asset.Size, Locale: asset.Locale, SortOrder: asset.SortOrder}
		if digest, size, ok := currentArtifactIntegrity(outRoot, exported); ok {
			record.Size, record.SHA256 = size, digest
		} else if validManifestSHA256(asset.SHA256) {
			record.SHA256 = asset.SHA256
		}
		entry.Media = append(entry.Media, record)
	}
	return entry
}

func validManifestSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func currentArtifactIntegrity(outRoot, path string) (string, int64, bool) {
	resolved := filepath.FromSlash(path)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(outRoot, resolved)
	}
	info, err := os.Lstat(resolved)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, false
	}
	var digest string
	var size int64
	if info.IsDir() {
		digest, size, err = filehash.Directory(resolved)
	} else {
		digest, size, err = filehash.File(resolved)
	}
	return digest, size, err == nil && validManifestSHA256(digest)
}
