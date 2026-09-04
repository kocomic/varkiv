package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryMatchConfirmationBindsIdentityAndCandidateSet(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	gameA, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Original", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	editionA, err := store.AddEdition(ctx, NewEdition{GameID: gameA.ID, DefaultTitle: "Original", EditionType: "original", Serial: "AGB-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	gameB, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Translation", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	editionB, err := store.AddEdition(ctx, NewEdition{GameID: gameB.ID, DefaultTitle: "Chinese translation", EditionType: "translation", Serial: "AGB-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDevice(ctx, NewDevice{Name: "Fixture handheld", OSFamily: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}

	original := NewInventoryItem{ID: "inventory-one", ClientItemID: strings.Repeat("c", 64), PlatformID: "gba", Serial: "AGB-TEST", Size: 4096}
	matched, err := store.MatchInventoryItem(ctx, original)
	if err != nil || matched.MatchStatus != "ambiguous" || matched.MatchMethod != "serial" {
		t.Fatalf("initial match=%#v err=%v", matched, err)
	}
	session, _, err := store.CreateSyncSession(ctx, NewSyncSession{
		DeviceID: device.ID, IdempotencyKey: "inventory-confirm-one", Inventory: []NewInventoryItem{matched},
	})
	if err != nil {
		t.Fatal(err)
	}
	review, candidates, err := store.ReviewInventoryMatch(ctx, session.ID, original.ID)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("review=%#v candidates=%v err=%v", review, candidates, err)
	}
	identityHash, err := InventoryIdentityHash(original)
	if err != nil {
		t.Fatal(err)
	}
	override, err := store.ConfirmInventoryMatchOverride(ctx, NewInventoryMatchOverride{
		DeviceID: device.ID, ClientItemID: original.ClientItemID, PlatformID: original.PlatformID,
		IdentityHash: identityHash, CandidateIDs: candidates, EditionID: editionB.ID, MatchMethod: "serial",
		SourceSessionID: session.ID, SourceInventoryItemID: original.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if override.CandidateHash == "" || override.EditionID != editionB.ID {
		t.Fatalf("confirmation omitted candidate binding: %#v", override)
	}
	encoded, err := json.Marshal(override)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{original.ClientItemID, identityHash, override.CandidateHash} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("private inventory identity leaked in JSON: %s", encoded)
		}
	}

	confirmed, err := store.MatchInventoryItemForDevice(ctx, device.ID, original)
	if err != nil || confirmed.MatchStatus != "matched" || confirmed.MatchedEditionID != editionB.ID || confirmed.MatchMethod != "confirmed_serial" {
		t.Fatalf("confirmation not reused: %#v err=%v", confirmed, err)
	}
	changedIdentity := original
	changedIdentity.Size++
	changed, err := store.MatchInventoryItemForDevice(ctx, device.ID, changedIdentity)
	if err != nil || changed.MatchStatus != "ambiguous" {
		t.Fatalf("changed identity reused confirmation: %#v err=%v", changed, err)
	}

	uniqueHash := strings.Repeat("a", 64)
	if _, err = store.AddArtifact(ctx, NewArtifact{EditionID: editionA.ID, Path: "fixture/original.gba", SHA256: uniqueHash, Size: original.Size}); err != nil {
		t.Fatal(err)
	}
	unique := original
	unique.SHA256 = uniqueHash
	uniqueMatch, err := store.MatchInventoryItemForDevice(ctx, device.ID, unique)
	if err != nil || uniqueMatch.MatchedEditionID != editionA.ID || uniqueMatch.MatchMethod != "sha256" {
		t.Fatalf("unique SHA did not win over manual confirmation: %#v err=%v", uniqueMatch, err)
	}

	second := original
	second.ID = "inventory-two"
	second.ClientItemID = strings.Repeat("d", 64)
	secondMatched, err := store.MatchInventoryItem(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	secondSession, _, err := store.CreateSyncSession(ctx, NewSyncSession{
		DeviceID: device.ID, IdempotencyKey: "inventory-confirm-two", Inventory: []NewInventoryItem{secondMatched},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, oldCandidates, err := store.ReviewInventoryMatch(ctx, secondSession.ID, second.ID)
	if err != nil || len(oldCandidates) != 2 {
		t.Fatalf("second review candidates=%v err=%v", oldCandidates, err)
	}
	gameC, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Revision", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddEdition(ctx, NewEdition{GameID: gameC.ID, DefaultTitle: "Revision", EditionType: "hack", Serial: "AGB-TEST"}); err != nil {
		t.Fatal(err)
	}
	secondIdentity, err := InventoryIdentityHash(second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ConfirmInventoryMatchOverride(ctx, NewInventoryMatchOverride{
		DeviceID: device.ID, ClientItemID: second.ClientItemID, PlatformID: second.PlatformID,
		IdentityHash: secondIdentity, CandidateIDs: oldCandidates, EditionID: editionA.ID, MatchMethod: "serial",
		SourceSessionID: secondSession.ID, SourceInventoryItemID: second.ID,
	})
	if !errors.Is(err, ErrInventoryMatchStale) {
		t.Fatalf("candidate drift was not rejected: %v", err)
	}
	staleOverride, err := store.MatchInventoryItemForDevice(ctx, device.ID, original)
	if err != nil || staleOverride.MatchStatus != "ambiguous" {
		t.Fatalf("changed candidate set reused old confirmation: %#v err=%v", staleOverride, err)
	}
}

func TestMigrationFromV19AddsInventoryMatchOverridesWithoutTouchingLibraryFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "from-v19.db")
	romPath := filepath.Join(root, "library", "gba", "do-not-read.gba")
	mediaPath := filepath.Join(root, "media", "do-not-read.png")
	for target, contents := range map[string]string{romPath: "fixture-rom-bytes", mediaPath: "fixture-media-bytes"} {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Preserved", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original", Serial: "PRESERVE"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDevice(ctx, NewDevice{Name: "Preserved device", OSFamily: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := store.CreateSyncSession(ctx, NewSyncSession{
		DeviceID: device.ID, IdempotencyKey: "preserved-session",
		Inventory: []NewInventoryItem{{ID: "preserved-inventory", ClientItemID: "opaque-client-item", PlatformID: "gba", Serial: "PRESERVE", Size: 17, MatchStatus: "matched", MatchedEditionID: edition.ID, MatchMethod: "serial"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TABLE inventory_match_overrides; PRAGMA user_version=19`); err != nil {
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
	version, err := migrated.SchemaVersion(ctx)
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	gotSession, err := migrated.GetSyncSession(ctx, session.ID)
	if err != nil || gotSession.DeviceID != device.ID {
		t.Fatalf("session not preserved: %#v err=%v", gotSession, err)
	}
	items, err := migrated.ListInventoryItems(ctx, session.ID)
	if err != nil || len(items) != 1 || items[0].MatchedEditionID != edition.ID || items[0].ClientItemID != "opaque-client-item" {
		t.Fatalf("inventory not preserved: %#v err=%v", items, err)
	}
	var candidateHashColumn string
	if err = migrated.db.QueryRow(`SELECT name FROM pragma_table_info('inventory_match_overrides') WHERE name='candidate_hash'`).Scan(&candidateHashColumn); err != nil || candidateHashColumn != "candidate_hash" {
		t.Fatalf("candidate-set binding column missing: %q err=%v", candidateHashColumn, err)
	}
	for target, expected := range map[string]string{romPath: "fixture-rom-bytes", mediaPath: "fixture-media-bytes"} {
		contents, readErr := os.ReadFile(target)
		if readErr != nil || string(contents) != expected {
			t.Fatalf("migration changed library file %s: %q err=%v", target, contents, readErr)
		}
	}
}
