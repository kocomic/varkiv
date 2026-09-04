package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"varkiv/internal/catalog"
)

const (
	cleanupFormat       = "varkiv-managed-storage-recovery-v1"
	cleanupManifestName = "operation.json"
	maxCleanupFiles     = 50_000
)

var cleanupRunIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)

var (
	ErrManagedStorageUnsafe   = errors.New("managed storage is unsafe to clean")
	ErrManagedStorageChanged  = errors.New("managed storage changed after preview")
	ErrCleanupRunNotFound     = errors.New("cleanup recovery operation was not found")
	ErrCleanupRestoreConflict = errors.New("cleanup recovery destination is occupied")
	ErrCleanupRecoveryDamaged = errors.New("cleanup recovery data is damaged")
)

type CleanupCandidate struct {
	ID           string    `json:"id"`
	StorageKind  string    `json:"storage_kind"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	ModifiedAt   time.Time `json:"modified_at"`
	mode         fs.FileMode
}

type CleanupPlan struct {
	Fingerprint string             `json:"fingerprint"`
	Candidates  []CleanupCandidate `json:"candidates"`
	TotalBytes  int64              `json:"total_bytes"`
}

type CleanupRun struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	ItemCount   int       `json:"item_count"`
	TotalBytes  int64     `json:"total_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

type cleanupItem struct {
	ID           string `json:"id"`
	StorageKind  string `json:"storage_kind"`
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	ModifiedNS   int64  `json:"modified_ns"`
	Mode         uint32 `json:"mode"`
	StoredPath   string `json:"stored_path"`
}

type cleanupManifest struct {
	Format      string        `json:"format"`
	ID          string        `json:"id"`
	Status      string        `json:"status"`
	Fingerprint string        `json:"fingerprint"`
	CreatedAt   time.Time     `json:"created_at"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
	Items       []cleanupItem `json:"items"`
}

func (r *Repository) cleanupRoot() string {
	return filepath.Join(r.StateRoot, "recovery", "managed-storage")
}

func normalizeManagedReference(value string) (string, error) {
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(value))))
	if value == "" || value == "." || filepath.IsAbs(filepath.FromSlash(value)) || value == ".." || strings.HasPrefix(value, "../") {
		return "", errors.New("managed storage reference is not a safe relative path")
	}
	return value, nil
}

func referenceSet(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		normalized, err := normalizeManagedReference(value)
		if err != nil {
			return nil, err
		}
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out, nil
}

func pathProtected(relative string, references []string) bool {
	for _, reference := range references {
		if relative == reference || strings.HasPrefix(relative, reference+"/") {
			return true
		}
	}
	return false
}

func candidateIdentity(kind, relative string, info fs.FileInfo) string {
	payload := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d", kind, relative, info.Size(), info.ModTime().UnixNano(), uint32(info.Mode().Perm()))
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:16])
}

func (r *Repository) scanCleanupRoot(ctx context.Context, kind, root string, references []string) ([]CleanupCandidate, error) {
	items := []CleanupCandidate{}
	err := filepath.WalkDir(root, func(localPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("managed storage could not be inspected")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if localPath == root {
			return nil
		}
		rel, err := filepath.Rel(root, localPath)
		if err != nil {
			return errors.New("managed storage path could not be normalized")
		}
		relative := filepath.ToSlash(rel)
		if kind == "media" && (relative == ".staging" || strings.HasPrefix(relative, ".staging/") || relative == "cache" || strings.HasPrefix(relative, "cache/")) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic link found; no files were moved", ErrManagedStorageUnsafe)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: unsupported file type found; no files were moved", ErrManagedStorageUnsafe)
		}
		if pathProtected(relative, references) {
			return nil
		}
		items = append(items, CleanupCandidate{ID: candidateIdentity(kind, relative, info), StorageKind: kind, RelativePath: relative, Size: info.Size(), ModifiedAt: info.ModTime().UTC(), mode: info.Mode().Perm()})
		if len(items) > maxCleanupFiles {
			return fmt.Errorf("managed storage cleanup exceeds the %d-file review limit", maxCleanupFiles)
		}
		return nil
	})
	return items, err
}

func cleanupFingerprint(items []CleanupCandidate) (string, error) {
	type fingerprintItem struct {
		ID           string `json:"id"`
		StorageKind  string `json:"storage_kind"`
		RelativePath string `json:"relative_path"`
		Size         int64  `json:"size"`
		ModifiedNS   int64  `json:"modified_ns"`
	}
	values := make([]fingerprintItem, len(items))
	for index, item := range items {
		values[index] = fingerprintItem{ID: item.ID, StorageKind: item.StorageKind, RelativePath: item.RelativePath, Size: item.Size, ModifiedNS: item.ModifiedAt.UnixNano()}
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// PreviewManagedCleanup returns only unreferenced regular files below the two
// service-managed roots. External library paths and save blobs are out of scope.
func (r *Repository) PreviewManagedCleanup(ctx context.Context, references catalog.ManagedStorageReferences) (CleanupPlan, error) {
	romReferences, err := referenceSet(references.ROM)
	if err != nil {
		return CleanupPlan{}, err
	}
	mediaReferences, err := referenceSet(references.Media)
	if err != nil {
		return CleanupPlan{}, err
	}
	rom, err := r.scanCleanupRoot(ctx, "rom", r.ROMRoot, romReferences)
	if err != nil {
		return CleanupPlan{}, err
	}
	media, err := r.scanCleanupRoot(ctx, "media", r.MediaRoot, mediaReferences)
	if err != nil {
		return CleanupPlan{}, err
	}
	items := append(rom, media...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].StorageKind != items[j].StorageKind {
			return items[i].StorageKind < items[j].StorageKind
		}
		return items[i].RelativePath < items[j].RelativePath
	})
	fingerprint, err := cleanupFingerprint(items)
	if err != nil {
		return CleanupPlan{}, err
	}
	plan := CleanupPlan{Fingerprint: fingerprint, Candidates: items}
	for _, item := range items {
		plan.TotalBytes += item.Size
	}
	return plan, nil
}

func writeManifest(path string, manifest cleanupManifest, exclusive bool) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if exclusive {
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return openErr
		}
		if _, err = file.Write(payload); err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".operation-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(payload)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (r *Repository) candidateSource(item CleanupCandidate) (string, error) {
	switch item.StorageKind {
	case "rom":
		return inside(r.ROMRoot, item.RelativePath)
	case "media":
		return inside(r.MediaRoot, item.RelativePath)
	default:
		return "", errors.New("cleanup candidate has an invalid storage kind")
	}
}

func candidateStillMatches(path string, item CleanupCandidate) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrManagedStorageChanged
	}
	if candidateIdentity(item.StorageKind, item.RelativePath, info) != item.ID || info.Size() != item.Size || info.ModTime().UnixNano() != item.ModifiedAt.UnixNano() {
		return ErrManagedStorageChanged
	}
	return nil
}

func rollbackMoves(moved []struct{ source, target string }) error {
	var rollbackErr error
	for index := len(moved) - 1; index >= 0; index-- {
		err := os.MkdirAll(filepath.Dir(moved[index].source), 0o755)
		if err == nil {
			err = os.Rename(moved[index].target, moved[index].source)
		}
		if err != nil && rollbackErr == nil {
			rollbackErr = err
		}
	}
	return rollbackErr
}

// QuarantineManagedCleanup moves the exact reviewed files to a private,
// same-volume recovery area. It never deletes bytes. A durable manifest is
// synced before the first rename so interrupted operations remain recoverable.
func (r *Repository) QuarantineManagedCleanup(ctx context.Context, fingerprint string, selected []CleanupCandidate) (CleanupRun, error) {
	if len(selected) == 0 {
		return CleanupRun{}, errors.New("at least one cleanup candidate must be selected")
	}
	runID := catalog.NewID()
	runRoot := filepath.Join(r.cleanupRoot(), runID)
	if err := os.MkdirAll(filepath.Join(runRoot, "files", "rom"), 0o700); err != nil {
		return CleanupRun{}, errors.New("cleanup recovery area could not be created")
	}
	if err := os.MkdirAll(filepath.Join(runRoot, "files", "media"), 0o700); err != nil {
		_ = os.RemoveAll(runRoot)
		return CleanupRun{}, errors.New("cleanup recovery area could not be created")
	}
	manifest := cleanupManifest{Format: cleanupFormat, ID: runID, Status: "prepared", Fingerprint: fingerprint, CreatedAt: time.Now().UTC(), Items: make([]cleanupItem, 0, len(selected))}
	for _, item := range selected {
		manifest.Items = append(manifest.Items, cleanupItem{ID: item.ID, StorageKind: item.StorageKind, RelativePath: item.RelativePath, Size: item.Size, ModifiedNS: item.ModifiedAt.UnixNano(), Mode: uint32(item.mode.Perm()), StoredPath: filepath.ToSlash(filepath.Join("files", item.StorageKind, item.ID))})
	}
	manifestPath := filepath.Join(runRoot, cleanupManifestName)
	if err := writeManifest(manifestPath, manifest, true); err != nil {
		_ = os.RemoveAll(runRoot)
		return CleanupRun{}, errors.New("cleanup recovery manifest could not be written")
	}
	moved := []struct{ source, target string }{}
	fail := func(source error) (CleanupRun, error) {
		if rollbackErr := rollbackMoves(moved); rollbackErr == nil {
			_ = os.RemoveAll(runRoot)
			return CleanupRun{}, source
		}
		return CleanupRun{}, errors.New("cleanup stopped and its recovery manifest was retained")
	}
	for _, item := range selected {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		source, err := r.candidateSource(item)
		if err != nil || candidateStillMatches(source, item) != nil {
			return fail(ErrManagedStorageChanged)
		}
		target := filepath.Join(runRoot, filepath.FromSlash(filepath.Join("files", item.StorageKind, item.ID)))
		if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			return fail(errors.New("cleanup recovery target already exists"))
		}
		if err = os.Rename(source, target); err != nil {
			return fail(errors.New("managed file could not be moved to recovery storage"))
		}
		moved = append(moved, struct{ source, target string }{source: source, target: target})
	}
	manifest.Status = "quarantined"
	manifest.CompletedAt = time.Now().UTC()
	if err := writeManifest(manifestPath, manifest, false); err != nil {
		return fail(errors.New("cleanup recovery manifest could not be finalized"))
	}
	return manifest.summary(), nil
}

func (manifest cleanupManifest) summary() CleanupRun {
	run := CleanupRun{ID: manifest.ID, Status: manifest.Status, ItemCount: len(manifest.Items), CreatedAt: manifest.CreatedAt, CompletedAt: manifest.CompletedAt}
	for _, item := range manifest.Items {
		run.TotalBytes += item.Size
	}
	return run
}

func decodeManifest(path string) (cleanupManifest, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16<<20 {
		return cleanupManifest{}, errors.New("cleanup recovery manifest is unavailable or invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return cleanupManifest{}, errors.New("cleanup recovery manifest could not be opened")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<20))
	decoder.DisallowUnknownFields()
	var manifest cleanupManifest
	if err = decoder.Decode(&manifest); err != nil || manifest.Format != cleanupFormat || !cleanupRunIDPattern.MatchString(manifest.ID) || manifest.CreatedAt.IsZero() || len(manifest.Items) == 0 || len(manifest.Items) > maxCleanupFiles {
		return cleanupManifest{}, errors.New("cleanup recovery manifest is invalid")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cleanupManifest{}, errors.New("cleanup recovery manifest contains trailing data")
	}
	if manifest.Status != "prepared" && manifest.Status != "quarantined" && manifest.Status != "restored" {
		return cleanupManifest{}, errors.New("cleanup recovery manifest has an invalid status")
	}
	seen := map[string]bool{}
	for _, item := range manifest.Items {
		if seen[item.ID] || (item.StorageKind != "rom" && item.StorageKind != "media") || item.Size < 0 || item.ID == "" || item.StoredPath != filepath.ToSlash(filepath.Join("files", item.StorageKind, item.ID)) {
			return cleanupManifest{}, errors.New("cleanup recovery manifest contains an invalid item")
		}
		if _, err = normalizeManagedReference(item.RelativePath); err != nil {
			return cleanupManifest{}, errors.New("cleanup recovery manifest contains an unsafe path")
		}
		seen[item.ID] = true
	}
	return manifest, nil
}

func (r *Repository) ListCleanupRuns(ctx context.Context) ([]CleanupRun, error) {
	root := r.cleanupRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []CleanupRun{}, nil
	}
	if err != nil {
		return nil, errors.New("cleanup recovery history could not be read")
	}
	runs := []CleanupRun{}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !cleanupRunIDPattern.MatchString(entry.Name()) {
			continue
		}
		manifest, readErr := decodeManifest(filepath.Join(root, entry.Name(), cleanupManifestName))
		if readErr != nil || manifest.ID != entry.Name() {
			return nil, errors.New("cleanup recovery history contains an invalid operation")
		}
		runs = append(runs, manifest.summary())
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return runs, nil
}

func (r *Repository) RestoreCleanupRun(ctx context.Context, id string) (CleanupRun, error) {
	if !cleanupRunIDPattern.MatchString(id) {
		return CleanupRun{}, errors.New("cleanup recovery id is invalid")
	}
	runRoot := filepath.Join(r.cleanupRoot(), id)
	manifestPath := filepath.Join(runRoot, cleanupManifestName)
	manifest, err := decodeManifest(manifestPath)
	if err != nil || manifest.ID != id {
		return CleanupRun{}, ErrCleanupRunNotFound
	}
	if manifest.Status == "restored" {
		return manifest.summary(), nil
	}
	type move struct{ source, target string }
	moves := make([]move, 0, len(manifest.Items))
	for _, item := range manifest.Items {
		if err = ctx.Err(); err != nil {
			return CleanupRun{}, err
		}
		candidate := CleanupCandidate{ID: item.ID, StorageKind: item.StorageKind, RelativePath: item.RelativePath, Size: item.Size, ModifiedAt: time.Unix(0, item.ModifiedNS).UTC(), mode: fs.FileMode(item.Mode)}
		target, pathErr := r.candidateSource(candidate)
		if pathErr != nil {
			return CleanupRun{}, errors.New("cleanup recovery destination is unsafe")
		}
		source := filepath.Join(runRoot, filepath.FromSlash(item.StoredPath))
		info, sourceErr := os.Lstat(source)
		if sourceErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != item.Size {
			return CleanupRun{}, ErrCleanupRecoveryDamaged
		}
		if _, targetErr := os.Lstat(target); !errors.Is(targetErr, os.ErrNotExist) {
			return CleanupRun{}, ErrCleanupRestoreConflict
		}
		moves = append(moves, move{source: source, target: target})
	}
	restored := []struct{ source, target string }{}
	rollbackRestore := func(source error) (CleanupRun, error) {
		if rollbackErr := rollbackMoves(restored); rollbackErr != nil {
			return CleanupRun{}, fmt.Errorf("%w: restore rollback failed after %v", ErrCleanupRecoveryDamaged, source)
		}
		return CleanupRun{}, source
	}
	for _, item := range moves {
		if err = os.MkdirAll(filepath.Dir(item.target), 0o755); err != nil {
			return rollbackRestore(errors.New("cleanup recovery directory could not be recreated"))
		}
		if err = os.Rename(item.source, item.target); err != nil {
			return rollbackRestore(errors.New("cleanup recovery could not restore all files"))
		}
		restored = append(restored, struct{ source, target string }{source: item.source, target: item.target})
	}
	manifest.Status = "restored"
	manifest.CompletedAt = time.Now().UTC()
	if err = writeManifest(manifestPath, manifest, false); err != nil {
		return rollbackRestore(errors.New("cleanup recovery manifest could not be finalized"))
	}
	return manifest.summary(), nil
}
