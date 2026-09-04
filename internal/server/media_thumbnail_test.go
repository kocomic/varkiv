package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"varkiv/internal/catalog"
)

func TestMediaThumbnailCacheVerifiesSourceAndRepairsOnlyCache(t *testing.T) {
	store, handler, root := testServer(t)
	game, err := store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Thumbnail", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	sourceImage := image.NewNRGBA(image.Rect(0, 0, 1000, 500))
	for y := range sourceImage.Bounds().Dy() {
		for x := range sourceImage.Bounds().Dx() {
			sourceImage.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 251), G: uint8(y % 241), B: uint8((x + y) % 239), A: 255})
		}
	}
	var source bytes.Buffer
	if err = png.Encode(&source, sourceImage); err != nil {
		t.Fatal(err)
	}
	hashBytes := sha256.Sum256(source.Bytes())
	hash := hex.EncodeToString(hashBytes[:])
	managedPath := filepath.Join(root, ".library-data", "media", "fixture", "original-private-name.png")
	if err = os.MkdirAll(filepath.Dir(managedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(managedPath, source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	item, err := store.AddMedia(context.Background(), catalog.NewMediaAsset{
		GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: "fixture/original-private-name.png", OriginalName: "private-original.png", MIMEType: "image/png", Size: int64(source.Len()), SHA256: hash, SourceType: "upload", ContentStatus: "available",
	})
	if err != nil {
		t.Fatal(err)
	}

	requestPath := "/api/v1/media/" + item.ID + "/thumbnail?size=128"
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, requestPath, nil))
	if first.Code != http.StatusOK || first.Header().Get("Content-Type") != "image/png" || first.Header().Get("ETag") == "" {
		t.Fatalf("first thumbnail = %d headers=%#v body=%s", first.Code, first.Header(), first.Body.String())
	}
	config, err := png.DecodeConfig(bytes.NewReader(first.Body.Bytes()))
	if err != nil || config.Width != 128 || config.Height != 64 {
		t.Fatalf("thumbnail dimensions = %dx%d err=%v", config.Width, config.Height, err)
	}
	cachePath := filepath.Join(root, ".library-data", "media", "cache", "thumbnails-v1", hash[:2], hash, "128.png")
	cacheInfo, err := os.Lstat(cachePath)
	if err != nil || !cacheInfo.Mode().IsRegular() || cacheInfo.Mode().Perm() != 0o600 {
		t.Fatalf("thumbnail cache mode = %#v err=%v", cacheInfo, err)
	}
	if strings.Contains(cachePath, item.OriginalName) || strings.Contains(cachePath, "original-private-name") {
		t.Fatalf("cache path leaked source naming: %s", cachePath)
	}

	// A damaged cache is replaceable. Even a cache symlink is replaced without
	// touching its target.
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	if err = os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(sentinel, cachePath); err != nil {
		t.Fatal(err)
	}
	repaired := httptest.NewRecorder()
	handler.ServeHTTP(repaired, httptest.NewRequest(http.MethodGet, requestPath, nil))
	if repaired.Code != http.StatusOK || !bytes.Equal(repaired.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("repaired thumbnail = %d %s", repaired.Code, repaired.Body.String())
	}
	if data, readErr := os.ReadFile(sentinel); readErr != nil || string(data) != "keep" {
		t.Fatalf("cache repair changed symlink target: %q err=%v", data, readErr)
	}

	// Concurrent callers converge on the same service-owned cache bytes.
	const callers = 8
	results := make(chan []byte, callers)
	errorsFound := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if response.Code != http.StatusOK {
				errorsFound <- response.Body.String()
				return
			}
			results <- response.Body.Bytes()
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for failure := range errorsFound {
		t.Errorf("concurrent thumbnail failed: %s", failure)
	}
	for data := range results {
		if !bytes.Equal(data, first.Body.Bytes()) {
			t.Fatal("concurrent thumbnail bytes drifted")
		}
	}

	conditional := httptest.NewRequest(http.MethodGet, requestPath, nil)
	conditional.Header.Set("If-None-Match", first.Header().Get("ETag"))
	conditionalResponse := httptest.NewRecorder()
	handler.ServeHTTP(conditionalResponse, conditional)
	if conditionalResponse.Code != http.StatusNotModified {
		t.Fatalf("conditional thumbnail = %d %s", conditionalResponse.Code, conditionalResponse.Body.String())
	}

	// Source identity is checked before the conditional response or cache hit.
	corrupt := bytes.Repeat([]byte{0x7f}, source.Len())
	if err = os.WriteFile(managedPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	drifted := httptest.NewRequest(http.MethodGet, requestPath, nil)
	drifted.Header.Set("If-None-Match", first.Header().Get("ETag"))
	driftedResponse := httptest.NewRecorder()
	handler.ServeHTTP(driftedResponse, drifted)
	var failure apiErrorEnvelope
	if err = json.Unmarshal(driftedResponse.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if driftedResponse.Code != http.StatusConflict || failure.Error.Code != "media_content_integrity_failed" || bytes.Contains(driftedResponse.Body.Bytes(), first.Body.Bytes()[:16]) {
		t.Fatalf("source drift served cached bytes: %d %s", driftedResponse.Code, driftedResponse.Body.String())
	}
}

func TestMediaThumbnailRejectsInvalidRequestsAndUnverifiedFormats(t *testing.T) {
	store, handler, root := testServer(t)
	game, err := store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Thumbnail errors", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(root, ".library-data", "media", "fixture")
	if err = os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	add := func(name, mimeType string, data []byte, hash string) catalog.MediaAsset {
		t.Helper()
		if err = os.WriteFile(filepath.Join(mediaRoot, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		item, addErr := store.AddMedia(context.Background(), catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", StorageKind: "managed", Path: "fixture/" + name, MIMEType: mimeType, Size: int64(len(data)), SHA256: hash, SourceType: "upload"})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return item
	}
	webp := []byte("RIFF\x10\x00\x00\x00WEBPVP8 unsupported")
	webpHashBytes := sha256.Sum256(webp)
	unsupported := add("cover.webp", "image/webp", webp, hex.EncodeToString(webpHashBytes[:]))
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	svgHashBytes := sha256.Sum256(svg)
	active := add("cover.svg", "image/svg+xml", svg, hex.EncodeToString(svgHashBytes[:]))
	unverified := add("cover.bin", "image/png", []byte("not-an-image"), "")

	for _, test := range []struct {
		path string
		code int
		api  string
	}{
		{path: "/api/v1/media/" + unsupported.ID + "/thumbnail?size=999", code: http.StatusBadRequest, api: "invalid_thumbnail_size"},
		{path: "/api/v1/media/" + unsupported.ID + "/thumbnail?size=128", code: http.StatusUnsupportedMediaType, api: "media_thumbnail_unsupported"},
		{path: "/api/v1/media/" + active.ID + "/thumbnail?size=128", code: http.StatusUnsupportedMediaType, api: "media_thumbnail_unsupported"},
		{path: "/api/v1/media/" + unverified.ID + "/thumbnail?size=128", code: http.StatusUnprocessableEntity, api: "media_thumbnail_unverified"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		var failure apiErrorEnvelope
		if err = json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.code || failure.Error.Code != test.api {
			t.Errorf("%s = %d/%s; want %d/%s", test.path, response.Code, failure.Error.Code, test.code, test.api)
		}
	}
	for _, dimensions := range [][2]int{{0, 1}, {1, 0}, {16_385, 1}, {5_000, 5_000}} {
		if err = validateThumbnailSource(dimensions[0], dimensions[1]); !errors.Is(err, errMediaThumbnailTooLarge) {
			t.Errorf("dimensions %v accepted: %v", dimensions, err)
		}
	}
}
