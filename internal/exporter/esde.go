package exporter

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"

	"varkiv/internal/catalog"
)

type outList struct {
	XMLName xml.Name  `xml:"gameList"`
	Games   []outGame `xml:"game"`
}
type outGame struct {
	Path    string `xml:"path"`
	Name    string `xml:"name"`
	Image   string `xml:"image,omitempty"`
	Marquee string `xml:"marquee,omitempty"`
	Video   string `xml:"video,omitempty"`
}

func ExportESDE(ctx context.Context, store *catalog.Store, libraryRoot, outRoot, locale string) (int, error) {
	return exportESDE(ctx, store, libraryRoot, libraryRoot, libraryRoot, outRoot, locale, false)
}

func ExportESDEWithStorage(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, locale string) (int, error) {
	return exportESDE(ctx, store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, locale, false)
}

func ExportESDEPortable(ctx context.Context, store *catalog.Store, libraryRoot, outRoot, locale string) (int, error) {
	return exportESDE(ctx, store, libraryRoot, libraryRoot, libraryRoot, outRoot, locale, true)
}

func exportESDE(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, locale string, portable bool) (int, error) {
	registry, err := store.PlatformRegistry(ctx)
	if err != nil {
		return 0, err
	}
	games, err := store.ListGames(ctx, locale)
	if err != nil {
		return 0, err
	}
	byPlatform := map[string][]outGame{}
	count := 0
	manifestEntries := []manifestEntry{}
	for _, w := range games {
		for _, e := range w.Editions {
			if len(e.Artifacts) == 0 {
				continue
			}
			paths := make([]string, 0, len(e.Artifacts))
			launchPath := ""
			launchArtifact := catalog.SelectLaunchArtifact(e.Artifacts)
			for _, a := range e.Artifacts {
				path := a.Path
				if !filepath.IsAbs(path) {
					root := libraryRoot
					if a.StorageKind == "managed" {
						root = managedROMRoot
					}
					path = filepath.Join(root, filepath.FromSlash(path))
				}
				abs, _ := filepath.Abs(path)
				exported := filepath.ToSlash(abs)
				manifestPath := exported
				if portable {
					if rel, relErr := filepath.Rel(outRoot, abs); relErr == nil {
						manifestPath = filepath.ToSlash(rel)
					}
				}
				paths = append(paths, manifestPath)
				if launchArtifact != nil && a.Path == launchArtifact.Path && a.Role == launchArtifact.Role && a.DiscIndex == launchArtifact.DiscIndex {
					launchPath = exported
				}
			}
			game := outGame{Path: launchPath, Name: editionName(w, e)}
			for _, asset := range append(append([]catalog.MediaAsset{}, w.Media...), e.Media...) {
				root := libraryRoot
				if asset.StorageKind == "managed" {
					root = managedMediaRoot
				}
				abs, _ := filepath.Abs(filepath.Join(root, filepath.FromSlash(asset.Path)))
				exported := filepath.ToSlash(abs)
				switch asset.Kind {
				case "cover", "box_front":
					if game.Image == "" {
						game.Image = exported
					}
				case "marquee", "logo":
					if game.Marquee == "" {
						game.Marquee = exported
					}
				case "video":
					if game.Video == "" {
						game.Video = exported
					}
				}
			}
			if launchPath != "" {
				byPlatform[w.Platform] = append(byPlatform[w.Platform], game)
			}
			manifestEntries = append(manifestEntries, makeManifestEntry(w, e, paths, libraryRoot, managedMediaRoot, outRoot, portable))
			count++
		}
	}
	platforms := make([]string, 0, len(byPlatform))
	for p := range byPlatform {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	for _, p := range platforms {
		frontendSystem := p
		if preset, ok := registry.Resolve(p); ok && len(preset.ESDESystems) > 0 {
			frontendSystem = preset.ESDESystems[0]
		}
		dir := filepath.Join(outRoot, "gamelists", frontendSystem)
		games := byPlatform[p]
		if portable {
			for i := range games {
				if rel, relErr := filepath.Rel(dir, filepath.FromSlash(games[i].Path)); relErr == nil {
					games[i].Path = filepath.ToSlash(rel)
				}
				for source, target := range map[*string]string{&games[i].Image: games[i].Image, &games[i].Marquee: games[i].Marquee, &games[i].Video: games[i].Video} {
					if target == "" {
						continue
					}
					if rel, relErr := filepath.Rel(dir, filepath.FromSlash(target)); relErr == nil {
						*source = filepath.ToSlash(rel)
					}
				}
			}
		}
		data, err := xml.MarshalIndent(outList{Games: games}, "", "  ")
		if err != nil {
			return count, err
		}
		data = append([]byte(xml.Header), data...)
		data = append(data, '\n')
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return count, err
		}
		if err = os.WriteFile(filepath.Join(dir, "gamelist.xml"), data, 0o644); err != nil {
			return count, err
		}
	}
	if err = writeLibraryManifest(ctx, store, outRoot, "es-de", locale, manifestEntries); err != nil {
		return count, err
	}
	return count, nil
}
