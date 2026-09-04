package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

func TestIngestGameCopiesROMSetAndDeduplicatesMedia(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(root, "psx", "Game"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"Game.cue": []byte(`FILE "Game.bin" BINARY`), "Game.bin": []byte("disc-data"), "cover-a.png": []byte("same-media"), "cover-b.png": []byte("same-media")} {
		if err := os.WriteFile(filepath.Join(root, "psx", "Game", name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	game := catalog.ImportedGame{Platform: "psx", EditionTitle: "Game", Artifacts: []catalog.NewArtifact{{Path: "psx/Game/Game.cue", Role: "rom"}, {Path: "psx/Game/Game.bin", Role: "disc"}}, Media: []catalog.NewMediaAsset{{Path: "psx/Game/cover-a.png", Kind: "cover"}, {Path: "psx/Game/cover-b.png", Kind: "cover"}}}
	result, err := repo.IngestGame(context.Background(), game, "copy", "copy")
	if err != nil {
		t.Fatal(err)
	}
	if result.ROMFiles != 2 || result.MediaFiles != 2 || result.Game.Artifacts[0].StorageKind != "managed" {
		t.Fatalf("bad ingest result: %#v", result)
	}
	if filepath.Dir(result.Game.Artifacts[0].Path) != filepath.Dir(result.Game.Artifacts[1].Path) {
		t.Fatalf("multi-file relative layout was not preserved: %#v", result.Game.Artifacts)
	}
	if result.Game.Media[0].Path != result.Game.Media[1].Path || result.Game.Media[0].SHA256 == "" {
		t.Fatalf("identical media was not deduplicated: %#v", result.Game.Media)
	}
	for _, item := range result.Game.Artifacts {
		path, resolveErr := repo.ResolveArtifact(catalog.Artifact{Path: item.Path, StorageKind: item.StorageKind})
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatal(statErr)
		}
	}
	if _, err = os.Stat(filepath.Join(root, "psx", "Game", "Game.bin")); err != nil {
		t.Fatalf("source ROM was modified: %v", err)
	}
}

func TestIngestGameRejectsSymlink(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.rom")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.rom")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.IngestGame(context.Background(), catalog.ImportedGame{Platform: "custom", Artifacts: []catalog.NewArtifact{{Path: "linked.rom"}}}, "copy", "ignore")
	if err == nil {
		t.Fatal("expected symlink import rejection")
	}
}

func TestIngestDirectoryKeepsCanonicalIdentity(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	directory := filepath.Join(root, "ps3", "Game")
	if err := os.MkdirAll(filepath.Join(directory, "PS3_GAME", "USRDIR"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "PS3_GAME", "PARAM.SFO"), []byte("parameter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "PS3_GAME", "USRDIR", "EBOOT.BIN"), []byte("executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceHash, sourceSize, err := filehash.Directory(directory)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.IngestGame(context.Background(), catalog.ImportedGame{Platform: "ps3", EditionID: "directory-edition", Artifacts: []catalog.NewArtifact{{Path: "ps3/Game", Role: "rom", SHA256: sourceHash, Size: sourceSize}}}, "copy", "ignore")
	if err != nil {
		t.Fatal(err)
	}
	artifact := result.Game.Artifacts[0]
	if artifact.SHA256 != sourceHash || artifact.Size != sourceSize || result.ROMFiles != 2 {
		t.Fatalf("managed directory identity=%#v result=%#v", artifact, result)
	}
	managed, err := repo.ResolveArtifact(catalog.Artifact{Path: artifact.Path, StorageKind: artifact.StorageKind})
	if err != nil {
		t.Fatal(err)
	}
	managedHash, managedSize, err := filehash.Directory(managed)
	if err != nil || managedHash != sourceHash || managedSize != sourceSize {
		t.Fatalf("managed hash=%q/%d source=%q/%d err=%v", managedHash, managedSize, sourceHash, sourceSize, err)
	}
}

func TestManagedIngestRejectsSymlinkParentsAndUnsafeEditionIdentity(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo.ROMRoot, "gba")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = repo.IngestGame(context.Background(), catalog.ImportedGame{Platform: "gba", EditionID: "../../escape", Artifacts: []catalog.NewArtifact{{Path: "gba/game.gba", Role: "rom"}}}, "copy", "ignore")
	if err == nil {
		t.Fatal("symlinked managed parent was accepted")
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("outside target changed: entries=%d err=%v", len(entries), readErr)
	}
	if err = os.Remove(filepath.Join(repo.ROMRoot, "gba")); err != nil {
		t.Fatal(err)
	}
	result, err := repo.IngestGame(context.Background(), catalog.ImportedGame{Platform: "gba", EditionID: "../../escape", Artifacts: []catalog.NewArtifact{{Path: "gba/game.gba", Role: "rom"}}}, "copy", "ignore")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Game.Artifacts[0].Path, "..") {
		t.Fatalf("unsafe managed path=%q", result.Game.Artifacts[0].Path)
	}
}

func TestStoreMediaRejectsSymlinkedBlobPrefix(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("private-media")
	digest := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(digest[:])
	prefix := filepath.Join(repo.MediaRoot, "blobs", "sha256", hexDigest[:2])
	outside := t.TempDir()
	if err = os.Symlink(outside, prefix); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, _, _, err = repo.StoreMedia(context.Background(), "cover.png", "image/png", strings.NewReader(string(body))); err == nil {
		t.Fatal("symlinked media blob prefix was accepted")
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("outside media target changed: entries=%d err=%v", len(entries), readErr)
	}
}

func TestStoreMediaRejectsCorruptExistingBlobWithoutReplacingIt(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	body := "expected-media"
	relative, _, _, _, err := repo.StoreMedia(context.Background(), "cover.png", "image/png", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo.MediaRoot, filepath.FromSlash(relative))
	corrupt := strings.Repeat("x", len(body))
	if err = os.WriteFile(target, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err = repo.StoreMedia(context.Background(), "cover.png", "image/png", strings.NewReader(body)); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("corrupt content-addressed media was accepted: %v", err)
	}
	remaining, readErr := os.ReadFile(target)
	if readErr != nil || string(remaining) != corrupt {
		t.Fatalf("corrupt blob was silently replaced: %q %v", remaining, readErr)
	}
}

func TestIngestGameNeverOverwritesExistingManagedEdition(t *testing.T) {
	root, state := filepath.Join(t.TempDir(), "library"), filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "game.rom"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := New(root, state)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(repo.ROMRoot, "custom", "fixed-id")
	if err = os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "keep.rom")
	if err = os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = repo.IngestGame(context.Background(), catalog.ImportedGame{Platform: "custom", EditionID: "fixed-id", Artifacts: []catalog.NewArtifact{{Path: "game.rom"}}}, "copy", "ignore")
	if err == nil {
		t.Fatal("expected existing destination rejection")
	}
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing managed data changed: %q %v", data, readErr)
	}
}
