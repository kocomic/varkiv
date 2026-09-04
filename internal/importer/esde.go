package importer

import (
	"context"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
)

type esdeList struct {
	Games []esdeGame `xml:"game"`
}
type esdeGame struct {
	Path    string `xml:"path"`
	Name    string `xml:"name"`
	Desc    string `xml:"desc"`
	Image   string `xml:"image"`
	Marquee string `xml:"marquee"`
	Video   string `xml:"video"`
}

func ImportESDE(ctx context.Context, store *catalog.Store, libraryRoot, gamelistPath, platform, locale string) (ImportResult, error) {
	games, err := PreviewESDE(libraryRoot, gamelistPath, platform, locale)
	if err != nil {
		return ImportResult{}, err
	}
	return Commit(ctx, store, games)
}

func PreviewESDE(libraryRoot, gamelistPath, platform, locale string) ([]catalog.ImportedGame, error) {
	return PreviewESDEWithRuntime(libraryRoot, gamelistPath, "", platform, locale)
}

// PreviewESDEWithRuntime imports an optional, explicitly selected
// es_systems.xml. Its command strings are attached as untrusted review hints;
// they are never tokenized, executed, or converted into launch arguments.
func PreviewESDEWithRuntime(libraryRoot, gamelistPath, runtimePath, platform, locale string) ([]catalog.ImportedGame, error) {
	registry, err := platforms.NewRegistry(platforms.All())
	if err != nil {
		return nil, err
	}
	return PreviewESDEWithRuntimeRegistry(libraryRoot, gamelistPath, runtimePath, platform, locale, registry)
}

func PreviewESDEWithRuntimeRegistry(libraryRoot, gamelistPath, runtimePath, platform, locale string, registry platforms.Registry) ([]catalog.ImportedGame, error) {
	return PreviewESDEWithContentRootAndRuntimeRegistry(libraryRoot, gamelistPath, "", runtimePath, platform, locale, registry)
}

func PreviewESDEWithContentRootAndRuntimeRegistry(libraryRoot, gamelistPath, contentRoot, runtimePath, platform, locale string, registry platforms.Registry) ([]catalog.ImportedGame, error) {
	data, err := readExactRegularFile(gamelistPath, "ES-DE gamelist", maxFrontendMetadataSize)
	if err != nil {
		return nil, err
	}
	var list esdeList
	if err = xml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse gamelist: %w", err)
	}
	games := make([]catalog.ImportedGame, 0, len(list.Games))
	dir := filepath.Dir(gamelistPath)
	artifactDir, err := libraryContentRoot(libraryRoot, dir, contentRoot)
	if err != nil {
		return nil, err
	}
	for _, g := range list.Games {
		title := strings.TrimSpace(g.Name)
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(g.Path), filepath.Ext(g.Path))
		}
		artifact, err := libraryArtifact(libraryRoot, artifactDir, g.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", title, err)
		}
		titles := map[string]string{}
		if locale != "" {
			titles[locale] = title
		}
		media := []catalog.NewMediaAsset{}
		for _, candidate := range []struct{ value, kind string }{{g.Image, "cover"}, {g.Marquee, "marquee"}, {g.Video, "video"}} {
			if strings.TrimSpace(candidate.value) == "" {
				continue
			}
			item, exists, mediaErr := libraryMedia(libraryRoot, dir, candidate.value, candidate.kind)
			if mediaErr != nil {
				return nil, fmt.Errorf("%s media: %w", title, mediaErr)
			}
			if exists {
				media = append(media, item)
			}
		}
		games = append(games, catalog.ImportedGame{Platform: platform, DefaultTitle: title, Titles: titles, EditionTitle: title, EditionType: "original", Artifacts: []catalog.NewArtifact{artifact}, Media: media})
	}
	games, err = attachLibraryManifest(libraryRoot, gamelistPath, "es-de", games)
	if err != nil {
		return nil, err
	}
	games, err = attachStructuredLaunchHints(libraryRoot, gamelistPath, games)
	if err != nil {
		return nil, err
	}
	return attachESDESystemHints(libraryRoot, runtimePath, platform, games, registry)
}
