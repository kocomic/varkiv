package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"varkiv/internal/catalog"
)

func TestInventoryMatchConfirmationAPIIsPrivateAndDriftSafe(t *testing.T) {
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
	ctx := context.Background()

	gameA, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Advance Wars", Platform: "gba", Titles: map[string]string{"zh-CN": "高级战争"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddEdition(ctx, catalog.NewEdition{GameID: gameA.ID, DefaultTitle: "Original", EditionType: "original", Serial: "AGB-AWR"})
	if err != nil {
		t.Fatal(err)
	}
	gameB, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Advance Wars Translation", Platform: "gba", Titles: map[string]string{"zh-CN": "高级战争 汉化版"}})
	if err != nil {
		t.Fatal(err)
	}
	editionB, err := store.AddEdition(ctx, catalog.NewEdition{GameID: gameB.ID, DefaultTitle: "Chinese translation", EditionType: "translation", Serial: "AGB-AWR"})
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := store.CreatePairingCode(ctx, catalog.NewPairingCode{CodeHash: "fixture-code-hash", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	deviceSecret := "fixture-device-secret"
	device, _, _, err := store.RedeemPairingCode(ctx, pairing.CodeHash, hashClientSecret(deviceSecret), catalog.NewDevice{Name: "ROCKNIX fixture", OSFamily: "linux", Architecture: "arm64"}, []string{"sync:read", "sync:write"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	input := catalog.NewInventoryItem{ID: "inventory-review", ClientItemID: strings.Repeat("f", 64), PlatformID: "gba", Serial: "AGB-AWR", Size: 16 * 1024 * 1024}
	matched, err := store.MatchInventoryItem(ctx, input)
	if err != nil || matched.MatchStatus != "ambiguous" {
		t.Fatalf("ambiguous fixture=%#v err=%v", matched, err)
	}
	session, _, err := store.CreateSyncSession(ctx, catalog.NewSyncSession{DeviceID: device.ID, IdempotencyKey: "inventory-review-session", Inventory: []catalog.NewInventoryItem{matched}})
	if err != nil {
		t.Fatal(err)
	}

	request := func(method, path string, body any, bearer string) *httptest.ResponseRecorder {
		var payload *bytes.Reader
		if body == nil {
			payload = bytes.NewReader(nil)
		} else {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			payload = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, path, payload)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	listed := request(http.MethodGet, "/api/v1/sync/inventory-matches?locale=zh-CN", nil, "admin-secret")
	if listed.Code != http.StatusOK {
		t.Fatalf("list reviews: %d %s", listed.Code, listed.Body.String())
	}
	var collection struct {
		Data []inventoryMatchReview `json:"data"`
	}
	if err = json.Unmarshal(listed.Body.Bytes(), &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Data) != 1 || len(collection.Data[0].Candidates) != 2 || collection.Data[0].DeviceName != device.Name {
		t.Fatalf("unexpected review collection: %#v", collection.Data)
	}
	responseText := listed.Body.String()
	for _, privateValue := range []string{input.ClientItemID, input.Serial, "client_item_id", "sha256", "product_code", "title_id", "/library/", ".gba"} {
		if strings.Contains(responseText, privateValue) {
			t.Fatalf("review response leaked private ROM identity %q: %s", privateValue, responseText)
		}
	}
	if collection.Data[0].Candidates[0].GameTitle == "" || collection.Data[0].Candidates[1].GameTitle == "" {
		t.Fatalf("localized candidate titles missing: %#v", collection.Data[0].Candidates)
	}

	selection := inventoryMatchRequest{SessionID: session.ID, InventoryItemID: input.ID, EditionID: editionB.ID}
	previewResponse := request(http.MethodPost, "/api/v1/sync/inventory-matches/preview?locale=zh-CN", selection, "admin-secret")
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview inventoryMatchPreview
	if err = json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.PreviewToken == "" || preview.SelectedEditionID != editionB.ID || !preview.AppliesToNextSync {
		t.Fatalf("invalid preview: %#v", preview)
	}
	tampered := selection
	tampered.PreviewToken = preview.PreviewToken + "tampered"
	if response := request(http.MethodPost, "/api/v1/sync/inventory-matches/commit", tampered, "admin-secret"); response.Code != http.StatusConflict {
		t.Fatalf("tampered preview token: %d %s", response.Code, response.Body.String())
	}

	gameC, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Advance Wars Hack", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddEdition(ctx, catalog.NewEdition{GameID: gameC.ID, DefaultTitle: "Balance hack", EditionType: "hack", Serial: "AGB-AWR"}); err != nil {
		t.Fatal(err)
	}
	stale := selection
	stale.PreviewToken = preview.PreviewToken
	if response := request(http.MethodPost, "/api/v1/sync/inventory-matches/commit", stale, "admin-secret"); response.Code != http.StatusConflict {
		t.Fatalf("candidate drift: %d %s", response.Code, response.Body.String())
	}
	freshPreviewResponse := request(http.MethodPost, "/api/v1/sync/inventory-matches/preview", selection, "admin-secret")
	if freshPreviewResponse.Code != http.StatusOK {
		t.Fatalf("fresh preview: %d %s", freshPreviewResponse.Code, freshPreviewResponse.Body.String())
	}
	if err = json.Unmarshal(freshPreviewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	commit := selection
	commit.PreviewToken = preview.PreviewToken
	committed := request(http.MethodPost, "/api/v1/sync/inventory-matches/commit", commit, "admin-secret")
	if committed.Code != http.StatusOK || !strings.Contains(committed.Body.String(), `"confirmed":true`) {
		t.Fatalf("commit: %d %s", committed.Code, committed.Body.String())
	}

	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/sync/inventory-matches"},
		{http.MethodPost, "/api/v1/sync/inventory-matches/preview"},
		{http.MethodPost, "/api/v1/sync/inventory-matches/commit"},
	} {
		if response := request(endpoint.method, endpoint.path, selection, deviceSecret); response.Code != http.StatusForbidden {
			t.Fatalf("device token reached owner review route %s: %d %s", endpoint.path, response.Code, response.Body.String())
		}
	}

	encoded, err := json.Marshal(syncSessionRequest{DeviceID: device.ID, Inventory: []syncInventoryInput{{ClientItemID: input.ClientItemID, PlatformID: input.PlatformID, Serial: input.Serial, Size: input.Size}}, Saves: []syncSaveStateInput{}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/sessions", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Idempotency-Key", "confirmed-next-sync")
	nextSync := httptest.NewRecorder()
	handler.ServeHTTP(nextSync, req)
	if nextSync.Code != http.StatusCreated {
		t.Fatalf("next sync: %d %s", nextSync.Code, nextSync.Body.String())
	}
	var syncResponse syncSessionResponse
	if err = json.Unmarshal(nextSync.Body.Bytes(), &syncResponse); err != nil {
		t.Fatal(err)
	}
	if len(syncResponse.Inventory) != 1 || syncResponse.Inventory[0].MatchedEditionID != editionB.ID || syncResponse.Inventory[0].MatchMethod != "confirmed_serial" {
		t.Fatalf("next sync did not reuse confirmation: %#v", syncResponse.Inventory)
	}
}
