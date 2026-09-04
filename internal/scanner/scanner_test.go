package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

func newStore(t *testing.T) *catalog.Store {
	t.Helper()
	store, err := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestScanHashesWithoutChangingFile(t *testing.T) {
	root := t.TempDir()
	romDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rom := filepath.Join(romDir, "Demo.gba")
	before := []byte("non-playable fixture")
	if err := os.WriteFile(rom, before, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := Scan(context.Background(), store, root, romDir, "gba")
	if err != nil {
		t.Fatal(err)
	}
	if r.Imported != 1 {
		t.Fatalf("result=%+v", r)
	}
	a, err := store.ArtifactByPath(context.Background(), "gba/Demo.gba")
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == "" {
		t.Fatal("missing SHA-256")
	}
	after, err := os.ReadFile(rom)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("scanner changed ROM contents")
	}
}

func TestScanRejectsOutsideLibrary(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	store, err := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = Scan(context.Background(), store, root, outside, "gba"); err == nil {
		t.Fatal("expected outside-library error")
	}
}

func TestDiscoverRejectsSymlinkedROMWithoutReadingTarget(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	romDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "private.gba")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(romDir, "linked.gba")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := newStore(t)
	if _, err := Discover(context.Background(), store, root, romDir, "gba"); err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), outside) {
		t.Fatalf("symlinked ROM error=%v", err)
	}
}

func TestDiscoverPreviewsWithoutImportingAndReportsDuplicates(t *testing.T) {
	root := t.TempDir()
	romDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(romDir, "Preview.gba"), []byte("preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	candidates, err := Discover(context.Background(), store, root, romDir, "gba")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Duplicate || candidates[0].Game.Artifacts[0].SHA256 == "" {
		t.Fatalf("unexpected preview: %#v", candidates)
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil || len(games) != 0 {
		t.Fatalf("preview mutated catalog: games=%#v err=%v", games, err)
	}
	if _, err = Scan(context.Background(), store, root, romDir, "gba"); err != nil {
		t.Fatal(err)
	}
	candidates, err = Discover(context.Background(), store, root, romDir, "gba")
	if err != nil || len(candidates) != 1 || !candidates[0].Duplicate {
		t.Fatalf("duplicate not reported: %#v err=%v", candidates, err)
	}
	otherDir := filepath.Join(root, "other")
	if err = os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(otherDir, "Same bytes.gba"), []byte("preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates, err = Discover(context.Background(), store, root, otherDir, "gba")
	if err != nil || len(candidates) != 1 || !candidates[0].Duplicate || !strings.Contains(candidates[0].Reason, "相同内容") {
		t.Fatalf("content duplicate not reported: %#v err=%v", candidates, err)
	}
}

func TestScanGroupsCueAndTracksIntoOneEdition(t *testing.T) {
	root := t.TempDir()
	psx := filepath.Join(root, "psx")
	if err := os.MkdirAll(psx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(psx, "Game.bin"), []byte("track"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(psx, "Game.cue"), []byte("FILE \"Game.bin\" BINARY\n  TRACK 01 MODE2/2352\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := Scan(context.Background(), store, root, psx, "psx")
	if err != nil {
		t.Fatal(err)
	}
	if result.Found != 1 || result.Imported != 1 {
		t.Fatalf("unexpected scan result: %#v", result)
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || len(games[0].Editions) != 1 || len(games[0].Editions[0].Artifacts) != 2 {
		t.Fatalf("cue was not grouped: %#v", games)
	}
}

func TestScanGroupsM3UAndNestedCueReferences(t *testing.T) {
	root := t.TempDir()
	psx := filepath.Join(root, "psx")
	if err := os.MkdirAll(psx, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Disc 1", "Disc 2"} {
		if err := os.WriteFile(filepath.Join(psx, name+".bin"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(psx, name+".cue"), []byte("FILE \""+name+".bin\" BINARY\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(psx, "Game.m3u"), []byte("Disc 1.cue\nDisc 2.cue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	defer store.Close()
	result, err := Scan(context.Background(), store, root, psx, "psx")
	if err != nil {
		t.Fatal(err)
	}
	games, _ := store.ListGames(context.Background(), "")
	if result.Imported != 1 || len(games[0].Editions[0].Artifacts) != 5 {
		t.Fatalf("nested playlist was not grouped: result=%#v games=%#v", result, games)
	}
}

func TestDiscoverDirectoryPlatformUsesTopLevelFoldersAsArtifacts(t *testing.T) {
	root := t.TempDir()
	scanRoot := filepath.Join(root, "ps3")
	gameRoot := filepath.Join(scanRoot, "Example Game")
	if err := os.MkdirAll(filepath.Join(gameRoot, "PS3_GAME", "USRDIR"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameRoot, "PS3_GAME", "PARAM.SFO"), []byte("parameter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameRoot, "PS3_GAME", "USRDIR", "EBOOT.BIN"), []byte("executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(scanRoot, ".metadata", "not-a-game"), 0o755); err != nil {
		t.Fatal(err)
	}
	expectedHash, expectedSize, err := filehash.Directory(gameRoot)
	if err != nil {
		t.Fatal(err)
	}
	store := newStore(t)
	candidates, err := Discover(context.Background(), store, root, scanRoot, "ps3")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Game.DefaultTitle != "Example Game" || len(candidates[0].Game.Artifacts) != 1 {
		t.Fatalf("directory candidates=%#v", candidates)
	}
	artifact := candidates[0].Game.Artifacts[0]
	if artifact.Path != "ps3/Example Game" || artifact.Role != "rom" || artifact.Missing || artifact.SHA256 != expectedHash || artifact.Size != expectedSize {
		t.Fatalf("directory artifact=%#v", artifact)
	}
}

func TestDiscoverDirectoryPlatformRejectsNestedSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	scanRoot := filepath.Join(root, "ps3")
	gameRoot := filepath.Join(scanRoot, "Linked Game")
	if err := os.MkdirAll(gameRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.bin")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(gameRoot, "EBOOT.BIN")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := newStore(t)
	if _, err := Discover(context.Background(), store, root, scanRoot, "ps3"); err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), outside) {
		t.Fatalf("nested symlink error=%v", err)
	}
}
