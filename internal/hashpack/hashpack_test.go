package hashpack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func fixtureRecord(hash, title string) Record {
	return Record{SHA256: hash, Size: 4096, Platform: "gba", GameKey: "game-key", GameDefaultTitle: title, GameTitles: map[string]string{"zh-CN": "测试游戏"}, EditionDefaultTitle: "Original", EditionType: "original", Languages: []string{"en", "ja"}, Role: "rom"}
}

func decodeFixtureArchive(t *testing.T, recordData []byte, recordCount int, manifestSuffix []byte, entryModes map[string]os.FileMode) []byte {
	t.Helper()
	source := Source{ID: "fixture", Name: "Fixture", License: "CC0-1.0"}
	recordSum := sha256.Sum256(recordData)
	recordsSHA256 := hex.EncodeToString(recordSum[:])
	manifest := Manifest{
		FormatVersion: FormatVersion,
		PackID:        packIdentity(source, "1", recordsSHA256),
		Source:        source,
		Release:       "1",
		CreatedAt:     time.Unix(1, 0).UTC(),
		RecordCount:   recordCount,
		RecordsSHA256: recordsSHA256,
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestData = append(manifestData, manifestSuffix...)

	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, item := range []struct {
		name string
		data []byte
	}{{ManifestName, manifestData}, {RecordsName, recordData}} {
		header := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
		mode := os.FileMode(0o644)
		if configured, ok := entryModes[item.name]; ok {
			mode = configured
		}
		header.SetMode(mode)
		entry, createErr := writer.CreateHeader(header)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = entry.Write(item.data); createErr != nil {
			t.Fatal(createErr)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestEncodeDecodeRoundTripAndPrivacyBoundary(t *testing.T) {
	record := fixtureRecord(strings.Repeat("a", 64), "Fixture")
	data, manifest, err := Encode(Source{ID: "example.fixture", Name: "Fixture identities", Publisher: "Example", License: "CC0-1.0"}, "2026.09", time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC), []Record{record})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RecordCount != 1 || manifest.PackID == "" || manifest.RecordsSHA256 == "" {
		t.Fatalf("incomplete manifest: %#v", manifest)
	}
	for _, forbidden := range []string{"/" + "Users/example", "library.db", "save", "device"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("encoded hash pack leaked %q", forbidden)
		}
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Manifest.PackID != manifest.PackID || len(decoded.Records) != 1 || decoded.Records[0].SHA256 != record.SHA256 || decoded.Records[0].GameTitles["zh-CN"] != "测试游戏" {
		t.Fatalf("round trip changed pack: %#v", decoded)
	}
}

func TestDecodeRejectsUnexpectedArchiveEntries(t *testing.T) {
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	entry, _ := writer.Create("../rom.gba")
	_, _ = entry.Write([]byte("not a ROM, but still forbidden"))
	_ = writer.Close()
	if _, err := Decode(out.Bytes()); err == nil {
		t.Fatal("archive with an unexpected path was accepted")
	}
}

func TestDecodeRejectsNonRegularArchiveEntries(t *testing.T) {
	recordData, _, err := recordsPayload([]Record{fixtureRecord(strings.Repeat("d", 64), "Fixture")})
	if err != nil {
		t.Fatal(err)
	}
	data := decodeFixtureArchive(t, recordData, 1, nil, map[string]os.FileMode{RecordsName: os.ModeSymlink | 0o777})
	if _, err = Decode(data); err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("expected non-regular ZIP entry rejection, got %v", err)
	}
}

func TestDecodeRejectsTrailingManifestValue(t *testing.T) {
	recordData, _, err := recordsPayload([]Record{fixtureRecord(strings.Repeat("e", 64), "Fixture")})
	if err != nil {
		t.Fatal(err)
	}
	data := decodeFixtureArchive(t, recordData, 1, []byte("\n{}"), nil)
	if _, err = Decode(data); err == nil || !strings.Contains(err.Error(), "exactly one JSON value") {
		t.Fatalf("expected trailing manifest value rejection, got %v", err)
	}
}

func TestDecodeRejectsNonCanonicalNDJSONFraming(t *testing.T) {
	recordData, _, err := recordsPayload([]Record{fixtureRecord(strings.Repeat("f", 64), "Fixture")})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		payload []byte
		message string
	}{
		{name: "unterminated final line", payload: bytes.TrimSuffix(recordData, []byte{'\n'}), message: "must end"},
		{name: "blank line", payload: append(append([]byte{}, recordData...), '\n'), message: "blank"},
		{name: "insignificant whitespace", payload: append([]byte{' '}, recordData...), message: "compact JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := decodeFixtureArchive(t, test.payload, 1, nil, nil)
			if _, decodeErr := Decode(data); decodeErr == nil || !strings.Contains(decodeErr.Error(), test.message) {
				t.Fatalf("expected %q rejection, got %v", test.message, decodeErr)
			}
		})
	}
}

func TestDecodeRejectsUnknownRecordFields(t *testing.T) {
	record := fixtureRecord(strings.Repeat("c", 64), "Fixture")
	_, manifest, err := Encode(Source{ID: "fixture", Name: "Fixture", License: "CC0-1.0"}, "1", time.Unix(1, 0), []Record{record})
	if err != nil {
		t.Fatal(err)
	}
	recordJSON, err := json.Marshal(struct {
		Record
		LocalPath string `json:"local_path"`
	}{Record: record, LocalPath: "/" + "Users/example/roms/game.gba"})
	if err != nil {
		t.Fatal(err)
	}
	recordJSON = append(recordJSON, '\n')
	recordSum := sha256.Sum256(recordJSON)
	manifest.RecordsSHA256 = hex.EncodeToString(recordSum[:])
	manifest.PackID = packIdentity(manifest.Source, manifest.Release, manifest.RecordsSHA256)
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, content := range map[string][]byte{ManifestName: manifestJSON, RecordsName: recordJSON} {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = entry.Write(content); createErr != nil {
			t.Fatal(createErr)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(out.Bytes()); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown record field rejection, got %v", err)
	}
}

func TestEncodeRejectsDuplicateAndInvalidIdentities(t *testing.T) {
	record := fixtureRecord(strings.Repeat("b", 64), "Fixture")
	if _, _, err := Encode(Source{ID: "fixture", Name: "Fixture", License: "CC0-1.0"}, "1", time.Now(), []Record{record, record}); err == nil {
		t.Fatal("duplicate SHA-256 was accepted")
	}
	record.SHA256 = "not-a-hash"
	if _, _, err := Encode(Source{ID: "fixture", Name: "Fixture", License: "CC0-1.0"}, "1", time.Now(), []Record{record}); err == nil {
		t.Fatal("invalid SHA-256 was accepted")
	}
}
