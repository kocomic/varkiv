package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func hashPackMultipart(t *testing.T, data []byte, token string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "fixture.hashpack")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if token != "" {
		if err = writer.WriteField("preview_token", token); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

func TestHashPackAPIExportPreviewAndAtomicImport(t *testing.T) {
	ctx := context.Background()
	sourceStore, sourceHandler, _ := testServer(t)
	game, err := sourceStore.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Shareable", Platform: "gba", Titles: map[string]string{"zh-CN": "可分享游戏"}})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := sourceStore.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original", Languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sourceStore.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "private/users/alice/shareable.gba", OriginalName: "Alice Private ROM.gba", Role: "rom", Size: 8192, SHA256: strings.Repeat("d", 64)}); err != nil {
		t.Fatal(err)
	}
	export := httptest.NewRecorder()
	exportBody := strings.NewReader(`{"source_id":"example.share","name":"Example identities","publisher":"Example","license":"CC0-1.0","release":"2026.09"}`)
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/v1/hash-packs/export", exportBody)
	exportRequest.Header.Set("Content-Type", "application/json")
	sourceHandler.ServeHTTP(export, exportRequest)
	if export.Code != http.StatusOK || export.Header().Get("X-Varkiv-Record-Count") != "1" {
		t.Fatalf("export=%d headers=%v body=%s", export.Code, export.Header(), export.Body.String())
	}
	packBytes := export.Body.Bytes()
	for _, private := range []string{"private/users/alice", "Alice Private ROM.gba"} {
		if bytes.Contains(packBytes, []byte(private)) {
			t.Fatalf("export leaked %q", private)
		}
	}

	destinationStore, destinationHandler, _ := testServer(t)
	previewBody, previewType := hashPackMultipart(t, packBytes, "")
	previewRecorder := httptest.NewRecorder()
	previewRequest := httptest.NewRequest(http.MethodPost, "/api/v1/hash-packs/preview", previewBody)
	previewRequest.Header.Set("Content-Type", previewType)
	destinationHandler.ServeHTTP(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview=%d %s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var preview hashPackPreviewResponse
	if err = json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.NewCount != 1 || preview.RecordCount != 1 || preview.PreviewToken == "" || preview.Source.ID != "example.share" {
		t.Fatalf("preview=%#v", preview)
	}

	staleBody, staleType := hashPackMultipart(t, packBytes, preview.PreviewToken+"changed")
	staleRecorder := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(http.MethodPost, "/api/v1/hash-packs/import", staleBody)
	staleRequest.Header.Set("Content-Type", staleType)
	destinationHandler.ServeHTTP(staleRecorder, staleRequest)
	if staleRecorder.Code != http.StatusConflict || !strings.Contains(staleRecorder.Body.String(), "hash_pack_preview_stale") {
		t.Fatalf("stale commit=%d %s", staleRecorder.Code, staleRecorder.Body.String())
	}

	commitBody, commitType := hashPackMultipart(t, packBytes, preview.PreviewToken)
	commitRecorder := httptest.NewRecorder()
	commitRequest := httptest.NewRequest(http.MethodPost, "/api/v1/hash-packs/import", commitBody)
	commitRequest.Header.Set("Content-Type", commitType)
	destinationHandler.ServeHTTP(commitRecorder, commitRequest)
	if commitRecorder.Code != http.StatusCreated {
		t.Fatalf("commit=%d %s", commitRecorder.Code, commitRecorder.Body.String())
	}
	repeatPreviewBody, repeatPreviewType := hashPackMultipart(t, packBytes, "")
	repeatPreviewRecorder := httptest.NewRecorder()
	repeatPreviewRequest := httptest.NewRequest(http.MethodPost, "/api/v1/hash-packs/preview", repeatPreviewBody)
	repeatPreviewRequest.Header.Set("Content-Type", repeatPreviewType)
	destinationHandler.ServeHTTP(repeatPreviewRecorder, repeatPreviewRequest)
	var repeatPreview hashPackPreviewResponse
	if repeatPreviewRecorder.Code != http.StatusOK || json.Unmarshal(repeatPreviewRecorder.Body.Bytes(), &repeatPreview) != nil || !repeatPreview.ExistingRelease {
		t.Fatalf("repeat preview=%d %s", repeatPreviewRecorder.Code, repeatPreviewRecorder.Body.String())
	}
	repeatBody, repeatType := hashPackMultipart(t, packBytes, repeatPreview.PreviewToken)
	repeatRecorder := httptest.NewRecorder()
	repeatRequest := httptest.NewRequest(http.MethodPost, "/api/v1/hash-packs/import", repeatBody)
	repeatRequest.Header.Set("Content-Type", repeatType)
	destinationHandler.ServeHTTP(repeatRecorder, repeatRequest)
	if repeatRecorder.Code != http.StatusOK || !strings.Contains(repeatRecorder.Body.String(), `"existing_release":true`) {
		t.Fatalf("idempotent commit=%d %s", repeatRecorder.Code, repeatRecorder.Body.String())
	}
	var games int
	listed, err := destinationStore.ListGames(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	games = len(listed)
	if games != 0 {
		t.Fatalf("hash-only import created %d library games", games)
	}

	sources := httptest.NewRecorder()
	destinationHandler.ServeHTTP(sources, httptest.NewRequest(http.MethodGet, "/api/v1/hash-sources", nil))
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), `"id":"example.share"`) || !strings.Contains(sources.Body.String(), `"record_count":1`) {
		t.Fatalf("sources=%d %s", sources.Code, sources.Body.String())
	}
	identity := httptest.NewRecorder()
	destinationHandler.ServeHTTP(identity, httptest.NewRequest(http.MethodGet, "/api/v1/hash-identities/"+strings.Repeat("d", 64), nil))
	if identity.Code != http.StatusOK {
		t.Fatalf("identity=%d %s", identity.Code, identity.Body.String())
	}
	resolved, err := io.ReadAll(identity.Body)
	if err != nil || !bytes.Contains(resolved, []byte(`"game_default_title":"Shareable"`)) || bytes.Contains(resolved, []byte("Alice")) {
		t.Fatalf("identity response=%s err=%v", resolved, err)
	}
	invalidIdentity := httptest.NewRecorder()
	destinationHandler.ServeHTTP(invalidIdentity, httptest.NewRequest(http.MethodGet, "/api/v1/hash-identities/not-a-sha", nil))
	if invalidIdentity.Code != http.StatusBadRequest || !strings.Contains(invalidIdentity.Body.String(), `"code":"invalid_argument"`) {
		t.Fatalf("invalid identity=%d %s", invalidIdentity.Code, invalidIdentity.Body.String())
	}
}
