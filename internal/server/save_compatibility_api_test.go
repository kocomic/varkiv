package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestExactRuntimeHeartbeatControlsCrossDriverBindingAndSession(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	groups, err := store.ListSaveCompatibilityGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].ID != snesRawSRMCompatibilityGroupID || len(groups[0].Members) != 2 {
		t.Fatalf("compatibility groups=%#v err=%v", groups, err)
	}
	profile, err := store.GetDeviceProfile(ctx, "builtin-device-rocknix")
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDevice(ctx, catalog.NewDevice{ID: "runtime-attested-device", Name: "Runtime Fixture", DeviceProfileID: profile.ID, OSFamily: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "SNES Fixture", Platform: "snes"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := store.CreateSaveStream(ctx, catalog.NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: "builtin-driver-emulatorjs-snes9x", Portability: "core-dependent", CompatibilityGroupID: snesRawSRMCompatibilityGroupID, EditionIDs: []string{edition.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateSaveBinding(ctx, catalog.NewSaveBinding{StreamID: stream.ID, EditionID: edition.ID, DeviceProfileID: profile.ID, DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-snes9x", LocalPaths: []string{"{{device.save_dir}}/{{rom.stem}}.srm"}})
	if err != nil {
		t.Fatal(err)
	}

	config := func() map[string]any {
		var response map[string]any
		jsonRequest(t, handler, http.MethodGet, "/api/v1/sync/config?device_id="+device.ID, nil, &response)
		return response
	}
	if bindings, _ := config()["bindings"].([]any); len(bindings) != 0 {
		t.Fatalf("unattested binding was delivered: %#v", bindings)
	}

	session := func(key string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"device_id": device.ID, "inventory": []any{}, "saves": []map[string]any{{"stream_id": stream.ID, "has_local_data": false}}})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/sync/sessions", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	denied := session("runtime-not-attested")
	if denied.Code != http.StatusConflict || !strings.Contains(denied.Body.String(), "save_runtime_not_attested") {
		t.Fatalf("unattested session status=%d body=%s", denied.Code, denied.Body.String())
	}
	if sessions, err := store.ListSyncSessions(ctx, device.ID); err != nil || len(sessions) != 0 {
		t.Fatalf("denied session persisted: %#v err=%v", sessions, err)
	}

	heartbeat := map[string]any{"capabilities": map[string]bool{"runtime_probe": true}, "runtime_attestations": []catalog.RuntimeAttestationReport{
		{Kind: "driver", RuntimeID: "builtin-driver-retroarch", ContractVersion: 10, SHA256: "484621fe4675e3cf9a0d47ec9f63d611540dfe98db0d7799d9c8d14e5881b080", Size: 14705288},
		{Kind: "core", RuntimeID: "builtin-core-snes9x", ContractVersion: 3, SHA256: "52a3ceadeb4798cc323094c614eff20456fad7cf2287a5add8a475c677c3939b", Size: 2436288},
	}}
	var updated catalog.Device
	jsonRequest(t, handler, http.MethodPost, "/api/v1/devices/"+device.ID+"/heartbeat", heartbeat, &updated)
	if !updated.Capabilities["runtime_identity_attested"] || !updated.Capabilities["verified_save_bridge"] {
		t.Fatalf("heartbeat did not expose verified state: %#v", updated.Capabilities)
	}
	configured := config()
	bindings, _ := configured["bindings"].([]any)
	requirements, _ := configured["runtime_attestation_requirements"].([]any)
	if len(bindings) != 1 || len(requirements) != 2 {
		t.Fatalf("authorized config bindings=%#v requirements=%#v", bindings, requirements)
	}
	accepted := session("runtime-exact")
	if accepted.Code != http.StatusCreated {
		t.Fatalf("attested session status=%d body=%s", accepted.Code, accepted.Body.String())
	}

	jsonRequest(t, handler, http.MethodPost, "/api/v1/devices/"+device.ID+"/heartbeat", map[string]any{"capabilities": map[string]bool{"runtime_probe": true}, "runtime_attestations": []any{}}, &updated)
	if updated.Capabilities["verified_save_bridge"] {
		t.Fatalf("empty snapshot retained verified bridge: %#v", updated.Capabilities)
	}
	if bindings, _ = config()["bindings"].([]any); len(bindings) != 0 {
		t.Fatalf("revoked binding was still delivered: %#v", bindings)
	}
}

func TestSaveCompatibilityCollectionsDoNotExposeHostPaths(t *testing.T) {
	_, handler, _ := testServer(t)
	for _, path := range []string{"/api/v1/save-compatibility-groups", "/api/v1/runtime-attestations"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "/"+"Users/") || strings.Contains(response.Body.String(), "\\Users\\") {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSyncConfigAndHeartbeatNeverCrossRuntimeRequirementsBetweenPlatforms(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	profile, err := store.GetDeviceProfile(ctx, "builtin-device-android-handheld")
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDevice(ctx, catalog.NewDevice{ID: "android-runtime-scope", Name: "Android", DeviceProfileID: profile.ID, OSFamily: "android", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	jsonRequest(t, handler, http.MethodGet, "/api/v1/sync/config?device_id="+device.ID, nil, &config)
	if requirements, _ := config["runtime_attestation_requirements"].([]any); len(requirements) != 0 {
		t.Fatalf("android config exposed linux runtime identities: %#v", requirements)
	}
	body, _ := json.Marshal(map[string]any{"capabilities": map[string]bool{"runtime_probe": true, "runtime_file_grants_configured": true}, "runtime_attestations": []catalog.RuntimeAttestationReport{
		{Kind: "driver", RuntimeID: "builtin-driver-retroarch", ContractVersion: 10, SHA256: "484621fe4675e3cf9a0d47ec9f63d611540dfe98db0d7799d9c8d14e5881b080", Size: 14705288},
		{Kind: "core", RuntimeID: "builtin-core-snes9x", ContractVersion: 3, SHA256: "52a3ceadeb4798cc323094c614eff20456fad7cf2287a5add8a475c677c3939b", Size: 2436288},
	}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+device.ID+"/heartbeat", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "runtime_attestation_not_requested") {
		t.Fatalf("android heartbeat accepted linux identity status=%d body=%s", response.Code, response.Body.String())
	}
	if items, listErr := store.ListRuntimeAttestations(ctx, device.ID); listErr != nil || len(items) != 0 {
		t.Fatalf("rejected heartbeat persisted identities: %#v err=%v", items, listErr)
	}
}
