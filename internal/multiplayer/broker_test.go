package multiplayer

import (
	"errors"
	"testing"
	"time"
)

func validCreateInput() CreateSessionInput {
	return CreateSessionInput{
		ProfileID: ProfileRetroArch,
		Content:   ContentIdentity{SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 131072, Platform: "nes"},
		Runtime:   RuntimeIdentity{Emulator: "retroarch", Version: "1.22.2", Core: "fceumm", CoreVersion: "git-abc123"},
		Transport: "relay", SavePolicy: "isolated",
		Host: ParticipantInput{ClientID: "host-1", DisplayName: "Host"},
	}
}

func TestBrokerRequiresExactCompatibilityAndDoesNotMutateOnFailure(t *testing.T) {
	broker := NewBroker()
	created, err := broker.Create(validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	join := JoinSessionInput{JoinToken: created.JoinToken, Content: created.Session.Content, Runtime: created.Session.Runtime, Client: ParticipantInput{ClientID: "client-1", DisplayName: "Guest"}}
	join.Runtime.CoreVersion = "different"
	_, err = broker.Join(created.Session.ID, join)
	var mismatch *CompatibilityError
	if !errors.As(err, &mismatch) || len(mismatch.Fields) != 1 || mismatch.Fields[0] != "runtime.core_version" {
		t.Fatalf("mismatch = %#v, %v", mismatch, err)
	}
	current, err := broker.Get(created.Session.ID)
	if err != nil || len(current.Participants) != 1 || current.State != "waiting" {
		t.Fatalf("session mutated after rejected join: %#v, %v", current, err)
	}
	join.Runtime = created.Session.Runtime
	joined, err := broker.Join(created.Session.ID, join)
	if err != nil || len(joined.Participants) != 2 || joined.State != "ready" {
		t.Fatalf("joined = %#v, %v", joined, err)
	}
	joined.Participants[0].DisplayName = "mutated"
	current, _ = broker.Get(created.Session.ID)
	if current.Participants[0].DisplayName != "Host" {
		t.Fatal("returned session aliased broker state")
	}
}

func TestBrokerInvitationAndExpiryBoundaries(t *testing.T) {
	broker := NewBroker()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	broker.now = func() time.Time { return now }
	created, err := broker.Create(validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	join := JoinSessionInput{JoinToken: "wrong", Content: created.Session.Content, Runtime: created.Session.Runtime, Client: ParticipantInput{ClientID: "client-1", DisplayName: "Guest"}}
	if _, err = broker.Join(created.Session.ID, join); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid token = %v", err)
	}
	now = now.Add(SessionLifetime)
	if _, err = broker.Get(created.Session.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry = %v", err)
	}
	if _, err = broker.Get(created.Session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session retained: %v", err)
	}
}

func TestBrokerRejectsIncompleteIdentity(t *testing.T) {
	input := validCreateInput()
	input.Content.SHA256 = "metadata-only"
	if _, err := NewBroker().Create(input); err == nil {
		t.Fatal("session accepted without a content hash")
	}
	input = validCreateInput()
	input.Runtime.CoreVersion = ""
	if _, err := NewBroker().Create(input); err == nil {
		t.Fatal("session accepted without a core version")
	}
}

func TestBrokerAcceptsEmulatorJSProfileAndRejectsRuntimeFamilyDrift(t *testing.T) {
	input := validCreateInput()
	input.ProfileID = ProfileEmulatorJS
	input.Runtime = RuntimeIdentity{Emulator: "emulatorjs", Version: "4.3.0-pre", Core: "fceumm", CoreVersion: "sha256:core-build"}
	input.Transport = "direct"
	input.SavePolicy = "no-persist"
	created, err := NewBroker().Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.ProfileID != ProfileEmulatorJS || created.Session.Runtime.Emulator != "emulatorjs" {
		t.Fatalf("unexpected session: %#v", created.Session)
	}
	input.Runtime.Emulator = "retroarch"
	if _, err = NewBroker().Create(input); err == nil {
		t.Fatal("EmulatorJS profile accepted a RetroArch runtime identity")
	}
}
