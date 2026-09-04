package server

import (
	_ "embed"
	"errors"
	"net/http"
	"strings"

	"varkiv/internal/multiplayer"
)

//go:embed multiplayer_openapi.yaml
var multiplayerOpenAPI []byte

func (s *Server) multiplayerRoutes() {
	s.mux.HandleFunc("GET /api/multiplayer/v1", s.multiplayerRoot)
	s.mux.HandleFunc("GET /api/multiplayer/v1/capabilities", s.multiplayerCapabilities)
	s.mux.HandleFunc("GET /api/multiplayer/v1/openapi.yaml", s.multiplayerOpenAPISpec)
	s.mux.HandleFunc("POST /api/multiplayer/v1/sessions", s.createMultiplayerSession)
	s.mux.HandleFunc("GET /api/multiplayer/v1/sessions/{id}", s.getMultiplayerSession)
	s.mux.HandleFunc("POST /api/multiplayer/v1/sessions/{id}/join", s.joinMultiplayerSession)
	s.mux.HandleFunc("POST /api/multiplayer/v1/sessions/{id}/close", s.closeMultiplayerSession)
}

func (s *Server) multiplayerRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":             "Varkiv Multiplayer Coordination API",
		"protocol_version": multiplayer.ProtocolVersion,
		"links": map[string]string{
			"capabilities": "/api/multiplayer/v1/capabilities",
			"openapi":      "/api/multiplayer/v1/openapi.yaml",
			"sessions":     "/api/multiplayer/v1/sessions",
		},
	})
}

func (s *Server) multiplayerCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol_version": multiplayer.ProtocolVersion,
		"coordination":     true,
		"automatic_launch": false,
		"data_relay":       false,
		"profiles": []map[string]any{{
			"id":               multiplayer.ProfileRetroArch,
			"support_level":    "coordination-preview",
			"emulator":         "retroarch",
			"mode":             "deterministic-input-sync",
			"transports":       []string{"relay", "direct"},
			"save_policies":    []string{"isolated", "host-authoritative", "no-persist"},
			"compatibility":    []string{"content.sha256", "content.size", "content.platform", "runtime.emulator", "runtime.version", "runtime.core", "runtime.core_version"},
			"max_participants": 4,
		}, {
			"id":               multiplayer.ProfileEmulatorJS,
			"support_level":    "experimental-playback",
			"emulator":         "emulatorjs",
			"mode":             "host-stream-webrtc",
			"transports":       []string{"direct"},
			"save_policies":    []string{"no-persist"},
			"compatibility":    []string{"content.sha256", "content.size", "content.platform", "runtime.emulator", "runtime.version", "runtime.core", "runtime.core_version"},
			"max_participants": 2,
		}},
	})
}

func (s *Server) multiplayerOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(multiplayerOpenAPI)
}

func (s *Server) createMultiplayerSession(w http.ResponseWriter, r *http.Request) {
	var input multiplayer.CreateSessionInput
	if !decode(w, r, &input) {
		return
	}
	created, err := s.multiplayer.Create(input)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_multiplayer_session", err.Error())
		return
	}
	w.Header().Set("Location", "/api/multiplayer/v1/sessions/"+created.Session.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) getMultiplayerSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.multiplayer.Get(r.PathValue("id"))
	if err != nil {
		writeMultiplayerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) joinMultiplayerSession(w http.ResponseWriter, r *http.Request) {
	var input multiplayer.JoinSessionInput
	if !decode(w, r, &input) {
		return
	}
	session, err := s.multiplayer.Join(r.PathValue("id"), input)
	if err != nil {
		writeMultiplayerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) closeMultiplayerSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.multiplayer.Close(r.PathValue("id"))
	if err != nil {
		writeMultiplayerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func writeMultiplayerError(w http.ResponseWriter, err error) {
	var mismatch *multiplayer.CompatibilityError
	switch {
	case errors.As(err, &mismatch):
		writeAPIError(w, http.StatusConflict, "compatibility_mismatch", strings.Join(mismatch.Fields, ", "))
	case errors.Is(err, multiplayer.ErrInvalidToken):
		writeAPIError(w, http.StatusUnauthorized, "invalid_invitation", "invalid or expired invitation")
	case errors.Is(err, multiplayer.ErrNotFound), errors.Is(err, multiplayer.ErrExpired):
		writeAPIError(w, http.StatusNotFound, "session_not_found", "multiplayer session not found")
	case errors.Is(err, multiplayer.ErrClosed):
		writeAPIError(w, http.StatusGone, "session_closed", "multiplayer session is closed")
	case errors.Is(err, multiplayer.ErrFull):
		writeAPIError(w, http.StatusConflict, "session_full", "multiplayer session is full")
	default:
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_multiplayer_request", err.Error())
	}
}
