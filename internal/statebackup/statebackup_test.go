package statebackup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/saves"
	storagex "varkiv/internal/storage"
)

type fixture struct {
	database   string
	state      string
	gameID     string
	revisionID string
	fileID     string
	rom        []byte
	media      []byte
	save       []byte
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func makeFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	state := filepath.Join(root, "service-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(state, "library.db")
	store, err := catalog.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = store.Close()
		}
	})

	rom := []byte("synthetic-managed-rom")
	romRel := filepath.ToSlash(filepath.Join("gba", "fixture", "game.gba"))
	romPath := filepath.Join(state, "roms", filepath.FromSlash(romRel))
	if err = os.MkdirAll(filepath.Dir(romPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(romPath, rom, 0o640); err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Synthetic game", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEditionWithArtifact(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"}, catalog.NewArtifact{
		Path: romRel, StorageKind: "managed", OriginalName: "game.gba", Role: "rom", Size: int64(len(rom)), SHA256: hashBytes(rom),
	})
	if err != nil {
		t.Fatal(err)
	}

	media := []byte("synthetic-cover")
	mediaHash := hashBytes(media)
	mediaRel := filepath.ToSlash(filepath.Join("blobs", "sha256", mediaHash[:2], mediaHash+".png"))
	mediaPath := filepath.Join(state, "media", filepath.FromSlash(mediaRel))
	if err = os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mediaPath, media, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMedia(ctx, catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: mediaRel, OriginalName: "cover.png", MIMEType: "image/png", Size: int64(len(media)), SHA256: mediaHash}); err != nil {
		t.Fatal(err)
	}

	device, err := store.CreateDevice(ctx, catalog.NewDevice{Name: "Synthetic device", OSFamily: "windows", Architecture: "x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	save := []byte("synthetic-save")
	saveRepo, err := saves.New(store, filepath.Join(state, "saves"))
	if err != nil {
		t.Fatal(err)
	}
	push, err := saveRepo.Push(ctx, saves.PushInput{EditionID: edition.ID, DeviceID: device.ID, DriverID: "retroarch", RelativePath: "game.srm", ScopeType: "game"}, strings.NewReader(string(save)))
	if err != nil {
		t.Fatal(err)
	}

	recoveryRel := filepath.ToSlash(filepath.Join("state", "recovery", "packages", "synthetic", "release-1"))
	recoveryRoot := filepath.Join(state, "recovery", "packages", "synthetic", "release-1")
	if err = os.MkdirAll(filepath.Join(recoveryRoot, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(recoveryRoot, "snapshot.json"), []byte("{\"format_version\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(recoveryRoot, "files", "old.cfg"), []byte("old=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreatePackageProfile(ctx, catalog.NewPackageProfile{Name: "Synthetic package", Frontend: "pegasus", Target: "windows", FileMode: "copy", OutputSlug: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreatePackagePlan(ctx, catalog.NewPackagePlanRecord{ProfileID: profile.ID, Fingerprint: "synthetic-fingerprint", PlanJSON: `{"items":[]}`, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RecordPackageRelease(ctx, catalog.NewPackageReleaseRecord{ProfileID: profile.ID, PlanID: plan.ID, OutputSlug: profile.OutputSlug, ResultJSON: `{"recovery_snapshot":"` + recoveryRel + `"}`}, "built"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return fixture{database: database, state: state, gameID: game.ID, revisionID: push.Revision.ID, fileID: push.Revision.Files[0].ID, rom: rom, media: media, save: save}
}

func TestCompleteStateBackupCheckAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	fixture := makeFixture(t)
	for _, excluded := range []string{
		filepath.Join(fixture.state, "media", "cache", "thumbnails-v1", "aa", "128.png"),
		filepath.Join(fixture.state, "media", ".staging", "active-upload"),
	} {
		if err := os.MkdirAll(filepath.Dir(excluded), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(excluded, []byte("rebuildable-or-transient"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(t.TempDir(), "new-backup")
	report, err := Create(ctx, fixture.database, fixture.state, backup)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != catalog.CurrentSchemaVersion || report.ManagedArtifacts != 1 || report.ManagedMedia != 1 || report.SaveBlobs != 1 || report.RecoverySnapshots != 1 || report.Files < 6 || report.Bytes == 0 {
		t.Fatalf("unexpected backup report: %#v", report)
	}
	for _, excluded := range []string{filepath.Join(backup, "state", "media", "cache"), filepath.Join(backup, "state", "media", ".staging")} {
		if _, err = os.Stat(excluded); !os.IsNotExist(err) {
			t.Fatalf("backup retained rebuildable or transient media state %s: %v", excluded, err)
		}
	}

	backupDB, err := sql.Open("sqlite", filepath.Join(backup, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	var portable string
	if err = backupDB.QueryRow(`SELECT blob_path FROM save_files LIMIT 1`).Scan(&portable); err != nil {
		backupDB.Close()
		t.Fatal(err)
	}
	backupDB.Close()
	if !strings.HasPrefix(portable, "state/saves/blobs/") || filepath.IsAbs(portable) || strings.Contains(portable, fixture.state) {
		t.Fatalf("backup save path is not portable: %q", portable)
	}

	restored := filepath.Join(t.TempDir(), "new-restore")
	restoreReport, err := Restore(ctx, backup, restored)
	if err != nil {
		t.Fatal(err)
	}
	if restoreReport.ManagedArtifacts != 1 || restoreReport.ManagedMedia != 1 || restoreReport.SaveBlobs != 1 || restoreReport.RecoverySnapshots != 1 {
		t.Fatalf("unexpected restore report: %#v", restoreReport)
	}
	restoredStore, err := catalog.Open(filepath.Join(restored, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	game, err := restoredStore.GetGame(ctx, fixture.gameID, "")
	if err != nil || game.DefaultTitle != "Synthetic game" {
		t.Fatalf("restored catalog = %#v, %v", game, err)
	}
	restoredRepo, err := saves.New(restoredStore, filepath.Join(restored, "state", "saves"))
	if err != nil {
		t.Fatal(err)
	}
	handle, _, err := restoredRepo.OpenRevisionFile(ctx, fixture.revisionID, fixture.fileID)
	if err != nil {
		t.Fatal(err)
	}
	content := make([]byte, len(fixture.save))
	if _, err = handle.Read(content); err != nil {
		handle.Close()
		t.Fatal(err)
	}
	handle.Close()
	if string(content) != string(fixture.save) {
		t.Fatalf("restored save = %q", content)
	}
	if got, err := os.ReadFile(filepath.Join(restored, "state", "roms", "gba", "fixture", "game.gba")); err != nil || string(got) != string(fixture.rom) {
		t.Fatalf("restored ROM = %q, %v", got, err)
	}
	mediaHash := hashBytes(fixture.media)
	if got, err := os.ReadFile(filepath.Join(restored, "state", "media", "blobs", "sha256", mediaHash[:2], mediaHash+".png")); err != nil || string(got) != string(fixture.media) {
		t.Fatalf("restored media = %q, %v", got, err)
	}
}

func TestManagedCleanupRecoverySurvivesCompleteStateBackup(t *testing.T) {
	ctx := context.Background()
	fixture := makeFixture(t)
	store, err := catalog.Open(fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := storagex.New(t.TempDir(), fixture.state)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	orphanRelative := filepath.ToSlash(filepath.Join("blobs", "sha256", "cc", "orphan.png"))
	orphan := filepath.Join(fixture.state, "media", filepath.FromSlash(orphanRelative))
	if err = os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = os.WriteFile(orphan, []byte("recoverable-orphan"), 0o600); err != nil {
		store.Close()
		t.Fatal(err)
	}
	references, err := store.ManagedStorageReferences(ctx)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	plan, err := repository.PreviewManagedCleanup(ctx, references)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	var candidate []storagex.CleanupCandidate
	for _, item := range plan.Candidates {
		if item.StorageKind == "media" && item.RelativePath == orphanRelative {
			candidate = append(candidate, item)
		}
	}
	run, err := repository.QuarantineManagedCleanup(ctx, plan.Fingerprint, candidate)
	if closeErr := store.Close(); err == nil {
		err = closeErr
	}
	if err != nil || run.Status != "quarantined" {
		t.Fatalf("cleanup run=%#v err=%v", run, err)
	}

	backup := filepath.Join(t.TempDir(), "cleanup-backup")
	if _, err = Create(ctx, fixture.database, fixture.state, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "cleanup-restore")
	if _, err = Restore(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	restoredRepository, err := storagex.New(t.TempDir(), filepath.Join(restored, "state"))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := restoredRepository.ListCleanupRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID || runs[0].Status != "quarantined" {
		t.Fatalf("restored cleanup history=%#v err=%v", runs, err)
	}
	if _, err = restoredRepository.RestoreCleanupRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(restored, "state", "media", filepath.FromSlash(orphanRelative)))
	if err != nil || string(got) != "recoverable-orphan" {
		t.Fatalf("restored quarantined file=%q err=%v", got, err)
	}
}

func TestBackupTamperAndExistingOutputsAreRejectedWithoutOverwrite(t *testing.T) {
	ctx := context.Background()
	fixture := makeFixture(t)
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(ctx, fixture.database, fixture.state, backup); err != nil {
		t.Fatal(err)
	}
	tampered := filepath.Join(backup, "state", "roms", "gba", "fixture", "game.gba")
	if err := os.WriteFile(tampered, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(ctx, backup); err == nil {
		t.Fatal("tampered backup was accepted")
	}
	failedRestore := filepath.Join(t.TempDir(), "failed-restore")
	if _, err := Restore(ctx, backup, failedRestore); err == nil {
		t.Fatal("tampered backup was restored")
	}
	if _, err := os.Lstat(failedRestore); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed validation created a restore output: %v", err)
	}

	existingRoot := t.TempDir()
	existing := filepath.Join(existingRoot, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "important.txt")
	if err := os.WriteFile(marker, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, fixture.database, fixture.state, existing); err == nil {
		t.Fatal("existing backup output was accepted")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "user-owned" {
		t.Fatalf("existing output changed: %q, %v", got, err)
	}
}

func TestBackupRejectsSymlinksAndOutputOverlap(t *testing.T) {
	fixture := makeFixture(t)
	if _, err := Create(context.Background(), fixture.database, fixture.state, filepath.Join(fixture.state, "nested-backup")); err == nil {
		t.Fatal("backup inside state root was accepted")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.state, "unsafe-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(context.Background(), fixture.database, fixture.state, output); err == nil {
		t.Fatal("state symlink was accepted")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed backup left an output: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "outside" {
		t.Fatalf("symlink target changed: %q, %v", got, err)
	}
}
