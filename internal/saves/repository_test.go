package saves

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("injected read failure")
}

func TestSaveHistoryDeduplicatesAndPreservesConflicts(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	w, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	e, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: w.ID, DefaultTitle: "Game", EditionType: "original"})
	deviceA, _ := store.CreateDevice(ctx, catalog.NewDevice{Name: "A", OSFamily: "linux", Distribution: "rocknix", Architecture: "aarch64"})
	deviceB, _ := store.CreateDevice(ctx, catalog.NewDevice{Name: "B", OSFamily: "android", Distribution: "android", Architecture: "aarch64"})
	repo, err := New(store, filepath.Join(t.TempDir(), "saves"))
	if err != nil {
		t.Fatal(err)
	}
	base := PushInput{EditionID: e.ID, DeviceID: deviceA.ID, DriverID: "retroarch", RelativePath: "game/game.srm", ScopeType: "game"}
	first, err := repo.Push(ctx, base, bytes.NewBufferString("one"))
	if err != nil || !first.Created || first.Conflict {
		t.Fatalf("first push: %#v %v", first, err)
	}
	duplicate, err := repo.Push(ctx, base, bytes.NewBufferString("one"))
	if err != nil || duplicate.Created || duplicate.Revision.ID != first.Revision.ID {
		t.Fatalf("duplicate push: %#v %v", duplicate, err)
	}
	secondInput := base
	secondInput.DeviceID = deviceB.ID
	secondInput.BaseRevisionID = first.Revision.ID
	second, err := repo.Push(ctx, secondInput, bytes.NewBufferString("two"))
	if err != nil || !second.Created || second.Conflict {
		t.Fatalf("linear push: %#v %v", second, err)
	}
	conflictInput := base
	conflictInput.BaseRevisionID = first.Revision.ID
	third, err := repo.Push(ctx, conflictInput, bytes.NewBufferString("three"))
	if err != nil || !third.Created || !third.Conflict {
		t.Fatalf("conflicting push: %#v %v", third, err)
	}
	file, revision, err := repo.OpenRevision(ctx, third.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	if string(data) != "three" || !revision.Conflict {
		t.Fatalf("downloaded wrong revision: %q %#v", data, revision)
	}
	revisions, err := store.ListSaveRevisions(ctx, e.ID)
	if err != nil || len(revisions) != 3 {
		t.Fatalf("history: %d %v", len(revisions), err)
	}
}

func TestSaveBaseRevisionCannotCrossStreams(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	gameA, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "A", Platform: "nes"})
	editionA, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: gameA.ID, DefaultTitle: "A", EditionType: "original"})
	gameB, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "B", Platform: "nes"})
	editionB, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: gameB.ID, DefaultTitle: "B", EditionType: "original"})
	device, _ := store.CreateDevice(ctx, catalog.NewDevice{Name: "Browser", OSFamily: "web"})
	repo, err := New(store, filepath.Join(t.TempDir(), "saves"))
	if err != nil {
		t.Fatal(err)
	}
	firstA, err := repo.Push(ctx, PushInput{EditionID: editionA.ID, DeviceID: device.ID, DriverID: "web-core", RelativePath: "battery.sav"}, strings.NewReader("a-one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Push(ctx, PushInput{EditionID: editionB.ID, DeviceID: device.ID, DriverID: "web-core", RelativePath: "battery.sav", BaseRevisionID: firstA.Revision.ID}, strings.NewReader("b-invalid")); err == nil || !strings.Contains(err.Error(), "new save stream") {
		t.Fatalf("foreign base was accepted for a new stream: %v", err)
	}
	firstB, err := repo.Push(ctx, PushInput{EditionID: editionB.ID, DeviceID: device.ID, DriverID: "web-core", RelativePath: "battery.sav"}, strings.NewReader("b-one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Push(ctx, PushInput{EditionID: editionB.ID, DeviceID: device.ID, DriverID: "web-core", RelativePath: "battery.sav", BaseRevisionID: firstA.Revision.ID}, strings.NewReader("b-invalid-two")); err == nil || !strings.Contains(err.Error(), "different save stream") {
		t.Fatalf("foreign base was accepted for an existing stream: %v", err)
	}
	revisions, err := store.ListSaveRevisions(ctx, editionB.ID)
	if err != nil || len(revisions) != 1 || revisions[0].ID != firstB.Revision.ID {
		t.Fatalf("foreign base changed stream B: %#v, %v", revisions, err)
	}
}

func TestSavePathCannotEscapeRepository(t *testing.T) {
	store, _ := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	defer store.Close()
	repo, _ := New(store, filepath.Join(t.TempDir(), "saves"))
	for _, logical := range []string{"../escape.srm", `C:\private\save.srm`, "C:/private/save.srm", "/absolute", "CON", "bad?.sav"} {
		_, err := repo.Push(context.Background(), PushInput{EditionID: "x", DeviceID: "y", RelativePath: logical}, bytes.NewBufferString("data"))
		if err == nil {
			t.Fatalf("expected portable-path rejection for %q", logical)
		}
		if strings.Contains(err.Error(), logical) {
			t.Fatalf("portable-path error disclosed the rejected path %q: %v", logical, err)
		}
	}
}

func TestCopyIncomingSaveEnforcesAggregateBudgetWithoutPartialAcceptance(t *testing.T) {
	var output bytes.Buffer
	written, err := copyIncomingSave(&output, strings.NewReader("1234"), 3)
	if !errors.Is(err, errRevisionLimit) || written != 4 {
		t.Fatalf("oversize copy = %d, %v", written, err)
	}
	output.Reset()
	written, err = copyIncomingSave(&output, strings.NewReader("123"), 3)
	if err != nil || written != 3 || output.String() != "123" {
		t.Fatalf("boundary copy = %d, %v, %q", written, err, output.String())
	}
	if _, err = copyIncomingSave(io.Discard, strings.NewReader(""), -1); !errors.Is(err, errRevisionLimit) {
		t.Fatalf("negative remaining budget was accepted: %v", err)
	}
}

func TestSaveRepositoryRejectsCorruptDeduplicatedBlobWithoutNewRevision(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Game", Platform: "gba"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	device, _ := store.CreateDevice(ctx, catalog.NewDevice{Name: "Device", OSFamily: "windows", Architecture: "x86_64"})
	repo, err := New(store, filepath.Join(t.TempDir(), "saves"))
	if err != nil {
		t.Fatal(err)
	}
	input := PushInput{EditionID: edition.ID, DeviceID: device.ID, DriverID: "retroarch", RelativePath: "game.srm"}
	first, err := repo.Push(ctx, input, bytes.NewBufferString("expected-save"))
	if err != nil {
		t.Fatal(err)
	}
	blob := first.Revision.Files[0].BlobPath
	corrupt := strings.Repeat("x", len("expected-save"))
	if err = os.WriteFile(blob, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Push(ctx, input, bytes.NewBufferString("expected-save")); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatalf("corrupt deduplicated save blob was accepted: %v", err)
	}
	revisions, listErr := store.ListSaveRevisions(ctx, edition.ID)
	if listErr != nil || len(revisions) != 1 {
		t.Fatalf("corrupt blob attempt changed revision history: %d %v", len(revisions), listErr)
	}
	remaining, readErr := os.ReadFile(blob)
	if readErr != nil || string(remaining) != corrupt {
		t.Fatalf("corrupt save blob was silently replaced: %q %v", remaining, readErr)
	}
	if _, _, err = repo.OpenRevision(ctx, first.Revision.ID); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatalf("corrupt save blob was served: %v", err)
	}
}

func TestMultiFileRevisionIsAtomicDeduplicatedAndConflictPreserving(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Memory card game", Platform: "ps2"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	deviceA, _ := store.CreateDevice(ctx, catalog.NewDevice{Name: "Windows A", OSFamily: "windows", Architecture: "x86_64"})
	deviceB, _ := store.CreateDevice(ctx, catalog.NewDevice{Name: "Windows B", OSFamily: "windows", Architecture: "x86_64"})
	root := filepath.Join(t.TempDir(), "saves")
	repo, err := New(store, root)
	if err != nil {
		t.Fatal(err)
	}
	base := PushSetInput{
		EditionID: edition.ID, DeviceID: deviceA.ID, DriverID: "pcsx2", ScopeType: "container", ScopeKey: "card-slot-1",
		Files: []IncomingFile{
			{LogicalPath: "cards/Mcd001.ps2", Reader: bytes.NewBufferString("card-one"), MTimeNS: 100},
			{LogicalPath: "states/index.json", Reader: bytes.NewBufferString("index-one"), MTimeNS: 101},
		},
	}
	first, err := repo.PushSet(ctx, base)
	if err != nil || !first.Created || first.Conflict || first.Revision.FileCount != 2 || len(first.Revision.Files) != 2 || first.Revision.Status != "current" {
		t.Fatalf("first multi-file revision = %#v, %v", first, err)
	}
	if _, _, err = repo.OpenRevision(ctx, first.Revision.ID); err == nil || !strings.Contains(err.Error(), "one file at a time") {
		t.Fatalf("multi-file legacy download was not rejected: %v", err)
	}
	for _, item := range first.Revision.Files {
		file, metadata, openErr := repo.OpenRevisionFile(ctx, first.Revision.ID, item.ID)
		if openErr != nil {
			t.Fatal(openErr)
		}
		content, _ := io.ReadAll(file)
		file.Close()
		if metadata.LogicalPath == "cards/Mcd001.ps2" && string(content) != "card-one" {
			t.Fatalf("wrong card bytes: %q", content)
		}
	}

	duplicate := base
	duplicate.Files = []IncomingFile{
		{LogicalPath: "states/index.json", Reader: bytes.NewBufferString("index-one"), MTimeNS: 999},
		{LogicalPath: "cards/Mcd001.ps2", Reader: bytes.NewBufferString("card-one"), MTimeNS: 998},
	}
	same, err := repo.PushSet(ctx, duplicate)
	if err != nil || same.Created || same.Revision.ID != first.Revision.ID {
		t.Fatalf("reordered identical revision was not deduplicated: %#v, %v", same, err)
	}

	linear := base
	linear.DeviceID = deviceB.ID
	linear.BaseRevisionID = first.Revision.ID
	linear.Files = []IncomingFile{
		{LogicalPath: "cards/Mcd001.ps2", Reader: bytes.NewBufferString("card-two")},
		{LogicalPath: "states/index.json", Reader: bytes.NewBufferString("index-one")},
	}
	second, err := repo.PushSet(ctx, linear)
	if err != nil || !second.Created || second.Conflict || second.Revision.Status != "current" {
		t.Fatalf("linear multi-file revision = %#v, %v", second, err)
	}
	old, _ := store.GetSaveRevision(ctx, first.Revision.ID)
	if old.Status != "superseded" {
		t.Fatalf("old current was not superseded: %#v", old)
	}

	stale := base
	stale.BaseRevisionID = first.Revision.ID
	stale.Files = []IncomingFile{
		{LogicalPath: "cards/Mcd001.ps2", Reader: bytes.NewBufferString("card-conflict")},
		{LogicalPath: "states/index.json", Reader: bytes.NewBufferString("index-conflict")},
	}
	conflict, err := repo.PushSet(ctx, stale)
	if err != nil || !conflict.Created || !conflict.Conflict || conflict.Revision.Status != "conflict" {
		t.Fatalf("stale multi-file revision = %#v, %v", conflict, err)
	}
	current, _ := store.GetSaveRevision(ctx, second.Revision.ID)
	if current.Status != "current" {
		t.Fatalf("conflict replaced current revision: %#v", current)
	}

	before, _ := store.ListSaveRevisions(ctx, edition.ID)
	mismatched := base
	mismatched.ExpectedContentHash = strings.Repeat("f", 64)
	mismatched.Files = []IncomingFile{{LogicalPath: "cards/Mcd001.ps2", Reader: bytes.NewBufferString("must-not-commit")}}
	if _, err = repo.PushSet(ctx, mismatched); !errors.Is(err, ErrContentHashMismatch) {
		t.Fatalf("expected negotiated hash mismatch, got %v", err)
	}
	afterMismatch, _ := store.ListSaveRevisions(ctx, edition.ID)
	if len(afterMismatch) != len(before) {
		t.Fatalf("hash mismatch left a revision: before=%d after=%d", len(before), len(afterMismatch))
	}
	broken := base
	broken.Files = []IncomingFile{
		{LogicalPath: "cards/Mcd001.ps2", Reader: bytes.NewBufferString("never-commit")},
		{LogicalPath: "states/index.json", Reader: &failingReader{}},
	}
	if _, err = repo.PushSet(ctx, broken); err == nil || !strings.Contains(err.Error(), "injected read failure") {
		t.Fatalf("expected staged reader failure, got %v", err)
	}
	after, _ := store.ListSaveRevisions(ctx, edition.ID)
	if len(after) != len(before) {
		t.Fatalf("failed multi-file upload left a revision: before=%d after=%d", len(before), len(after))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".save-upload-") {
			t.Fatalf("failed multi-file upload left temporary file %s", entry.Name())
		}
	}
	streams, err := store.ListSaveStreams(ctx, edition.ID)
	if err != nil || len(streams) != 1 || streams[0].OwnerType != "container" || streams[0].OwnerKey != "card-slot-1" {
		t.Fatalf("container stream = %#v, %v", streams, err)
	}
}
