package runtimecfg

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestResolveRetroArchLaunchUsesCoreAndArgvWithoutAbsolutePaths(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	adapter, _ := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{ID: "adapter", Name: "Pegasus", Format: "pegasus"})
	device, _ := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "device", Name: "Windows", Target: "windows", OSFamily: "windows", PathStyle: "windows", DefaultFrontendID: adapter.ID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves"}})
	driver, _ := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: catalog.DriverLaunchSpec{RequiresCore: true, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	core, _ := store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "mgba", Name: "mGBA", LibraryNames: []string{"mgba_libretro"}, Platforms: []string{"gba"}})
	_, _ = store.CreateCoreMapping(ctx, catalog.NewCoreMapping{ScopeType: "global", PlatformID: "gba", CoreID: core.ID})
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/translation.ips", Role: "patch", SHA256: "resolver-patch"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", SHA256: "resolver-rom"})
	_, _ = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID})
	resolved, err := Resolve(ctx, store, edition.ID, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Arguments) != 3 || resolved.Arguments[1] != "mgba_libretro" || resolved.Arguments[2] != "gba/game.gba" {
		t.Fatalf("arguments = %#v", resolved.Arguments)
	}
	if resolved.CoreResolution == nil || resolved.CoreResolution.Resolution != "global" || resolved.ROMPath != "gba/game.gba" {
		t.Fatalf("resolution = %#v", resolved)
	}
}

func TestResolveDoesNotRenderAbsoluteSourcePath(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	device, _ := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "device", Name: "Windows", Target: "windows", OSFamily: "windows", PathStyle: "windows"})
	driver, _ := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "driver", Name: "Standalone", Family: "standalone", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: catalog.DriverLaunchSpec{Arguments: []string{"{{rom.source_path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	privatePath := filepath.Join(t.TempDir(), "private", "game.gba")
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", SourcePath: privatePath, Role: "rom", SHA256: "resolver-rom"})
	_, _ = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID})
	_, err = Resolve(ctx, store, edition.ID, device.ID)
	if err == nil || !strings.Contains(err.Error(), "unavailable rom.source_path") || strings.Contains(err.Error(), privatePath) {
		t.Fatalf("absolute source path resolution error=%v", err)
	}
}

func TestResolveReportsExplicitAndroidIntentComponent(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	device, _ := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "android", Name: "Android", Target: "android", OSFamily: "android", PathStyle: "android-uri"})
	driver, _ := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{ID: "ppsspp", Name: "PPSSPP", Family: "ppsspp", Platforms: []string{"psp"}, Targets: []string{"android"}, Launch: catalog.DriverLaunchSpec{AndroidIntent: &catalog.AndroidIntentSpec{Action: "android.intent.action.VIEW", Package: "org.ppsspp.ppsspp", Activity: ".PpssppActivity", Data: "{{rom.uri}}"}, Arguments: []string{"{{rom.path}}"}}, Save: catalog.DriverSaveSpec{Scope: "game", Layout: "directory"}})
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "psp"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "psp/game.iso", Role: "rom", SHA256: "resolver-rom"})
	_, _ = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID})
	resolved, err := Resolve(ctx, store, edition.ID, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AndroidPackage != "org.ppsspp.ppsspp" || resolved.AndroidActivity != ".PpssppActivity" || len(resolved.Warnings) != 0 {
		t.Fatalf("Android resolution = %#v", resolved)
	}
}

func TestResolveOmitsAndroidIntentFromWindowsStandaloneLaunch(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	device, _ := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "windows", Name: "Windows", Target: "windows", OSFamily: "windows", PathStyle: "windows"})
	driver, _ := store.CreateEmulatorDriver(ctx, catalog.NewEmulatorDriver{
		ID: "ppsspp", Name: "PPSSPP", Family: "standalone", Platforms: []string{"psp"}, Targets: []string{"windows", "android"},
		Launch: catalog.DriverLaunchSpec{
			Executables:     map[string][]string{"windows": {"PPSSPPWindows64.exe"}},
			AndroidPackage:  "org.ppsspp.ppsspp",
			AndroidActivity: ".PpssppActivity",
			AndroidIntent:   &catalog.AndroidIntentSpec{Package: "org.ppsspp.ppsspp", Activity: ".PpssppActivity"},
			Arguments:       []string{"{{rom.path}}"},
		},
		Save: catalog.DriverSaveSpec{Scope: "game", Layout: "directory"},
	})
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "psp"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "psp/game.iso", Role: "rom", SHA256: "resolver-rom"})
	_, _ = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID})

	resolved, err := Resolve(ctx, store, edition.ID, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AndroidPackage != "" || resolved.AndroidActivity != "" {
		t.Fatalf("Windows resolution leaked Android component: %#v", resolved)
	}
	if len(resolved.ExecutableHints) != 1 || resolved.ExecutableHints[0] != "PPSSPPWindows64.exe" || len(resolved.Warnings) != 0 {
		t.Fatalf("Windows executable resolution = %#v", resolved)
	}
}
