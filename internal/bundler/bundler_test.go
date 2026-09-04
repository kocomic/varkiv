package bundler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
	"varkiv/internal/importer"
)

func TestReferencePackagesUseTargetRelativePathsAndNeverClaimContentOwnership(t *testing.T) {
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(library, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(library, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	romBytes, coverBytes := []byte("reference-rom"), []byte("reference-cover")
	if err := os.WriteFile(filepath.Join(library, "gba", "game.gba"), romBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "media", "cover.png"), coverBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	romSum, coverSum := sha256.Sum256(romBytes), sha256.Sum256(coverBytes)
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Reference Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", Size: int64(len(romBytes)), SHA256: hex.EncodeToString(romSum[:])}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMedia(ctx, catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "library", Path: "media/cover.png", Size: int64(len(coverBytes)), SHA256: hex.EncodeToString(coverSum[:]), ContentStatus: "available"}); err != nil {
		t.Fatal(err)
	}

	for _, frontend := range []string{"pegasus", "es-de"} {
		t.Run(frontend, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "reference-package")
			profile := Profile{Name: frontend + " reference", Frontend: frontend, Target: "portable", FileMode: "reference", Locale: "en", Templates: []ConfigTemplate{{Name: "Reference path", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "rom={{rom.path}}\n"}}}
			result, buildErr := Build(ctx, store, library, out, profile)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if result.Copied != 0 || result.Linked != 0 || result.Exported != 1 || len(result.Warnings) != 2 || !strings.Contains(strings.Join(result.Warnings, "\n"), "relative to the target package root") || !strings.Contains(strings.Join(result.Warnings, "\n"), "no device profile selected") {
				t.Fatalf("reference build result = %#v", result)
			}
			if _, statErr := os.Stat(filepath.Join(out, "gba", "game.gba")); !os.IsNotExist(statErr) {
				t.Fatalf("reference build copied ROM content: %v", statErr)
			}
			metadataPath := filepath.Join(out, "gba", "metadata.pegasus.txt")
			wantROMPath := "file: game.gba"
			if frontend == "es-de" {
				metadataPath = filepath.Join(out, "gamelists", "gba", "gamelist.xml")
				wantROMPath = "<path>../../gba/game.gba</path>"
			}
			metadata, readErr := os.ReadFile(metadataPath)
			if readErr != nil || strings.Contains(string(metadata), library) || !strings.Contains(string(metadata), wantROMPath) {
				t.Fatalf("reference metadata is not target-relative: %q, %v", metadata, readErr)
			}
			manifest, readErr := os.ReadFile(filepath.Join(out, "library-manifest.json"))
			if readErr != nil || strings.Contains(string(manifest), library) || !strings.Contains(string(manifest), `"path": "gba/game.gba"`) || !strings.Contains(string(manifest), hex.EncodeToString(romSum[:])) || !strings.Contains(string(manifest), hex.EncodeToString(coverSum[:])) {
				t.Fatalf("reference recovery manifest is not portable or lost integrity: %s, %v", manifest, readErr)
			}
			config, readErr := os.ReadFile(filepath.Join(out, "config", edition.ID+".cfg"))
			if readErr != nil || strings.Contains(string(config), library) || string(config) != "rom=gba/game.gba\n" {
				t.Fatalf("reference configuration is not target-relative: %q, %v", config, readErr)
			}

			// Reference evidence does not make a later user-supplied target file
			// owned by Varkiv. A copy build must refuse to overwrite it.
			if err = os.MkdirAll(filepath.Join(out, "gba"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(out, "gba", "game.gba"), romBytes, 0o644); err != nil {
				t.Fatal(err)
			}
			profile.FileMode = "copy"
			if _, buildErr = Build(ctx, store, library, out, profile); !errors.Is(buildErr, ErrUnmanagedTargetConflict) {
				t.Fatalf("copy build claimed a reference-only target: %v", buildErr)
			}
		})
	}
}

func TestCustomFrontendFormatUsesOnlyItsAuditedExportHandler(t *testing.T) {
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(library, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	rom := []byte("custom-frontend-rom")
	if err := os.WriteFile(filepath.Join(library, "gba", "game.gba"), rom, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rom)
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Custom Frontend Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", Size: int64(len(rom)), SHA256: hex.EncodeToString(sum[:])}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	custom, err := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "manga-pegasus", Name: "Manga Pegasus", Format: "manga-library", Handler: "pegasus", Capabilities: map[string]bool{"export": true}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "custom-package")
	result, err := Build(ctx, store, library, out, Profile{Name: "Custom frontend", Frontend: "pegasus", FrontendAdapterID: custom.ID, Target: "portable", FileMode: "copy", Locale: "en"})
	if err != nil || result.Exported != 1 {
		t.Fatalf("custom frontend handler build = %#v, %v", result, err)
	}
	metadata, err := os.ReadFile(filepath.Join(out, "gba", "metadata.pegasus.txt"))
	if err != nil || !strings.Contains(string(metadata), "Custom Frontend Game") {
		t.Fatalf("custom format did not use audited Pegasus renderer: %q, %v", metadata, err)
	}

	mismatched, err := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "manga-esde", Name: "Manga ES-DE", Format: "manga-xml", Handler: "es-de", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	mismatchOut := filepath.Join(t.TempDir(), "mismatch")
	if _, err = Build(ctx, store, library, mismatchOut, Profile{Name: "Mismatch", Frontend: "pegasus", FrontendAdapterID: mismatched.ID, Target: "portable", FileMode: "copy"}); err == nil || !strings.Contains(err.Error(), "handler does not match") {
		t.Fatalf("mismatched frontend handler was accepted: %v", err)
	}
	if _, statErr := os.Stat(mismatchOut); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched handler created output before rejection: %v", statErr)
	}
	unbound, err := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "legacy-frontend", Name: "Legacy metadata", Format: "legacy-only", Enabled: &enabled})
	if err != nil || unbound.Handler != "" {
		t.Fatalf("legacy unbound frontend = %#v, %v", unbound, err)
	}
	if _, err = Build(ctx, store, library, filepath.Join(t.TempDir(), "unbound"), Profile{Name: "Unbound", Frontend: "pegasus", FrontendAdapterID: unbound.ID, Target: "portable", FileMode: "copy"}); err == nil || !strings.Contains(err.Error(), "no audited export handler") {
		t.Fatalf("unbound legacy frontend was executable: %v", err)
	}
}

func TestPackagePlanBlocksCatalogFingerprintDriftBeforeWriting(t *testing.T) {
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "library")
	for _, rel := range []string{"gba", "media", "wiiu/Game/content"} {
		if err := os.MkdirAll(filepath.Join(library, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	romPath := filepath.Join(library, "gba", "game.gba")
	mediaPath := filepath.Join(library, "media", "cover.png")
	directoryFile := filepath.Join(library, "wiiu", "Game", "content", "game.rpx")
	for path, content := range map[string]string{romPath: "catalog-rom", mediaPath: "catalog-cover", directoryFile: "catalog-directory"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	romSHA, romSize, _ := filehash.File(romPath)
	mediaSHA, mediaSize, _ := filehash.File(mediaPath)
	directorySHA, directorySize, _ := filehash.Directory(filepath.Join(library, "wiiu", "Game"))
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Drift Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", Size: romSize, SHA256: romSHA}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMedia(ctx, catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "library", Path: "media/cover.png", Size: mediaSize, SHA256: mediaSHA, ContentStatus: "available"}); err != nil {
		t.Fatal(err)
	}
	directoryGame, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Directory Drift", Platform: "wiiu"})
	directoryEdition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: directoryGame.ID, DefaultTitle: "Original", EditionType: "original"})
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: directoryEdition.ID, Path: "wiiu/Game", Role: "rom", Size: directorySize, SHA256: directorySHA}); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{romPath: "replaced-rom", mediaPath: "replaced-cover", directoryFile: "replaced-directory"} {
		if err = os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "package")
	profile := Profile{Name: "drift guard", Frontend: "pegasus", Target: "portable", Locale: "en", FileMode: "reference"}
	plan, err := PlanWithStorage(ctx, store, library, library, library, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 3 {
		t.Fatalf("conflicts=%v items=%#v", plan.Conflicts, plan.Items)
	}
	for _, target := range []string{"gba/game.gba", "media/cover.png", "wiiu/Game"} {
		found := false
		for _, item := range plan.Items {
			if item.Target == target && item.Action == "conflict" && strings.Contains(item.Detail, "catalog fingerprint") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing drift conflict for %s: %#v", target, plan.Items)
		}
	}
	if _, err = Build(ctx, store, library, out, profile); !errors.Is(err, ErrUnmanagedTargetConflict) {
		t.Fatalf("changed catalog content was exported: %v", err)
	}
	if _, err = os.Stat(filepath.Join(out, "library-manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("blocked plan wrote package metadata: %v", err)
	}
}

func TestPortableManifestV6CarriesCustomPlatformAndCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "library")
	packageRoot := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(library, "fixture-handheld"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "fixture-handheld", "demo.opk"), []byte("portable-fixture-handheld-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	platformInput := catalog.NewCustomPlatform{
		ID: "fixture-handheld", Name: "Fixture Handheld", NameZH: "测试掌机", Vendor: "Community", Category: "handheld",
		Aliases: []string{"fixture-hh"}, Extensions: []string{".opk"}, ESDESystems: []string{"fixture-handheld-es"}, BIOS: "none", Runtime: "native",
		SuggestedEmulators: map[string][]string{"handheld_linux": {"Fixture Handheld Player"}},
	}
	if _, err = source.CreateCustomPlatform(ctx, platformInput); err != nil {
		t.Fatal(err)
	}
	game, err := source.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Fixture Handheld Demo", Platform: "fixture-handheld"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := source.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "fixture-handheld/demo.opk", Role: "rom"}); err != nil {
		t.Fatal(err)
	}
	profile := Profile{Name: "custom-platform", Frontend: "es-de", Target: "portable", Locale: "en", FileMode: "copy"}
	if _, err = Build(ctx, source, library, packageRoot, profile); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(packageRoot, "library-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FormatVersion   int                         `json:"format_version"`
		CustomPlatforms []catalog.NewCustomPlatform `json:"custom_platforms"`
	}
	if err = json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != 6 || len(manifest.CustomPlatforms) != 1 || manifest.CustomPlatforms[0].ID != "fixture-handheld" || manifest.CustomPlatforms[0].Enabled != nil {
		t.Fatalf("portable platform definition missing or contains local state: %s", manifestData)
	}

	target, err := catalog.Open(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	result, err := importer.ImportLibraryManifest(ctx, target, packageRoot, filepath.Join(packageRoot, "library-manifest.json"))
	if err != nil || result.Imported != 1 {
		t.Fatalf("portable import=%#v err=%v", result, err)
	}
	importedPlatform, err := target.GetCustomPlatform(ctx, "fixture-handheld")
	if err != nil || importedPlatform.NameZH != "测试掌机" || len(importedPlatform.ESDESystems) != 1 || importedPlatform.ESDESystems[0] != "fixture-handheld-es" {
		t.Fatalf("custom platform=%#v err=%v", importedPlatform, err)
	}
	importedGame, err := target.GetGame(ctx, game.ID, "en")
	if err != nil || importedGame.Platform != "fixture-handheld" {
		t.Fatalf("custom game=%#v err=%v", importedGame, err)
	}

	rollbackTarget, err := catalog.Open(filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTarget.Close()
	if _, _, err = rollbackTarget.ImportGame(ctx, catalog.ImportedGame{Platform: "gba", DefaultTitle: "Existing", EditionTitle: "Existing", EditionType: "original", Artifacts: []catalog.NewArtifact{{Path: "fixture-handheld/demo.opk", SHA256: "existing-content"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = importer.ImportLibraryManifest(ctx, rollbackTarget, packageRoot, filepath.Join(packageRoot, "library-manifest.json")); !errors.Is(err, catalog.ErrImportDuplicate) && (err == nil || !strings.Contains(err.Error(), catalog.ErrImportDuplicate.Error())) {
		t.Fatalf("expected duplicate rollback, got %v", err)
	}
	if _, err = rollbackTarget.GetCustomPlatform(ctx, "fixture-handheld"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("custom platform escaped failed transaction: %v", err)
	}
}

func TestReplaceHardlinkNeverDeletesUnownedLegacyTempName(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.rom")
	target := filepath.Join(root, "target.rom")
	legacyTemp := target + ".link-tmp"
	if err := os.WriteFile(source, []byte("rom-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyTemp, []byte("user-owned-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceHardlink(source, target); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(legacyTemp)
	if err != nil || string(marker) != "user-owned-marker" {
		t.Fatalf("unowned legacy temp path changed: %q %v", marker, err)
	}
	linked, err := os.ReadFile(target)
	if err != nil || string(linked) != "rom-content" {
		t.Fatalf("hardlink target=%q err=%v", linked, err)
	}
}

func TestBuildPortablePackageIsIncremental(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nds"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nds", "game.nds"), []byte("rom-nds"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	w, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.AddEdition(ctx, catalog.NewEdition{GameID: w.ID, DefaultTitle: "Game", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: e.ID, Path: "gba/game.gba", Role: "rom"}); err != nil {
		t.Fatal(err)
	}
	nds, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game DS", Platform: "nds"})
	if err != nil {
		t.Fatal(err)
	}
	ndsEdition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: nds.ID, DefaultTitle: "Game DS", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: ndsEdition.ID, Path: "nds/game.nds", Role: "rom"}); err != nil {
		t.Fatal(err)
	}
	series, err := store.CreateSeries(ctx, catalog.NewSeries{DefaultTitle: "Game Saga", Titles: map[string]string{"zh-CN": "游戏传奇"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutSeriesMember(ctx, series.ID, w.ID, catalog.NewSeriesMember{RelationType: "mainline", SortOrder: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutSeriesMember(ctx, series.ID, nds.ID, catalog.NewSeriesMember{RelationType: "port", SortOrder: 20}); err != nil {
		t.Fatal(err)
	}
	profile := Profile{Name: "test", Frontend: "es-de", Target: "rocknix", Locale: "en", FileMode: "copy"}
	first, err := Build(ctx, store, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	if first.Copied != 2 || first.Exported != 2 || first.ManifestFiles != 2 {
		t.Fatalf("unexpected first result: %#v", first)
	}
	gamelist, err := os.ReadFile(filepath.Join(out, "gamelists", "gba", "gamelist.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gamelist), root) || !strings.Contains(string(gamelist), "../../gba/game.gba") {
		t.Fatalf("gamelist is not portable:\n%s", gamelist)
	}
	manifestData, err := os.ReadFile(filepath.Join(out, "library-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var libraryManifest struct {
		FormatVersion int `json:"format_version"`
		Series        []struct {
			ID      string `json:"id"`
			Members []struct {
				GameID       string `json:"game_id"`
				RelationType string `json:"relation_type"`
			} `json:"members"`
		} `json:"series"`
	}
	if err = json.Unmarshal(manifestData, &libraryManifest); err != nil {
		t.Fatal(err)
	}
	if libraryManifest.FormatVersion != 6 || len(libraryManifest.Series) != 1 || len(libraryManifest.Series[0].Members) != 2 || libraryManifest.Series[0].Members[1].RelationType != "port" {
		t.Fatalf("series missing from neutral manifest: %s", manifestData)
	}
	second, err := Build(ctx, store, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	if second.Unchanged != 2 || second.Copied != 0 {
		t.Fatalf("expected incremental build, got %#v", second)
	}
}

func TestManagedPackageUpdateKeepsRecoverableSnapshot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	rom := filepath.Join(root, "gba", "game.gba")
	if err := os.WriteFile(rom, []byte("release-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Game", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	profile := Profile{Name: "recoverable", Frontend: "pegasus", Target: "portable", Locale: "en", FileMode: "copy"}
	if _, err = Build(ctx, store, root, out, profile); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(rom, []byte("release-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Build(ctx, store, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	if result.RecoverySnapshot == "" || !strings.HasPrefix(result.RecoverySnapshot, recoveryDirectory+"/"+filepath.Base(out)+"/") {
		t.Fatalf("missing recovery snapshot: %#v", result)
	}
	current, err := os.ReadFile(filepath.Join(out, "gba", "game.gba"))
	if err != nil || string(current) != "release-two" {
		t.Fatalf("current package ROM = %q, %v", current, err)
	}
	previous, err := os.ReadFile(filepath.Join(filepath.Dir(out), filepath.FromSlash(result.RecoverySnapshot), "files", "gba", "game.gba"))
	if err != nil || string(previous) != "release-one" {
		t.Fatalf("recovery ROM = %q, %v", previous, err)
	}
	snapshot, err := os.ReadFile(filepath.Join(filepath.Dir(out), filepath.FromSlash(result.RecoverySnapshot), "snapshot.json"))
	if err != nil || strings.Contains(string(snapshot), root) || !strings.Contains(string(snapshot), `"path": "gba/game.gba"`) {
		t.Fatalf("unsafe recovery manifest: %s, %v", snapshot, err)
	}
	if _, err = os.Lstat(filepath.Join(out, recoveryDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery data leaked into package output: %v", err)
	}
	if _, err = os.Lstat(filepath.Join(out, legacyRecoveryDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy recovery data leaked into package output: %v", err)
	}
}

func TestBuildRecoveryRestoresOldFilesAndRemovesOnlyNewTargets(t *testing.T) {
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(out, "config", "existing.cfg")
	if err := os.WriteFile(existing, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Items: []PlanItem{
		{Target: "config/existing.cfg", Action: "generate"},
		{Target: "config/new.cfg", Action: "generate"},
	}}
	recoveryBase := filepath.Join(t.TempDir(), "recovery")
	recovery, err := prepareBuildRecovery(out, recoveryBase, "state/recovery/test", plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(existing, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	newTarget := filepath.Join(out, "config", "new.cfg")
	if err = os.WriteFile(newTarget, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = recovery.restore(); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(existing)
	if err != nil || string(restored) != "before" {
		t.Fatalf("existing file not restored: %q, %v", restored, err)
	}
	if info, err := os.Stat(existing); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("existing mode not restored: %v, %v", info, err)
	}
	if _, err = os.Lstat(newTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new target was not removed: %v", err)
	}
	if _, err = os.Stat(filepath.Join(recovery.root, "snapshot.json")); err != nil {
		t.Fatalf("recovery snapshot was not retained: %v", err)
	}
}

func TestBuildRecoveryRootMustBeIndependentAndReal(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "config.cfg"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Items: []PlanItem{{Target: "config.cfg", Action: "generate"}}}
	if _, err := prepareBuildRecovery(out, filepath.Join(out, "recovery"), "state/recovery/test", plan); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("recovery inside package was accepted: %v", err)
	}
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "recovery-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareBuildRecovery(out, link, "state/recovery/test", plan); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlink recovery root was accepted: %v", err)
	}
}

func TestPackageRecoveryDirectoryAndSymlinkParentsAreReserved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Game", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	profile := Profile{Name: "reserved", Frontend: "pegasus", FileMode: "copy", Templates: []ConfigTemplate{{Name: "bad", Scope: "package", OutputPath: recoveryDirectory + "/options.cfg", Body: "safe=true\n"}}}
	plan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil || len(plan.Conflicts) != 1 || plan.Conflicts[0] != recoveryDirectory+"/options.cfg" {
		t.Fatalf("reserved path was not rejected: %#v, %v", plan, err)
	}
	if _, err = Build(ctx, store, root, out, profile); !errors.Is(err, ErrUnmanagedTargetConflict) {
		t.Fatalf("reserved path build was not rejected: %v", err)
	}

	outside := t.TempDir()
	if err = os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(out, "gba")); err != nil {
		t.Fatal(err)
	}
	profile.Templates = nil
	if _, err = Build(ctx, store, root, out, profile); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlinked target parent was accepted: %v", err)
	}
	if entries, readErr := os.ReadDir(outside); readErr != nil || len(entries) != 0 {
		t.Fatalf("symlink target was modified: %v, %v", entries, readErr)
	}
}

func TestBuildCopiesDirectoryArtifact(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	dir := filepath.Join(root, "wiiu", "Game")
	if err := os.MkdirAll(filepath.Join(dir, "code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "code", "game.rpx"), []byte("rpx"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	w, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Directory Game", Platform: "wiiu"})
	e, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: w.ID, DefaultTitle: "Directory Game", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: e.ID, Path: "wiiu/Game", Role: "rom"})
	out := filepath.Join(t.TempDir(), "pack")
	result, err := Build(ctx, store, root, out, Profile{Name: "dir", Frontend: "pegasus", FileMode: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Copied != 1 {
		t.Fatalf("expected directory child copy, got %#v", result)
	}
	if _, err = os.Stat(filepath.Join(out, "wiiu", "Game", "code", "game.rpx")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAndroidPegasusPackage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	work, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: work.ID, DefaultTitle: "Game", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	out := filepath.Join(t.TempDir(), "android-pegasus")
	result, err := Build(ctx, store, root, out, Profile{Name: "android-pegasus", Frontend: "pegasus", Target: "android", Locale: "zh-CN", FileMode: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Exported != 1 || len(result.Warnings) != 1 || result.Warnings[0] != "no device profile selected; target filesystem constraints were not checked" {
		t.Fatalf("unexpected Android Pegasus result: %#v", result)
	}
	if _, err = os.Stat(filepath.Join(out, "gba", "metadata.pegasus.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(out, "library-manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAndroidPegasusWarningsTrackReviewedIntentBinding(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	enabled := true
	adapter, err := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "android-pegasus", Name: "Pegasus", Format: "pegasus", Handler: "pegasus", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "android-device", Name: "Android", Target: "android", OSFamily: "android", PathStyle: "posix", DefaultFrontendID: adapter.ID, Paths: map[string]string{"rom_dir": "roms", "core_dir": "cores"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"android"}, Launch: catalog.DriverLaunchSpec{RequiresCore: true, AndroidIntent: &catalog.AndroidIntentSpec{Package: "com.retroarch.aarch64", Activity: "com.retroarch.browser.retroactivity.RetroActivityFuture"}, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	core, err := store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "mgba", Name: "mGBA", LibraryNames: []string{"mgba_libretro"}, Platforms: []string{"gba"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Game", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	profile := Profile{Name: "android-pegasus", Frontend: "pegasus", Target: "android", DeviceProfileID: device.ID, FrontendAdapterID: adapter.ID, Locale: "en", FileMode: "copy"}
	unbound, err := Build(ctx, store, root, filepath.Join(t.TempDir(), "android-pegasus-unbound"), profile)
	if err != nil {
		t.Fatal(err)
	}
	wantUnbound := "edition " + edition.ID + " has no launch binding for device profile " + device.ID
	if unbound.Exported != 1 || len(unbound.Warnings) != 1 || unbound.Warnings[0] != wantUnbound {
		t.Fatalf("unbound Android package warning = %#v, want %q", unbound.Warnings, wantUnbound)
	}
	if _, err = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, FrontendAdapterID: adapter.ID, DriverID: driver.ID, CoreID: core.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := Build(ctx, store, root, filepath.Join(t.TempDir(), "android-pegasus-bound"), profile)
	if err != nil {
		t.Fatal(err)
	}
	if result.Exported != 1 || len(result.Warnings) != 0 {
		t.Fatalf("reviewed Android Intent binding produced warnings: %#v", result)
	}
}

func TestPlanAndBuildSafeConfigurationTemplates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "translation.ips"), []byte("patch"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/translation.ips", Role: "patch"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	out := filepath.Join(t.TempDir(), "package")
	profile := Profile{Name: "configured", Frontend: "pegasus", Target: "windows", Locale: "en", FileMode: "copy", Templates: []ConfigTemplate{
		{Name: "package settings", Scope: "package", OutputPath: "config/package.json", Body: `{"frontend":"{{profile.frontend}}","target":"{{profile.target}}"}`},
		{Name: "edition options", Scope: "edition", OutputPath: "config/{{platform.id}}/{{edition.id}}.cfg", Body: "rom={{rom.path}}\nfullscreen=true\n"},
	}}
	plan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 || plan.Fingerprint == "" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if _, err = Build(ctx, store, root, out, profile); err != nil {
		t.Fatal(err)
	}
	packageConfig, err := os.ReadFile(filepath.Join(out, "config", "package.json"))
	if err != nil || !strings.Contains(string(packageConfig), `"frontend":"pegasus"`) {
		t.Fatalf("package template = %q, %v", packageConfig, err)
	}
	editionConfig, err := os.ReadFile(filepath.Join(out, "config", "gba", edition.ID+".cfg"))
	if err != nil || !strings.Contains(string(editionConfig), "rom=gba/game.gba") {
		t.Fatalf("edition template = %q, %v", editionConfig, err)
	}
	metadata, err := os.ReadFile(filepath.Join(out, "gba", "metadata.pegasus.txt"))
	if err != nil || strings.Contains(string(metadata), "file: translation.ips") || !strings.Contains(string(metadata), "file: game.gba") {
		t.Fatalf("frontend launch files = %q, %v", metadata, err)
	}
	secondPlan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	if secondPlan.Fingerprint != plan.Fingerprint {
		t.Fatalf("output state changed source fingerprint: %s != %s", secondPlan.Fingerprint, plan.Fingerprint)
	}
	foundUnchanged := false
	for _, item := range secondPlan.Items {
		if item.Kind == "config" && item.Action == "unchanged" {
			foundUnchanged = true
		}
	}
	if !foundUnchanged {
		t.Fatalf("unchanged config was not detected: %#v", secondPlan.Items)
	}
}

func TestBuildRefusesToOverwriteUnmanagedTarget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("catalog-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(out, "gba", "game.gba")
	if err := os.WriteFile(target, []byte("user-owned-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	profile := Profile{Name: "conflict", Frontend: "pegasus", Target: "portable", FileMode: "copy"}
	plan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil || len(plan.Conflicts) != 1 || plan.Conflicts[0] != "gba/game.gba" {
		t.Fatalf("unmanaged collision plan = %#v, %v", plan, err)
	}
	if _, err = Build(ctx, store, root, out, profile); !errors.Is(err, ErrUnmanagedTargetConflict) {
		t.Fatalf("unmanaged target was not rejected: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "user-owned-file" {
		t.Fatalf("unmanaged file changed: %q, %v", data, err)
	}
}

func TestBuildRefusesToOverwriteModifiedManagedTargets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("catalog-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"})
	profile := Profile{Name: "managed", Frontend: "pegasus", Target: "portable", FileMode: "copy", Templates: []ConfigTemplate{{
		Name: "settings", Scope: "package", OutputPath: "config/emulator.cfg", Body: "fullscreen=true\n",
	}}}
	if _, err = Build(ctx, store, root, out, profile); err != nil {
		t.Fatal(err)
	}
	romTarget := filepath.Join(out, "gba", "game.gba")
	configTarget := filepath.Join(out, "config", "emulator.cfg")
	if err = os.WriteFile(romTarget, []byte("user-edited-rom-output"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configTarget, []byte("user-edited-config"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	wantConflicts := map[string]bool{"gba/game.gba": false, "config/emulator.cfg": false}
	for _, conflict := range plan.Conflicts {
		if _, ok := wantConflicts[conflict]; ok {
			wantConflicts[conflict] = true
		}
	}
	for path, found := range wantConflicts {
		if !found {
			t.Fatalf("modified managed target %q was not reported: %#v", path, plan)
		}
	}
	if _, err = Build(ctx, store, root, out, profile); !errors.Is(err, ErrUnmanagedTargetConflict) {
		t.Fatalf("modified managed targets were not rejected: %v", err)
	}
	for path, want := range map[string]string{romTarget: "user-edited-rom-output", configTarget: "user-edited-config"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != want {
			t.Fatalf("modified managed target changed: %s = %q, %v", path, data, readErr)
		}
	}
}

func TestBuildExportsResolvedRetroArchLaunchManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	adapter, _ := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "adapter", Name: "Pegasus", Format: "pegasus"})
	device, _ := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "device", Name: "Windows", Target: "windows", OSFamily: "windows", PathStyle: "windows", DefaultFrontendID: adapter.ID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves"}})
	driver, _ := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: catalog.DriverLaunchSpec{RequiresCore: true, Executables: map[string][]string{"windows": {"retroarch.exe"}}, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	core, _ := store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "mgba", Name: "mGBA", LibraryNames: []string{"mgba_libretro"}, Platforms: []string{"gba"}})
	_, _ = store.CreateCoreMapping(ctx, catalog.NewCoreMapping{ScopeType: "global", PlatformID: "gba", CoreID: core.ID})
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", SHA256: "launch-manifest-rom"})
	_, _ = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID, FrontendAdapterID: adapter.ID})
	profile := Profile{Name: "launches", Frontend: "pegasus", Target: "windows", DeviceProfileID: device.ID, FrontendAdapterID: adapter.ID, FileMode: "copy", Templates: []ConfigTemplate{{Name: "reviewed argv", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "arguments_json={{launch.arguments_json}}\nexecutable_hints_json={{launch.executable_hints_json}}\n"}}}
	plan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil || len(plan.Conflicts) != 0 {
		t.Fatalf("launch plan = %#v, %v", plan, err)
	}
	if _, err = Build(ctx, store, root, out, profile); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(out, "varkiv-launches.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil || !strings.Contains(string(manifest), `"mgba_libretro"`) || !strings.Contains(string(manifest), `"gba/game.gba"`) || strings.Contains(string(manifest), root) {
		t.Fatalf("launch manifest = %s, %v", manifest, err)
	}
	resolvedConfig, err := os.ReadFile(filepath.Join(out, "config", edition.ID+".cfg"))
	if err != nil || !strings.Contains(string(resolvedConfig), `arguments_json=["-L","mgba_libretro","gba/game.gba"]`) || !strings.Contains(string(resolvedConfig), `executable_hints_json=["retroarch.exe"]`) || strings.Contains(string(resolvedConfig), root) {
		t.Fatalf("resolved argv template = %s, %v", resolvedConfig, err)
	}
	if err = os.WriteFile(manifestPath, []byte("user launch configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil || len(plan.Conflicts) == 0 || plan.Conflicts[0] != "varkiv-launches.json" {
		t.Fatalf("modified launch manifest was not protected: %#v, %v", plan, err)
	}
}

func TestPortableRuntimeCatalogAndTemplatesRoundTripAtomically(t *testing.T) {
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "library")
	out := filepath.Join(t.TempDir(), "portable-package")
	if err := os.MkdirAll(filepath.Join(library, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "gba", "portable.gba"), []byte("portable-runtime-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	enabled := true
	adapter, err := source.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "portable-pegasus", Name: "Portable Pegasus", Format: "pegasus", Capabilities: map[string]bool{"export": true}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	device, err := source.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "portable-device", Name: "Portable Device", Target: "portable-os", OSFamily: "linux", PathStyle: "posix", DefaultFrontendID: adapter.ID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := source.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "portable-ra", Name: "Portable RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"portable-os"}, Launch: catalog.DriverLaunchSpec{RequiresCore: true, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "single-file", Patterns: []string{"{{rom.stem}}.srm"}}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	core, err := source.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "portable-mgba", Name: "Portable mGBA", LibraryNames: []string{"portable_mgba_libretro"}, Platforms: []string{"gba"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateCoreMapping(ctx, catalog.NewCoreMapping{ScopeType: "global", PlatformID: "gba", CoreID: core.ID}); err != nil {
		t.Fatal(err)
	}
	game, _ := source.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Portable Runtime", Platform: "gba"})
	edition, _ := source.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if _, err = source.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/portable.gba", Role: "rom", SHA256: "portable-runtime-source"}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID, FrontendAdapterID: adapter.ID, CoreID: core.ID}); err != nil {
		t.Fatal(err)
	}
	profile, err := source.CreatePackageProfile(ctx, catalog.NewPackageProfile{ID: "portable-profile", Name: "Portable profile", Frontend: "pegasus", Target: device.Target, DeviceProfileID: device.ID, FrontendAdapterID: adapter.ID, Locale: "en", FileMode: "copy", OutputSlug: "portable-profile", Enabled: &enabled, Templates: []catalog.NewPackageConfigTemplate{{Name: "Core options", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "core={{core.library}}\nrom={{rom.path}}\n", SortOrder: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	bundleProfile := Profile{ID: profile.ID, Name: profile.Name, Frontend: profile.Frontend, Target: profile.Target, DeviceProfileID: profile.DeviceProfileID, FrontendAdapterID: profile.FrontendAdapterID, Locale: profile.Locale, FileMode: profile.FileMode, OutputSlug: profile.OutputSlug, Enabled: true, Templates: []ConfigTemplate{{Name: profile.Templates[0].Name, Scope: profile.Templates[0].Scope, OutputPath: profile.Templates[0].OutputPath, Body: profile.Templates[0].Body}}}
	if _, err = Build(ctx, source, library, out, bundleProfile); err != nil {
		t.Fatal(err)
	}
	launchData, err := os.ReadFile(filepath.Join(out, "varkiv-launches.json"))
	if err != nil || !strings.Contains(string(launchData), `"format_version": 2`) || !strings.Contains(string(launchData), `"portable-ra"`) || !strings.Contains(string(launchData), `"package_profile"`) || strings.Contains(string(launchData), `"created_at"`) {
		t.Fatalf("portable runtime sidecar=%s err=%v", launchData, err)
	}
	destination, err := catalog.Open(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	games, err := importer.PreviewLibraryManifest(out, filepath.Join(out, "library-manifest.json"))
	if err != nil || len(games) != 1 || games[0].RuntimeCatalog == nil || len(games[0].RuntimeHints) != 1 {
		t.Fatalf("portable preview=%#v err=%v", games, err)
	}
	if err = destination.ImportGamesAtomic(ctx, games); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.GetFrontendAdapter(ctx, adapter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.GetDeviceProfile(ctx, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.GetEmulatorDriver(ctx, driver.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.GetRetroArchCore(ctx, core.ID); err != nil {
		t.Fatal(err)
	}
	restoredProfile, err := destination.GetPackageProfile(ctx, profile.ID)
	if err != nil || len(restoredProfile.Templates) != 1 || restoredProfile.Templates[0].Body != profile.Templates[0].Body {
		t.Fatalf("restored profile=%#v err=%v", restoredProfile, err)
	}
	hints, err := destination.ListRuntimeImportHints(ctx, edition.ID, "pending")
	if err != nil || len(hints) != 1 || hints[0].SourceFormat != "varkiv-launches-v2" {
		t.Fatalf("restored hints=%#v err=%v", hints, err)
	}
	if _, err = destination.ApplyRuntimeImportHint(ctx, hints[0].ID, catalog.NewLaunchBinding{EditionID: edition.ID}); err != nil {
		t.Fatalf("portable definitions did not make the reviewed hint applicable: %v", err)
	}
}

func TestESDERuntimeReviewToDeclarativePackageRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	dir := filepath.Join(root, "gamelists", "gba")
	out := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "safe.gba"), []byte("safe-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	gamelist := filepath.Join(dir, "gamelist.xml")
	if err := os.WriteFile(gamelist, []byte(`<gameList><game><path>./safe.gba</path><name>Safe</name></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(root, "es_systems.xml")
	const foreignCommand = `foreign-shell --unsafe %ROM%`
	if err := os.WriteFile(runtime, []byte(`<systemList><system><name>gba</name><command>`+foreignCommand+`</command></system></systemList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	games, err := importer.PreviewESDEWithRuntime(root, gamelist, runtime, "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ImportGamesAtomic(ctx, games); err != nil {
		t.Fatal(err)
	}
	imported, err := store.ListGames(ctx, "en")
	if err != nil || len(imported) != 1 || len(imported[0].Editions) != 1 {
		t.Fatalf("imported games=%#v err=%v", imported, err)
	}
	edition := imported[0].Editions[0]
	hints, err := store.ListRuntimeImportHints(ctx, edition.ID, "pending")
	if err != nil || len(hints) != 1 || hints[0].RawCommand != foreignCommand || len(hints[0].Arguments) != 0 {
		t.Fatalf("runtime hints=%#v err=%v", hints, err)
	}
	adapter, _ := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "adapter-esde", Name: "ES-DE", Format: "es-de"})
	device, _ := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "device-rocknix", Name: "ROCKNIX", Target: "rocknix", OSFamily: "linux", PathStyle: "posix", DefaultFrontendID: adapter.ID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves"}})
	driver, _ := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "driver-ra", Name: "RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"rocknix"}, Launch: catalog.DriverLaunchSpec{RequiresCore: true, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	core, _ := store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "core-mgba", Name: "mGBA", LibraryNames: []string{"mgba_libretro"}, Platforms: []string{"gba"}})
	_, _ = store.CreateCoreMapping(ctx, catalog.NewCoreMapping{ScopeType: "global", PlatformID: "gba", CoreID: core.ID})
	_, err = store.ApplyRuntimeImportHint(ctx, hints[0].ID, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID, FrontendAdapterID: adapter.ID, CoreID: core.ID, Arguments: []string{"--appendconfig", "{{device.config_dir}}/gba.cfg", "{{rom.path}}"}})
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{Name: "reviewed-esde", Frontend: "es-de", Target: "rocknix", DeviceProfileID: device.ID, FrontendAdapterID: adapter.ID, FileMode: "copy", Templates: []ConfigTemplate{{Name: "Core options", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "rom={{rom.path}}\ncore={{core.library}}\ndevice_config={{device.config_dir}}\ndriver={{driver.id}}\nvideo_threaded=true\n"}}}
	if _, err = Build(ctx, store, root, out, profile); err != nil {
		t.Fatal(err)
	}
	launches, err := os.ReadFile(filepath.Join(out, "varkiv-launches.json"))
	if err != nil || !strings.Contains(string(launches), `--appendconfig`) || !strings.Contains(string(launches), `mgba_libretro`) || strings.Contains(string(launches), foreignCommand) {
		t.Fatalf("reviewed launch manifest=%s err=%v", launches, err)
	}
	config, err := os.ReadFile(filepath.Join(out, "config", edition.ID+".cfg"))
	if err != nil || !strings.Contains(string(config), "core=mgba_libretro") || !strings.Contains(string(config), "device_config=config") || !strings.Contains(string(config), "driver=driver-ra") || !strings.Contains(string(config), "video_threaded=true") || strings.Contains(string(config), foreignCommand) {
		t.Fatalf("custom config=%s err=%v", config, err)
	}
}

func TestConfigTemplateRejectsExecutableAndUnsafeActions(t *testing.T) {
	base := Profile{Name: "unsafe", Frontend: "pegasus", Target: "windows", FileMode: "reference"}
	cases := []ConfigTemplate{
		{Name: "script", Scope: "package", OutputPath: "launch.ps1", Body: "echo ok"},
		{Name: "traversal", Scope: "package", OutputPath: "../config.ini", Body: "safe=true"},
		{Name: "environment", Scope: "package", OutputPath: "config.ini", Body: "home={{env.HOME}}"},
		{Name: "template action", Scope: "package", OutputPath: "config.ini", Body: "{{if profile.name}}yes{{end}}"},
		{Name: "wrong scope", Scope: "package", OutputPath: "config.ini", Body: "rom={{rom.path}}"},
		{Name: "raw argv", Scope: "edition", OutputPath: "config.ini", Body: "arguments={{launch.arguments}}"},
	}
	for _, item := range cases {
		profile := base
		profile.Templates = []ConfigTemplate{item}
		if _, err := normalizeProfile(profile); err == nil {
			t.Fatalf("unsafe template %q was accepted", item.Name)
		}
	}
}
