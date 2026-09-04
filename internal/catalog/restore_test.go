package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRestoreDatabaseBackupCreatesValidatedNewDatabase(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Restored fixture", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err = store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restoreRoot := t.TempDir()
	restoredPath := filepath.Join(restoreRoot, "restored.db")
	version, err := RestoreDatabaseBackup(ctx, backup, restoredPath)
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("restore = version %d, %v", version, err)
	}
	info, err := os.Lstat(restoredPath)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("restored file = %#v, %v", info, err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetGame(ctx, game.ID, "")
	if err != nil || got.DefaultTitle != game.DefaultTitle {
		t.Fatalf("restored game = %#v, %v", got, err)
	}
}

func TestRestoreDatabaseBackupNeverOverwritesOrLeavesFailedOutput(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	existing := filepath.Join(root, "existing.db")
	if err := os.WriteFile(existing, []byte("user-owned-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreDatabaseBackup(ctx, backup, existing); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing restore output was accepted: %v", err)
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "user-owned-content" {
		t.Fatalf("existing output changed: %q, %v", contents, err)
	}

	corrupt := filepath.Join(root, "corrupt.db")
	if err = os.WriteFile(corrupt, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	failedOutput := filepath.Join(root, "failed-output.db")
	if _, err = RestoreDatabaseBackup(ctx, corrupt, failedOutput); err == nil {
		t.Fatal("corrupt backup was accepted")
	}
	if _, err = os.Lstat(failedOutput); !os.IsNotExist(err) {
		t.Fatalf("failed restore left an output: %v", err)
	}
}

func TestRestoreDatabaseBackupRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	ctx := context.Background()
	store := testStore(t)
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	linkedBackup := filepath.Join(root, "linked-backup.db")
	if err := os.Symlink(backup, linkedBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreDatabaseBackup(ctx, linkedBackup, filepath.Join(root, "from-link.db")); err == nil {
		t.Fatal("symbolic-link backup was accepted")
	}
	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreDatabaseBackup(ctx, backup, filepath.Join(linkedParent, "restored.db")); err == nil {
		t.Fatal("symbolic-link output parent was accepted")
	}
	if entries, err := os.ReadDir(realParent); err != nil || len(entries) != 0 {
		t.Fatalf("symlink rejection wrote outside target: %d entries, %v", len(entries), err)
	}
}

func TestOpenReadOnlyDoesNotMigrateWriteOrCreateSidecars(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "readonly.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateGame(ctx, NewGame{DefaultTitle: "Preserved", Platform: "gba"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err = os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected sidecar before check %s: %v", suffix, err)
		}
	}
	readonly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := readonly.SchemaVersion(ctx)
	if err != nil || version != CurrentSchemaVersion {
		readonly.Close()
		t.Fatalf("readonly schema = %d, %v", version, err)
	}
	if _, err = readonly.CreateGame(ctx, NewGame{DefaultTitle: "Must fail", Platform: "gba"}); err == nil {
		readonly.Close()
		t.Fatal("read-only store accepted a write")
	}
	if err = readonly.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err = os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read-only check created sidecar %s: %v", suffix, err)
		}
	}
}
