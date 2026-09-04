// Package hashpack implements Varkiv's privacy-minimized ROM identity
// exchange format. A hash pack never contains ROM bytes, local paths, saves,
// media, devices, or play history.
package hashpack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	FormatVersion   = 1
	ManifestName    = "manifest.json"
	RecordsName     = "records.ndjson"
	MaxPackBytes    = 64 << 20
	MaxRecordsBytes = 128 << 20
	MaxRecords      = 1_000_000
)

var (
	portableID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hex32      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex8       = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

type Source struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Publisher string `json:"publisher,omitempty"`
	License   string `json:"license"`
}

type Manifest struct {
	FormatVersion int       `json:"format_version"`
	PackID        string    `json:"pack_id"`
	Source        Source    `json:"source"`
	Release       string    `json:"release"`
	CreatedAt     time.Time `json:"created_at"`
	RecordCount   int       `json:"record_count"`
	RecordsSHA256 string    `json:"records_sha256"`
}

// Record deliberately excludes every machine-local or behavioral field.
// GameKey is only a source-scoped grouping hint; SHA256 remains the ROM identity.
type Record struct {
	SHA256              string            `json:"sha256"`
	Size                int64             `json:"size"`
	CRC32               string            `json:"crc32,omitempty"`
	MD5                 string            `json:"md5,omitempty"`
	Platform            string            `json:"platform"`
	GameKey             string            `json:"game_key"`
	GameDefaultTitle    string            `json:"game_default_title"`
	GameTitles          map[string]string `json:"game_titles,omitempty"`
	EditionDefaultTitle string            `json:"edition_default_title"`
	EditionTitles       map[string]string `json:"edition_titles,omitempty"`
	EditionType         string            `json:"edition_type"`
	Version             string            `json:"version,omitempty"`
	Languages           []string          `json:"languages,omitempty"`
	Author              string            `json:"author,omitempty"`
	Region              string            `json:"region,omitempty"`
	Serial              string            `json:"serial,omitempty"`
	ProductCode         string            `json:"product_code,omitempty"`
	TitleID             string            `json:"title_id,omitempty"`
	Role                string            `json:"role"`
	DiscIndex           int               `json:"disc_index,omitempty"`
	ParentSHA256        string            `json:"parent_sha256,omitempty"`
}

type Pack struct {
	Manifest Manifest
	Records  []Record
}

func packIdentity(source Source, release, recordsSHA256 string) string {
	parts := []string{"varkiv-hashpack-v1", source.ID, source.Name, source.Publisher, source.License, release, recordsSHA256}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func normalizeSource(in Source) (Source, error) {
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	in.Name = strings.TrimSpace(in.Name)
	in.Publisher = strings.TrimSpace(in.Publisher)
	in.License = strings.TrimSpace(in.License)
	if !portableID.MatchString(in.ID) {
		return in, errors.New("source.id must be a portable lower-case identifier")
	}
	if in.Name == "" || len(in.Name) > 200 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		return in, errors.New("source.name is required and must not exceed 200 UTF-8 bytes")
	}
	if in.License == "" || len(in.License) > 200 || strings.IndexFunc(in.License, unicode.IsControl) >= 0 {
		return in, errors.New("source.license is required and must not exceed 200 UTF-8 bytes")
	}
	if len(in.Publisher) > 200 || strings.IndexFunc(in.Publisher, unicode.IsControl) >= 0 {
		return in, errors.New("source.publisher must not exceed 200 UTF-8 bytes")
	}
	return in, nil
}

func normalizeTitles(values map[string]string) map[string]string {
	out := map[string]string{}
	for locale, title := range values {
		locale, title = strings.TrimSpace(locale), strings.TrimSpace(title)
		if locale != "" && title != "" {
			out[locale] = title
		}
	}
	return out
}

func normalizeRecord(in Record) (Record, error) {
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	in.CRC32 = strings.ToLower(strings.TrimSpace(in.CRC32))
	in.MD5 = strings.ToLower(strings.TrimSpace(in.MD5))
	in.ParentSHA256 = strings.ToLower(strings.TrimSpace(in.ParentSHA256))
	in.Platform = strings.ToLower(strings.TrimSpace(in.Platform))
	in.GameKey = strings.TrimSpace(in.GameKey)
	in.GameDefaultTitle = strings.TrimSpace(in.GameDefaultTitle)
	in.EditionDefaultTitle = strings.TrimSpace(in.EditionDefaultTitle)
	in.EditionType = strings.ToLower(strings.TrimSpace(in.EditionType))
	in.Role = strings.ToLower(strings.TrimSpace(in.Role))
	in.GameTitles = normalizeTitles(in.GameTitles)
	in.EditionTitles = normalizeTitles(in.EditionTitles)
	if !hex64.MatchString(in.SHA256) {
		return in, errors.New("sha256 must contain 64 lower-case hexadecimal characters")
	}
	if in.Size <= 0 {
		return in, errors.New("size must be positive")
	}
	if in.CRC32 != "" && !hex8.MatchString(in.CRC32) {
		return in, errors.New("crc32 must contain 8 lower-case hexadecimal characters")
	}
	if in.MD5 != "" && !hex32.MatchString(in.MD5) {
		return in, errors.New("md5 must contain 32 lower-case hexadecimal characters")
	}
	if in.ParentSHA256 != "" && !hex64.MatchString(in.ParentSHA256) {
		return in, errors.New("parent_sha256 must contain 64 lower-case hexadecimal characters")
	}
	if in.Platform == "" || in.GameKey == "" || in.GameDefaultTitle == "" || in.EditionDefaultTitle == "" {
		return in, errors.New("platform, game_key, game_default_title, and edition_default_title are required")
	}
	if in.EditionType == "" {
		in.EditionType = "original"
	}
	if in.Role == "" {
		in.Role = "rom"
	}
	if in.Role != "rom" && in.Role != "disc" && in.Role != "executable" {
		return in, errors.New("role must be rom, disc, or executable")
	}
	if in.DiscIndex < 0 || in.DiscIndex > 64 {
		return in, errors.New("disc_index must be between 0 and 64")
	}
	languageSet := map[string]bool{}
	in.Languages = append([]string{}, in.Languages...)
	for index, value := range in.Languages {
		value = strings.TrimSpace(value)
		if value == "" || languageSet[value] {
			return in, errors.New("languages must contain unique non-empty values")
		}
		languageSet[value] = true
		in.Languages[index] = value
	}
	sort.Strings(in.Languages)
	return in, nil
}

func recordsPayload(records []Record) ([]byte, []Record, error) {
	if len(records) == 0 {
		return nil, nil, errors.New("hash pack must contain at least one record")
	}
	if len(records) > MaxRecords {
		return nil, nil, fmt.Errorf("hash pack exceeds %d records", MaxRecords)
	}
	normalized := make([]Record, len(records))
	seen := make(map[string]bool, len(records))
	var out bytes.Buffer
	for index, record := range records {
		var err error
		if normalized[index], err = normalizeRecord(record); err != nil {
			return nil, nil, fmt.Errorf("record %d: %w", index, err)
		}
		if seen[normalized[index].SHA256] {
			return nil, nil, fmt.Errorf("record %d repeats sha256 %s", index, normalized[index].SHA256)
		}
		seen[normalized[index].SHA256] = true
		data, err := json.Marshal(normalized[index])
		if err != nil {
			return nil, nil, err
		}
		if out.Len()+len(data)+1 > MaxRecordsBytes {
			return nil, nil, fmt.Errorf("hash pack records exceed %d bytes", MaxRecordsBytes)
		}
		out.Write(data)
		out.WriteByte('\n')
	}
	return out.Bytes(), normalized, nil
}

func Encode(source Source, release string, createdAt time.Time, records []Record) ([]byte, Manifest, error) {
	var err error
	if source, err = normalizeSource(source); err != nil {
		return nil, Manifest{}, err
	}
	release = strings.TrimSpace(release)
	if release == "" || len(release) > 128 || strings.IndexFunc(release, unicode.IsControl) >= 0 {
		return nil, Manifest{}, errors.New("release is required and must not exceed 128 UTF-8 bytes")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	createdAt = createdAt.UTC()
	recordData, normalized, err := recordsPayload(records)
	if err != nil {
		return nil, Manifest{}, err
	}
	recordsSum := sha256.Sum256(recordData)
	recordsSHA256 := hex.EncodeToString(recordsSum[:])
	manifest := Manifest{FormatVersion: FormatVersion, PackID: packIdentity(source, release, recordsSHA256), Source: source, Release: release, CreatedAt: createdAt, RecordCount: len(normalized), RecordsSHA256: recordsSHA256}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, Manifest{}, err
	}
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for _, entry := range []struct {
		name string
		data []byte
	}{{ManifestName, append(manifestData, '\n')}, {RecordsName, recordData}} {
		writer, createErr := archive.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Deflate})
		if createErr != nil {
			archive.Close()
			return nil, Manifest{}, createErr
		}
		if _, createErr = writer.Write(entry.data); createErr != nil {
			archive.Close()
			return nil, Manifest{}, createErr
		}
	}
	if err = archive.Close(); err != nil {
		return nil, Manifest{}, err
	}
	if out.Len() > MaxPackBytes {
		return nil, Manifest{}, fmt.Errorf("encoded hash pack exceeds %d bytes", MaxPackBytes)
	}
	return out.Bytes(), manifest, nil
}

func readZipEntry(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("hash pack entry exceeds its size limit")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("hash pack entry exceeds its size limit")
	}
	return data, nil
}

func decodeSingleJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document must contain exactly one JSON value")
		}
		return fmt.Errorf("trailing content after JSON value: %w", err)
	}
	return nil
}

func Decode(data []byte) (Pack, error) {
	if len(data) == 0 || len(data) > MaxPackBytes {
		return Pack{}, fmt.Errorf("hash pack must contain between 1 and %d bytes", MaxPackBytes)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Pack{}, errors.New("hash pack must be a valid ZIP archive")
	}
	if len(archive.File) != 2 {
		return Pack{}, errors.New("hash pack must contain exactly manifest.json and records.ndjson")
	}
	entries := map[string]*zip.File{}
	for _, file := range archive.File {
		if file.Name != ManifestName && file.Name != RecordsName {
			return Pack{}, fmt.Errorf("unexpected hash pack entry %q", file.Name)
		}
		if entries[file.Name] != nil || !file.FileInfo().Mode().IsRegular() {
			return Pack{}, fmt.Errorf("invalid duplicate or non-regular entry %q", file.Name)
		}
		entries[file.Name] = file
	}
	manifestData, err := readZipEntry(entries[ManifestName], 1<<20)
	if err != nil {
		return Pack{}, err
	}
	var manifest Manifest
	if err = decodeSingleJSON(manifestData, &manifest); err != nil {
		return Pack{}, fmt.Errorf("decode hash pack manifest: %w", err)
	}
	if manifest.FormatVersion != FormatVersion || !hex64.MatchString(manifest.PackID) || !hex64.MatchString(manifest.RecordsSHA256) || manifest.CreatedAt.IsZero() {
		return Pack{}, errors.New("hash pack manifest has an invalid contract or identity")
	}
	if manifest.Source, err = normalizeSource(manifest.Source); err != nil {
		return Pack{}, err
	}
	manifest.Release = strings.TrimSpace(manifest.Release)
	if manifest.Release == "" || len(manifest.Release) > 128 || strings.IndexFunc(manifest.Release, unicode.IsControl) >= 0 || manifest.RecordCount < 1 || manifest.RecordCount > MaxRecords {
		return Pack{}, errors.New("hash pack manifest has an invalid release or record count")
	}
	recordData, err := readZipEntry(entries[RecordsName], MaxRecordsBytes)
	if err != nil {
		return Pack{}, err
	}
	sum := sha256.Sum256(recordData)
	if hex.EncodeToString(sum[:]) != manifest.RecordsSHA256 {
		return Pack{}, errors.New("records.ndjson does not match manifest records_sha256")
	}
	if packIdentity(manifest.Source, manifest.Release, manifest.RecordsSHA256) != manifest.PackID {
		return Pack{}, errors.New("hash pack identity does not match its source, release, and records")
	}
	if len(recordData) == 0 || recordData[len(recordData)-1] != '\n' {
		return Pack{}, errors.New("records.ndjson must end every compact JSON record with a newline")
	}
	records := make([]Record, 0, manifest.RecordCount)
	seen := map[string]bool{}
	for offset := 0; offset < len(recordData); {
		lineEnd := bytes.IndexByte(recordData[offset:], '\n')
		if lineEnd < 0 {
			return Pack{}, errors.New("records.ndjson contains an unterminated record")
		}
		line := recordData[offset : offset+lineEnd]
		offset += lineEnd + 1
		if len(line) == 0 {
			return Pack{}, fmt.Errorf("record %d is blank", len(records))
		}
		var compact bytes.Buffer
		if err = json.Compact(&compact, line); err != nil {
			return Pack{}, fmt.Errorf("decode record %d: %w", len(records), err)
		}
		if !bytes.Equal(compact.Bytes(), line) {
			return Pack{}, fmt.Errorf("record %d must be compact JSON without insignificant whitespace", len(records))
		}
		var record Record
		if err = decodeSingleJSON(line, &record); err != nil {
			return Pack{}, fmt.Errorf("decode record %d: %w", len(records), err)
		}
		if record, err = normalizeRecord(record); err != nil {
			return Pack{}, fmt.Errorf("record %d: %w", len(records), err)
		}
		if seen[record.SHA256] {
			return Pack{}, fmt.Errorf("record %d repeats sha256 %s", len(records), record.SHA256)
		}
		seen[record.SHA256] = true
		records = append(records, record)
		if len(records) > MaxRecords {
			return Pack{}, fmt.Errorf("hash pack exceeds %d records", MaxRecords)
		}
	}
	if len(records) != manifest.RecordCount {
		return Pack{}, fmt.Errorf("manifest declares %d records but archive contains %d", manifest.RecordCount, len(records))
	}
	return Pack{Manifest: manifest, Records: records}, nil
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// IsSHA256 reports whether value is a canonical lower-case SHA-256 digest.
func IsSHA256(value string) bool { return hex64.MatchString(value) }
