package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestRuntimeImportHintApplyIsExplicitAndAtomic(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	driver, err := s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "Test driver", Family: "standalone", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game", Layout: "file", Patterns: []string{"*.sav"}, Refresh: "poll", Portability: "driver-dependent"}})
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDeviceProfile(ctx, NewDeviceProfile{Name: "Test device", Target: "windows", OSFamily: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	game := ImportedGame{Platform: "gba", DefaultTitle: "Hint game", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "gba/hint.gba", SHA256: "hint-rom"}}, RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: `dangerous-shell --delete "{file.path}"`, SourceRef: "gba/metadata.pegasus.txt"}}}
	if err = s.ImportGamesAtomic(ctx, []ImportedGame{game}); err != nil {
		t.Fatal(err)
	}
	games, err := s.ListGames(ctx, "")
	if err != nil || len(games) != 1 {
		t.Fatalf("games=%#v err=%v", games, err)
	}
	hints, err := s.ListRuntimeImportHints(ctx, games[0].Editions[0].ID, "pending")
	if err != nil || len(hints) != 1 || hints[0].Trust != "untrusted" {
		t.Fatalf("hints=%#v err=%v", hints, err)
	}
	binding, err := s.ApplyRuntimeImportHint(ctx, hints[0].ID, NewLaunchBinding{DeviceProfileID: device.ID, DriverID: driver.ID, Arguments: []string{"--safe", "{{rom.path}}"}})
	if err != nil {
		t.Fatal(err)
	}
	if binding.EditionID != games[0].Editions[0].ID || len(binding.Arguments) != 2 || binding.Arguments[0] != "--safe" {
		t.Fatalf("binding=%#v", binding)
	}
	applied, err := s.GetRuntimeImportHint(ctx, hints[0].ID)
	if err != nil || applied.Status != "applied" || applied.RawCommand == "" {
		t.Fatalf("applied=%#v err=%v", applied, err)
	}
	if _, err = s.ApplyRuntimeImportHint(ctx, hints[0].ID, NewLaunchBinding{DriverID: driver.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected already-applied hint to be unavailable, got %v", err)
	}
}

func runtimeHintBatchFixture(t *testing.T) (*Store, RuntimeHintBatchReview, []RuntimeImportHint) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	enabled := true
	profile, err := s.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "batch-device", Name: "Batch device", Target: "windows", OSFamily: "windows", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "batch-retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"pokemini"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true}, Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	core, err := s.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "batch-pokemini", Name: "PokeMini", LibraryNames: []string{"pokemini_libretro"}, Platforms: []string{"pokemini"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	games := []ImportedGame{
		{Platform: "pokemini", DefaultTitle: "Batch one", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "pokemini/one.zip", SHA256: "batch-one"}}, RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: `retroarch.exe -L pokemini_libretro.dll "{file.path}"`}}},
		{Platform: "pokemini", DefaultTitle: "Batch two", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "pokemini/two.zip", SHA256: "batch-two"}}, RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: `retroarch.exe -L pokemini_libretro.dll "{file.path}"`}}},
	}
	if err = s.ImportGamesAtomic(ctx, games); err != nil {
		t.Fatal(err)
	}
	hints, err := s.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 2 {
		t.Fatalf("hints=%#v err=%v", hints, err)
	}
	return s, RuntimeHintBatchReview{
		HintIDs: []string{hints[1].ID, hints[0].ID}, DeviceProfileID: profile.ID,
		DriverID: driver.ID, CoreID: core.ID, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"},
	}, hints
}

func TestRuntimeImportHintBatchReviewAppliesEveryBindingAtomically(t *testing.T) {
	s, review, _ := runtimeHintBatchFixture(t)
	ctx := context.Background()
	snapshot, err := s.ReviewRuntimeImportHintBatch(ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PlatformID != "pokemini" || len(snapshot.Hints) != 2 || snapshot.DefinitionFingerprint == "" || snapshot.Hints[0].Fingerprint == "" || snapshot.Review.HintIDs[0] > snapshot.Review.HintIDs[1] {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	result, err := s.ApplyRuntimeImportHintBatchIfSnapshot(ctx, snapshot)
	if err != nil || result.Applied != 2 || len(result.Applications) != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, application := range result.Applications {
		if application.Hint.Status != "applied" || application.Binding.DeviceProfileID != review.DeviceProfileID || application.Binding.DriverID != review.DriverID || application.Binding.CoreID != review.CoreID || len(application.Binding.Arguments) != 3 {
			t.Fatalf("application=%#v", application)
		}
	}
	pending, err := s.ListRuntimeImportHints(ctx, "", "pending")
	bindings, bindingErr := s.ListLaunchBindings(ctx, "")
	if err != nil || bindingErr != nil || len(pending) != 0 || len(bindings) != 2 {
		t.Fatalf("pending=%#v bindings=%#v err=%v bindingErr=%v", pending, bindings, err, bindingErr)
	}
}

func TestRuntimeImportHintBatchDriftLeavesEveryUnchangedHintPending(t *testing.T) {
	s, review, hints := runtimeHintBatchFixture(t)
	ctx := context.Background()
	snapshot, err := s.ReviewRuntimeImportHintBatch(ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DismissRuntimeImportHint(ctx, hints[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyRuntimeImportHintBatchIfSnapshot(ctx, snapshot); !errors.Is(err, ErrRuntimeHintBatchStale) {
		t.Fatalf("expected stale batch, got %v", err)
	}
	bindings, err := s.ListLaunchBindings(ctx, "")
	if err != nil || len(bindings) != 0 {
		t.Fatalf("drifted batch wrote bindings=%#v err=%v", bindings, err)
	}
	first, firstErr := s.GetRuntimeImportHint(ctx, hints[0].ID)
	second, secondErr := s.GetRuntimeImportHint(ctx, hints[1].ID)
	if firstErr != nil || secondErr != nil || first.Status != "pending" || second.Status != "dismissed" {
		t.Fatalf("first=%#v second=%#v errors=%v/%v", first, second, firstErr, secondErr)
	}
}

func TestRuntimeImportHintBatchRuntimeDefinitionDriftIsRejected(t *testing.T) {
	s, review, _ := runtimeHintBatchFixture(t)
	ctx := context.Background()
	snapshot, err := s.ReviewRuntimeImportHintBatch(ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE emulator_drivers SET name='Drifted RetroArch' WHERE id=?`, review.DriverID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyRuntimeImportHintBatchIfSnapshot(ctx, snapshot); !errors.Is(err, ErrRuntimeHintBatchStale) {
		t.Fatalf("expected runtime-definition drift rejection, got %v", err)
	}
	bindings, listErr := s.ListLaunchBindings(ctx, "")
	if listErr != nil || len(bindings) != 0 {
		t.Fatalf("definition drift wrote bindings=%#v err=%v", bindings, listErr)
	}
}

func TestRuntimeImportHintBatchConflictDoesNotPartiallyApply(t *testing.T) {
	s, review, hints := runtimeHintBatchFixture(t)
	ctx := context.Background()
	snapshot, err := s.ReviewRuntimeImportHintBatch(ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateLaunchBinding(ctx, NewLaunchBinding{EditionID: hints[1].EditionID, DeviceProfileID: review.DeviceProfileID, DriverID: review.DriverID, CoreID: review.CoreID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyRuntimeImportHintBatchIfSnapshot(ctx, snapshot); !errors.Is(err, ErrRuntimeHintBatchConflict) {
		t.Fatalf("expected batch conflict, got %v", err)
	}
	bindings, listErr := s.ListLaunchBindings(ctx, "")
	first, hintErr := s.GetRuntimeImportHint(ctx, hints[0].ID)
	if listErr != nil || hintErr != nil || len(bindings) != 1 || first.Status != "pending" {
		t.Fatalf("conflict partially applied: bindings=%#v first=%#v errors=%v/%v", bindings, first, listErr, hintErr)
	}
}

func TestUntrustedRuntimeHintSuggestsExactCataloguedRetroArchCore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enabled := true
	driver, err := s.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"pokemini"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true, Executables: map[string][]string{"windows": {"retroarch.exe"}}, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	core, err := s.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "pokemini", Name: "PokeMini", LibraryNames: []string{"pokemini_libretro"}, Platforms: []string{"pokemini"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	raw := `"{env.appdir}\RetroArch\retroarch.exe" -L "{env.appdir}\RetroArch\cores\pokemini_libretro.dll" "{file.path}"`
	game := ImportedGame{Platform: "pokemini", DefaultTitle: "Hint game", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "pokemini/hint.zip", SHA256: "hint-rom"}}, RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: raw, SourceRef: "pokemini/metadata.pegasus.txt"}}}
	if err = s.ImportGamesAtomic(ctx, []ImportedGame{game}); err != nil {
		t.Fatal(err)
	}
	hints, err := s.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 1 {
		t.Fatalf("hints=%#v err=%v", hints, err)
	}
	hint := hints[0]
	if hint.Trust != "untrusted" || hint.DriverID != driver.ID || hint.CoreID != core.ID || hint.RawCommand != raw || len(hint.Arguments) != 0 {
		t.Fatalf("unsafe or incomplete suggestion: %#v", hint)
	}
	if _, err = s.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "pokemini-alternate", Name: "Alternate PokeMini", LibraryNames: []string{"pokemini_libretro"}, Platforms: []string{"pokemini"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	ambiguousRaw := raw + " --ambiguous"
	if err = s.ImportGamesAtomic(ctx, []ImportedGame{{
		Platform: "pokemini", DefaultTitle: "Ambiguous hint", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []NewArtifact{{Path: "pokemini/ambiguous.zip", SHA256: "ambiguous-rom"}},
		RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: ambiguousRaw}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "foreign", Name: "Foreign", LibraryNames: []string{"foreign_libretro"}, Platforms: []string{"pokemini"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	mismatchedRaw := `retroarch -L foreign_libretro.dll "{file.path}"`
	if err = s.ImportGamesAtomic(ctx, []ImportedGame{{
		Platform: "gba", DefaultTitle: "Mismatched hint", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []NewArtifact{{Path: "gba/mismatched.gba", SHA256: "mismatched-rom"}},
		RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: mismatchedRaw}},
	}}); err != nil {
		t.Fatal(err)
	}
	hints, err = s.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 3 {
		t.Fatalf("expanded hints=%#v err=%v", hints, err)
	}
	for _, candidate := range hints {
		if candidate.RawCommand == ambiguousRaw || candidate.RawCommand == mismatchedRaw {
			if candidate.DriverID != "" || candidate.CoreID != "" || len(candidate.Arguments) != 0 || candidate.Trust != "untrusted" {
				t.Fatalf("ambiguous or cross-platform hint was guessed: %#v", candidate)
			}
		}
	}
}

func TestRuntimeImportHintFailureRollsBackWholeBatch(t *testing.T) {
	s := testStore(t)
	err := s.ImportGamesAtomic(context.Background(), []ImportedGame{
		{Platform: "gba", DefaultTitle: "First", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "gba/first-hint.gba", SHA256: "first-hint"}}},
		{Platform: "gba", DefaultTitle: "Second", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "gba/second-hint.gba", SHA256: "second-hint"}}, RuntimeHints: []NewRuntimeImportHint{{SourceKind: "structured-sidecar", SourceFormat: "varkiv-launches-v1", DriverID: "bad id with spaces"}}},
	})
	if err == nil {
		t.Fatal("expected invalid runtime hint to abort batch")
	}
	games, listErr := s.ListGames(context.Background(), "")
	if listErr != nil || len(games) != 0 {
		t.Fatalf("atomic import left games=%#v err=%v", games, listErr)
	}
}

func TestPendingUntrustedRuntimeSuggestionsRefreshAfterCatalogUpgrade(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	raw := `retroarch.exe -L pokemini_libretro.dll "{file.path}"`
	if err := s.ImportGamesAtomic(ctx, []ImportedGame{{
		Platform: "pokemini", DefaultTitle: "Historical hint", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []NewArtifact{{Path: "pokemini/historical.zip", SHA256: "historical-rom"}},
		RuntimeHints: []NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: raw}},
	}}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := s.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"pokemini"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true}, Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "pokemini", Name: "PokeMini", LibraryNames: []string{"pokemini_libretro"}, Platforms: []string{"pokemini"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.SuggestPendingRuntimeHints(ctx)
	if err != nil || changed != 1 {
		t.Fatalf("refresh changed=%d err=%v", changed, err)
	}
	hints, err := s.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 1 || hints[0].DriverID != "retroarch" || hints[0].CoreID != "pokemini" || hints[0].Trust != "untrusted" || hints[0].Status != "pending" || hints[0].RawCommand != raw || len(hints[0].Arguments) != 0 {
		t.Fatalf("refreshed hint=%#v err=%v", hints, err)
	}
	if changed, err = s.SuggestPendingRuntimeHints(ctx); err != nil || changed != 0 {
		t.Fatalf("idempotent refresh changed=%d err=%v", changed, err)
	}
}

func TestMigrationFromV9AddsRuntimeImportHints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v9.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := s.CreateGame(context.Background(), NewGame{DefaultTitle: "Preserved", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TABLE runtime_import_hints; PRAGMA user_version = 9;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	version, err := migrated.SchemaVersion(context.Background())
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if _, err = migrated.GetGame(context.Background(), game.ID, ""); err != nil {
		t.Fatalf("v9 data was not preserved: %v", err)
	}
	var table string
	if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='runtime_import_hints'`).Scan(&table); err != nil || table != "runtime_import_hints" {
		t.Fatalf("runtime_import_hints missing: %q %v", table, err)
	}
}
