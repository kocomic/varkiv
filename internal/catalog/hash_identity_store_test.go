package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"varkiv/internal/hashpack"
)

func TestHashPackReferenceImportDoesNotCreateLibraryRows(t *testing.T) {
	ctx := context.Background()
	sourceStore, err := Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sourceStore.Close()
	game, err := sourceStore.CreateGame(ctx, NewGame{DefaultTitle: "Advance Fixture", Platform: "gba", Titles: map[string]string{"zh-CN": "高级战争测试"}})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := sourceStore.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Translation", EditionType: "translation", Languages: []string{"zh-CN"}, Titles: map[string]string{"en": "Translation"}})
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	if _, err = sourceStore.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: "gba/fixture.gba", Role: "rom", Size: 4096, SHA256: hash}); err != nil {
		t.Fatal(err)
	}
	data, manifest, err := sourceStore.ExportHashPack(ctx, hashpack.Source{ID: "example.fixture", Name: "Fixture identities", Publisher: "Example", License: "CC0-1.0"}, "2026.09")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := hashpack.Decode(data)
	if err != nil || manifest.RecordCount != 1 {
		t.Fatalf("decode export: %#v, %v", manifest, err)
	}

	destination, err := Open(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	preview, err := destination.PreviewHashPack(ctx, pack, hashpack.Digest(data))
	if err != nil || preview.NewCount != 1 || preview.ExistingCount != 0 || preview.ConflictCount != 0 {
		t.Fatalf("unexpected preview: %#v, %v", preview, err)
	}
	result, err := destination.ImportHashPack(ctx, pack, hashpack.Digest(data))
	if err != nil || result.ImportedRecords != 1 || result.ExistingRelease {
		t.Fatalf("unexpected import: %#v, %v", result, err)
	}
	var games, artifacts int
	if err = destination.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM games`).Scan(&games); err != nil {
		t.Fatal(err)
	}
	if err = destination.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts`).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if games != 0 || artifacts != 0 {
		t.Fatalf("hash-only import polluted library: games=%d artifacts=%d", games, artifacts)
	}
	matches, err := destination.ResolveHashIdentities(ctx, hash)
	if err != nil || len(matches) != 1 || matches[0].EditionType != "translation" || matches[0].GameTitles["zh-CN"] != "高级战争测试" {
		t.Fatalf("identity lookup=%#v err=%v", matches, err)
	}
	repeated, err := destination.ImportHashPack(ctx, pack, hashpack.Digest(data))
	if err != nil || !repeated.ExistingRelease || repeated.ImportedRecords != 0 {
		t.Fatalf("idempotent import=%#v err=%v", repeated, err)
	}
	regeneratedData, regeneratedManifest, err := hashpack.Encode(pack.Manifest.Source, pack.Manifest.Release, time.Now().Add(time.Hour), pack.Records)
	if err != nil || regeneratedManifest.PackID != pack.Manifest.PackID || hashpack.Digest(regeneratedData) == hashpack.Digest(data) {
		t.Fatalf("regenerated release identity drifted: manifest=%#v err=%v", regeneratedManifest, err)
	}
	regeneratedPack, err := hashpack.Decode(regeneratedData)
	if err != nil {
		t.Fatal(err)
	}
	regenerated, err := destination.ImportHashPack(ctx, regeneratedPack, hashpack.Digest(regeneratedData))
	if err != nil || !regenerated.ExistingRelease {
		t.Fatalf("equivalent regenerated release was not idempotent: %#v err=%v", regenerated, err)
	}
}

func TestHashPackReleaseIsImmutableAndConflictingSourcesCoexist(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash := strings.Repeat("c", 64)
	record := hashpack.Record{SHA256: hash, Size: 10, Platform: "gba", GameKey: "one", GameDefaultTitle: "One", EditionDefaultTitle: "Original", EditionType: "original", Role: "rom"}
	data, _, _ := hashpack.Encode(hashpack.Source{ID: "source.one", Name: "One", License: "CC0-1.0"}, "1", time.Now(), []hashpack.Record{record})
	pack, _ := hashpack.Decode(data)
	if _, err = store.ImportHashPack(ctx, pack, hashpack.Digest(data)); err != nil {
		t.Fatal(err)
	}
	sameRecord := record
	sameRecord.GameKey = "another-publisher-key"
	sameData, _, _ := hashpack.Encode(hashpack.Source{ID: "source.same", Name: "Same", License: "CC0-1.0"}, "1", time.Now(), []hashpack.Record{sameRecord})
	samePack, _ := hashpack.Decode(sameData)
	samePreview, err := store.PreviewHashPack(ctx, samePack, hashpack.Digest(sameData))
	if err != nil || samePreview.ExistingCount != 1 || samePreview.ConflictCount != 0 {
		t.Fatalf("source-scoped grouping key became conflict: %#v err=%v", samePreview, err)
	}
	record.GameDefaultTitle = "Changed"
	changedData, _, _ := hashpack.Encode(hashpack.Source{ID: "source.one", Name: "One", License: "CC0-1.0"}, "1", time.Now(), []hashpack.Record{record})
	changedPack, _ := hashpack.Decode(changedData)
	preview, err := store.PreviewHashPack(ctx, changedPack, hashpack.Digest(changedData))
	if err != nil || !preview.ReleaseConflict || preview.ConflictCount != 1 {
		t.Fatalf("release conflict preview=%#v err=%v", preview, err)
	}
	if _, err = store.ImportHashPack(ctx, changedPack, hashpack.Digest(changedData)); !errors.Is(err, ErrHashReleaseConflict) {
		t.Fatalf("immutable release accepted: %v", err)
	}
	record.GameKey = "two"
	otherData, _, _ := hashpack.Encode(hashpack.Source{ID: "source.two", Name: "Two", License: "CC0-1.0"}, "1", time.Now(), []hashpack.Record{record})
	otherPack, _ := hashpack.Decode(otherData)
	if _, err = store.ImportHashPack(ctx, otherPack, hashpack.Digest(otherData)); err != nil {
		t.Fatal(err)
	}
	matches, err := store.ResolveHashIdentities(ctx, hash)
	if err != nil || len(matches) != 2 {
		t.Fatalf("provenance records=%d err=%v", len(matches), err)
	}
}

func TestMigrationFromV26AddsHashReferenceTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v26.db")
	store, err := Open(path)
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
	if _, err = db.Exec(`DROP TABLE hash_identities; DROP TABLE hash_releases; DROP TABLE hash_sources; PRAGMA user_version=26;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	version, err := migrated.SchemaVersion(context.Background())
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{"hash_sources", "hash_releases", "hash_identities"} {
		var count int
		if err = migrated.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing %s after migration: count=%d err=%v", table, count, err)
		}
	}
	var packIDColumn int
	if err = migrated.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('hash_releases') WHERE name='pack_id'`).Scan(&packIDColumn); err != nil || packIDColumn != 1 {
		t.Fatalf("missing hash_releases.pack_id after migration: count=%d err=%v", packIDColumn, err)
	}
}
