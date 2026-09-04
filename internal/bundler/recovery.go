package bundler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	recoveryDirectory       = ".varkiv-recovery"
	legacyRecoveryDirectory = ".varkiv-backups"
)

type recoveryEntry struct {
	Path      string `json:"path"`
	Existed   bool   `json:"existed"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
	BackupRel string `json:"backup_path,omitempty"`
}

type buildRecovery struct {
	out      string
	root     string
	relative string
	entries  []recoveryEntry
}

func copyRecoveryExclusive(source, target string) error {
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
	_, copyErr := output.ReadFrom(input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func writeRecoveryExclusive(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := output.Write(content)
	if writeErr == nil {
		writeErr = output.Sync()
	}
	if closeErr := output.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func actionWritesTarget(action string) bool {
	switch action {
	case "unchanged", "reference", "missing", "conflict", "deduplicated":
		return false
	default:
		return true
	}
}

func validateManagedTargetParents(out, relative string) error {
	current := out
	components := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for _, component := range components[:len(components)-1] {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed target parent %q must be a real directory", filepath.ToSlash(strings.TrimPrefix(current, out+string(filepath.Separator))))
		}
	}
	return nil
}

func pathWithin(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensureRecoveryDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(abs); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("package recovery root must be a real directory")
		}
		return abs, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}

	ancestor := abs
	missing := []string{}
	for {
		info, statErr := os.Lstat(ancestor)
		if statErr == nil {
			if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", errors.New("package recovery parent must be a directory")
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("package recovery root has no existing parent")
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
		if err = os.Mkdir(resolved, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, statErr := os.Lstat(resolved)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("package recovery root must contain only real directories")
		}
	}
	return resolved, nil
}

func prepareBuildRecovery(out, recoveryBase, recoveryLocator string, plan Plan) (*buildRecovery, error) {
	out, err := filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	recoveryBase, err = filepath.Abs(recoveryBase)
	if err != nil {
		return nil, err
	}
	if pathWithin(out, recoveryBase) || pathWithin(recoveryBase, out) {
		return nil, errors.New("package recovery root must be independent from package output")
	}
	locator, err := cleanRelative(filepath.ToSlash(recoveryLocator))
	if err != nil || locator == "." {
		return nil, errors.New("package recovery locator must be a non-empty relative path")
	}
	targets := map[string]bool{}
	for _, item := range plan.Items {
		if actionWritesTarget(item.Action) {
			targets[filepath.ToSlash(item.Target)] = true
		}
	}
	paths := make([]string, 0, len(targets))
	for path := range targets {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	recovery := &buildRecovery{out: out, entries: make([]recoveryEntry, 0, len(paths))}
	for _, portable := range paths {
		relative, err := cleanRelative(portable)
		if err != nil {
			return nil, err
		}
		portable = filepath.ToSlash(relative)
		if portable == recoveryDirectory || strings.HasPrefix(portable, recoveryDirectory+"/") || portable == legacyRecoveryDirectory || strings.HasPrefix(portable, legacyRecoveryDirectory+"/") {
			return nil, fmt.Errorf("package target %q uses the reserved recovery directory", portable)
		}
		if err = validateManagedTargetParents(out, relative); err != nil {
			return nil, fmt.Errorf("inspect managed target %q: %w", portable, err)
		}
		target := filepath.Join(out, relative)
		info, statErr := os.Lstat(target)
		entry := recoveryEntry{Path: portable}
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			recovery.entries = append(recovery.entries, entry)
			continue
		case statErr != nil:
			return nil, fmt.Errorf("inspect managed target %q: %w", portable, statErr)
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("managed target %q must be a regular file before update", portable)
		}
		entry.Existed = true
		entry.Mode = uint32(info.Mode().Perm())
		entry.Size = info.Size()
		entry.SHA256, _, statErr = hashFile(target)
		if statErr != nil {
			return nil, fmt.Errorf("hash managed target %q before update: %w", portable, statErr)
		}
		recovery.entries = append(recovery.entries, entry)
	}

	hasExisting := false
	for _, entry := range recovery.entries {
		if entry.Existed {
			hasExisting = true
			break
		}
	}
	if !hasExisting {
		return recovery, nil
	}
	base, err := ensureRecoveryDirectory(recoveryBase)
	if err != nil {
		return nil, err
	}
	prefix := "release-" + time.Now().UTC().Format("20060102T150405Z") + "-"
	root, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return nil, err
	}
	recovery.root = root
	snapshotComplete := false
	defer func() {
		if !snapshotComplete {
			_ = os.RemoveAll(root)
		}
	}()
	recovery.relative = filepath.ToSlash(filepath.Join(locator, filepath.Base(root)))
	for index := range recovery.entries {
		entry := &recovery.entries[index]
		if !entry.Existed {
			continue
		}
		entry.BackupRel = filepath.ToSlash(filepath.Join("files", filepath.FromSlash(entry.Path)))
		if err = copyRecoveryExclusive(filepath.Join(out, filepath.FromSlash(entry.Path)), filepath.Join(root, filepath.FromSlash(entry.BackupRel))); err != nil {
			return nil, fmt.Errorf("snapshot managed target %q: %w", entry.Path, err)
		}
	}
	manifest, err := json.MarshalIndent(struct {
		FormatVersion int             `json:"format_version"`
		CreatedAt     time.Time       `json:"created_at"`
		Purpose       string          `json:"purpose"`
		Files         []recoveryEntry `json:"files"`
	}{1, time.Now().UTC(), "recoverable pre-update package snapshot", recovery.entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = writeRecoveryExclusive(filepath.Join(root, "snapshot.json"), append(manifest, '\n')); err != nil {
		return nil, err
	}
	snapshotComplete = true
	return recovery, nil
}

func (recovery *buildRecovery) restore() error {
	if recovery == nil {
		return nil
	}
	var restoreErrors []error
	for _, entry := range recovery.entries {
		target := filepath.Join(recovery.out, filepath.FromSlash(entry.Path))
		if err := validateManagedTargetParents(recovery.out, filepath.FromSlash(entry.Path)); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("validate %q during rollback: %w", entry.Path, err))
			continue
		}
		if entry.Existed {
			if recovery.root == "" || entry.BackupRel == "" {
				restoreErrors = append(restoreErrors, fmt.Errorf("no snapshot is available for %q", entry.Path))
				continue
			}
			if err := copyAtomic(filepath.Join(recovery.root, filepath.FromSlash(entry.BackupRel)), target); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restore %q: %w", entry.Path, err))
				continue
			}
			if err := os.Chmod(target, os.FileMode(entry.Mode)); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restore mode for %q: %w", entry.Path, err))
			}
			continue
		}
		info, err := os.Lstat(target)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			restoreErrors = append(restoreErrors, fmt.Errorf("inspect new target %q during rollback: %w", entry.Path, err))
		case !info.Mode().IsRegular():
			restoreErrors = append(restoreErrors, fmt.Errorf("refusing to remove non-file target %q during rollback", entry.Path))
		default:
			if err = os.Remove(target); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("remove new target %q during rollback: %w", entry.Path, err))
			}
		}
	}
	return errors.Join(restoreErrors...)
}
