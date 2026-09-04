package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGameEditionsHaveIndependentSaveNamespaces(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Final Fantasy VI", Platform: "snes", Titles: map[string]string{"zh": "最终幻想 VI", "ja": "ファイナルファンタジーVI"}})
	if err != nil {
		t.Fatal(err)
	}
	e1, err := s.AddEdition(ctx, NewEdition{GameID: w.ID, DefaultTitle: "Original", EditionType: "original", Languages: []string{"ja"}})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.AddEdition(ctx, NewEdition{GameID: w.ID, DefaultTitle: "Chinese v1.2", EditionType: "translation", Languages: []string{"zh-CN"}})
	if err != nil {
		t.Fatal(err)
	}
	if e1.SaveNamespace == e2.SaveNamespace || e1.SaveNamespace == "" || e2.SaveNamespace == "" {
		t.Fatalf("save namespaces must be distinct: %q %q", e1.SaveNamespace, e2.SaveNamespace)
	}
	got, err := s.GetGame(ctx, w.ID, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayTitle != "最终幻想 VI" {
		t.Fatalf("locale fallback: got %q", got.DisplayTitle)
	}
	if len(got.Editions) != 2 {
		t.Fatalf("got %d editions", len(got.Editions))
	}
}

func TestArtifactShapeValidationNormalizesRoles(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, NewGame{DefaultTitle: "Artifact", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	artifact, err := store.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: " ROM "})
	if err != nil || artifact.Role != "rom" {
		t.Fatalf("normalized artifact=%#v err=%v", artifact, err)
	}
	if _, err = store.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: "gba/bad.bin", Role: "firmware"}); err == nil || !strings.Contains(err.Error(), "artifact role") {
		t.Fatalf("invalid role accepted: %v", err)
	}
	artifact.DiscIndex = -1
	if _, err = store.UpdateArtifact(ctx, artifact.ID, NewArtifact{EditionID: edition.ID, Path: artifact.Path, Role: artifact.Role, DiscIndex: artifact.DiscIndex, StorageKind: artifact.StorageKind}); err == nil || !strings.Contains(err.Error(), "disc_index") {
		t.Fatalf("invalid disc index accepted: %v", err)
	}
}

func TestMediaMetadataUpdatePreservesOwnershipAndBlobIdentity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Media", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.AddMedia(ctx, NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: "media/aa/blob.png", OriginalName: "cover.png", MIMEType: "image/png", Size: 3, SHA256: strings.Repeat("a", 64), Locale: " zh-CN ", SourceType: "upload", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateMediaMetadata(ctx, media.ID, MediaMetadataUpdate{Kind: " Screenshot ", Locale: " ja ", SortOrder: 7})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != "screenshot" || updated.Locale != "ja" || updated.SortOrder != 7 {
		t.Fatalf("metadata not updated: %#v", updated)
	}
	if updated.GameID != media.GameID || updated.EditionID != media.EditionID || updated.Path != media.Path || updated.SHA256 != media.SHA256 || updated.Size != media.Size || updated.StorageKind != media.StorageKind || updated.OriginalName != media.OriginalName || updated.MIMEType != media.MIMEType || updated.SourceType != media.SourceType {
		t.Fatalf("metadata update changed immutable media identity: before=%#v after=%#v", media, updated)
	}
	if _, err = store.UpdateMediaMetadata(ctx, media.ID, MediaMetadataUpdate{Kind: "unknown", SortOrder: 0}); err == nil || !strings.Contains(err.Error(), "invalid media kind") {
		t.Fatalf("invalid kind accepted: %v", err)
	}
	if _, err = store.UpdateMediaMetadata(ctx, media.ID, MediaMetadataUpdate{Kind: "cover", SortOrder: -1}); err == nil || !strings.Contains(err.Error(), "sort_order") {
		t.Fatalf("negative sort order accepted: %v", err)
	}
	if _, err = store.AddMedia(ctx, NewMediaAsset{GameID: game.ID, Kind: "cover", Path: "media/bad.png", SortOrder: -1}); err == nil || !strings.Contains(err.Error(), "sort_order") {
		t.Fatalf("negative upload/import sort order accepted by store: %v", err)
	}
}

func TestMediaContentStatusBatchIsAtomicAndPreservesIdentity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Media status", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.AddMedia(ctx, NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: "media/aa/first.png", OriginalName: "first.png", MIMEType: "image/png", Size: 3, SHA256: strings.Repeat("a", 64), SourceType: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddMedia(ctx, NewMediaAsset{GameID: game.ID, Kind: "screenshot", StorageKind: "managed", Path: "media/bb/second.png", OriginalName: "second.png", MIMEType: "image/png", Size: 4, SHA256: strings.Repeat("b", 64), SourceType: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := "2026-08-27T06:00:00Z"
	if err = store.UpdateMediaContentStatuses(ctx, []MediaContentStatusUpdate{
		{ID: first.ID, ContentStatus: "available", ContentCheckedAt: checkedAt},
		{ID: "missing-media", ContentStatus: "missing", ContentCheckedAt: checkedAt},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing member error=%v, want sql.ErrNoRows", err)
	}
	rolledBack, err := store.GetMedia(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ContentStatus != "unverified" || rolledBack.ContentCheckedAt != "" {
		t.Fatalf("failed batch partially committed: %#v", rolledBack)
	}
	if err = store.UpdateMediaContentStatuses(ctx, []MediaContentStatusUpdate{
		{ID: first.ID, ContentStatus: "available", ContentCheckedAt: checkedAt},
		{ID: second.ID, ContentStatus: "missing", ContentCheckedAt: checkedAt},
	}); err != nil {
		t.Fatal(err)
	}
	updatedFirst, err := store.GetMedia(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	updatedSecond, err := store.GetMedia(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedFirst.ContentStatus != "available" || updatedSecond.ContentStatus != "missing" || updatedFirst.ContentCheckedAt != checkedAt || updatedSecond.ContentCheckedAt != checkedAt {
		t.Fatalf("status batch mismatch: first=%#v second=%#v", updatedFirst, updatedSecond)
	}
	if updatedFirst.GameID != first.GameID || updatedFirst.Path != first.Path || updatedFirst.SHA256 != first.SHA256 || updatedFirst.Size != first.Size || updatedFirst.Kind != first.Kind {
		t.Fatalf("status batch changed immutable identity: before=%#v after=%#v", first, updatedFirst)
	}
}

func TestAddEditionWithArtifactRollsBackOnHashConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	game, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Transactional edition", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	firstID := NewID()
	if _, err = s.AddEditionWithArtifact(ctx,
		NewEdition{ID: firstID, GameID: game.ID, DefaultTitle: "Original", EditionType: "original"},
		NewArtifact{EditionID: firstID, Path: "gba/original.gba", SHA256: "same-rom-hash", Size: 3},
	); err != nil {
		t.Fatal(err)
	}
	secondID := NewID()
	if _, err = s.AddEditionWithArtifact(ctx,
		NewEdition{ID: secondID, GameID: game.ID, DefaultTitle: "Duplicate", EditionType: "translation"},
		NewArtifact{EditionID: secondID, Path: "gba/duplicate.gba", SHA256: "same-rom-hash", Size: 3},
	); err == nil {
		t.Fatal("duplicate artifact hash unexpectedly committed")
	}
	refreshed, err := s.GetGame(ctx, game.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Editions) != 1 || refreshed.Editions[0].ID != firstID || refreshed.PrimaryEditionID != firstID {
		t.Fatalf("hash conflict left a partial edition or primary pointer: %#v", refreshed)
	}
}

func TestPairingExpirySingleUseAndTokenRevocation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	expired, err := s.CreatePairingCode(ctx, NewPairingCode{CodeHash: "expired-code-hash", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE pairing_codes SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.RedeemPairingCode(ctx, "expired-code-hash", "unused-token-hash", NewDevice{Name: "Expired", OSFamily: "linux"}, nil, time.Now().Add(time.Hour)); !errors.Is(err, ErrPairingCodeExpired) {
		t.Fatalf("expired pairing code = %v", err)
	}

	code, err := s.CreatePairingCode(ctx, NewPairingCode{CodeHash: "single-use-code-hash", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	device, token, _, err := s.RedeemPairingCode(ctx, "single-use-code-hash", "client-token-hash", NewDevice{Name: "Handheld", OSFamily: "linux"}, []string{"sync:read"}, time.Now().Add(time.Hour))
	if err != nil || device.ID == "" || token.ID == "" {
		t.Fatalf("redeem = %#v %#v %v", device, token, err)
	}
	stored, err := s.GetPairingCode(ctx, code.ID)
	if err != nil || stored.RedeemedAt.IsZero() || stored.CodeHash != "single-use-code-hash" {
		t.Fatalf("stored pairing state = %#v %v", stored, err)
	}
	if _, _, _, err = s.RedeemPairingCode(ctx, "single-use-code-hash", "second-token-hash", NewDevice{Name: "Duplicate", OSFamily: "linux"}, nil, time.Now().Add(time.Hour)); !errors.Is(err, ErrPairingCodeRedeemed) {
		t.Fatalf("reused pairing code = %v", err)
	}
	if _, err = s.AuthenticateClientToken(ctx, "client-token-hash"); err != nil {
		t.Fatalf("client token hash did not authenticate: %v", err)
	}
	if _, err = s.RevokeDevice(ctx, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateClientToken(ctx, "client-token-hash"); !errors.Is(err, ErrClientTokenInvalid) {
		t.Fatalf("revoked token remained active: %v", err)
	}
}

func TestPairingCodeBindsEnabledAdministratorDeviceProfileAtomically(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	unavailable, err := s.CreatePairingCode(ctx, NewPairingCode{
		CodeHash:        "unavailable-profile-code",
		RequestedDevice: map[string]any{"device_profile_id": "missing-profile"},
		ExpiresAt:       time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.RedeemPairingCode(ctx, unavailable.CodeHash, "unused-token", NewDevice{Name: "Handheld", OSFamily: "linux"}, nil, time.Now().Add(time.Hour)); !errors.Is(err, ErrPairingDeviceProfileUnavailable) {
		t.Fatalf("unavailable pairing profile = %v", err)
	}
	stored, err := s.GetPairingCode(ctx, unavailable.ID)
	if err != nil || !stored.RedeemedAt.IsZero() {
		t.Fatalf("failed profile redemption consumed the code: %#v %v", stored, err)
	}
	var deviceCount int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices`).Scan(&deviceCount); err != nil || deviceCount != 0 {
		t.Fatalf("failed profile redemption created a device: count=%d err=%v", deviceCount, err)
	}

	profile, err := s.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "selected-rocknix-profile", Name: "Selected ROCKNIX", Target: "rocknix", OSFamily: "handheld-linux"})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.CreatePairingCode(ctx, NewPairingCode{
		CodeHash:        "selected-profile-code",
		RequestedDevice: map[string]any{"device_profile_id": profile.ID},
		ExpiresAt:       time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	device, _, target, err := s.RedeemPairingCode(ctx, selected.CodeHash, "selected-token", NewDevice{Name: "Handheld", OSFamily: "linux"}, nil, time.Now().Add(time.Hour))
	if err != nil || device.DeviceProfileID != profile.ID || target != "rocknix" {
		t.Fatalf("administrator profile was not bound: device=%#v target=%q err=%v", device, target, err)
	}
}

func TestReferencedDeviceProfileIdentityCannotDriftOrDisappear(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	profile, err := s.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "paired-custom-profile", Name: "Custom handheld", Target: "rocknix", OSFamily: "handheld-linux", Architecture: "aarch64"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.CreatePairingCode(ctx, NewPairingCode{CodeHash: "bound-profile-code", RequestedDevice: map[string]any{"device_profile_id": profile.ID}, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	drift := NewDeviceProfile{Name: profile.Name, Target: "muos", OSFamily: profile.OSFamily, Architecture: profile.Architecture, PathStyle: profile.PathStyle, MaxPath: profile.MaxPath, Enabled: &enabled}
	if _, err = s.UpdateDeviceProfile(ctx, profile.ID, drift); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("pending pairing code allowed profile target drift: %v", err)
	}
	if err = s.DeleteDeviceProfile(ctx, profile.ID); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("pending pairing code allowed profile deletion: %v", err)
	}
	device, _, target, err := s.RedeemPairingCode(ctx, code.CodeHash, "bound-profile-token", NewDevice{Name: "Handheld", OSFamily: "linux", Architecture: "arm64"}, nil, time.Now().Add(time.Hour))
	if err != nil || device.DeviceProfileID != profile.ID || target != "rocknix" {
		t.Fatalf("redeem bound profile: %#v %q %v", device, target, err)
	}
	rename := NewDeviceProfile{Name: "Renamed handheld", Target: profile.Target, OSFamily: profile.OSFamily, Architecture: profile.Architecture, PathStyle: profile.PathStyle, MaxPath: profile.MaxPath, Enabled: &enabled}
	updated, err := s.UpdateDeviceProfile(ctx, profile.ID, rename)
	if err != nil || updated.Name != "Renamed handheld" {
		t.Fatalf("non-identity profile edit was blocked: %#v %v", updated, err)
	}
	drift.Target = "windows"
	if _, err = s.UpdateDeviceProfile(ctx, profile.ID, drift); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("paired device allowed profile identity drift: %v", err)
	}
	if err = s.DeleteDeviceProfile(ctx, profile.ID); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("paired device allowed profile deletion: %v", err)
	}
}

func TestImportGameCanAppendToStableGame(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	gameID := NewID()
	for i, path := range []string{"snes/original.sfc", "snes/translation.sfc"} {
		_, created, err := s.ImportGame(ctx, ImportedGame{GameID: gameID, Platform: "snes", DefaultTitle: "Game", EditionTitle: []string{"Original", "Translation"}[i], EditionType: []string{"original", "translation"}[i], Artifacts: []NewArtifact{{Path: path}}})
		if err != nil {
			t.Fatal(err)
		}
		if !created {
			t.Fatal("expected import")
		}
	}
	w, err := s.GetGame(ctx, gameID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Editions) != 2 {
		t.Fatalf("expected one work with two editions, got %d", len(w.Editions))
	}
}

func TestNonEmptyArtifactHashIsUnique(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	work, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Hash identity", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AddEdition(ctx, NewEdition{GameID: work.ID, DefaultTitle: "First", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddEdition(ctx, NewEdition{GameID: work.ID, DefaultTitle: "Second", EditionType: "revision"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddArtifact(ctx, NewArtifact{EditionID: first.ID, Path: "gba/first.gba", Role: "rom", SHA256: "same-content"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddArtifact(ctx, NewArtifact{EditionID: second.ID, Path: "gba/second.gba", Role: "rom", SHA256: "same-content"}); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint") {
		t.Fatalf("duplicate content hash was accepted: %v", err)
	}
	if _, err = s.AddArtifact(ctx, NewArtifact{EditionID: second.ID, Path: "gba/unhashed.gba", Role: "rom"}); err != nil {
		t.Fatalf("empty hash should remain available for manual paths: %v", err)
	}
}

func TestEditionCanMoveAndGamesCanMerge(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	target, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Final Fantasy VI", Platform: "snes", Titles: map[string]string{"zh-CN": "最终幻想 VI"}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Final Fantasy VI Chinese", Platform: "snes", Titles: map[string]string{"zh-TW": "太空戰士 VI"}})
	if err != nil {
		t.Fatal(err)
	}
	media, err := s.AddMedia(ctx, NewMediaAsset{GameID: source.ID, Kind: "cover", StorageKind: "managed", Path: "sha256/aa/cover.png", OriginalName: "cover.png"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.AddEdition(ctx, NewEdition{GameID: source.ID, DefaultTitle: "Chinese v1.2", EditionType: "translation", Languages: []string{"zh-CN"}})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := s.MoveEdition(ctx, e.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.GameID != target.ID || moved.SaveNamespace != e.SaveNamespace {
		t.Fatalf("move changed identity: %#v", moved)
	}
	if _, err = s.MergeGames(ctx, target.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGame(ctx, target.ID, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayTitle != "太空戰士 VI" || len(got.Editions) != 1 || len(got.Media) != 1 || got.Media[0].ID != media.ID || got.Media[0].GameID != target.ID {
		t.Fatalf("merged work lost metadata: %#v", got)
	}
}

func TestGameMergeFingerprintRejectsGraphDriftAtomically(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	target, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Steel Hero", Platform: "gba", Titles: map[string]string{"zh-CN": "钢铁英雄"}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Steel Hero Translation", Platform: "gba", Titles: map[string]string{"ja": "スチールヒーロー"}})
	if err != nil {
		t.Fatal(err)
	}
	targetEdition, err := s.AddEdition(ctx, NewEdition{GameID: target.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	vanishing, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Temporary duplicate", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	vanishingPreview, err := s.PreviewGameMerge(ctx, target.ID, vanishing.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGame(ctx, vanishing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeGamesIfFingerprint(ctx, target.ID, vanishing.ID, vanishingPreview.SnapshotFingerprint); !errors.Is(err, ErrGameMergeStale) {
		t.Fatalf("deleted source should stale the reviewed merge, got %v", err)
	}
	sourceEdition, err := s.AddEdition(ctx, NewEdition{GameID: source.ID, DefaultTitle: "Chinese", EditionType: "translation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddArtifact(ctx, NewArtifact{EditionID: sourceEdition.ID, Path: "gba/steel-zh.gba", Role: "rom", SHA256: "merge-fingerprint-source"}); err != nil {
		t.Fatal(err)
	}
	gameMedia, err := s.AddMedia(ctx, NewMediaAsset{GameID: source.ID, Kind: "cover", Path: "sha256/merge/cover.png"})
	if err != nil {
		t.Fatal(err)
	}
	editionMedia, err := s.AddMedia(ctx, NewMediaAsset{EditionID: sourceEdition.ID, Kind: "screenshot", Path: "sha256/merge/screen.png"})
	if err != nil {
		t.Fatal(err)
	}
	series, err := s.CreateSeries(ctx, NewSeries{DefaultTitle: "Steel Saga"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutSeriesMember(ctx, series.ID, source.ID, NewSeriesMember{RelationType: "mainline", SortOrder: 20}); err != nil {
		t.Fatal(err)
	}

	preview, err := s.PreviewGameMerge(ctx, target.ID, source.ID, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if preview.SnapshotFingerprint == "" || preview.TargetEditions != 1 || preview.SourceEditions != 1 || preview.SourceArtifacts != 1 || preview.SourceGameMedia != 1 || preview.SourceEditionMedia != 1 || preview.SourceSeriesMemberships != 1 {
		t.Fatalf("unexpected merge preview: %#v", preview)
	}
	driftEdition, err := s.AddEdition(ctx, NewEdition{GameID: source.ID, DefaultTitle: "Hard mode", EditionType: "hack"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeGamesIfFingerprint(ctx, target.ID, source.ID, preview.SnapshotFingerprint); !errors.Is(err, ErrGameMergeStale) {
		t.Fatalf("expected stale merge rejection, got %v", err)
	}
	if got, err := s.GetGame(ctx, target.ID, ""); err != nil || len(got.Editions) != 1 {
		t.Fatalf("target changed after stale merge: %#v, %v", got, err)
	}
	if got, err := s.GetGame(ctx, source.ID, ""); err != nil || len(got.Editions) != 2 {
		t.Fatalf("source changed after stale merge: %#v, %v", got, err)
	}

	preview, err = s.PreviewGameMerge(ctx, target.ID, source.ID, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	merged, err := s.MergeGamesIfFingerprint(ctx, target.ID, source.ID, preview.SnapshotFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Editions) != 3 {
		t.Fatalf("unexpected merged editions: %#v", merged.Editions)
	}
	byID := map[string]Edition{}
	for _, edition := range merged.Editions {
		byID[edition.ID] = edition
	}
	if byID[targetEdition.ID].SaveNamespace != targetEdition.SaveNamespace {
		t.Fatal("merge changed the target Edition identity or save namespace")
	}
	if byID[sourceEdition.ID].SaveNamespace != sourceEdition.SaveNamespace || byID[driftEdition.ID].SaveNamespace != driftEdition.SaveNamespace {
		t.Fatal("merge changed an Edition ID or save namespace")
	}
	if len(merged.Media) != 1 || merged.Media[0].ID != gameMedia.ID || merged.Media[0].GameID != target.ID {
		t.Fatalf("game media did not follow merge: %#v", merged.Media)
	}
	if got := byID[sourceEdition.ID].Media; len(got) != 1 || got[0].ID != editionMedia.ID {
		t.Fatalf("edition media was not preserved: %#v", got)
	}
	if _, err = s.GetGame(ctx, source.ID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("source game still exists after merge: %v", err)
	}
	gotSeries, err := s.GetSeries(ctx, series.ID, "")
	if err != nil || len(gotSeries.Members) != 1 || gotSeries.Members[0].GameID != target.ID {
		t.Fatalf("series membership was not preserved: %#v, %v", gotSeries, err)
	}
}

func TestImportGameRollsBackAllMetadataOnMediaFailure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	gameID, editionID := NewID(), NewID()
	_, _, err := s.ImportGame(ctx, ImportedGame{
		GameID:       gameID,
		EditionID:    editionID,
		Platform:     "snes",
		DefaultTitle: "Transactional import",
		EditionTitle: "Original",
		EditionType:  "original",
		Artifacts:    []NewArtifact{{Path: "snes/transactional.sfc"}},
		Media:        []NewMediaAsset{{Kind: "not-a-real-kind", Path: "cover.png"}},
	})
	if err == nil {
		t.Fatal("expected invalid media to fail the import")
	}
	if _, err = s.GetGame(ctx, gameID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("work survived rolled-back import: %v", err)
	}
	if _, err = s.GetEdition(ctx, editionID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("edition survived rolled-back import: %v", err)
	}
	if _, err = s.ArtifactByPath(ctx, "snes/transactional.sfc"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("artifact survived rolled-back import: %v", err)
	}
}

func TestEditionUpdateAndMetadataDeleteNeverDeleteROM(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	rom := filepath.Join(t.TempDir(), "game.gba")
	if err := os.WriteFile(rom, []byte("rom-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Game", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.AddEdition(ctx, NewEdition{GameID: w.ID, DefaultTitle: "Game", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddArtifact(ctx, NewArtifact{EditionID: e.ID, Path: rom, Role: "rom"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateEdition(ctx, e.ID, NewEdition{DefaultTitle: "Game Translation", EditionType: "translation", Languages: []string{"zh-CN"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.SaveNamespace != e.SaveNamespace || updated.EditionType != "translation" {
		t.Fatalf("unexpected update: %#v", updated)
	}
	if err = s.DeleteArtifact(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(rom); err != nil {
		t.Fatalf("metadata deletion touched ROM: %v", err)
	}
	if err = s.DeleteEdition(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(rom); err != nil {
		t.Fatalf("edition deletion touched ROM: %v", err)
	}
}

func TestMoveRejectsDifferentPlatforms(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	gba, _ := s.CreateGame(ctx, NewGame{DefaultTitle: "GBA", Platform: "gba"})
	ps2, _ := s.CreateGame(ctx, NewGame{DefaultTitle: "PS2", Platform: "ps2"})
	e, _ := s.AddEdition(ctx, NewEdition{GameID: gba.ID, DefaultTitle: "Edition", EditionType: "original"})
	if _, err := s.MoveEdition(ctx, e.ID, ps2.ID); !errors.Is(err, ErrPlatformMismatch) {
		t.Fatalf("expected platform mismatch, got %v", err)
	}
}

func TestSeriesGroupsCrossPlatformGamesWithoutMergingEditions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	snes, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Chrono Trigger", Platform: "snes"})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Chrono Trigger DS", Platform: "nds"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.AddEdition(ctx, NewEdition{GameID: snes.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	port, err := s.AddEdition(ctx, NewEdition{GameID: ds.ID, DefaultTitle: "DS release", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	series, err := s.CreateSeries(ctx, NewSeries{DefaultTitle: "Chrono", Description: "Time-travel RPG series", Titles: map[string]string{"zh-CN": "时空系列", "ja": "クロノ"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutSeriesMember(ctx, series.ID, snes.ID, NewSeriesMember{RelationType: "mainline", SortOrder: 10}); err != nil {
		t.Fatal(err)
	}
	series, err = s.PutSeriesMember(ctx, series.ID, ds.ID, NewSeriesMember{RelationType: "port", SortOrder: 20})
	if err != nil {
		t.Fatal(err)
	}
	if series.DisplayTitle != "Chrono" || len(series.Members) != 2 || series.Members[0].Game.Platform != "snes" || series.Members[1].Game.Platform != "nds" {
		t.Fatalf("unexpected cross-platform series: %#v", series)
	}
	localized, err := s.GetSeries(ctx, series.ID, "zh-CN")
	if err != nil || localized.DisplayTitle != "时空系列" {
		t.Fatalf("localized series = %#v, %v", localized, err)
	}
	if original.SaveNamespace == port.SaveNamespace || series.Members[0].Game.ID == series.Members[1].Game.ID {
		t.Fatal("series grouping merged work identity or save namespaces")
	}
	if err = s.DeleteSeriesMember(ctx, series.ID, ds.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSeries(ctx, series.ID, ""); len(got.Members) != 1 {
		t.Fatalf("expected one remaining member, got %#v", got.Members)
	}
	if _, err = s.GetGame(ctx, ds.ID, ""); err != nil {
		t.Fatalf("removing a series relation deleted its Game: %v", err)
	}
	if err = s.DeleteSeries(ctx, series.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetGame(ctx, snes.ID, ""); err != nil {
		t.Fatalf("deleting a series deleted its member Game: %v", err)
	}
}

func TestSeriesMutationReplacesMetadataAndMembersAtomically(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	gba, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Advance", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	nds, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Dual", Platform: "nds"})
	if err != nil {
		t.Fatal(err)
	}

	failedID := NewID()
	missingMembers := []SeriesMemberMutation{{GameID: gba.ID, RelationType: "mainline", SortOrder: 10}, {GameID: NewID(), RelationType: "port", SortOrder: 20}}
	if _, err = s.CreateSeriesMutation(ctx, SeriesMutation{ID: failedID, DefaultTitle: "Must Roll Back", Members: &missingMembers}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing second member error = %v", err)
	}
	if _, err = s.GetSeries(ctx, failedID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed atomic create left series behind: %v", err)
	}

	members := []SeriesMemberMutation{{GameID: gba.ID, RelationType: "mainline", SortOrder: 10}, {GameID: nds.ID, RelationType: "port", SortOrder: 20}}
	series, err := s.CreateSeriesMutation(ctx, SeriesMutation{DefaultTitle: "Handheld Saga", Description: "before", Titles: map[string]string{"zh-CN": "掌机传奇"}, Members: &members})
	if err != nil {
		t.Fatal(err)
	}
	badMembers := []SeriesMemberMutation{{GameID: gba.ID, RelationType: "remake", SortOrder: 1}, {GameID: nds.ID, RelationType: "port", SortOrder: -1}}
	if _, err = s.UpdateSeriesMutation(ctx, series.ID, SeriesMutation{DefaultTitle: "Partially Changed", Description: "after", Members: &badMembers}); err == nil || !strings.Contains(err.Error(), "sort_order") {
		t.Fatalf("negative order error = %v", err)
	}
	unchanged, err := s.GetSeries(ctx, series.ID, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.DefaultTitle != "Handheld Saga" || unchanged.Description != "before" || unchanged.DisplayTitle != "掌机传奇" || len(unchanged.Members) != 2 || unchanged.Members[0].RelationType != "mainline" || unchanged.Members[1].SortOrder != 20 {
		t.Fatalf("failed update changed series graph: %#v", unchanged)
	}

	replacement := []SeriesMemberMutation{{GameID: nds.ID, RelationType: "remake", SortOrder: 3}}
	updated, err := s.UpdateSeriesMutation(ctx, series.ID, SeriesMutation{DefaultTitle: "Handheld Saga II", Description: "after", Titles: map[string]string{"ja": "携帯物語"}, Members: &replacement})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DefaultTitle != "Handheld Saga II" || updated.Description != "after" || len(updated.Members) != 1 || updated.Members[0].GameID != nds.ID || updated.Members[0].RelationType != "remake" || updated.Members[0].SortOrder != 3 {
		t.Fatalf("replacement = %#v", updated)
	}
	if _, err = s.GetGame(ctx, gba.ID, ""); err != nil {
		t.Fatalf("removing a membership changed its Game: %v", err)
	}
}

func TestSchemaVersionIntegrityAndBackup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	version, err := s.SchemaVersion(ctx)
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, %v", version, err)
	}
	if result, err := s.IntegrityCheck(ctx); err != nil || result != "ok" {
		t.Fatalf("integrity = %q, %v", result, err)
	}
	if _, err = s.CreateGame(ctx, NewGame{DefaultTitle: "Backup Game", Platform: "gba"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backups", "library.db")
	if err = s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err = s.Backup(ctx, backup); err == nil {
		t.Fatal("expected existing backup destination to be rejected")
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	works, err := restored.ListGames(ctx, "")
	if err != nil || len(works) != 1 || works[0].DefaultTitle != "Backup Game" {
		t.Fatalf("backup contents = %#v, %v", works, err)
	}
}

func TestV18MediaStatusMigrationDoesNotInspectFiles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "media-status-v17.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Offline media", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.AddMedia(ctx, NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "library", Path: "missing/cover.png", OriginalName: "cover.png", MIMEType: "image/png", Size: 12, SHA256: strings.Repeat("a", 64), ContentStatus: "available"})
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
	if _, err = db.Exec(`DROP INDEX idx_media_content_status; ALTER TABLE media_assets DROP COLUMN content_checked_at; ALTER TABLE media_assets DROP COLUMN content_status; PRAGMA user_version=17;`); err != nil {
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
	got, err := migrated.GetMedia(ctx, media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentStatus != "unverified" || got.ContentCheckedAt != "" || got.Path != media.Path || got.SHA256 != media.SHA256 || got.Size != media.Size || got.GameID != game.ID {
		t.Fatalf("v18 migration changed media identity or inspected a missing file: %#v", got)
	}
	var indexCount int
	if err = migrated.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_media_content_status'`).Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("status index count=%d err=%v", indexCount, err)
	}
}

func TestV19MigrationInvalidatesUnboundHardwareEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime-evidence-v18.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "legacy-device", Name: "Legacy device", Target: "windows", OSFamily: "windows"})
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
	legacyEvidence := `{"scope":"hardware","device":"fixture","software_version":"1","verified_at":"2026-08-27","result":"passed","scenarios":["launch"]}`
	if _, err = db.Exec(`UPDATE device_profiles SET support_level='hardware-tested',evidence_json=? WHERE id=?; PRAGMA user_version=18`, legacyEvidence, device.ID); err != nil {
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
	got, err := migrated.GetDeviceProfile(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SupportLevel != "catalogued" || got.Evidence["stale"] != true || got.Evidence["stale_reason"] != "runtime_contract_unbound" {
		t.Fatalf("legacy hardware evidence was not safely invalidated: %#v", got)
	}
}

func TestImportGamesAtomicRollsBackEarlierItems(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	err := s.ImportGamesAtomic(ctx, []ImportedGame{
		{
			GameID:       NewID(),
			EditionID:    NewID(),
			Platform:     "gba",
			DefaultTitle: "First",
			EditionTitle: "Original",
			EditionType:  "original",
			Artifacts:    []NewArtifact{{Path: "gba/first.gba", SHA256: "first-hash"}},
		},
		{
			GameID:       NewID(),
			EditionID:    NewID(),
			Platform:     "gba",
			DefaultTitle: "Second",
			EditionTitle: "Invalid",
			EditionType:  "not-a-real-edition-type",
			Artifacts:    []NewArtifact{{Path: "gba/second.gba", SHA256: "second-hash"}},
		},
	})
	if err == nil {
		t.Fatal("expected the invalid second item to abort the batch")
	}
	works, listErr := s.ListGames(ctx, "")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(works) != 0 {
		t.Fatalf("atomic batch left %d works behind", len(works))
	}
}

func downgradeV9ToV8(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		DROP TRIGGER IF EXISTS trg_frontend_adapters_handler_update;
		DROP TRIGGER IF EXISTS trg_package_profiles_frontend_insert;
		DROP TRIGGER IF EXISTS trg_package_profiles_frontend_update;
		DROP TABLE inventory_items;
		DROP TABLE sync_operations;
		DROP TABLE sync_sessions;
		DROP TABLE client_tokens;
		DROP TABLE pairing_codes;
		DROP TABLE save_bindings;
		DROP TABLE save_files;
		DROP TABLE save_revisions;
		DROP TABLE save_stream_editions;
		DROP TABLE save_streams;
		ALTER TABLE devices DROP COLUMN device_profile_id;
		ALTER TABLE devices DROP COLUMN agent_version;
		ALTER TABLE devices DROP COLUMN status;
		ALTER TABLE devices DROP COLUMN revoked_at;
		CREATE TABLE save_revisions (
		  id TEXT PRIMARY KEY,
		  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
		  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE RESTRICT,
		  driver_id TEXT NOT NULL DEFAULT '',
		  relative_path TEXT NOT NULL,
		  scope_type TEXT NOT NULL DEFAULT 'game',
		  scope_key TEXT NOT NULL DEFAULT '',
		  checksum TEXT NOT NULL,
		  size INTEGER NOT NULL,
		  blob_path TEXT NOT NULL,
		  base_revision_id TEXT NOT NULL DEFAULT '',
		  conflict INTEGER NOT NULL DEFAULT 0,
		  created_at TEXT NOT NULL
		);
		CREATE INDEX idx_save_revisions_unit ON save_revisions(edition_id,driver_id,scope_type,scope_key,relative_path,created_at DESC);
		CREATE INDEX idx_save_revisions_checksum ON save_revisions(checksum);
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationFromV4AddsUniqueArtifactHashIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v4.db")
	s, err := Open(path)
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
	downgradeV9ToV8(t, db)
	if _, err = db.Exec(`DROP TABLE launch_bindings; DROP TABLE core_mappings; DROP TABLE retroarch_cores; DROP TABLE emulator_drivers; DROP TABLE device_profiles; DROP TABLE frontend_adapters; ALTER TABLE package_profiles DROP COLUMN device_profile_id; ALTER TABLE package_profiles DROP COLUMN frontend_adapter_id; DROP INDEX idx_artifacts_sha_unique; PRAGMA user_version = 4;`); err != nil {
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
		t.Fatalf("schema version = %d, %v", version, err)
	}
	var indexSQL string
	if err = migrated.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_artifacts_sha_unique'`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexSQL, "UNIQUE INDEX") || !strings.Contains(indexSQL, "sha256<>''") {
		t.Fatalf("unexpected v5 index definition: %q", indexSQL)
	}
}

func TestMigrationFromV5AddsPersistentLibrarySources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v5.db")
	s, err := Open(path)
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
	downgradeV9ToV8(t, db)
	if _, err = db.Exec(`DROP TABLE launch_bindings; DROP TABLE core_mappings; DROP TABLE retroarch_cores; DROP TABLE emulator_drivers; DROP TABLE device_profiles; DROP TABLE frontend_adapters; ALTER TABLE package_profiles DROP COLUMN device_profile_id; ALTER TABLE package_profiles DROP COLUMN frontend_adapter_id; DROP TABLE source_scans; DROP TABLE library_sources; PRAGMA user_version = 5;`); err != nil {
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
		t.Fatalf("schema version = %d, %v", version, err)
	}
	for _, table := range []string{"library_sources", "source_scans"} {
		var name string
		if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("missing v6 table %s: %q, %v", table, name, err)
		}
	}
}

func TestMigrationFromV10AddsRuntimeMetadataPathWithoutLosingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v10.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateLibrarySource(ctx, NewLibrarySource{Name: "ES-DE GBA", Kind: "esde", MetadataPath: "gamelists/gba/gamelist.xml", Platform: "gba", MetadataLocale: "en"})
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
	if _, err = db.Exec(`
		DROP INDEX idx_library_sources_identity;
		ALTER TABLE library_sources DROP COLUMN runtime_metadata_path;
		CREATE UNIQUE INDEX idx_library_sources_identity ON library_sources(kind,root_path,metadata_path,platform);
		PRAGMA user_version = 10;
	`); err != nil {
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
	got, err := migrated.GetLibrarySource(ctx, source.ID)
	if err != nil || got.Name != source.Name || got.MetadataPath != source.MetadataPath || got.RuntimeMetadataPath != "" {
		t.Fatalf("source changed across migration: %#v, %v", got, err)
	}
}

func TestMigrationFromV11AddsRuntimeContractVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v11.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDeviceProfile(ctx, NewDeviceProfile{Name: "Fixture handheld", Target: "fixture", OSFamily: "handheld-linux"})
	if err != nil {
		t.Fatal(err)
	}
	core, err := s.CreateRetroArchCore(ctx, NewRetroArchCore{Name: "Fixture core", LibraryNames: []string{"fixture_libretro"}, Platforms: []string{"gba"}})
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
	if _, err = db.Exec(`
		ALTER TABLE device_profiles DROP COLUMN contract_version;
		ALTER TABLE retroarch_cores DROP COLUMN contract_version;
		PRAGMA user_version = 11;
	`); err != nil {
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
	gotDevice, err := migrated.GetDeviceProfile(ctx, device.ID)
	if err != nil || gotDevice.ContractVersion != 1 || gotDevice.Name != device.Name {
		t.Fatalf("device profile changed across migration: %#v, %v", gotDevice, err)
	}
	gotCore, err := migrated.GetRetroArchCore(ctx, core.ID)
	if err != nil || gotCore.ContractVersion != 1 || gotCore.Name != core.Name {
		t.Fatalf("RetroArch core changed across migration: %#v, %v", gotCore, err)
	}
}

func TestMigrationFromV12AllowsNeutralSourcesAndPreservesScanHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v12.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateLibrarySource(ctx, NewLibrarySource{Name: "Pegasus GBA", Kind: "pegasus", MetadataPath: "gba/metadata.pegasus.txt", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := s.CreateSourceScan(ctx, NewSourceScan{SourceID: source.ID, Status: "ready", StartedAt: time.Now(), FinishedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), CandidateCount: 2, ImportableCount: 1, MissingCount: 1, PreviewTokenHash: "v12-digest"})
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
	// Recreate the exact v12 table contract without touching any rows. This is
	// test-fixture setup only; production migration never enables writable_schema.
	if _, err = db.Exec(`
		PRAGMA writable_schema = ON;
		UPDATE sqlite_master
		SET sql = replace(sql, '''rom_directory'',''pegasus'',''esde'',''varkiv''', '''rom_directory'',''pegasus'',''esde''')
		WHERE type='table' AND name='library_sources';
		PRAGMA writable_schema = OFF;
		PRAGMA schema_version = 1312;
		PRAGMA user_version = 12;
	`); err != nil {
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
	gotSource, err := migrated.GetLibrarySource(ctx, source.ID)
	if err != nil || gotSource.MetadataPath != source.MetadataPath || gotSource.LastScanStatus != "ready" {
		t.Fatalf("source changed across v13 migration: %#v, %v", gotSource, err)
	}
	gotScan, err := migrated.GetSourceScan(ctx, scan.ID)
	var tokenDigest string
	if queryErr := migrated.db.QueryRow(`SELECT preview_token_hash FROM source_scans WHERE id=?`, scan.ID).Scan(&tokenDigest); queryErr != nil {
		t.Fatal(queryErr)
	}
	if err != nil || tokenDigest != "v12-digest" || gotScan.CandidateCount != 2 || gotScan.MissingCount != 1 {
		t.Fatalf("scan changed across v13 migration: %#v, %v", gotScan, err)
	}
	neutral, err := migrated.CreateLibrarySource(ctx, NewLibrarySource{Name: "Multi-platform recovery", Kind: "varkiv", MetadataPath: "recovery/library-manifest.json", ROMStoragePolicy: "reference", MediaStoragePolicy: "copy"})
	if err != nil || neutral.Kind != "varkiv" || neutral.Platform != "" || neutral.RootPath != "recovery" {
		t.Fatalf("neutral source after migration: %#v, %v", neutral, err)
	}
}

func TestMigrationFromV13AddsSourceAdaptersAndPreservesSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v13.db")
	ctx := context.Background()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateLibrarySource(ctx, NewLibrarySource{Name: "Existing Pegasus", Kind: "pegasus", MetadataPath: "gba/metadata.pegasus.txt", Platform: "gba"})
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
	if _, err = db.Exec(`
		DROP TRIGGER trg_library_sources_adapter_insert;
		DROP TRIGGER trg_library_sources_adapter_update;
		DROP TRIGGER trg_source_adapters_handler_update;
		DROP INDEX idx_library_sources_adapter;
		ALTER TABLE library_sources DROP COLUMN source_adapter_id;
		DROP INDEX idx_source_adapters_format;
		DROP TABLE source_adapters;
		PRAGMA user_version = 13;
	`); err != nil {
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
	got, err := migrated.GetLibrarySource(ctx, source.ID)
	if err != nil || got.SourceAdapterID != "builtin-source-pegasus" || got.MetadataPath != source.MetadataPath {
		t.Fatalf("source changed across v14 migration: %#v, %v", got, err)
	}
	adapters, err := migrated.ListSourceAdapters(ctx)
	if err != nil || len(adapters) != 4 {
		t.Fatalf("source adapters after migration = %d, %v", len(adapters), err)
	}
	for _, adapter := range adapters {
		expectedContract := 2
		if adapter.ID == "builtin-source-direct-rom" {
			expectedContract = 3
		} else if adapter.ID == "builtin-source-varkiv" {
			expectedContract = 4
		}
		if adapter.ContractVersion != expectedContract || adapter.SupportLevel != "package-tested" || len(adapter.Capabilities) < 5 {
			t.Fatalf("incomplete migrated source adapter contract: %#v", adapter)
		}
	}
}

func TestMigrationFromV25NormalizesPreviewNeutralNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v25-neutral.db")
	ctx := context.Background()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateLibrarySource(ctx, NewLibrarySource{
		Name: "Preview neutral source", Kind: "varkiv", MetadataPath: "recovery/library-manifest.json",
		ROMStoragePolicy: "reference", MediaStoragePolicy: "copy",
	})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := store.CreateSourceScan(ctx, NewSourceScan{
		SourceID: source.ID, Status: "ready", CandidateCount: 3, ImportableCount: 2, MissingCount: 1,
		PreviewTokenHash: "preview-v25-token",
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
	if _, err = db.Exec(`
		DROP TRIGGER trg_library_sources_adapter_insert;
		DROP TRIGGER trg_library_sources_adapter_update;
		DROP TRIGGER trg_source_adapters_handler_update;
		DROP TRIGGER trg_source_adapters_builtin_ownership_update;
		PRAGMA writable_schema = ON;
		UPDATE sqlite_master
		SET sql = replace(sql, '''rom_directory'',''pegasus'',''esde'',''varkiv''', '''rom_directory'',''pegasus'',''esde'',''legacy-neutral''')
		WHERE type='table' AND name IN ('library_sources','source_adapters');
		PRAGMA writable_schema = OFF;
		PRAGMA schema_version = 2526;
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		UPDATE source_adapters
		SET id='builtin-source-legacy-neutral',format='legacy-neutral',handler='legacy-neutral'
		WHERE id='builtin-source-varkiv';
		UPDATE library_sources
		SET kind='legacy-neutral',source_adapter_id='builtin-source-legacy-neutral'
		WHERE id=?;
		PRAGMA user_version = 25;
	`, source.ID); err != nil {
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
	gotSource, err := migrated.GetLibrarySource(ctx, source.ID)
	if err != nil || gotSource.Kind != "varkiv" || gotSource.SourceAdapterID != "builtin-source-varkiv" {
		t.Fatalf("neutral source namespace was not normalized: %#v, %v", gotSource, err)
	}
	gotScan, err := migrated.GetSourceScan(ctx, scan.ID)
	if err != nil || gotScan.CandidateCount != 3 || gotScan.ImportableCount != 2 || gotScan.MissingCount != 1 {
		t.Fatalf("scan history changed across v26 migration: %#v, %v", gotScan, err)
	}
	var legacyRows int
	if err = migrated.db.QueryRow(`
		SELECT
		  (SELECT COUNT(*) FROM source_adapters WHERE id LIKE '%legacy-neutral%' OR format='legacy-neutral' OR handler='legacy-neutral') +
		  (SELECT COUNT(*) FROM library_sources WHERE kind='legacy-neutral' OR source_adapter_id LIKE '%legacy-neutral%')
	`).Scan(&legacyRows); err != nil || legacyRows != 0 {
		t.Fatalf("preview namespace remained after v26 migration: rows=%d err=%v", legacyRows, err)
	}
}

func TestMigrationFromV27RenamesProductNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v27-product-name.db")
	ctx := context.Background()
	legacyProductName := "vari" + "antia"
	legacyAdapterID := "builtin-source-" + legacyProductName
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateLibrarySource(ctx, NewLibrarySource{
		Name: "Recovery source", Kind: "varkiv", MetadataPath: "recovery/library-manifest.json",
		ROMStoragePolicy: "reference", MediaStoragePolicy: "copy",
	})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := store.CreateSourceScan(ctx, NewSourceScan{
		SourceID: source.ID, Status: "ready", CandidateCount: 4, ImportableCount: 3, MissingCount: 1,
		PreviewTokenHash: "preview-v27-token",
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
	if _, err = db.Exec(`
		DROP TRIGGER trg_library_sources_adapter_insert;
		DROP TRIGGER trg_library_sources_adapter_update;
		DROP TRIGGER trg_source_adapters_handler_update;
		DROP TRIGGER trg_source_adapters_builtin_ownership_update;
		PRAGMA writable_schema = ON;
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	canonicalConstraint := "'rom_directory','pegasus','esde','varkiv'"
	legacyConstraint := "'rom_directory','pegasus','esde','" + legacyProductName + "'"
	if _, err = db.Exec(`UPDATE sqlite_master SET sql=replace(sql,?,?) WHERE type='table' AND name IN ('library_sources','source_adapters')`, canonicalConstraint, legacyConstraint); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA writable_schema = OFF; PRAGMA schema_version = 2728;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		UPDATE source_adapters
		SET id=?,format=?,handler=?
		WHERE id='builtin-source-varkiv';
		UPDATE library_sources
		SET kind=?,source_adapter_id=?
		WHERE id=?;
		PRAGMA user_version = 27;
	`, legacyAdapterID, legacyProductName, legacyProductName, legacyProductName, legacyAdapterID, source.ID); err != nil {
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
	gotSource, err := migrated.GetLibrarySource(ctx, source.ID)
	if err != nil || gotSource.Kind != "varkiv" || gotSource.SourceAdapterID != "builtin-source-varkiv" {
		t.Fatalf("product namespace was not renamed: %#v, %v", gotSource, err)
	}
	gotScan, err := migrated.GetSourceScan(ctx, scan.ID)
	if err != nil || gotScan.CandidateCount != 4 || gotScan.ImportableCount != 3 || gotScan.MissingCount != 1 {
		t.Fatalf("scan history changed across v28 migration: %#v, %v", gotScan, err)
	}
	var staleRows int
	legacyPattern := "%" + legacyProductName + "%"
	if err = migrated.db.QueryRow(`
		SELECT
		  (SELECT COUNT(*) FROM source_adapters WHERE id LIKE ? OR format=? OR handler=?) +
		  (SELECT COUNT(*) FROM library_sources WHERE kind=? OR source_adapter_id LIKE ?)
	`, legacyPattern, legacyProductName, legacyProductName, legacyProductName, legacyPattern).Scan(&staleRows); err != nil || staleRows != 0 {
		t.Fatalf("old product namespace remained after v28 migration: rows=%d err=%v", staleRows, err)
	}
}

func TestMigrationFromV14ConstrainsArtifactShape(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "from-v14.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Artifact migration", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: "gba/migrate.gba", Role: "rom", Size: 1})
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
	if _, err = db.Exec(`DROP TRIGGER trg_artifacts_shape_insert; DROP TRIGGER trg_artifacts_shape_update; UPDATE artifacts SET role=' ROM ' WHERE id=?; PRAGMA user_version=14`, artifact.ID); err != nil {
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
	got, err := migrated.ArtifactByPath(ctx, artifact.Path)
	if err != nil || got.Role != "rom" {
		t.Fatalf("normalized artifact=%#v err=%v", got, err)
	}
	if _, err = migrated.db.ExecContext(ctx, `UPDATE artifacts SET role='mystery' WHERE id=?`, artifact.ID); err == nil || !strings.Contains(err.Error(), "invalid artifact shape") {
		t.Fatalf("database accepted invalid artifact role: %v", err)
	}
	if _, err = migrated.db.ExecContext(ctx, `UPDATE artifacts SET disc_index=-1 WHERE id=?`, artifact.ID); err == nil || !strings.Contains(err.Error(), "invalid artifact shape") {
		t.Fatalf("database accepted invalid disc index: %v", err)
	}
	if err = migrated.Close(); err != nil {
		t.Fatal(err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid-v14.db")
	invalid, err := Open(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	game, _ = invalid.CreateGame(ctx, NewGame{DefaultTitle: "Invalid", Platform: "gba"})
	edition, _ = invalid.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	artifact, _ = invalid.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: "gba/invalid.gba", Role: "rom"})
	if err = invalid.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TRIGGER trg_artifacts_shape_insert; DROP TRIGGER trg_artifacts_shape_update; UPDATE artifacts SET role='mystery' WHERE id=?; PRAGMA user_version=14`, artifact.ID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if invalid, err = Open(invalidPath); err == nil || !strings.Contains(err.Error(), "migration 15") {
		if invalid != nil {
			invalid.Close()
		}
		t.Fatalf("invalid v14 artifact migrated: %v", err)
	}
	db, err = sql.Open("sqlite", invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	var preservedRole string
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT role FROM artifacts WHERE id=?`, artifact.ID).Scan(&preservedRole); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if version != 14 || preservedRole != "mystery" {
		t.Fatalf("failed migration changed source database: version=%d role=%q", version, preservedRole)
	}
}

func TestMigrationFromV15RenamesGameSchemaWithoutLosingRelations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "from-v15.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Schema Game", Platform: "gba", Titles: map[string]string{"zh-CN": "架构游戏"}})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.AddMedia(ctx, NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: "sha256/16/cover.png", OriginalName: "cover.png"})
	if err != nil {
		t.Fatal(err)
	}
	series, err := store.CreateSeries(ctx, NewSeries{DefaultTitle: "Schema Series"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutSeriesMember(ctx, series.ID, game.ID, NewSeriesMember{RelationType: "mainline", SortOrder: 7}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	// Recovery and migration fixtures may carry the latest physical shape with
	// an older user_version. Open rewinds the naming layer transactionally and
	// then exercises the exact v15 -> v16 migration.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA user_version=15`); err != nil {
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
	got, err := migrated.GetGame(ctx, game.ID, "zh-CN")
	if err != nil || got.DisplayTitle != "架构游戏" || len(got.Editions) != 1 || got.Editions[0].ID != edition.ID {
		t.Fatalf("migrated game=%#v err=%v", got, err)
	}
	mediaItems, err := migrated.ListMedia(ctx, game.ID, "", "cover")
	if err != nil || len(mediaItems) != 1 || mediaItems[0].ID != media.ID {
		t.Fatalf("migrated media=%#v err=%v", mediaItems, err)
	}
	gotSeries, err := migrated.GetSeries(ctx, series.ID, "")
	if err != nil || len(gotSeries.Members) != 1 || gotSeries.Members[0].GameID != game.ID {
		t.Fatalf("migrated series=%#v err=%v", gotSeries, err)
	}
	if version, versionErr := migrated.SchemaVersion(ctx); versionErr != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, versionErr)
	}
	for _, legacy := range []string{"works", "localized_titles_v15", "localized_titles_v16"} {
		var count int
		if err = migrated.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, legacy).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy table %q remains: count=%d err=%v", legacy, count, err)
		}
	}
	for table, columns := range map[string][]string{
		"editions":       {"game_id", "work_id"},
		"media_assets":   {"game_id", "work_id"},
		"series_members": {"game_id", "work_id"},
	} {
		rows, queryErr := migrated.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		seen := map[string]bool{}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
				rows.Close()
				t.Fatal(queryErr)
			}
			seen[name] = true
		}
		if queryErr = rows.Close(); queryErr != nil {
			t.Fatal(queryErr)
		}
		if !seen[columns[0]] || seen[columns[1]] {
			t.Fatalf("%s columns=%#v", table, seen)
		}
	}
	if _, err = migrated.db.ExecContext(ctx, `INSERT INTO localized_titles(owner_type,owner_id,locale,title) VALUES('work',?,'en','legacy')`, game.ID); err == nil {
		t.Fatal("v16 accepted legacy localized-title owner type")
	}
	if _, err = migrated.db.ExecContext(ctx, `INSERT INTO editions(id,game_id,default_title,edition_type,save_namespace,created_at,updated_at) VALUES('orphan','missing','Orphan','original','orphan',?,?)`, nowText(), nowText()); err == nil {
		t.Fatal("v16 accepted an edition with a missing game")
	}
	if result, integrityErr := migrated.IntegrityCheck(ctx); integrityErr != nil || result != "ok" {
		t.Fatalf("integrity=%q err=%v", result, integrityErr)
	}
}

func TestMigrationFromV15NamingConflictRollsBack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v15-conflict.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Preserved", Platform: "gba"})
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
	if _, err = db.Exec(`CREATE TABLE works(id TEXT PRIMARY KEY); PRAGMA user_version=15`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if conflicted, openErr := Open(path); openErr == nil || !strings.Contains(openErr.Error(), "migration 16 naming") {
		if conflicted != nil {
			conflicted.Close()
		}
		t.Fatalf("mixed v15/v16 names migrated: %v", openErr)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, games, works, preserved int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='games'`).Scan(&games); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='works'`).Scan(&works); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM games WHERE id=?`, game.ID).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if version != 15 || games != 1 || works != 1 || preserved != 1 {
		t.Fatalf("failed migration changed database: version=%d games=%d works=%d preserved=%d", version, games, works, preserved)
	}
}

func TestSourceAdapterContractsAreVersionedAndReferenced(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	custom, err := store.CreateSourceAdapter(ctx, NewSourceAdapter{Name: "Reviewed Pegasus variant", Format: "pegasus-reviewed", Handler: "pegasus", ContractVersion: 3, Capabilities: map[string]bool{"metadata": true}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateLibrarySource(ctx, NewLibrarySource{Name: "Custom source", Kind: "pegasus", SourceAdapterID: custom.ID, MetadataPath: "gba/metadata.pegasus.txt", Platform: "gba"})
	if err != nil || source.SourceAdapterID != custom.ID {
		t.Fatalf("custom adapter source = %#v, %v", source, err)
	}
	if err = store.DeleteSourceAdapter(ctx, custom.ID); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("referenced adapter deletion = %v", err)
	}
	if _, err = store.UpdateSourceAdapter(ctx, custom.ID, NewSourceAdapter{Name: custom.Name, Format: custom.Format, Handler: "esde"}); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("referenced adapter handler mutation = %v", err)
	}
	if _, err = store.CreateLibrarySource(ctx, NewLibrarySource{Name: "Mismatched", Kind: "esde", SourceAdapterID: custom.ID, MetadataPath: "gamelists/gba/gamelist.xml", Platform: "gba"}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched source adapter was accepted: %v", err)
	}
	if _, err = store.UpdateSourceAdapter(ctx, "builtin-source-pegasus", NewSourceAdapter{Name: "Changed", Format: "changed", Handler: "pegasus"}); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("built-in source adapter was mutable: %v", err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE source_adapters SET handler='esde' WHERE id=?`, custom.ID); err == nil || !strings.Contains(err.Error(), "referenced source adapter handler") {
		t.Fatalf("database trigger accepted referenced handler drift: %v", err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE library_sources SET source_adapter_id=NULL WHERE id=?`, source.ID); err == nil || !strings.Contains(err.Error(), "source adapter must match") {
		t.Fatalf("database trigger accepted a source without an adapter: %v", err)
	}
}

func TestMigrationFromV6AddsPackageLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v6.db")
	s, err := Open(path)
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
	downgradeV9ToV8(t, db)
	if _, err = db.Exec(`DROP TABLE launch_bindings; DROP TABLE core_mappings; DROP TABLE retroarch_cores; DROP TABLE emulator_drivers; DROP TABLE device_profiles; DROP TABLE frontend_adapters; DROP TABLE package_releases; DROP TABLE package_plans; DROP TABLE package_config_templates; DROP TABLE package_profiles; PRAGMA user_version = 6;`); err != nil {
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
		t.Fatalf("schema version = %d, %v", version, err)
	}
	for _, table := range []string{"package_profiles", "package_config_templates", "package_plans", "package_releases"} {
		var name string
		if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("missing v7 table %s: %q, %v", table, name, err)
		}
	}
	for _, table := range []string{"frontend_adapters", "device_profiles", "emulator_drivers", "retroarch_cores", "core_mappings", "launch_bindings"} {
		var name string
		if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("missing v8 table %s: %q, %v", table, name, err)
		}
	}
}

func TestMigrationFromV7AddsRuntimeCatalogWithoutLosingPackageHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v7.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.CreatePackageProfile(ctx, NewPackageProfile{
		Name: "Preserved package", Frontend: "pegasus", Target: "windows", Locale: "en",
		FileMode: "copy", OutputSlug: "preserved-package",
		Templates: []NewPackageConfigTemplate{{Name: "Options", Scope: "package", OutputPath: "config/options.cfg", Body: "safe=true\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.CreatePackagePlan(ctx, NewPackagePlanRecord{ProfileID: profile.ID, Fingerprint: "v7-fingerprint", Status: "ready", PlanJSON: `{"items":[]}`, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.RecordPackageRelease(ctx, NewPackageReleaseRecord{ProfileID: profile.ID, PlanID: plan.ID, OutputSlug: profile.OutputSlug, ResultJSON: `{"copied_files":1}`}, "built")
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
	downgradeV9ToV8(t, db)
	if _, err = db.Exec(`
		DROP TABLE launch_bindings;
		DROP TABLE core_mappings;
		DROP TABLE retroarch_cores;
		DROP TABLE emulator_drivers;
		DROP TABLE device_profiles;
		DROP TABLE frontend_adapters;
		ALTER TABLE package_profiles DROP COLUMN device_profile_id;
		ALTER TABLE package_profiles DROP COLUMN frontend_adapter_id;
		PRAGMA user_version = 7;
	`); err != nil {
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
		t.Fatalf("schema version = %d, %v", version, err)
	}
	preserved, err := migrated.GetPackageProfile(ctx, profile.ID)
	if err != nil || preserved.Name != profile.Name || len(preserved.Templates) != 1 || preserved.DeviceProfileID != "" || preserved.FrontendAdapterID != "" {
		t.Fatalf("package profile changed across v7 migration: %#v, %v", preserved, err)
	}
	preservedPlan, err := migrated.GetPackagePlan(ctx, plan.ID)
	if err != nil || preservedPlan.Status != "built" || preservedPlan.Fingerprint != plan.Fingerprint {
		t.Fatalf("package plan changed across v7 migration: %#v, %v", preservedPlan, err)
	}
	releases, err := migrated.ListPackageReleases(ctx, profile.ID)
	if err != nil || len(releases) != 1 || releases[0].ID != release.ID || releases[0].Status != "succeeded" {
		t.Fatalf("package release changed across v7 migration: %#v, %v", releases, err)
	}
	for _, table := range []string{"frontend_adapters", "device_profiles", "emulator_drivers", "retroarch_cores", "core_mappings", "launch_bindings"} {
		var name string
		if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("missing v8 table %s: %q, %v", table, name, err)
		}
	}
}

func TestMigrationFromV8CreatesSaveStreamsAndPreservesLegacyRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v8-saves.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Save migration", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := s.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDevice(ctx, NewDevice{Name: "Legacy device", OSFamily: "windows", Architecture: "x86_64"})
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
	downgradeV9ToV8(t, db)
	createdA := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	createdB := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO save_revisions(id,edition_id,device_id,driver_id,relative_path,scope_type,scope_key,checksum,size,blob_path,base_revision_id,conflict,created_at) VALUES
		('legacy-revision-a',?,?,?,?,?,?,?,?,?,'',0,?),
		('legacy-revision-b',?,?,?,?,?,?,?,?,?,'legacy-revision-a',1,?)`,
		edition.ID, device.ID, "retroarch", "saves/game.srm", "game", "", "hash-a", 6, "/isolated/blobs/a", createdA,
		edition.ID, device.ID, "retroarch", "saves/game.srm", "game", "", "hash-b", 7, "/isolated/blobs/b", createdB); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA user_version = 8`); err != nil {
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
		t.Fatalf("schema version = %d, %v", version, err)
	}
	streams, err := migrated.ListSaveStreams(ctx, edition.ID)
	if err != nil || len(streams) != 1 || streams[0].OwnerType != "edition" || streams[0].DriverID != "retroarch" || len(streams[0].Editions) != 1 {
		t.Fatalf("migrated streams = %#v, %v", streams, err)
	}
	revisions, err := migrated.ListSaveRevisions(ctx, edition.ID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("migrated revisions = %#v, %v", revisions, err)
	}
	byID := map[string]SaveRevision{}
	for _, revision := range revisions {
		byID[revision.ID] = revision
	}
	if got := byID["legacy-revision-a"]; got.Status != "current" || got.FileCount != 1 || len(got.Files) != 1 || got.Files[0].Checksum != "hash-a" || got.Files[0].BlobPath != "/isolated/blobs/a" {
		t.Fatalf("legacy current revision changed: %#v", got)
	}
	if got := byID["legacy-revision-b"]; got.Status != "conflict" || got.ParentRevisionID != "legacy-revision-a" || !got.Conflict || got.Files[0].Checksum != "hash-b" {
		t.Fatalf("legacy conflict revision changed: %#v", got)
	}
	for _, table := range []string{"save_streams", "save_stream_editions", "save_revisions", "save_files", "save_bindings", "pairing_codes", "client_tokens", "sync_sessions", "sync_operations", "inventory_items", "runtime_import_hints"} {
		var name string
		if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("missing v9 table %s: %q, %v", table, name, err)
		}
	}
}

func TestPackageProfilePlanAndReleaseLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	profile, err := s.CreatePackageProfile(ctx, NewPackageProfile{Name: "Windows RA", Frontend: "pegasus", Target: "windows", Locale: "en", FileMode: "copy", OutputSlug: "windows-ra", Templates: []NewPackageConfigTemplate{{Name: "Options", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "rom={{rom.path}}"}}})
	if err != nil {
		t.Fatal(err)
	}
	if profile.OutputSlug != "windows-ra" || len(profile.Templates) != 1 || !profile.Enabled {
		t.Fatalf("unexpected package profile: %#v", profile)
	}
	plan, err := s.CreatePackagePlan(ctx, NewPackagePlanRecord{ProfileID: profile.ID, Fingerprint: "fingerprint", Status: "ready", PlanJSON: `{"items":[]}`, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.RecordPackageRelease(ctx, NewPackageReleaseRecord{ProfileID: profile.ID, PlanID: plan.ID, OutputSlug: profile.OutputSlug, ResultJSON: `{"copied_files":1}`}, "built")
	if err != nil {
		t.Fatal(err)
	}
	if release.Status != "succeeded" {
		t.Fatalf("unexpected release: %#v", release)
	}
	plan, err = s.GetPackagePlan(ctx, plan.ID)
	if err != nil || plan.Status != "built" {
		t.Fatalf("release and plan were not committed atomically: %#v, %v", plan, err)
	}
	if _, err = s.RecordPackageRelease(ctx, NewPackageReleaseRecord{ProfileID: profile.ID, PlanID: "missing-plan", OutputSlug: profile.OutputSlug, ResultJSON: `{}`}, "built"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing plan should roll back release: %v", err)
	}
	var releaseCount int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM package_releases`).Scan(&releaseCount); err != nil || releaseCount != 1 {
		t.Fatalf("failed atomic release left a row: %d, %v", releaseCount, err)
	}
	if err = s.DeletePackageProfile(ctx, profile.ID); !errors.Is(err, ErrPackageProfileHasHistory) {
		t.Fatalf("profile with release history was deleted: %v", err)
	}
	disabled := false
	profile, err = s.UpdatePackageProfile(ctx, profile.ID, NewPackageProfile{Name: profile.Name, Frontend: profile.Frontend, Target: profile.Target, Locale: profile.Locale, FileMode: profile.FileMode, OutputSlug: profile.OutputSlug, Enabled: &disabled, Templates: []NewPackageConfigTemplate{{Name: "Options", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "rom={{rom.path}}"}}})
	if err != nil || profile.Enabled {
		t.Fatalf("package profile disable failed: %#v, %v", profile, err)
	}
}

func TestMigrationFromV20AddsAuditedFrontendHandlersWithoutGuessingCustomFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-v20.db")
	ctx := context.Background()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	pegasus, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "v20-pegasus", Name: "Legacy Pegasus", Format: "pegasus", Handler: "pegasus", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "v20-custom", Name: "Legacy custom", Format: "legacy-custom", Handler: "es-de", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "v20-profile", Name: "Legacy profile", Frontend: "pegasus", Target: "windows", FrontendAdapterID: pegasus.ID, FileMode: "copy", OutputSlug: "v20-profile"})
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
	if _, err = db.Exec(`
		DROP TRIGGER IF EXISTS trg_frontend_adapters_handler_update;
		DROP TRIGGER IF EXISTS trg_package_profiles_frontend_insert;
		DROP TRIGGER IF EXISTS trg_package_profiles_frontend_update;
		ALTER TABLE frontend_adapters DROP COLUMN handler;
		PRAGMA user_version=20;
	`); err != nil {
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
	if version, versionErr := migrated.SchemaVersion(ctx); versionErr != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, %v", version, versionErr)
	}
	gotPegasus, err := migrated.GetFrontendAdapter(ctx, pegasus.ID)
	if err != nil || gotPegasus.Handler != "pegasus" {
		t.Fatalf("known v20 frontend handler was not restored: %#v, %v", gotPegasus, err)
	}
	gotCustom, err := migrated.GetFrontendAdapter(ctx, custom.ID)
	if err != nil || gotCustom.Handler != "" || gotCustom.Format != custom.Format {
		t.Fatalf("unknown v20 frontend format was guessed or changed: %#v, %v", gotCustom, err)
	}
	gotProfile, err := migrated.GetPackageProfile(ctx, profile.ID)
	if err != nil || gotProfile.FrontendAdapterID != pegasus.ID || gotProfile.Frontend != "pegasus" {
		t.Fatalf("v20 package profile changed: %#v, %v", gotProfile, err)
	}
	if _, err = migrated.CreatePackageProfile(ctx, NewPackageProfile{Name: "Unsafe legacy", Frontend: "es-de", Target: "portable", FrontendAdapterID: custom.ID, FileMode: "copy", OutputSlug: "unsafe-legacy"}); err == nil || !strings.Contains(err.Error(), "frontend adapter handler") {
		t.Fatalf("unbound legacy frontend was accepted by a package profile: %v", err)
	}
	bound, err := migrated.UpdateFrontendAdapter(ctx, custom.ID, NewFrontendAdapter{Name: custom.Name, Format: custom.Format, Handler: "es-de", ContractVersion: custom.ContractVersion, Capabilities: custom.Capabilities, SupportLevel: custom.SupportLevel, Evidence: custom.Evidence, Enabled: &enabled})
	if err != nil || bound.Handler != "es-de" {
		t.Fatalf("legacy frontend could not be explicitly bound: %#v, %v", bound, err)
	}
	if _, err = migrated.CreatePackageProfile(ctx, NewPackageProfile{Name: "Safe legacy", Frontend: "es-de", Target: "portable", FrontendAdapterID: custom.ID, FileMode: "copy", OutputSlug: "safe-legacy"}); err != nil {
		t.Fatalf("explicitly bound custom frontend was not usable: %v", err)
	}
	if _, err = migrated.UpdateFrontendAdapter(ctx, custom.ID, NewFrontendAdapter{Name: custom.Name, Format: custom.Format, Handler: "pegasus", ContractVersion: custom.ContractVersion, Capabilities: custom.Capabilities, SupportLevel: custom.SupportLevel, Evidence: custom.Evidence, Enabled: &enabled}); err == nil || !strings.Contains(err.Error(), "referenced frontend adapter handler") {
		t.Fatalf("referenced frontend handler drift was accepted: %v", err)
	}
}

func TestRuntimeCatalogCorePriorityAndLaunchBindings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enabled := true
	adapter, err := s.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "adapter", Name: "Test frontend", Format: "test", Capabilities: map[string]bool{"export": true}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateFrontendAdapter(ctx, adapter.ID, NewFrontendAdapter{Name: "changed", Format: "changed"}); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("builtin adapter was editable: %v", err)
	}
	device, err := s.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "device", Name: "Handheld", Target: "windows", OSFamily: "windows", PathStyle: "windows", DefaultFrontendID: adapter.ID, Paths: map[string]string{"config_dir": "config", "save_dir": "saves"}})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "driver", Name: "RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "unsafe", Family: "unsafe", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{env.HOME}}"}}, Save: DriverSaveSpec{Scope: "game"}}); err == nil {
		t.Fatal("unsafe launch argument was accepted")
	}
	if _, err = s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "unsafe save", Family: "unsafe-save", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game", Patterns: []string{"{{env.HOME}}/save"}}}); err == nil {
		t.Fatal("unsafe save pattern was accepted")
	}
	if _, err = s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "unsafe config", Family: "unsafe-config", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, ConfigPaths: map[string]string{"settings": "../../private"}}); err == nil {
		t.Fatal("unsafe config path was accepted")
	}
	if _, err = s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "platform saves", Family: "platform-saves", Platforms: []string{"gamecube", "wii"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game", ScopeByPlatform: map[string]string{"gamecube": "container"}, PatternsByPlatform: map[string][]string{"wii": {"{{driver.user_dir}}/Wii/title/{{edition.title_id_high}}/{{edition.title_id_low}}/data"}}}}); err != nil {
		t.Fatalf("safe platform-specific save contract rejected: %v", err)
	}
	androidDriver, err := s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "Android RA", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"android"}, Launch: DriverLaunchSpec{AndroidIntent: &AndroidIntentSpec{Package: "com.retroarch.aarch64", PackageCandidates: []string{" com.retroarch ", "com.retroarch.aarch64", "com.retroarch"}, Activity: "com.retroarch.browser.retroactivity.RetroActivityFuture", StringExtras: map[string]string{"ROM": "{{rom.uri}}"}, Flags: []string{"grant-read-uri"}}, Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}})
	if err != nil || androidDriver.Launch.AndroidIntent == nil || androidDriver.Launch.AndroidPackage != "com.retroarch.aarch64" || !slices.Equal(androidDriver.Launch.AndroidIntent.PackageCandidates, []string{"com.retroarch"}) {
		t.Fatalf("valid declarative Android intent was not stored: %#v, %v", androidDriver, err)
	}
	if _, err = s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "Unsafe Android candidate", Family: "unsafe-candidate", Platforms: []string{"gba"}, Targets: []string{"android"}, Launch: DriverLaunchSpec{AndroidIntent: &AndroidIntentSpec{Package: "com.example.emulator", PackageCandidates: []string{"not a package"}, Activity: ".MainActivity"}, Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}}); err == nil {
		t.Fatal("unsafe Android package candidate was accepted")
	}
	if _, err = s.CreateEmulatorDriver(ctx, NewEmulatorDriver{Name: "Unsafe Android", Family: "unsafe", Platforms: []string{"gba"}, Targets: []string{"android"}, Launch: DriverLaunchSpec{AndroidIntent: &AndroidIntentSpec{Package: "com.example.emulator", Activity: ".MainActivity", StringExtras: map[string]string{"ROM": "{{env.HOME}}"}}, Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}}); err == nil {
		t.Fatalf("unsafe Android intent template was accepted: %v", err)
	}
	coreIDs := []string{"global", "platform", "device", "edition"}
	for _, id := range coreIDs {
		if _, err = s.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "core-" + id, Name: id, LibraryNames: []string{id + "_libretro"}, Platforms: []string{"gba"}}); err != nil {
			t.Fatal(err)
		}
	}
	game, _ := s.CreateGame(ctx, NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := s.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = s.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", SHA256: "runtime-test-rom"})
	for _, mapping := range []NewCoreMapping{
		{ID: "mapping-global", ScopeType: "global", PlatformID: "gba", CoreID: "core-global"},
		{ID: "mapping-platform", ScopeType: "platform", PlatformID: "gba", CoreID: "core-platform"},
		{ID: "mapping-device", ScopeType: "device_profile", ScopeKey: device.ID, PlatformID: "gba", CoreID: "core-device"},
		{ID: "mapping-edition", ScopeType: "edition", ScopeKey: edition.ID, PlatformID: "gba", CoreID: "core-edition"},
	} {
		if _, err = s.CreateCoreMapping(ctx, mapping); err != nil {
			t.Fatal(err)
		}
	}
	resolution, err := s.ResolveCore(ctx, "gba", edition.ID, device.ID)
	if err != nil || resolution.Resolution != "edition" || resolution.Core == nil || resolution.Core.ID != "core-edition" {
		t.Fatalf("edition resolution = %#v, %v", resolution, err)
	}
	if err = s.DeleteCoreMapping(ctx, "mapping-edition"); err != nil {
		t.Fatal(err)
	}
	resolution, _ = s.ResolveCore(ctx, "gba", edition.ID, device.ID)
	if resolution.Resolution != "device_profile" || resolution.Core.ID != "core-device" {
		t.Fatalf("device resolution = %#v", resolution)
	}
	binding, err := s.CreateLaunchBinding(ctx, NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID, FrontendAdapterID: adapter.ID})
	if err != nil {
		t.Fatal(err)
	}
	resolvedBinding, err := s.ResolveLaunchBinding(ctx, edition.ID, device.ID)
	if err != nil || resolvedBinding.ID != binding.ID {
		t.Fatalf("launch binding resolution = %#v, %v", resolvedBinding, err)
	}
}

func TestLibrarySourceLifecyclePreservesScanHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	source, err := s.CreateLibrarySource(ctx, NewLibrarySource{Name: "GBA collection", Kind: "rom_directory", RootPath: "gba", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	if source.RootPath != "gba" || !source.Enabled || source.LastScanStatus != "never" {
		t.Fatalf("unexpected source: %#v", source)
	}
	ready, err := s.CreateSourceScan(ctx, NewSourceScan{SourceID: source.ID, Status: "ready", StartedAt: time.Now(), FinishedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), CandidateCount: 3, ImportableCount: 1, MissingCount: 1, DuplicateCount: 1, PreviewTokenHash: "digest"})
	if err != nil {
		t.Fatal(err)
	}
	if ready.CandidateCount != 3 || ready.ImportableCount != 1 {
		t.Fatalf("unexpected scan: %#v", ready)
	}
	if _, err = s.UpdateSourceScanStatus(ctx, ready.ID, "committed", "", ""); err != nil {
		t.Fatal(err)
	}
	scans, err := s.ListSourceScans(ctx, source.ID)
	if err != nil || len(scans) != 1 || scans[0].Status != "committed" {
		t.Fatalf("scan history = %#v, %v", scans, err)
	}
	if err = s.DeleteLibrarySource(ctx, source.ID); !errors.Is(err, ErrSourceHasScans) {
		t.Fatalf("source with audit history was deleted: %v", err)
	}
	disabled := false
	source, err = s.UpdateLibrarySource(ctx, source.ID, NewLibrarySource{Name: source.Name, Kind: source.Kind, RootPath: source.RootPath, Platform: source.Platform, ROMStoragePolicy: source.ROMStoragePolicy, MediaStoragePolicy: source.MediaStoragePolicy, Enabled: &disabled})
	if err != nil || source.Enabled {
		t.Fatalf("source disable failed: %#v, %v", source, err)
	}
}

func TestMigrationFromV22ProtectsBuiltinNamespaceAndCoreMappings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builtin-ownership-v22.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	enabled := true
	core, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "builtin-core-v23-fixture", Name: "Fixture core", LibraryNames: []string{"fixture_libretro"}, Platforms: []string{"gba", "gbc"}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	builtinMapping, err := store.CreateCoreMapping(ctx, NewCoreMapping{ID: "builtin-mapping-global-v23-fixture", ScopeType: "global", PlatformID: "gba", CoreID: core.ID, Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	customMapping, err := store.CreateCoreMapping(ctx, NewCoreMapping{ID: "custom-v23-mapping", ScopeType: "global", PlatformID: "gbc", CoreID: core.ID})
	if err != nil {
		t.Fatal(err)
	}
	builtinProfile, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "builtin-package-v24-fixture", Name: "Built-in package", Frontend: "pegasus", Target: "portable", FileMode: "reference", OutputSlug: "builtin-v24-package", Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	customProfile, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "custom-v24-package", Name: "Custom package", Frontend: "pegasus", Target: "portable", FileMode: "reference", OutputSlug: "custom-v24-package"})
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
	if _, err = db.Exec(`
		DROP TRIGGER IF EXISTS trg_core_mappings_builtin_namespace_insert;
		DROP TRIGGER IF EXISTS trg_package_profiles_builtin_namespace_insert;
		DROP TRIGGER IF EXISTS trg_core_mappings_builtin_ownership_update;
		DROP TRIGGER IF EXISTS trg_package_profiles_builtin_ownership_update;
		ALTER TABLE core_mappings DROP COLUMN builtin;
		ALTER TABLE package_profiles DROP COLUMN builtin;
		PRAGMA user_version=22;
	`); err != nil {
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
	if version, versionErr := migrated.SchemaVersion(ctx); versionErr != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, versionErr)
	}
	gotBuiltin, err := migrated.GetCoreMapping(ctx, builtinMapping.ID)
	if err != nil || !gotBuiltin.Builtin {
		t.Fatalf("migrated built-in mapping=%#v err=%v", gotBuiltin, err)
	}
	gotCustom, err := migrated.GetCoreMapping(ctx, customMapping.ID)
	if err != nil || gotCustom.Builtin {
		t.Fatalf("migrated custom mapping=%#v err=%v", gotCustom, err)
	}
	if _, err = migrated.UpdateCoreMapping(ctx, builtinMapping.ID, NewCoreMapping{ScopeType: "global", PlatformID: "gba", CoreID: core.ID}); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("built-in mapping update err=%v", err)
	}
	if err = migrated.DeleteCoreMapping(ctx, builtinMapping.ID); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("built-in mapping delete err=%v", err)
	}
	if _, err = migrated.UpdateCoreMapping(ctx, customMapping.ID, NewCoreMapping{ScopeType: "global", PlatformID: "gbc", CoreID: core.ID, Notes: "still editable"}); err != nil {
		t.Fatal(err)
	}
	gotBuiltinProfile, err := migrated.GetPackageProfile(ctx, builtinProfile.ID)
	if err != nil || !gotBuiltinProfile.Builtin {
		t.Fatalf("migrated built-in package profile=%#v err=%v", gotBuiltinProfile, err)
	}
	gotCustomProfile, err := migrated.GetPackageProfile(ctx, customProfile.ID)
	if err != nil || gotCustomProfile.Builtin {
		t.Fatalf("migrated custom package profile=%#v err=%v", gotCustomProfile, err)
	}
	if _, err = migrated.UpdatePackageProfile(ctx, builtinProfile.ID, NewPackageProfile{Name: "Changed", Frontend: builtinProfile.Frontend, Target: builtinProfile.Target, FileMode: builtinProfile.FileMode, OutputSlug: builtinProfile.OutputSlug}); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("built-in package update err=%v", err)
	}
	if err = migrated.DeletePackageProfile(ctx, builtinProfile.ID); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("built-in package delete err=%v", err)
	}
	if _, err = migrated.UpdatePackageProfile(ctx, customProfile.ID, NewPackageProfile{Name: "Still editable", Frontend: customProfile.Frontend, Target: customProfile.Target, FileMode: customProfile.FileMode, OutputSlug: customProfile.OutputSlug}); err != nil {
		t.Fatal(err)
	}

	reservedCreates := []func() error{
		func() error {
			_, err := migrated.CreateSourceAdapter(ctx, NewSourceAdapter{ID: "builtin-user-source", Name: "User source", Format: "user-source", Handler: "pegasus"})
			return err
		},
		func() error {
			_, err := migrated.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "builtin-user-frontend", Name: "User frontend", Format: "user-frontend", Handler: "pegasus"})
			return err
		},
		func() error {
			_, err := migrated.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "builtin-user-device", Name: "User device", Target: "portable", OSFamily: "portable"})
			return err
		},
		func() error {
			_, err := migrated.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "builtin-user-driver", Name: "User driver", Family: "fixture", Platforms: []string{"gba"}, Targets: []string{"portable"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}})
			return err
		},
		func() error {
			_, err := migrated.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "builtin-user-core", Name: "User core", LibraryNames: []string{"user_libretro"}, Platforms: []string{"gba"}})
			return err
		},
		func() error {
			_, err := migrated.CreateCoreMapping(ctx, NewCoreMapping{ID: "builtin-user-mapping", ScopeType: "platform", PlatformID: "gba", CoreID: core.ID})
			return err
		},
		func() error {
			_, err := migrated.CreatePackageProfile(ctx, NewPackageProfile{ID: "builtin-user-package", Name: "User package", Frontend: "pegasus", Target: "portable", FileMode: "reference", OutputSlug: "user-package"})
			return err
		},
	}
	for index, create := range reservedCreates {
		if err = create(); !errors.Is(err, ErrBuiltinNamespaceReserved) {
			t.Fatalf("reserved create %d err=%v", index, err)
		}
	}
	now := nowText()
	if _, err = migrated.db.ExecContext(ctx, `INSERT INTO source_adapters(id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES('builtin-direct-sql','Direct SQL','direct-sql','pegasus',1,'{}','catalogued','{}',0,1,?,?)`, now, now); err == nil || !strings.Contains(err.Error(), "builtin namespace is reserved") {
		t.Fatalf("database source namespace trigger err=%v", err)
	}
	if _, err = migrated.db.ExecContext(ctx, `INSERT INTO core_mappings(id,scope_type,scope_key,platform_id,core_id,priority,notes,builtin,created_at,updated_at) VALUES('builtin-direct-mapping','platform','','gba',?,0,'',0,?,?)`, core.ID, now, now); err == nil || !strings.Contains(err.Error(), "builtin namespace is reserved") {
		t.Fatalf("database mapping namespace trigger err=%v", err)
	}
	if _, err = migrated.db.ExecContext(ctx, `INSERT INTO package_profiles(id,name,frontend,target,locale,file_mode,output_slug,enabled,builtin,created_at,updated_at,device_profile_id,frontend_adapter_id) VALUES('builtin-direct-package','Direct package','pegasus','portable','en','reference','direct-package',1,0,?,?, '', '')`, now, now); err == nil || !strings.Contains(err.Error(), "builtin namespace is reserved") {
		t.Fatalf("database package namespace trigger err=%v", err)
	}
}

func TestMigrationFromV23ProtectsBuiltinPackageProfilesWithoutLosingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builtin-package-v23.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	builtin, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "builtin-package-v24-history", Name: "Built-in history", Frontend: "pegasus", Target: "portable", Locale: "en", FileMode: "reference", OutputSlug: "builtin-v24-history", Builtin: true, Templates: []NewPackageConfigTemplate{{Name: "Options", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "rom={{rom.path}}\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "custom-package-v24-history", Name: "Custom history", Frontend: "pegasus", Target: "portable", Locale: "en", FileMode: "reference", OutputSlug: "custom-v24-history"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreatePackagePlan(ctx, NewPackagePlanRecord{ProfileID: builtin.ID, Fingerprint: "v23-history-fingerprint", PlanJSON: `{"format":"fixture"}`, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.CreatePackageRelease(ctx, NewPackageReleaseRecord{ProfileID: builtin.ID, PlanID: plan.ID, OutputSlug: builtin.OutputSlug, ResultJSON: `{"status":"fixture"}`})
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
	if _, err = db.Exec(`DROP TRIGGER IF EXISTS trg_package_profiles_builtin_namespace_insert; DROP TRIGGER IF EXISTS trg_package_profiles_builtin_ownership_update; ALTER TABLE package_profiles DROP COLUMN builtin; PRAGMA user_version=23;`); err != nil {
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
	if version, versionErr := migrated.SchemaVersion(ctx); versionErr != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, versionErr)
	}
	gotBuiltin, err := migrated.GetPackageProfile(ctx, builtin.ID)
	if err != nil || !gotBuiltin.Builtin || len(gotBuiltin.Templates) != 1 || gotBuiltin.Templates[0].Body != builtin.Templates[0].Body {
		t.Fatalf("migrated built-in profile=%#v err=%v", gotBuiltin, err)
	}
	gotCustom, err := migrated.GetPackageProfile(ctx, custom.ID)
	if err != nil || gotCustom.Builtin {
		t.Fatalf("migrated custom profile=%#v err=%v", gotCustom, err)
	}
	if _, err = migrated.GetPackagePlan(ctx, plan.ID); err != nil {
		t.Fatalf("package plan history lost: %v", err)
	}
	if _, err = migrated.GetPackageRelease(ctx, release.ID); err != nil {
		t.Fatalf("package release history lost: %v", err)
	}
	if _, err = migrated.UpdatePackageProfile(ctx, builtin.ID, NewPackageProfile{Name: builtin.Name, Frontend: builtin.Frontend, Target: builtin.Target, Locale: builtin.Locale, FileMode: builtin.FileMode, OutputSlug: builtin.OutputSlug}); !errors.Is(err, ErrBuiltinImmutable) {
		t.Fatalf("migrated built-in profile update err=%v", err)
	}
	if _, err = migrated.UpdatePackageProfile(ctx, custom.ID, NewPackageProfile{Name: "Editable", Frontend: custom.Frontend, Target: custom.Target, Locale: custom.Locale, FileMode: custom.FileMode, OutputSlug: custom.OutputSlug}); err != nil {
		t.Fatalf("migrated custom profile edit err=%v", err)
	}
}

func TestMigrationFromV24LocksRuntimeOwnershipWithoutChangingCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builtin-ownership-v24.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	enabled := true

	builtinSource, err := store.CreateSourceAdapter(ctx, NewSourceAdapter{ID: "builtin-source-v25-fixture", Name: "Built-in source", Format: "fixture-source", Handler: "pegasus", Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	customSource, err := store.CreateSourceAdapter(ctx, NewSourceAdapter{ID: "custom-source-v25-fixture", Name: "Custom source", Format: "fixture-source-custom", Handler: "pegasus", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	builtinFrontend, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "builtin-frontend-v25-fixture", Name: "Built-in frontend", Format: "fixture-pegasus", Handler: "pegasus", Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	customFrontend, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "custom-frontend-v25-fixture", Name: "Custom frontend", Format: "fixture-esde", Handler: "es-de", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	builtinDevice, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "builtin-device-v25-fixture", Name: "Built-in device", Target: "fixture-built-in", OSFamily: "portable", DefaultFrontendID: builtinFrontend.ID, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	customDevice, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "custom-device-v25-fixture", Name: "Custom device", Target: "fixture-custom", OSFamily: "portable", DefaultFrontendID: customFrontend.ID, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	builtinDriver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "builtin-driver-v25-fixture", Name: "Built-in driver", Family: "fixture-built-in", Platforms: []string{"gba"}, Targets: []string{builtinDevice.Target}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	customDriver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "custom-driver-v25-fixture", Name: "Custom driver", Family: "fixture-custom", Platforms: []string{"gbc"}, Targets: []string{customDevice.Target}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	builtinCore, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "builtin-core-v25-fixture", Name: "Built-in core", LibraryNames: []string{"fixture_builtin_libretro"}, Platforms: []string{"gba"}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	customCore, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "custom-core-v25-fixture", Name: "Custom core", LibraryNames: []string{"fixture_custom_libretro"}, Platforms: []string{"gbc"}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	builtinMapping, err := store.CreateCoreMapping(ctx, NewCoreMapping{ID: "builtin-mapping-v25-fixture", ScopeType: "global", PlatformID: "gba", CoreID: builtinCore.ID, Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	customMapping, err := store.CreateCoreMapping(ctx, NewCoreMapping{ID: "custom-mapping-v25-fixture", ScopeType: "global", PlatformID: "gbc", CoreID: customCore.ID})
	if err != nil {
		t.Fatal(err)
	}
	builtinProfile, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "builtin-package-v25-fixture", Name: "Built-in package", Frontend: "pegasus", Target: builtinDevice.Target, DeviceProfileID: builtinDevice.ID, FrontendAdapterID: builtinFrontend.ID, Locale: "en", FileMode: "reference", OutputSlug: "fixture-built-in", Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	customProfile, err := store.CreatePackageProfile(ctx, NewPackageProfile{ID: "custom-package-v25-fixture", Name: "Custom package", Frontend: "es-de", Target: customDevice.Target, DeviceProfileID: customDevice.ID, FrontendAdapterID: customFrontend.ID, Locale: "en", FileMode: "reference", OutputSlug: "fixture-custom", Enabled: &enabled})
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
	if _, err = db.Exec(`
		DROP TRIGGER trg_source_adapters_builtin_ownership_update;
		DROP TRIGGER trg_frontend_adapters_builtin_ownership_update;
		DROP TRIGGER trg_device_profiles_builtin_ownership_update;
		DROP TRIGGER trg_emulator_drivers_builtin_ownership_update;
		DROP TRIGGER trg_retroarch_cores_builtin_ownership_update;
		DROP TRIGGER trg_core_mappings_builtin_ownership_update;
		DROP TRIGGER trg_package_profiles_builtin_ownership_update;
		PRAGMA user_version=24;
	`); err != nil {
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
	if version, versionErr := migrated.SchemaVersion(ctx); versionErr != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, versionErr)
	}
	type ownershipFixture struct {
		table     string
		builtinID string
		customID  string
		field     string
	}
	fixtures := []ownershipFixture{
		{"source_adapters", builtinSource.ID, customSource.ID, "name"},
		{"frontend_adapters", builtinFrontend.ID, customFrontend.ID, "name"},
		{"device_profiles", builtinDevice.ID, customDevice.ID, "name"},
		{"emulator_drivers", builtinDriver.ID, customDriver.ID, "name"},
		{"retroarch_cores", builtinCore.ID, customCore.ID, "name"},
		{"core_mappings", builtinMapping.ID, customMapping.ID, "notes"},
		{"package_profiles", builtinProfile.ID, customProfile.ID, "name"},
	}
	for _, fixture := range fixtures {
		var builtin, custom int
		if err = migrated.db.QueryRow(`SELECT builtin FROM `+fixture.table+` WHERE id=?`, fixture.builtinID).Scan(&builtin); err != nil {
			t.Fatal(err)
		}
		if err = migrated.db.QueryRow(`SELECT builtin FROM `+fixture.table+` WHERE id=?`, fixture.customID).Scan(&custom); err != nil {
			t.Fatal(err)
		}
		if builtin != 1 || custom != 0 {
			t.Fatalf("%s ownership changed: builtin=%d custom=%d", fixture.table, builtin, custom)
		}
		if _, err = migrated.db.Exec(`UPDATE `+fixture.table+` SET builtin=0 WHERE id=?`, fixture.builtinID); err == nil || !strings.Contains(err.Error(), "builtin ownership and namespace are immutable") {
			t.Fatalf("%s allowed built-in demotion: %v", fixture.table, err)
		}
		if _, err = migrated.db.Exec(`UPDATE `+fixture.table+` SET builtin=1 WHERE id=?`, fixture.customID); err == nil || !strings.Contains(err.Error(), "builtin ownership and namespace are immutable") {
			t.Fatalf("%s allowed ownership promotion: %v", fixture.table, err)
		}
		if _, err = migrated.db.Exec(`UPDATE `+fixture.table+` SET id=? WHERE id=?`, "builtin-hijack-"+fixture.table, fixture.customID); err == nil || !strings.Contains(err.Error(), "builtin ownership and namespace are immutable") {
			t.Fatalf("%s allowed reserved rename: %v", fixture.table, err)
		}
		if _, err = migrated.db.Exec(`UPDATE `+fixture.table+` SET `+fixture.field+`=`+fixture.field+`||' updated' WHERE id=?`, fixture.customID); err != nil {
			t.Fatalf("%s custom content update failed: %v", fixture.table, err)
		}
	}

	// Built-in reconciliation changes contract content without changing its
	// ownership or identifier, and must remain possible during upgrades.
	if _, err = migrated.ReconcileBuiltinSourceAdapter(ctx, NewSourceAdapter{ID: builtinSource.ID, Name: "Built-in source v2", Format: builtinSource.Format, Handler: builtinSource.Handler, ContractVersion: builtinSource.ContractVersion + 1, Builtin: true, Enabled: &enabled}); err != nil {
		t.Fatalf("built-in reconciliation blocked by ownership trigger: %v", err)
	}
}

func TestMigrationFromV24RejectsReservedIdentifierWithoutOwnershipAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tampered-v24.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	custom, err := store.CreateSourceAdapter(ctx, NewSourceAdapter{ID: "custom-source-before-v25", Name: "Custom source", Format: "custom-source", Handler: "pegasus"})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TRIGGER trg_source_adapters_builtin_ownership_update`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE source_adapters SET id='builtin-tampered-source' WHERE id=?; PRAGMA user_version=24;`, custom.ID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err = Open(path); err == nil || !strings.Contains(err.Error(), `source_adapters contains reserved identifier "builtin-tampered-source" without application ownership`) {
		t.Fatalf("tampered v24 migration error=%v", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, builtin int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT builtin FROM source_adapters WHERE id='builtin-tampered-source'`).Scan(&builtin); err != nil {
		t.Fatal(err)
	}
	if version != 24 || builtin != 0 {
		t.Fatalf("failed migration changed state: version=%d builtin=%d", version, builtin)
	}
}

func TestMigrationFromV4RejectsDuplicateArtifactHashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate-v4.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	work, err := s.CreateGame(ctx, NewGame{DefaultTitle: "Legacy duplicates", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	for index, romPath := range []string{"gba/a.gba", "gba/b.gba"} {
		edition, addErr := s.AddEdition(ctx, NewEdition{GameID: work.ID, DefaultTitle: fmt.Sprintf("Edition %d", index+1), EditionType: "revision"})
		if addErr != nil {
			t.Fatal(addErr)
		}
		if _, addErr = s.AddArtifact(ctx, NewArtifact{EditionID: edition.ID, Path: romPath}); addErr != nil {
			t.Fatal(addErr)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP INDEX idx_artifacts_sha_unique; UPDATE artifacts SET sha256='legacy-duplicate'; PRAGMA user_version = 4;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err = Open(path); err == nil || !strings.Contains(err.Error(), "duplicate artifact SHA-256") {
		t.Fatalf("expected actionable duplicate migration error, got %v", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("failed migration advanced schema to %d", version)
	}
}
