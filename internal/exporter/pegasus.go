package exporter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"varkiv/internal/catalog"
)

func ExportPegasus(ctx context.Context, store *catalog.Store, libraryRoot, outRoot, locale string) (int, error) {
	return exportPegasus(ctx, store, libraryRoot, libraryRoot, libraryRoot, outRoot, locale, false)
}

func ExportPegasusWithStorage(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, locale string) (int, error) {
	return exportPegasus(ctx, store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, locale, false)
}

func ExportPegasusPortable(ctx context.Context, store *catalog.Store, libraryRoot, outRoot, locale string) (int, error) {
	return exportPegasus(ctx, store, libraryRoot, libraryRoot, libraryRoot, outRoot, locale, true)
}

func exportPegasus(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, locale string, portable bool) (int, error) {
	games, err := store.ListGames(ctx, locale)
	if err != nil {
		return 0, err
	}
	byPlatform := map[string][]catalog.Game{}
	for _, w := range games {
		byPlatform[w.Platform] = append(byPlatform[w.Platform], w)
	}
	platforms := make([]string, 0, len(byPlatform))
	for p := range byPlatform {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	count := 0
	manifestEntries := []manifestEntry{}
	for _, platform := range platforms {
		dir := filepath.Join(outRoot, platform)
		var b strings.Builder
		fmt.Fprintf(&b, "collection: %s\nshortname: %s\n\n", platform, platform)
		for _, w := range byPlatform[platform] {
			for _, e := range w.Editions {
				manifestPaths := make([]string, 0, len(e.Artifacts))
				fmt.Fprintf(&b, "game: %s\n", editionName(w, e))
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
						if rel, relErr := filepath.Rel(dir, abs); relErr == nil {
							exported = filepath.ToSlash(rel)
						}
					}
					manifestPaths = append(manifestPaths, manifestPath)
					if catalog.IsLaunchArtifactRole(a.Role) && !a.Missing {
						fmt.Fprintf(&b, "file: %s\n", exported)
					}
				}
				assetKeys := map[string]string{"cover": "box_front", "box_front": "box_front", "box_back": "box_back", "box_spine": "box_spine", "logo": "logo", "screenshot": "screenshot", "title_screen": "titlescreen", "background": "background", "marquee": "marquee", "video": "video", "poster": "poster", "banner": "banner", "cartridge": "cartridge"}
				media := append(append([]catalog.MediaAsset{}, w.Media...), e.Media...)
				for _, asset := range media {
					key, ok := assetKeys[asset.Kind]
					if !ok {
						continue
					}
					root := libraryRoot
					if asset.StorageKind == "managed" {
						root = managedMediaRoot
					}
					abs, _ := filepath.Abs(filepath.Join(root, filepath.FromSlash(asset.Path)))
					exported := filepath.ToSlash(abs)
					if portable {
						if rel, relErr := filepath.Rel(dir, abs); relErr == nil {
							exported = filepath.ToSlash(rel)
						}
					}
					fmt.Fprintf(&b, "assets.%s: %s\n", key, exported)
				}
				fmt.Fprintf(&b, "x-varkiv-game-id: %s\nx-varkiv-edition-id: %s\nx-varkiv-game-title: %s\nx-varkiv-edition-title: %s\nx-varkiv-edition-type: %s\n", w.ID, e.ID, w.DefaultTitle, e.DefaultTitle, e.EditionType)
				if e.Version != "" {
					fmt.Fprintf(&b, "x-varkiv-version: %s\n", e.Version)
				}
				if e.Author != "" {
					fmt.Fprintf(&b, "x-varkiv-author: %s\n", e.Author)
				}
				if len(e.Languages) > 0 {
					fmt.Fprintf(&b, "x-varkiv-languages: %s\n", strings.Join(e.Languages, ", "))
				}
				for l, t := range w.Titles {
					fmt.Fprintf(&b, "x-varkiv-game-title-%s: %s\n", l, t)
				}
				for l, t := range e.Titles {
					fmt.Fprintf(&b, "x-varkiv-edition-title-%s: %s\n", l, t)
				}
				b.WriteString("\n")
				manifestEntries = append(manifestEntries, makeManifestEntry(w, e, manifestPaths, libraryRoot, managedMediaRoot, outRoot, portable))
				count++
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return count, err
		}
		if err := os.WriteFile(filepath.Join(dir, "metadata.pegasus.txt"), []byte(b.String()), 0o644); err != nil {
			return count, err
		}
	}
	if err := writeLibraryManifest(ctx, store, outRoot, "pegasus", locale, manifestEntries); err != nil {
		return count, err
	}
	return count, nil
}
