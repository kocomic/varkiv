package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

var ErrPlatformMismatch = errors.New("games must use the same platform")
var ErrInvalidGameMerge = errors.New("different target and source game ids are required")
var ErrGameMergeStale = errors.New("game merge preview is stale")
var ErrDeviceHasSaveRevisions = errors.New("device has save revisions and cannot be deleted")
var ErrPairedDeviceIdentityInUse = errors.New("paired device profile, operating system, and architecture require revocation and re-pairing")
var ErrDeviceRevocationRequired = errors.New("use the dedicated revoke operation so every device token is revoked atomically")
var ErrImportDuplicate = errors.New("import candidate already exists")
var ErrSourceHasScans = errors.New("library source has scan history and cannot be deleted; disable it instead")
var ErrPackageProfileHasHistory = errors.New("package profile has plans or releases and cannot be deleted; disable it instead")
var ErrBuiltinImmutable = errors.New("builtin catalog entries are immutable; create a custom copy instead")
var ErrBuiltinNamespaceReserved = errors.New("builtin-* identifiers are reserved for application-owned catalog entries")
var ErrRuntimeObjectInUse = errors.New("runtime catalog entry is referenced and cannot be deleted; disable it instead")
var ErrCustomPlatformInUse = errors.New("custom platform is referenced and cannot be deleted; disable it instead")
var ErrPlatformDefinitionConflict = errors.New("imported custom platform conflicts with the existing definition")
var ErrPlatformDefinitionDisabled = errors.New("imported custom platform already exists but is disabled")

const CurrentSchemaVersion = 28

func reservedBuiltinID(id string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "builtin-")
}

func validateBuiltinID(id string, builtin bool) error {
	if reservedBuiltinID(id) && !builtin {
		return ErrBuiltinNamespaceReserved
	}
	return nil
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func readOnlySQLiteURI(absolute string, immutable bool) string {
	uriPath := filepath.ToSlash(absolute)
	// url.URL needs a leading slash to serialize an absolute Windows drive
	// path as file:///C:/..., which modernc SQLite accepts on Windows.
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := &url.URL{Scheme: "file", Path: uriPath}
	query := uri.Query()
	query.Set("mode", "ro")
	if immutable {
		query.Set("immutable", "1")
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

// OpenReadOnly opens an existing exact SQLite file without changing journal
// mode, running migrations, or creating sidecars. It is intended for offline
// checks, including databases mounted from a read-only restored volume.
func OpenReadOnly(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("database must be an exact regular file")
	}
	// An offline database without WAL sidecars can be treated as immutable,
	// which prevents SQLite from creating empty WAL/SHM files even when the
	// database header remembers WAL mode. If a live WAL exists, omit immutable
	// so the check observes its committed pages.
	immutable := false
	if _, walErr := os.Lstat(absolute + "-wal"); errors.Is(walErr, os.ErrNotExist) {
		if _, shmErr := os.Lstat(absolute + "-shm"); errors.Is(shmErr, os.ErrNotExist) {
			immutable = true
		}
	}
	db, err := sql.Open("sqlite", readOnlySQLiteURI(absolute, immutable))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys = ON; PRAGMA query_only = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	return version, err
}

func (s *Store) IntegrityCheck(ctx context.Context) (string, error) {
	var result string
	err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result)
	return result, err
}

func (s *Store) ForeignKeyViolationCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&count)
	return count, err
}

// Backup creates a compact, transactionally consistent SQLite snapshot.
// SQLite requires VACUUM INTO to target a path that does not already exist.
func (s *Store) Backup(ctx context.Context, destination string) error {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if _, err = os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	quoted := strings.ReplaceAll(destination, "'", "''")
	_, err = s.db.ExecContext(ctx, `VACUUM INTO '`+quoted+`'`)
	return err
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }
