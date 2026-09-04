package deviceagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/server"
)

func issuePairingCode(t *testing.T, handler http.Handler) string {
	t.Helper()
	body := bytes.NewBufferString(`{"expires_in_seconds":120,"requested_device":{"device_profile_id":"builtin-device-windows-handheld"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pairing-codes", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer admin-secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("issue pairing code: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Code
}

func TestRemainingSaveDownloadBudgetEnforcesAggregateContract(t *testing.T) {
	remaining, err := remainingSaveDownloadBudget(maxSyncBytes-3, 3, maxSyncFiles)
	if err != nil || remaining != 3 {
		t.Fatalf("exact boundary = %d, %v", remaining, err)
	}
	for _, test := range []struct {
		total    int64
		declared int64
		files    int
	}{
		{maxSyncBytes - 3, 4, 1},
		{maxSyncBytes + 1, -1, 1},
		{-1, -1, 1},
		{0, -1, maxSyncFiles + 1},
	} {
		if _, err = remainingSaveDownloadBudget(test.total, test.declared, test.files); err == nil {
			t.Fatalf("unsafe budget accepted: %#v", test)
		}
	}
}

func TestAddLocalFileRejectsNonPortablePathBeforeHashing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-source.sav")
	if err := os.WriteFile(path, []byte("save"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	set := LocalSet{}
	if err = addLocalFile(&set, path, "bad?.sav", info); err == nil {
		t.Fatal("non-portable local save path was accepted")
	}
	if strings.Contains(err.Error(), "private-source") || len(set.Files) != 0 || set.TotalSize != 0 {
		t.Fatalf("rejected path disclosed local data or mutated the set: %#v, %v", set, err)
	}
}

func TestStableSingleFileLogicalPathDoesNotDiscloseUnsafeLocalName(t *testing.T) {
	for input, expected := range map[string]string{
		"private-game.SRM":  "primary.srm",
		"private-game":      "primary",
		"private-game.bad?": "primary",
		"private-game.状态":   "primary",
	} {
		if actual := stableSingleFileLogicalPath(input); actual != expected || strings.Contains(actual, "private") {
			t.Fatalf("stable path for %q = %q, want %q", input, actual, expected)
		}
	}
}

func pairTestAgent(t *testing.T, origin string, handler http.Handler, root, configPath, name string) Config {
	t.Helper()
	config, err := Pair(context.Background(), PairInput{
		ServerURL: origin, Code: issuePairingCode(t, handler), Name: name,
		OSFamily: "windows", Architecture: "x86_64",
		AgentVersion: "test", RootDir: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestAgentMultiFileRoundTripBackupAndConflictSafety(t *testing.T) {
	library := filepath.Join(t.TempDir(), "library")
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(library, 0o755); err != nil {
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
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "PS2 Game", Platform: "ps2"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "PS2 Game", EditionType: "original", Serial: "SLUS-00000"})
	if err != nil {
		t.Fatal(err)
	}
	secondGame, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "PS2 Game Two", Platform: "ps2"})
	if err != nil {
		t.Fatal(err)
	}
	secondEdition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: secondGame.ID, DefaultTitle: "PS2 Game Two", EditionType: "original", Serial: "SLUS-00001"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "container", OwnerKey: "pcsx2-card-slot-1", DriverID: "builtin-driver-pcsx2", Portability: "driver-dependent", EditionIDs: []string{edition.ID, secondEdition.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stream.Editions) != 2 {
		t.Fatalf("shared container stream editions=%#v", stream.Editions)
	}
	enabled := true
	var secondBinding catalog.SaveBinding
	for _, editionID := range []string{edition.ID, secondEdition.ID} {
		created, createErr := store.CreateSaveBinding(ctx, catalog.NewSaveBinding{
			StreamID: stream.ID, EditionID: editionID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-pcsx2",
			LocalPaths: []string{"{{driver.user_dir}}/memcards"}, Discovery: map[string]any{"mode": "directory", "refresh": "process-exit"}, Enabled: &enabled,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if editionID == secondEdition.ID {
			secondBinding = created
		}
	}

	rootA := filepath.Join(t.TempDir(), "device-a")
	rootB := filepath.Join(t.TempDir(), "device-b")
	for _, root := range []string{rootA, rootB} {
		if err = os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	saveA := filepath.Join(rootA, "PCSX2-user", "memcards")
	if err = os.MkdirAll(filepath.Join(saveA, "states"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(saveA, "Mcd001.ps2"), []byte("card-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(saveA, "states", "index.json"), []byte("index-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	configAPath := filepath.Join(t.TempDir(), "agent-a.json")
	configBPath := filepath.Join(t.TempDir(), "agent-b.json")
	configA := pairTestAgent(t, httpServer.URL, app.Handler(), rootA, configAPath, "Device A")
	configB := pairTestAgent(t, httpServer.URL, app.Handler(), rootB, configBPath, "Device B")
	if configA.DeviceTarget != "windows" || configB.DeviceTarget != "windows" {
		t.Fatalf("pairing did not persist the selected device target: a=%q b=%q", configA.DeviceTarget, configB.DeviceTarget)
	}
	configA.DriverRoots = map[string]string{"builtin-driver-pcsx2": filepath.Join(rootA, "PCSX2-user")}
	configB.DriverRoots = map[string]string{"builtin-driver-pcsx2": filepath.Join(rootB, "PCSX2-user")}
	if err = UpdateConfig(configAPath, configA); err != nil {
		t.Fatal(err)
	}
	if err = UpdateConfig(configBPath, configB); err != nil {
		t.Fatal(err)
	}
	driftedConfig, err := LoadConfig(configAPath)
	if err != nil {
		t.Fatal(err)
	}
	driftedConfig.DeviceTarget = "rocknix"
	if err = UpdateConfig(configAPath, driftedConfig); err != nil {
		t.Fatal(err)
	}
	driftedResult, driftErr := SyncOnce(ctx, configAPath)
	if !errors.Is(driftErr, ErrDeviceTargetDrift) || driftedResult.SessionID != "" {
		t.Fatalf("device target drift reached a sync session: %#v %v", driftedResult, driftErr)
	}
	driftSessions, listErr := store.ListSyncSessions(ctx, configA.DeviceID)
	if listErr != nil || len(driftSessions) != 0 {
		t.Fatalf("device target drift left sessions=%#v err=%v", driftSessions, listErr)
	}
	driftedConfig, err = LoadConfig(configAPath)
	if err != nil {
		t.Fatal(err)
	}
	driftedConfig.DeviceTarget = "windows"
	if err = UpdateConfig(configAPath, driftedConfig); err != nil {
		t.Fatal(err)
	}
	romRoot := filepath.Join(rootA, "roms", "ps2")
	if err = os.MkdirAll(romRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(romRoot, "game.iso")
	if err = os.WriteFile(romPath, []byte("small-rom-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	romHash, _, err := hashFile(romPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "ps2/game.iso", SHA256: romHash, Size: 17}); err != nil {
		t.Fatal(err)
	}
	configA, err = LoadConfig(configAPath)
	if err != nil {
		t.Fatal(err)
	}
	configA.ROMRoots = map[string]string{"ps2": romRoot}
	emulatorDir := filepath.Join(rootA, "emulators")
	coreDir := filepath.Join(rootA, "cores")
	if err = os.MkdirAll(emulatorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(coreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(emulatorDir, "pcsx2-qt.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(coreDir, "mgba_libretro.dll"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	configA.PathOverrides["emulator_dir"] = emulatorDir
	configA.PathOverrides["core_dir"] = coreDir
	if err = UpdateConfig(configAPath, configA); err != nil {
		t.Fatal(err)
	}

	if _, err = store.UpdateSaveBinding(ctx, secondBinding.ID, catalog.NewSaveBinding{
		StreamID: stream.ID, EditionID: secondEdition.ID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-pcsx2",
		LocalPaths: []string{"{{driver.user_dir}}/different-card"}, Discovery: map[string]any{"mode": "directory", "refresh": "process-exit"}, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	rejected, err := SyncOnce(ctx, configAPath)
	if !errors.Is(err, ErrInconsistentSharedSaveBinding) || rejected.SessionID != "" {
		t.Fatalf("inconsistent shared bindings reached a sync session: %#v %v", rejected, err)
	}
	sessions, listErr := store.ListSyncSessions(ctx, configA.DeviceID)
	if listErr != nil || len(sessions) != 0 {
		t.Fatalf("inconsistent shared bindings left sessions=%#v err=%v", sessions, listErr)
	}
	if _, err = store.UpdateSaveBinding(ctx, secondBinding.ID, catalog.NewSaveBinding{
		StreamID: stream.ID, EditionID: secondEdition.ID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-pcsx2",
		LocalPaths: []string{"{{driver.user_dir}}/memcards"}, Discovery: map[string]any{"mode": "directory", "refresh": "process-exit"}, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}

	first, err := SyncOnce(ctx, configAPath)
	if err != nil || first.Uploaded != 1 || first.Status != "complete" {
		t.Fatalf("initial upload: %#v %v", first, err)
	}
	firstSession, err := store.GetSyncSession(ctx, first.SessionID)
	if err != nil || len(firstSession.Operations) != 1 || firstSession.Operations[0].StreamID != stream.ID {
		t.Fatalf("shared stream was not negotiated exactly once: %#v %v", firstSession.Operations, err)
	}
	completedConfig, err := LoadConfig(configAPath)
	if err != nil || completedConfig.LastSync == nil || completedConfig.LastSync.State != "complete" || completedConfig.LastSync.Uploaded != 1 || !completedConfig.LastSync.SessionRecorded || completedConfig.LastSync.LastSuccessAt == "" || completedConfig.LastSync.ErrorCode != "" {
		t.Fatalf("completed local status=%#v err=%v", completedConfig.LastSync, err)
	}
	deviceA, err := store.GetDevice(ctx, configA.DeviceID)
	if err != nil || !deviceA.Capabilities["runtime_probe"] || !deviceA.Capabilities["emulator_installed"] || !deviceA.Capabilities["retroarch_core_installed"] {
		t.Fatalf("runtime probe capability heartbeat: %#v %v", deviceA.Capabilities, err)
	}
	revisions, err := store.ListStreamRevisions(ctx, stream.ID)
	if err != nil || len(revisions) != 1 || revisions[0].FileCount != 2 {
		t.Fatalf("server multi-file revision: %#v %v", revisions, err)
	}
	repeated, err := SyncOnce(ctx, configAPath)
	if err != nil || repeated.Uploaded != 0 || repeated.Downloaded != 0 || repeated.Conflicts != 0 || repeated.Status != "complete" {
		t.Fatalf("unchanged repeat was not a no-op: %#v %v", repeated, err)
	}
	revisions, err = store.ListStreamRevisions(ctx, stream.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("unchanged repeat created another revision: %#v %v", revisions, err)
	}
	inventory, err := store.ListInventoryItems(ctx, first.SessionID)
	if err != nil || len(inventory) != 1 || inventory[0].MatchStatus != "matched" || inventory[0].MatchedEditionID != edition.ID || len(inventory[0].ClientItemID) != 64 || strings.Contains(inventory[0].ClientItemID, "game.iso") {
		t.Fatalf("privacy-minimized ROM inventory: %#v %v", inventory, err)
	}
	second, err := SyncOnce(ctx, configBPath)
	if err != nil || second.Downloaded != 1 || second.Status != "complete" {
		t.Fatalf("initial download: %#v %v", second, err)
	}
	saveB := filepath.Join(rootB, "PCSX2-user", "memcards")
	for path, expected := range map[string]string{"Mcd001.ps2": "card-v1", filepath.Join("states", "index.json"): "index-v1"} {
		data, readErr := os.ReadFile(filepath.Join(saveB, path))
		if readErr != nil || string(data) != expected {
			t.Fatalf("downloaded %s = %q, %v", path, data, readErr)
		}
	}

	if err = os.WriteFile(filepath.Join(saveA, "Mcd001.ps2"), []byte("card-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	advanced, err := SyncOnce(ctx, configAPath)
	if err != nil || advanced.Uploaded != 1 {
		t.Fatalf("server advance: %#v %v", advanced, err)
	}
	updatedB, err := SyncOnce(ctx, configBPath)
	if err != nil || updatedB.Downloaded != 1 {
		t.Fatalf("unchanged client should download: %#v %v", updatedB, err)
	}
	data, _ := os.ReadFile(filepath.Join(saveB, "Mcd001.ps2"))
	if string(data) != "card-v2" {
		t.Fatalf("new server save was not installed: %q", data)
	}
	backupRoot := filepath.Join(rootB, ".varkiv", "backups", stream.ID)
	backups, err := os.ReadDir(backupRoot)
	if err != nil || len(backups) != 1 {
		t.Fatalf("recoverable backup missing: %d %v", len(backups), err)
	}
	oldData, err := os.ReadFile(filepath.Join(backupRoot, backups[0].Name(), "Mcd001.ps2"))
	if err != nil || string(oldData) != "card-v1" {
		t.Fatalf("backup contents = %q, %v", oldData, err)
	}

	if err = os.WriteFile(filepath.Join(saveA, "Mcd001.ps2"), []byte("card-v3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = SyncOnce(ctx, configAPath); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(saveB, "states", "index.json"), []byte("local-important"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict, err := SyncOnce(ctx, configBPath)
	if !errors.Is(err, ErrSyncConflict) || conflict.Conflicts != 1 {
		t.Fatalf("diverged clients must conflict: %#v %v", conflict, err)
	}
	conflictConfig, loadErr := LoadConfig(configBPath)
	if loadErr != nil || conflictConfig.LastSync == nil || conflictConfig.LastSync.State != "conflict" || conflictConfig.LastSync.Conflicts != 1 || conflictConfig.LastSync.ErrorCode != "sync_conflict" || conflictConfig.LastSync.LastSuccessAt == "" {
		t.Fatalf("conflict local status=%#v err=%v", conflictConfig.LastSync, loadErr)
	}
	important, _ := os.ReadFile(filepath.Join(saveB, "states", "index.json"))
	if string(important) != "local-important" {
		t.Fatalf("conflict overwrote local data: %q", important)
	}
	if _, err = store.RevokeDevice(ctx, configB.DeviceID); err != nil {
		t.Fatal(err)
	}
	revokedResult, revokedErr := SyncOnce(ctx, configBPath)
	if revokedErr == nil || revokedResult.Uploaded != 0 || revokedResult.Downloaded != 0 {
		t.Fatalf("revoked device unexpectedly synchronized: %#v %v", revokedResult, revokedErr)
	}
	revokedConfig, loadErr := LoadConfig(configBPath)
	if loadErr != nil || revokedConfig.LastSync == nil || revokedConfig.LastSync.State != "failed" || revokedConfig.LastSync.ErrorCode != "sync_failed" {
		t.Fatalf("revoked token status=%#v err=%v", revokedConfig.LastSync, loadErr)
	}
	important, _ = os.ReadFile(filepath.Join(saveB, "states", "index.json"))
	if string(important) != "local-important" {
		t.Fatalf("revoked request changed local data: %q", important)
	}

	existing := filepath.Join(t.TempDir(), "important.sav")
	if err = os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "download.sav")
	if err = os.WriteFile(source, []byte("replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = copyExclusive(source, existing, 0o600); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("exclusive copy accepted overwrite: %v", err)
	}
	kept, _ := os.ReadFile(existing)
	if string(kept) != "keep" {
		t.Fatalf("exclusive copy changed important data: %q", kept)
	}
}

func TestSharedSaveStreamRejectsInconsistentLocalTargetsBeforeSession(t *testing.T) {
	root := t.TempDir()
	driver := catalog.EmulatorDriver{ID: "builtin-driver-pcsx2", Save: catalog.DriverSaveSpec{Layout: "memory-card"}}
	stream := catalog.SaveStream{ID: "shared-container"}
	remote := deviceConfigResponse{Bindings: []bindingDescriptor{
		{Binding: catalog.SaveBinding{ID: "binding-a", LocalPaths: []string{"cards/slot-a"}}, Stream: stream, EditionID: "edition-a", Driver: driver},
		{Binding: catalog.SaveBinding{ID: "binding-b", LocalPaths: []string{"cards/slot-b"}}, Stream: stream, EditionID: "edition-b", Driver: driver},
	}}
	_, err := collectLocalSets(Config{RootDir: root}, remote)
	if !errors.Is(err, ErrInconsistentSharedSaveBinding) {
		t.Fatalf("inconsistent shared targets error=%v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "slot-a") || strings.Contains(err.Error(), "slot-b") {
		t.Fatalf("shared target error leaked a local path: %v", err)
	}
}

func TestReconcileDeviceTargetBackfillsLegacyConfigAndRejectsLaterDrift(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "legacy-agent.json")
	legacy := Config{ServerURL: "https://library.example.test", DeviceID: "device", AccessToken: "private-token", DeviceProfileID: "custom-profile", RootDir: root}
	if err := SaveConfig(configPath, legacy); err != nil {
		t.Fatal(err)
	}
	remote := deviceConfigResponse{DeviceProfile: catalog.DeviceProfile{Target: "windows"}}
	if err := reconcileDeviceTarget(configPath, &legacy, remote); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadConfig(configPath)
	if err != nil || stored.DeviceTarget != "windows" {
		t.Fatalf("legacy device target was not persisted: target=%q err=%v", stored.DeviceTarget, err)
	}
	if err = reconcileDeviceTarget(configPath, &stored, deviceConfigResponse{DeviceProfile: catalog.DeviceProfile{Target: "rocknix"}}); !errors.Is(err, ErrDeviceTargetDrift) {
		t.Fatalf("later device target drift was accepted: %v", err)
	}
	after, err := LoadConfig(configPath)
	if err != nil || after.DeviceTarget != "windows" {
		t.Fatalf("target drift changed the stored identity: target=%q err=%v", after.DeviceTarget, err)
	}
}

func TestContentHashMatchesAndroidSingleFileFixedVector(t *testing.T) {
	files := []LocalFile{{
		LogicalPath: "primary.srm",
		Checksum:    "1a20d1ac4c9f15e47bef3c205377617bf199e1961f6e5928e928345e2bb4b625",
		Size:        20,
	}}
	if actual, expected := contentHash(files), "43ab9a1bc5bcc39d766bee0acbf59e69cc11f73d092898f9cea42bf71c2ceb59"; actual != expected {
		t.Fatalf("cross-client single-file content hash=%q want %q", actual, expected)
	}
}

func TestSaveInstallFailuresPreserveTheExactTrackedTarget(t *testing.T) {
	newFile := func(t *testing.T, path, logical, content string) LocalFile {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		checksum, size, err := hashFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return LocalFile{LogicalPath: logical, Path: path, Checksum: checksum, Size: size, Mode: 0o600}
	}
	assertSingleTarget := func(t *testing.T, target, expected string) {
		t.Helper()
		data, err := os.ReadFile(target)
		if err != nil || string(data) != expected {
			t.Fatalf("tracked target=%q err=%v", data, err)
		}
	}

	for _, fixture := range []struct {
		name   string
		mutate func(saveInstallOps) saveInstallOps
	}{
		{
			name: "disk full while staging",
			mutate: func(ops saveInstallOps) saveInstallOps {
				ops.copyFile = func(string, string, os.FileMode) error { return syscall.ENOSPC }
				return ops
			},
		},
		{
			name: "emulator keeps the save locked",
			mutate: func(ops saveInstallOps) saveInstallOps {
				ops.replaceFile = func(string, string) error { return os.ErrPermission }
				return ops
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "saves", "game.srm")
			currentFile := newFile(t, target, "primary.srm", "important-local-save")
			downloadedFile := newFile(t, filepath.Join(root, "download", "primary.srm"), "primary.srm", "server-save")
			current := LocalSet{Files: []LocalFile{currentFile}, ContentHash: contentHash([]LocalFile{currentFile})}
			err := installDownloadedSave(
				Config{RootDir: root}, catalog.SyncSession{ID: "failure-session"}, catalog.SyncOperation{StreamID: "stream"},
				current, []LocalFile{downloadedFile}, target, true, true, fixture.mutate(defaultSaveInstallOps()),
			)
			if err == nil {
				t.Fatal("injected installation failure unexpectedly succeeded")
			}
			assertSingleTarget(t, target, "important-local-save")
			backups, readErr := os.ReadDir(filepath.Join(root, ".varkiv", "backups", "stream"))
			if readErr != nil || len(backups) != 1 {
				t.Fatalf("verified backup missing after failure: entries=%d err=%v", len(backups), readErr)
			}
			backupData, readErr := os.ReadFile(filepath.Join(root, ".varkiv", "backups", "stream", backups[0].Name(), "primary.srm"))
			if readErr != nil || string(backupData) != "important-local-save" {
				t.Fatalf("failure backup=%q err=%v", backupData, readErr)
			}
			entries, readErr := os.ReadDir(filepath.Dir(target))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".varkiv-install-") || strings.HasPrefix(entry.Name(), ".varkiv-previous-") {
					t.Fatalf("failure left transient replacement path %q", entry.Name())
				}
			}
		})
	}
}

func TestDirectoryInstallFailureRestoresOriginalAndKeepsBackup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "saves", "card")
	oldPath := filepath.Join(target, "slot", "data.bin")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("important-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldHash, oldSize, err := hashFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	currentFile := LocalFile{LogicalPath: "slot/data.bin", Path: oldPath, Checksum: oldHash, Size: oldSize, Mode: 0o600}
	downloadPath := filepath.Join(root, "download", "slot", "data.bin")
	if err = os.MkdirAll(filepath.Dir(downloadPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(downloadPath, []byte("server-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	newHash, newSize, err := hashFile(downloadPath)
	if err != nil {
		t.Fatal(err)
	}
	downloaded := LocalFile{LogicalPath: "slot/data.bin", Path: downloadPath, Checksum: newHash, Size: newSize, Mode: 0o600}
	ops := defaultSaveInstallOps()
	renameCalls := 0
	ops.rename = func(source, destination string) error {
		renameCalls++
		if renameCalls == 2 {
			return os.ErrPermission
		}
		return os.Rename(source, destination)
	}
	err = installDownloadedSave(
		Config{RootDir: root}, catalog.SyncSession{ID: "directory-session"}, catalog.SyncOperation{StreamID: "stream"},
		LocalSet{Files: []LocalFile{currentFile}, ContentHash: contentHash([]LocalFile{currentFile})}, []LocalFile{downloaded}, target, true, false, ops,
	)
	if err == nil || renameCalls != 3 {
		t.Fatalf("directory rollback error=%v rename_calls=%d", err, renameCalls)
	}
	data, readErr := os.ReadFile(oldPath)
	if readErr != nil || string(data) != "important-directory" {
		t.Fatalf("directory rollback lost original: %q %v", data, readErr)
	}
	backups, readErr := os.ReadDir(filepath.Join(root, ".varkiv", "backups", "stream"))
	if readErr != nil || len(backups) != 1 {
		t.Fatalf("directory rollback backup missing: entries=%d err=%v", len(backups), readErr)
	}
}

func TestInterruptedAgentAttemptRemainsExplicitlyRunning(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent.json")
	config := Config{ServerURL: "https://example.invalid", DeviceID: "device", AccessToken: "token", RootDir: t.TempDir()}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	attempted := time.Now().UTC()
	if err := recordSyncStart(configPath, attempted); err != nil {
		t.Fatal(err)
	}
	interrupted, err := LoadConfig(configPath)
	if err != nil || interrupted.LastSync == nil || interrupted.LastSync.State != "running" || interrupted.LastSync.FinishedAt != "" || interrupted.LastSync.ErrorCode != "" {
		t.Fatalf("interrupted status=%#v err=%v", interrupted.LastSync, err)
	}
}

func TestSyncFailurePersistsOnlyGenericLocalStatus(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	serverURL := closed.URL
	closed.Close()
	configPath := filepath.Join(t.TempDir(), "agent.json")
	config := Config{ServerURL: serverURL, DeviceID: "device-private", AccessToken: "token-private", DeviceProfileID: "profile-private", RootDir: t.TempDir()}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncOnce(context.Background(), configPath); err == nil {
		t.Fatal("sync to a closed local server unexpectedly succeeded")
	}
	updated, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastSync == nil || updated.LastSync.State != "failed" || updated.LastSync.ErrorCode != "sync_failed" || updated.LastSync.FinishedAt == "" || updated.LastSync.LastSuccessAt != "" || updated.LastSync.SessionRecorded {
		t.Fatalf("failed local status=%#v", updated.LastSync)
	}
	encoded, err := json.Marshal(updated.LastSync)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{serverURL, config.DeviceID, config.AccessToken, config.DeviceProfileID, config.RootDir, configPath} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("local status retained private value %q: %s", private, encoded)
		}
	}
}

func TestAgentSyncLockSerializesAndRecoversWithoutDeleting(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent.json")
	releaseFirst, err := acquireSyncLock(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if releaseSecond, secondErr := acquireSyncLock(configPath); !errors.Is(secondErr, ErrSyncInProgress) {
		if releaseSecond != nil {
			releaseSecond()
		}
		releaseFirst()
		t.Fatalf("concurrent sync lock=%v", secondErr)
	}
	releaseFirst()
	releaseAfterCrashBoundary, err := acquireSyncLock(configPath)
	if err != nil {
		t.Fatalf("released operating-system lock stayed busy: %v", err)
	}
	releaseAfterCrashBoundary()
	lockPath := configPath + ".sync.lock"
	info, err := os.Lstat(lockPath)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("lock file=%#v err=%v", info, err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil || len(data) != 0 {
		t.Fatalf("lock file persisted private data: %q err=%v", data, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("lock permissions=%04o", info.Mode().Perm())
	}
}

func TestAgentRejectsLooseConfigAndSymlinkTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "saves")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	config := Config{ServerURL: "https://example.invalid", DeviceID: "device", AccessToken: "secret", RootDir: root, Streams: map[string]StreamState{}}
	remote := deviceConfigResponse{Device: catalog.Device{ID: "device"}, DeviceProfile: catalog.DeviceProfile{Target: "windows", Paths: map[string]string{"save_dir": "saves"}}}
	descriptor := bindingDescriptor{Binding: catalog.SaveBinding{LocalPaths: []string{"{{device.save_dir}}/game.sav"}}, Stream: catalog.SaveStream{ID: "stream"}, EditionID: "edition", SaveNamespace: "game", Driver: catalog.EmulatorDriver{ID: "driver"}}
	if _, err := renderBindingPath(descriptor.Binding.LocalPaths[0], config, remote, descriptor); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink traversal was accepted: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "agent.json")
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(configPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(configPath); err == nil || !strings.Contains(err.Error(), "permissions") {
			t.Fatalf("loose token file permissions were accepted: %v", err)
		}
	}
}

func TestDriverRootTemplateRequiresExplicitRootAndIdentity(t *testing.T) {
	root := t.TempDir()
	driverRoot := filepath.Join(root, "Vita3K")
	if err := os.MkdirAll(driverRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	remote := deviceConfigResponse{Device: catalog.Device{ID: "device"}, DeviceProfile: catalog.DeviceProfile{Target: "windows"}}
	descriptor := bindingDescriptor{EditionID: "edition", TitleID: "PCSE00001", Driver: catalog.EmulatorDriver{ID: "builtin-driver-vita3k"}}
	template := "{{driver.user_dir}}/ux0/user/00/savedata/{{edition.title_id}}"
	if _, err := renderBindingPath(template, Config{RootDir: root, DriverRoots: map[string]string{}}, remote, descriptor); err == nil || !strings.Contains(err.Error(), "driver.user_dir") {
		t.Fatalf("missing driver root was accepted: %v", err)
	}
	config := Config{RootDir: root, DriverRoots: map[string]string{"builtin-driver-vita3k": driverRoot}}
	resolved, err := renderBindingPath(template, config, remote, descriptor)
	if err != nil || resolved != filepath.Join(driverRoot, "ux0", "user", "00", "savedata", "PCSE00001") {
		t.Fatalf("driver root template = %q, %v", resolved, err)
	}
	descriptor.TitleID = ""
	if _, err = renderBindingPath(template, config, remote, descriptor); err == nil || !strings.Contains(err.Error(), "edition.title_id") {
		t.Fatalf("missing title identity was accepted: %v", err)
	}
}

func TestResolveDeviceLocalROMStemByDigestWithoutPathDisclosure(t *testing.T) {
	hash := strings.Repeat("a", 64)
	descriptor := bindingDescriptor{
		Binding:        catalog.SaveBinding{LocalPaths: []string{"{{device.save_dir}}/{{rom.stem}}.srm"}},
		PlatformID:     "gba",
		ROMMatchSHA256: hash,
	}
	resolved, err := resolveDeviceLocalROMStems([]bindingDescriptor{descriptor}, []localROMName{
		{PlatformID: "gb", SHA256: hash, Stem: "wrong-platform-private-name"},
		{PlatformID: "gba", SHA256: hash, Stem: "renamed-on-device"},
	})
	if err != nil || len(resolved) != 1 || resolved[0].ROMStem != "renamed-on-device" {
		t.Fatalf("device-local ROM basename was not resolved by platform and digest: %#v %v", resolved, err)
	}
	data, err := json.Marshal(inventoryRequest{ClientItemID: strings.Repeat("b", 64), PlatformID: "gba", SHA256: hash, Size: 4})
	if err != nil || strings.Contains(string(data), "renamed-on-device") {
		t.Fatalf("serialized inventory disclosed a local ROM name: %s %v", data, err)
	}

	_, err = resolveDeviceLocalROMStems([]bindingDescriptor{descriptor}, []localROMName{
		{PlatformID: "gba", SHA256: hash, Stem: "private-a"},
		{PlatformID: "gba", SHA256: hash, Stem: "private-b"},
	})
	if err == nil || strings.Contains(err.Error(), "private-a") || strings.Contains(err.Error(), "private-b") {
		t.Fatalf("ambiguous local names were accepted or disclosed: %v", err)
	}

	resolved, err = resolveDeviceLocalROMStems([]bindingDescriptor{descriptor}, nil)
	if err != nil || resolved[0].ROMStem != "" {
		t.Fatalf("missing local match must remain unresolved instead of using a server-side name: %#v %v", resolved, err)
	}
}

func TestAgentRetroArchSyncUsesRenamedLocalROMStemPrivately(t *testing.T) {
	library := filepath.Join(t.TempDir(), "library")
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(library, 0o755); err != nil {
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

	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Library title", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Library title", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "device")
	romRoot := filepath.Join(root, "roms", "gba")
	saveRoot := filepath.Join(root, "saves")
	if err = os.MkdirAll(romRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(saveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	privateStem := "renamed-only-on-device"
	romPath := filepath.Join(romRoot, privateStem+".gba")
	if err = os.WriteFile(romPath, []byte("small-private-rom-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	romHash, romSize, err := hashFile(romPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/library-canonical-name.gba", Role: "rom", SHA256: romHash, Size: romSize}); err != nil {
		t.Fatal(err)
	}
	stream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: "builtin-driver-retroarch", Portability: "core-dependent"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err = store.CreateSaveBinding(ctx, catalog.NewSaveBinding{
		StreamID: stream.ID, EditionID: edition.ID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-retroarch",
		LocalPaths: []string{"{{device.save_dir}}/{{rom.stem}}.srm"}, Discovery: map[string]any{"mode": "file"}, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(saveRoot, privateStem+".srm"), []byte("private-save-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(t.TempDir(), "agent.json")
	config := pairTestAgent(t, httpServer.URL, app.Handler(), root, configPath, "Renamed ROM device")
	config.ROMRoots = map[string]string{"gba": romRoot}
	config.PathOverrides["save_dir"] = saveRoot
	if err = UpdateConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	result, err := SyncOnce(ctx, configPath)
	if err != nil || result.Uploaded != 1 || result.Status != "complete" {
		t.Fatalf("renamed RetroArch save upload: %#v %v", result, err)
	}
	revisions, err := store.ListStreamRevisions(ctx, stream.ID)
	if err != nil || len(revisions) != 1 || len(revisions[0].Files) != 1 || revisions[0].Files[0].LogicalPath != "primary.srm" {
		t.Fatalf("single-file revision did not use a privacy-stable logical name: %#v %v", revisions, err)
	}
	inventory, err := store.ListInventoryItems(ctx, result.SessionID)
	encoded, marshalErr := json.Marshal(struct {
		Inventory []catalog.InventoryItem `json:"inventory"`
		Revisions []catalog.SaveRevision  `json:"revisions"`
	}{inventory, revisions})
	if err != nil || marshalErr != nil || strings.Contains(string(encoded), privateStem) {
		t.Fatalf("central sync state disclosed the device-local basename: %s inventoryErr=%v marshalErr=%v", encoded, err, marshalErr)
	}

	secondRoot := filepath.Join(t.TempDir(), "second-device")
	secondROMRoot := filepath.Join(secondRoot, "roms", "gba")
	secondSaveRoot := filepath.Join(secondRoot, "saves")
	if err = os.MkdirAll(secondROMRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(secondSaveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	secondStem := "different-name-on-second-device"
	if err = os.WriteFile(filepath.Join(secondROMRoot, secondStem+".gba"), []byte("small-private-rom-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondConfigPath := filepath.Join(t.TempDir(), "second-agent.json")
	secondConfig := pairTestAgent(t, httpServer.URL, app.Handler(), secondRoot, secondConfigPath, "Second renamed ROM device")
	secondConfig.ROMRoots = map[string]string{"gba": secondROMRoot}
	secondConfig.PathOverrides["save_dir"] = secondSaveRoot
	if err = UpdateConfig(secondConfigPath, secondConfig); err != nil {
		t.Fatal(err)
	}
	secondResult, err := SyncOnce(ctx, secondConfigPath)
	if err != nil || secondResult.Downloaded != 1 || secondResult.Status != "complete" {
		t.Fatalf("second renamed RetroArch save download: %#v %v", secondResult, err)
	}
	downloaded, err := os.ReadFile(filepath.Join(secondSaveRoot, secondStem+".srm"))
	if err != nil || string(downloaded) != "private-save-fixture" {
		t.Fatalf("save was not restored beside the second device's own ROM basename: %q %v", downloaded, err)
	}
	secondInventory, err := store.ListInventoryItems(ctx, secondResult.SessionID)
	secondEncoded, marshalErr := json.Marshal(secondInventory)
	if err != nil || marshalErr != nil || strings.Contains(string(secondEncoded), secondStem) {
		t.Fatalf("second device inventory disclosed its local basename: %s inventoryErr=%v marshalErr=%v", secondEncoded, err, marshalErr)
	}
}
