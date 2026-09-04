package deviceagent

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
	"varkiv/internal/server"
)

func TestROMInventoryCacheDetectsSameMetadataContentDrift(t *testing.T) {
	root := t.TempDir()
	romRoot := filepath.Join(root, "gba")
	if err := os.MkdirAll(romRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(romRoot, "Fixture.gba")
	fixedTime := time.Unix(1_700_000_000, 123)
	if err := os.WriteFile(romPath, []byte("AAAA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(romPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	config := Config{ROMRoots: map[string]string{"gba": romRoot}, ROMCache: map[string]ROMCacheEntry{}}
	first, err := enumerateROMInventory(context.Background(), &config)
	if err != nil || len(first) != 1 {
		t.Fatalf("first inventory=%#v err=%v", first, err)
	}
	firstCache := config.ROMCache[first[0].ClientItemID]
	if firstCache.Kind != "file" || firstCache.Signal == "" || firstCache.VerifiedAt == 0 {
		t.Fatalf("file cache lacks verification metadata: %#v", firstCache)
	}
	poisoned := firstCache
	poisoned.SHA256 = strings.Repeat("z", 64)
	poisoned.VerifiedAt = time.Now().Add(48 * time.Hour).Unix()
	config.ROMCache[first[0].ClientItemID] = poisoned
	revalidated, err := enumerateROMInventory(context.Background(), &config)
	if err != nil || len(revalidated) != 1 || revalidated[0].SHA256 != first[0].SHA256 || config.ROMCache[first[0].ClientItemID].VerifiedAt > time.Now().Unix() {
		t.Fatalf("invalid or future-dated cache entry was trusted: %#v err=%v", revalidated, err)
	}
	if err = os.WriteFile(romPath, []byte("BBBB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(romPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	second, err := enumerateROMInventory(context.Background(), &config)
	if err != nil || len(second) != 1 {
		t.Fatalf("second inventory=%#v err=%v", second, err)
	}
	if second[0].SHA256 == first[0].SHA256 || config.ROMCache[second[0].ClientItemID].Signal == firstCache.Signal {
		t.Fatal("same-size content drift with a restored mtime reused the stale ROM identity")
	}
}

func TestROMInventorySupportsDirectoryGamesWithoutLeakingNames(t *testing.T) {
	root := t.TempDir()
	romRoot := filepath.Join(root, "ps3")
	gameRoot := filepath.Join(romRoot, "Private Fixture Game")
	contentRoot := filepath.Join(gameRoot, "PS3_GAME", "USRDIR")
	if err := os.MkdirAll(contentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	parameterPath := filepath.Join(gameRoot, "PS3_GAME", "PARAM.SFO")
	executablePath := filepath.Join(contentRoot, "EBOOT.BIN")
	if err := os.WriteFile(parameterPath, []byte("parameter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("EXEC-A"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(romRoot, ".metadata", "not-a-game"), 0o700); err != nil {
		t.Fatal(err)
	}
	expectedHash, expectedSize, err := filehash.Directory(gameRoot)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{ROMRoots: map[string]string{"ps3": romRoot}, ROMCache: map[string]ROMCacheEntry{}}
	first, err := enumerateROMInventory(context.Background(), &config)
	if err != nil || len(first) != 1 {
		t.Fatalf("directory inventory=%#v err=%v", first, err)
	}
	if first[0].PlatformID != "ps3" || first[0].SHA256 != expectedHash || first[0].Size != expectedSize || len(first[0].ClientItemID) != 64 || strings.Contains(first[0].ClientItemID, "Private") {
		t.Fatalf("directory identity is incomplete or non-private: %#v", first[0])
	}
	entry := config.ROMCache[first[0].ClientItemID]
	if entry.Kind != "directory" || entry.Signal == "" || entry.VerifiedAt == 0 {
		t.Fatalf("directory cache lacks verification metadata: %#v", entry)
	}
	fixedTime := time.Unix(1_700_000_000, 456)
	if err = os.Chtimes(executablePath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(executablePath, []byte("EXEC-B"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(executablePath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	second, err := enumerateROMInventory(context.Background(), &config)
	if err != nil || len(second) != 1 || second[0].SHA256 == first[0].SHA256 {
		t.Fatalf("directory content drift was not re-identified: %#v err=%v", second, err)
	}
}

func TestDirectoryROMInventoryMatchesEditionThroughSyncSession(t *testing.T) {
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "library")
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(library, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app, err := server.New(store, library, server.WithStateRoot(state), server.WithToken("admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Directory Game", Platform: "ps3"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Directory Game", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	deviceRoot := filepath.Join(t.TempDir(), "device")
	romRoot := filepath.Join(deviceRoot, "roms", "ps3")
	gameRoot := filepath.Join(romRoot, "Private Directory Fixture")
	if err = os.MkdirAll(filepath.Join(gameRoot, "PS3_GAME", "USRDIR"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(gameRoot, "PS3_GAME", "PARAM.SFO"), []byte("parameter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(gameRoot, "PS3_GAME", "USRDIR", "EBOOT.BIN"), []byte("executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := filehash.Directory(gameRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "ps3/Private Directory Fixture", SHA256: digest, Size: size}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "agent.json")
	config := pairTestAgent(t, httpServer.URL, app.Handler(), deviceRoot, configPath, "Directory fixture device")
	config.ROMRoots = map[string]string{"ps3": romRoot}
	if err = UpdateConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	result, err := SyncOnce(ctx, configPath)
	if err != nil || result.SessionID == "" || result.Status != "complete" {
		t.Fatalf("directory inventory sync=%#v err=%v", result, err)
	}
	inventory, err := store.ListInventoryItems(ctx, result.SessionID)
	if err != nil || len(inventory) != 1 || inventory[0].MatchStatus != "matched" || inventory[0].MatchedEditionID != edition.ID || strings.Contains(inventory[0].ClientItemID, "Private") {
		t.Fatalf("directory inventory did not match privately: %#v err=%v", inventory, err)
	}
}
