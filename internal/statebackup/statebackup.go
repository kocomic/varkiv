// Package statebackup creates and restores private, machine-verifiable snapshots
// of the SQLite catalog and all service-managed files. It never reads the
// external ROM library and never replaces an existing destination.
package statebackup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"

	_ "modernc.org/sqlite"
)

const (
	FormatVersion   = 1
	manifestName    = "backup.json"
	databaseName    = "library.db"
	manifestMaxSize = 64 << 20
)

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

type Manifest struct {
	FormatVersion int       `json:"format_version"`
	CreatedAt     time.Time `json:"created_at"`
	SchemaVersion int       `json:"schema_version"`
	Files         []Entry   `json:"files"`
}

type Report struct {
	SchemaVersion     int
	Files             int
	Bytes             int64
	ManagedArtifacts  int
	ManagedMedia      int
	SaveBlobs         int
	RecoverySnapshots int
}

// Create writes a complete backup to a brand-new directory. The external
// library is deliberately outside this operation's authority.
func Create(ctx context.Context, databasePath, stateRoot, outputRoot string) (report Report, err error) {
	databasePath, err = exactRegular(databasePath, "database")
	if err != nil {
		return report, err
	}
	stateRoot, err = exactDirectory(stateRoot, "state root")
	if err != nil {
		return report, err
	}
	outputRoot, err = newPath(outputRoot)
	if err != nil {
		return report, err
	}
	if overlaps(outputRoot, stateRoot) || overlaps(stateRoot, outputRoot) || overlaps(outputRoot, databasePath) || overlaps(databasePath, outputRoot) {
		return report, errors.New("backup output must be independent from the database and state root")
	}
	if err = os.Mkdir(outputRoot, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return report, errors.New("backup output already exists; refusing to overwrite it")
		}
		return report, errors.New("backup output could not be created")
	}
	created := true
	defer func() {
		if created && err != nil {
			_ = os.RemoveAll(outputRoot)
		}
	}()

	store, err := catalog.Open(databasePath)
	if err != nil {
		return report, errors.New("database could not be opened for a consistent snapshot")
	}
	databaseOut := filepath.Join(outputRoot, databaseName)
	err = store.Backup(ctx, databaseOut)
	closeErr := store.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return report, errors.New("database snapshot could not be created")
	}
	if err = os.Chmod(databaseOut, 0o600); err != nil {
		return report, errors.New("database snapshot permissions could not be restricted")
	}
	if err = makeDatabasePortable(ctx, databaseOut); err != nil {
		return report, err
	}
	report.SchemaVersion, err = catalog.ValidateDatabaseBackup(ctx, databaseOut)
	if err != nil {
		return report, errors.New("database snapshot failed validation")
	}

	entries := []Entry{}
	databaseEntry, err := inspectFile(databaseOut, databaseName)
	if err != nil {
		return report, err
	}
	entries = append(entries, databaseEntry)
	excluded := excludedDatabaseFiles(databasePath, stateRoot)
	stateEntries, err := copyState(stateRoot, filepath.Join(outputRoot, "state"), excluded)
	if err != nil {
		return report, err
	}
	entries = append(entries, stateEntries...)
	if err = verifySourceStable(stateRoot, excluded, stateEntries); err != nil {
		return report, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	manifest := Manifest{FormatVersion: FormatVersion, CreatedAt: time.Now().UTC(), SchemaVersion: report.SchemaVersion, Files: entries}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return report, errors.New("backup manifest could not be encoded")
	}
	payload = append(payload, '\n')
	if err = writeExclusive(filepath.Join(outputRoot, manifestName), payload, 0o600); err != nil {
		return report, errors.New("backup manifest could not be written")
	}
	report, err = Check(ctx, outputRoot)
	if err != nil {
		return Report{}, err
	}
	created = false
	return report, nil
}

// Check verifies the exact file set, every content hash, SQLite integrity and
// the service-managed resources referenced by the database.
func Check(ctx context.Context, backupRoot string) (Report, error) {
	backupRoot, err := exactDirectory(backupRoot, "backup root")
	if err != nil {
		return Report{}, err
	}
	manifestPath := filepath.Join(backupRoot, manifestName)
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > manifestMaxSize {
		return Report{}, errors.New("backup manifest is unavailable or invalid")
	}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		return Report{}, errors.New("backup manifest could not be read")
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil || manifest.FormatVersion != FormatVersion || manifest.CreatedAt.IsZero() || manifest.SchemaVersion < 1 || manifest.SchemaVersion > catalog.CurrentSchemaVersion {
		return Report{}, errors.New("backup manifest format is invalid or unsupported")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Report{}, errors.New("backup manifest contains trailing data")
	}
	expected := make(map[string]Entry, len(manifest.Files))
	for _, entry := range manifest.Files {
		if err = validateEntry(entry); err != nil {
			return Report{}, err
		}
		if _, exists := expected[entry.Path]; exists {
			return Report{}, errors.New("backup manifest contains duplicate file entries")
		}
		expected[entry.Path] = entry
	}
	if _, ok := expected[databaseName]; !ok {
		return Report{}, errors.New("backup manifest does not contain the database snapshot")
	}
	actual := map[string]bool{}
	err = filepath.WalkDir(backupRoot, func(localPath string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("backup contents could not be inspected")
		}
		if localPath == backupRoot {
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return errors.New("backup contains a symbolic link")
		}
		if item.IsDir() {
			return nil
		}
		if !item.Type().IsRegular() {
			return errors.New("backup contains a non-regular file")
		}
		rel, relErr := filepath.Rel(backupRoot, localPath)
		if relErr != nil {
			return errors.New("backup file path could not be normalized")
		}
		portable := filepath.ToSlash(rel)
		if portable == manifestName {
			return nil
		}
		entry, ok := expected[portable]
		if !ok {
			return errors.New("backup contains a file that is not in its manifest")
		}
		got, inspectErr := inspectFile(localPath, portable)
		if inspectErr != nil || got.Size != entry.Size || got.SHA256 != entry.SHA256 || got.Mode != entry.Mode {
			return errors.New("backup file failed content validation")
		}
		actual[portable] = true
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	if len(actual) != len(expected) {
		return Report{}, errors.New("backup is missing one or more manifest files")
	}
	database := filepath.Join(backupRoot, databaseName)
	version, err := catalog.ValidateDatabaseBackup(ctx, database)
	if err != nil || version != manifest.SchemaVersion {
		return Report{}, errors.New("backup database failed integrity or schema validation")
	}
	report, err := validateReferences(ctx, database, filepath.Join(backupRoot, "state"), false)
	if err != nil {
		return Report{}, err
	}
	report.SchemaVersion = version
	report.Files = len(expected)
	for _, entry := range expected {
		report.Bytes += entry.Size
	}
	return report, nil
}

// Restore first validates the source, then creates an entirely new root. It
// never merges with or replaces an existing database or state directory.
func Restore(ctx context.Context, backupRoot, outputRoot string) (report Report, err error) {
	report, err = Check(ctx, backupRoot)
	if err != nil {
		return Report{}, err
	}
	backupRoot, err = exactDirectory(backupRoot, "backup root")
	if err != nil {
		return Report{}, err
	}
	outputRoot, err = newPath(outputRoot)
	if err != nil {
		return Report{}, err
	}
	if overlaps(outputRoot, backupRoot) || overlaps(backupRoot, outputRoot) {
		return Report{}, errors.New("restore output must be independent from the backup")
	}
	if err = os.Mkdir(outputRoot, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Report{}, errors.New("restore output already exists; refusing to overwrite it")
		}
		return Report{}, errors.New("restore output could not be created")
	}
	created := true
	defer func() {
		if created && err != nil {
			_ = os.RemoveAll(outputRoot)
		}
	}()

	manifest, err := readManifest(backupRoot)
	if err != nil {
		return Report{}, err
	}
	for _, entry := range manifest.Files {
		source := filepath.Join(backupRoot, filepath.FromSlash(entry.Path))
		target := filepath.Join(outputRoot, filepath.FromSlash(entry.Path))
		if err = copyExclusive(source, target, os.FileMode(entry.Mode)); err != nil {
			return Report{}, errors.New("restore file could not be created")
		}
	}
	restoredDB := filepath.Join(outputRoot, databaseName)
	restoredState := filepath.Join(outputRoot, "state")
	if err = rebaseSavePaths(ctx, restoredDB, restoredState); err != nil {
		return Report{}, err
	}
	version, err := catalog.ValidateDatabaseBackup(ctx, restoredDB)
	if err != nil || version != report.SchemaVersion {
		return Report{}, errors.New("restored database failed post-copy validation")
	}
	semantic, err := validateReferences(ctx, restoredDB, restoredState, true)
	if err != nil {
		return Report{}, err
	}
	report.ManagedArtifacts = semantic.ManagedArtifacts
	report.ManagedMedia = semantic.ManagedMedia
	report.SaveBlobs = semantic.SaveBlobs
	report.RecoverySnapshots = semantic.RecoverySnapshots
	created = false
	return report, nil
}

func readManifest(root string) (Manifest, error) {
	payload, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil {
		return Manifest{}, errors.New("backup manifest could not be read")
	}
	var manifest Manifest
	if err = json.Unmarshal(payload, &manifest); err != nil {
		return Manifest{}, errors.New("backup manifest could not be decoded")
	}
	return manifest, nil
}

func makeDatabasePortable(ctx context.Context, database string) error {
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return errors.New("database snapshot could not be made portable")
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE save_files SET blob_path='state/saves/blobs/' || substr(checksum,1,2) || '/' || checksum`); err != nil {
		return errors.New("database snapshot save references could not be made portable")
	}
	return nil
}

func rebaseSavePaths(ctx context.Context, database, stateRoot string) error {
	stateRoot, err := filepath.Abs(stateRoot)
	if err != nil {
		return errors.New("restored state root could not be normalized")
	}
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return errors.New("restored database could not be opened for path rebasing")
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,checksum FROM save_files ORDER BY id`)
	if err != nil {
		return errors.New("restored save references could not be read")
	}
	type item struct{ id, checksum string }
	items := []item{}
	for rows.Next() {
		var value item
		if err = rows.Scan(&value.id, &value.checksum); err != nil {
			rows.Close()
			return errors.New("restored save references could not be decoded")
		}
		items = append(items, value)
	}
	if err = rows.Close(); err != nil {
		return errors.New("restored save references could not be read")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("restored save references could not be rebased")
	}
	defer tx.Rollback()
	for _, value := range items {
		if !validSHA256(value.checksum) {
			return errors.New("restored database contains an invalid save checksum")
		}
		absolute := filepath.Join(stateRoot, "saves", "blobs", value.checksum[:2], value.checksum)
		if _, err = tx.ExecContext(ctx, `UPDATE save_files SET blob_path=? WHERE id=?`, absolute, value.id); err != nil {
			return errors.New("restored save references could not be rebased")
		}
	}
	if err = tx.Commit(); err != nil {
		return errors.New("restored save references could not be committed")
	}
	return nil
}

func validateReferences(ctx context.Context, database, stateRoot string, absoluteSaves bool) (Report, error) {
	uri := "file:" + filepath.ToSlash(database) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return Report{}, errors.New("backup database references could not be opened")
	}
	defer db.Close()
	var foreignViolation int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignViolation); err != nil || foreignViolation != 0 {
		return Report{}, errors.New("backup database failed foreign-key validation")
	}
	report := Report{}
	rows, err := db.QueryContext(ctx, `SELECT path,size,sha256,missing FROM artifacts WHERE storage_kind='managed' ORDER BY id`)
	if err != nil {
		return report, errors.New("managed ROM references could not be read")
	}
	for rows.Next() {
		var relative, checksum string
		var size int64
		var missing int
		if err = rows.Scan(&relative, &size, &checksum, &missing); err != nil {
			rows.Close()
			return report, errors.New("managed ROM reference is invalid")
		}
		if missing != 0 {
			continue
		}
		local, pathErr := referencedPath(stateRoot, "roms", relative)
		if pathErr != nil || !validSHA256(checksum) {
			rows.Close()
			return report, errors.New("managed ROM reference is unsafe or incomplete")
		}
		gotHash, gotSize, hashErr := hashResource(local)
		if hashErr != nil || gotHash != checksum || gotSize != size {
			rows.Close()
			return report, errors.New("managed ROM content does not match the database")
		}
		report.ManagedArtifacts++
	}
	if err = rows.Close(); err != nil {
		return report, errors.New("managed ROM references could not be read")
	}
	rows, err = db.QueryContext(ctx, `SELECT path,size,sha256 FROM media_assets WHERE storage_kind='managed' ORDER BY id`)
	if err != nil {
		return report, errors.New("managed media references could not be read")
	}
	for rows.Next() {
		var relative, checksum string
		var size int64
		if err = rows.Scan(&relative, &size, &checksum); err != nil {
			rows.Close()
			return report, errors.New("managed media reference is invalid")
		}
		local, pathErr := referencedPath(stateRoot, "media", relative)
		if pathErr != nil || !validSHA256(checksum) {
			rows.Close()
			return report, errors.New("managed media reference is unsafe or incomplete")
		}
		gotHash, gotSize, hashErr := filehash.File(local)
		if hashErr != nil || gotHash != checksum || gotSize != size {
			rows.Close()
			return report, errors.New("managed media content does not match the database")
		}
		report.ManagedMedia++
	}
	if err = rows.Close(); err != nil {
		return report, errors.New("managed media references could not be read")
	}
	rows, err = db.QueryContext(ctx, `SELECT checksum,size,blob_path FROM save_files ORDER BY id`)
	if err != nil {
		return report, errors.New("save blob references could not be read")
	}
	seenBlobs := map[string]bool{}
	for rows.Next() {
		var checksum, stored string
		var size int64
		if err = rows.Scan(&checksum, &size, &stored); err != nil || !validSHA256(checksum) {
			rows.Close()
			return report, errors.New("save blob reference is invalid")
		}
		expected := filepath.Join(stateRoot, "saves", "blobs", checksum[:2], checksum)
		if absoluteSaves {
			if filepath.Clean(stored) != filepath.Clean(expected) {
				rows.Close()
				return report, errors.New("restored save blob reference was not safely rebased")
			}
		} else if stored != path.Join("state", "saves", "blobs", checksum[:2], checksum) {
			rows.Close()
			return report, errors.New("backup save blob reference is not portable")
		}
		gotHash, gotSize, hashErr := filehash.File(expected)
		if hashErr != nil || gotHash != checksum || gotSize != size {
			rows.Close()
			return report, errors.New("save blob content does not match the database")
		}
		if !seenBlobs[checksum] {
			seenBlobs[checksum] = true
			report.SaveBlobs++
		}
	}
	if err = rows.Close(); err != nil {
		return report, errors.New("save blob references could not be read")
	}
	rows, err = db.QueryContext(ctx, `SELECT result_json FROM package_releases WHERE status='succeeded' ORDER BY id`)
	if err != nil {
		return report, errors.New("package recovery references could not be read")
	}
	seenRecovery := map[string]bool{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return report, errors.New("package recovery reference is invalid")
		}
		var result struct {
			RecoverySnapshot string `json:"recovery_snapshot"`
		}
		if err = json.Unmarshal([]byte(raw), &result); err != nil {
			rows.Close()
			return report, errors.New("package release result is invalid")
		}
		if result.RecoverySnapshot == "" || seenRecovery[result.RecoverySnapshot] {
			continue
		}
		portable, cleanErr := cleanPortable(result.RecoverySnapshot)
		if cleanErr != nil || !strings.HasPrefix(portable, "state/recovery/packages/") {
			rows.Close()
			return report, errors.New("package recovery reference is unsafe")
		}
		local := filepath.Join(filepath.Dir(stateRoot), filepath.FromSlash(portable))
		info, statErr := os.Lstat(local)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			rows.Close()
			return report, errors.New("package recovery snapshot is missing")
		}
		snapshot, statErr := os.Lstat(filepath.Join(local, "snapshot.json"))
		if statErr != nil || !snapshot.Mode().IsRegular() || snapshot.Mode()&os.ModeSymlink != 0 {
			rows.Close()
			return report, errors.New("package recovery snapshot manifest is missing")
		}
		seenRecovery[portable] = true
		report.RecoverySnapshots++
	}
	if err = rows.Close(); err != nil {
		return report, errors.New("package recovery references could not be read")
	}
	return report, nil
}

func referencedPath(stateRoot, category, relative string) (string, error) {
	portable, err := cleanPortable(filepath.ToSlash(relative))
	if err != nil {
		return "", err
	}
	root := filepath.Join(stateRoot, category)
	local := filepath.Join(root, filepath.FromSlash(portable))
	if !overlaps(root, local) {
		return "", errors.New("reference escapes state root")
	}
	return local, nil
}

func hashResource(local string) (string, int64, error) {
	info, err := os.Lstat(local)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, errors.New("resource is unavailable")
	}
	if info.IsDir() {
		return filehash.Directory(local)
	}
	if info.Mode().IsRegular() {
		return filehash.File(local)
	}
	return "", 0, errors.New("resource is not a regular file or directory")
}

func copyState(sourceRoot, targetRoot string, excluded map[string]bool) ([]Entry, error) {
	entries := []Entry{}
	err := filepath.WalkDir(sourceRoot, func(source string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("state root could not be read")
		}
		if source == sourceRoot {
			return nil
		}
		if excluded[filepath.Clean(source)] {
			if item.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return errors.New("state root contains a symbolic link")
		}
		if item.IsDir() {
			return nil
		}
		if !item.Type().IsRegular() {
			return errors.New("state root contains a non-regular file")
		}
		rel, err := filepath.Rel(sourceRoot, source)
		if err != nil {
			return errors.New("state file path could not be normalized")
		}
		portable := path.Join("state", filepath.ToSlash(rel))
		target := filepath.Join(targetRoot, rel)
		entry, err := copyAndInspect(source, target, portable)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func verifySourceStable(root string, excluded map[string]bool, copied []Entry) error {
	expected := map[string]Entry{}
	for _, entry := range copied {
		expected[entry.Path] = entry
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(local string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("state root changed while the backup was being created")
		}
		if local == root {
			return nil
		}
		if excluded[filepath.Clean(local)] {
			if item.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 || (!item.IsDir() && !item.Type().IsRegular()) {
			return errors.New("state root changed to an unsafe file type during backup")
		}
		if item.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, local)
		if relErr != nil {
			return errors.New("state root changed while the backup was being created")
		}
		portable := path.Join("state", filepath.ToSlash(rel))
		entry, ok := expected[portable]
		if !ok {
			return errors.New("state root gained a file while the backup was being created")
		}
		got, inspectErr := inspectFile(local, portable)
		if inspectErr != nil || got.Size != entry.Size || got.SHA256 != entry.SHA256 || got.Mode != entry.Mode {
			return errors.New("state root changed while the backup was being created")
		}
		seen[portable] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("state root lost a file while the backup was being created")
	}
	return nil
}

func excludedDatabaseFiles(database, stateRoot string) map[string]bool {
	excluded := map[string]bool{
		filepath.Clean(filepath.Join(stateRoot, "media", "cache")):    true,
		filepath.Clean(filepath.Join(stateRoot, "media", ".staging")): true,
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		candidate := filepath.Clean(database + suffix)
		if overlaps(stateRoot, candidate) {
			excluded[candidate] = true
		}
	}
	return excluded
}

func copyAndInspect(source, target, portable string) (Entry, error) {
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return Entry{}, errors.New("state file is not an exact regular file")
	}
	if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return Entry{}, errors.New("backup directory could not be created")
	}
	input, err := os.Open(source)
	if err != nil {
		return Entry{}, errors.New("state file could not be read")
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Entry{}, errors.New("backup file could not be created")
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil {
		copyErr = os.Chmod(target, before.Mode().Perm())
	}
	if copyErr != nil {
		return Entry{}, errors.New("backup file could not be written")
	}
	after, err := os.Lstat(source)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || size != before.Size() {
		return Entry{}, errors.New("state file changed while it was being copied")
	}
	return Entry{Path: portable, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Mode: uint32(before.Mode().Perm())}, nil
}

func inspectFile(local, portable string) (Entry, error) {
	info, err := os.Lstat(local)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Entry{}, errors.New("file is not an exact regular file")
	}
	hash, size, err := filehash.File(local)
	if err != nil {
		return Entry{}, errors.New("file could not be hashed")
	}
	return Entry{Path: portable, Size: size, SHA256: hash, Mode: uint32(info.Mode().Perm())}, nil
}

func copyExclusive(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil {
		copyErr = os.Chmod(target, mode.Perm())
	}
	return copyErr
}

func writeExclusive(local string, payload []byte, mode os.FileMode) error {
	output, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := output.Write(payload)
	if writeErr == nil {
		writeErr = output.Sync()
	}
	if closeErr := output.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func validateEntry(entry Entry) error {
	clean, err := cleanPortable(entry.Path)
	if err != nil || clean != entry.Path || (entry.Path != databaseName && !strings.HasPrefix(entry.Path, "state/")) {
		return errors.New("backup manifest contains an unsafe file path")
	}
	if entry.Size < 0 || !validSHA256(entry.SHA256) || entry.Mode > 0o777 {
		return errors.New("backup manifest contains invalid file metadata")
	}
	return nil
}

func cleanPortable(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return "", errors.New("path is not portable")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes its root")
	}
	return clean, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func exactRegular(value, label string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%s could not be normalized", label)
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must be an exact regular file", label)
	}
	return abs, nil
}

func exactDirectory(value, label string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%s could not be normalized", label)
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must be an exact real directory", label)
	}
	return abs, nil
}

func newPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("output path is required")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("output path could not be normalized")
	}
	if _, err = os.Lstat(abs); err == nil {
		return "", errors.New("output already exists; refusing to overwrite it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("output path could not be inspected")
	}
	parent, err := exactDirectory(filepath.Dir(abs), "output parent")
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func overlaps(base, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
