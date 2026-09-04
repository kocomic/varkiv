package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"varkiv/internal/bundler"
	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
	"varkiv/internal/runtimecfg"
	"varkiv/internal/saves"
)

func TestPreviewTokensAreOpaqueDeterministicAndDomainSeparated(t *testing.T) {
	server := &Server{}
	payload := struct {
		RawCommand string `json:"raw_command"`
		SourceRef  string `json:"source_ref"`
	}{RawCommand: `retroarch.exe -L private_core.dll "private game.zip"`, SourceRef: "private/source/metadata.pegasus.txt"}

	first, err := server.signPreviewValue(previewTokenDomainRuntimeHintBatch, payload)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := server.signPreviewValue(previewTokenDomainRuntimeHintBatch, payload)
	if err != nil || repeated != first {
		t.Fatalf("repeated token = %q, %v", repeated, err)
	}
	otherDomain, err := server.signPreviewValue(previewTokenDomainImport, payload)
	if err != nil || otherDomain == first {
		t.Fatalf("domain-separated token = %q, %v", otherDomain, err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(decoded) != sha256.Size {
		t.Fatalf("opaque token bytes = %d, %v", len(decoded), err)
	}
	if strings.Contains(first, payload.RawCommand) || strings.Contains(first, payload.SourceRef) {
		t.Fatalf("token exposed signed payload: %q", first)
	}
	if _, err = server.signPreviewValue("", payload); err == nil {
		t.Fatal("empty token domain was accepted")
	}
}

func TestEveryV1RouteAndOpenAPIMethodStayInSync(t *testing.T) {
	documented := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(openAPI))
	path := ""
	methodLine := regexp.MustCompile(`^(get|post|put|delete|patch):`)
	inPaths := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "paths:" {
			inPaths = true
			continue
		}
		if line == "components:" {
			break
		}
		if !inPaths {
			continue
		}
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		trimmed := strings.TrimSpace(line)
		match := methodLine.FindStringSubmatch(trimmed)
		if path != "" && strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "      ") && len(match) == 2 {
			documented[strings.ToUpper(match[1])+" "+path] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	prefixed := regexp.MustCompile(`HandleFunc\("([A-Z]+) "\+prefix(?:\+"([^"]*)")?`)
	for _, match := range prefixed.FindAllStringSubmatch(string(source), -1) {
		path = match[2]
		if path == "" {
			path = "/"
		}
		registered[match[1]+" "+path] = true
	}
	explicit := regexp.MustCompile(`HandleFunc\("([A-Z]+) (/api/v1[^"]*)"`)
	for _, match := range explicit.FindAllStringSubmatch(string(source), -1) {
		path = strings.TrimPrefix(match[2], "/api/v1")
		if path == "" {
			path = "/"
		}
		registered[match[1]+" "+path] = true
	}
	for route := range registered {
		if !documented[route] {
			t.Errorf("registered v1 route is missing from OpenAPI: %s", route)
		}
	}
	for route := range documented {
		if !registered[route] {
			t.Errorf("OpenAPI method has no registered v1 route: %s", route)
		}
	}
	parameter := regexp.MustCompile(`\{[^/]+\}`)
	for route := range registered {
		method, routePath, found := strings.Cut(route, " ")
		if !found {
			t.Fatalf("invalid registered route key %q", route)
		}
		concrete := parameter.ReplaceAllString(routePath, "fixture")
		allowed := allowedAPIMethods("/api/v1" + concrete)
		found = false
		for _, candidate := range allowed {
			if candidate == method {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("method-not-allowed map omits registered route: %s (got %v)", route, allowed)
		}
	}
	if len(registered) == 0 || len(documented) == 0 {
		t.Fatalf("route parity check found registered=%d documented=%d", len(registered), len(documented))
	}
}

func testServer(t *testing.T) (*catalog.Store, http.Handler, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	app, err := New(store, root)
	if err != nil {
		t.Fatal(err)
	}
	return store, app.Handler(), root
}

func jsonRequest(t *testing.T, handler http.Handler, method, path string, input any, output any) int {
	t.Helper()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, body)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code >= 400 {
		t.Fatalf("%s %s: %d %s", method, path, recorder.Code, recorder.Body.String())
	}
	if output != nil && recorder.Code != http.StatusNoContent {
		if err := json.Unmarshal(recorder.Body.Bytes(), output); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code
}

func jsonErrorRequest(t *testing.T, handler http.Handler, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

func testSaveSetHash(logicalPath, content string) string {
	fileDigest := sha256.Sum256([]byte(content))
	setDigest := sha256.New()
	setDigest.Write([]byte(logicalPath))
	setDigest.Write([]byte{0})
	setDigest.Write([]byte(hex.EncodeToString(fileDigest[:])))
	setDigest.Write([]byte{0})
	setDigest.Write([]byte(fmt.Sprint(len(content))))
	setDigest.Write([]byte{0})
	return hex.EncodeToString(setDigest.Sum(nil))
}

func TestLibraryMaintenanceAPI(t *testing.T) {
	_, handler, _ := testServer(t)
	var presets []map[string]any
	jsonRequest(t, handler, http.MethodGet, "/api/platforms", nil, &presets)
	if len(presets) != 72 {
		t.Fatalf("platform presets missing: %d", len(presets))
	}
	var ngpcSuggestions map[string]any
	for _, preset := range presets {
		if preset["id"] == "ngpc" {
			ngpcSuggestions, _ = preset["suggested_emulators"].(map[string]any)
			break
		}
	}
	if windows, ok := ngpcSuggestions["windows"].([]any); !ok || len(windows) != 1 || windows[0] != "RetroArch · Beetle NeoPop" {
		t.Fatalf("NGPC platform suggestions missing from API: %#v", ngpcSuggestions)
	}
	var switchSuggestions map[string]any
	for _, preset := range presets {
		if preset["id"] == "switch" {
			switchSuggestions, _ = preset["suggested_emulators"].(map[string]any)
			break
		}
	}
	if windows, ok := switchSuggestions["windows"].([]any); !ok || len(windows) != 1 || windows[0] != "Eden" {
		t.Fatalf("Switch Windows suggestion missing from API: %#v", switchSuggestions)
	}
	if android, ok := switchSuggestions["android"].([]any); !ok || len(android) != 0 {
		t.Fatalf("Switch Android suggestion must remain empty: %#v", switchSuggestions)
	}
	var alias map[string]any
	jsonRequest(t, handler, http.MethodGet, "/api/platforms/ps1", nil, &alias)
	if alias["id"] != "psx" {
		t.Fatalf("platform alias was not resolved: %#v", alias)
	}
	var first, second catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/games", catalog.NewGame{DefaultTitle: "Game", Platform: "ps1", Titles: map[string]string{"zh-CN": "游戏"}}, &first)
	if first.Platform != "psx" {
		t.Fatalf("game platform was not canonicalized: %#v", first)
	}
	jsonRequest(t, handler, http.MethodPost, "/api/games", catalog.NewGame{DefaultTitle: "Translation", Platform: "psx"}, &second)
	var created catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/editions", editionRequest{NewEdition: catalog.NewEdition{GameID: second.ID, DefaultTitle: "Chinese", EditionType: "translation", Languages: []string{"zh-CN"}}}, &created)
	if len(created.Editions) != 1 {
		t.Fatalf("edition not created: %#v", created)
	}
	editionID := created.Editions[0].ID
	var moved catalog.Edition
	jsonRequest(t, handler, http.MethodPost, "/api/editions/"+editionID+"/move", catalog.MoveEdition{TargetGameID: first.ID}, &moved)
	if moved.GameID != first.ID {
		t.Fatalf("edition not moved: %#v", moved)
	}
	var merged catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/games/"+first.ID+"/merge", catalog.MergeGames{SourceGameID: second.ID}, &merged)
	if len(merged.Editions) != 1 {
		t.Fatalf("merge lost edition: %#v", merged)
	}
	jsonRequest(t, handler, http.MethodPut, "/api/games/"+first.ID+"/primary", map[string]string{"edition_id": editionID}, &merged)
	if merged.PrimaryEditionID != editionID {
		t.Fatalf("primary edition not set: %#v", merged)
	}
}

func TestSaveSetupRejectsMissingEditionIdentityBeforeAnyWrite(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Switch fixture", Platform: "switch"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	input := catalog.NewSaveSetup{
		Stream:  catalog.NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: "builtin-driver-eden", Portability: "driver-dependent", EditionIDs: []string{edition.ID}},
		Binding: catalog.NewSaveBinding{EditionID: edition.ID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-eden", LocalPaths: []string{"{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}"}},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/save-bindings/setup", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	var failure apiErrorEnvelope
	if recorder.Code != http.StatusBadRequest || json.Unmarshal(recorder.Body.Bytes(), &failure) != nil || failure.Error.Code != "save_binding_identity_required" || !strings.Contains(failure.Error.Message, "16-hex edition.title_id") {
		t.Fatalf("missing identity response = %d %s", recorder.Code, recorder.Body.String())
	}
	streams, _ := store.ListSaveStreams(ctx, edition.ID)
	bindings, _ := store.ListSaveBindings(ctx, edition.ID, "")
	if len(streams) != 0 || len(bindings) != 0 {
		t.Fatalf("rejected setup left partial rows: streams=%d bindings=%d", len(streams), len(bindings))
	}

	edition, err = store.UpdateEdition(ctx, edition.ID, catalog.NewEdition{GameID: game.ID, DefaultTitle: edition.DefaultTitle, EditionType: edition.EditionType, TitleID: "0100A5B00CBD5000"})
	if err != nil {
		t.Fatal(err)
	}
	var created catalog.SaveSetup
	if code := jsonRequest(t, handler, http.MethodPost, "/api/v1/save-bindings/setup", input, &created); code != http.StatusCreated || created.Stream.ID == "" || created.Binding.StreamID != created.Stream.ID {
		t.Fatalf("valid identity setup = %d %#v", code, created)
	}
}

func TestSignedGameMergePreviewRejectsTamperingAndDrift(t *testing.T) {
	store, handler, _ := testServer(t)
	var target, source catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Target", Platform: "gba"}, &target)
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Source", Platform: "gba"}, &source)
	invalid := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/games/"+target.ID+"/merge/preview", map[string]string{})
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_argument") {
		t.Fatalf("invalid merge preview = %d %s", invalid.Code, invalid.Body.String())
	}
	var targetWithEdition, sourceWithEdition catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/editions", editionRequest{NewEdition: catalog.NewEdition{GameID: target.ID, DefaultTitle: "Original", EditionType: "original"}}, &targetWithEdition)
	jsonRequest(t, handler, http.MethodPost, "/api/v1/editions", editionRequest{NewEdition: catalog.NewEdition{GameID: source.ID, DefaultTitle: "Translation", EditionType: "translation"}}, &sourceWithEdition)
	oldSourceEdition := sourceWithEdition.Editions[0]

	var preview gameMergePreviewResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games/"+target.ID+"/merge/preview?locale=zh-CN", catalog.MergeGames{SourceGameID: source.ID}, &preview)
	if preview.PreviewToken == "" || preview.SnapshotFingerprint == "" || preview.TargetEditions != 1 || preview.SourceEditions != 1 || preview.ResultEditions != 2 || preview.FailurePolicy != "atomic" || preview.ROMFilesMoved || preview.SaveNamespacesChanged || !preview.SourceGameMetadataRemoved {
		t.Fatalf("unexpected signed preview: %#v", preview)
	}
	if strings.Contains(preview.PreviewToken, target.ID) || strings.Contains(preview.PreviewToken, source.ID) {
		t.Fatal("opaque merge token exposed catalog identity")
	}
	tampered := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/games/"+target.ID+"/merge", catalog.MergeGames{SourceGameID: source.ID, PreviewToken: preview.PreviewToken + "tampered", SnapshotFingerprint: preview.SnapshotFingerprint})
	if tampered.Code != http.StatusConflict || !strings.Contains(tampered.Body.String(), "game_merge_stale") {
		t.Fatalf("tampered merge = %d %s", tampered.Code, tampered.Body.String())
	}
	if got, err := store.GetGame(context.Background(), source.ID, ""); err != nil || len(got.Editions) != 1 {
		t.Fatalf("tampered merge wrote catalog state: %#v, %v", got, err)
	}

	driftEdition, err := store.AddEdition(context.Background(), catalog.NewEdition{GameID: source.ID, DefaultTitle: "Hack", EditionType: "hack"})
	if err != nil {
		t.Fatal(err)
	}
	stale := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/games/"+target.ID+"/merge", catalog.MergeGames{SourceGameID: source.ID, PreviewToken: preview.PreviewToken, SnapshotFingerprint: preview.SnapshotFingerprint})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "game_merge_stale") {
		t.Fatalf("stale merge = %d %s", stale.Code, stale.Body.String())
	}
	if got, err := store.GetGame(context.Background(), target.ID, ""); err != nil || len(got.Editions) != 1 {
		t.Fatalf("stale merge changed target: %#v, %v", got, err)
	}

	jsonRequest(t, handler, http.MethodPost, "/api/v1/games/"+target.ID+"/merge/preview", catalog.MergeGames{SourceGameID: source.ID}, &preview)
	var merged catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games/"+target.ID+"/merge", catalog.MergeGames{SourceGameID: source.ID, PreviewToken: preview.PreviewToken, SnapshotFingerprint: preview.SnapshotFingerprint}, &merged)
	if len(merged.Editions) != 3 {
		t.Fatalf("signed merge lost editions: %#v", merged)
	}
	byID := map[string]catalog.Edition{}
	for _, edition := range merged.Editions {
		byID[edition.ID] = edition
	}
	if byID[oldSourceEdition.ID].SaveNamespace != oldSourceEdition.SaveNamespace || byID[driftEdition.ID].SaveNamespace != driftEdition.SaveNamespace {
		t.Fatal("signed merge changed edition identity or save namespace")
	}
	if _, err = store.GetGame(context.Background(), source.ID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("source survived signed merge: %v", err)
	}
}

func TestCustomPlatformAPIIsMergedAndCanonicalizesAliases(t *testing.T) {
	_, handler, _ := testServer(t)
	enabled := true
	input := catalog.NewCustomPlatform{ID: "fixture-handheld", Name: "Fixture Handheld", NameZH: "测试掌机", Category: "handheld", Aliases: []string{"fixture-hh"}, Extensions: []string{".opk"}, ESDESystems: []string{"fixture-handheld-es"}, Enabled: &enabled}
	var created catalog.CustomPlatform
	if code := jsonRequest(t, handler, http.MethodPost, "/api/v1/custom-platforms", input, &created); code != http.StatusCreated || created.ID != "fixture-handheld" || created.Builtin {
		t.Fatalf("created=%#v code=%d", created, code)
	}
	var resolved platforms.Platform
	jsonRequest(t, handler, http.MethodGet, "/api/v1/platforms/fixture-handheld-es", nil, &resolved)
	if resolved.ID != "fixture-handheld" || resolved.Builtin || len(resolved.Extensions) != 1 {
		t.Fatalf("resolved=%#v", resolved)
	}
	conflict := input
	conflict.ID, conflict.Name, conflict.Aliases = "other-hand", "Other Hand", []string{"gba"}
	conflictResponse := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/custom-platforms", conflict)
	if conflictResponse.Code != http.StatusConflict || !strings.Contains(conflictResponse.Body.String(), "platform_key_conflict") {
		t.Fatalf("platform collision: %d %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	var game catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Custom", Platform: "fixture-hh"}, &game)
	if game.Platform != "fixture-handheld" {
		t.Fatalf("custom alias was not canonicalized: %#v", game)
	}
	disabled := false
	input.Enabled = &disabled
	var updated catalog.CustomPlatform
	jsonRequest(t, handler, http.MethodPut, "/api/v1/custom-platforms/fixture-handheld", input, &updated)
	if updated.Enabled {
		t.Fatalf("updated=%#v", updated)
	}
	var active []platforms.Platform
	jsonRequest(t, handler, http.MethodGet, "/api/platforms", nil, &active)
	for _, item := range active {
		if item.ID == "fixture-handheld" {
			t.Fatal("disabled custom platform remained in active platform collection")
		}
	}
	response := jsonErrorRequest(t, handler, http.MethodDelete, "/api/v1/custom-platforms/fixture-handheld", nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "custom_platform_in_use") {
		t.Fatalf("delete referenced custom platform: %d %s", response.Code, response.Body.String())
	}
}

func TestRuntimeCatalogSeedingIsIdempotentAndPreservesCustomEntries(t *testing.T) {
	store, _, root := testServer(t)
	ctx := context.Background()
	enabled := true
	custom, err := store.CreateFrontendAdapter(ctx, catalog.NewFrontendAdapter{
		ID: "custom-frontend", Name: "Custom frontend", Format: "custom", ContractVersion: 1,
		Capabilities: map[string]bool{"export": true}, Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	customDevice, err := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{
		ID: "custom-device", Name: "Custom device", ContractVersion: 7, Target: "custom-target", OSFamily: "custom-os", Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	customCore, err := store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{
		ID: "custom-core", Name: "Custom core", ContractVersion: 7, LibraryNames: []string{"custom_libretro"}, Platforms: []string{"gba"}, Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeAdapters, _ := store.ListFrontendAdapters(ctx)
	beforeDevices, _ := store.ListDeviceProfiles(ctx)
	beforeDrivers, _ := store.ListEmulatorDrivers(ctx)
	beforeCores, _ := store.ListRetroArchCores(ctx)
	beforeMappings, _ := store.ListCoreMappings(ctx, "")

	if _, err = New(store, root); err != nil {
		t.Fatal(err)
	}
	if _, err = New(store, root); err != nil {
		t.Fatal(err)
	}
	afterAdapters, _ := store.ListFrontendAdapters(ctx)
	afterDevices, _ := store.ListDeviceProfiles(ctx)
	afterDrivers, _ := store.ListEmulatorDrivers(ctx)
	afterCores, _ := store.ListRetroArchCores(ctx)
	afterMappings, _ := store.ListCoreMappings(ctx, "")
	if len(afterAdapters) != len(beforeAdapters) || len(afterDevices) != len(beforeDevices) || len(afterDrivers) != len(beforeDrivers) || len(afterCores) != len(beforeCores) || len(afterMappings) != len(beforeMappings) {
		t.Fatalf("runtime seed duplicated rows: before=%d/%d/%d/%d/%d after=%d/%d/%d/%d/%d", len(beforeAdapters), len(beforeDevices), len(beforeDrivers), len(beforeCores), len(beforeMappings), len(afterAdapters), len(afterDevices), len(afterDrivers), len(afterCores), len(afterMappings))
	}
	found := false
	for _, adapter := range afterAdapters {
		if adapter.ID == custom.ID && adapter.Name == custom.Name && !adapter.Builtin {
			found = true
		}
	}
	if !found {
		t.Fatal("runtime seed removed or rewrote a custom frontend adapter")
	}
	gotCustomDevice, err := store.GetDeviceProfile(ctx, customDevice.ID)
	if err != nil || gotCustomDevice.ContractVersion != 7 || gotCustomDevice.Builtin {
		t.Fatalf("runtime seed rewrote a custom device profile: %#v, %v", gotCustomDevice, err)
	}
	gotCustomCore, err := store.GetRetroArchCore(ctx, customCore.ID)
	if err != nil || gotCustomCore.ContractVersion != 7 || gotCustomCore.Builtin {
		t.Fatalf("runtime seed rewrote a custom core: %#v, %v", gotCustomCore, err)
	}
	builtinDevice, err := store.GetDeviceProfile(ctx, "builtin-device-rocknix")
	if err != nil || builtinDevice.ContractVersion < 2 {
		t.Fatalf("built-in device contract was not reconciled: %#v, %v", builtinDevice, err)
	}
	for _, id := range []string{"builtin-device-windows-handheld", "builtin-device-steamos-bazzite", "builtin-device-android-handheld", "builtin-device-rocknix", "builtin-device-darkos", "builtin-device-arkos", "builtin-device-knulli", "builtin-device-muos", "builtin-device-onionos", "builtin-device-portable"} {
		profile, profileErr := store.GetDeviceProfile(ctx, id)
		expectedScope := "fixture"
		if id == "builtin-device-android-handheld" {
			expectedScope = "android-emulator"
		}
		if profileErr != nil || profile.ContractVersion < 3 || profile.SupportLevel != "package-tested" || profile.Evidence["scope"] != expectedScope {
			t.Fatalf("package-tested handheld contract was not reconciled for %s: %#v, %v", id, profile, profileErr)
		}
	}
	androidProfile, androidErr := store.GetDeviceProfile(ctx, "builtin-device-android-handheld")
	androidNote, androidNoteOK := androidProfile.Evidence["note"].(string)
	if androidErr != nil || androidProfile.ContractVersion != 4 || androidProfile.Evidence["verified_at"] != "2026-08-30" || !androidNoteOK || !strings.Contains(androidNote, "Azahar Google Play") || !strings.Contains(androidNote, "not Android handheld hardware") {
		t.Fatalf("Android emulator evidence was not reconciled: %#v, %v", androidProfile, androidErr)
	}
	for id, marker := range map[string]string{
		"builtin-device-knulli":  "gameStop hook completed downloads",
		"builtin-device-muos":    "sync/start/stop entries completed downloads",
		"builtin-device-onionos": "actual armv7l user-mode process",
	} {
		profile, profileErr := store.GetDeviceProfile(ctx, id)
		note, noteOK := profile.Evidence["note"].(string)
		if profileErr != nil || profile.ContractVersion != 4 || profile.Evidence["verified_at"] != "2026-08-29" || !noteOK || !strings.Contains(note, marker) || !strings.Contains(note, "Hardware behavior remains unverified") {
			t.Fatalf("software-process fixture evidence was not reconciled for %s: %#v, %v", id, profile, profileErr)
		}
	}
	windowsProfile, windowsErr := store.GetDeviceProfile(ctx, "builtin-device-windows-handheld")
	windowsNote, windowsNoteOK := windowsProfile.Evidence["note"].(string)
	if windowsErr != nil || windowsProfile.ContractVersion != 4 || windowsProfile.Evidence["verified_at"] != "2026-08-30" || !windowsNoteOK || !strings.Contains(windowsNote, "exact parsed Task XML argv") || !strings.Contains(windowsNote, "Windows hardware") {
		t.Fatalf("Windows software-process fixture evidence was not reconciled: %#v, %v", windowsProfile, windowsErr)
	}
	for id, expected := range map[string][2]string{
		"builtin-steamos-bazzite-esde-zh": {"steamos-bazzite", "builtin-device-steamos-bazzite"},
		"builtin-darkos-esde-zh":          {"darkos", "builtin-device-darkos"},
		"builtin-arkos-esde-zh":           {"arkos", "builtin-device-arkos"},
		"builtin-knulli-esde-zh":          {"knulli", "builtin-device-knulli"},
	} {
		profile, profileErr := store.GetPackageProfile(ctx, id)
		if profileErr != nil || profile.Target != expected[0] || profile.DeviceProfileID != expected[1] || profile.Frontend != "es-de" || profile.FrontendAdapterID != esdeAdapterID || !profile.Enabled {
			t.Fatalf("handheld package preset %s=%#v err=%v", id, profile, profileErr)
		}
		if _, profileErr = bundler.ValidateProfile(packageProfileToBundler(profile)); profileErr != nil {
			t.Fatalf("handheld package preset %s is invalid: %v", id, profileErr)
		}
	}
	for _, profile := range defaultPackageProfiles() {
		if profile.Target == "muos" || profile.Target == "onionos" {
			t.Fatalf("native frontend support was claimed without an adapter: %#v", profile)
		}
	}
	builtinCore, err := store.GetRetroArchCore(ctx, "builtin-core-mgba")
	if err != nil || builtinCore.ContractVersion < 2 {
		t.Fatalf("built-in core contract was not reconciled: %#v, %v", builtinCore, err)
	}
	pokeminiCore, err := store.GetRetroArchCore(ctx, "builtin-core-pokemini")
	if err != nil || !slices.Contains(pokeminiCore.Platforms, "pokemini") || !slices.Contains(pokeminiCore.LibraryNames, "pokemini_libretro") {
		t.Fatalf("Pokémon Mini core catalog entry was not reconciled: %#v, %v", pokeminiCore, err)
	}
	pokeminiResolution, err := store.ResolveCore(ctx, "pokemini", "", "")
	if err != nil || pokeminiResolution.Core.ID != pokeminiCore.ID || pokeminiResolution.Resolution != "global" {
		t.Fatalf("Pokémon Mini core default was not reconciled: %#v, %v", pokeminiResolution, err)
	}
	virtualBoyCore, err := store.GetRetroArchCore(ctx, "builtin-core-beetle-vb")
	if err != nil || !slices.Contains(virtualBoyCore.Platforms, "virtualboy") || !slices.Contains(virtualBoyCore.LibraryNames, "mednafen_vb_libretro") {
		t.Fatalf("Virtual Boy core catalog entry was not reconciled: %#v, %v", virtualBoyCore, err)
	}
	virtualBoyResolution, err := store.ResolveCore(ctx, "virtualboy", "", "")
	if err != nil || virtualBoyResolution.Core.ID != virtualBoyCore.ID || virtualBoyResolution.Resolution != "global" {
		t.Fatalf("Virtual Boy core default was not reconciled: %#v, %v", virtualBoyResolution, err)
	}
	gameWatchCore, err := store.GetRetroArchCore(ctx, "builtin-core-gw")
	if err != nil || !slices.Contains(gameWatchCore.Platforms, "gameandwatch") || !slices.Contains(gameWatchCore.LibraryNames, "gw_libretro") || gameWatchCore.Evidence["save_support"] != false || gameWatchCore.Evidence["state_support"] != false {
		t.Fatalf("Game & Watch GW core catalog entry was not reconciled: %#v, %v", gameWatchCore, err)
	}
	gameWatchResolution, err := store.ResolveCore(ctx, "gameandwatch", "", "")
	if err != nil || gameWatchResolution.Core.ID != gameWatchCore.ID || gameWatchResolution.Resolution != "global" {
		t.Fatalf("Game & Watch GW core default was not reconciled: %#v, %v", gameWatchResolution, err)
	}
	retroarch, err := store.GetEmulatorDriver(ctx, "builtin-driver-retroarch")
	verifiedCombinations, combinationsOK := retroarch.Evidence["verified_save_combinations"].([]any)
	gameWatchSavePatterns, hasGameWatchSaveOverride := retroarch.Save.PatternsByPlatform["gameandwatch"]
	neoPopSavePatterns := retroarch.Save.PatternsByPlatform["ngpc"]
	if err != nil || retroarch.ContractVersion < 10 || !slices.Contains(retroarch.Platforms, "pokemini") || !slices.Contains(retroarch.Platforms, "virtualboy") || !slices.Contains(retroarch.Platforms, "gameandwatch") || !slices.Contains(retroarch.Platforms, "sega32x") || slices.Contains(retroarch.Platforms, "32x") || !slices.Contains(retroarch.Targets, "darkos") || len(retroarch.Launch.Executables["darkos"]) == 0 || retroarch.Launch.AndroidIntent == nil || retroarch.Launch.AndroidIntent.Package != "com.retroarch.aarch64" || len(retroarch.Save.Patterns) != 1 || retroarch.Save.Patterns[0] != "{{rom.stem}}.srm" || !hasGameWatchSaveOverride || len(gameWatchSavePatterns) != 0 || len(neoPopSavePatterns) != 1 || neoPopSavePatterns[0] != "{{rom.stem}}.flash" || !combinationsOK || len(verifiedCombinations) != 1 {
		t.Fatalf("RetroArch Android intent contract was not reconciled: %#v, %v", retroarch, err)
	}
	mame, mameErr := store.GetEmulatorDriver(ctx, "builtin-driver-mame")
	if mameErr != nil || mame.ContractVersion < 7 || !slices.Contains(mame.Platforms, "gameandwatch") || slices.Contains(mame.Platforms, "mame") || !slices.Contains(mame.Targets, "darkos") || len(mame.Launch.Executables["darkos"]) == 0 {
		t.Fatalf("MAME Game & Watch contract was not reconciled: %#v, %v", mame, mameErr)
	}
	fbneo, fbneoErr := store.GetEmulatorDriver(ctx, "builtin-driver-fbneo")
	if fbneoErr != nil || fbneo.ContractVersion < 6 || !slices.Contains(fbneo.Platforms, "neogeo") || slices.Contains(fbneo.Platforms, "fbneo") || !slices.Contains(fbneo.Targets, "darkos") || len(fbneo.Launch.Executables["darkos"]) == 0 {
		t.Fatalf("FinalBurn Neo dArkOS contract was not reconciled: %#v, %v", fbneo, fbneoErr)
	}
	dolphin, dolphinErr := store.GetEmulatorDriver(ctx, "builtin-driver-dolphin")
	if dolphinErr != nil || dolphin.ContractVersion < 5 || !slices.Contains(dolphin.Platforms, "wii") || slices.Contains(dolphin.Platforms, "wiiware") || slices.Contains(dolphin.Targets, "android") || dolphin.Launch.AndroidIntent != nil {
		t.Fatalf("Dolphin canonical Wii contract was not reconciled: %#v, %v", dolphin, dolphinErr)
	}
	azahar, azaharErr := store.GetEmulatorDriver(ctx, "builtin-driver-azahar")
	if azaharErr != nil || azahar.ContractVersion < 5 || !slices.Contains(azahar.Targets, "android") || azahar.Launch.AndroidIntent == nil || azahar.Launch.AndroidIntent.Package != "org.azahar_emu.azahar" || !slices.Equal(azahar.Launch.AndroidIntent.PackageCandidates, []string{"io.github.lime3ds.android"}) || azahar.Launch.AndroidIntent.Activity != "org.citra.citra_emu.activities.EmulationActivity" || azahar.Launch.AndroidIntent.Data != "{{rom.uri}}" {
		t.Fatalf("Azahar Android intent contract was not reconciled: %#v, %v", azahar, azaharErr)
	}
	ppsspp, err := store.GetEmulatorDriver(ctx, "builtin-driver-ppsspp")
	if err != nil || ppsspp.ContractVersion < 4 || ppsspp.Launch.AndroidIntent == nil || ppsspp.Launch.AndroidIntent.Package != "org.ppsspp.ppsspp" {
		t.Fatalf("PPSSPP Android intent contract was not reconciled: %#v, %v", ppsspp, err)
	}
	eden, err := store.GetEmulatorDriver(ctx, "builtin-driver-eden")
	sources, sourcesOK := eden.Evidence["sources"].([]any)
	if err != nil || eden.ContractVersion != 5 || eden.Family != "eden" || !slices.Equal(eden.Platforms, []string{"switch"}) || !slices.Equal(eden.Targets, []string{"windows", "steamos-bazzite"}) || eden.Launch.AndroidIntent != nil || !slices.Equal(eden.Launch.Arguments, []string{"-g", "{{rom.path}}"}) || !slices.Equal(eden.Launch.Executables["windows"], []string{"eden.exe", "eden-cli.exe"}) || !slices.Equal(eden.Launch.Executables["steamos-bazzite"], []string{"eden", "eden-cli"}) || eden.Save.Scope != "game" || eden.Save.Layout != "directory" || !slices.Equal(eden.Save.Patterns, []string{"{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}"}) || eden.Save.Refresh != "process-exit" || eden.Save.Portability != "same-driver" || len(eden.ConfigPaths) != 0 || eden.SupportLevel != "catalogued" || !sourcesOK || len(sources) != 4 {
		t.Fatalf("Eden Switch contract was not reconciled: %#v, %v", eden, err)
	}
	for _, id := range []string{"builtin-driver-emulatorjs-fceumm", "builtin-driver-emulatorjs-gambatte", "builtin-driver-emulatorjs-genesis-plus-gx", "builtin-driver-emulatorjs-mgba", "builtin-driver-emulatorjs-mupen64plus-next", "builtin-driver-emulatorjs-smsplus", "builtin-driver-emulatorjs-snes9x", "builtin-driver-emulatorjs-stella2014"} {
		driver, driverErr := store.GetEmulatorDriver(ctx, id)
		scope := driver.Evidence["scope"]
		if driverErr != nil || driver.ContractVersion < 2 || driver.SupportLevel != "package-tested" || (scope != "real-browser" && scope != "cross-runtime") || driver.Evidence["result"] != "passed" {
			t.Fatalf("real-browser EmulatorJS evidence was not reconciled for %s: %#v, %v", id, driver, driverErr)
		}
	}
	genesisWeb, err := store.GetEmulatorDriver(ctx, "builtin-driver-emulatorjs-genesis-plus-gx")
	if err != nil || genesisWeb.ContractVersion < 4 {
		t.Fatalf("Genesis Plus GX browser interaction/save evidence missing: %#v %v", genesisWeb, err)
	}
	smsWeb, err := store.GetEmulatorDriver(ctx, "builtin-driver-emulatorjs-smsplus")
	if err != nil || smsWeb.ContractVersion < 3 {
		t.Fatalf("SMS Plus GX browser interaction/save evidence missing: %#v %v", smsWeb, err)
	}
	snesWeb, err := store.GetEmulatorDriver(ctx, "builtin-driver-emulatorjs-snes9x")
	if err != nil || snesWeb.ContractVersion < 5 || snesWeb.Evidence["observed_rom_result"] != "gilyon spctest reached Success at terminal test 0557 under EmulatorJS 4.2.3 Snes9x" || snesWeb.Evidence["observed_save_result"] != "2 KiB SRAM Web 0x5A -> RetroArch 0xA5 -> Web byte-exact roundtrip passed" {
		t.Fatalf("Snes9x terminal/save evidence missing: %#v %v", snesWeb, err)
	}
	snesCore, err := store.GetRetroArchCore(ctx, "builtin-core-snes9x")
	if err != nil || snesCore.ContractVersion < 3 || snesCore.SupportLevel != "package-tested" || snesCore.Evidence["commit"] != "6ca2343e5f3b0acbea49ca958251e3a0af58a81d" {
		t.Fatalf("exact Snes9x native compatibility evidence missing: %#v %v", snesCore, err)
	}
}

func TestBuiltinRuntimeCatalogCoversCanonicalPlatforms(t *testing.T) {
	registered := map[string]bool{}
	for _, platform := range platforms.All() {
		registered[platform.ID] = true
	}
	covered := map[string]bool{}
	var retroarch catalog.NewEmulatorDriver
	for _, driver := range builtinEmulatorDrivers() {
		if driver.ID == "builtin-driver-retroarch" {
			retroarch = driver
		}
		for _, platformID := range driver.Platforms {
			if !registered[platformID] {
				t.Fatalf("driver %s declares non-canonical platform %q", driver.ID, platformID)
			}
			covered[platformID] = true
		}
	}
	if retroarch.ID == "" {
		t.Fatal("RetroArch driver is missing")
	}
	for platformID := range registered {
		if !covered[platformID] {
			t.Fatalf("registered platform %q has no emulator driver", platformID)
		}
	}
	cores := map[string]catalog.NewRetroArchCore{}
	for _, core := range builtinRetroArchCores() {
		cores[core.ID] = core
		for _, platformID := range core.Platforms {
			if !registered[platformID] {
				t.Fatalf("core %s declares non-canonical platform %q", core.ID, platformID)
			}
		}
	}
	mappings := map[string]catalog.NewCoreMapping{}
	for _, mapping := range builtinCoreMappings() {
		if !registered[mapping.PlatformID] {
			t.Fatalf("mapping %s declares non-canonical platform %q", mapping.ID, mapping.PlatformID)
		}
		core, ok := cores[mapping.CoreID]
		if !ok || !slices.Contains(core.Platforms, mapping.PlatformID) {
			t.Fatalf("mapping %s points to incompatible core %q for %q", mapping.ID, mapping.CoreID, mapping.PlatformID)
		}
		mappings[mapping.PlatformID] = mapping
	}
	for _, platformID := range retroarch.Platforms {
		if _, ok := mappings[platformID]; !ok {
			t.Fatalf("RetroArch platform %q has no global core mapping", platformID)
		}
	}
}

func TestRuntimeCatalogSuggestsExactVirtualBoyBeetleCoreWithoutTrustingCommand(t *testing.T) {
	store, _, _ := testServer(t)
	ctx := context.Background()
	raw := `"{env.appdir}\RetroArch\retroarch.exe" -L "{env.appdir}\RetroArch\cores\mednafen_vb_libretro.dll" "{file.path}"`
	if err := store.ImportGamesAtomic(ctx, []catalog.ImportedGame{{
		Platform: "virtualboy", DefaultTitle: "Virtual Boy hint", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []catalog.NewArtifact{{Path: "virtualboy/hint.vb", SHA256: "virtualboy-hint"}},
		RuntimeHints: []catalog.NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: raw}},
	}}); err != nil {
		t.Fatal(err)
	}
	hints, err := store.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 1 {
		t.Fatalf("hints=%#v err=%v", hints, err)
	}
	hint := hints[0]
	if hint.Trust != "untrusted" || hint.DriverID != "builtin-driver-retroarch" || hint.CoreID != "builtin-core-beetle-vb" || hint.RawCommand != raw || len(hint.Arguments) != 0 {
		t.Fatalf("unsafe or incomplete Virtual Boy suggestion: %#v", hint)
	}
}

func TestRuntimeCatalogSuggestsExactGameWatchGWCoreWithoutTrustingCommand(t *testing.T) {
	store, _, _ := testServer(t)
	ctx := context.Background()
	raw := `"{env.appdir}\RetroArch\retroarch.exe" -L "{env.appdir}\RetroArch\cores\gw_libretro.dll" "{file.path}"`
	if err := store.ImportGamesAtomic(ctx, []catalog.ImportedGame{{
		Platform: "gameandwatch", DefaultTitle: "Game & Watch hint", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []catalog.NewArtifact{{Path: "gameandwatch/hint.zip", SHA256: "game-watch-hint"}},
		RuntimeHints: []catalog.NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: raw}},
	}}); err != nil {
		t.Fatal(err)
	}
	hints, err := store.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 1 {
		t.Fatalf("hints=%#v err=%v", hints, err)
	}
	hint := hints[0]
	if hint.Trust != "untrusted" || hint.DriverID != "builtin-driver-retroarch" || hint.CoreID != "builtin-core-gw" || hint.RawCommand != raw || len(hint.Arguments) != 0 {
		t.Fatalf("unsafe or incomplete Game & Watch suggestion: %#v", hint)
	}
}

func TestRuntimeCatalogSuggestsCanonicalSega32XPicoDriveWithoutTrustingCommand(t *testing.T) {
	store, _, _ := testServer(t)
	ctx := context.Background()
	raw := `"{env.appdir}\RetroArch\retroarch.exe" -L "{env.appdir}\RetroArch\cores\picodrive_libretro.dll" "{file.path}"`
	if err := store.ImportGamesAtomic(ctx, []catalog.ImportedGame{{
		Platform: "sega32x", DefaultTitle: "Sega 32X hint", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []catalog.NewArtifact{{Path: "sega32x/hint.32x", SHA256: "sega32x-hint"}},
		RuntimeHints: []catalog.NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: raw}},
	}}); err != nil {
		t.Fatal(err)
	}
	hints, err := store.ListRuntimeImportHints(ctx, "", "pending")
	if err != nil || len(hints) != 1 {
		t.Fatalf("hints=%#v err=%v", hints, err)
	}
	hint := hints[0]
	if hint.Trust != "untrusted" || hint.DriverID != "builtin-driver-retroarch" || hint.CoreID != "builtin-core-picodrive" || hint.RawCommand != raw || len(hint.Arguments) != 0 {
		t.Fatalf("unsafe or incomplete Sega 32X suggestion: %#v", hint)
	}
}

func TestRuntimeCatalogReconcilesOnlyOlderBuiltinDeviceAndCoreContracts(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enabled := true
	if _, err = store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{ID: "builtin-device-rocknix", Name: "Old ROCKNIX", ContractVersion: 1, Target: "rocknix-old", OSFamily: "handheld-linux", Builtin: true, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "builtin-core-mgba", Name: "Old mGBA", ContractVersion: 1, LibraryNames: []string{"old_mgba_libretro"}, Platforms: []string{"gba"}, Builtin: true, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateRetroArchCore(ctx, catalog.NewRetroArchCore{ID: "builtin-core-snes9x", Name: "Old Snes9x", ContractVersion: 2, LibraryNames: []string{"snes9x_libretro"}, Platforms: []string{"snes"}, SupportLevel: "catalogued", Evidence: map[string]any{"note": "name-only entry"}, Builtin: true, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	var retroarchCurrent catalog.NewEmulatorDriver
	for _, driver := range builtinEmulatorDrivers() {
		if driver.ID == "builtin-driver-retroarch" {
			retroarchCurrent = driver
			break
		}
	}
	if retroarchCurrent.ID == "" {
		t.Fatal("RetroArch driver fixture is missing")
	}
	retroarchLegacy := retroarchCurrent
	retroarchLegacy.ContractVersion = 9
	retroarchLegacy.Platforms = slices.DeleteFunc(slices.Clone(retroarchCurrent.Platforms), func(platform string) bool { return platform == "sega32x" })
	if _, err = store.CreateEmulatorDriver(ctx, retroarchLegacy); err != nil {
		t.Fatal(err)
	}
	var snesCurrent catalog.NewEmulatorDriver
	for _, driver := range builtinEmulatorDrivers() {
		if driver.ID == "builtin-driver-emulatorjs-snes9x" {
			snesCurrent = driver
			break
		}
	}
	if snesCurrent.ID == "" {
		t.Fatal("Snes9x Web driver fixture is missing")
	}
	snesLegacy := snesCurrent
	snesLegacy.ContractVersion = 4
	snesLegacy.Evidence = map[string]any{"scope": "real-browser", "result": "passed", "observed_rom_result": "gilyon spctest reached Success at terminal test 0557 under EmulatorJS 4.2.3 Snes9x"}
	if _, err = store.CreateEmulatorDriver(ctx, snesLegacy); err != nil {
		t.Fatal(err)
	}
	if _, err = New(store, root); err != nil {
		t.Fatal(err)
	}
	device, err := store.GetDeviceProfile(ctx, "builtin-device-rocknix")
	if err != nil || device.ContractVersion != 3 || device.Target != "rocknix" || device.Name != "ROCKNIX handheld" || device.Evidence["scope"] != "fixture" {
		t.Fatalf("older built-in device was not upgraded: %#v, %v", device, err)
	}
	core, err := store.GetRetroArchCore(ctx, "builtin-core-mgba")
	if err != nil || core.ContractVersion != 2 || core.LibraryNames[0] != "mgba_libretro" || core.Name != "mGBA" {
		t.Fatalf("older built-in core was not upgraded: %#v, %v", core, err)
	}
	snesCore, err := store.GetRetroArchCore(ctx, "builtin-core-snes9x")
	if err != nil || snesCore.ContractVersion != 3 || snesCore.SupportLevel != "package-tested" || snesCore.Evidence["commit"] != "6ca2343e5f3b0acbea49ca958251e3a0af58a81d" {
		t.Fatalf("older Snes9x core evidence was not upgraded: %#v, %v", snesCore, err)
	}
	snes, err := store.GetEmulatorDriver(ctx, "builtin-driver-emulatorjs-snes9x")
	if err != nil || snes.ContractVersion != 5 || snes.Evidence["observed_rom_result"] != "gilyon spctest reached Success at terminal test 0557 under EmulatorJS 4.2.3 Snes9x" || snes.Evidence["observed_save_result"] != "2 KiB SRAM Web 0x5A -> RetroArch 0xA5 -> Web byte-exact roundtrip passed" {
		t.Fatalf("older Snes9x Web evidence was not upgraded: %#v, %v", snes, err)
	}
	retroarch, err := store.GetEmulatorDriver(ctx, "builtin-driver-retroarch")
	if err != nil || retroarch.ContractVersion != 10 || !slices.Contains(retroarch.Platforms, "virtualboy") || !slices.Contains(retroarch.Platforms, "gameandwatch") || !slices.Contains(retroarch.Platforms, "sega32x") {
		t.Fatalf("older RetroArch catalog omitted a classic handheld platform after upgrade: %#v, %v", retroarch, err)
	}
}

func TestRuntimeCatalogUpgradesV4BuiltinDriversForDarkOSWithoutRewritingCustomDrivers(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	selected := map[string]catalog.NewEmulatorDriver{}
	for _, driver := range builtinEmulatorDrivers() {
		if slices.Contains([]string{"builtin-driver-retroarch", "builtin-driver-mame", "builtin-driver-fbneo"}, driver.ID) {
			selected[driver.ID] = driver
		}
	}
	for id, current := range selected {
		legacy := current
		legacy.ContractVersion = 4
		legacy.Targets = slices.DeleteFunc(slices.Clone(current.Targets), func(target string) bool { return target == "darkos" })
		legacy.Launch.Executables = maps.Clone(current.Launch.Executables)
		delete(legacy.Launch.Executables, "darkos")
		if _, err = store.CreateEmulatorDriver(ctx, legacy); err != nil {
			t.Fatalf("create legacy %s: %v", id, err)
		}
	}
	custom := selected["builtin-driver-mame"]
	custom.ID, custom.Name, custom.Family, custom.ContractVersion, custom.Builtin = "custom-driver-mame", "Custom MAME", "custom-mame", 9, false
	custom.Targets = []string{"rocknix"}
	custom.Launch.Executables = map[string][]string{"rocknix": {"custom-mame"}}
	if _, err = store.CreateEmulatorDriver(ctx, custom); err != nil {
		t.Fatal(err)
	}

	if _, err = New(store, root); err != nil {
		t.Fatal(err)
	}
	for id := range selected {
		driver, getErr := store.GetEmulatorDriver(ctx, id)
		if getErr != nil || driver.ContractVersion != selected[id].ContractVersion || !slices.Contains(driver.Targets, "darkos") || len(driver.Launch.Executables["darkos"]) == 0 {
			t.Fatalf("legacy driver %s was not upgraded to its current contract: %#v, %v", id, driver, getErr)
		}
	}
	gotCustom, err := store.GetEmulatorDriver(ctx, custom.ID)
	if err != nil || gotCustom.ContractVersion != 9 || gotCustom.Name != "Custom MAME" || slices.Contains(gotCustom.Targets, "darkos") || gotCustom.Launch.Executables["rocknix"][0] != "custom-mame" {
		t.Fatalf("custom driver was rewritten: %#v, %v", gotCustom, err)
	}
}

func TestBuiltinEmulatorTargetsHaveConcreteLaunchContracts(t *testing.T) {
	for _, driver := range builtinEmulatorDrivers() {
		for _, target := range driver.Targets {
			if target == "web" && driver.Family == "emulatorjs" {
				continue
			}
			if target == "android" {
				if driver.Launch.AndroidIntent == nil || driver.Launch.AndroidIntent.Package == "" || driver.Launch.AndroidIntent.Activity == "" {
					t.Fatalf("%s advertises Android without an explicit Intent: %#v", driver.ID, driver.Launch)
				}
				continue
			}
			if len(driver.Launch.Executables[target]) == 0 {
				t.Fatalf("%s advertises %s without executable candidates", driver.ID, target)
			}
		}
	}
	var ppsspp, azahar catalog.NewEmulatorDriver
	for _, driver := range builtinEmulatorDrivers() {
		if driver.ID == "builtin-driver-ppsspp" {
			ppsspp = driver
		}
		if driver.ID == "builtin-driver-azahar" {
			azahar = driver
		}
	}
	if ppsspp.ID == "" || ppsspp.ContractVersion < 4 || ppsspp.Launch.AndroidIntent == nil || ppsspp.Launch.AndroidIntent.Package != "org.ppsspp.ppsspp" || ppsspp.Launch.AndroidIntent.Data != "{{rom.uri}}" {
		t.Fatalf("PPSSPP Android contract = %#v", ppsspp)
	}
	if azahar.ID == "" || azahar.ContractVersion < 5 || azahar.Launch.AndroidIntent == nil || !slices.Equal(azahar.Launch.AndroidIntent.PackageCandidates, []string{"io.github.lime3ds.android"}) || azahar.Launch.AndroidIntent.MIMEType != "application/octet-stream" {
		t.Fatalf("Azahar Android contract = %#v", azahar)
	}
}

func TestBuiltinDriverTargetMatrixResolvesAndExportsDevicePackages(t *testing.T) {
	store, handler, root := testServer(t)
	ctx := context.Background()
	drivers := builtinEmulatorDrivers()
	drivers = slices.DeleteFunc(drivers, func(driver catalog.NewEmulatorDriver) bool {
		return slices.Contains(driver.Targets, "web")
	})
	registeredDrivers := make(map[string]bool, len(drivers))
	for _, driver := range drivers {
		registeredDrivers[driver.ID] = true
	}
	for _, required := range []string{"builtin-driver-bigpemu", "builtin-driver-tsugaru", "builtin-driver-xemu", "builtin-driver-xenia"} {
		if !registeredDrivers[required] {
			t.Fatalf("required native driver %q is missing", required)
		}
	}
	deviceByTarget := map[string]string{
		"windows": "builtin-device-windows-handheld", "steamos-bazzite": "builtin-device-steamos-bazzite",
		"android": "builtin-device-android-handheld", "rocknix": "builtin-device-rocknix",
		"darkos": "builtin-device-darkos", "arkos": "builtin-device-arkos", "knulli": "builtin-device-knulli",
		"muos": "builtin-device-muos", "onionos": "builtin-device-onionos",
	}
	expectedPairCount := make(map[string]int, len(deviceByTarget))
	for _, driver := range drivers {
		for _, target := range driver.Targets {
			expectedPairCount[target]++
		}
	}
	expectedByTarget := make(map[string]map[string]bool, len(deviceByTarget))
	for target := range deviceByTarget {
		expectedByTarget[target] = map[string]bool{}
	}
	resolvedPairs := 0
	for index, declared := range drivers {
		driver, err := store.GetEmulatorDriver(ctx, declared.ID)
		if err != nil {
			t.Fatalf("get %s: %v", declared.ID, err)
		}
		if len(driver.Platforms) == 0 {
			t.Fatalf("%s has no supported platform", driver.ID)
		}
		platformID := driver.Platforms[0]
		romName := fmt.Sprintf("driver-%02d.rom", index)
		romPath := filepath.ToSlash(filepath.Join(platformID, romName))
		if err = os.MkdirAll(filepath.Join(root, platformID), 0o755); err != nil {
			t.Fatal(err)
		}
		content := []byte("fictional-driver-matrix:" + driver.ID)
		if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(romPath)), content, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: driver.Name + " fixture", Platform: platformID})
		if err != nil {
			t.Fatalf("create game for %s: %v", driver.ID, err)
		}
		edition, err := store.AddEdition(ctx, catalog.NewEdition{
			GameID: game.ID, DefaultTitle: "Original", EditionType: "original",
			ProductCode: "TEST-00001", TitleID: "0005000012345678", Serial: "TEST00001",
		})
		if err != nil {
			t.Fatalf("create edition for %s: %v", driver.ID, err)
		}
		if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: romPath, Role: "rom", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}); err != nil {
			t.Fatalf("create artifact for %s: %v", driver.ID, err)
		}
		for _, target := range driver.Targets {
			deviceID, ok := deviceByTarget[target]
			if !ok {
				t.Fatalf("%s advertises target %q without a built-in DeviceProfile", driver.ID, target)
			}
			device, err := store.GetDeviceProfile(ctx, deviceID)
			if err != nil || device.Target != target || device.DefaultFrontendID == "" {
				t.Fatalf("device target contract %s=%#v err=%v", target, device, err)
			}
			if _, err = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: deviceID, DriverID: driver.ID, FrontendAdapterID: device.DefaultFrontendID}); err != nil {
				t.Fatalf("bind %s/%s: %v", driver.ID, target, err)
			}
			resolved, err := runtimecfg.Resolve(ctx, store, edition.ID, deviceID)
			if err != nil {
				t.Fatalf("resolve %s/%s: %v", driver.ID, target, err)
			}
			if resolved.Driver.ID != driver.ID || resolved.DeviceProfile.ID != deviceID || resolved.ROMPath != romPath || len(resolved.Arguments) == 0 || len(resolved.Warnings) != 0 {
				t.Fatalf("incomplete resolution for %s/%s: %#v", driver.ID, target, resolved)
			}
			if strings.Contains(strings.Join(resolved.Arguments, "\n"), root) {
				t.Fatalf("resolution leaked host data for %s/%s: %#v", driver.ID, target, resolved)
			}
			if target == "android" {
				if resolved.AndroidPackage == "" || resolved.AndroidActivity == "" || len(resolved.ExecutableHints) != 0 {
					t.Fatalf("Android resolution is not explicit or leaked desktop hints for %s: %#v", driver.ID, resolved)
				}
			} else if len(resolved.ExecutableHints) == 0 || resolved.AndroidPackage != "" || resolved.AndroidActivity != "" {
				t.Fatalf("native resolution is incomplete or leaked Android fields for %s/%s: %#v", driver.ID, target, resolved)
			}
			expectedByTarget[target][driver.ID] = true
			resolvedPairs++
		}
	}

	declaredPairs := 0
	for _, driver := range drivers {
		declaredPairs += len(driver.Targets)
	}
	if resolvedPairs != declaredPairs {
		t.Fatalf("resolved/declared driver-target pairs=%d/%d", resolvedPairs, declaredPairs)
	}

	for target, deviceID := range deviceByTarget {
		expectedDrivers := expectedByTarget[target]
		if len(expectedDrivers) != expectedPairCount[target] {
			t.Fatalf("target %s has %d declared built-in drivers, want %d", target, len(expectedDrivers), expectedPairCount[target])
		}
		device, err := store.GetDeviceProfile(ctx, deviceID)
		if err != nil {
			t.Fatal(err)
		}
		adapter, err := store.GetFrontendAdapter(ctx, device.DefaultFrontendID)
		if err != nil || adapter.Handler == "" {
			t.Fatalf("target %s frontend=%#v err=%v", target, adapter, err)
		}
		enabled := true
		var profile catalog.PackageProfile
		jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles", catalog.NewPackageProfile{
			Name: "Built-in driver matrix " + target, Frontend: adapter.Handler, Target: target,
			DeviceProfileID: deviceID, FrontendAdapterID: adapter.ID,
			Locale: "en", FileMode: "copy", OutputSlug: "builtin-driver-matrix-" + target, Enabled: &enabled,
		}, &profile)
		var plan packagePlanResponse
		jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &plan)
		if len(plan.Plan.Conflicts) != 0 {
			t.Fatalf("driver matrix package %s conflicts: %#v", target, plan.Plan.Conflicts)
		}
		if wantWarnings := len(drivers) - len(expectedDrivers); len(plan.Plan.Warnings) != wantWarnings {
			t.Fatalf("driver matrix package %s warnings=%#v want=%d", target, plan.Plan.Warnings, wantWarnings)
		}
		var release packageReleaseResponse
		jsonRequest(t, handler, http.MethodPost, "/api/v1/package-plans/"+plan.ID+"/build", map[string]any{}, &release)
		manifestPath := filepath.Join(root, ".library-data", "exports", profile.OutputSlug, "varkiv-launches.json")
		manifestBytes, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(manifestBytes), root) {
			t.Fatalf("%s launch manifest exposed its host library root", target)
		}
		var manifest struct {
			FormatVersion  int                            `json:"format_version"`
			RuntimeCatalog catalog.PortableRuntimeCatalog `json:"runtime_catalog"`
			Bindings       []struct {
				ROMPath string `json:"rom_path"`
				Binding struct {
					DriverID string `json:"driver_id"`
				} `json:"binding"`
				ExecutableHints []string `json:"executable_hints"`
				AndroidPackage  string   `json:"android_package"`
				AndroidActivity string   `json:"android_activity"`
			} `json:"bindings"`
		}
		if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.FormatVersion != 2 || len(manifest.Bindings) != len(expectedDrivers) {
			t.Fatalf("%s launch manifest identity/count=%d/%d want=%d", target, manifest.FormatVersion, len(manifest.Bindings), len(expectedDrivers))
		}
		if len(manifest.RuntimeCatalog.FrontendAdapters) != 1 || len(manifest.RuntimeCatalog.DeviceProfiles) != 1 || len(manifest.RuntimeCatalog.EmulatorDrivers) != len(expectedDrivers) || len(manifest.RuntimeCatalog.RetroArchCores) != 1 || manifest.RuntimeCatalog.PackageProfile == nil {
			t.Fatalf("%s launch manifest omitted referenced runtime definitions: %#v", target, manifest.RuntimeCatalog)
		}
		if manifest.RuntimeCatalog.FrontendAdapters[0].ID != adapter.ID || manifest.RuntimeCatalog.DeviceProfiles[0].ID != deviceID {
			t.Fatalf("%s launch manifest runtime target identity drifted: %#v", target, manifest.RuntimeCatalog)
		}
		seen := make(map[string]bool, len(manifest.Bindings))
		for _, binding := range manifest.Bindings {
			if !expectedDrivers[binding.Binding.DriverID] || seen[binding.Binding.DriverID] {
				t.Fatalf("%s package has unexpected or duplicate driver %q", target, binding.Binding.DriverID)
			}
			if binding.ROMPath == "" {
				t.Fatalf("%s package has empty ROM path: %#v", target, binding)
			}
			if target == "android" {
				if binding.AndroidPackage == "" || binding.AndroidActivity == "" || len(binding.ExecutableHints) != 0 {
					t.Fatalf("Android package binding is incomplete: %#v", binding)
				}
			} else if len(binding.ExecutableHints) == 0 || binding.AndroidPackage != "" || binding.AndroidActivity != "" {
				t.Fatalf("%s package binding is incomplete or leaked Android fields: %#v", target, binding)
			}
			seen[binding.Binding.DriverID] = true
		}
		if !maps.Equal(seen, expectedDrivers) {
			t.Fatalf("%s exported driver set=%#v want=%#v", target, seen, expectedDrivers)
		}

		// Treat every built-in target package as a portable recovery artifact, not
		// merely a renderer output. A fresh catalog must require explicit review
		// before recreating any launch binding, resolve the same target-specific
		// contract afterwards, and reproduce the launch sidecar byte for byte.
		packageRoot := filepath.Dir(manifestPath)
		freshStore, err := catalog.Open(filepath.Join(t.TempDir(), target+"-reimport.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { freshStore.Close() })
		freshApp, err := New(freshStore, packageRoot)
		if err != nil {
			t.Fatal(err)
		}
		freshHandler := freshApp.Handler()
		request := importRequest{Format: "varkiv", Source: "library-manifest.json", ROMStorage: "reference", MediaStorage: "reference"}
		var preview struct {
			Parsed       int               `json:"parsed"`
			PreviewToken string            `json:"preview_token"`
			Candidates   []importCandidate `json:"candidates"`
		}
		jsonRequest(t, freshHandler, http.MethodPost, "/api/v1/imports/preview", request, &preview)
		if preview.Parsed != len(drivers) || len(preview.Candidates) != len(drivers) || preview.PreviewToken == "" {
			t.Fatalf("%s fresh import preview=%#v", target, preview)
		}
		request.PreviewToken = preview.PreviewToken
		for _, candidate := range preview.Candidates {
			if candidate.Status != "new" || candidate.Availability != "ready" || candidate.MissingArtifacts != 0 {
				t.Fatalf("%s fresh import candidate is not ready: %#v", target, candidate)
			}
			request.SelectedTokens = append(request.SelectedTokens, candidate.Token)
		}
		var imported map[string]any
		jsonRequest(t, freshHandler, http.MethodPost, "/api/v1/imports/commit", request, &imported)
		if imported["parsed"] != float64(len(drivers)) || imported["imported"] != float64(len(drivers)) || imported["skipped"] != float64(0) || imported["failure_policy"] != "atomic" {
			t.Fatalf("%s fresh import result=%#v", target, imported)
		}
		freshBindings, err := freshStore.ListLaunchBindings(ctx, "")
		if err != nil || len(freshBindings) != 0 {
			t.Fatalf("%s fresh import trusted launch bindings=%#v err=%v", target, freshBindings, err)
		}
		hints, err := freshStore.ListRuntimeImportHints(ctx, "", "pending")
		if err != nil || len(hints) != len(expectedDrivers) {
			t.Fatalf("%s fresh runtime hints=%#v err=%v", target, hints, err)
		}
		freshSeen := make(map[string]bool, len(hints))
		for _, hint := range hints {
			if !expectedDrivers[hint.DriverID] || freshSeen[hint.DriverID] || hint.SourceKind != "structured-sidecar" || hint.SourceFormat != "varkiv-launches-v2" || hint.Trust != "structured" || hint.RawCommand != "" {
				t.Fatalf("%s fresh runtime hint is incomplete or unsafe: %#v", target, hint)
			}
			binding, applyErr := freshStore.ApplyRuntimeImportHint(ctx, hint.ID, catalog.NewLaunchBinding{})
			if applyErr != nil || binding.DriverID != hint.DriverID || binding.DeviceProfileID != deviceID || binding.FrontendAdapterID != adapter.ID {
				t.Fatalf("%s apply fresh runtime hint=%#v err=%v", target, binding, applyErr)
			}
			resolved, resolveErr := runtimecfg.Resolve(ctx, freshStore, hint.EditionID, deviceID)
			if resolveErr != nil || resolved.Driver.ID != hint.DriverID || len(resolved.Arguments) == 0 || len(resolved.Warnings) != 0 {
				t.Fatalf("%s fresh runtime resolution=%#v err=%v", target, resolved, resolveErr)
			}
			if target == "android" {
				if resolved.AndroidPackage == "" || resolved.AndroidActivity == "" || len(resolved.ExecutableHints) != 0 {
					t.Fatalf("Android fresh runtime resolution is incomplete: %#v", resolved)
				}
			} else if len(resolved.ExecutableHints) == 0 || resolved.AndroidPackage != "" || resolved.AndroidActivity != "" {
				t.Fatalf("%s fresh runtime resolution leaked target fields: %#v", target, resolved)
			}
			freshSeen[hint.DriverID] = true
		}
		if !maps.Equal(freshSeen, expectedDrivers) {
			t.Fatalf("%s fresh reviewed driver set=%#v want=%#v", target, freshSeen, expectedDrivers)
		}
		importedProfile, err := freshStore.GetPackageProfile(ctx, profile.ID)
		if err != nil || importedProfile.OutputSlug != profile.OutputSlug || importedProfile.DeviceProfileID != deviceID || importedProfile.FrontendAdapterID != adapter.ID {
			t.Fatalf("%s portable package profile=%#v err=%v", target, importedProfile, err)
		}
		var freshPlan packagePlanResponse
		jsonRequest(t, freshHandler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &freshPlan)
		if len(freshPlan.Plan.Conflicts) != 0 || !slices.Equal(freshPlan.Plan.Warnings, plan.Plan.Warnings) {
			t.Fatalf("%s fresh package plan conflicts/warnings=%#v/%#v want warnings=%#v", target, freshPlan.Plan.Conflicts, freshPlan.Plan.Warnings, plan.Plan.Warnings)
		}
		var freshRelease packageReleaseResponse
		jsonRequest(t, freshHandler, http.MethodPost, "/api/v1/package-plans/"+freshPlan.ID+"/build", map[string]any{}, &freshRelease)
		freshManifestPath := filepath.Join(packageRoot, ".library-data", "exports", profile.OutputSlug, "varkiv-launches.json")
		freshManifestBytes, err := os.ReadFile(freshManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(freshManifestBytes, manifestBytes) {
			t.Fatalf("%s launch manifest changed after fresh-database review and re-export", target)
		}

		if target == "windows" {
			var drifted map[string]any
			if err = json.Unmarshal(manifestBytes, &drifted); err != nil {
				t.Fatal(err)
			}
			runtimeCatalog := drifted["runtime_catalog"].(map[string]any)
			driftedDrivers := runtimeCatalog["emulator_drivers"].([]any)
			driftedDriver := driftedDrivers[0].(map[string]any)
			driftedDriver["contract_version"] = driftedDriver["contract_version"].(float64) + 1
			driftedBytes, marshalErr := json.MarshalIndent(drifted, "", "  ")
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if err = os.WriteFile(manifestPath, append(driftedBytes, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			driftStore, openErr := catalog.Open(filepath.Join(t.TempDir(), "windows-drift-reimport.db"))
			if openErr != nil {
				t.Fatal(openErr)
			}
			t.Cleanup(func() { driftStore.Close() })
			driftApp, appErr := New(driftStore, packageRoot)
			if appErr != nil {
				t.Fatal(appErr)
			}
			driftResponse := jsonErrorRequest(t, driftApp.Handler(), http.MethodPost, "/api/v1/imports/preview", importRequest{Format: "varkiv", Source: "library-manifest.json", ROMStorage: "reference", MediaStorage: "reference"})
			if driftResponse.Code != http.StatusConflict || !strings.Contains(driftResponse.Body.String(), "runtime_definition_conflict") {
				t.Fatalf("drifted built-in runtime contract preview=%d %s", driftResponse.Code, driftResponse.Body.String())
			}
			driftGames, listErr := driftStore.ListGames(ctx, "")
			if listErr != nil || len(driftGames) != 0 {
				t.Fatalf("drifted built-in runtime contract wrote games=%#v err=%v", driftGames, listErr)
			}
		}
	}
}

func TestRuntimeCatalogLaunchResolutionAndPackageExportAPI(t *testing.T) {
	store, handler, root := testServer(t)
	type sourceAdapterCollection struct {
		Data []catalog.SourceAdapter `json:"data"`
	}
	var sourceAdapters sourceAdapterCollection
	jsonRequest(t, handler, http.MethodGet, "/api/v1/source-adapters", nil, &sourceAdapters)
	if len(sourceAdapters.Data) != 4 {
		t.Fatalf("source adapters = %d", len(sourceAdapters.Data))
	}
	var customSourceAdapter catalog.SourceAdapter
	jsonRequest(t, handler, http.MethodPost, "/api/v1/source-adapters", catalog.NewSourceAdapter{Name: "Reviewed Pegasus", Format: "reviewed-pegasus", Handler: "pegasus", Capabilities: map[string]bool{"metadata": true}}, &customSourceAdapter)
	if customSourceAdapter.Builtin || customSourceAdapter.Handler != "pegasus" {
		t.Fatalf("custom source adapter = %#v", customSourceAdapter)
	}
	type adapterCollection struct {
		Data []catalog.FrontendAdapter `json:"data"`
	}
	var adapters adapterCollection
	jsonRequest(t, handler, http.MethodGet, "/api/v1/frontend-adapters", nil, &adapters)
	if len(adapters.Data) != 2 {
		t.Fatalf("frontend adapters = %d", len(adapters.Data))
	}
	for _, adapter := range adapters.Data {
		if adapter.Handler == "" || adapter.ContractVersion < 5 {
			t.Fatalf("built-in frontend lacks an audited handler contract: %#v", adapter)
		}
	}
	var customFrontend catalog.FrontendAdapter
	jsonRequest(t, handler, http.MethodPost, "/api/v1/frontend-adapters", catalog.NewFrontendAdapter{Name: "Manga Pegasus", Format: "manga-pegasus", Handler: "pegasus", Capabilities: map[string]bool{"export": true}}, &customFrontend)
	if customFrontend.Builtin || customFrontend.Format != "manga-pegasus" || customFrontend.Handler != "pegasus" {
		t.Fatalf("custom frontend adapter = %#v", customFrontend)
	}
	var customProfile catalog.PackageProfile
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles", catalog.NewPackageProfile{Name: "Custom frontend package", Frontend: "pegasus", Target: "windows", DeviceProfileID: "builtin-device-windows-handheld", FrontendAdapterID: customFrontend.ID, FileMode: "reference", OutputSlug: "custom-frontend-package"}, &customProfile)
	if customProfile.FrontendAdapterID != customFrontend.ID {
		t.Fatalf("custom frontend handler was not selected by package profile: %#v", customProfile)
	}
	builtinProfile, err := store.GetPackageProfile(t.Context(), "builtin-windows-pegasus-zh")
	if err != nil || !builtinProfile.Builtin {
		t.Fatalf("built-in package profile ownership=%#v err=%v", builtinProfile, err)
	}
	immutableProfile := jsonErrorRequest(t, handler, http.MethodPut, "/api/v1/package-profiles/"+builtinProfile.ID, catalog.NewPackageProfile{Name: builtinProfile.Name, Frontend: builtinProfile.Frontend, Target: builtinProfile.Target, DeviceProfileID: builtinProfile.DeviceProfileID, FrontendAdapterID: builtinProfile.FrontendAdapterID, Locale: builtinProfile.Locale, FileMode: builtinProfile.FileMode, OutputSlug: builtinProfile.OutputSlug})
	if immutableProfile.Code != http.StatusConflict || !strings.Contains(immutableProfile.Body.String(), "builtin_immutable") {
		t.Fatalf("built-in package update response=%d %s", immutableProfile.Code, immutableProfile.Body.String())
	}
	immutableProfile = jsonErrorRequest(t, handler, http.MethodDelete, "/api/v1/package-profiles/"+builtinProfile.ID, nil)
	if immutableProfile.Code != http.StatusConflict || !strings.Contains(immutableProfile.Body.String(), "builtin_immutable") {
		t.Fatalf("built-in package delete response=%d %s", immutableProfile.Code, immutableProfile.Body.String())
	}
	type deviceCollection struct {
		Data []catalog.DeviceProfile `json:"data"`
	}
	var devices deviceCollection
	jsonRequest(t, handler, http.MethodGet, "/api/v1/device-profiles", nil, &devices)
	if len(devices.Data) < 9 {
		t.Fatalf("device profiles = %d", len(devices.Data))
	}
	type driverCollection struct {
		Data []catalog.EmulatorDriver `json:"data"`
	}
	var drivers driverCollection
	jsonRequest(t, handler, http.MethodGet, "/api/v1/emulator-drivers", nil, &drivers)
	if len(drivers.Data) < 11 {
		t.Fatalf("emulator drivers = %d", len(drivers.Data))
	}
	type coreCollection struct {
		Data []catalog.RetroArchCore `json:"data"`
	}
	var cores coreCollection
	jsonRequest(t, handler, http.MethodGet, "/api/v1/retroarch-cores", nil, &cores)
	if len(cores.Data) < 12 {
		t.Fatalf("RetroArch cores = %d", len(cores.Data))
	}
	immutable := jsonErrorRequest(t, handler, http.MethodPut, "/api/v1/frontend-adapters/"+pegasusAdapterID, catalog.NewFrontendAdapter{Name: "Changed", Format: "changed", Handler: "pegasus"})
	if immutable.Code != http.StatusConflict || !strings.Contains(immutable.Body.String(), "builtin_immutable") {
		t.Fatalf("builtin mutation response = %d %s", immutable.Code, immutable.Body.String())
	}
	reserved := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/source-adapters", catalog.NewSourceAdapter{ID: "builtin-user-source", Name: "Reserved", Format: "reserved", Handler: "pegasus"})
	if reserved.Code != http.StatusConflict || !strings.Contains(reserved.Body.String(), "builtin_namespace_reserved") {
		t.Fatalf("reserved namespace response = %d %s", reserved.Code, reserved.Body.String())
	}
	builtinMapping, err := store.GetCoreMapping(t.Context(), "builtin-mapping-global-gba")
	if err != nil || !builtinMapping.Builtin {
		t.Fatalf("built-in mapping ownership=%#v err=%v", builtinMapping, err)
	}
	immutableMapping := jsonErrorRequest(t, handler, http.MethodDelete, "/api/v1/core-mappings/"+builtinMapping.ID, nil)
	if immutableMapping.Code != http.StatusConflict || !strings.Contains(immutableMapping.Body.String(), "builtin_immutable") {
		t.Fatalf("built-in mapping delete response = %d %s", immutableMapping.Code, immutableMapping.Body.String())
	}
	unsafe := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/emulator-drivers", catalog.NewEmulatorDriver{Name: "Unsafe", Family: "unsafe", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: catalog.DriverLaunchSpec{Arguments: []string{"{{env.HOME}}"}}, Save: catalog.DriverSaveSpec{Scope: "game"}})
	if unsafe.Code != http.StatusBadRequest {
		t.Fatalf("unsafe driver response = %d %s", unsafe.Code, unsafe.Body.String())
	}
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("api-launch-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	game, _ := store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(context.Background(), catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(context.Background(), catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", SHA256: "api-launch-rom-hash"})
	var binding catalog.LaunchBinding
	jsonRequest(t, handler, http.MethodPost, "/api/v1/launch-bindings", catalog.NewLaunchBinding{EditionID: edition.ID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-retroarch", FrontendAdapterID: pegasusAdapterID}, &binding)
	var resolved struct {
		ROMPath        string                 `json:"rom_path"`
		Arguments      []string               `json:"arguments"`
		CoreResolution catalog.CoreResolution `json:"core_resolution"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/launch-bindings/resolve?edition_id="+edition.ID+"&device_profile_id=builtin-device-windows-handheld", nil, &resolved)
	if resolved.ROMPath != "gba/game.gba" || len(resolved.Arguments) != 3 || resolved.Arguments[1] != "mgba_libretro" || resolved.CoreResolution.Resolution != "global" {
		t.Fatalf("launch resolution = %#v", resolved)
	}
	enabled := true
	var profile catalog.PackageProfile
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles", catalog.NewPackageProfile{Name: "Windows launch test", Frontend: "pegasus", Target: "windows", Locale: "en", FileMode: "copy", OutputSlug: "windows-launch-test", Enabled: &enabled}, &profile)
	if profile.DeviceProfileID != "builtin-device-windows-handheld" || profile.FrontendAdapterID != pegasusAdapterID {
		t.Fatalf("package runtime refs were not normalized: %#v", profile)
	}
	var plan packagePlanResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &plan)
	foundLaunchManifest := false
	for _, item := range plan.Plan.Items {
		if item.Target == "varkiv-launches.json" && item.Action != "conflict" {
			foundLaunchManifest = true
		}
	}
	if !foundLaunchManifest || len(plan.Plan.Conflicts) != 0 {
		t.Fatalf("package plan did not include a safe launch manifest: %#v", plan.Plan)
	}
	var release packageReleaseResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-plans/"+plan.ID+"/build", map[string]any{}, &release)
	manifest, err := os.ReadFile(filepath.Join(root, ".library-data", "exports", profile.OutputSlug, "varkiv-launches.json"))
	if err != nil || !strings.Contains(string(manifest), "mgba_libretro") || strings.Contains(string(manifest), root) {
		t.Fatalf("exported launch manifest = %s, %v", manifest, err)
	}
}

func TestRuntimeImportHintReviewAPIIsInertAndAtomic(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	if err := store.ImportGamesAtomic(ctx, []catalog.ImportedGame{{
		Platform: "gba", DefaultTitle: "Imported runtime", EditionTitle: "Original", EditionType: "original",
		Artifacts:    []catalog.NewArtifact{{Path: "gba/imported-runtime.gba", SHA256: "imported-runtime-hash"}},
		RuntimeHints: []catalog.NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: "rm -rf {file.path}", SourceRef: "gba/metadata.pegasus.txt"}},
	}}); err != nil {
		t.Fatal(err)
	}
	games, err := store.ListGames(ctx, "")
	if err != nil || len(games) != 1 {
		t.Fatalf("games=%#v err=%v", games, err)
	}
	var listed struct {
		Data []catalog.RuntimeImportHint `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/runtime-import-hints?edition_id="+games[0].Editions[0].ID+"&status=pending", nil, &listed)
	if len(listed.Data) != 1 || listed.Data[0].Trust != "untrusted" || !strings.Contains(listed.Data[0].RawCommand, "rm -rf") {
		t.Fatalf("runtime hints=%#v", listed.Data)
	}
	var applied catalog.RuntimeHintApplication
	jsonRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/"+listed.Data[0].ID+"/apply", catalog.NewLaunchBinding{
		DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-mgba", Arguments: []string{"{{rom.path}}"},
	}, &applied)
	if applied.Hint.Status != "applied" || applied.Binding.DriverID != "builtin-driver-retroarch" || len(applied.Binding.Arguments) != 1 || strings.Contains(strings.Join(applied.Binding.Arguments, " "), "rm -rf") {
		t.Fatalf("applied=%#v", applied)
	}
	retry := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/"+listed.Data[0].ID+"/apply", catalog.NewLaunchBinding{DriverID: "builtin-driver-retroarch"})
	if retry.Code != http.StatusNotFound {
		t.Fatalf("reapply status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestRuntimeImportHintBatchAPIUsesSignedAtomicReview(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	importPair := func(prefix string) []catalog.RuntimeImportHint {
		t.Helper()
		games := []catalog.ImportedGame{
			{Platform: "pokemini", DefaultTitle: prefix + " one", EditionTitle: "Original", EditionType: "original", Artifacts: []catalog.NewArtifact{{Path: "pokemini/" + prefix + "-one.zip", SHA256: prefix + "-one"}}, RuntimeHints: []catalog.NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: `retroarch.exe -L pokemini_libretro.dll "{file.path}"`}}},
			{Platform: "pokemini", DefaultTitle: prefix + " two", EditionTitle: "Original", EditionType: "original", Artifacts: []catalog.NewArtifact{{Path: "pokemini/" + prefix + "-two.zip", SHA256: prefix + "-two"}}, RuntimeHints: []catalog.NewRuntimeImportHint{{SourceKind: "pegasus-command", SourceFormat: "pegasus-metadata", RawCommand: `retroarch.exe -L pokemini_libretro.dll "{file.path}"`}}},
		}
		if err := store.ImportGamesAtomic(ctx, games); err != nil {
			t.Fatal(err)
		}
		hints, err := store.ListRuntimeImportHints(ctx, "", "pending")
		if err != nil {
			t.Fatal(err)
		}
		return hints[len(hints)-2:]
	}
	requestFor := func(hints []catalog.RuntimeImportHint) runtimeHintBatchRequest {
		return runtimeHintBatchRequest{
			HintIDs: []string{hints[1].ID, hints[0].ID}, DeviceProfileID: "builtin-device-android-handheld",
			DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-pokemini",
			Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"},
		}
	}

	first := importPair("batch-api")
	request := requestFor(first)
	var preview runtimeHintBatchPreview
	jsonRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/batch/preview", request, &preview)
	if preview.Count != 2 || preview.PlatformID != "pokemini" || preview.FailurePolicy != "atomic" || preview.RawCommandsExecuted || preview.PreviewToken == "" {
		t.Fatalf("preview=%#v", preview)
	}
	tampered := request
	tampered.PreviewToken = preview.PreviewToken + "tampered"
	response := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/batch/commit", tampered)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "runtime_hint_batch_stale") {
		t.Fatalf("tampered commit=%d %s", response.Code, response.Body.String())
	}
	request.PreviewToken = preview.PreviewToken
	var applied catalog.RuntimeHintBatchResult
	jsonRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/batch/commit", request, &applied)
	if applied.Applied != 2 || len(applied.Applications) != 2 {
		t.Fatalf("applied=%#v", applied)
	}
	for _, application := range applied.Applications {
		if application.Hint.Status != "applied" || strings.Contains(strings.Join(application.Binding.Arguments, " "), "retroarch.exe") {
			t.Fatalf("raw command entered binding: %#v", application)
		}
	}

	second := importPair("batch-drift")
	driftRequest := requestFor(second)
	var driftPreview runtimeHintBatchPreview
	jsonRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/batch/preview", driftRequest, &driftPreview)
	jsonRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/"+second[1].ID+"/dismiss", nil, &catalog.RuntimeImportHint{})
	driftRequest.PreviewToken = driftPreview.PreviewToken
	response = jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/runtime-import-hints/batch/commit", driftRequest)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "runtime_hint_batch_stale") {
		t.Fatalf("drifted commit=%d %s", response.Code, response.Body.String())
	}
	bindings, err := store.ListLaunchBindings(ctx, "")
	remaining, remainingErr := store.GetRuntimeImportHint(ctx, second[0].ID)
	if err != nil || remainingErr != nil || len(bindings) != 2 || remaining.Status != "pending" {
		t.Fatalf("bindings=%#v remaining=%#v errors=%v/%v", bindings, remaining, err, remainingErr)
	}
}

func TestCrossPlatformSeriesAPI(t *testing.T) {
	_, handler, _ := testServer(t)
	var gba, nds catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Game Advance", Platform: "gba"}, &gba)
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Game DS", Platform: "nds"}, &nds)
	var series catalog.Series
	status := jsonRequest(t, handler, http.MethodPost, "/api/v1/series", catalog.NewSeries{DefaultTitle: "Game Saga", Titles: map[string]string{"zh-CN": "游戏传奇"}}, &series)
	if status != http.StatusCreated || series.ID == "" {
		t.Fatalf("series not created: %#v", series)
	}
	jsonRequest(t, handler, http.MethodPut, "/api/v1/series/"+series.ID+"/members/"+gba.ID, catalog.NewSeriesMember{RelationType: "mainline", SortOrder: 10}, &series)
	jsonRequest(t, handler, http.MethodPut, "/api/v1/series/"+series.ID+"/members/"+nds.ID, catalog.NewSeriesMember{RelationType: "port", SortOrder: 20}, &series)
	if len(series.Members) != 2 || series.Members[0].Game.Platform != "gba" || series.Members[1].Game.Platform != "nds" {
		t.Fatalf("cross-platform members missing: %#v", series)
	}
	var localized catalog.Series
	jsonRequest(t, handler, http.MethodGet, "/api/v1/series/"+series.ID+"?locale=zh-CN", nil, &localized)
	if localized.DisplayTitle != "游戏传奇" {
		t.Fatalf("localized title missing: %#v", localized)
	}
	var list struct {
		Data []catalog.Series `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/series?q=nds&locale=zh-CN", nil, &list)
	if len(list.Data) != 1 {
		t.Fatalf("series member search failed: %#v", list.Data)
	}
	if status = jsonRequest(t, handler, http.MethodDelete, "/api/v1/series/"+series.ID+"/members/"+nds.ID, nil, nil); status != http.StatusNoContent {
		t.Fatalf("member delete status = %d", status)
	}
}

func TestSeriesMutationAPIIsAtomic(t *testing.T) {
	_, handler, _ := testServer(t)
	var gba, nds catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Atomic GBA", Platform: "gba"}, &gba)
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Atomic NDS", Platform: "nds"}, &nds)
	members := []catalog.SeriesMemberMutation{{GameID: gba.ID, RelationType: "mainline", SortOrder: 10}, {GameID: nds.ID, RelationType: "port", SortOrder: 20}}
	var series catalog.Series
	status := jsonRequest(t, handler, http.MethodPost, "/api/v1/series", catalog.SeriesMutation{DefaultTitle: "Atomic Series", Description: "before", Titles: map[string]string{"en": "Atomic Series"}, Members: &members}, &series)
	if status != http.StatusCreated || len(series.Members) != 2 {
		t.Fatalf("atomic create = status %d, %#v", status, series)
	}

	badMembers := []catalog.SeriesMemberMutation{{GameID: gba.ID, RelationType: "remake", SortOrder: 1}, {GameID: nds.ID, RelationType: "port", SortOrder: -1}}
	bad := jsonErrorRequest(t, handler, http.MethodPut, "/api/v1/series/"+series.ID, catalog.SeriesMutation{DefaultTitle: "Must Not Persist", Description: "after", Members: &badMembers})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("negative member status=%d body=%s", bad.Code, bad.Body.String())
	}
	var unchanged catalog.Series
	jsonRequest(t, handler, http.MethodGet, "/api/v1/series/"+series.ID, nil, &unchanged)
	if unchanged.DefaultTitle != "Atomic Series" || unchanged.Description != "before" || len(unchanged.Members) != 2 || unchanged.Members[0].RelationType != "mainline" || unchanged.Members[1].SortOrder != 20 {
		t.Fatalf("failed request partially changed series: %#v", unchanged)
	}

	replacement := []catalog.SeriesMemberMutation{{GameID: nds.ID, RelationType: "remake", SortOrder: 3}}
	jsonRequest(t, handler, http.MethodPut, "/api/v1/series/"+series.ID, catalog.SeriesMutation{DefaultTitle: "Atomic Series II", Description: "after", Members: &replacement}, &series)
	if series.DefaultTitle != "Atomic Series II" || len(series.Members) != 1 || series.Members[0].GameID != nds.ID || series.Members[0].SortOrder != 3 {
		t.Fatalf("atomic replacement = %#v", series)
	}
}

func TestPersistentROMSourceScanCommitAndNonDestructiveDelete(t *testing.T) {
	_, handler, root := testServer(t)
	romDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(romDir, "Advance Wars.gba")
	if err := os.WriteFile(romPath, []byte("small-fixture-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	var source catalog.LibrarySource
	status := jsonRequest(t, handler, http.MethodPost, "/api/v1/sources", catalog.NewLibrarySource{Name: "GBA on NAS", Kind: "rom_directory", RootPath: "gba", Platform: "gba", ROMStoragePolicy: "reference", MediaStoragePolicy: "ignore"}, &source)
	if status != http.StatusCreated || source.RootPath != "gba" || !source.Enabled || source.SourceAdapterID != "builtin-source-direct-rom" {
		t.Fatalf("source create = %d %#v", status, source)
	}
	var preview struct {
		Scan         catalog.SourceScan `json:"scan"`
		PreviewToken string             `json:"preview_token"`
		Candidates   []importCandidate  `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources/"+source.ID+"/scans", map[string]any{}, &preview)
	if preview.Scan.Status != "ready" || preview.Scan.ImportableCount != 1 || len(preview.Candidates) != 1 || preview.PreviewToken == "" {
		t.Fatalf("source preview = %#v", preview)
	}
	var committed map[string]any
	jsonRequest(t, handler, http.MethodPost, "/api/v1/source-scans/"+preview.Scan.ID+"/commit", sourceScanCommitRequest{PreviewToken: preview.PreviewToken, SelectedTokens: []string{preview.Candidates[0].Token}}, &committed)
	if committed["imported"] != float64(1) {
		t.Fatalf("source commit = %#v", committed)
	}
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/sources/"+source.ID, nil))
	if deleteResponse.Code != http.StatusConflict {
		t.Fatalf("source history deletion status = %d %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if data, err := os.ReadFile(romPath); err != nil || string(data) != "small-fixture-rom" {
		t.Fatalf("source file changed after metadata deletion attempt: %q, %v", data, err)
	}
	var scans struct {
		Data []catalog.SourceScan `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/source-scans?source_id="+source.ID, nil, &scans)
	if len(scans.Data) != 1 || scans.Data[0].Status != "committed" {
		t.Fatalf("scan audit history = %#v", scans.Data)
	}
}

func TestPersistentNeutralManifestSourceKeepsPerEntryPlatforms(t *testing.T) {
	_, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "recovery", "roms"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "recovery", "roms", "fixture.gba"), []byte("neutral-persistent-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"format_version": 4,
		"entries": []map[string]any{{
			"game_id": "neutral-source-game", "edition_id": "neutral-source-edition", "platform": "gba",
			"game_default_title": "Neutral source game", "edition_default_title": "Original", "edition_type": "original",
			"artifacts": []string{"roms/fixture.gba"}, "media": []any{},
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "recovery", "library-manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	var source catalog.LibrarySource
	status := jsonRequest(t, handler, http.MethodPost, "/api/v1/sources", catalog.NewLibrarySource{Name: "Recovery manifest", Kind: "varkiv", MetadataPath: "recovery/library-manifest.json", ROMStoragePolicy: "reference", MediaStoragePolicy: "copy"}, &source)
	if status != http.StatusCreated || source.Kind != "varkiv" || source.Platform != "" || source.RootPath != "recovery" {
		t.Fatalf("neutral source create = %d %#v", status, source)
	}
	var preview struct {
		Scan         catalog.SourceScan `json:"scan"`
		PreviewToken string             `json:"preview_token"`
		Candidates   []importCandidate  `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources/"+source.ID+"/scans", map[string]any{}, &preview)
	if preview.Scan.ImportableCount != 1 || len(preview.Candidates) != 1 || preview.Candidates[0].Game.Platform != "gba" {
		t.Fatalf("neutral source preview = %#v", preview)
	}
	var committed map[string]any
	jsonRequest(t, handler, http.MethodPost, "/api/v1/source-scans/"+preview.Scan.ID+"/commit", sourceScanCommitRequest{PreviewToken: preview.PreviewToken, SelectedTokens: []string{preview.Candidates[0].Token}}, &committed)
	if committed["parsed"] != float64(1) || committed["imported"] != float64(1) || committed["skipped"] != float64(0) {
		t.Fatalf("neutral source commit = %#v", committed)
	}
}

func TestNeutralManifestV6PreviewBindsAndAtomicallyImportsCustomPlatform(t *testing.T) {
	store, handler, root := testServer(t)
	ctx := context.Background()
	recovery := filepath.Join(root, "portable-v6")
	romBody := []byte("portable-v6-custom-platform-rom")
	if err := os.MkdirAll(filepath.Join(recovery, "roms"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recovery, "roms", "fixture.opk"), romBody, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(romBody)
	manifestPath := filepath.Join(recovery, "library-manifest.json")
	writeManifest := func(nameZH string) {
		t.Helper()
		manifest := map[string]any{
			"format_version": 6,
			"custom_platforms": []map[string]any{{
				"id": "fixture-handheld-api", "name": "Fixture Handheld API", "name_zh": nameZH, "vendor": "Community", "category": "handheld",
				"aliases": []string{"oh-api"}, "extensions": []string{".opk"}, "esde_systems": []string{"fixture-handheld-api-es"}, "bios": "none", "runtime": "native",
			}},
			"entries": []map[string]any{{
				"game_id": "portable-v6-game", "edition_id": "portable-v6-edition", "platform": "fixture-handheld-api",
				"game_default_title": "Portable v6 game", "edition_default_title": "Original", "edition_type": "original",
				"artifacts":        []string{"roms/fixture.opk"},
				"artifact_records": []map[string]any{{"path": "roms/fixture.opk", "role": "rom", "original_name": "fixture.opk", "size": len(romBody), "sha256": hex.EncodeToString(digest[:])}},
			}},
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(manifestPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest("便携平台")
	request := importRequest{Format: "varkiv", Source: "portable-v6/library-manifest.json", ROMStorage: "reference", MediaStorage: "ignore"}
	var preview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request, &preview)
	if len(preview.Candidates) != 1 || preview.Candidates[0].Game.PlatformDefinition == nil || preview.Candidates[0].Game.PlatformDefinition.ID != "fixture-handheld-api" {
		t.Fatalf("portable platform is absent from signed preview: %#v", preview)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	writeManifest("便携平台新定义")
	stale := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/commit", request)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "import_preview_stale") {
		t.Fatalf("changed portable definition was not bound to preview: %d %s", stale.Code, stale.Body.String())
	}
	if _, err := store.GetCustomPlatform(ctx, "fixture-handheld-api"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale preview wrote custom platform: %v", err)
	}

	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", importRequest{Format: "varkiv", Source: "portable-v6/library-manifest.json", ROMStorage: "reference", MediaStorage: "ignore"}, &preview)
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	var committed map[string]any
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/commit", request, &committed)
	if committed["imported"] != float64(1) || committed["failure_policy"] != "atomic" {
		t.Fatalf("v6 commit = %#v", committed)
	}
	platform, err := store.GetCustomPlatform(ctx, "fixture-handheld-api")
	if err != nil || platform.NameZH != "便携平台新定义" || !platform.Enabled {
		t.Fatalf("imported platform = %#v, %v", platform, err)
	}
	game, err := store.GetGame(ctx, "portable-v6-game", "zh-CN")
	if err != nil || game.Platform != "fixture-handheld-api" {
		t.Fatalf("imported game = %#v, %v", game, err)
	}

	writeManifest("冲突定义")
	conflict := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", importRequest{Format: "varkiv", Source: "portable-v6/library-manifest.json", ROMStorage: "reference", MediaStorage: "ignore"})
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "platform_definition_conflict") {
		t.Fatalf("conflicting portable definition = %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestLibrarySourceRejectsPathsOutsideLibraryRoot(t *testing.T) {
	_, handler, _ := testServer(t)
	outside := filepath.Join(t.TempDir(), "private.gba")
	if err := os.WriteFile(outside, []byte("do-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/sources", catalog.NewLibrarySource{Name: "Outside", Kind: "rom_directory", RootPath: outside, Platform: "gba"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "inside library") {
		t.Fatalf("outside path was not rejected: %d %s", response.Code, response.Body.String())
	}
}

func TestPersistentSourceRejectsTokenFromAnotherPreview(t *testing.T) {
	_, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "token.gba"), []byte("token-fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	var source catalog.LibrarySource
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources", catalog.NewLibrarySource{Name: "Token source", Kind: "rom_directory", RootPath: "gba", Platform: "gba"}, &source)
	var preview struct {
		Scan       catalog.SourceScan `json:"scan"`
		Candidates []importCandidate  `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources/"+source.ID+"/scans", map[string]any{}, &preview)
	response := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/source-scans/"+preview.Scan.ID+"/commit", sourceScanCommitRequest{PreviewToken: "not-this-preview", SelectedTokens: []string{preview.Candidates[0].Token}})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "import_preview_stale") {
		t.Fatalf("mismatched source token = %d %s", response.Code, response.Body.String())
	}
	var stale catalog.SourceScan
	jsonRequest(t, handler, http.MethodGet, "/api/v1/source-scans/"+preview.Scan.ID, nil, &stale)
	if stale.Status != "stale" || stale.FailureCode != "import_preview_stale" {
		t.Fatalf("stale source scan was not audited: %#v", stale)
	}
}

func TestPackageProfilePlanBuildReleaseAndDrift(t *testing.T) {
	store, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(root, "gba", "package.gba")
	if err := os.WriteFile(romPath, []byte("package-rom-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Package Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/package.gba", Role: "rom"})
	var profile catalog.PackageProfile
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles", catalog.NewPackageProfile{Name: "Windows configured", Frontend: "pegasus", Target: "windows", Locale: "en", FileMode: "copy", OutputSlug: "windows-configured", Templates: []catalog.NewPackageConfigTemplate{{Name: "Edition options", Scope: "edition", OutputPath: "config/{{edition.id}}.cfg", Body: "rom={{rom.path}}\nfullscreen=true\n"}}}, &profile)
	if profile.ID == "" || len(profile.Templates) != 1 {
		t.Fatalf("package profile = %#v", profile)
	}
	var planned packagePlanResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &planned)
	if planned.Status != "ready" || planned.Fingerprint == "" || len(planned.Plan.Conflicts) != 0 {
		t.Fatalf("package plan = %#v", planned)
	}
	if !planned.Plan.SpaceChecked || planned.Plan.EstimatedWriteBytes <= 0 || planned.Plan.AvailableBytes <= planned.Plan.EstimatedWriteBytes || filepath.IsAbs(planned.Plan.Output) {
		t.Fatalf("package space preflight or public output path = %#v", planned.Plan)
	}
	var release packageReleaseResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-plans/"+planned.ID+"/build", map[string]any{}, &release)
	if release.Status != "succeeded" || release.Result["output"] != "state/exports/windows-configured" {
		t.Fatalf("package release = %#v", release)
	}
	configPath := filepath.Join(root, ".library-data", "exports", "windows-configured", "config", edition.ID+".cfg")
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "rom=gba/package.gba") {
		t.Fatalf("rendered config = %q, %v", data, err)
	}
	var history struct {
		Data []packageReleaseResponse `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/package-releases?profile_id="+profile.ID, nil, &history)
	if len(history.Data) != 1 || history.Data[0].PlanID != planned.ID {
		t.Fatalf("release history = %#v", history.Data)
	}
	var driftPlan packagePlanResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &driftPlan)
	if err = os.WriteFile(romPath, []byte("package-rom-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/package-plans/"+driftPlan.ID+"/build", map[string]any{})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "package_plan_stale") {
		t.Fatalf("drifted plan = %d %s", stale.Code, stale.Body.String())
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/package-plans/"+driftPlan.ID, nil, &driftPlan)
	if driftPlan.Status != "stale" {
		t.Fatalf("drifted plan was not audited: %#v", driftPlan)
	}
	var updatePlan packagePlanResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &updatePlan)
	var updated packageReleaseResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-plans/"+updatePlan.ID+"/build", map[string]any{}, &updated)
	recovery, _ := updated.Result["recovery_snapshot"].(string)
	if updated.Status != "succeeded" || recovery == "" || !strings.HasPrefix(recovery, "state/recovery/packages/windows-configured/release-") || strings.Contains(recovery, root) {
		t.Fatalf("updated package has no safe recovery locator: %#v", updated)
	}
	previousROM, err := os.ReadFile(filepath.Join(root, ".library-data", "recovery", "packages", "windows-configured", filepath.Base(recovery), "files", "gba", "package.gba"))
	if err != nil || string(previousROM) != "package-rom-v1" {
		t.Fatalf("pre-update package ROM was not retained: %q, %v", previousROM, err)
	}
	if _, err = os.Lstat(filepath.Join(root, ".library-data", "exports", "windows-configured", ".varkiv-recovery")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery history was included in package output: %v", err)
	}
}

func TestConfigTemplatePresetCatalogIsFilteredReadOnlyAndBundlerSafe(t *testing.T) {
	_, handler, _ := testServer(t)
	var all struct {
		Data       []ConfigTemplatePreset `json:"data"`
		Pagination pagination             `json:"pagination"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/config-template-presets", nil, &all)
	if len(all.Data) != 4 || all.Pagination.Total != 4 {
		t.Fatalf("template preset catalog = %#v", all)
	}
	ids := map[string]bool{}
	for _, item := range all.Data {
		if ids[item.ID] || item.ContractVersion < 1 || item.Name == "" || item.Summary == "" || len(item.Targets) == 0 || len(item.Frontends) == 0 {
			t.Fatalf("invalid template preset = %#v", item)
		}
		ids[item.ID] = true
		if filepath.IsAbs(filepath.FromSlash(item.OutputPath)) || strings.Contains(item.Body, "$HOME") || strings.Contains(item.Body, "{{env.") {
			t.Fatalf("template preset exposes a host path or environment value = %#v", item)
		}
		if item.ID == "builtin-template-launch-resolution" && (item.ContractVersion != 2 || !strings.Contains(item.Body, "{{launch.arguments_json}}") || !strings.Contains(item.Body, "{{launch.executable_hints_json}}")) {
			t.Fatalf("launch preset does not expose typed reviewed argv = %#v", item)
		}
		_, err := bundler.ValidateProfile(bundler.Profile{Name: "preset", Frontend: item.Frontends[0], Target: item.Targets[0], FileMode: "copy", Templates: []bundler.ConfigTemplate{{Name: item.Name, Scope: item.Scope, OutputPath: item.OutputPath, Body: item.Body}}})
		if err != nil {
			t.Fatalf("template preset %s is not accepted by the production validator: %v", item.ID, err)
		}
	}
	var android struct {
		Data []ConfigTemplatePreset `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/config-template-presets?target=android&frontend=pegasus", nil, &android)
	if len(android.Data) != 4 || android.Data[3].ID != "builtin-template-android-intent" {
		t.Fatalf("android preset filter = %#v", android.Data)
	}
	var windows struct {
		Data []ConfigTemplatePreset `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/config-template-presets?target=windows&frontend=es-de", nil, &windows)
	if len(windows.Data) != 3 {
		t.Fatalf("windows preset filter = %#v", windows.Data)
	}
	if response := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/config-template-presets", map[string]any{"name": "mutate"}); response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("preset catalog mutation = %d allow=%q body=%s", response.Code, response.Header().Get("Allow"), response.Body.String())
	}
}

func TestPackagePlanRefusesUnmanagedOutputCollision(t *testing.T) {
	store, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "collision.gba"), []byte("catalog-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Collision", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/collision.gba", Role: "rom"})
	var profile catalog.PackageProfile
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles", catalog.NewPackageProfile{Name: "Collision", Frontend: "pegasus", Target: "portable", FileMode: "copy", OutputSlug: "collision"}, &profile)
	target := filepath.Join(root, ".library-data", "exports", "collision", "gba", "collision.gba")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("user-owned-output"), 0o600); err != nil {
		t.Fatal(err)
	}
	var plan packagePlanResponse
	jsonRequest(t, handler, http.MethodPost, "/api/v1/package-profiles/"+profile.ID+"/plans", map[string]any{}, &plan)
	if len(plan.Plan.Conflicts) != 1 || plan.Plan.Conflicts[0] != "gba/collision.gba" {
		t.Fatalf("unmanaged collision missing from plan: %#v", plan.Plan)
	}
	response := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/package-plans/"+plan.ID+"/build", map[string]any{})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "unmanaged_target_conflict") {
		t.Fatalf("unmanaged build = %d %s", response.Code, response.Body.String())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "user-owned-output" {
		t.Fatalf("unmanaged output changed: %q, %v", data, err)
	}
	var releases struct {
		Data []packageReleaseResponse `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/package-releases?profile_id="+profile.ID, nil, &releases)
	if len(releases.Data) != 1 || releases.Data[0].Status != "failed" || releases.Data[0].Result["code"] != "unmanaged_target_conflict" {
		t.Fatalf("failed release audit = %#v", releases.Data)
	}
	failureJSON, _ := json.Marshal(releases.Data[0].Result)
	if strings.Contains(string(failureJSON), root) || strings.Contains(string(failureJSON), "user-owned-output") {
		t.Fatalf("failed release leaked private output details: %s", failureJSON)
	}
}

func TestV1APIContract(t *testing.T) {
	_, handler, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platforms?limit=2&offset=1", nil)
	req.Header.Set("X-Request-ID", "contract-test-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("X-Request-ID") != "contract-test-1" || recorder.Header().Get("X-Varkiv-API-Version") != "v1" {
		t.Fatalf("bad v1 headers: %d %#v", recorder.Code, recorder.Header())
	}
	var collection struct {
		Data       []map[string]any `json:"data"`
		Pagination pagination       `json:"pagination"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Data) != 2 || collection.Pagination.Limit != 2 || collection.Pagination.Offset != 1 || collection.Pagination.Total < 40 {
		t.Fatalf("bad pagination: %#v", collection)
	}

	badCategory := httptest.NewRecorder()
	handler.ServeHTTP(badCategory, httptest.NewRequest(http.MethodGet, "/api/v1/platforms?category=toaster", nil))
	var apiFailure apiErrorEnvelope
	if err := json.Unmarshal(badCategory.Body.Bytes(), &apiFailure); err != nil {
		t.Fatal(err)
	}
	if badCategory.Code != http.StatusBadRequest || apiFailure.Error.Code != "invalid_argument" {
		t.Fatalf("bad category error: %d %#v", badCategory.Code, apiFailure)
	}

	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/api/platforms", nil))
	if legacy.Header().Get("Deprecation") != "true" || !strings.Contains(legacy.Header().Get("Link"), "/api/v1") {
		t.Fatalf("legacy compatibility headers missing: %#v", legacy.Header())
	}
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/api/v1/platforms/does-not-exist", nil))
	if err := json.Unmarshal(notFound.Body.Bytes(), &apiFailure); err != nil {
		t.Fatal(err)
	}
	if notFound.Code != http.StatusNotFound || apiFailure.Error.Code != "platform_not_found" || apiFailure.Error.RequestID == "" {
		t.Fatalf("bad structured error: %d %#v", notFound.Code, apiFailure)
	}

	mediaType := httptest.NewRecorder()
	badBody := httptest.NewRequest(http.MethodPost, "/api/v1/games", strings.NewReader(`{"default_title":"Game","platform":"gba"}`))
	badBody.Header.Set("Content-Type", "text/plain")
	handler.ServeHTTP(mediaType, badBody)
	if err := json.Unmarshal(mediaType.Body.Bytes(), &apiFailure); err != nil {
		t.Fatal(err)
	}
	if mediaType.Code != http.StatusUnsupportedMediaType || apiFailure.Error.Code != "unsupported_media_type" {
		t.Fatalf("bad media type error: %d %#v", mediaType.Code, apiFailure)
	}

	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodPatch, "/api/v1/games", nil))
	if err := json.Unmarshal(method.Body.Bytes(), &apiFailure); err != nil {
		t.Fatal(err)
	}
	if method.Code != http.StatusMethodNotAllowed || apiFailure.Error.Code != "method_not_allowed" || !strings.Contains(method.Header().Get("Allow"), http.MethodGet) {
		t.Fatalf("bad method error: %d %#v %#v", method.Code, apiFailure, method.Header())
	}

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	if err := json.Unmarshal(unknown.Body.Bytes(), &apiFailure); err != nil {
		t.Fatal(err)
	}
	if unknown.Code != http.StatusNotFound || apiFailure.Error.Code != "api_route_not_found" {
		t.Fatalf("bad route error: %d %#v", unknown.Code, apiFailure)
	}
	removedWorkAlias := httptest.NewRecorder()
	handler.ServeHTTP(removedWorkAlias, httptest.NewRequest(http.MethodGet, "/api/v1/works", nil))
	if err := json.Unmarshal(removedWorkAlias.Body.Bytes(), &apiFailure); err != nil {
		t.Fatal(err)
	}
	if removedWorkAlias.Code != http.StatusNotFound || apiFailure.Error.Code != "api_route_not_found" {
		t.Fatalf("removed work alias remained reachable: %d %#v", removedWorkAlias.Code, apiFailure)
	}

	for _, path := range []string{"/api/v1", "/api/v1/capabilities", "/api/v1/health/live", "/api/v1/health/ready", "/api/v1/openapi.yaml"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, response.Code, response.Body.String())
		}
	}
}

func TestMediaMetadataAPIUpdatesOnlyLibrarySemantics(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Media API", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.AddMedia(ctx, catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: "media/aa/blob.png", OriginalName: "cover.png", MIMEType: "image/png", Size: 3, SHA256: strings.Repeat("b", 64), SourceType: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	var updated catalog.MediaAsset
	jsonRequest(t, handler, http.MethodPut, "/api/v1/media/"+media.ID, catalog.MediaMetadataUpdate{Kind: "poster", Locale: "en", SortOrder: 4}, &updated)
	if updated.Kind != "poster" || updated.Locale != "en" || updated.SortOrder != 4 {
		t.Fatalf("media metadata response = %#v", updated)
	}
	if updated.GameID != media.GameID || updated.Path != media.Path || updated.SHA256 != media.SHA256 || updated.Size != media.Size || updated.StorageKind != media.StorageKind {
		t.Fatalf("media identity changed through API: before=%#v after=%#v", media, updated)
	}
	bad := jsonErrorRequest(t, handler, http.MethodPut, "/api/v1/media/"+media.ID, catalog.MediaMetadataUpdate{Kind: "cover", SortOrder: -1})
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "invalid_argument") {
		t.Fatalf("negative media ordering = %d %s", bad.Code, bad.Body.String())
	}
	missing := jsonErrorRequest(t, handler, http.MethodPut, "/api/v1/media/00000000-0000-0000-0000-000000000000", catalog.MediaMetadataUpdate{Kind: "cover", SortOrder: 0})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing media update = %d %s", missing.Code, missing.Body.String())
	}
}

func TestV1ResourceCompleteness(t *testing.T) {
	_, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	var game catalog.Game
	gameBody, err := json.Marshal(catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	gameRequest := httptest.NewRequest(http.MethodPost, "/api/v1/games", bytes.NewReader(gameBody))
	gameRequest.Header.Set("Content-Type", "application/json")
	gameResponse := httptest.NewRecorder()
	handler.ServeHTTP(gameResponse, gameRequest)
	if gameResponse.Code != http.StatusCreated {
		t.Fatalf("game create: %d %s", gameResponse.Code, gameResponse.Body.String())
	}
	if err := json.Unmarshal(gameResponse.Body.Bytes(), &game); err != nil {
		t.Fatal(err)
	}
	if got, want := gameResponse.Header().Get("Location"), "/api/v1/games/"+game.ID; got != want {
		t.Fatalf("game location: got %q, want %q", got, want)
	}
	var withEdition catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/editions", editionRequest{NewEdition: catalog.NewEdition{GameID: game.ID, DefaultTitle: "Game", EditionType: "original"}}, &withEdition)
	editionID := withEdition.Editions[0].ID
	var artifact catalog.Artifact
	jsonRequest(t, handler, http.MethodPost, "/api/v1/artifacts", catalog.NewArtifact{EditionID: editionID, Path: "gba/game.gba", Role: "rom"}, &artifact)
	var gotEdition catalog.Edition
	jsonRequest(t, handler, http.MethodGet, "/api/v1/editions/"+editionID, nil, &gotEdition)
	if gotEdition.ID != editionID {
		t.Fatalf("edition GET failed: %#v", gotEdition)
	}
	var gotArtifact catalog.Artifact
	jsonRequest(t, handler, http.MethodGet, "/api/v1/artifacts/"+artifact.ID, nil, &gotArtifact)
	if gotArtifact.ID != artifact.ID {
		t.Fatalf("artifact GET failed: %#v", gotArtifact)
	}

	var device catalog.Device
	jsonRequest(t, handler, http.MethodPost, "/api/v1/devices", catalog.NewDevice{Name: "RG", OSFamily: "android", Architecture: "aarch64"}, &device)
	var gotDevice catalog.Device
	jsonRequest(t, handler, http.MethodGet, "/api/v1/devices/"+device.ID, nil, &gotDevice)
	jsonRequest(t, handler, http.MethodPut, "/api/v1/devices/"+device.ID, catalog.NewDevice{Name: "RG Updated", OSFamily: "android", Architecture: "aarch64"}, &gotDevice)
	if gotDevice.Name != "RG Updated" {
		t.Fatalf("device update failed: %#v", gotDevice)
	}
	if status := jsonRequest(t, handler, http.MethodDelete, "/api/v1/devices/"+device.ID, nil, nil); status != http.StatusNoContent {
		t.Fatalf("device delete status: %d", status)
	}
}

func TestManualEditionWithMissingArtifactIsRejectedAtomically(t *testing.T) {
	_, handler, root := testServer(t)
	var game catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Planned game", Platform: "gba"}, &game)
	body, err := json.Marshal(editionRequest{
		NewEdition:   catalog.NewEdition{GameID: game.ID, DefaultTitle: "Missing ROM", EditionType: "translation"},
		ArtifactPath: "gba/not-present.gba",
		ArtifactRole: "rom",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/editions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var failure apiErrorEnvelope
	if err = json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || failure.Error.Code != "artifact_missing" || strings.Contains(response.Body.String(), root) {
		t.Fatalf("missing artifact was not rejected: %d %s", response.Code, response.Body.String())
	}
	var refreshed catalog.Game
	jsonRequest(t, handler, http.MethodGet, "/api/v1/games/"+game.ID, nil, &refreshed)
	if len(refreshed.Editions) != 0 || refreshed.PrimaryEditionID != "" {
		t.Fatalf("failed manual create left partial catalog records: %#v", refreshed)
	}
}

func TestArtifactValidationErrorsAreStableAndDoNotExposeHostPaths(t *testing.T) {
	_, handler, root := testServer(t)
	var game catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Private path test", Platform: "ps2"}, &game)
	var withEdition catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/editions", editionRequest{NewEdition: catalog.NewEdition{GameID: game.ID, DefaultTitle: "Private path test", EditionType: "original"}}, &withEdition)
	editionID := withEdition.Editions[0].ID

	directory := filepath.Join(root, "ps2", "game")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "private-target"), filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.iso"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "ps2", "linked-parent")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		code string
		want int
	}{
		{name: "outside library", path: filepath.Join(filepath.Dir(root), "private-outside.rom"), code: "artifact_outside_library", want: http.StatusBadRequest},
		{name: "unhashable directory", path: "ps2/game", code: "artifact_unreadable", want: http.StatusUnprocessableEntity},
		{name: "symlinked parent", path: "ps2/linked-parent/private.iso", code: "artifact_unreadable", want: http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(catalog.NewArtifact{EditionID: editionID, Path: test.path, Role: "rom"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/artifacts", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var failure apiErrorEnvelope
			if err = json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.want || failure.Error.Code != test.code || strings.Contains(response.Body.String(), root) || strings.Contains(response.Body.String(), outside) {
				t.Fatalf("unexpected private-path error: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestImportPreviewAndPackageAPI(t *testing.T) {
	_, handler, root := testServer(t)
	var profiles []bundler.Profile
	jsonRequest(t, handler, http.MethodGet, "/api/package-profiles", nil, &profiles)
	androidFrontends := map[string]bool{}
	for _, profile := range profiles {
		if profile.Target == "android" {
			androidFrontends[profile.Frontend] = true
		}
	}
	if !androidFrontends["pegasus"] || !androidFrontends["es-de"] {
		t.Fatalf("Android frontend presets incomplete: %#v", androidFrontends)
	}
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "preview.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := "collection: gba\n\ngame: Preview Game\nfile: preview.gba\n"
	if err := os.WriteFile(filepath.Join(root, "gba", "metadata.pegasus.txt"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	var sources []importSource
	jsonRequest(t, handler, http.MethodGet, "/api/import-sources?format=pegasus", nil, &sources)
	if len(sources) != 1 || sources[0].Path != "gba/metadata.pegasus.txt" || sources[0].Platform != "gba" {
		t.Fatalf("bad discovered import sources: %#v", sources)
	}
	request := importRequest{Format: "pegasus", Source: "gba/metadata.pegasus.txt", Platform: "gba", Locale: "en"}
	var preview struct {
		Parsed       int               `json:"parsed"`
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/imports/preview", request, &preview)
	if preview.Parsed != 1 || preview.Candidates[0].Status != "new" {
		t.Fatalf("bad preview: %#v", preview)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	var imported struct {
		Imported int `json:"imported"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/imports/commit", request, &imported)
	if imported.Imported != 1 {
		t.Fatalf("bad import result: %#v", imported)
	}
	var packed struct {
		Exported int    `json:"exported_editions"`
		Output   string `json:"output"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/packages", map[string]string{"name": "test pack", "frontend": "es-de", "target": "rocknix", "locale": "en", "file_mode": "copy"}, &packed)
	if packed.Exported != 1 {
		t.Fatalf("bad package result: %#v", packed)
	}
	if packed.Output != "state/exports/test-pack" {
		t.Fatalf("package leaked or returned an unexpected output locator: %q", packed.Output)
	}
	if _, err := os.Stat(filepath.Join(root, ".library-data", "exports", "test-pack", "package-manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSeparateMetadataContentRootPersistsAndParticipatesInSignedPreview(t *testing.T) {
	store, handler, root := testServer(t)
	for _, directory := range []string{"metadata/gba", "roms/gba", "other/gba"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "roms", "gba", "separate.gba"), []byte("selected-root-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other", "gba", "separate.gba"), []byte("other-root-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := "collection: GBA\n\ngame: Separate root\nfile: separate.gba\n"
	if err := os.WriteFile(filepath.Join(root, "metadata", "gba", "metadata.pegasus.txt"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	request := importRequest{Format: "pegasus", Source: "metadata/gba/metadata.pegasus.txt", ContentRoot: "roms/gba", Platform: "gba", ROMStorage: "reference", MediaStorage: "ignore"}
	var preview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request, &preview)
	if len(preview.Candidates) != 1 || preview.Candidates[0].Status != "new" || preview.Candidates[0].Game.Artifacts[0].Path != "roms/gba/separate.gba" {
		t.Fatalf("separate root preview failed: %#v", preview)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	request.ContentRoot = "other/gba"
	stale := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/commit", request)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "import_preview_stale") {
		t.Fatalf("changed content root must invalidate preview: %d %s", stale.Code, stale.Body.String())
	}
	if games, err := store.ListGames(context.Background(), ""); err != nil || len(games) != 0 {
		t.Fatalf("stale root wrote catalog data: %#v %v", games, err)
	}

	var source catalog.LibrarySource
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources", catalog.NewLibrarySource{
		Name: "Separated Pegasus", Kind: "pegasus", RootPath: "roms/gba", MetadataPath: "metadata/gba/metadata.pegasus.txt", Platform: "gba", ROMStoragePolicy: "reference", MediaStoragePolicy: "ignore",
	}, &source)
	if source.RootPath != "roms/gba" || source.MetadataPath != "metadata/gba/metadata.pegasus.txt" {
		t.Fatalf("persistent source lost separate root: %#v", source)
	}
	var scan struct {
		Scan         catalog.SourceScan `json:"scan"`
		PreviewToken string             `json:"preview_token"`
		Candidates   []importCandidate  `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources/"+source.ID+"/scans", map[string]any{}, &scan)
	if len(scan.Candidates) != 1 || scan.Candidates[0].Game.Artifacts[0].Path != "roms/gba/separate.gba" {
		t.Fatalf("persistent scan did not use content root: %#v", scan)
	}
	var result map[string]any
	jsonRequest(t, handler, http.MethodPost, "/api/v1/source-scans/"+scan.Scan.ID+"/commit", sourceScanCommitRequest{PreviewToken: scan.PreviewToken, SelectedTokens: []string{scan.Candidates[0].Token}}, &result)
	if result["imported"] != float64(1) {
		t.Fatalf("persistent separate-root commit failed: %#v", result)
	}
}

func TestROMScanPreviewAndCommitAPI(t *testing.T) {
	store, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "Direct.gba"), []byte("direct-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := romImportRequest{Source: "gba", Platform: "gba", ROMStorage: "reference"}
	var preview struct {
		Parsed            int                      `json:"parsed"`
		PreviewToken      string                   `json:"preview_token"`
		Candidates        []importCandidate        `json:"candidates"`
		SourceDiagnostics []importSourceDiagnostic `json:"source_diagnostics"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/preview", request, &preview)
	if preview.Parsed != 1 || preview.Candidates[0].Status != "new" || preview.Candidates[0].Availability != "ready" || len(preview.SourceDiagnostics) != 0 {
		t.Fatalf("bad ROM preview: %#v", preview)
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil || len(games) != 0 {
		t.Fatalf("ROM preview mutated catalog: games=%#v err=%v", games, err)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	var result struct {
		Imported int `json:"imported"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/commit", request, &result)
	if result.Imported != 1 {
		t.Fatalf("bad ROM commit: %#v", result)
	}
	request.PreviewToken = ""
	request.SelectedTokens = nil
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/preview", request, &preview)
	if preview.Candidates[0].Status != "duplicate" {
		t.Fatalf("duplicate ROM not reported: %#v", preview)
	}
}

func TestROMImportRejectsStaleAndTamperedCandidateTokens(t *testing.T) {
	store, handler, root := testServer(t)
	romDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(romDir, "Drift.gba")
	if err := os.WriteFile(romPath, []byte("before-preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := romImportRequest{Source: "gba", Platform: "gba", ROMStorage: "reference"}
	var preview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/preview", request, &preview)
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token + "tampered"}
	tampered := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/commit", request)
	if tampered.Code != http.StatusConflict || !strings.Contains(tampered.Body.String(), "import_preview_stale") {
		t.Fatalf("tampered candidate token was not rejected: %d %s", tampered.Code, tampered.Body.String())
	}

	request.SelectedTokens = []string{preview.Candidates[0].Token, preview.Candidates[0].Token}
	duplicate := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/commit", request)
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(duplicate.Body.String(), "selected_tokens must not contain duplicates") {
		t.Fatalf("duplicate candidate token was not rejected: %d %s", duplicate.Code, duplicate.Body.String())
	}

	request.SelectedTokens = []string{preview.Candidates[0].Token}
	if err := os.WriteFile(romPath, []byte("changed-after-preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/commit", request)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "import_preview_stale") {
		t.Fatalf("changed source was not rejected: %d %s", stale.Code, stale.Body.String())
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil || len(games) != 0 {
		t.Fatalf("stale preview wrote metadata: games=%#v err=%v", games, err)
	}
}

func TestESDERuntimeSourceIsPartOfSignedPreviewContext(t *testing.T) {
	store, handler, root := testServer(t)
	dir := filepath.Join(root, "gamelists", "gba")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "safe.gba"), []byte("safe-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gamelist.xml"), []byte(`<gameList><game><path>./safe.gba</path><name>Safe</name></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"systems-a.xml", "systems-b.xml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(`<systemList><system><name>gba</name><command>`+name+` %ROM%</command></system></systemList>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := importRequest{Format: "es-de", Source: "gamelists/gba/gamelist.xml", RuntimeSource: "systems-a.xml", Platform: "gba", ROMStorage: "reference", MediaStorage: "ignore"}
	var preview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request, &preview)
	if len(preview.Candidates) != 1 || len(preview.Candidates[0].Game.RuntimeHints) != 1 {
		t.Fatalf("preview=%#v", preview)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	request.RuntimeSource = "systems-b.xml"
	stale := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/commit", request)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "import_preview_stale") {
		t.Fatalf("changed runtime source was not rejected: %d %s", stale.Code, stale.Body.String())
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil || len(games) != 0 {
		t.Fatalf("stale ES-DE preview wrote metadata: %#v, %v", games, err)
	}
}

func TestImportTokenCapabilitiesAndOpenAPIContract(t *testing.T) {
	_, handler, _ := testServer(t)
	capabilities := httptest.NewRecorder()
	handler.ServeHTTP(capabilities, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if capabilities.Code != http.StatusOK {
		t.Fatalf("capabilities: %d %s", capabilities.Code, capabilities.Body.String())
	}
	var body struct {
		ContractVersion int             `json:"contract_version"`
		APIVersion      string          `json:"api_version"`
		Imports         []string        `json:"imports"`
		Features        map[string]bool `json:"features"`
	}
	if err := json.Unmarshal(capabilities.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ContractVersion != capabilitiesContractVersion || body.APIVersion != apiVersion {
		t.Fatalf("capability contract identity=%d/%q", body.ContractVersion, body.APIVersion)
	}
	if !maps.Equal(body.Features, capabilityFeatures()) {
		t.Fatalf("capability feature drift: got=%#v want=%#v", body.Features, capabilityFeatures())
	}
	if !body.Features["import_preview_tokens"] || !body.Features["atomic_import_batches"] || !body.Features["neutral_manifest_v5"] || !body.Features["neutral_manifest_v6"] || !body.Features["portable_custom_platforms"] || !body.Features["portable_runtime_catalog_v2"] || !body.Features["portable_builtin_snapshots"] || !body.Features["directory_rom_inventory"] || !body.Features["inventory_match_confirmation"] {
		t.Fatalf("import safety capabilities missing: %#v", body.Features)
	}
	if !slices.Contains(body.Imports, "varkiv-v6") || !slices.Contains(body.Imports, "varkiv-v5-compatible") || !slices.Contains(body.Imports, "varkiv-v4-compatible") {
		t.Fatalf("neutral manifest capability versions missing: %#v", body.Imports)
	}

	spec := httptest.NewRecorder()
	handler.ServeHTTP(spec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))
	contract := spec.Body.String()
	for _, required := range []string{"preview_token:", "selected_tokens:", "content_root:", "source_diagnostics:", "wrapped_archives_detected", "failure_policy:", "const: atomic", "import_preview_stale", "game_merge_stale", "/games/{id}/merge/preview:", "GameMergePreview:", "platform_definition:", "runtime_catalog:", "PortableRuntimeCatalog:", "/runtime-import-hints:", "/runtime-import-hints/{id}/apply:", "RuntimeImportHint:", "/pairing-codes/redeem:", "/save-streams:", "/save-bindings:", "/save-bindings/setup:", "/sync/sessions:", "/sync/inventory-matches:", "/sync/inventory-matches/preview:", "/sync/inventory-matches/commit:", "InventoryMatchReview:", "Idempotency-Key", "SyncOperation:", "SaveRevision:"} {
		if !strings.Contains(contract, required) {
			t.Fatalf("OpenAPI contract is missing %q", required)
		}
	}
	if strings.Contains(contract, "\n              selected:") {
		t.Fatal("OpenAPI still exposes index-based import selection")
	}
	featureStart := strings.Index(contract, "    CapabilityFeatures:\n")
	documentStart := strings.Index(contract, "    CapabilityDocument:\n")
	nextSchema := strings.Index(contract, "    HardwareReadinessGate:\n")
	if featureStart < 0 || documentStart <= featureStart || nextSchema <= documentStart {
		t.Fatal("OpenAPI capability schemas are missing or out of order")
	}
	featureSchema := contract[featureStart:documentStart]
	documentSchema := contract[documentStart:nextSchema]
	if !strings.Contains(featureSchema, "      additionalProperties: false") || !strings.Contains(documentSchema, "      additionalProperties: false") {
		t.Fatal("OpenAPI capability schemas must reject undeclared fields")
	}
	for name := range capabilityFeatures() {
		if !strings.Contains(featureSchema, "        "+name+": {type: boolean}") {
			t.Errorf("OpenAPI capability feature schema is missing %q", name)
		}
	}
	for _, field := range []string{"contract_version: {type: integer, const: 1}", "api_version: {type: string, const: v1}", "features: {$ref: '#/components/schemas/CapabilityFeatures'}"} {
		if !strings.Contains(documentSchema, field) {
			t.Errorf("OpenAPI capability document is missing %q", field)
		}
	}
}

func TestROMImportBatchFailureIsAtomicAndCleansManagedFiles(t *testing.T) {
	store, handler, root := testServer(t)
	romDir := filepath.Join(root, "gba")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Same A.gba", "Same B.gba"} {
		if err := os.WriteFile(filepath.Join(romDir, name), []byte("identical-content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	request := romImportRequest{Source: "gba", Platform: "gba", ROMStorage: "copy"}
	var preview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/preview", request, &preview)
	if len(preview.Candidates) != 2 {
		t.Fatalf("expected two candidates: %#v", preview)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token, preview.Candidates[1].Token}
	failed := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/roms/commit", request)
	if failed.Code != http.StatusConflict || !strings.Contains(failed.Body.String(), "import_batch_conflict") {
		t.Fatalf("atomic batch conflict not reported: %d %s", failed.Code, failed.Body.String())
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil || len(games) != 0 {
		t.Fatalf("failed batch left metadata: games=%#v err=%v", games, err)
	}
	managedPlatform := filepath.Join(root, ".library-data", "roms", "gba")
	entries, err := os.ReadDir(managedPlatform)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed batch left managed ROM directories: %#v", entries)
	}
}

func TestMetadataImportReportsAndSkipsMissingROM(t *testing.T) {
	store, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := "game: Offline Game\nfile: missing.gba\n"
	if err := os.WriteFile(filepath.Join(root, "gba", "metadata.pegasus.txt"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	request := importRequest{Format: "pegasus", Source: "gba/metadata.pegasus.txt", Platform: "gba", ROMStorage: "reference"}
	var preview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request, &preview)
	if len(preview.Candidates) != 1 || preview.Candidates[0].Status != "missing" || preview.Candidates[0].MissingArtifacts != 1 {
		t.Fatalf("missing ROM not reported: %#v", preview)
	}
	request.PreviewToken = preview.PreviewToken
	request.SelectedTokens = []string{preview.Candidates[0].Token}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v1/imports/commit", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing ROM commit should fail: %d %s", recorder.Code, recorder.Body.String())
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil || len(games) != 0 {
		t.Fatalf("missing metadata created catalog records: games=%#v err=%v", games, err)
	}
}

func TestMetadataImportReportsAggregateWrappedSourceDiagnostics(t *testing.T) {
	_, handler, root := testServer(t)
	metadataDir := filepath.Join(root, "metadata", "pokemini")
	contentDir := filepath.Join(root, "downloads", "pokemini")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "metadata.pegasus.txt"), []byte("game: Wrapped source\nfile: missing.min\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"POKE MINI.7z.tkzlm", "POKE MINI.zip.001", "POKE MINI.zip.002", "private-platform.7z.tkzlm", "playable.zip"} {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte("synthetic container marker"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := importRequest{Format: "pegasus", Source: "metadata/pokemini/metadata.pegasus.txt", ContentRoot: "downloads/pokemini", Platform: "pokemini", ROMStorage: "reference", MediaStorage: "ignore"}
	response := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request)
	if response.Code != http.StatusOK {
		t.Fatalf("wrapped source preview: %d %s", response.Code, response.Body.String())
	}
	var preview struct {
		PreviewToken      string                   `json:"preview_token"`
		Candidates        []importCandidate        `json:"candidates"`
		SourceDiagnostics []importSourceDiagnostic `json:"source_diagnostics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].Status != "missing" {
		t.Fatalf("missing candidate changed: %#v", preview.Candidates)
	}
	wantDiagnostics := []importSourceDiagnostic{
		{Code: "wrapped_archives_detected", Count: 2},
		{Code: "split_archives_detected", Count: 2},
		{Code: "platform_wrapped_archives_detected", Count: 1},
		{Code: "platform_split_archive_parts_detected", Count: 2},
	}
	if !slices.Equal(preview.SourceDiagnostics, wantDiagnostics) {
		t.Fatalf("aggregate source diagnostics changed: %#v", preview.SourceDiagnostics)
	}
	responseText := response.Body.String()
	for _, privateValue := range []string{"private-platform", "POKE MINI", root} {
		if strings.Contains(responseText, privateValue) {
			t.Fatalf("source diagnostic leaked private source detail %q: %s", privateValue, responseText)
		}
	}

	var source catalog.LibrarySource
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources", catalog.NewLibrarySource{
		Name: "Wrapped source", Kind: "pegasus", RootPath: "downloads/pokemini", MetadataPath: "metadata/pokemini/metadata.pegasus.txt", Platform: "pokemini", ROMStoragePolicy: "reference", MediaStoragePolicy: "ignore",
	}, &source)
	var scan struct {
		SourceDiagnostics []importSourceDiagnostic `json:"source_diagnostics"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/sources/"+source.ID+"/scans", map[string]any{}, &scan)
	if !slices.Equal(scan.SourceDiagnostics, wantDiagnostics) {
		t.Fatalf("persistent source lost diagnostics: %#v", scan.SourceDiagnostics)
	}
}

func TestManagedImportAndMediaAPI(t *testing.T) {
	store, handler, root := testServer(t)
	if err := os.MkdirAll(filepath.Join(root, "gba", "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "managed.gba"), []byte("managed-rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 520)...)
	if err := os.WriteFile(filepath.Join(root, "gba", "media", "cover.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	gamelist := `<?xml version="1.0"?><gameList><game><path>./managed.gba</path><name>Managed Game</name><image>./media/cover.png</image></game></gameList>`
	if err := os.WriteFile(filepath.Join(root, "gba", "gamelist.xml"), []byte(gamelist), 0o644); err != nil {
		t.Fatal(err)
	}
	request := importRequest{Format: "es-de", Source: "gba/gamelist.xml", Platform: "gba", Locale: "en", ROMStorage: "copy", MediaStorage: "copy"}
	var managedPreview struct {
		PreviewToken string            `json:"preview_token"`
		Candidates   []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request, &managedPreview)
	request.PreviewToken = managedPreview.PreviewToken
	request.SelectedTokens = []string{managedPreview.Candidates[0].Token}
	var committed struct {
		Imported   int `json:"imported"`
		ROMFiles   int `json:"rom_files_copied"`
		MediaFiles int `json:"media_files_copied"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/commit", request, &committed)
	if committed.Imported != 1 || committed.ROMFiles != 1 || committed.MediaFiles != 1 {
		t.Fatalf("bad managed import: %#v", committed)
	}
	request.PreviewToken = ""
	request.SelectedTokens = nil
	var duplicatePreview struct {
		Candidates []importCandidate `json:"candidates"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/imports/preview", request, &duplicatePreview)
	if len(duplicatePreview.Candidates) != 1 || duplicatePreview.Candidates[0].Status != "duplicate" {
		t.Fatalf("managed source duplicate not detected: %#v", duplicatePreview)
	}
	games, err := store.ListGames(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	artifact, importedMedia := games[0].Editions[0].Artifacts[0], games[0].Editions[0].Media[0]
	if artifact.StorageKind != "managed" || artifact.SourcePath != "gba/managed.gba" || importedMedia.StorageKind != "managed" {
		t.Fatalf("storage provenance missing: %#v %#v", artifact, importedMedia)
	}
	if _, err = os.Stat(filepath.Join(root, ".library-data", "roms", filepath.FromSlash(artifact.Path))); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, ".library-data", "media", filepath.FromSlash(importedMedia.Path))); err != nil {
		t.Fatal(err)
	}
	var packed struct {
		Output string `json:"output"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/packages", map[string]string{"name": "managed-media", "frontend": "es-de", "target": "portable", "locale": "en", "file_mode": "copy"}, &packed)
	gamelistPath := filepath.Join(root, ".library-data", "exports", "managed-media", "gamelists", "gba", "gamelist.xml")
	gamelistData, err := os.ReadFile(gamelistPath)
	if err != nil {
		t.Fatal(err)
	}
	start, end := strings.Index(string(gamelistData), "<image>"), strings.Index(string(gamelistData), "</image>")
	if start < 0 || end <= start {
		t.Fatalf("exported media missing from ES-DE metadata: %s", gamelistData)
	}
	imagePath := string(gamelistData)[start+len("<image>") : end]
	if _, err = os.Stat(filepath.Clean(filepath.Join(filepath.Dir(gamelistPath), filepath.FromSlash(imagePath)))); err != nil {
		t.Fatalf("exported media path is broken: %s: %v", imagePath, err)
	}
	mediaRoot := filepath.Join(root, ".library-data", "media")
	countMediaFiles := func() int {
		count := 0
		if walkErr := filepath.WalkDir(mediaRoot, func(_ string, entry os.DirEntry, walkErr error) error {
			if walkErr == nil && !entry.IsDir() {
				count++
			}
			return walkErr
		}); walkErr != nil {
			t.Fatal(walkErr)
		}
		return count
	}
	filesBeforeInvalidUpload := countMediaFiles()
	var invalidBody bytes.Buffer
	invalidWriter := multipart.NewWriter(&invalidBody)
	_ = invalidWriter.WriteField("game_id", "missing-work")
	_ = invalidWriter.WriteField("kind", "cover")
	invalidPart, err := invalidWriter.CreateFormFile("file", "orphan.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = invalidPart.Write(append(png, byte(1))); err != nil {
		t.Fatal(err)
	}
	if err = invalidWriter.Close(); err != nil {
		t.Fatal(err)
	}
	invalidUpload := httptest.NewRequest(http.MethodPost, "/api/v1/media/upload", &invalidBody)
	invalidUpload.Header.Set("Content-Type", invalidWriter.FormDataContentType())
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidUpload)
	if invalidResponse.Code != http.StatusNotFound || countMediaFiles() != filesBeforeInvalidUpload {
		t.Fatalf("invalid owner created an orphan media blob: status=%d before=%d after=%d", invalidResponse.Code, filesBeforeInvalidUpload, countMediaFiles())
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("game_id", games[0].ID)
	_ = writer.WriteField("kind", "cover")
	part, err := writer.CreateFormFile("file", "uploaded.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(png); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/media/upload", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated || uploadResponse.Header().Get("Location") == "" {
		t.Fatalf("media upload: %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var item catalog.MediaAsset
	if err = json.Unmarshal(uploadResponse.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.ContentStatus != "available" || item.ContentCheckedAt == "" {
		t.Fatalf("validated upload did not record media availability: %#v", item)
	}
	content := httptest.NewRecorder()
	handler.ServeHTTP(content, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+item.ID+"/content", nil))
	if content.Code != http.StatusOK || content.Header().Get("ETag") == "" || !bytes.Equal(content.Body.Bytes(), png) {
		t.Fatalf("media content: %d %#v", content.Code, content.Header())
	}
	cached := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+item.ID+"/content", nil)
	cached.Header.Set("If-None-Match", content.Header().Get("ETag"))
	cachedResponse := httptest.NewRecorder()
	handler.ServeHTTP(cachedResponse, cached)
	if cachedResponse.Code != http.StatusNotModified {
		t.Fatalf("media conditional GET: %d", cachedResponse.Code)
	}
	managedPath := filepath.Join(root, ".library-data", "media", filepath.FromSlash(item.Path))
	corrupt := bytes.Repeat([]byte{0x7f}, len(png))
	if err = os.WriteFile(managedPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	corruptRequest := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+item.ID+"/content", nil)
	corruptRequest.Header.Set("If-None-Match", content.Header().Get("ETag"))
	corruptResponse := httptest.NewRecorder()
	handler.ServeHTTP(corruptResponse, corruptRequest)
	var corruptFailure apiErrorEnvelope
	if err = json.Unmarshal(corruptResponse.Body.Bytes(), &corruptFailure); err != nil {
		t.Fatal(err)
	}
	if corruptResponse.Code != http.StatusConflict || corruptFailure.Error.Code != "media_content_integrity_failed" || strings.Contains(corruptResponse.Body.String(), root) || bytes.Contains(corruptResponse.Body.Bytes(), corrupt[:16]) {
		t.Fatalf("corrupt media response leaked or bypassed validation: status=%d body=%s", corruptResponse.Code, corruptResponse.Body.String())
	}
	var corruptRecheck struct {
		Checked int `json:"checked"`
		Changed int `json:"changed"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/media/recheck", map[string]any{}, &corruptRecheck)
	if corruptRecheck.Checked < 2 || corruptRecheck.Changed < 2 {
		t.Fatalf("corrupt shared blob was not reported for every relation: %#v", corruptRecheck)
	}
	var corruptItem catalog.MediaAsset
	jsonRequest(t, handler, http.MethodGet, "/api/v1/media/"+item.ID, nil, &corruptItem)
	if corruptItem.ContentStatus != "changed" || corruptItem.ContentCheckedAt == "" {
		t.Fatalf("corrupt status was not persisted: %#v", corruptItem)
	}
	if err = os.Remove(managedPath); err != nil {
		t.Fatal(err)
	}
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+item.ID+"/content", nil))
	var missingFailure apiErrorEnvelope
	if err = json.Unmarshal(missingResponse.Body.Bytes(), &missingFailure); err != nil {
		t.Fatal(err)
	}
	if missingResponse.Code != http.StatusNotFound || missingFailure.Error.Code != "media_content_unavailable" || strings.Contains(missingResponse.Body.String(), root) {
		t.Fatalf("missing media response = status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
	var missingRecheck struct {
		Checked int `json:"checked"`
		Missing int `json:"missing"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/media/recheck", map[string]any{}, &missingRecheck)
	if missingRecheck.Checked < 2 || missingRecheck.Missing < 2 {
		t.Fatalf("missing shared blob was not reported for every relation: %#v", missingRecheck)
	}
	var missingItem catalog.MediaAsset
	jsonRequest(t, handler, http.MethodGet, "/api/v1/media/"+item.ID, nil, &missingItem)
	if missingItem.ContentStatus != "missing" || missingItem.ContentCheckedAt == "" {
		t.Fatalf("missing status was not persisted: %#v", missingItem)
	}
	if status := jsonRequest(t, handler, http.MethodDelete, "/api/v1/media/"+item.ID, nil, nil); status != http.StatusNoContent {
		t.Fatalf("media delete: %d", status)
	}
}

func TestImportSourceCollectionDirectoryNormalization(t *testing.T) {
	_, handler, root := testServer(t)
	for _, directory := range []string{"FC hack", "SFC-MSU1", "PS2 hack", "FBNEO ACT V"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, directory, "metadata.pegasus.txt"), []byte("collection: "+directory+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var sources []importSource
	jsonRequest(t, handler, http.MethodGet, "/api/import-sources?format=pegasus", nil, &sources)
	want := map[string]string{"FC hack": "nes", "SFC-MSU1": "snes", "PS2 hack": "ps2", "FBNEO ACT V": "arcade"}
	for _, source := range sources {
		directory := filepath.Base(filepath.Dir(filepath.FromSlash(source.Path)))
		if source.Platform != want[directory] {
			t.Fatalf("%s mapped to %q, want %q", directory, source.Platform, want[directory])
		}
	}
}

func TestDeviceSaveUploadAndDownloadAPI(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	w, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: w.ID, DefaultTitle: "Game", EditionType: "original"})
	romHash := strings.Repeat("a", 64)
	if _, err := store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom", Size: 1024, SHA256: romHash}); err != nil {
		t.Fatal(err)
	}
	var device catalog.Device
	jsonRequest(t, handler, http.MethodPost, "/api/devices", catalog.NewDevice{Name: "RG", OSFamily: "linux", Distribution: "rocknix", Architecture: "aarch64"}, &device)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("edition_id", edition.ID)
	_ = writer.WriteField("device_id", device.ID)
	_ = writer.WriteField("driver_id", "retroarch")
	_ = writer.WriteField("relative_path", "saves/game.srm")
	file, _ := writer.CreateFormFile("file", "game.srm")
	_, _ = file.Write([]byte("save-data"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/saves/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", recorder.Code, recorder.Body.String())
	}
	var upload struct {
		Revision catalog.SaveRevision `json:"revision"`
		Created  bool                 `json:"created"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	var revision catalog.SaveRevision
	jsonRequest(t, handler, http.MethodGet, "/api/v1/saves/"+upload.Revision.ID, nil, &revision)
	if revision.ID != upload.Revision.ID {
		t.Fatalf("save revision GET failed: %#v", revision)
	}
	var manifest struct {
		MatchingOrder []string `json:"matching_order"`
		Editions      []struct {
			EditionID      string           `json:"edition_id"`
			SaveNamespace  string           `json:"save_namespace"`
			RevisionCount  int              `json:"revision_count"`
			LatestRevision map[string]any   `json:"latest_revision"`
			Artifacts      []map[string]any `json:"artifacts"`
		} `json:"editions"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/sync/manifest", nil, &manifest)
	if len(manifest.Editions) != 1 || manifest.Editions[0].EditionID != edition.ID || manifest.Editions[0].SaveNamespace != edition.SaveNamespace || manifest.Editions[0].RevisionCount != 1 {
		t.Fatalf("sync manifest did not preserve Edition save identity: %#v", manifest)
	}
	if len(manifest.MatchingOrder) == 0 || manifest.MatchingOrder[0] != "sha256" || len(manifest.Editions[0].Artifacts) != 1 || manifest.Editions[0].Artifacts[0]["sha256"] != romHash {
		t.Fatalf("sync manifest ROM matching evidence missing: %#v", manifest)
	}
	if _, leaked := manifest.Editions[0].LatestRevision["blob_path"]; leaked {
		t.Fatalf("sync manifest leaked server blob path: %#v", manifest.Editions[0].LatestRevision)
	}
	deleteDevice := httptest.NewRecorder()
	handler.ServeHTTP(deleteDevice, httptest.NewRequest(http.MethodDelete, "/api/v1/devices/"+device.ID, nil))
	var deleteFailure apiErrorEnvelope
	if err := json.Unmarshal(deleteDevice.Body.Bytes(), &deleteFailure); err != nil {
		t.Fatal(err)
	}
	if deleteDevice.Code != http.StatusConflict || deleteFailure.Error.Code != "device_has_save_revisions" {
		t.Fatalf("device revision guard failed: %d %#v", deleteDevice.Code, deleteFailure)
	}
	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/api/saves/"+upload.Revision.ID+"/download", nil))
	if download.Code != http.StatusOK || download.Body.String() != "save-data" {
		t.Fatalf("download: %d %q", download.Code, download.Body.String())
	}
}

func TestMultiFileSaveRevisionArchiveIsCompleteAndIntegrityChecked(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Archive fixture", Platform: "ps2"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Archive fixture", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDevice(ctx, catalog.NewDevice{Name: "Archive device", OSFamily: "windows", Architecture: "x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: "builtin-driver-pcsx2", Portability: "driver-dependent"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal([]saveUploadManifestFile{{LogicalPath: "card/Mcd001.ps2", Mode: 0o600}, {LogicalPath: "state/index.json", Mode: 0o600}})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("manifest", string(manifest))
	_ = writer.WriteField("edition_id", edition.ID)
	_ = writer.WriteField("device_id", device.ID)
	for _, fixture := range []struct{ name, content string }{{"Mcd001.ps2", "memory-card"}, {"index.json", "state-index"}} {
		part, createErr := writer.CreateFormFile("files", fixture.name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = io.WriteString(part, fixture.content)
	}
	_ = writer.Close()
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/save-streams/"+stream.ID+"/revisions", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("multi-file upload: %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var pushed saves.PushResult
	if err = json.Unmarshal(uploadResponse.Body.Bytes(), &pushed); err != nil {
		t.Fatal(err)
	}

	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/api/v1/save-revisions/"+pushed.Revision.ID+"/archive", nil))
	if download.Code != http.StatusOK || download.Header().Get("Content-Type") != "application/zip" || download.Header().Get("Cache-Control") != "no-store" || download.Header().Get("ETag") == "" {
		t.Fatalf("archive response: %d %#v %s", download.Code, download.Header(), download.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range archive.File {
		handle, openErr := entry.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(handle)
		_ = handle.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		got[entry.Name] = string(data)
	}
	want := map[string]string{"card/Mcd001.ps2": "memory-card", "state/index.json": "state-index"}
	if !maps.Equal(got, want) {
		t.Fatalf("archive entries = %#v, want %#v", got, want)
	}

	stored, err := store.GetSaveRevision(ctx, pushed.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(stored.Files[0].BlobPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := httptest.NewRecorder()
	handler.ServeHTTP(corrupt, httptest.NewRequest(http.MethodGet, "/api/v1/save-revisions/"+pushed.Revision.ID+"/archive", nil))
	if corrupt.Code != http.StatusInternalServerError || corrupt.Header().Get("Content-Type") == "application/zip" || strings.Contains(corrupt.Body.String(), stored.Files[0].BlobPath) {
		t.Fatalf("corrupt archive was streamed or leaked a path: %d %#v %s", corrupt.Code, corrupt.Header(), corrupt.Body.String())
	}
}

func TestSaveArchivePortablePathContractRejectsUnsafeLegacyMetadata(t *testing.T) {
	for _, logical := range []string{"../private.sav", `C:\private\save.sav`, "C:/private/save.sav", "CON", "bad?.sav"} {
		if _, err := saveArchiveLogicalPaths([]catalog.SaveFile{{ID: "private-file-id", LogicalPath: logical}}); err == nil {
			t.Fatalf("archive path contract accepted %q", logical)
		} else if strings.Contains(err.Error(), logical) {
			t.Fatalf("archive path rejection disclosed %q: %v", logical, err)
		}
	}
	if _, err := saveArchiveLogicalPaths([]catalog.SaveFile{{ID: "a", LogicalPath: "same.sav"}, {ID: "b", LogicalPath: "same.sav"}}); err == nil {
		t.Fatal("archive path contract accepted a duplicate logical path")
	}
}

func TestAutomaticROMMatchDigestRejectsCompressedOuterName(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, path := range []string{"gba/game.gba", "psx/game.m3u", "psx/game.cue"} {
		artifact := &catalog.Artifact{Path: path, SHA256: hash}
		if got := safeAutomaticROMMatchSHA256(artifact); got != hash {
			t.Fatalf("safe launch artifact %q digest = %q", path, got)
		}
	}
	for _, artifact := range []*catalog.Artifact{
		{Path: "gba/private-name.zip", SHA256: hash},
		{Path: "gba/private-name.7Z", SHA256: hash},
		{Path: "gba/game.gba", SHA256: "invalid"},
		{Path: "gba/game.gba", SHA256: hash, Missing: true},
		nil,
	} {
		if got := safeAutomaticROMMatchSHA256(artifact); got != "" {
			t.Fatalf("unsafe or unavailable artifact produced an automatic digest: %#v %q", artifact, got)
		}
	}
}

func TestBearerAuthProtectsAPI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	_ = os.MkdirAll(root, 0o755)
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app, err := New(store, root, WithToken("secret-token"))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/games", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}
	unauthorizedV1 := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorizedV1, httptest.NewRequest(http.MethodGet, "/api/v1/games", nil))
	var failure apiErrorEnvelope
	if err := json.Unmarshal(unauthorizedV1.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if unauthorizedV1.Code != http.StatusUnauthorized || failure.Error.Code != "authentication_required" {
		t.Fatalf("expected structured v1 401, got %d %#v", unauthorizedV1.Code, failure)
	}
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/games", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer secret-token")
	authorized := httptest.NewRecorder()
	app.Handler().ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized request: %d %s", authorized.Code, authorized.Body.String())
	}
	health := httptest.NewRecorder()
	app.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Header().Get("Content-Security-Policy"), "img-src 'self' data: blob:") {
		t.Fatalf("health/security headers missing: %d %#v", health.Code, health.Header())
	}
	for _, path := range []string{"/api/v1/health/ready", "/api/v1/capabilities", "/api/v1/openapi.yaml"} {
		public := httptest.NewRecorder()
		app.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, path, nil))
		if public.Code != http.StatusOK {
			t.Fatalf("public endpoint %s: %d %s", path, public.Code, public.Body.String())
		}
	}
}

func TestPairingClientTokenScopeAndRevocation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app, err := New(store, root, WithStateRoot(state), WithToken("admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	request := func(method, path string, input any, bearer string) *httptest.ResponseRecorder {
		var body io.Reader
		if input != nil {
			data, marshalErr := json.Marshal(input)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			body = bytes.NewReader(data)
		}
		req := httptest.NewRequest(method, path, body)
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	missingProfile := request(http.MethodPost, "/api/v1/pairing-codes", map[string]any{"expires_in_seconds": 120, "requested_device": map[string]any{}}, "admin-secret")
	if missingProfile.Code != http.StatusBadRequest || !strings.Contains(missingProfile.Body.String(), `"code":"pairing_device_profile_required"`) {
		t.Fatalf("pairing code accepted no profile: %d %s", missingProfile.Code, missingProfile.Body.String())
	}
	unavailableProfile := request(http.MethodPost, "/api/v1/pairing-codes", map[string]any{"expires_in_seconds": 120, "requested_device": map[string]any{"device_profile_id": "missing-profile"}}, "admin-secret")
	if unavailableProfile.Code != http.StatusConflict || !strings.Contains(unavailableProfile.Body.String(), `"code":"pairing_device_profile_unavailable"`) {
		t.Fatalf("pairing code accepted an unavailable profile: %d %s", unavailableProfile.Code, unavailableProfile.Body.String())
	}
	disabled := false
	if _, err = store.CreateDeviceProfile(context.Background(), catalog.NewDeviceProfile{ID: "disabled-pair-profile", Name: "Disabled", Target: "fixture", OSFamily: "linux", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	disabledProfile := request(http.MethodPost, "/api/v1/pairing-codes", map[string]any{"expires_in_seconds": 120, "requested_device": map[string]any{"device_profile_id": "disabled-pair-profile"}}, "admin-secret")
	if disabledProfile.Code != http.StatusConflict || !strings.Contains(disabledProfile.Body.String(), `"code":"pairing_device_profile_unavailable"`) {
		t.Fatalf("pairing code accepted a disabled profile: %d %s", disabledProfile.Code, disabledProfile.Body.String())
	}

	created := request(http.MethodPost, "/api/v1/pairing-codes", map[string]any{
		"expires_in_seconds": 120,
		"requested_device":   map[string]any{"name": "Living room handheld", "device_profile_id": "builtin-device-rocknix"},
	}, "admin-secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("create pairing code: %d %s", created.Code, created.Body.String())
	}
	var codeBody struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &codeBody); err != nil {
		t.Fatal(err)
	}
	if len(codeBody.Code) != 11 || !strings.Contains(codeBody.Code, "-") {
		t.Fatalf("unexpected display code: %q", codeBody.Code)
	}
	storedCode, err := store.GetPairingCode(context.Background(), codeBody.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedCode.CodeHash == "" || strings.Contains(storedCode.CodeHash, strings.ReplaceAll(codeBody.Code, "-", "")) {
		t.Fatal("pairing code was not stored as a one-way hash")
	}
	if len(storedCode.RequestedDevice) != 1 || storedCode.RequestedDevice["device_profile_id"] != "builtin-device-rocknix" {
		t.Fatalf("pairing request was not canonicalized: %#v", storedCode.RequestedDevice)
	}
	wrongProfile := request(http.MethodPost, "/api/v1/pairing-codes/redeem", map[string]any{
		"code": codeBody.Code,
		"device": map[string]any{
			"name": "Wrong profile", "device_profile_id": "builtin-device-windows-handheld", "os_family": "windows",
		},
	}, "")
	if wrongProfile.Code != http.StatusConflict || !strings.Contains(wrongProfile.Body.String(), `"code":"pairing_device_profile_mismatch"`) {
		t.Fatalf("pairing profile selection was not enforced: %d %s", wrongProfile.Code, wrongProfile.Body.String())
	}
	incompatibleDevice := request(http.MethodPost, "/api/v1/pairing-codes/redeem", map[string]any{
		"code": codeBody.Code,
		"device": map[string]any{
			"name": "Wrong operating system", "device_profile_id": "builtin-device-rocknix", "os_family": "android", "architecture": "arm64-v8a",
		},
	}, "")
	if incompatibleDevice.Code != http.StatusConflict || !strings.Contains(incompatibleDevice.Body.String(), `"code":"pairing_device_profile_incompatible"`) {
		t.Fatalf("pairing profile compatibility was not enforced: %d %s", incompatibleDevice.Code, incompatibleDevice.Body.String())
	}

	redeemed := request(http.MethodPost, "/api/v1/pairing-codes/redeem", map[string]any{
		"code": codeBody.Code,
		"device": map[string]any{
			"name": "Living room handheld", "os_family": "linux", "distribution": "rocknix", "architecture": "arm64", "agent_version": "0.1.0",
		},
	}, "")
	if redeemed.Code != http.StatusCreated {
		t.Fatalf("redeem pairing code: %d %s", redeemed.Code, redeemed.Body.String())
	}
	var tokenBody struct {
		Device       catalog.Device `json:"device"`
		DeviceTarget string         `json:"device_target"`
		AccessToken  string         `json:"access_token"`
	}
	if err = json.Unmarshal(redeemed.Body.Bytes(), &tokenBody); err != nil {
		t.Fatal(err)
	}
	if len(tokenBody.AccessToken) != 64 || tokenBody.Device.ID == "" || tokenBody.Device.DeviceProfileID != "builtin-device-rocknix" || tokenBody.DeviceTarget != "rocknix" {
		t.Fatalf("invalid token response: %#v", tokenBody)
	}
	if _, err = store.AuthenticateClientToken(context.Background(), tokenBody.AccessToken); !errors.Is(err, catalog.ErrClientTokenInvalid) {
		t.Fatal("plaintext token unexpectedly authenticates as a stored hash")
	}
	if _, err = store.AuthenticateClientToken(context.Background(), hashClientSecret(tokenBody.AccessToken)); err != nil {
		t.Fatalf("hashed token did not authenticate: %v", err)
	}
	identityChange := request(http.MethodPut, "/api/v1/devices/"+tokenBody.Device.ID, map[string]any{
		"name": "Living room handheld", "device_profile_id": "builtin-device-windows-handheld", "os_family": "windows", "distribution": "windows", "architecture": "x86_64", "status": "active", "capabilities": map[string]bool{},
	}, "admin-secret")
	if identityChange.Code != http.StatusConflict || !strings.Contains(identityChange.Body.String(), `"code":"paired_device_identity_in_use"`) {
		t.Fatalf("active paired identity was mutable: %d %s", identityChange.Code, identityChange.Body.String())
	}
	genericRevoke := request(http.MethodPut, "/api/v1/devices/"+tokenBody.Device.ID, map[string]any{
		"name": tokenBody.Device.Name, "device_profile_id": tokenBody.Device.DeviceProfileID, "os_family": tokenBody.Device.OSFamily, "distribution": tokenBody.Device.Distribution, "architecture": tokenBody.Device.Architecture, "status": "revoked", "capabilities": tokenBody.Device.Capabilities,
	}, "admin-secret")
	if genericRevoke.Code != http.StatusConflict || !strings.Contains(genericRevoke.Body.String(), `"code":"device_revocation_required"`) {
		t.Fatalf("generic update bypassed atomic revoke: %d %s", genericRevoke.Code, genericRevoke.Body.String())
	}

	reused := request(http.MethodPost, "/api/v1/pairing-codes/redeem", map[string]any{
		"code":   codeBody.Code,
		"device": map[string]any{"name": "Duplicate", "os_family": "android"},
	}, "")
	if reused.Code != http.StatusConflict {
		t.Fatalf("reused pairing code: %d %s", reused.Code, reused.Body.String())
	}

	manifest := request(http.MethodGet, "/api/v1/sync/manifest", nil, tokenBody.AccessToken)
	if manifest.Code != http.StatusForbidden {
		t.Fatalf("client token reached the owner-only legacy library manifest: %d %s", manifest.Code, manifest.Body.String())
	}
	deviceConfig := request(http.MethodGet, "/api/v1/sync/config", nil, tokenBody.AccessToken)
	if deviceConfig.Code != http.StatusOK || strings.Contains(deviceConfig.Body.String(), "Foreign save") {
		t.Fatalf("client did not receive its scoped sync config: %d %s", deviceConfig.Code, deviceConfig.Body.String())
	}
	games := request(http.MethodGet, "/api/v1/games", nil, tokenBody.AccessToken)
	if games.Code != http.StatusForbidden {
		t.Fatalf("client escaped scope: %d %s", games.Code, games.Body.String())
	}
	ctx := context.Background()
	boundGame, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Bound save", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	boundEdition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: boundGame.ID, DefaultTitle: "Bound save", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	boundStream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "edition", OwnerKey: boundEdition.ID, DriverID: "builtin-driver-retroarch", Portability: "core-dependent"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err = store.CreateSaveBinding(ctx, catalog.NewSaveBinding{StreamID: boundStream.ID, EditionID: boundEdition.ID, DeviceProfileID: "builtin-device-rocknix", DriverID: "builtin-driver-retroarch", LocalPaths: []string{"saves/primary.srm"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	boundRevision, err := app.saves.PushSet(ctx, saves.PushSetInput{EditionID: boundEdition.ID, DeviceID: tokenBody.Device.ID, DriverID: "builtin-driver-retroarch", Files: []saves.IncomingFile{{LogicalPath: "primary.srm", Reader: strings.NewReader("bound")}}})
	if err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, "/api/v1/save-revisions/"+boundRevision.Revision.ID, nil, tokenBody.AccessToken); response.Code != http.StatusOK {
		t.Fatalf("device could not read its bound negotiated revision: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/v1/save-revisions", "/api/v1/save-revisions/" + boundRevision.Revision.ID + "/archive", "/api/v1/save-revisions/" + boundRevision.Revision.ID + "/files/" + boundRevision.Revision.Files[0].ID + "/content", "/api/v1/save-streams", "/api/v1/saves"} {
		if response := request(http.MethodGet, path, nil, tokenBody.AccessToken); response.Code != http.StatusForbidden {
			t.Fatalf("device token reached owner-only save route %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	foreignGame, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Foreign save", Platform: "ps2"})
	if err != nil {
		t.Fatal(err)
	}
	foreignEdition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: foreignGame.ID, DefaultTitle: "Foreign save", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	foreignStream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "edition", OwnerKey: foreignEdition.ID, DriverID: "builtin-driver-pcsx2", Portability: "driver-dependent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateSaveBinding(ctx, catalog.NewSaveBinding{StreamID: foreignStream.ID, EditionID: foreignEdition.ID, DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-pcsx2", LocalPaths: []string{"cards/Mcd001.ps2"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	foreignRevision, err := app.saves.PushSet(ctx, saves.PushSetInput{EditionID: foreignEdition.ID, DeviceID: tokenBody.Device.ID, DriverID: "builtin-driver-pcsx2", Files: []saves.IncomingFile{{LogicalPath: "Mcd001.ps2", Reader: strings.NewReader("foreign")}}})
	if err != nil {
		t.Fatal(err)
	}
	foreignResponse := request(http.MethodGet, "/api/v1/save-revisions/"+foreignRevision.Revision.ID, nil, tokenBody.AccessToken)
	if foreignResponse.Code != http.StatusForbidden || !strings.Contains(foreignResponse.Body.String(), "save_revision_not_bound") {
		t.Fatalf("device read a revision outside its profile bindings: %d %s", foreignResponse.Code, foreignResponse.Body.String())
	}
	if _, err = store.CreateSaveBinding(ctx, catalog.NewSaveBinding{StreamID: boundStream.ID, EditionID: boundEdition.ID, DriverID: "builtin-driver-retroarch", LocalPaths: []string{"saves/primary.srm"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	noProfile, err := store.CreateDevice(ctx, catalog.NewDevice{Name: "No profile", OSFamily: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	noProfileConfig := request(http.MethodGet, "/api/v1/sync/config?device_id="+noProfile.ID, nil, "admin-secret")
	if noProfileConfig.Code != http.StatusOK {
		t.Fatalf("no-profile sync config: %d %s", noProfileConfig.Code, noProfileConfig.Body.String())
	}
	var noProfileBody struct {
		Bindings []syncBindingDescriptor `json:"bindings"`
	}
	if err = json.Unmarshal(noProfileConfig.Body.Bytes(), &noProfileBody); err != nil {
		t.Fatal(err)
	}
	if len(noProfileBody.Bindings) != 1 || noProfileBody.Bindings[0].Binding.DeviceProfileID != "" || noProfileBody.Bindings[0].Stream.ID != boundStream.ID {
		t.Fatalf("device without a profile received profile-specific save bindings: %#v", noProfileBody.Bindings)
	}
	heartbeat := request(http.MethodPost, "/api/v1/devices/"+tokenBody.Device.ID+"/heartbeat", map[string]any{"capabilities": map[string]bool{"runtime_probe": true, "runtime_file_grants_configured": true, "emulator_installed": true, "untrusted_key": true}}, tokenBody.AccessToken)
	if heartbeat.Code != http.StatusOK {
		t.Fatalf("own heartbeat: %d %s", heartbeat.Code, heartbeat.Body.String())
	}
	var heartbeatDevice catalog.Device
	if err = json.Unmarshal(heartbeat.Body.Bytes(), &heartbeatDevice); err != nil {
		t.Fatal(err)
	}
	if !heartbeatDevice.Capabilities["runtime_probe"] || !heartbeatDevice.Capabilities["runtime_file_grants_configured"] || !heartbeatDevice.Capabilities["emulator_installed"] || heartbeatDevice.Capabilities["untrusted_key"] {
		t.Fatalf("heartbeat capability allowlist failed: %#v", heartbeatDevice.Capabilities)
	}
	foreignHeartbeat := request(http.MethodPost, "/api/v1/devices/not-this-device/heartbeat", map[string]any{}, tokenBody.AccessToken)
	if foreignHeartbeat.Code != http.StatusForbidden {
		t.Fatalf("foreign heartbeat: %d %s", foreignHeartbeat.Code, foreignHeartbeat.Body.String())
	}
	revoked := request(http.MethodPost, "/api/v1/devices/"+tokenBody.Device.ID+"/revoke", map[string]any{}, "admin-secret")
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", revoked.Code, revoked.Body.String())
	}
	reactivate := request(http.MethodPut, "/api/v1/devices/"+tokenBody.Device.ID, map[string]any{
		"name": tokenBody.Device.Name, "device_profile_id": tokenBody.Device.DeviceProfileID, "os_family": tokenBody.Device.OSFamily, "distribution": tokenBody.Device.Distribution, "architecture": tokenBody.Device.Architecture, "status": "active", "capabilities": tokenBody.Device.Capabilities,
	}, "admin-secret")
	if reactivate.Code != http.StatusForbidden || !strings.Contains(reactivate.Body.String(), `"code":"device_revoked"`) {
		t.Fatalf("revoked device was reactivated: %d %s", reactivate.Code, reactivate.Body.String())
	}
	afterRevoke := request(http.MethodGet, "/api/v1/sync/config", nil, tokenBody.AccessToken)
	if afterRevoke.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token remained active: %d %s", afterRevoke.Code, afterRevoke.Body.String())
	}
}

func TestSyncNegotiationIdempotencyAmbiguityAndConflict(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app, err := New(store, root, WithStateRoot(state), WithToken("admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gameA, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "A", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	editionA, err := store.AddEdition(ctx, catalog.NewEdition{GameID: gameA.ID, DefaultTitle: "A", EditionType: "original", Serial: "SERIAL-A", ProductCode: "ULUS-00000", TitleID: "0001000141424344"})
	if err != nil {
		t.Fatal(err)
	}
	gameB, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "B", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	editionB, err := store.AddEdition(ctx, catalog.NewEdition{GameID: gameB.ID, DefaultTitle: "B", EditionType: "translation", Serial: "SERIAL-A"})
	if err != nil {
		t.Fatal(err)
	}
	for index, edition := range []catalog.Edition{editionA, editionB} {
		artifactHash := strings.Repeat(string(rune('a'+index)), 64)
		if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game-" + string(rune('a'+index)) + ".gba", SHA256: artifactHash, Size: 4}); err != nil {
			t.Fatal(err)
		}
	}
	device, err := store.CreateDevice(ctx, catalog.NewDevice{Name: "ROCKNIX", DeviceProfileID: "builtin-device-rocknix", OSFamily: "linux", Distribution: "rocknix", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "edition", OwnerKey: editionA.ID, DriverID: "builtin-driver-retroarch", Portability: "core-dependent"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	binding := catalog.NewSaveBinding{StreamID: stream.ID, EditionID: editionA.ID, DeviceProfileID: "builtin-device-rocknix", DriverID: "builtin-driver-retroarch", LocalPaths: []string{"{{device.save_dir}}/{{edition.save_namespace}}.srm"}, Discovery: map[string]any{"refresh": "process-exit"}, Enabled: &enabled}
	createdBinding, err := store.CreateSaveBinding(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if createdBinding.LocalPaths[0] != binding.LocalPaths[0] {
		t.Fatalf("save binding path changed: %#v", createdBinding)
	}
	if _, err = store.CreateLaunchBinding(ctx, catalog.NewLaunchBinding{
		EditionID: editionA.ID, DeviceProfileID: "builtin-device-rocknix", DriverID: "builtin-driver-retroarch",
	}); err != nil {
		t.Fatal(err)
	}
	configRequest := httptest.NewRequest(http.MethodGet, "/api/v1/sync/config?device_id="+device.ID, nil)
	configRequest.Header.Set("Authorization", "Bearer admin-secret")
	configResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(configResponse, configRequest)
	if configResponse.Code != http.StatusOK {
		t.Fatalf("sync config: %d %s", configResponse.Code, configResponse.Body.String())
	}
	var mobileConfig struct {
		Bindings  []syncBindingDescriptor       `json:"bindings"`
		Launches  []runtimecfg.LaunchResolution `json:"launches"`
		Platforms []platforms.Platform          `json:"platforms"`
	}
	if err = json.Unmarshal(configResponse.Body.Bytes(), &mobileConfig); err != nil {
		t.Fatal(err)
	}
	if len(mobileConfig.Bindings) != 1 || len(mobileConfig.Launches) != 1 || len(mobileConfig.Platforms) < 40 {
		t.Fatalf("mobile sync config omitted bindings, launches, or platform extensions: bindings=%d launches=%d platforms=%d", len(mobileConfig.Bindings), len(mobileConfig.Launches), len(mobileConfig.Platforms))
	}
	var mobileGBA platforms.Platform
	for _, item := range mobileConfig.Platforms {
		if item.ID == "gba" {
			mobileGBA = item
			break
		}
	}
	if mobileGBA.Name != "Nintendo Game Boy Advance" || mobileGBA.NameZH != "Game Boy Advance" || len(mobileGBA.Extensions) == 0 {
		t.Fatalf("mobile sync config omitted readable platform identity: %#v", mobileGBA)
	}
	if mobileConfig.Bindings[0].ROMMatchSHA256 != strings.Repeat("a", 64) || mobileConfig.Bindings[0].ROMStem != "" {
		t.Fatalf("sync config must provide the launch digest without guessing a device-local basename: %#v", mobileConfig.Bindings[0])
	}
	if mobileConfig.Bindings[0].ProductCode != "ULUS-00000" || mobileConfig.Bindings[0].TitleIDHigh != "00010001" || mobileConfig.Bindings[0].TitleIDLow != "41424344" {
		t.Fatalf("sync config omitted public standalone-emulator identity: %#v", mobileConfig.Bindings[0])
	}
	first, err := app.saves.PushSet(ctx, saves.PushSetInput{EditionID: editionA.ID, DeviceID: device.ID, DriverID: "builtin-driver-retroarch", Files: []saves.IncomingFile{{LogicalPath: "game.srm", Reader: strings.NewReader("first")}}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(key string, input syncSessionRequest) *httptest.ResponseRecorder {
		data, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/sessions", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer admin-secret")
		req.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, req)
		return recorder
	}
	uploadOperation := func(sessionID, operationID, content string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		manifest, marshalErr := json.Marshal(syncUploadManifest{EditionID: editionA.ID, Files: []saveUploadManifestFile{{LogicalPath: "game.srm"}}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		field, fieldErr := writer.CreateFormField("manifest")
		if fieldErr != nil {
			t.Fatal(fieldErr)
		}
		_, _ = field.Write(manifest)
		filePart, fileErr := writer.CreateFormFile("files", "ignored-client-name.srm")
		if fileErr != nil {
			t.Fatal(fileErr)
		}
		_, _ = filePart.Write([]byte(content))
		if closeErr := writer.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/sessions/"+sessionID+"/operations/"+operationID+"/upload", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer admin-secret")
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, req)
		return recorder
	}
	clientContent := "client save"
	localHash := testSaveSetHash("game.srm", clientContent)
	request := syncSessionRequest{
		DeviceID:  device.ID,
		Inventory: []syncInventoryInput{{ClientItemID: "slot-1", PlatformID: "gba", Serial: "SERIAL-A", Size: 4}},
		Saves:     []syncSaveStateInput{{StreamID: stream.ID, BaseRevisionID: first.Revision.ID, ContentHash: localHash, HasLocalData: true}},
	}
	duplicateStreamRequest := request
	duplicateStreamRequest.Saves = []syncSaveStateInput{
		{StreamID: stream.ID, BaseRevisionID: first.Revision.ID, ContentHash: localHash, HasLocalData: true},
		{StreamID: stream.ID, BaseRevisionID: first.Revision.ID, ContentHash: localHash, HasLocalData: true},
	}
	duplicateStream := call("sync-key-duplicate-stream", duplicateStreamRequest)
	if duplicateStream.Code != http.StatusBadRequest || !strings.Contains(duplicateStream.Body.String(), "stream_id is required and must be unique") {
		t.Fatalf("duplicate stream state was not rejected: %d %s", duplicateStream.Code, duplicateStream.Body.String())
	}
	sessionsAfterDuplicate, err := store.ListSyncSessions(ctx, device.ID)
	if err != nil || len(sessionsAfterDuplicate) != 0 {
		t.Fatalf("duplicate stream request left sessions=%#v err=%v", sessionsAfterDuplicate, err)
	}
	badRequest := request
	badRequest.Saves = []syncSaveStateInput{{StreamID: stream.ID, BaseRevisionID: first.Revision.ID, ContentHash: strings.Repeat("c", 64), HasLocalData: true}}
	badPlan := call("sync-key-bad-hash", badRequest)
	if badPlan.Code != http.StatusCreated {
		t.Fatalf("bad hash plan: %d %s", badPlan.Code, badPlan.Body.String())
	}
	var badResponse syncSessionResponse
	if err = json.Unmarshal(badPlan.Body.Bytes(), &badResponse); err != nil {
		t.Fatal(err)
	}
	beforeMismatch, err := store.ListStreamRevisions(ctx, stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	badUpload := uploadOperation(badResponse.Session.ID, badResponse.Session.Operations[0].ID, clientContent)
	if badUpload.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hash mismatch upload: %d %s", badUpload.Code, badUpload.Body.String())
	}
	afterMismatch, err := store.ListStreamRevisions(ctx, stream.ID)
	if err != nil || len(afterMismatch) != len(beforeMismatch) {
		t.Fatalf("hash mismatch created a revision: before=%d after=%d err=%v", len(beforeMismatch), len(afterMismatch), err)
	}
	if entries, readErr := os.ReadDir(filepath.Join(state, "staging")); readErr != nil || len(entries) != 0 {
		t.Fatalf("hash mismatch left staging data: entries=%d err=%v", len(entries), readErr)
	}

	created := call("sync-key-0001", request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", created.Code, created.Body.String())
	}
	var response syncSessionResponse
	if err = json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Inventory) != 1 || response.Inventory[0].MatchStatus != "ambiguous" || response.Inventory[0].MatchedEditionID != "" || response.Inventory[0].MatchMethod != "serial" {
		t.Fatalf("ambiguous ROM was silently matched: %#v", response.Inventory)
	}
	if len(response.Session.Operations) != 1 || response.Session.Operations[0].Action != "upload" || response.Session.Status != "transferring" {
		t.Fatalf("unexpected upload plan: %#v", response.Session)
	}
	replay := call("sync-key-0001", request)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("idempotent replay: %d %#v %s", replay.Code, replay.Header(), replay.Body.String())
	}
	changed := request
	changed.Saves = []syncSaveStateInput{{StreamID: stream.ID, BaseRevisionID: first.Revision.ID, ContentHash: strings.Repeat("d", 64), HasLocalData: true}}
	mismatch := call("sync-key-0001", changed)
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("idempotency mismatch: %d %s", mismatch.Code, mismatch.Body.String())
	}
	uploaded := uploadOperation(response.Session.ID, response.Session.Operations[0].ID, clientContent)
	if uploaded.Code != http.StatusOK {
		t.Fatalf("negotiated upload: %d %s", uploaded.Code, uploaded.Body.String())
	}
	var uploadResponse struct {
		Session  catalog.SyncSession  `json:"session"`
		Revision catalog.SaveRevision `json:"revision"`
	}
	if err = json.Unmarshal(uploaded.Body.Bytes(), &uploadResponse); err != nil {
		t.Fatal(err)
	}
	if uploadResponse.Session.Status != "complete" || uploadResponse.Revision.ContentHash != localHash || uploadResponse.Revision.Status != "current" {
		t.Fatalf("upload did not commit atomically: %#v", uploadResponse)
	}

	second, err := app.saves.PushSet(ctx, saves.PushSetInput{EditionID: editionA.ID, DeviceID: device.ID, DriverID: "builtin-driver-retroarch", BaseRevisionID: uploadResponse.Revision.ID, Files: []saves.IncomingFile{{LogicalPath: "game.srm", Reader: strings.NewReader("server advanced")}}})
	if err != nil || !second.Created || second.Conflict {
		t.Fatalf("server advance: %#v %v", second, err)
	}
	conflict := call("sync-key-0002", request)
	if conflict.Code != http.StatusCreated {
		t.Fatalf("conflict session: %d %s", conflict.Code, conflict.Body.String())
	}
	if err = json.Unmarshal(conflict.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Session.Status != "complete" || response.Session.ConflictCount != 1 || response.Session.Operations[0].Action != "conflict" {
		t.Fatalf("stale baseline did not produce explicit conflict: %#v", response.Session)
	}

	download := call("sync-key-0003", syncSessionRequest{DeviceID: device.ID, Saves: []syncSaveStateInput{{StreamID: stream.ID, HasLocalData: false}}})
	if download.Code != http.StatusCreated {
		t.Fatalf("download session: %d %s", download.Code, download.Body.String())
	}
	if err = json.Unmarshal(download.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Session.Operations[0].Action != "download" || response.Session.Operations[0].TargetRevisionID != second.Revision.ID {
		t.Fatalf("download plan did not target current revision: %#v", response.Session)
	}
	downloadFile := httptest.NewRecorder()
	downloadRequest := httptest.NewRequest(http.MethodGet, "/api/v1/sync/sessions/"+response.Session.ID+"/operations/"+response.Session.Operations[0].ID+"/files/"+second.Revision.Files[0].ID+"/content", nil)
	downloadRequest.Header.Set("Authorization", "Bearer admin-secret")
	app.Handler().ServeHTTP(downloadFile, downloadRequest)
	if downloadFile.Code != http.StatusOK || downloadFile.Body.String() != "server advanced" {
		t.Fatalf("operation download: %d %q", downloadFile.Code, downloadFile.Body.String())
	}
	ackBody, _ := json.Marshal(map[string]string{"actual_hash": second.Revision.ContentHash})
	ackRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sync/sessions/"+response.Session.ID+"/operations/"+response.Session.Operations[0].ID+"/ack", bytes.NewReader(ackBody))
	ackRequest.Header.Set("Content-Type", "application/json")
	ackRequest.Header.Set("Authorization", "Bearer admin-secret")
	ack := httptest.NewRecorder()
	app.Handler().ServeHTTP(ack, ackRequest)
	if ack.Code != http.StatusOK || !strings.Contains(ack.Body.String(), `"status":"complete"`) {
		t.Fatalf("download ack: %d %s", ack.Code, ack.Body.String())
	}

}

func TestMutableStateCanLiveOutsideReadOnlyLibrary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(root, "gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gba", "game.gba"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	game, err := store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(context.Background(), catalog.NewEdition{GameID: game.ID, DefaultTitle: "Game", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(context.Background(), catalog.NewArtifact{EditionID: edition.ID, Path: "gba/game.gba", Role: "rom"}); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0o755)
	app, err := New(store, root, WithStateRoot(state))
	if err != nil {
		t.Fatal(err)
	}
	var packed struct {
		Output string `json:"output"`
	}
	jsonRequest(t, app.Handler(), http.MethodPost, "/api/packages", map[string]string{"name": "readonly-test", "frontend": "es-de", "target": "rocknix", "locale": "en", "file_mode": "copy"}, &packed)
	if packed.Output != "state/exports/readonly-test" {
		t.Fatalf("package exposed an absolute state path: %q", packed.Output)
	}
	if _, err = os.Stat(filepath.Join(root, ".library-data")); !os.IsNotExist(err) {
		t.Fatalf("server wrote mutable state into library: %v", err)
	}
}
