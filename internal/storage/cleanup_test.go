package storage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"varkiv/internal/catalog"
)

func writeCleanupFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cleanupRepository(t *testing.T) (*Repository, string) {
	t.Helper()
	root := t.TempDir()
	library, state := filepath.Join(root, "library"), filepath.Join(root, "state")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	repository, err := New(library, state)
	if err != nil {
		t.Fatal(err)
	}
	return repository, state
}

func TestManagedCleanupQuarantinesOnlyOrphansAndRestores(t *testing.T) {
	repository, state := cleanupRepository(t)
	writeCleanupFixture(t, filepath.Join(repository.ROMRoot, "gba", "kept", "game.gba"), "kept rom")
	writeCleanupFixture(t, filepath.Join(repository.ROMRoot, "gba", "orphan", "old.gba"), "orphan rom")
	writeCleanupFixture(t, filepath.Join(repository.MediaRoot, "blobs", "sha256", "aa", "kept.png"), "kept media")
	writeCleanupFixture(t, filepath.Join(repository.MediaRoot, "blobs", "sha256", "bb", "orphan.png"), "orphan media")
	writeCleanupFixture(t, filepath.Join(repository.MediaRoot, ".staging", "active-upload"), "staging")
	writeCleanupFixture(t, filepath.Join(repository.MediaRoot, "cache", "thumbnails-v1", "aa", "thumb.png"), "thumbnail cache")

	plan, err := repository.PreviewManagedCleanup(context.Background(), catalog.ManagedStorageReferences{
		ROM: []string{"gba/kept"}, Media: []string{"blobs/sha256/aa/kept.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 2 || plan.TotalBytes != int64(len("orphan rom")+len("orphan media")) || plan.Fingerprint == "" {
		t.Fatalf("unexpected cleanup plan: %#v", plan)
	}
	run, err := repository.QuarantineManagedCleanup(context.Background(), plan.Fingerprint, plan.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "quarantined" || run.ItemCount != 2 {
		t.Fatalf("unexpected cleanup run: %#v", run)
	}
	for _, item := range plan.Candidates {
		path, pathErr := repository.candidateSource(item)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("candidate was not moved: %s", item.RelativePath)
		}
	}
	if _, err = os.Stat(filepath.Join(repository.ROMRoot, "gba", "kept", "game.gba")); err != nil {
		t.Fatal("referenced ROM was touched", err)
	}
	if _, err = os.Stat(filepath.Join(repository.MediaRoot, ".staging", "active-upload")); err != nil {
		t.Fatal("active staging file was touched", err)
	}
	if _, err = os.Stat(filepath.Join(repository.MediaRoot, "cache", "thumbnails-v1", "aa", "thumb.png")); err != nil {
		t.Fatal("rebuildable media cache was touched", err)
	}
	if info, err := os.Stat(filepath.Join(state, "recovery", "managed-storage", run.ID, cleanupManifestName)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("private recovery manifest mode=%v err=%v", info, err)
	}

	history, err := repository.ListCleanupRuns(context.Background())
	if err != nil || len(history) != 1 || history[0].ID != run.ID {
		t.Fatalf("cleanup history=%#v err=%v", history, err)
	}
	restored, err := repository.RestoreCleanupRun(context.Background(), run.ID)
	if err != nil || restored.Status != "restored" {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	for _, item := range plan.Candidates {
		path, _ := repository.candidateSource(item)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("candidate was not restored: %s: %v", item.RelativePath, statErr)
		}
	}
	second, err := repository.RestoreCleanupRun(context.Background(), run.ID)
	if err != nil || second.Status != "restored" {
		t.Fatalf("idempotent restore=%#v err=%v", second, err)
	}
}

func TestManagedCleanupDriftRollsBackEarlierMoves(t *testing.T) {
	repository, _ := cleanupRepository(t)
	first := filepath.Join(repository.ROMRoot, "gba", "a.gba")
	second := filepath.Join(repository.ROMRoot, "gba", "b.gba")
	writeCleanupFixture(t, first, "a")
	writeCleanupFixture(t, second, "b")
	plan, err := repository.PreviewManagedCleanup(context.Background(), catalog.ManagedStorageReferences{})
	if err != nil || len(plan.Candidates) != 2 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	if err = os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.QuarantineManagedCleanup(context.Background(), plan.Fingerprint, plan.Candidates); err == nil {
		t.Fatal("drifted cleanup unexpectedly succeeded")
	}
	if _, err = os.Stat(first); err != nil {
		t.Fatal("earlier move was not rolled back", err)
	}
	history, err := repository.ListCleanupRuns(context.Background())
	if err != nil || len(history) != 0 {
		t.Fatalf("failed cleanup left a completed operation: %#v %v", history, err)
	}
}

func TestManagedCleanupRejectsSymlinksAndRestoreCollisions(t *testing.T) {
	repository, _ := cleanupRepository(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeCleanupFixture(t, outside, "private")
	if err := os.Symlink(outside, filepath.Join(repository.ROMRoot, "link.gba")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := repository.PreviewManagedCleanup(context.Background(), catalog.ManagedStorageReferences{}); err == nil {
		t.Fatal("symlink cleanup unexpectedly succeeded")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "private" {
		t.Fatalf("outside target changed: %q %v", data, err)
	}
	if err := os.Remove(filepath.Join(repository.ROMRoot, "link.gba")); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(repository.ROMRoot, "orphan.gba")
	writeCleanupFixture(t, orphan, "original")
	plan, err := repository.PreviewManagedCleanup(context.Background(), catalog.ManagedStorageReferences{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.QuarantineManagedCleanup(context.Background(), plan.Fingerprint, plan.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	writeCleanupFixture(t, orphan, "replacement")
	if _, err = repository.RestoreCleanupRun(context.Background(), run.ID); err == nil {
		t.Fatal("restore collision unexpectedly overwrote a file")
	}
	data, err := os.ReadFile(orphan)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("restore collision changed destination: %q %v", data, err)
	}
}
