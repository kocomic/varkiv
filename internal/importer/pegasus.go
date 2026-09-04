package importer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"varkiv/internal/catalog"
)

type ImportResult struct {
	Parsed   int `json:"parsed"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

type pegEntry struct {
	values          map[string][]string
	inheritedLaunch string
}

type pegasusAssetKind struct {
	keys       []string
	kind       string
	fileNames  []string
	extensions map[string]bool
}

var (
	pegasusImageExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}
	pegasusVideoExtensions = map[string]bool{".avi": true, ".mp4": true, ".webm": true}
	pegasusAudioExtensions = map[string]bool{".mp3": true, ".ogg": true, ".wav": true}
	pegasusAssetKinds      = []pegasusAssetKind{
		{keys: []string{"assets.box_front", "assets.boxfront"}, kind: "cover", fileNames: []string{"boxfront", "box_front", "boxart2d"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.box_back", "assets.boxback"}, kind: "box_back", fileNames: []string{"boxback", "box_back"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.box_spine", "assets.boxspine", "assets.box_side", "assets.boxside"}, kind: "box_spine", fileNames: []string{"boxspine", "box_spine", "boxside", "box_side"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.cartridge"}, kind: "cartridge", fileNames: []string{"cartridge", "disc", "cart"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.logo"}, kind: "logo", fileNames: []string{"logo", "wheel"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.poster"}, kind: "poster", fileNames: []string{"poster", "flyer"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.marquee"}, kind: "marquee", fileNames: []string{"marquee"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.bezel"}, kind: "bezel", fileNames: []string{"bezel", "screenmarquee", "border"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.tile"}, kind: "tile", fileNames: []string{"tile"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.banner"}, kind: "banner", fileNames: []string{"banner"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.background"}, kind: "background", fileNames: []string{"background"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.screenshot"}, kind: "screenshot", fileNames: []string{"screenshot"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.titlescreen", "assets.title_screen"}, kind: "title_screen", fileNames: []string{"titlescreen"}, extensions: pegasusImageExtensions},
		{keys: []string{"assets.video"}, kind: "video", fileNames: []string{"video"}, extensions: pegasusVideoExtensions},
		{keys: []string{"assets.music"}, kind: "music", fileNames: []string{"music"}, extensions: pegasusAudioExtensions},
	}
)

func ImportPegasus(ctx context.Context, store *catalog.Store, libraryRoot, metadataPath, platform, locale string) (ImportResult, error) {
	return ImportPegasusWithContentRoot(ctx, store, libraryRoot, metadataPath, "", platform, locale)
}

func ImportPegasusWithContentRoot(ctx context.Context, store *catalog.Store, libraryRoot, metadataPath, contentRoot, platform, locale string) (ImportResult, error) {
	games, err := PreviewPegasusWithContentRoot(libraryRoot, metadataPath, contentRoot, platform, locale)
	if err != nil {
		return ImportResult{}, err
	}
	return Commit(ctx, store, games)
}

func PreviewPegasus(libraryRoot, metadataPath, platform, locale string) ([]catalog.ImportedGame, error) {
	return PreviewPegasusWithContentRoot(libraryRoot, metadataPath, "", platform, locale)
}

func PreviewPegasusWithContentRoot(libraryRoot, metadataPath, contentRoot, platform, locale string) ([]catalog.ImportedGame, error) {
	entries, err := parsePegasus(metadataPath)
	if err != nil {
		return nil, err
	}
	games := make([]catalog.ImportedGame, 0, len(entries))
	dir := filepath.Dir(metadataPath)
	artifactDir, err := libraryContentRoot(libraryRoot, dir, contentRoot)
	if err != nil {
		return nil, err
	}
	// External CLI metadata remains supported, but its host path is never
	// persisted as a source reference or used for ancestor sidecar discovery.
	sourceRef, _ := libraryPath(libraryRoot, dir, filepath.Base(metadataPath))
	for _, entry := range entries {
		title := first(entry, "game")
		if title == "" {
			continue
		}
		paths := append([]string{}, entry.values["file"]...)
		paths = append(paths, entry.values["files"]...)
		artifacts := make([]catalog.NewArtifact, 0, len(paths))
		for i, p := range paths {
			artifact, err := libraryArtifact(libraryRoot, artifactDir, p)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", title, err)
			}
			artifact.DiscIndex = i + 1
			artifacts = append(artifacts, artifact)
		}
		if len(artifacts) == 0 {
			continue
		}
		titles := map[string]string{}
		editionTitles := map[string]string{}
		if locale != "" {
			titles[locale] = title
		}
		for key, values := range entry.values {
			if strings.HasPrefix(key, "x-varkiv-game-title-") && len(values) > 0 {
				titles[strings.TrimPrefix(key, "x-varkiv-game-title-")] = values[0]
			}
			if strings.HasPrefix(key, "x-varkiv-edition-title-") && len(values) > 0 {
				editionTitles[strings.TrimPrefix(key, "x-varkiv-edition-title-")] = values[0]
			}
		}
		langs := splitCSV(first(entry, "x-varkiv-languages"))
		editionType := first(entry, "x-varkiv-edition-type")
		if editionType == "" {
			editionType = "original"
		}
		gameID := first(entry, "x-varkiv-game-id")
		editionID := first(entry, "x-varkiv-edition-id")
		gameTitle := first(entry, "x-varkiv-game-title")
		if gameTitle == "" {
			gameTitle = title
		}
		editionTitle := first(entry, "x-varkiv-edition-title")
		if editionTitle == "" {
			editionTitle = title
		}
		media := []catalog.NewMediaAsset{}
		explicitMediaKinds := map[string]bool{}
		for _, assetKind := range pegasusAssetKinds {
			for _, key := range assetKind.keys {
				for _, value := range entry.values[key] {
					item, exists, mediaErr := libraryMedia(libraryRoot, dir, value, assetKind.kind)
					if mediaErr != nil {
						return nil, fmt.Errorf("%s media: %w", title, mediaErr)
					}
					if exists {
						item.SortOrder = len(media)
						media = append(media, item)
						explicitMediaKinds[assetKind.kind] = true
					}
				}
			}
		}
		discovered, mediaErr := discoverPegasusMedia(libraryRoot, dir, title, paths, explicitMediaKinds)
		if mediaErr != nil {
			return nil, fmt.Errorf("%s media: %w", title, mediaErr)
		}
		for _, item := range discovered {
			item.SortOrder = len(media)
			media = append(media, item)
		}
		runtimeHints := []catalog.NewRuntimeImportHint{}
		rawLaunch := first(entry, "launch")
		if rawLaunch == "" {
			rawLaunch = first(entry, "command")
		}
		if rawLaunch == "" {
			rawLaunch = entry.inheritedLaunch
		}
		if rawLaunch != "" && len(rawLaunch) <= 8192 && !strings.ContainsRune(rawLaunch, '\x00') {
			runtimeHints = append(runtimeHints, catalog.NewRuntimeImportHint{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: rawLaunch, SourceRef: sourceRef})
		}
		games = append(games, catalog.ImportedGame{GameID: gameID, EditionID: editionID, Platform: platform, DefaultTitle: gameTitle, Titles: titles, EditionTitle: editionTitle, EditionTitles: editionTitles, EditionType: editionType, Version: first(entry, "x-varkiv-version"), Languages: langs, Author: first(entry, "x-varkiv-author"), Artifacts: artifacts, Media: media, RuntimeHints: runtimeHints})
	}
	games, err = attachLibraryManifest(libraryRoot, metadataPath, "pegasus", games)
	if err != nil {
		return nil, err
	}
	return attachStructuredLaunchHints(libraryRoot, metadataPath, games)
}

// discoverPegasusMedia implements Pegasus' documented media/<game-name>/
// fallback. Explicit assets win per media kind. Discovery is bounded to exact,
// recognized file names and formats and reuses libraryMedia's root and symlink
// checks before hashing any bytes.
func discoverPegasusMedia(libraryRoot, metadataDir, title string, artifactPaths []string, explicitKinds map[string]bool) ([]catalog.NewMediaAsset, error) {
	// Metadata-only exports may live outside the configured library root while
	// their ROM paths still point into it. There is no safe library-relative
	// media directory to discover in that case.
	if _, err := libraryPath(libraryRoot, metadataDir, "."); err != nil {
		return nil, nil
	}
	directoryNames := []string{}
	seenNames := map[string]bool{}
	addDirectoryName := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") || seenNames[value] {
			return
		}
		seenNames[value] = true
		directoryNames = append(directoryNames, value)
	}
	addDirectoryName(title)
	for _, artifactPath := range artifactPaths {
		name := filepath.Base(filepath.FromSlash(strings.TrimSpace(artifactPath)))
		addDirectoryName(strings.TrimSuffix(name, filepath.Ext(name)))
	}

	byFileName := map[string]pegasusAssetKind{}
	for _, kind := range pegasusAssetKinds {
		if explicitKinds[kind.kind] {
			continue
		}
		for _, name := range kind.fileNames {
			byFileName[name] = kind
		}
	}
	media := []catalog.NewMediaAsset{}
	seenPaths := map[string]bool{}
	for _, directoryName := range directoryNames {
		relativeDirectory, err := libraryPath(libraryRoot, metadataDir, filepath.Join("media", directoryName))
		if err != nil {
			return nil, err
		}
		absoluteDirectory, info, exists, err := exactLibraryEntry(libraryRoot, relativeDirectory)
		if err != nil {
			return nil, err
		}
		if !exists || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(absoluteDirectory)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if len(media) >= 256 || entry.IsDir() {
				continue
			}
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			kind, recognized := byFileName[strings.ToLower(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))]
			if !recognized || !kind.extensions[extension] {
				continue
			}
			value := filepath.Join("media", directoryName, entry.Name())
			item, exists, err := libraryMedia(libraryRoot, metadataDir, value, kind.kind)
			if err != nil {
				return nil, err
			}
			if !exists || seenPaths[item.Path] {
				continue
			}
			seenPaths[item.Path] = true
			media = append(media, item)
		}
	}
	return media, nil
}

func Commit(ctx context.Context, store *catalog.Store, games []catalog.ImportedGame) (ImportResult, error) {
	result := ImportResult{Parsed: len(games)}
	batch := make([]catalog.ImportedGame, 0, len(games))
	for _, game := range games {
		available := len(game.Artifacts) > 0
		for _, artifact := range game.Artifacts {
			if artifact.Missing || strings.TrimSpace(artifact.SHA256) == "" {
				available = false
				break
			}
		}
		if !available {
			result.Skipped++
			continue
		}
		batch = append(batch, game)
	}
	if len(batch) == 0 {
		return result, nil
	}
	definitions, err := CustomPlatformDefinitions(batch)
	if err != nil {
		return result, err
	}
	if err := store.ImportGamesAndCustomPlatformsAtomic(ctx, batch, definitions); err != nil {
		return result, fmt.Errorf("atomic import batch: %w", err)
	}
	result.Imported = len(batch)
	return result, nil
}

// CustomPlatformDefinitions extracts and de-duplicates the portable platform
// definitions attached to selected candidates. JSON equality is intentional:
// two candidates may share a definition, but cannot disagree about it inside
// one atomic batch.
func CustomPlatformDefinitions(games []catalog.ImportedGame) ([]catalog.NewCustomPlatform, error) {
	definitions := []catalog.NewCustomPlatform{}
	byID := map[string][]byte{}
	for _, game := range games {
		if game.PlatformDefinition == nil {
			continue
		}
		definition := *game.PlatformDefinition
		normalized, err := catalog.NormalizeCustomPlatform(definition)
		if err != nil {
			return nil, err
		}
		definition = normalized.PortableDefinition()
		data, err := json.Marshal(definition)
		if err != nil {
			return nil, err
		}
		if existing, ok := byID[normalized.ID]; ok {
			if string(existing) != string(data) {
				return nil, fmt.Errorf("portable custom platform %q has conflicting definitions", normalized.ID)
			}
			continue
		}
		byID[normalized.ID] = data
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func parsePegasus(path string) ([]pegEntry, error) {
	data, err := readExactRegularFile(path, "Pegasus metadata", maxFrontendMetadataSize)
	if err != nil {
		return nil, err
	}
	var entries []pegEntry
	var current *pegEntry
	collectionValues := map[string][]string{}
	var activeValues map[string][]string
	var lastKey string
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 64<<10), 1<<20)
	lineNo := 0
	for s.Scan() {
		lineNo++
		raw := s.Text()
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if (strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")) && activeValues != nil && lastKey != "" {
			vals := activeValues[lastKey]
			if listKey(lastKey) {
				if len(vals) == 1 && vals[0] == "" {
					vals = nil
				}
				activeValues[lastKey] = append(vals, trim)
			} else if len(vals) > 0 {
				vals[len(vals)-1] += "\n" + trim
				activeValues[lastKey] = vals
			}
			continue
		}
		idx := strings.Index(raw, ":")
		if idx < 1 {
			return nil, fmt.Errorf("Pegasus metadata line %d is invalid", lineNo)
		}
		key := strings.ToLower(strings.TrimSpace(raw[:idx]))
		value := strings.TrimSpace(raw[idx+1:])
		if key == "collection" {
			collectionValues = map[string][]string{"collection": {value}}
			current = nil
			activeValues = collectionValues
			lastKey = key
			continue
		}
		if key == "game" {
			inherited := ""
			if values := collectionValues["launch"]; len(values) > 0 {
				inherited = strings.TrimSpace(values[0])
			} else if values := collectionValues["command"]; len(values) > 0 {
				inherited = strings.TrimSpace(values[0])
			}
			entries = append(entries, pegEntry{values: map[string][]string{}, inheritedLaunch: inherited})
			current = &entries[len(entries)-1]
			activeValues = current.values
		}
		if activeValues == nil {
			continue
		}
		activeValues[key] = append(activeValues[key], value)
		lastKey = key
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func first(e pegEntry, key string) string {
	v := e.values[key]
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}
func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' })
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	sort.Strings(parts)
	return parts
}

func listKey(key string) bool {
	if strings.HasPrefix(key, "assets.") {
		return true
	}
	switch key {
	case "file", "files", "developer", "developers", "publisher", "publishers":
		return true
	}
	return false
}
