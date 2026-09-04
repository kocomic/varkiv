package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"varkiv/internal/buildinfo"
	"varkiv/internal/bundler"
	"varkiv/internal/catalog"
	"varkiv/internal/deviceagent"
	"varkiv/internal/server"
)

func TestLoopbackAddress(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"0.0.0.0:8080", false},
		{"192.168.1.20:8080", false},
		{":8080", false},
		{"not-an-address", false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			if got := loopbackAddress(test.address); got != test.want {
				t.Fatalf("loopbackAddress(%q) = %v, want %v", test.address, got, test.want)
			}
		})
	}
}

func TestVersionCommandIdentity(t *testing.T) {
	var output bytes.Buffer
	if err := versionCommand(nil, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Varkiv "+buildinfo.Version+"\n"; got != want {
		t.Fatalf("plain version = %q, want %q", got, want)
	}

	output.Reset()
	if err := versionCommand([]string{"--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Format             string `json:"format"`
		ApplicationVersion string `json:"application_version"`
	}
	if err := json.Unmarshal(output.Bytes(), &identity); err != nil {
		t.Fatalf("decode version identity: %v", err)
	}
	if identity.Format != "varkiv-version-v1" || identity.ApplicationVersion != buildinfo.Version {
		t.Fatalf("version identity = %#v", identity)
	}
	if err := versionCommand([]string{"unexpected"}, &output); err == nil {
		t.Fatal("version accepted a positional argument")
	}
}

func TestParseDriverRootsRequiresExactRealDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "PCSX2-user")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := parseDriverRoots([]string{"builtin-driver-pcsx2=" + root})
	if err != nil || got["builtin-driver-pcsx2"] != root {
		t.Fatalf("driver roots = %#v, %v", got, err)
	}
	if _, err = parseDriverRoots([]string{"bad/id=" + root}); err == nil {
		t.Fatal("unsafe driver ID was accepted")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "linked")
		if err = os.Symlink(root, link); err == nil {
			if _, err = parseDriverRoots([]string{"builtin-driver-pcsx2=" + link}); err == nil {
				t.Fatal("symbolic-link driver root was accepted")
			}
		}
	}
}

func TestMetadataOnlyExportsRequireExplicitHostPathConsent(t *testing.T) {
	for name, run := range map[string]func([]string) error{
		"pegasus": exportPegasus,
		"es-de":   exportESDE,
	} {
		t.Run(name, func(t *testing.T) {
			err := run([]string{"--db", filepath.Join(t.TempDir(), "missing.db"), "--out", filepath.Join(t.TempDir(), "out")})
			if err == nil || !strings.Contains(err.Error(), "--allow-host-paths") {
				t.Fatalf("metadata-only export was not safely rejected: %v", err)
			}
		})
	}
}

func TestResolveMetadataSourceAcceptsLibraryRelativeAndPreservesExplicitPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	libraryMetadata := filepath.Join(root, "gba", "metadata.pegasus.txt")
	if err := os.WriteFile(libraryMetadata, []byte("game: fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveMetadataSource(root, "gba/metadata.pegasus.txt"); got != libraryMetadata {
		t.Fatalf("library-relative metadata = %q, want %q", got, libraryMetadata)
	}
	explicit := filepath.Join(t.TempDir(), "external.xml")
	if err := os.WriteFile(explicit, []byte("<gameList/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveMetadataSource(root, explicit); got != explicit {
		t.Fatalf("explicit metadata = %q, want %q", got, explicit)
	}
}

func TestCLIRuntimeRoundTripUsesSavedProfileBindingAndTemplate(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "portable-runtime-v2"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dbPath := filepath.Join(root, "runtime.db")
	manifest := filepath.Join(fixture, "library-manifest.json")
	if err = importVarkiv([]string{"--db", dbPath, "--library", fixture, "--source", manifest}); err != nil {
		t.Fatal(err)
	}
	store, err := openExistingDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	hints, err := store.ListRuntimeImportHints(context.Background(), "e2e-runtime-v2-edition", "pending")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if len(hints) != 1 || hints[0].Trust != "structured" {
		store.Close()
		t.Fatalf("pending structured hints = %#v", hints)
	}
	if _, err = store.GetFrontendAdapter(context.Background(), "builtin-frontend-pegasus"); err != nil {
		store.Close()
		t.Fatalf("CLI database did not receive shared built-ins: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = runtimeHintCommand([]string{"apply", "--db", dbPath, "--id", hints[0].ID}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "pack")
	if err = buildPack([]string{"--db", dbPath, "--library", fixture, "--out", out, "--profile-id", "e2e-runtime-v2-profile"}); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(out, "config", "e2e-runtime-v2-edition.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != "core=e2e_mgba_libretro\nrom=gba/runtime-v2.gba\n" {
		t.Fatalf("rendered reviewed template = %q", config)
	}
	launches, err := os.ReadFile(filepath.Join(out, "varkiv-launches.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"e2e-runtime-v2-device", "e2e-runtime-v2-driver", "e2e-runtime-v2-core", "e2e-runtime-v2-profile", "e2e_mgba_libretro"} {
		if !strings.Contains(string(launches), required) {
			t.Fatalf("portable launch manifest omitted %q: %s", required, launches)
		}
	}
	if err = buildPack([]string{"--db", dbPath, "--library", fixture, "--out", filepath.Join(root, "conflict"), "--profile-id", "e2e-runtime-v2-profile", "--target", "windows"}); err == nil || !strings.Contains(err.Error(), "remove conflicting flags") {
		t.Fatalf("profile override was not rejected: %v", err)
	}
}

func TestBuildPackReadsManagedROMAndMediaFromStateRoot(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "library")
	state := filepath.Join(root, "managed-state")
	dbPath := filepath.Join(root, "library.db")
	if err := os.MkdirAll(library, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Managed fixture", Platform: "gba"})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	rom := []byte("managed-rom-fixture")
	romSum := sha256.Sum256(rom)
	romRel := "gba/managed.gba"
	romPath := filepath.Join(state, "roms", filepath.FromSlash(romRel))
	if err = os.MkdirAll(filepath.Dir(romPath), 0o700); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = os.WriteFile(romPath, rom, 0o600); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: romRel, StorageKind: "managed", OriginalName: "managed.gba", Role: "rom", Size: int64(len(rom)), SHA256: hex.EncodeToString(romSum[:])}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	media := []byte("managed-cover-fixture")
	mediaSum := sha256.Sum256(media)
	mediaDigest := hex.EncodeToString(mediaSum[:])
	mediaRel := filepath.ToSlash(filepath.Join("blobs", "sha256", mediaDigest[:2], mediaDigest+".png"))
	mediaPath := filepath.Join(state, "media", filepath.FromSlash(mediaRel))
	if err = os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = os.WriteFile(mediaPath, media, 0o600); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err = store.AddMedia(ctx, catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: mediaRel, OriginalName: "cover.png", MIMEType: "image/png", Size: int64(len(media)), SHA256: mediaDigest}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "pack")
	if err = buildPack([]string{"--db", dbPath, "--library", library, "--state", state, "--out", out, "--name", "managed-cli", "--frontend", "es-de", "--target", "portable", "--locale", "en", "--mode", "copy"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{filepath.Join(out, filepath.FromSlash(romRel)): rom, filepath.Join(out, filepath.FromSlash(mediaRel)): media} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != string(want) {
			t.Fatalf("managed output %s = %q, want %q", path, got, want)
		}
	}
}

func TestPublicRuntimeHintOmitsRawCommandAndSourcePath(t *testing.T) {
	item := catalog.RuntimeImportHint{ID: "hint", EditionID: "edition", SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: "private command", SourceRef: "private/path/metadata.pegasus.txt", Trust: "untrusted", Status: "pending"}
	encoded, err := json.Marshal(publicRuntimeHint(item))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{item.RawCommand, item.SourceRef} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("runtime hint view leaked %q: %s", private, encoded)
		}
	}
}

func TestSanitizeCommandErrorRemovesPrivateArgumentsAndDerivedPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-library")
	token := "owner-token-that-must-not-appear"
	server := "https://private-device.example.test"
	t.Setenv("GAME_LIBRARY_TOKEN", token)
	source := errors.New("open " + filepath.Join(root, "gba", "private-game.gba") + " through " + server + " using " + token)
	got := sanitizeCommandError(source, []string{"--library", root, "--server=" + server})
	for _, private := range []string{root, "private-game.gba", server, token} {
		if strings.Contains(got.Error(), private) {
			t.Fatalf("sanitized error retained private value %q: %v", private, got)
		}
	}
	if !strings.Contains(got.Error(), "<redacted>") {
		t.Fatalf("sanitized error lost its useful placeholder: %v", got)
	}
}

func TestPrivateStatusAndBuildOutputContainNoIdentifiersOrPaths(t *testing.T) {
	config := deviceagent.Config{
		ServerURL:       "https://private-device.example.test",
		DeviceID:        "device-private-identifier",
		DeviceProfileID: "custom-private-profile",
		RootDir:         filepath.Join(t.TempDir(), "private-root"),
		Streams:         map[string]deviceagent.StreamState{"private-stream": {}},
		LastSync:        &deviceagent.AgentSyncStatus{State: "failed", AttemptedAt: "2026-08-27T12:00:00Z", FinishedAt: "2026-08-27T12:00:01Z", ErrorCode: "sync_failed"},
	}
	status := formatAgentStatus(config, "yes")
	for _, private := range []string{config.ServerURL, config.DeviceID, config.DeviceProfileID, config.RootDir, "private-stream"} {
		if strings.Contains(status, private) {
			t.Fatalf("agent status retained private value %q: %s", private, status)
		}
	}
	if status != "server_configured=true device_paired=true profile_configured=true root_configured=true streams=1 pending=yes last_sync=failed session_recorded=false uploaded=0 downloaded=0 conflicts=0 error_code=sync_failed\n" {
		t.Fatalf("unexpected private agent status: %q", status)
	}
	encoded, err := json.Marshal(agentStatusView(config, "yes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{config.ServerURL, config.DeviceID, config.DeviceProfileID, config.RootDir, "private-stream"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("agent status JSON retained private value %q: %s", private, encoded)
		}
	}

	result := bundler.Result{
		Output:           filepath.Join(t.TempDir(), "private-package"),
		Exported:         2,
		Copied:           1,
		Missing:          1,
		Warnings:         []string{"missing: private/game.gba"},
		RecoverySnapshot: ".varkiv-backups/private-snapshot",
	}
	build := formatBuildPackResult(result)
	for _, private := range []string{result.Output, result.Warnings[0], result.RecoverySnapshot, "private-package", "private-snapshot"} {
		if strings.Contains(build, private) {
			t.Fatalf("build result retained private value %q: %s", private, build)
		}
	}
	if !strings.Contains(build, "warnings=1") || !strings.Contains(build, "recovery_snapshot=true") {
		t.Fatalf("build result lost diagnostic counts: %s", build)
	}
}

func TestCompleteStateBackupCommandsRoundTripToNewRoot(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(state, "library.db")
	store, err := catalog.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateGame(t.Context(), catalog.NewGame{DefaultTitle: "CLI fixture", Platform: "gba"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	if err = backupState([]string{"--db", database, "--state", state, "--out", backup}); err != nil {
		t.Fatal(err)
	}
	if err = checkState([]string{"--from", backup}); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(root, "restored")
	if err = restoreState([]string{"--from", backup, "--out", restored}); err != nil {
		t.Fatal(err)
	}
	if err = dbCheck([]string{"--db", filepath.Join(restored, "library.db")}); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseCheckIsReadOnlyAndDoesNotMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "older.db")
	store, err := catalog.Open(path)
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
	if _, err = db.Exec(`PRAGMA user_version = 15`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = dbCheck([]string{"--db", path}); err == nil || !strings.Contains(err.Error(), "schema 15") {
		t.Fatalf("older schema was not safely rejected: %v", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if version != 15 {
		t.Fatalf("db-check migrated schema to %d", version)
	}
}

func TestReleaseAuditKeepsExternalAndHardwareGatesClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "release-audit.db")
	store, err := catalog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.New(store, filepath.Join(root, "library")); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = releaseAudit([]string{"--db", path, "--json", "--require-hardware"}); err == nil || !strings.Contains(err.Error(), "real-device release evidence gates are pending") || strings.Contains(err.Error(), root) {
		t.Fatalf("unverified release audit error=%v", err)
	}
	readonly, err := catalog.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	version, err := readonly.SchemaVersion(context.Background())
	if err != nil || version != catalog.CurrentSchemaVersion {
		t.Fatalf("release audit changed schema version=%d err=%v", version, err)
	}
}

func TestReleaseAuditExternalGatesSeparateLicenseFromContributionRights(t *testing.T) {
	gates := currentExternalReleaseGates()
	want := []externalReleaseGate{
		{ID: "formal-product-name", Status: "external-review-required"},
		{ID: "project-license", Status: "ready"},
		{ID: "contribution-rights", Status: "ready"},
		{ID: "protected-release-authorization", Status: "external-review-required"},
	}
	if !reflect.DeepEqual(gates, want) {
		t.Fatalf("external release gates = %#v, want %#v", gates, want)
	}
}

func TestReleaseAuditJSONV3IsStableAndPrivacyMinimized(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private-library.db")
	store, err := catalog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	privateTitle := "PRIVATE-ROM-TITLE-MUST-NOT-LEAK"
	if _, err = store.CreateGame(t.Context(), catalog.NewGame{DefaultTitle: privateTitle, Platform: "gba"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err = server.New(store, filepath.Join(root, "private-rom-root")); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err = releaseAuditTo([]string{"--db", path, "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	raw := output.String()
	for _, privateValue := range []string{root, path, privateTitle, "private-rom-root"} {
		if strings.Contains(raw, privateValue) {
			t.Fatalf("release audit leaked private value %q: %s", privateValue, raw)
		}
	}
	var report releaseAuditReport
	if err = json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode release audit JSON: %v\n%s", err, raw)
	}
	if report.Format != "varkiv-release-audit-v3" || report.ApplicationVersion != buildinfo.Version || report.SchemaVersion != catalog.CurrentSchemaVersion {
		t.Fatalf("release audit identity = %q application=%q schema=%d", report.Format, report.ApplicationVersion, report.SchemaVersion)
	}
	if !report.SoftwareReady || report.HardwareReady || report.Hardware.Ready || report.PublicReleaseReady {
		t.Fatalf("release audit readiness software=%t hardware=%t nested_hardware=%t public=%t", report.SoftwareReady, report.HardwareReady, report.Hardware.Ready, report.PublicReleaseReady)
	}
	var contract map[string]json.RawMessage
	if err = json.Unmarshal(output.Bytes(), &contract); err != nil {
		t.Fatalf("decode release audit JSON contract: %v", err)
	}
	for _, required := range []string{"application_version", "software_ready", "hardware_ready", "public_release_ready"} {
		if _, ok := contract[required]; !ok {
			t.Fatalf("release audit JSON omitted required readiness field %q: %s", required, raw)
		}
	}
	if !reflect.DeepEqual(report.ExternalGates, currentExternalReleaseGates()) {
		t.Fatalf("release audit external gates = %#v", report.ExternalGates)
	}
	if len(report.Software.Gates) != 7 || len(report.Hardware.Gates) != 4 {
		t.Fatalf("release audit gate counts software=%d hardware=%d", len(report.Software.Gates), len(report.Hardware.Gates))
	}
}

func TestReleaseAuditRejectsSoftwareDriftWithoutRepairingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "release-audit-drift.db")
	store, err := catalog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.New(store, filepath.Join(root, "library")); err != nil {
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
	if _, err = db.Exec(`UPDATE source_adapters SET enabled=0 WHERE id='builtin-source-pegasus'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	err = releaseAudit([]string{"--db", path, "--json"})
	if err == nil || !strings.Contains(err.Error(), "software release evidence gates are pending") || strings.Contains(err.Error(), root) {
		t.Fatalf("software-drift release audit error=%v", err)
	}
	readonly, err := catalog.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	adapter, err := readonly.GetSourceAdapter(context.Background(), "builtin-source-pegasus")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Enabled {
		t.Fatal("release audit silently repaired a disabled source adapter")
	}
}
