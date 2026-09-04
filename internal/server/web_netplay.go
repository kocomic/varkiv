package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/multiplayer"
)

const webNetplayPlatform = "nes"

type webNetplayICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type webNetplayReadinessResponse struct {
	Enabled              bool                           `json:"enabled"`
	Experimental         bool                           `json:"experimental"`
	SignalReady          bool                           `json:"signal_ready"`
	SameOriginSignal     bool                           `json:"same_origin_signal"`
	AssetMode            string                         `json:"asset_mode"`
	IntegrityVerified    bool                           `json:"integrity_verified"`
	EmulatorVersion      string                         `json:"emulatorjs_version,omitempty"`
	ProfileID            string                         `json:"profile_id"`
	Platforms            []string                       `json:"supported_platforms"`
	PlatformCapabilities []webNetplayPlatformCapability `json:"platform_capabilities"`
	SavePolicy           string                         `json:"save_policy"`
	ICEServerCount       int                            `json:"ice_server_count"`
	AssetsVerified       int                            `json:"assets_verified"`
	BytesVerified        int64                          `json:"bytes_verified"`
	Runtime              multiplayer.RuntimeIdentity    `json:"runtime"`
}

type webNetplayPlatformCapability struct {
	PlatformID      string   `json:"platform_id"`
	Core            string   `json:"core"`
	Extensions      []string `json:"extensions"`
	MinimumROMBytes int64    `json:"minimum_rom_bytes"`
	MaximumROMBytes int64    `json:"maximum_rom_bytes"`
}

type webNetplayCreateInput struct {
	EditionID   string `json:"edition_id"`
	Locale      string `json:"locale,omitempty"`
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name"`
}

type webNetplayJoinInput struct {
	InviteCode  string `json:"invite_code"`
	EditionID   string `json:"edition_id"`
	Locale      string `json:"locale,omitempty"`
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name"`
}

type webNetplaySessionResponse struct {
	Session    multiplayer.Session `json:"session"`
	InviteCode string              `json:"invite_code,omitempty"`
	PlayerURL  string              `json:"player_url"`
	Role       string              `json:"role"`
	ExpiresAt  time.Time           `json:"expires_at"`
}

func (s *Server) configureWebNetplay() error {
	assets := s.webNetplayAssets
	directory := s.webNetplayDir
	signal := s.webNetplaySignalRaw
	if assets == "" && directory == "" && signal == "" && s.webNetplayICEServersRaw == "" {
		return nil
	}
	if signal == "" || (assets == "" && directory == "") {
		return errors.New("web netplay requires EmulatorJS assets and a signal upstream")
	}
	if assets != "" && directory != "" {
		return errors.New("web netplay EmulatorJS assets URL and local directory are mutually exclusive")
	}
	if directory != "" {
		resolved, report, err := validateWebEmulatorDirectory(directory, webNetplayEmulatorAssetManifest)
		if err != nil {
			return fmt.Errorf("web netplay assets: %w", err)
		}
		report.Version = webNetplayEmulatorVersion
		s.webNetplayDir, s.webNetplayReport = resolved, report
		s.webNetplayAssets = "/emulatorjs-netplay/"
	} else if err := validateWebEmulatorAssets(assets); err != nil {
		return fmt.Errorf("web netplay assets: %w", err)
	}
	parsed, err := url.Parse(signal)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("web netplay signal upstream must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	s.webNetplaySignal = parsed
	s.webNetplayProxy = httputil.NewSingleHostReverseProxy(parsed)
	s.webNetplayProxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "netplay signal service unavailable", http.StatusBadGateway)
	}
	servers, err := parseWebNetplayICEServers(s.webNetplayICEServersRaw)
	if err != nil {
		return err
	}
	s.webNetplayICEServers = servers
	return nil
}

func parseWebNetplayICEServers(raw string) ([]webNetplayICEServer, error) {
	if raw == "" {
		return []webNetplayICEServer{}, nil
	}
	var servers []webNetplayICEServer
	if err := json.Unmarshal([]byte(raw), &servers); err != nil || len(servers) > 8 {
		return nil, errors.New("web netplay ICE servers must be a JSON array with at most eight entries")
	}
	for _, server := range servers {
		if len(server.URLs) == 0 || len(server.URLs) > 8 || len(server.Username) > 256 || len(server.Credential) > 512 {
			return nil, errors.New("web netplay ICE server entry is invalid")
		}
		for _, value := range server.URLs {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Scheme == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "stun" && parsed.Scheme != "stuns" && parsed.Scheme != "turn" && parsed.Scheme != "turns") {
				return nil, errors.New("web netplay ICE URL must use stun, stuns, turn, or turns without embedded credentials")
			}
		}
	}
	return servers, nil
}

func (s *Server) webNetplaySignalProxy(w http.ResponseWriter, r *http.Request) {
	if s.webNetplayProxy == nil {
		http.NotFound(w, r)
		return
	}
	s.webNetplayProxy.ServeHTTP(w, r)
}

func (s *Server) webNetplaySignalReady(ctx context.Context) bool {
	if s.webNetplaySignal == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	endpoint := *s.webNetplaySignal
	endpoint.Path = "/games"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func webNetplayRuntime() multiplayer.RuntimeIdentity {
	return multiplayer.RuntimeIdentity{Emulator: "emulatorjs", Version: webNetplayEmulatorVersion, Core: "fceumm", CoreVersion: webNetplayCoreVersion}
}

func (s *Server) webNetplayReadiness(w http.ResponseWriter, r *http.Request) {
	mode := "disabled"
	verified := false
	if s.webNetplayDir != "" {
		mode, verified = "self-hosted-verified", true
	} else if s.webNetplayAssets != "" {
		mode = "external-unverified"
	}
	writeJSON(w, http.StatusOK, webNetplayReadinessResponse{
		Enabled: s.webNetplayProxy != nil && s.webNetplayAssets != "", Experimental: true,
		SignalReady: s.webNetplaySignalReady(r.Context()), SameOriginSignal: true,
		AssetMode: mode, IntegrityVerified: verified, EmulatorVersion: webNetplayEmulatorVersion,
		ProfileID: multiplayer.ProfileEmulatorJS, Platforms: []string{webNetplayPlatform}, SavePolicy: "no-persist",
		PlatformCapabilities: []webNetplayPlatformCapability{{PlatformID: webNetplayPlatform, Core: "fceumm", Extensions: []string{".nes", ".unf", ".unif"}, MinimumROMBytes: webEmulationPlatformMinimumBytes[webNetplayPlatform], MaximumROMBytes: webEmulationMaxROMBytes}},
		ICEServerCount:       len(s.webNetplayICEServers), AssetsVerified: s.webNetplayReport.AssetsVerified,
		BytesVerified: s.webNetplayReport.BytesVerified, Runtime: webNetplayRuntime(),
	})
}

func normalizeWebNetplayIdentity(clientID, displayName string) (multiplayer.ParticipantInput, error) {
	clientID = strings.TrimSpace(clientID)
	displayName = strings.TrimSpace(displayName)
	if clientID == "" || len(clientID) > 128 || displayName == "" || len([]rune(displayName)) > 20 {
		return multiplayer.ParticipantInput{}, errors.New("client_id and a display_name of at most 20 characters are required")
	}
	return multiplayer.ParticipantInput{ClientID: clientID, DisplayName: displayName}, nil
}

func (s *Server) resolveWebNetplayEdition(r *http.Request, editionID, locale string) (catalog.Game, catalog.Edition, catalog.Artifact, error) {
	if s.webNetplayProxy == nil || s.webNetplayAssets == "" {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, errors.New("web netplay is not configured")
	}
	edition, err := s.store.GetEdition(r.Context(), strings.TrimSpace(editionID), webEmulationLocale(locale))
	if err != nil {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, err
	}
	game, err := s.store.GetGame(r.Context(), edition.GameID, webEmulationLocale(locale))
	if err != nil {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, err
	}
	if game.Platform != webNetplayPlatform {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, errors.New("the web netplay experiment currently supports NES only")
	}
	artifact := catalog.SelectLaunchArtifact(edition.Artifacts)
	if artifact == nil || artifact.SHA256 == "" || !webEmulationArtifactPlausible(game.Platform, artifact.Path, artifact.Size) {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, errors.New("a verified browser-playable ROM is required")
	}
	file, err := s.openVerifiedWebROM(*artifact)
	if err != nil {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, errors.New("ROM content no longer matches its catalog identity")
	}
	defer file.Close()
	if err = validateWebROMHeader(game.Platform, artifact.Path, file); err != nil {
		return catalog.Game{}, catalog.Edition{}, catalog.Artifact{}, errors.New("ROM header is invalid for web netplay")
	}
	return game, edition, *artifact, nil
}

func webNetplayContent(game catalog.Game, artifact catalog.Artifact) multiplayer.ContentIdentity {
	return multiplayer.ContentIdentity{SHA256: artifact.SHA256, Size: artifact.Size, Platform: game.Platform}
}

func (s *Server) createWebNetplaySession(w http.ResponseWriter, r *http.Request) {
	var input webNetplayCreateInput
	if !decode(w, r, &input) {
		return
	}
	participant, err := normalizeWebNetplayIdentity(input.ClientID, input.DisplayName)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "web_netplay_identity_invalid", err.Error())
		return
	}
	if !s.webNetplaySignalReady(r.Context()) {
		writeAPIError(w, http.StatusServiceUnavailable, "web_netplay_signal_unavailable", "web netplay signal service is unavailable")
		return
	}
	game, edition, artifact, err := s.resolveWebNetplayEdition(r, input.EditionID, input.Locale)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "web_netplay_unavailable", err.Error())
		return
	}
	created, err := s.multiplayer.Create(multiplayer.CreateSessionInput{
		ProfileID: multiplayer.ProfileEmulatorJS, Content: webNetplayContent(game, artifact), Runtime: webNetplayRuntime(),
		Transport: "direct", SavePolicy: "no-persist", Host: participant,
	})
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "web_netplay_session_invalid", err.Error())
		return
	}
	playerURL, err := s.createWebNetplayPlayerURL(created.Session, artifact, edition, input.Locale, "host", participant.DisplayName)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, webNetplaySessionResponse{
		Session: created.Session, InviteCode: created.Session.ID + "." + created.JoinToken,
		PlayerURL: playerURL, Role: "host", ExpiresAt: created.Session.ExpiresAt,
	})
}

func parseWebNetplayInvite(value string) (string, string, error) {
	id, token, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok || len(id) != 32 || len(token) != 64 {
		return "", "", errors.New("invitation code is invalid")
	}
	if _, err := hex.DecodeString(id + token); err != nil {
		return "", "", errors.New("invitation code is invalid")
	}
	return id, token, nil
}

func (s *Server) joinWebNetplaySession(w http.ResponseWriter, r *http.Request) {
	var input webNetplayJoinInput
	if !decode(w, r, &input) {
		return
	}
	id, joinToken, err := parseWebNetplayInvite(input.InviteCode)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "web_netplay_invite_invalid", err.Error())
		return
	}
	participant, err := normalizeWebNetplayIdentity(input.ClientID, input.DisplayName)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "web_netplay_identity_invalid", err.Error())
		return
	}
	game, edition, artifact, err := s.resolveWebNetplayEdition(r, input.EditionID, input.Locale)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "web_netplay_unavailable", err.Error())
		return
	}
	joined, err := s.multiplayer.Join(id, multiplayer.JoinSessionInput{
		JoinToken: joinToken, Content: webNetplayContent(game, artifact), Runtime: webNetplayRuntime(), Client: participant,
	})
	if err != nil {
		writeMultiplayerError(w, err)
		return
	}
	playerURL, err := s.createWebNetplayPlayerURL(joined, artifact, edition, input.Locale, "guest", participant.DisplayName)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, webNetplaySessionResponse{Session: joined, PlayerURL: playerURL, Role: "guest", ExpiresAt: joined.ExpiresAt})
}

func (s *Server) webNetplayRoomPassword(sessionID string) string {
	mac := hmac.New(sha256.New, s.importKey[:])
	_, _ = mac.Write([]byte("web-netplay-room:" + sessionID))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func (s *Server) createWebNetplayPlayerURL(session multiplayer.Session, artifact catalog.Artifact, edition catalog.Edition, locale, role, displayName string) (string, error) {
	token, err := s.signWebEmulationToken(webEmulationToken{
		ArtifactID: artifact.ID, EditionID: edition.ID, SHA256: artifact.SHA256, Core: session.Runtime.Core,
		DriverID: webEmulationDriverID(session.Runtime.Core), Locale: webEmulationLocale(locale), ExpiresAt: session.ExpiresAt.Unix(),
		NetplaySessionID: session.ID, NetplayRole: role, NetplayRoom: "Varkiv-" + session.ID,
		NetplayPassword: s.webNetplayRoomPassword(session.ID), NetplayPlayer: displayName,
	})
	if err != nil {
		return "", err
	}
	return "/play-netplay/" + token, nil
}

func webNetplayEmulatorLocale(locale string) string {
	switch webEmulationLocale(locale) {
	case "ja":
		return "ja"
	case "zh-CN", "zh-TW":
		return "zh"
	default:
		return "en"
	}
}

func (s *Server) webNetplayPlayer(w http.ResponseWriter, r *http.Request) {
	payload, err := s.parseWebEmulationToken(r.PathValue("token"))
	if err != nil || payload.NetplaySessionID == "" || (payload.NetplayRole != "host" && payload.NetplayRole != "guest") || payload.NetplayRoom == "" || payload.NetplayPassword == "" || payload.NetplayPlayer == "" {
		http.Error(w, "This web-netplay session is invalid or expired.", http.StatusUnauthorized)
		return
	}
	session, err := s.multiplayer.Get(payload.NetplaySessionID)
	if err != nil || session.ProfileID != multiplayer.ProfileEmulatorJS || session.Runtime != webNetplayRuntime() || session.SavePolicy != "no-persist" {
		http.Error(w, "This web-netplay session is no longer available.", http.StatusGone)
		return
	}
	artifact, err := s.store.GetArtifact(r.Context(), payload.ArtifactID)
	if err != nil || artifact.EditionID != payload.EditionID || !strings.EqualFold(artifact.SHA256, payload.SHA256) || artifact.SHA256 != session.Content.SHA256 || artifact.Size != session.Content.Size {
		http.Error(w, "The ROM identity changed after this session was created.", http.StatusConflict)
		return
	}
	file, err := s.openVerifiedWebROM(artifact)
	if err != nil {
		http.Error(w, "The verified ROM is unavailable.", http.StatusConflict)
		return
	}
	_ = file.Close()
	edition, err := s.store.GetEdition(r.Context(), payload.EditionID, payload.Locale)
	if err != nil {
		http.Error(w, "The selected game version is unavailable.", http.StatusConflict)
		return
	}
	var nonce [18]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		http.Error(w, "Unable to create player.", http.StatusInternalServerError)
		return
	}
	nonceText := base64.RawURLEncoding.EncodeToString(nonce[:])
	assets := webAssetDirectory(s.webNetplayAssets)
	romName := url.PathEscape(filepath.Base(filepath.FromSlash(artifact.OriginalName)))
	if romName == "." || romName == "" {
		romName = url.PathEscape(filepath.Base(filepath.FromSlash(artifact.Path)))
	}
	gameID, _ := strconv.ParseUint(artifact.SHA256[:13], 16, 64)
	options := map[string]any{
		"EJS_player": "#game", "EJS_core": session.Runtime.Core,
		"EJS_gameUrl":    "/api/v1/web-emulation/content/" + r.PathValue("token") + "/" + romName,
		"EJS_pathtodata": assets, "EJS_gameName": edition.DisplayTitle, "EJS_gameID": gameID,
		"EJS_language": webNetplayEmulatorLocale(payload.Locale), "EJS_disableAutoLang": true,
		"EJS_startOnLoaded": false, "EJS_noAutoFocus": false, "EJS_threads": false,
		"EJS_netplayServer": "same-origin", "EJS_netplayICEServers": s.webNetplayICEServers,
	}
	labels := webPlayerLabels(payload.Locale)
	configJSON, err := json.Marshal(map[string]any{
		"loader": assets + "loader.js", "saveUrl": "", "baseRevision": "", "labels": labels, "options": options,
		"netplay": map[string]any{"session_id": session.ID, "role": payload.NetplayRole, "room": payload.NetplayRoom, "password": payload.NetplayPassword, "player": payload.NetplayPlayer},
	})
	if err != nil {
		http.Error(w, "Unable to create player.", http.StatusInternalServerError)
		return
	}
	assetOrigin := "'self'"
	if parsed, parseErr := url.Parse(assets); parseErr == nil && parsed.IsAbs() {
		assetOrigin = parsed.Scheme + "://" + parsed.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' "+assetOrigin+" blob: 'nonce-"+nonceText+"' 'wasm-unsafe-eval' 'unsafe-eval'; style-src 'self' "+assetOrigin+" 'nonce-"+nonceText+"' 'unsafe-inline'; img-src 'self' "+assetOrigin+" data: blob:; media-src 'self' blob:; connect-src 'self' "+assetOrigin+" data: blob:; worker-src 'self' blob:; font-src 'self' "+assetOrigin+" data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(http.StatusOK)
	_ = webPlayerTemplate.Execute(w, map[string]any{"Lang": payload.Locale, "Title": edition.DisplayTitle, "Loading": labels["loading"], "Nonce": nonceText, "Config": template.JS(configJSON)})
}
