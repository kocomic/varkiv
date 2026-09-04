package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/saves"
)

func webEmulatorIdentity(name string, content []byte) webEmulatorAssetIdentity {
	digest := sha256.Sum256(content)
	return webEmulatorAssetIdentity{Path: name, Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
}

func withWebEmulatorManifestForTest(manifest []webEmulatorAssetIdentity) Option {
	return func(server *Server) { server.webEmulatorManifest = manifest }
}

func webEmulationFixture(t *testing.T, platform string, options ...Option) (*Server, http.Handler, catalog.Edition, catalog.Artifact, string, []byte) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(filepath.Join(root, platform), 0o755); err != nil {
		t.Fatal(err)
	}
	dbRoot := t.TempDir()
	store, err := catalog.Open(filepath.Join(dbRoot, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	game, err := store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Browser Fixture", Platform: platform})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(context.Background(), catalog.NewEdition{GameID: game.ID, DefaultTitle: "Browser Fixture", EditionType: "homebrew"})
	if err != nil {
		t.Fatal(err)
	}
	content := append([]byte{'N', 'E', 'S', 0x1a, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, bytes.Repeat([]byte{0xea}, 48)...)
	romPath := filepath.Join(root, platform, "fixture.nes")
	if err = os.WriteFile(romPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	artifact, err := store.AddArtifact(context.Background(), catalog.NewArtifact{
		EditionID: edition.ID, Path: filepath.ToSlash(filepath.Join(platform, "fixture.nes")), Role: "rom",
		Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), StorageKind: "library", OriginalName: "fixture.nes",
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(store, root, append([]Option{WithStateRoot(filepath.Join(dbRoot, "state"))}, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	return app, app.Handler(), edition, artifact, romPath, content
}

func authenticatedWebRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestWebEmulationDriverIDNormalizesRuntimeCoreNames(t *testing.T) {
	for core, want := range map[string]string{
		"fceumm":           "builtin-driver-emulatorjs-fceumm",
		"genesis_plus_gx":  "builtin-driver-emulatorjs-genesis-plus-gx",
		"mednafen_ngp":     "builtin-driver-emulatorjs-mednafen-ngp",
		"mupen64plus_next": "builtin-driver-emulatorjs-mupen64plus-next",
	} {
		if got := webEmulationDriverID(core); got != want {
			t.Fatalf("webEmulationDriverID(%q) = %q, want %q", core, got, want)
		}
	}
}

func TestWebEmulationArtifactExtensionsArePlatformSpecific(t *testing.T) {
	if len(webEmulationCores) != len(webEmulationPlatformExtensions) || len(webEmulationCores) != len(webEmulationPlatformMinimumBytes) {
		t.Fatalf("web emulation platform maps drift: cores=%d extensions=%d minimums=%d", len(webEmulationCores), len(webEmulationPlatformExtensions), len(webEmulationPlatformMinimumBytes))
	}
	for platform, core := range webEmulationCores {
		if core == "" || len(webEmulationPlatformExtensions[platform]) == 0 || webEmulationPlatformMinimumBytes[platform] <= 0 {
			t.Fatalf("web emulation platform %q has incomplete capability: core=%q extensions=%v minimum=%d", platform, core, webEmulationPlatformExtensions[platform], webEmulationPlatformMinimumBytes[platform])
		}
	}
	for platform := range webEmulationPlatformExtensions {
		if webEmulationCores[platform] == "" {
			t.Fatalf("web emulation extensions have no core for platform %q", platform)
		}
	}
	for _, test := range []struct {
		platform string
		path     string
		want     bool
	}{
		{platform: "n64", path: "game.z64", want: true},
		{platform: "n64", path: "game.nes", want: false},
		{platform: "nes", path: "game.nes", want: true},
		{platform: "nes", path: "game.gba", want: false},
		{platform: "gba", path: "game.GBA", want: true},
		{platform: "ngpc", path: "game.ngc", want: true},
		{platform: "ngpc", path: "game.NGP", want: true},
		{platform: "ngpc", path: "game.zip", want: true},
		{platform: "ngpc", path: "game.7z", want: false},
		{platform: "ps2", path: "game.iso", want: false},
	} {
		if got := webEmulationArtifactSupported(test.platform, test.path); got != test.want {
			t.Fatalf("webEmulationArtifactSupported(%q, %q) = %t, want %t", test.platform, test.path, got, test.want)
		}
	}
}

func TestWebEmulationArtifactPlausibilityRejectsPlaceholders(t *testing.T) {
	for _, test := range []struct {
		platform string
		path     string
		size     int64
		want     bool
	}{
		{platform: "gba", path: "game.gba", size: 49, want: false},
		{platform: "gba", path: "game.gba", size: 0xc0, want: true},
		{platform: "gb", path: "game.gb", size: 32767, want: false},
		{platform: "gb", path: "game.gb", size: 32768, want: true},
		{platform: "nes", path: "game.nes", size: 63, want: false},
		{platform: "nes", path: "game.nes", size: 64, want: true},
		{platform: "ngpc", path: "game.ngc", size: 64, want: true},
		{platform: "ngpc", path: "game.zip", size: 64, want: true},
		{platform: "gba", path: "game.nes", size: 32768, want: false},
	} {
		if got := webEmulationArtifactPlausible(test.platform, test.path, test.size); got != test.want {
			t.Fatalf("webEmulationArtifactPlausible(%q, %q, %d) = %t, want %t", test.platform, test.path, test.size, got, test.want)
		}
	}
}

func TestWebEmulationSingleROMZIPBoundary(t *testing.T) {
	writeArchive := func(t *testing.T, entries map[string][]byte) *os.File {
		t.Helper()
		archivePath := filepath.Join(t.TempDir(), "fixture.zip")
		output, err := os.Create(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(output)
		for name, content := range entries {
			entry, createErr := writer.Create(name)
			if createErr != nil {
				t.Fatal(createErr)
			}
			if _, writeErr := entry.Write(content); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err = output.Close(); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return file
	}

	if err := validateWebROMHeader("ngpc", "fixture.zip", writeArchive(t, map[string][]byte{"fixture.ngc": bytes.Repeat([]byte{0x5a}, 64)})); err != nil {
		t.Fatalf("single flat NGPC ROM ZIP rejected: %v", err)
	}
	for name, entries := range map[string]map[string][]byte{
		"multiple ROMs": {"one.ngc": bytes.Repeat([]byte{1}, 64), "two.ngp": bytes.Repeat([]byte{2}, 64)},
		"nested path":   {"folder/game.ngc": bytes.Repeat([]byte{1}, 64)},
		"wrong format":  {"game.gba": bytes.Repeat([]byte{1}, 64)},
		"placeholder":   {"game.ngc": bytes.Repeat([]byte{1}, 63)},
	} {
		if err := validateWebROMHeader("ngpc", "fixture.zip", writeArchive(t, entries)); err == nil {
			t.Fatalf("%s archive was accepted", name)
		}
	}
}

func TestWebEmulationROMHeaderValidationRejectsWrongPlatformContent(t *testing.T) {
	write := func(t *testing.T, content []byte) *os.File {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fixture.rom")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return file
	}

	gba := make([]byte, 0xc0)
	gba[0xb2] = 0x96
	for _, value := range gba[0xa0:0xbd] {
		gba[0xbd] -= value
	}
	gba[0xbd] -= 0x19
	if err := validateWebROMHeader("gba", "fixture.gba", write(t, gba)); err != nil {
		t.Fatalf("valid GBA header rejected: %v", err)
	}
	wrongFixedByte := append([]byte(nil), gba...)
	wrongFixedByte[0xb2] = 0
	if err := validateWebROMHeader("gba", "fixture.gba", write(t, wrongFixedByte)); err == nil {
		t.Fatal("GBA header with invalid fixed byte was accepted")
	}
	wrongChecksum := append([]byte(nil), gba...)
	wrongChecksum[0xbd]++
	if err := validateWebROMHeader("gba", "fixture.gba", write(t, wrongChecksum)); err == nil {
		t.Fatal("GBA header with invalid checksum was accepted")
	}

	gb := make([]byte, 0x150)
	copy(gb[0x104:0x134], []byte{
		0xce, 0xed, 0x66, 0x66, 0xcc, 0x0d, 0x00, 0x0b,
		0x03, 0x73, 0x00, 0x83, 0x00, 0x0c, 0x00, 0x0d,
		0x00, 0x08, 0x11, 0x1f, 0x88, 0x89, 0x00, 0x0e,
		0xdc, 0xcc, 0x6e, 0xe6, 0xdd, 0xdd, 0xd9, 0x99,
		0xbb, 0xbb, 0x67, 0x63, 0x6e, 0x0e, 0xec, 0xcc,
		0xdd, 0xdc, 0x99, 0x9f, 0xbb, 0xb9, 0x33, 0x3e,
	})
	copy(gb[0x134:0x144], []byte("VARKIV TEST"))
	for _, value := range gb[0x134:0x14d] {
		gb[0x14d] = gb[0x14d] - value - 1
	}
	if err := validateWebROMHeader("gb", "fixture.gb", write(t, gb)); err != nil {
		t.Fatalf("valid Game Boy header rejected: %v", err)
	}
	wrongGBLogo := append([]byte(nil), gb...)
	wrongGBLogo[0x104] ^= 0xff
	if err := validateWebROMHeader("gb", "fixture.gb", write(t, wrongGBLogo)); err == nil {
		t.Fatal("Game Boy header with invalid logo was accepted")
	}
	wrongGBChecksum := append([]byte(nil), gb...)
	wrongGBChecksum[0x14d]++
	if err := validateWebROMHeader("gb", "fixture.gb", write(t, wrongGBChecksum)); err == nil {
		t.Fatal("Game Boy header with invalid checksum was accepted")
	}

	nes := append([]byte{'N', 'E', 'S', 0x1a}, make([]byte, 60)...)
	if err := validateWebROMHeader("nes", "fixture.nes", write(t, nes)); err != nil {
		t.Fatalf("valid NES header rejected: %v", err)
	}
	if err := validateWebROMHeader("nes", "fixture.nes", write(t, bytes.Repeat([]byte{0xea}, 64))); err == nil {
		t.Fatal("NES content without an iNES header was accepted")
	}
}

func TestWebEmulationSessionStreamsOnlyVerifiedROMWithShortLivedCapability(t *testing.T) {
	app, handler, edition, artifact, romPath, content := webEmulationFixture(t, "nes", WithToken("admin-secret"), WithWebEmulatorAssets("https://cdn.emulatorjs.org/4.2.3/data/"))

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/web-emulation/sessions", strings.NewReader(`{"edition_id":"`+edition.ID+`"}`)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("session creation without owner token: %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	created := authenticatedWebRequest(t, handler, http.MethodPost, "/api/v1/web-emulation/sessions", webEmulationSessionInput{EditionID: edition.ID, Locale: "zh-CN"})
	if created.Code != http.StatusCreated {
		t.Fatalf("session create: %d %s", created.Code, created.Body.String())
	}
	var session webEmulationSession
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.Status.Available || session.Status.Core != "fceumm" || session.Status.DriverID != "builtin-driver-emulatorjs-fceumm" || session.Status.ArtifactID != artifact.ID || session.Status.SaveSupport != "automatic-when-core-emits" || session.Status.GamepadMapping != "user-configurable" || !slices.Equal(session.Status.InputSupport, []string{"keyboard", "gamepad", "touch"}) || session.AssetSource != "external" {
		t.Fatalf("unexpected session: %#v", session)
	}
	driver, err := app.store.GetEmulatorDriver(context.Background(), session.Status.DriverID)
	if err != nil || driver.Family != "emulatorjs" || len(driver.Targets) != 1 || driver.Targets[0] != "web" || driver.Save.Portability != "core-dependent" {
		t.Fatalf("browser driver contract is missing or drifted: %#v, %v", driver, err)
	}
	if strings.Contains(created.Body.String(), "admin-secret") || strings.Contains(created.Body.String(), romPath) || !strings.HasPrefix(session.PlayerURL, "/play/") {
		t.Fatalf("session response exposed private input or omitted player capability: %s", created.Body.String())
	}
	token := strings.TrimPrefix(session.PlayerURL, "/play/")

	player := httptest.NewRecorder()
	handler.ServeHTTP(player, httptest.NewRequest(http.MethodGet, session.PlayerURL, nil))
	if player.Code != http.StatusOK || !strings.Contains(player.Body.String(), "https://cdn.emulatorjs.org/4.2.3/data/loader.js") || strings.Contains(player.Body.String(), romPath) {
		t.Fatalf("player response: %d %s", player.Code, player.Body.String())
	}
	if player.Header().Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(player.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") || player.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("player security headers are incomplete: %#v", player.Header())
	}
	if !strings.Contains(player.Body.String(), "正在准备网页模拟器") || !strings.Contains(player.Body.String(), "EJS_onSaveSave") || !strings.Contains(player.Body.String(), `data-runtime-state="loading"`) || !strings.Contains(player.Body.String(), "varkiv:player-started") || !strings.Contains(player.Body.String(), "varkiv:web-player-state") || !strings.Contains(player.Body.String(), "varkiv:web-player-input") || !strings.Contains(player.Body.String(), "navigator.getGamepads") || strings.Contains(player.Body.String(), "pad.id") || !strings.Contains(player.Body.String(), "location.origin") || !strings.Contains(player.Body.String(), "if(restoreQueue)return restoreQueue") || !strings.Contains(player.Body.String(), "Date.now()-lastRestoreAt<1000") || strings.Contains(player.Body.String(), "Preparing browser player") || strings.Contains(player.Body.String(), "postMessage({type:'varkiv:web-player-state',state:value},'*')") {
		t.Fatalf("localized automatic save bridge is missing: %s", player.Body.String())
	}

	saveURL := "/api/v1/web-emulation/saves/" + token
	emptySave := httptest.NewRecorder()
	handler.ServeHTTP(emptySave, httptest.NewRequest(http.MethodGet, saveURL, nil))
	if emptySave.Code != http.StatusNoContent {
		t.Fatalf("empty browser save response: %d %s", emptySave.Code, emptySave.Body.String())
	}
	firstSaveBytes := []byte("browser-save-one")
	firstSaveRequest := httptest.NewRequest(http.MethodPost, saveURL, bytes.NewReader(firstSaveBytes))
	firstSaveRequest.Header.Set("Content-Type", "application/octet-stream")
	firstSave := httptest.NewRecorder()
	handler.ServeHTTP(firstSave, firstSaveRequest)
	if firstSave.Code != http.StatusCreated {
		t.Fatalf("first browser save upload: %d %s", firstSave.Code, firstSave.Body.String())
	}
	var firstResult saves.PushResult
	if decodeErr := json.Unmarshal(firstSave.Body.Bytes(), &firstResult); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !firstResult.Created || firstResult.Conflict || firstResult.Revision.DriverID != "builtin-driver-emulatorjs-fceumm" || firstResult.Revision.DeviceID != webEmulationDeviceID {
		t.Fatalf("unexpected first browser save result: %#v", firstResult)
	}
	restoredSave := httptest.NewRecorder()
	handler.ServeHTTP(restoredSave, httptest.NewRequest(http.MethodGet, saveURL, nil))
	if restoredSave.Code != http.StatusOK || !bytes.Equal(restoredSave.Body.Bytes(), firstSaveBytes) || restoredSave.Header().Get("X-Varkiv-Revision") != firstResult.Revision.ID {
		t.Fatalf("browser save restore: %d headers=%#v body=%q", restoredSave.Code, restoredSave.Header(), restoredSave.Body.Bytes())
	}
	secondSaveRequest := httptest.NewRequest(http.MethodPost, saveURL, strings.NewReader("browser-save-two"))
	secondSaveRequest.Header.Set("Content-Type", "application/octet-stream")
	secondSaveRequest.Header.Set("X-Varkiv-Base-Revision", firstResult.Revision.ID)
	secondSave := httptest.NewRecorder()
	handler.ServeHTTP(secondSave, secondSaveRequest)
	if secondSave.Code != http.StatusCreated || !strings.Contains(secondSave.Body.String(), `"conflict":false`) {
		t.Fatalf("linear browser save update: %d %s", secondSave.Code, secondSave.Body.String())
	}

	contentURL := "/api/v1/web-emulation/content/" + token + "/fixture.nes"
	complete := httptest.NewRecorder()
	handler.ServeHTTP(complete, httptest.NewRequest(http.MethodGet, contentURL, nil))
	if complete.Code != http.StatusOK || !bytes.Equal(complete.Body.Bytes(), content) || complete.Header().Get("Accept-Ranges") != "bytes" || complete.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("complete content response: %d headers=%#v body=%q", complete.Code, complete.Header(), complete.Body.Bytes())
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, contentURL, nil)
	rangeRequest.Header.Set("Range", "bytes=4-11")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || !bytes.Equal(ranged.Body.Bytes(), content[4:12]) || ranged.Header().Get("Content-Range") != "bytes 4-11/64" {
		t.Fatalf("range response: %d headers=%#v body=%x", ranged.Code, ranged.Header(), ranged.Body.Bytes())
	}

	tampered := httptest.NewRecorder()
	handler.ServeHTTP(tampered, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/content/"+token+"x/fixture.nes", nil))
	if tampered.Code != http.StatusUnauthorized {
		t.Fatalf("tampered capability status: %d %s", tampered.Code, tampered.Body.String())
	}
	past, err := app.signWebEmulationToken(webEmulationToken{ArtifactID: artifact.ID, EditionID: edition.ID, SHA256: artifact.SHA256, Locale: "en", ExpiresAt: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	expired := httptest.NewRecorder()
	handler.ServeHTTP(expired, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/content/"+past+"/fixture.nes", nil))
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired capability status: %d %s", expired.Code, expired.Body.String())
	}

	changed := bytes.Repeat([]byte{0x44}, len(content))
	if err = os.WriteFile(romPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	drifted := httptest.NewRecorder()
	handler.ServeHTTP(drifted, httptest.NewRequest(http.MethodGet, contentURL, nil))
	if drifted.Code != http.StatusConflict || bytes.Contains(drifted.Body.Bytes(), changed[:16]) || !strings.HasPrefix(drifted.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("drift response leaked ROM bytes: %d headers=%#v body=%q", drifted.Code, drifted.Header(), drifted.Body.Bytes())
	}
}

func TestWebEmulationAvailabilityIsExplicitAndConfigurationBound(t *testing.T) {
	_, disabledHandler, edition, _, _, _ := webEmulationFixture(t, "nes")
	disabled := httptest.NewRecorder()
	disabledHandler.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/editions/"+edition.ID, nil))
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"reason":"not_configured"`) {
		t.Fatalf("disabled status: %d %s", disabled.Code, disabled.Body.String())
	}
	capabilities := httptest.NewRecorder()
	disabledHandler.ServeHTTP(capabilities, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if !strings.Contains(capabilities.Body.String(), `"web_emulation":false`) {
		t.Fatalf("disabled capability drift: %s", capabilities.Body.String())
	}

	_, unsupportedHandler, unsupportedEdition, _, _, _ := webEmulationFixture(t, "ps2", WithWebEmulatorAssets("/emulatorjs/"))
	unsupported := httptest.NewRecorder()
	unsupportedHandler.ServeHTTP(unsupported, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/editions/"+unsupportedEdition.ID, nil))
	if unsupported.Code != http.StatusOK || !strings.Contains(unsupported.Body.String(), `"reason":"platform_not_supported"`) {
		t.Fatalf("native-only platform status: %d %s", unsupported.Code, unsupported.Body.String())
	}
	capabilities = httptest.NewRecorder()
	unsupportedHandler.ServeHTTP(capabilities, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if !strings.Contains(capabilities.Body.String(), `"web_emulation":true`) {
		t.Fatalf("enabled capability drift: %s", capabilities.Body.String())
	}

	_, mismatchedHandler, mismatchedEdition, _, _, _ := webEmulationFixture(t, "gba", WithWebEmulatorAssets("/emulatorjs/"))
	mismatched := httptest.NewRecorder()
	mismatchedHandler.ServeHTTP(mismatched, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/editions/"+mismatchedEdition.ID, nil))
	if mismatched.Code != http.StatusOK || !strings.Contains(mismatched.Body.String(), `"reason":"artifact_not_supported"`) {
		t.Fatalf("cross-platform extension was accepted: %d %s", mismatched.Code, mismatched.Body.String())
	}
}

func TestWebEmulatorAssetConfigurationRejectsExecutableAndAmbiguousURLs(t *testing.T) {
	for _, value := range []string{"javascript:alert(1)", "//cdn.example/data/", "https://user:pass@example.test/data/", "https://example.test/data/?token=secret"} {
		t.Run(value, func(t *testing.T) {
			root := t.TempDir()
			store, err := catalog.Open(filepath.Join(root, "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err = New(store, filepath.Join(root, "library"), WithWebEmulatorAssets(value)); err == nil {
				t.Fatalf("unsafe asset URL accepted: %q", value)
			}
		})
	}
}

func TestLocalWebEmulatorDirectoryIsConfinedAndServed(t *testing.T) {
	assets := t.TempDir()
	loaderContent := []byte("window.localEmulator = true;")
	coreContent := []byte{0x37, 0x7a, 0xbc, 0xaf}
	manifest := []webEmulatorAssetIdentity{
		webEmulatorIdentity("loader.js", loaderContent),
		webEmulatorIdentity("cores/fixture.data", coreContent),
	}
	if err := os.MkdirAll(filepath.Join(assets, "cores"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "loader.js"), loaderContent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "cores", "fixture.data"), coreContent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "unlisted.js"), []byte("unverified"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, handler, edition, _, _, _ := webEmulationFixture(t, "nes", withWebEmulatorManifestForTest(manifest), WithWebEmulatorDirectory(assets))

	loader := httptest.NewRecorder()
	handler.ServeHTTP(loader, httptest.NewRequest(http.MethodGet, "/emulatorjs/loader.js", nil))
	if loader.Code != http.StatusOK || loader.Body.String() != "window.localEmulator = true;" {
		t.Fatalf("local loader response: %d %q", loader.Code, loader.Body.String())
	}
	if loader.Header().Get("Cache-Control") != "public, max-age=3600, must-revalidate" || loader.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("local loader security/cache headers: %#v", loader.Header())
	}
	core := httptest.NewRecorder()
	handler.ServeHTTP(core, httptest.NewRequest(http.MethodGet, "/emulatorjs/cores/fixture.data", nil))
	if core.Code != http.StatusOK || !bytes.Equal(core.Body.Bytes(), []byte{0x37, 0x7a, 0xbc, 0xaf}) {
		t.Fatalf("nested local asset response: %d %x", core.Code, core.Body.Bytes())
	}
	directory := httptest.NewRecorder()
	handler.ServeHTTP(directory, httptest.NewRequest(http.MethodGet, "/emulatorjs/cores/", nil))
	if directory.Code != http.StatusNotFound || strings.Contains(directory.Body.String(), "fixture.data") {
		t.Fatalf("directory listing exposed: %d %q", directory.Code, directory.Body.String())
	}
	unlisted := httptest.NewRecorder()
	handler.ServeHTTP(unlisted, httptest.NewRequest(http.MethodGet, "/emulatorjs/unlisted.js", nil))
	if unlisted.Code != http.StatusNotFound || strings.Contains(unlisted.Body.String(), "unverified") {
		t.Fatalf("unlisted asset was served: %d %q", unlisted.Code, unlisted.Body.String())
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/editions/"+edition.ID, nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"available":true`) {
		t.Fatalf("local directory did not enable web emulation: %d %s", status.Code, status.Body.String())
	}
	readiness := httptest.NewRecorder()
	handler.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/readiness", nil))
	var ready webEmulatorReadiness
	if readiness.Code != http.StatusOK || json.Unmarshal(readiness.Body.Bytes(), &ready) != nil {
		t.Fatalf("local readiness response: %d %s", readiness.Code, readiness.Body.String())
	}
	if !ready.Enabled || ready.Mode != "self-hosted-verified" || !ready.SameOrigin || !ready.IntegrityVerified || ready.EmulatorJSVersion != webEmulatorAssetVersion || ready.AssetsVerified != 2 || ready.BytesVerified != int64(len(loaderContent)+len(coreContent)) {
		t.Fatalf("local readiness drift: %#v", ready)
	}
	expectedPlatforms := make([]string, 0, len(webEmulationCores))
	for platform := range webEmulationCores {
		expectedPlatforms = append(expectedPlatforms, platform)
	}
	sort.Strings(expectedPlatforms)
	expectedExtensionSet := make(map[string]bool)
	expectedCapabilities := make([]webEmulatorPlatformCapability, 0, len(expectedPlatforms))
	for _, platform := range expectedPlatforms {
		extensions := make([]string, 0, len(webEmulationPlatformExtensions[platform]))
		for extension := range webEmulationPlatformExtensions[platform] {
			extensions = append(extensions, extension)
			expectedExtensionSet[extension] = true
		}
		sort.Strings(extensions)
		expectedCapabilities = append(expectedCapabilities, webEmulatorPlatformCapability{PlatformID: platform, Core: webEmulationCores[platform], Extensions: extensions, MinimumROMBytes: webEmulationPlatformMinimumBytes[platform]})
	}
	expectedExtensions := make([]string, 0, len(expectedExtensionSet))
	for extension := range expectedExtensionSet {
		expectedExtensions = append(expectedExtensions, extension)
	}
	sort.Strings(expectedExtensions)
	if !slices.Equal(ready.SupportedPlatforms, expectedPlatforms) || !slices.Equal(ready.SupportedExtensions, expectedExtensions) || len(ready.PlatformCapabilities) != len(expectedCapabilities) {
		t.Fatalf("local readiness capability drift: platforms=%v extensions=%v capabilities=%v", ready.SupportedPlatforms, ready.SupportedExtensions, ready.PlatformCapabilities)
	}
	for index, expected := range expectedCapabilities {
		actual := ready.PlatformCapabilities[index]
		if actual.PlatformID != expected.PlatformID || actual.Core != expected.Core || !slices.Equal(actual.Extensions, expected.Extensions) || actual.MinimumROMBytes != expected.MinimumROMBytes {
			t.Fatalf("local readiness platform capability %d drift: actual=%#v expected=%#v", index, actual, expected)
		}
	}
	if strings.Contains(readiness.Body.String(), assets) {
		t.Fatalf("local readiness leaked host path: %s", readiness.Body.String())
	}
	driftedContent := append([]byte(nil), loaderContent...)
	driftedContent[0] ^= 0xff
	if err := os.WriteFile(filepath.Join(assets, "loader.js"), driftedContent, 0o600); err != nil {
		t.Fatal(err)
	}
	drifted := httptest.NewRecorder()
	handler.ServeHTTP(drifted, httptest.NewRequest(http.MethodGet, "/emulatorjs/loader.js", nil))
	if drifted.Code != http.StatusNotFound || bytes.Contains(drifted.Body.Bytes(), driftedContent) {
		t.Fatalf("runtime asset drift was served: %d %q", drifted.Code, drifted.Body.String())
	}
}

func TestLocalWebEmulatorDirectoryConfigurationFailsClosed(t *testing.T) {
	root := t.TempDir()
	store, err := catalog.Open(filepath.Join(root, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assets := filepath.Join(root, "assets")
	if err = os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []webEmulatorAssetIdentity{webEmulatorIdentity("loader.js", []byte("loader"))}
	if _, err = New(store, filepath.Join(root, "library"), withWebEmulatorManifestForTest(manifest), WithWebEmulatorDirectory(assets)); err == nil || !strings.Contains(err.Error(), "loader.js") {
		t.Fatalf("directory without loader.js was accepted: %v", err)
	}
	if err = os.WriteFile(filepath.Join(assets, "loader.js"), []byte("loader"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = New(store, filepath.Join(root, "library"), WithWebEmulatorDirectory(assets), WithWebEmulatorAssets("https://cdn.example/data/")); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("ambiguous local and remote assets were accepted: %v", err)
	}
}

func TestWebEmulatorDirectoryIntegrityFailsClosedWithoutLeakingRoot(t *testing.T) {
	content := []byte("pinned asset")
	manifest := []webEmulatorAssetIdentity{webEmulatorIdentity("cores/pinned.data", content)}
	create := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "cores"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "cores", "pinned.data"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	t.Run("valid", func(t *testing.T) {
		root := create(t)
		resolved, report, err := validateWebEmulatorDirectory(root, manifest)
		if err != nil || resolved == "" || report.AssetsVerified != 1 || report.BytesVerified != int64(len(content)) {
			t.Fatalf("valid manifest rejected: resolved=%q report=%#v err=%v", resolved, report, err)
		}
	})
	for _, test := range []struct {
		name    string
		mutate  func(*testing.T, string)
		message string
	}{
		{name: "missing", mutate: func(t *testing.T, root string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, "cores", "pinned.data")); err != nil {
				t.Fatal(err)
			}
		}, message: "missing"},
		{name: "size", mutate: func(t *testing.T, root string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, "cores", "pinned.data"), append(content, '!'), 0o600); err != nil {
				t.Fatal(err)
			}
		}, message: "size"},
		{name: "hash", mutate: func(t *testing.T, root string) {
			t.Helper()
			changed := append([]byte(nil), content...)
			changed[0] ^= 0xff
			if err := os.WriteFile(filepath.Join(root, "cores", "pinned.data"), changed, 0o600); err != nil {
				t.Fatal(err)
			}
		}, message: "hash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := create(t)
			test.mutate(t, root)
			_, _, err := validateWebEmulatorDirectory(root, manifest)
			if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), root) {
				t.Fatalf("unsafe verification error: %v", err)
			}
		})
	}
	t.Run("asset symlink", func(t *testing.T) {
		root := create(t)
		target := filepath.Join(t.TempDir(), "external.data")
		if err := os.WriteFile(target, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "cores", "pinned.data")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "cores", "pinned.data")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		_, _, err := validateWebEmulatorDirectory(root, manifest)
		if err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), root) {
			t.Fatalf("asset symlink accepted or leaked root: %v", err)
		}
	})
	t.Run("root symlink", func(t *testing.T) {
		root := create(t)
		link := filepath.Join(t.TempDir(), "assets-link")
		if err := os.Symlink(root, link); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		_, _, err := validateWebEmulatorDirectory(link, manifest)
		if err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), root) {
			t.Fatalf("root symlink accepted or leaked root: %v", err)
		}
	})
}

func TestWebEmulatorManifestMatchesSharedFixture(t *testing.T) {
	var fixture struct {
		EmulatorJS struct {
			Version string                     `json:"version"`
			Assets  []webEmulatorAssetIdentity `json:"assets"`
		} `json:"emulatorjs"`
		Fixtures []struct {
			ID          string `json:"id"`
			VisualProbe *struct {
				CenterFraction           float64 `json:"center_fraction"`
				BrightnessThreshold      int     `json:"brightness_threshold"`
				MinBrightPixels          int     `json:"min_bright_pixels"`
				ExpectedCenterSHA256     string  `json:"expected_center_sha256"`
				TemporalOccupancyFrames  int     `json:"temporal_occupancy_frames"`
				TemporalIntervalMS       int     `json:"temporal_interval_ms"`
				TemporalGridWidth        int     `json:"temporal_grid_width"`
				TemporalGridHeight       int     `json:"temporal_grid_height"`
				TemporalMinNonblankFrame int     `json:"temporal_min_nonblank_frames"`
				TemporalMinBrightCells   int     `json:"temporal_min_bright_cells"`
				TemporalMinCellHits      int     `json:"temporal_min_cell_hits"`
			} `json:"visual_probe"`
		} `json:"fixtures"`
	}
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "web-emulation", "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.EmulatorJS.Version != webEmulatorAssetVersion || len(fixture.EmulatorJS.Assets) != len(webEmulatorAssetManifest) {
		t.Fatalf("manifest header drift: version=%q assets=%d", fixture.EmulatorJS.Version, len(fixture.EmulatorJS.Assets))
	}
	for i, expected := range fixture.EmulatorJS.Assets {
		if actual := webEmulatorAssetManifest[i]; actual != expected {
			t.Fatalf("manifest asset %d drift: actual=%#v expected=%#v", i, actual, expected)
		}
	}
	if len(fixture.Fixtures) != 12 {
		t.Fatalf("browser fixture count drift: %d", len(fixture.Fixtures))
	}
	for _, item := range fixture.Fixtures {
		probe := item.VisualProbe
		if probe == nil || probe.CenterFraction <= 0 || probe.CenterFraction > 1 || probe.BrightnessThreshold < 0 || probe.BrightnessThreshold > 255 || probe.MinBrightPixels <= 0 {
			t.Fatalf("browser fixture %q can report started without a bounded visual probe: %#v", item.ID, probe)
		}
		if probe.ExpectedCenterSHA256 != "" {
			decoded, decodeErr := hex.DecodeString(probe.ExpectedCenterSHA256)
			if decodeErr != nil || len(decoded) != sha256.Size {
				t.Fatalf("browser fixture %q has an invalid terminal visual SHA-256: %q", item.ID, probe.ExpectedCenterSHA256)
			}
		}
		if probe.TemporalOccupancyFrames != 0 {
			if probe.TemporalOccupancyFrames < 2 || probe.TemporalOccupancyFrames > 120 ||
				probe.TemporalIntervalMS < 0 || probe.TemporalIntervalMS > 1000 ||
				probe.TemporalGridWidth < 16 || probe.TemporalGridWidth > 512 ||
				probe.TemporalGridHeight < 16 || probe.TemporalGridHeight > 512 ||
				probe.TemporalMinNonblankFrame < 1 || probe.TemporalMinNonblankFrame > probe.TemporalOccupancyFrames ||
				probe.TemporalMinBrightCells < 1 || probe.TemporalMinBrightCells > probe.TemporalGridWidth*probe.TemporalGridHeight ||
				probe.TemporalMinCellHits < 1 || probe.TemporalMinCellHits > probe.TemporalOccupancyFrames {
				t.Fatalf("browser fixture %q has an invalid temporal occupancy probe: %#v", item.ID, probe)
			}
		}
	}
}

func TestExternalWebEmulatorReadinessIsExplicitAndPrivacySafe(t *testing.T) {
	remote := "https://cdn.example.test/private/path/"
	_, handler, _, _, _, _ := webEmulationFixture(t, "nes", WithWebEmulatorAssets(remote))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/web-emulation/readiness", nil))
	var ready webEmulatorReadiness
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &ready) != nil {
		t.Fatalf("external readiness response: %d %s", response.Code, response.Body.String())
	}
	if !ready.Enabled || ready.Mode != "external-unverified" || ready.SameOrigin || ready.IntegrityVerified || ready.AssetsVerified != 0 || strings.Contains(response.Body.String(), remote) || strings.Contains(response.Body.String(), "cdn.example.test") {
		t.Fatalf("external readiness drift or leaked URL: %#v body=%s", ready, response.Body.String())
	}
}
