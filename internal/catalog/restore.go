package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

// ValidateDatabaseBackup opens an exact regular SQLite file read-only and
// verifies both its integrity and schema compatibility. It never migrates or
// otherwise writes the candidate backup.
func ValidateDatabaseBackup(ctx context.Context, path string) (int, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return 0, errors.New("database backup is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("database backup must be an exact regular file")
	}
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return 0, errors.New("database backup could not be opened")
	}
	defer db.Close()
	var integrity string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return 0, errors.New("database backup failed integrity validation")
	}
	var version int
	if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, errors.New("database backup schema could not be read")
	}
	if version < 1 || version > CurrentSchemaVersion {
		return 0, fmt.Errorf("database backup schema %d is not supported by this version", version)
	}
	return version, nil
}

// RestoreDatabaseBackup copies a validated backup to a brand-new database
// path. Existing destinations are never opened, truncated, replaced, or
// removed. A failed copy or validation removes only the exact output file this
// call proved it created.
func RestoreDatabaseBackup(ctx context.Context, source, destination string) (version int, err error) {
	version, err = ValidateDatabaseBackup(ctx, source)
	if err != nil {
		return 0, err
	}
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return 0, err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return 0, err
	}
	if sourcePath == destinationPath {
		return 0, errors.New("restore output must differ from the backup source")
	}
	parent := filepath.Dir(destinationPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("restore output parent must be an existing real directory")
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return 0, errors.New("database backup is unavailable")
	}
	defer input.Close()
	output, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, errors.New("restore output already exists; refusing to overwrite it")
		}
		return 0, errors.New("restore output could not be created")
	}
	created := true
	defer func() {
		if created && err != nil {
			_ = os.Remove(destinationPath)
		}
	}()
	if _, err = io.Copy(output, input); err == nil {
		err = output.Sync()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, errors.New("restore output could not be written")
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(destinationPath, 0o600); err != nil {
			return 0, errors.New("restore output permissions could not be restricted")
		}
	}
	verifiedVersion, verifyErr := ValidateDatabaseBackup(ctx, destinationPath)
	if verifyErr != nil || verifiedVersion != version {
		return 0, errors.New("restored database failed post-copy validation")
	}
	created = false
	return version, nil
}
