package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/multiplayer"
)

func TestMultiplayerRouteAndOpenAPIMethodsStayInSync(t *testing.T) {
	documented := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(multiplayerOpenAPI))
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
		if match := methodLine.FindStringSubmatch(strings.TrimSpace(line)); path != "" && strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "      ") && len(match) == 2 {
			documented[strings.ToUpper(match[1])+" "+path] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("multiplayer.go")
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	pattern := regexp.MustCompile(`HandleFunc\("([A-Z]+) /api/multiplayer/v1([^\"]*)"`)
	for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
		path = match[2]
		if path == "" {
			path = "/"
		}
		registered[match[1]+" "+path] = true
	}
	for route := range registered {
		if !documented[route] {
			t.Errorf("registered multiplayer route missing from OpenAPI: %s", route)
		}
	}
	for route := range documented {
		if !registered[route] {
			t.Errorf("documented multiplayer route missing from server: %s", route)
		}
	}
	if len(registered) != 7 || len(documented) != 7 {
		t.Fatalf("route count drift: registered=%d documented=%d", len(registered), len(documented))
	}
}

func multiplayerTestHandler(t *testing.T) http.Handler {
	t.Helper()
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app, err := New(store, root, WithToken("admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return app.Handler()
}

func multiplayerJSON(t *testing.T, handler http.Handler, method, path string, input any, token string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data)).WithContext(context.Background())
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestMultiplayerAPISecurityVersionAndCompatibility(t *testing.T) {
	handler := multiplayerTestHandler(t)
	capabilities := httptest.NewRecorder()
	handler.ServeHTTP(capabilities, httptest.NewRequest(http.MethodGet, "/api/multiplayer/v1/capabilities", nil))
	if capabilities.Code != http.StatusOK || capabilities.Header().Get("X-Varkiv-Multiplayer-Version") != "v1" || capabilities.Header().Get("Deprecation") != "" || !strings.Contains(capabilities.Body.String(), `"data_relay":false`) {
		t.Fatalf("capabilities = %d %#v %s", capabilities.Code, capabilities.Header(), capabilities.Body.String())
	}

	input := multiplayer.CreateSessionInput{
		ProfileID: multiplayer.ProfileRetroArch,
		Content:   multiplayer.ContentIdentity{SHA256: strings.Repeat("a", 64), Size: 131072, Platform: "nes"},
		Runtime:   multiplayer.RuntimeIdentity{Emulator: "retroarch", Version: "1.22.2", Core: "fceumm", CoreVersion: "git-abc123"},
		Transport: "relay", SavePolicy: "isolated",
		Host: multiplayer.ParticipantInput{ClientID: "host", DisplayName: "Host"},
	}
	unauthorized := multiplayerJSON(t, handler, http.MethodPost, "/api/multiplayer/v1/sessions", input, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized create = %d", unauthorized.Code)
	}
	createdResponse := multiplayerJSON(t, handler, http.MethodPost, "/api/multiplayer/v1/sessions", input, "admin-secret")
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created multiplayer.CreatedSession
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.JoinToken) != 64 || strings.Contains(createdResponse.Header().Get("Location"), created.JoinToken) {
		t.Fatal("invitation leaked into location or had weak size")
	}

	join := multiplayer.JoinSessionInput{JoinToken: created.JoinToken, Content: created.Session.Content, Runtime: created.Session.Runtime, Client: multiplayer.ParticipantInput{ClientID: "guest", DisplayName: "Guest"}}
	join.Content.SHA256 = strings.Repeat("b", 64)
	rejected := multiplayerJSON(t, handler, http.MethodPost, "/api/multiplayer/v1/sessions/"+created.Session.ID+"/join", join, "")
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "content.sha256") {
		t.Fatalf("mismatch = %d %s", rejected.Code, rejected.Body.String())
	}
	join.Content = created.Session.Content
	joined := multiplayerJSON(t, handler, http.MethodPost, "/api/multiplayer/v1/sessions/"+created.Session.ID+"/join", join, "")
	if joined.Code != http.StatusOK || !strings.Contains(joined.Body.String(), `"state":"ready"`) {
		t.Fatalf("join = %d %s", joined.Code, joined.Body.String())
	}
}

func TestMultiplayerWrongMethodsReturnAllow(t *testing.T) {
	handler := multiplayerTestHandler(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/multiplayer/v1/sessions", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("wrong method = %d allow=%q body=%s", response.Code, response.Header().Get("Allow"), response.Body.String())
	}
}
