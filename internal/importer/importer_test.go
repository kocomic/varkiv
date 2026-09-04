package importer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
	"varkiv/internal/platforms"
)

func openStore(t *testing.T) *catalog.Store {
	t.Helper()
	s, err := catalog.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPreviewLibraryManifestV4RestoresResourcesSeriesAndInertLaunchHints(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "package")
	if err := os.MkdirAll(filepath.Join(packageDir, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageDir, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "gba", "safe.gba"), []byte("neutral-rom-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "media", "cover.png"), []byte("neutral-media-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "format_version": 4,
  "frontend": "",
  "series": [{"id":"series-one","default_title":"Series One","titles":{"zh-CN":"系列一"},"members":[{"game_id":"game-one","relation_type":"mainline","sort_order":1}]}],
  "entries": [
    {"game_id":"game-one","edition_id":"edition-one","platform":"gba","game_default_title":"Game One","game_titles":{"zh-CN":"游戏一"},"edition_default_title":"Original","edition_titles":{"ja":"オリジナル"},"edition_type":"original","languages":["en"],"artifacts":["gba/safe.gba"],"media":[{"owner_type":"game","kind":"cover","path":"media/cover.png","original_name":"cover.png","mime_type":"image/png","sort_order":0}]},
    {"game_id":"game-two","edition_id":"edition-two","platform":"gba","game_default_title":"Missing Game","edition_default_title":"Missing Edition","edition_type":"translation","languages":["zh-CN"],"artifacts":["gba/missing.gba"],"media":[]}
  ]
}`
	manifestPath := filepath.Join(packageDir, libraryManifestName)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	launches := `{"format_version":1,"bindings":[{"edition_id":"edition-one","rom_path":"package/gba/safe.gba","binding":{"driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","arguments":["-L","{{core.library}}","{{rom.path}}"]},"driver":{"id":"builtin-driver-retroarch"},"core_resolution":{"core":{"id":"builtin-core-mgba"}}}]}`
	if err := os.WriteFile(filepath.Join(packageDir, launchManifestName), []byte(launches), 0o600); err != nil {
		t.Fatal(err)
	}
	games, err := PreviewLibraryManifest(root, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 || games[0].GameID != "game-one" || games[0].EditionID != "edition-one" || games[0].Artifacts[0].SHA256 == "" || games[0].Artifacts[0].Missing {
		t.Fatalf("ready manifest entry was not restored: %#v", games)
	}
	if len(games[0].Media) != 1 || games[0].Media[0].GameID != "game-one" || len(games[0].SeriesMemberships) != 1 || games[0].SeriesMemberships[0].Series.ID != "series-one" {
		t.Fatalf("media or series was not restored: %#v", games[0])
	}
	if len(games[0].RuntimeHints) != 1 || games[0].RuntimeHints[0].SourceKind != "structured-sidecar" || games[0].RuntimeHints[0].DriverID != "builtin-driver-retroarch" {
		t.Fatalf("reviewable launch hint was not restored: %#v", games[0].RuntimeHints)
	}
	if len(games[1].Artifacts) != 1 || !games[1].Artifacts[0].Missing || games[1].Artifacts[0].SHA256 != "" {
		t.Fatalf("missing ROM was not preserved as non-importable: %#v", games[1])
	}
}

func TestPreviewESDEManifestReplacesFrontendMediaAlias(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"gba", "blobs", filepath.Join("gamelists", "gba")} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("esde-manifest-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blobs", "logo.png"), []byte("esde-manifest-logo"), 0o600); err != nil {
		t.Fatal(err)
	}
	gamelist := filepath.Join(root, "gamelists", "gba", "gamelist.xml")
	if err := os.WriteFile(gamelist, []byte(`<gameList><game><path>../../gba/game.gba</path><name>Game</name><marquee>../../blobs/logo.png</marquee></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format_version":4,"frontend":"es-de","entries":[{"game_id":"esde-game","edition_id":"esde-edition","platform":"gba","game_default_title":"Game","edition_default_title":"Original","edition_type":"original","artifacts":["gba/game.gba"],"media":[{"owner_type":"edition","kind":"logo","path":"blobs/logo.png"}]}]}`
	if err := os.WriteFile(filepath.Join(root, libraryManifestName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	games, err := PreviewESDE(root, gamelist, "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].GameID != "esde-game" || games[0].EditionID != "esde-edition" || len(games[0].Media) != 1 || games[0].Media[0].Kind != "logo" || games[0].Media[0].EditionID != "esde-edition" {
		t.Fatalf("neutral media semantics did not replace the ES-DE alias: %#v", games)
	}
}

func TestPreviewLibraryManifestV5PreservesArtifactSemanticsAndRejectsDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "psx"), 0o755); err != nil {
		t.Fatal(err)
	}
	discBody := []byte("disc-image-fixture")
	patchBody := []byte("patch-v1")
	if err := os.WriteFile(filepath.Join(root, "psx", "game.bin"), discBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "psx", "translation.ips"), patchBody, 0o600); err != nil {
		t.Fatal(err)
	}
	discDigest, patchDigest := sha256.Sum256(discBody), sha256.Sum256(patchBody)
	manifest := fmt.Sprintf(`{"format_version":5,"entries":[{
"game_id":"game-v5","edition_id":"edition-v5","platform":"psx","game_default_title":"Game","edition_default_title":"Translation","edition_type":"translation",
"artifacts":["psx/game.bin","psx/translation.ips"],
"artifact_records":[
  {"path":"psx/game.bin","role":"disc","disc_index":1,"original_name":"Disc 1.bin","size":%d,"sha256":"%x"},
  {"path":"psx/translation.ips","role":"patch","original_name":"Translation.ips","size":%d,"sha256":"%x"}
] }]}`, len(discBody), discDigest, len(patchBody), patchDigest)
	manifestPath := filepath.Join(root, libraryManifestName)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	games, err := PreviewLibraryManifest(root, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || len(games[0].Artifacts) != 2 || games[0].Artifacts[0].Role != "disc" || games[0].Artifacts[0].DiscIndex != 1 || games[0].Artifacts[0].OriginalName != "Disc 1.bin" || games[0].Artifacts[1].Role != "patch" || games[0].Artifacts[1].DiscIndex != 0 {
		t.Fatalf("artifact semantics were not restored: %#v", games)
	}
	if err = os.WriteFile(filepath.Join(root, "psx", "translation.ips"), []byte("patch-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = PreviewLibraryManifest(root, manifestPath); err == nil || !strings.Contains(err.Error(), "hash changed") {
		t.Fatalf("artifact drift was not rejected: %v", err)
	}
}

func TestPreviewLibraryManifestV5RestoresDirectoryArtifact(t *testing.T) {
	root := t.TempDir()
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
	digest, size, err := filehash.Directory(directory)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"format_version":5,"entries":[{"game_id":"ps3-game","edition_id":"ps3-edition","platform":"ps3","game_default_title":"Game","edition_default_title":"Original","edition_type":"original","artifacts":["ps3/Game"],"artifact_records":[{"path":"ps3/Game","role":"rom","original_name":"Game","size":%d,"sha256":"%s"}]}]}`, size, digest)
	manifestPath := filepath.Join(root, libraryManifestName)
	if err = os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	games, err := PreviewLibraryManifest(root, manifestPath)
	if err != nil || len(games) != 1 || len(games[0].Artifacts) != 1 || games[0].Artifacts[0].Missing || games[0].Artifacts[0].SHA256 != digest || games[0].Artifacts[0].Size != size {
		t.Fatalf("directory artifact=%#v err=%v", games, err)
	}
}

func TestPreviewLibraryManifestV5RejectsMediaDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	romBody, coverBody := []byte("rom"), []byte("cover-v1")
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), romBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "cover.png"), coverBody, 0o600); err != nil {
		t.Fatal(err)
	}
	romDigest, coverDigest := sha256.Sum256(romBody), sha256.Sum256(coverBody)
	manifest := fmt.Sprintf(`{"format_version":5,"entries":[{"game_id":"media-v5","edition_id":"media-v5-edition","platform":"gba","game_default_title":"Media","edition_default_title":"Original","artifacts":["gba/game.gba"],"artifact_records":[{"path":"gba/game.gba","role":"rom","size":%d,"sha256":"%x"}],"media":[{"owner_type":"game","kind":"cover","path":"gba/cover.png","size":%d,"sha256":"%x"}]}]}`, len(romBody), romDigest, len(coverBody), coverDigest)
	manifestPath := filepath.Join(root, libraryManifestName)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewLibraryManifest(root, manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "cover.png"), []byte("cover-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewLibraryManifest(root, manifestPath); err == nil || !strings.Contains(err.Error(), "media hash changed") {
		t.Fatalf("media drift was not rejected: %v", err)
	}
}

func TestLibraryManifestRejectsSymlinkedArtifactAndMediaWithoutLeakingTarget(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "private.gba")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "gba", "linked.gba")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manifestPath := filepath.Join(root, libraryManifestName)
	manifest := `{"format_version":4,"entries":[{"game_id":"linked","edition_id":"linked-edition","platform":"gba","game_default_title":"Linked","edition_default_title":"Original","artifacts":["gba/linked.gba"]}]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewLibraryManifest(root, manifestPath); err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), outside) {
		t.Fatalf("symlinked artifact error=%v", err)
	}
	if err := os.Remove(filepath.Join(root, "gba", "linked.gba")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "gba", "cover.png")); err != nil {
		t.Fatal(err)
	}
	manifest = `{"format_version":4,"entries":[{"game_id":"media","edition_id":"media-edition","platform":"gba","game_default_title":"Media","edition_default_title":"Original","artifacts":["gba/game.gba"],"media":[{"owner_type":"game","kind":"cover","path":"gba/cover.png"}]}]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewLibraryManifest(root, manifestPath); err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), outside) {
		t.Fatalf("symlinked media error=%v", err)
	}
}

func TestImportLibraryManifestSkipsMissingAndCommitsReadyEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "ready.gba"), []byte("neutral-cli-ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format_version":4,"entries":[
{"game_id":"ready-game","edition_id":"ready-edition","platform":"gba","game_default_title":"Ready","edition_default_title":"Original","edition_type":"original","artifacts":["gba/ready.gba"]},
{"game_id":"missing-game","edition_id":"missing-edition","platform":"ps2","game_default_title":"Missing","edition_default_title":"Original","edition_type":"original","artifacts":["ps2/missing.iso"]}
]}`
	manifestPath := filepath.Join(root, libraryManifestName)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	result, err := ImportLibraryManifest(context.Background(), store, root, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Parsed != 2 || result.Imported != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].ID != "ready-game" || games[0].Platform != "gba" || len(games[0].Editions) != 1 || games[0].Editions[0].ID != "ready-edition" {
		t.Fatalf("unexpected committed games: %#v", games)
	}
}

func TestPreviewLibraryManifestRejectsSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, libraryManifestName)
	if err := os.WriteFile(secret, []byte(`{"format_version":4,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, libraryManifestName)
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PreviewLibraryManifest(root, link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlinked manifest was not rejected: %v", err)
	}
}

func TestPegasusRuntimeHintsPreferStructuredSidecarAndKeepRawCommandInert(t *testing.T) {
	root := t.TempDir()
	platformDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(platformDir, 0o755); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(platformDir, "safe.gba")
	if err := os.WriteFile(romPath, []byte("tiny-rom-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(platformDir, "metadata.pegasus.txt")
	if err := os.WriteFile(metadata, []byte("collection: GBA\nlaunch: retroarch -L mgba {file.path}\n\ngame: Safe\nfile: safe.gba\nx-varkiv-edition-id: edition-safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "format_version": 1,
  "device_profile_id": "builtin-device-windows-handheld",
  "frontend_adapter_id": "builtin-frontend-pegasus",
  "bindings": [{
    "edition_id": "edition-safe",
    "rom_path": "gba/safe.gba",
    "binding": {"driver_id":"builtin-driver-retroarch","core_id":"builtin-core-mgba","arguments":["--appendconfig","{{device.config_dir}}/safe.cfg"]},
    "driver": {"id":"builtin-driver-retroarch"},
    "core_resolution": {"core":{"id":"builtin-core-mgba"}}
  }]
}`
	if err := os.WriteFile(filepath.Join(root, launchManifestName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	games, err := PreviewPegasus(root, metadata, "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || len(games[0].RuntimeHints) != 2 {
		t.Fatalf("games=%#v", games)
	}
	var raw, structured *catalog.NewRuntimeImportHint
	for index := range games[0].RuntimeHints {
		hint := &games[0].RuntimeHints[index]
		if hint.SourceKind == "pegasus-command" {
			raw = hint
		} else if hint.SourceKind == "structured-sidecar" {
			structured = hint
		}
	}
	if raw == nil || !strings.Contains(raw.RawCommand, "{file.path}") || len(raw.Arguments) != 0 {
		t.Fatalf("raw hint=%#v", raw)
	}
	if structured == nil || structured.DriverID != "builtin-driver-retroarch" || structured.CoreID != "builtin-core-mgba" || structured.SourceRef != launchManifestName || len(structured.Arguments) != 2 {
		t.Fatalf("structured hint=%#v", structured)
	}
}

func TestFrontendMetadataIsBoundedRegularAndErrorsDoNotLeakPaths(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	privateMetadata := filepath.Join(outside, "private.pegasus.txt")
	if err := os.WriteFile(privateMetadata, []byte("game: Private\nfile: private.gba\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "gba", "metadata.pegasus.txt")
	if err := os.Symlink(privateMetadata, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PreviewPegasus(root, linked, "gba", "en"); err == nil || !strings.Contains(err.Error(), "exact regular file") || strings.Contains(err.Error(), outside) {
		t.Fatalf("symlinked metadata error=%v", err)
	}
	huge := filepath.Join(root, "gba", "gamelist.xml")
	handle, err := os.OpenFile(huge, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = handle.Truncate(maxFrontendMetadataSize + 1); err != nil {
		handle.Close()
		t.Fatal(err)
	}
	if err = handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = PreviewESDE(root, huge, "gba", "en"); err == nil || !strings.Contains(err.Error(), "exceeds") || strings.Contains(err.Error(), root) {
		t.Fatalf("oversized metadata error=%v", err)
	}
	malformed := filepath.Join(root, "gba", "malformed.pegasus.txt")
	if err = os.WriteFile(malformed, []byte("this line has no separator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = PreviewPegasus(root, malformed, "gba", "en"); err == nil || !strings.Contains(err.Error(), "line 1") || strings.Contains(err.Error(), root) {
		t.Fatalf("malformed metadata error=%v", err)
	}
}

func TestLaunchSidecarDoesNotFollowSymlinkOutsideLibraryRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	platformDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(platformDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platformDir, "game.gba"), []byte("rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(platformDir, "metadata.pegasus.txt")
	if err := os.WriteFile(metadata, []byte("collection: GBA\ngame: Game\nfile: game.gba\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, launchManifestName)
	if err := os.WriteFile(secret, []byte(`{"format_version":1,"bindings":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, launchManifestName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	games, err := PreviewPegasus(root, metadata, "gba", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || len(games[0].RuntimeHints) != 0 {
		t.Fatalf("outside sidecar was read: %#v", games)
	}
}

func TestESDESystemsImportIsExplicitPlatformScopedAndInert(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gamelists", "gba")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "safe.gba"), []byte("safe-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	gamelist := filepath.Join(dir, "gamelist.xml")
	if err := os.WriteFile(gamelist, []byte(`<gameList><game><path>./safe.gba</path><name>Safe</name></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(root, "custom_systems.xml")
	if err := os.WriteFile(runtime, []byte(`<systemList>
  <system><name>gba</name><command label="RetroArch">retroarch -L %CORE_RETROARCH% %ROM%</command></system>
  <system><name>nds</name><command>melonDS %ROM%</command></system>
</systemList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	withoutRuntime, err := PreviewESDEWithRuntime(root, gamelist, "", "gba", "en")
	if err != nil || len(withoutRuntime) != 1 || len(withoutRuntime[0].RuntimeHints) != 0 {
		t.Fatalf("runtime config was not opt-in: %#v, %v", withoutRuntime, err)
	}
	games, err := PreviewESDEWithRuntime(root, gamelist, runtime, "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || len(games[0].RuntimeHints) != 1 {
		t.Fatalf("games=%#v", games)
	}
	hint := games[0].RuntimeHints[0]
	if hint.SourceKind != "esde-system" || hint.SourceRef != "custom_systems.xml" || !strings.Contains(hint.RawCommand, "%CORE_RETROARCH%") || len(hint.Arguments) != 0 || hint.DriverID != "" {
		t.Fatalf("ES-DE command was not kept inert: %#v", hint)
	}
}

func TestESDESystemsRejectsSelectedSymlinkOutsideLibraryRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "gamelists", "gba")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "safe.gba"), []byte("safe-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	gamelist := filepath.Join(dir, "gamelist.xml")
	if err := os.WriteFile(gamelist, []byte(`<gameList><game><path>./safe.gba</path><name>Safe</name></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "es_systems.xml")
	if err := os.WriteFile(secret, []byte(`<systemList><system><name>gba</name><command>secret %ROM%</command></system></systemList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(root, "es_systems.xml")
	if err := os.Symlink(secret, selected); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PreviewESDEWithRuntime(root, gamelist, selected, "gba", ""); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("outside symlink was not rejected: %v", err)
	}
}

func TestPegasusMultiFileIsOneEdition(t *testing.T) {
	s := openStore(t)
	root := filepath.Join("..", "..", "testdata", "pegasus")
	source := filepath.Join(root, "gba", "metadata.pegasus.txt")
	r, err := ImportPegasus(context.Background(), s, root, source, "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if r.Imported != 2 {
		t.Fatalf("imported=%d", r.Imported)
	}
	games, err := s.ListGames(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range games {
		if w.DefaultTitle == "Multi Disc Demo" {
			found = true
			if len(w.Editions) != 1 || len(w.Editions[0].Artifacts) != 2 {
				t.Fatalf("expected 1 edition/2 artifacts: %#v", w)
			}
		}
	}
	if !found {
		t.Fatal("multi-file game missing")
	}
}

func TestPegasusDiscoversConventionalMediaAndKeepsExplicitKindPrecedence(t *testing.T) {
	root := t.TempDir()
	platformDir := filepath.Join(root, "pokemini")
	mediaDir := filepath.Join(platformDir, "media", "Fixture File")
	explicitDir := filepath.Join(platformDir, "selected")
	for _, directory := range []string{platformDir, mediaDir, explicitDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(platformDir, "Fixture File.zip"), []byte("rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"boxFront.png":  "conventional-cover",
		"cartridge.png": "cartridge",
		"logo.jpg":      "logo",
		"video.mp4":     "video",
		"notes.txt":     "ignored",
	} {
		if err := os.WriteFile(filepath.Join(mediaDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(explicitDir, "cover.png"), []byte("explicit-cover"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(platformDir, "metadata.pegasus.txt")
	if err := os.WriteFile(metadata, []byte("collection: Fixture\n\ngame: 本地标题\nfile: Fixture File.zip\nassets.boxFront: selected/cover.png\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	games, err := PreviewPegasus(root, metadata, "pokemini", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || len(games[0].Media) != 4 {
		t.Fatalf("conventional media=%#v", games)
	}
	want := map[string]string{
		"cover":     "pokemini/selected/cover.png",
		"cartridge": "pokemini/media/Fixture File/cartridge.png",
		"logo":      "pokemini/media/Fixture File/logo.jpg",
		"video":     "pokemini/media/Fixture File/video.mp4",
	}
	for _, item := range games[0].Media {
		if item.Path != want[item.Kind] || item.SHA256 == "" || item.Size == 0 {
			t.Fatalf("media item=%#v want=%q", item, want[item.Kind])
		}
		delete(want, item.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing media kinds=%#v", want)
	}
}

func TestPegasusConventionalMediaRejectsSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	platformDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(filepath.Join(platformDir, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platformDir, "game.gba"), []byte("rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(platformDir, "metadata.pegasus.txt")
	if err := os.WriteFile(metadata, []byte("collection: GBA\n\ngame: Game\nfile: game.gba\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "boxFront.png"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(platformDir, "media", "Game")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PreviewPegasus(root, metadata, "gba", "en"); err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), outside) {
		t.Fatalf("symlinked conventional media error=%v", err)
	}
}

func TestFrontendMetadataCanResolveROMsFromAnExplicitSeparateContentRoot(t *testing.T) {
	root := t.TempDir()
	metadataDir := filepath.Join(root, "frontend", "gba")
	contentDir := filepath.Join(root, "roms", "gba")
	mediaDir := filepath.Join(metadataDir, "media")
	for _, directory := range []string{metadataDir, contentDir, mediaDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(contentDir, "separate.gba"), []byte("separate-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "cover.png"), append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 520)...), 0o600); err != nil {
		t.Fatal(err)
	}
	pegasus := filepath.Join(metadataDir, "metadata.pegasus.txt")
	if err := os.WriteFile(pegasus, []byte("collection: GBA\n\ngame: Separate\nfile: separate.gba\nassets.box_front: media/cover.png\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	withoutRoot, err := PreviewPegasus(root, pegasus, "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutRoot) != 1 || !withoutRoot[0].Artifacts[0].Missing {
		t.Fatalf("default metadata-relative lookup should stay missing: %#v", withoutRoot)
	}
	withRoot, err := PreviewPegasusWithContentRoot(root, pegasus, "roms/gba", "gba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(withRoot) != 1 || withRoot[0].Artifacts[0].Missing || withRoot[0].Artifacts[0].SHA256 == "" || withRoot[0].Artifacts[0].Path != "roms/gba/separate.gba" {
		t.Fatalf("separate content root was not applied: %#v", withRoot)
	}
	if len(withRoot[0].Media) != 1 || withRoot[0].Media[0].Path != "frontend/gba/media/cover.png" {
		t.Fatalf("media must remain relative to metadata: %#v", withRoot[0].Media)
	}

	gamelist := filepath.Join(metadataDir, "gamelist.xml")
	if err = os.WriteFile(gamelist, []byte(`<gameList><game><path>./separate.gba</path><name>Separate ES-DE</name><image>./media/cover.png</image></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := platforms.NewRegistry(platforms.All())
	if err != nil {
		t.Fatal(err)
	}
	esde, err := PreviewESDEWithContentRootAndRuntimeRegistry(root, gamelist, "roms/gba", "", "gba", "en", registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(esde) != 1 || esde[0].Artifacts[0].Missing || esde[0].Artifacts[0].Path != "roms/gba/separate.gba" || len(esde[0].Media) != 1 || esde[0].Media[0].Path != "frontend/gba/media/cover.png" {
		t.Fatalf("ES-DE separate root semantics drifted: %#v", esde)
	}
}

func TestSeparateContentRootRejectsMissingAndSymlinkedDirectories(t *testing.T) {
	root := t.TempDir()
	metadataDir := filepath.Join(root, "metadata")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(metadataDir, "metadata.pegasus.txt")
	if err := os.WriteFile(metadata, []byte("game: Safe\nfile: safe.gba\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewPegasusWithContentRoot(root, metadata, "missing", "gba", "en"); err == nil || !strings.Contains(err.Error(), "existing real directory") || strings.Contains(err.Error(), root) {
		t.Fatalf("missing content root error is not safe: %v", err)
	}
	outside := t.TempDir()
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PreviewPegasusWithContentRoot(root, metadata, "linked", "gba", "en"); err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), outside) {
		t.Fatalf("symlinked content root error is not safe: %v", err)
	}
}

func TestESDEImport(t *testing.T) {
	s := openStore(t)
	root := filepath.Join("..", "..", "testdata", "esde")
	source := filepath.Join(root, "gamelists", "gba", "gamelist.xml")
	r, err := ImportESDE(context.Background(), s, root, source, "gba", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if r.Imported != 1 {
		t.Fatalf("imported=%d", r.Imported)
	}
	games, _ := s.ListGames(context.Background(), "zh-CN")
	if len(games) != 1 || games[0].DisplayTitle != "示例游戏 汉化版" {
		t.Fatalf("unexpected games: %#v", games)
	}
}

func TestCLICommitSkipsMissingAndRollsBackReadyBatchOnFailure(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	result, err := Commit(ctx, s, []catalog.ImportedGame{{Platform: "gba", DefaultTitle: "Missing", EditionTitle: "Missing", EditionType: "original", Artifacts: []catalog.NewArtifact{{Path: "gba/missing.gba", Missing: true}}}})
	if err != nil || result.Parsed != 1 || result.Imported != 0 || result.Skipped != 1 {
		t.Fatalf("missing result=%#v err=%v", result, err)
	}
	_, err = Commit(ctx, s, []catalog.ImportedGame{
		{Platform: "gba", DefaultTitle: "Ready", EditionTitle: "Ready", EditionType: "original", Artifacts: []catalog.NewArtifact{{Path: "gba/ready.gba", SHA256: "ready-hash"}}},
		{Platform: "gba", DefaultTitle: "Broken", EditionTitle: "Broken", EditionType: "invalid", Artifacts: []catalog.NewArtifact{{Path: "gba/broken.gba", SHA256: "broken-hash"}}},
	})
	if err == nil {
		t.Fatal("expected invalid second candidate to abort atomic CLI batch")
	}
	games, listErr := s.ListGames(ctx, "")
	if listErr != nil || len(games) != 0 {
		t.Fatalf("CLI batch left partial games=%#v err=%v", games, listErr)
	}
}
