package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"varkiv/internal/catalog"
)

func webNetplaySignalFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/games" && r.URL.Path != "/list" && !strings.HasPrefix(r.URL.Path, "/socket.io/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestWebNetplayCreateJoinAndPlayerContract(t *testing.T) {
	signal := webNetplaySignalFixture(t)
	_, handler, edition, _, romPath, _ := webEmulationFixture(t, "nes", WithToken("admin-secret"), WithWebNetplay("https://cdn.emulatorjs.org/4.3.0-pre/data/", "", signal.URL, `[{"urls":["stun:stun.example.test:3478"]}]`))

	readiness := httptest.NewRecorder()
	handler.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/api/v1/web-netplay/readiness", nil))
	if readiness.Code != http.StatusOK || !strings.Contains(readiness.Body.String(), `"signal_ready":true`) || !strings.Contains(readiness.Body.String(), `"profile_id":"emulatorjs-webrtc-v1"`) || strings.Contains(readiness.Body.String(), signal.URL) {
		t.Fatalf("unexpected or leaky readiness: %d %s", readiness.Code, readiness.Body.String())
	}

	input := webNetplayCreateInput{EditionID: edition.ID, Locale: "zh-CN", ClientID: "host-browser", DisplayName: "房主"}
	createdResponse := authenticatedWebRequest(t, handler, http.MethodPost, "/api/v1/web-netplay/sessions", input)
	// authenticatedWebRequest carries the owner token; make the unauthenticated
	// boundary explicit with a separate request.
	request := httptest.NewRequest(http.MethodPost, "/api/v1/web-netplay/sessions", strings.NewReader(`{"edition_id":"`+edition.ID+`","client_id":"host","display_name":"Host"}`))
	request.Header.Set("Content-Type", "application/json")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = %d %s", denied.Code, denied.Body.String())
	}
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("host create = %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var host webNetplaySessionResponse
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &host); err != nil {
		t.Fatal(err)
	}
	if host.Role != "host" || host.InviteCode == "" || !strings.HasPrefix(host.PlayerURL, "/play-netplay/") || host.Session.SavePolicy != "no-persist" || host.Session.Runtime != webNetplayRuntime() {
		t.Fatalf("unexpected host session: %#v", host)
	}
	player := httptest.NewRecorder()
	handler.ServeHTTP(player, httptest.NewRequest(http.MethodGet, host.PlayerURL, nil))
	if player.Code != http.StatusOK || !strings.Contains(player.Body.String(), "https://cdn.emulatorjs.org/4.3.0-pre/data/loader.js") || !strings.Contains(player.Body.String(), `EJS_netplayServer`) || !strings.Contains(player.Body.String(), `same-origin`) || !strings.Contains(player.Body.String(), `startNetplay`) || strings.Contains(player.Body.String(), `/web-emulation/saves/`) {
		t.Fatalf("host player contract missing: %d %s", player.Code, player.Body.String())
	}
	if strings.Contains(player.Body.String(), host.InviteCode) || strings.Contains(player.Body.String(), romPath) || strings.Contains(player.Body.String(), signal.URL) {
		t.Fatalf("host player leaked invitation, path, or internal upstream")
	}

	join := webNetplayJoinInput{InviteCode: host.InviteCode, EditionID: edition.ID, Locale: "en", ClientID: "guest-browser", DisplayName: "Guest"}
	joinBody, err := json.Marshal(join)
	if err != nil {
		t.Fatal(err)
	}
	joinRequest := httptest.NewRequest(http.MethodPost, "/api/v1/web-netplay/sessions/join", bytes.NewReader(joinBody))
	joinRequest.Header.Set("Content-Type", "application/json")
	joinedResponse := httptest.NewRecorder()
	handler.ServeHTTP(joinedResponse, joinRequest)
	if joinedResponse.Code != http.StatusOK {
		t.Fatalf("guest join = %d %s", joinedResponse.Code, joinedResponse.Body.String())
	}
	var guest webNetplaySessionResponse
	if err := json.Unmarshal(joinedResponse.Body.Bytes(), &guest); err != nil {
		t.Fatal(err)
	}
	if guest.Role != "guest" || guest.InviteCode != "" || guest.Session.State != "ready" || len(guest.Session.Participants) != 2 {
		t.Fatalf("unexpected guest session: %#v", guest)
	}

	third := join
	third.ClientID, third.DisplayName = "third-browser", "Third"
	full := authenticatedWebRequest(t, handler, http.MethodPost, "/api/v1/web-netplay/sessions/join", third)
	if full.Code != http.StatusConflict || !strings.Contains(full.Body.String(), "session_full") {
		t.Fatalf("third participant was not rejected: %d %s", full.Code, full.Body.String())
	}

	proxied := httptest.NewRecorder()
	handler.ServeHTTP(proxied, httptest.NewRequest(http.MethodGet, "/games", nil))
	if proxied.Code != http.StatusOK || strings.TrimSpace(proxied.Body.String()) != "{}" {
		t.Fatalf("same-origin signal proxy failed: %d %s", proxied.Code, proxied.Body.String())
	}
}

func TestWebNetplayRejectsContentDriftWithoutJoining(t *testing.T) {
	signal := webNetplaySignalFixture(t)
	app, handler, edition, _, _, _ := webEmulationFixture(t, "nes", WithToken("admin-secret"), WithWebNetplay("https://cdn.emulatorjs.org/4.3.0-pre/data/", "", signal.URL, ""))
	created := authenticatedWebRequest(t, handler, http.MethodPost, "/api/v1/web-netplay/sessions", webNetplayCreateInput{EditionID: edition.ID, ClientID: "host", DisplayName: "Host"})
	var host webNetplaySessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &host); err != nil {
		t.Fatal(err)
	}

	game, err := app.store.CreateGame(context.Background(), catalog.NewGame{DefaultTitle: "Different", Platform: "nes"})
	if err != nil {
		t.Fatal(err)
	}
	different, err := app.store.AddEdition(context.Background(), catalog.NewEdition{GameID: game.ID, DefaultTitle: "Different", EditionType: "homebrew"})
	if err != nil {
		t.Fatal(err)
	}
	content := append([]byte{'N', 'E', 'S', 0x1a, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, bytes.Repeat([]byte{0x5a}, 48)...)
	path := filepath.Join(app.libraryRoot, "nes", "different.nes")
	if err = os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if _, err = app.store.AddArtifact(context.Background(), catalog.NewArtifact{EditionID: different.ID, Path: "nes/different.nes", Role: "rom", Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), StorageKind: "library", OriginalName: "different.nes"}); err != nil {
		t.Fatal(err)
	}
	join := webNetplayJoinInput{InviteCode: host.InviteCode, EditionID: different.ID, ClientID: "guest", DisplayName: "Guest"}
	rejected := authenticatedWebRequest(t, handler, http.MethodPost, "/api/v1/web-netplay/sessions/join", join)
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "compatibility_mismatch") {
		t.Fatalf("content drift not rejected: %d %s", rejected.Code, rejected.Body.String())
	}
	current, err := app.multiplayer.Get(host.Session.ID)
	if err != nil || current.State != "waiting" || len(current.Participants) != 1 {
		t.Fatalf("rejected join mutated session: %#v %v", current, err)
	}
}

func TestWebNetplayConfigurationFailsClosed(t *testing.T) {
	if _, err := parseWebNetplayICEServers(`[{"urls":["https://not-ice.example"]}]`); err == nil {
		t.Fatal("non-ICE URL accepted")
	}
	if _, err := parseWebNetplayICEServers(`[{"urls":["turn://user:secret@example.test"]}]`); err == nil {
		t.Fatal("embedded ICE credentials accepted")
	}
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = New(store, root, WithWebNetplay("https://cdn.emulatorjs.org/4.3.0-pre/data/", "", "", "")); err == nil {
		t.Fatal("assets without signal upstream accepted")
	}
}

func TestWebNetplayReadinessDoesNotFollowSignalRedirects(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	_, handler, _, _, _, _ := webEmulationFixture(t, "nes", WithWebNetplay("https://cdn.emulatorjs.org/4.3.0-pre/data/", "", redirect.URL, ""))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/web-netplay/readiness", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"signal_ready":false`) {
		t.Fatalf("redirecting signal endpoint reported ready: %d %s", response.Code, response.Body.String())
	}
	if followed.Load() {
		t.Fatal("signal readiness followed an upstream redirect")
	}
}

func TestWebNetplayManifestMatchesSharedAssetLock(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "web-netplay", "assets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		EmulatorJS struct {
			Version string                     `json:"version"`
			Assets  []webEmulatorAssetIdentity `json:"assets"`
		} `json:"emulatorjs"`
	}
	if err = json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.EmulatorJS.Version != webNetplayEmulatorVersion || len(lock.EmulatorJS.Assets) != len(webNetplayEmulatorAssetManifest) {
		t.Fatalf("asset lock drift: version=%q assets=%d", lock.EmulatorJS.Version, len(lock.EmulatorJS.Assets))
	}
	for index, want := range webNetplayEmulatorAssetManifest {
		if got := lock.EmulatorJS.Assets[index]; got != want {
			t.Fatalf("asset %d drift: got=%#v want=%#v", index, got, want)
		}
	}
}
