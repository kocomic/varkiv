package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	storagex "varkiv/internal/storage"
)

func managedCleanupServer(t *testing.T) (*catalog.Store, http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	library, state := filepath.Join(root, "library"), filepath.Join(root, "state")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(root, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app, err := New(store, library, WithStateRoot(state))
	if err != nil {
		t.Fatal(err)
	}
	return store, app.Handler(), state
}

func TestManagedStorageCleanupAPIPreviewCommitAndRestore(t *testing.T) {
	store, handler, state := managedCleanupServer(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Kept", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Kept", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	keptRelative := "gba/kept/game.gba"
	kept := filepath.Join(state, "roms", filepath.FromSlash(keptRelative))
	orphan := filepath.Join(state, "roms", "gba", "orphan", "private-name.gba")
	for path, value := range map[string]string{kept: "kept", orphan: "orphan"} {
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: keptRelative, StorageKind: "managed", Role: "rom", Size: 4, SHA256: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}

	var preview managedCleanupPreview
	jsonRequest(t, handler, http.MethodPost, "/api/v1/storage-cleanup/preview", map[string]any{}, &preview)
	if preview.PreviewToken == "" || len(preview.Candidates) != 1 || preview.Candidates[0].RelativePath != "gba/orphan/private-name.gba" {
		t.Fatalf("cleanup preview=%#v", preview)
	}
	var run storagex.CleanupRun
	status := jsonRequest(t, handler, http.MethodPost, "/api/v1/storage-cleanup/commit", managedCleanupCommitRequest{PreviewToken: preview.PreviewToken, SelectedIDs: []string{preview.Candidates[0].ID}}, &run)
	if status != http.StatusCreated || run.Status != "quarantined" || run.ItemCount != 1 {
		t.Fatalf("cleanup run=%#v status=%d", run, status)
	}
	if _, err = os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan still active: %v", err)
	}
	if _, err = os.Stat(kept); err != nil {
		t.Fatalf("referenced file was moved: %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/storage-cleanup/runs", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private-name.gba") || strings.Contains(response.Body.String(), state) {
		t.Fatalf("cleanup history leaked a path: %d %s", response.Code, response.Body.String())
	}
	var envelope collectionEnvelope[storagex.CleanupRun]
	if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Data) != 1 {
		t.Fatalf("cleanup history=%#v err=%v", envelope, err)
	}

	var restored storagex.CleanupRun
	jsonRequest(t, handler, http.MethodPost, "/api/v1/storage-cleanup/runs/"+run.ID+"/restore", map[string]any{}, &restored)
	if restored.Status != "restored" {
		t.Fatalf("restored=%#v", restored)
	}
	if data, err := os.ReadFile(orphan); err != nil || string(data) != "orphan" {
		t.Fatalf("orphan was not restored: %q %v", data, err)
	}
}

func TestManagedStorageCleanupRejectsTokenAndCatalogDriftAtomically(t *testing.T) {
	store, handler, state := managedCleanupServer(t)
	orphan := filepath.Join(state, "media", "blobs", "sha256", "aa", "orphan.png")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	var preview managedCleanupPreview
	jsonRequest(t, handler, http.MethodPost, "/api/v1/storage-cleanup/preview", map[string]any{}, &preview)
	tampered := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/storage-cleanup/commit", managedCleanupCommitRequest{PreviewToken: "tampered", SelectedIDs: []string{preview.Candidates[0].ID}})
	if tampered.Code != http.StatusConflict || !strings.Contains(tampered.Body.String(), "managed_cleanup_stale") {
		t.Fatalf("tampered cleanup=%d %s", tampered.Code, tampered.Body.String())
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("tampered cleanup moved a file", err)
	}
	game, _ := store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Late reference", Platform: "gba"})
	edition, _ := store.AddEdition(context.Background(), catalog.NewEdition{GameID: game.ID, DefaultTitle: "Late reference", EditionType: "original"})
	if _, err := store.AddMedia(context.Background(), catalog.NewMediaAsset{EditionID: edition.ID, Kind: "cover", StorageKind: "managed", Path: "blobs/sha256/aa/orphan.png", OriginalName: "cover.png", Size: 5, SHA256: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	drift := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/storage-cleanup/commit", managedCleanupCommitRequest{PreviewToken: preview.PreviewToken, SelectedIDs: []string{preview.Candidates[0].ID}})
	if drift.Code != http.StatusConflict || !strings.Contains(drift.Body.String(), "managed_cleanup_stale") {
		t.Fatalf("catalog drift cleanup=%d %s", drift.Code, drift.Body.String())
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("catalog drift moved a newly referenced file", err)
	}
	runs, err := filepath.Glob(filepath.Join(state, "recovery", "managed-storage", "*"))
	if err != nil || len(runs) != 0 {
		t.Fatalf("rejected cleanup left recovery operations: %v %v", runs, err)
	}
}
