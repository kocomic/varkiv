// Package multiplayer owns Varkiv's short-lived, emulator-aware session
// coordination protocol. It deliberately has no dependency on the catalog or
// HTTP server so it can later move behind a separate process without changing
// the public contract.
package multiplayer

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion   = "v1"
	ProfileRetroArch  = "retroarch-netplay-v1"
	ProfileEmulatorJS = "emulatorjs-webrtc-v1"
	SessionLifetime   = 4 * time.Hour
)

var (
	ErrNotFound     = errors.New("multiplayer session not found")
	ErrExpired      = errors.New("multiplayer session expired")
	ErrClosed       = errors.New("multiplayer session closed")
	ErrInvalidToken = errors.New("invalid multiplayer invitation token")
	ErrFull         = errors.New("multiplayer session is full")
)

type ContentIdentity struct {
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Platform string `json:"platform"`
}

type RuntimeIdentity struct {
	Emulator    string `json:"emulator"`
	Version     string `json:"version"`
	Core        string `json:"core"`
	CoreVersion string `json:"core_version"`
}

type ParticipantInput struct {
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name"`
}

type Participant struct {
	ClientID    string    `json:"client_id"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
}

type CreateSessionInput struct {
	ProfileID  string           `json:"profile_id"`
	Content    ContentIdentity  `json:"content"`
	Runtime    RuntimeIdentity  `json:"runtime"`
	Transport  string           `json:"transport"`
	SavePolicy string           `json:"save_policy"`
	Host       ParticipantInput `json:"host"`
}

type JoinSessionInput struct {
	JoinToken string           `json:"join_token"`
	Content   ContentIdentity  `json:"content"`
	Runtime   RuntimeIdentity  `json:"runtime"`
	Client    ParticipantInput `json:"client"`
}

type Session struct {
	ID           string          `json:"id"`
	Protocol     string          `json:"protocol_version"`
	ProfileID    string          `json:"profile_id"`
	State        string          `json:"state"`
	Content      ContentIdentity `json:"content"`
	Runtime      RuntimeIdentity `json:"runtime"`
	Transport    string          `json:"transport"`
	SavePolicy   string          `json:"save_policy"`
	Participants []Participant   `json:"participants"`
	CreatedAt    time.Time       `json:"created_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

type CreatedSession struct {
	Session   Session `json:"session"`
	JoinToken string  `json:"join_token"`
}

type CompatibilityError struct {
	Fields []string
}

func (e *CompatibilityError) Error() string {
	return "multiplayer compatibility mismatch: " + strings.Join(e.Fields, ", ")
}

type storedSession struct {
	session   Session
	tokenHash [sha256.Size]byte
}

type Broker struct {
	mu       sync.Mutex
	sessions map[string]*storedSession
	now      func() time.Time
}

func NewBroker() *Broker {
	return &Broker{sessions: make(map[string]*storedSession), now: time.Now}
}

func (b *Broker) Create(input CreateSessionInput) (CreatedSession, error) {
	input = normalizeCreate(input)
	if err := validateCreate(input); err != nil {
		return CreatedSession{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("create session id: %w", err)
	}
	joinToken, err := randomToken(32)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("create join token: %w", err)
	}
	now := b.now().UTC()
	session := Session{
		ID: id, Protocol: ProtocolVersion, ProfileID: input.ProfileID, State: "waiting",
		Content: input.Content, Runtime: input.Runtime, Transport: input.Transport,
		SavePolicy: input.SavePolicy, CreatedAt: now, ExpiresAt: now.Add(SessionLifetime),
		Participants: []Participant{{ClientID: input.Host.ClientID, DisplayName: input.Host.DisplayName, Role: "host", JoinedAt: now}},
	}
	b.mu.Lock()
	b.sessions[id] = &storedSession{session: session, tokenHash: sha256.Sum256([]byte(joinToken))}
	b.mu.Unlock()
	return CreatedSession{Session: cloneSession(session), JoinToken: joinToken}, nil
}

func (b *Broker) Get(id string) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, err := b.active(strings.TrimSpace(id))
	if err != nil {
		return Session{}, err
	}
	return cloneSession(stored.session), nil
}

func (b *Broker) Join(id string, input JoinSessionInput) (Session, error) {
	input = normalizeJoin(input)
	if err := validateParticipant(input.Client); err != nil {
		return Session{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, err := b.active(strings.TrimSpace(id))
	if err != nil {
		return Session{}, err
	}
	provided := sha256.Sum256([]byte(input.JoinToken))
	if subtle.ConstantTimeCompare(provided[:], stored.tokenHash[:]) != 1 {
		return Session{}, ErrInvalidToken
	}
	if fields := compatibilityMismatches(stored.session, input); len(fields) > 0 {
		return Session{}, &CompatibilityError{Fields: fields}
	}
	for _, participant := range stored.session.Participants {
		if participant.ClientID == input.Client.ClientID {
			return cloneSession(stored.session), nil
		}
	}
	if len(stored.session.Participants) >= maxParticipants(stored.session.ProfileID) {
		return Session{}, ErrFull
	}
	stored.session.Participants = append(stored.session.Participants, Participant{ClientID: input.Client.ClientID, DisplayName: input.Client.DisplayName, Role: "player", JoinedAt: b.now().UTC()})
	if len(stored.session.Participants) > 1 {
		stored.session.State = "ready"
	}
	return cloneSession(stored.session), nil
}

func (b *Broker) Close(id string) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.sessions[strings.TrimSpace(id)]
	if !ok {
		return Session{}, ErrNotFound
	}
	if stored.session.State == "closed" {
		return cloneSession(stored.session), nil
	}
	stored.session.State = "closed"
	return cloneSession(stored.session), nil
}

func (b *Broker) active(id string) (*storedSession, error) {
	stored, ok := b.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	if stored.session.State == "closed" {
		return nil, ErrClosed
	}
	if !b.now().Before(stored.session.ExpiresAt) {
		delete(b.sessions, id)
		return nil, ErrExpired
	}
	return stored, nil
}

func normalizeCreate(input CreateSessionInput) CreateSessionInput {
	input.ProfileID = strings.ToLower(strings.TrimSpace(input.ProfileID))
	input.Content = normalizeContent(input.Content)
	input.Runtime = normalizeRuntime(input.Runtime)
	input.Transport = strings.ToLower(strings.TrimSpace(input.Transport))
	input.SavePolicy = strings.ToLower(strings.TrimSpace(input.SavePolicy))
	input.Host = normalizeParticipant(input.Host)
	return input
}

func normalizeJoin(input JoinSessionInput) JoinSessionInput {
	input.JoinToken = strings.TrimSpace(input.JoinToken)
	input.Content = normalizeContent(input.Content)
	input.Runtime = normalizeRuntime(input.Runtime)
	input.Client = normalizeParticipant(input.Client)
	return input
}

func normalizeContent(value ContentIdentity) ContentIdentity {
	value.SHA256 = strings.ToLower(strings.TrimSpace(value.SHA256))
	value.Platform = strings.ToLower(strings.TrimSpace(value.Platform))
	return value
}

func normalizeRuntime(value RuntimeIdentity) RuntimeIdentity {
	value.Emulator = strings.ToLower(strings.TrimSpace(value.Emulator))
	value.Version = strings.TrimSpace(value.Version)
	value.Core = strings.ToLower(strings.TrimSpace(value.Core))
	value.CoreVersion = strings.TrimSpace(value.CoreVersion)
	return value
}

func normalizeParticipant(value ParticipantInput) ParticipantInput {
	value.ClientID = strings.TrimSpace(value.ClientID)
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	return value
}

func validateCreate(input CreateSessionInput) error {
	if input.ProfileID != ProfileRetroArch && input.ProfileID != ProfileEmulatorJS {
		return errors.New("unsupported multiplayer profile")
	}
	if err := validateContent(input.Content); err != nil {
		return err
	}
	if err := validateRuntime(input.ProfileID, input.Runtime); err != nil {
		return err
	}
	if input.Transport != "relay" && input.Transport != "direct" {
		return errors.New("transport must be relay or direct")
	}
	if input.SavePolicy != "isolated" && input.SavePolicy != "host-authoritative" && input.SavePolicy != "no-persist" {
		return errors.New("unsupported save policy")
	}
	if input.ProfileID == ProfileEmulatorJS && (input.Transport != "direct" || input.SavePolicy != "no-persist") {
		return errors.New("EmulatorJS WebRTC requires direct transport and no-persist saves")
	}
	return validateParticipant(input.Host)
}

func maxParticipants(profileID string) int {
	if profileID == ProfileEmulatorJS {
		return 2
	}
	return 4
}

func validateContent(value ContentIdentity) error {
	if len(value.SHA256) != 64 {
		return errors.New("content sha256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value.SHA256); err != nil {
		return errors.New("content sha256 must contain 64 hexadecimal characters")
	}
	if value.Size <= 0 || value.Platform == "" || len(value.Platform) > 64 {
		return errors.New("content size and platform are required")
	}
	return nil
}

func validateRuntime(profileID string, value RuntimeIdentity) error {
	wantEmulator := "retroarch"
	if profileID == ProfileEmulatorJS {
		wantEmulator = "emulatorjs"
	}
	if value.Emulator != wantEmulator || value.Version == "" || value.Core == "" || value.CoreVersion == "" {
		return fmt.Errorf("%s, emulator version, core, and core version are required", wantEmulator)
	}
	if len(value.Version) > 64 || len(value.Core) > 64 || len(value.CoreVersion) > 128 {
		return errors.New("runtime identity is too long")
	}
	return nil
}

func validateParticipant(value ParticipantInput) error {
	if value.ClientID == "" || len(value.ClientID) > 128 || value.DisplayName == "" || len(value.DisplayName) > 80 {
		return errors.New("client id and display name are required")
	}
	return nil
}

func compatibilityMismatches(session Session, input JoinSessionInput) []string {
	fields := make([]string, 0, 7)
	if input.Content.SHA256 != session.Content.SHA256 {
		fields = append(fields, "content.sha256")
	}
	if input.Content.Size != session.Content.Size {
		fields = append(fields, "content.size")
	}
	if input.Content.Platform != session.Content.Platform {
		fields = append(fields, "content.platform")
	}
	if input.Runtime.Emulator != session.Runtime.Emulator {
		fields = append(fields, "runtime.emulator")
	}
	if input.Runtime.Version != session.Runtime.Version {
		fields = append(fields, "runtime.version")
	}
	if input.Runtime.Core != session.Runtime.Core {
		fields = append(fields, "runtime.core")
	}
	if input.Runtime.CoreVersion != session.Runtime.CoreVersion {
		fields = append(fields, "runtime.core_version")
	}
	sort.Strings(fields)
	return fields
}

func cloneSession(value Session) Session {
	value.Participants = append([]Participant(nil), value.Participants...)
	return value
}

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
