package bundler

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
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/exporter"
)

type Profile struct {
	ID                string           `json:"id,omitempty"`
	Name              string           `json:"name"`
	Frontend          string           `json:"frontend"`
	Target            string           `json:"target"`
	DeviceProfileID   string           `json:"device_profile_id,omitempty"`
	FrontendAdapterID string           `json:"frontend_adapter_id,omitempty"`
	Locale            string           `json:"locale"`
	FileMode          string           `json:"file_mode"`
	OutputSlug        string           `json:"output_slug,omitempty"`
	Enabled           bool             `json:"enabled"`
	Templates         []ConfigTemplate `json:"templates,omitempty"`
}

type ConfigTemplate struct {
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	OutputPath string `json:"output_path"`
	Body       string `json:"body"`
}

type FileRecord struct {
	EditionID string `json:"edition_id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	FormatVersion  int             `json:"format_version"`
	GeneratedAt    time.Time       `json:"generated_at"`
	Profile        Profile         `json:"profile"`
	Files          []FileRecord    `json:"files"`
	ManagedPaths   []string        `json:"managed_paths"`
	ManagedRecords []ManagedRecord `json:"managed_records"`
}

type ManagedRecord struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Result struct {
	Output           string   `json:"output"`
	Exported         int      `json:"exported_editions"`
	Copied           int      `json:"copied_files"`
	Linked           int      `json:"linked_files"`
	Unchanged        int      `json:"unchanged_files"`
	Missing          int      `json:"missing_files"`
	Warnings         []string `json:"warnings"`
	ManifestFiles    int      `json:"manifest_files"`
	RecoverySnapshot string   `json:"recovery_snapshot,omitempty"`
}

func Build(ctx context.Context, store *catalog.Store, libraryRoot, outRoot string, profile Profile) (Result, error) {
	return BuildWithStorage(ctx, store, libraryRoot, libraryRoot, libraryRoot, outRoot, profile)
}

func BuildWithStorage(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot string, profile Profile) (result Result, err error) {
	recoveryRoot := filepath.Join(filepath.Dir(outRoot), recoveryDirectory, filepath.Base(filepath.Clean(outRoot)))
	recoveryLocator := filepath.ToSlash(filepath.Join(recoveryDirectory, filepath.Base(filepath.Clean(outRoot))))
	return BuildWithStorageAndRecovery(ctx, store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, recoveryRoot, recoveryLocator, profile)
}

func BuildWithStorageAndRecovery(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, recoveryRoot, recoveryLocator string, profile Profile) (result Result, err error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Frontend = strings.ToLower(strings.TrimSpace(profile.Frontend))
	profile.Target = strings.ToLower(strings.TrimSpace(profile.Target))
	profile.FileMode = strings.ToLower(strings.TrimSpace(profile.FileMode))
	if profile.Name == "" {
		return Result{}, errors.New("profile name is required")
	}
	if profile.Frontend != "pegasus" && profile.Frontend != "es-de" {
		return Result{}, errors.New("frontend must be pegasus or es-de")
	}
	if profile.FileMode == "" {
		profile.FileMode = "copy"
	}
	if profile.FileMode != "copy" && profile.FileMode != "hardlink" && profile.FileMode != "reference" {
		return Result{}, errors.New("file_mode must be copy, hardlink, or reference")
	}
	if profile.Locale == "" {
		profile.Locale = "zh-CN"
	}
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return Result{}, err
	}
	managedROM, err := filepath.Abs(managedROMRoot)
	if err != nil {
		return Result{}, err
	}
	managedMedia, err := filepath.Abs(managedMediaRoot)
	if err != nil {
		return Result{}, err
	}
	out, err := filepath.Abs(outRoot)
	if err != nil {
		return Result{}, err
	}
	if out == root {
		return Result{}, errors.New("output must differ from library root")
	}
	plan, renderedConfigs, err := planWithStorage(ctx, store, root, managedROM, managedMedia, out, profile)
	if err != nil {
		return Result{}, err
	}
	if len(plan.Conflicts) > 0 {
		return Result{}, fmt.Errorf("%w: %s", ErrUnmanagedTargetConflict, strings.Join(plan.Conflicts, ", "))
	}
	if err = os.MkdirAll(out, 0o755); err != nil {
		return Result{}, err
	}
	profile = plan.Profile
	games, err := store.ListGames(ctx, profile.Locale)
	if err != nil {
		return Result{}, err
	}
	recovery, err := prepareBuildRecovery(out, recoveryRoot, recoveryLocator, plan)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if err == nil {
			return
		}
		if restoreErr := recovery.restore(); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("package rollback was incomplete: %w", restoreErr))
		}
	}()
	// Preserve the plan's precise preflight/runtime warnings in the completed
	// build result. In particular, Android Pegasus packages with reviewed Intent
	// bindings must not be reported as unconfigured, while editions that really
	// lack a binding remain visible to CLI and API callers.
	result = Result{Output: out, Warnings: append([]string{}, plan.Warnings...), RecoverySnapshot: recovery.relative}
	manifest := Manifest{FormatVersion: 3, GeneratedAt: time.Now().UTC(), Profile: profile, Files: []FileRecord{}, ManagedPaths: append([]string{}, plan.ManagedPaths...), ManagedRecords: []ManagedRecord{}}
	syncedMedia := map[string]bool{}
	for _, game := range games {
		for _, edition := range game.Editions {
			for _, artifact := range edition.Artifacts {
				artifactRoot := root
				if artifact.StorageKind == "managed" {
					artifactRoot = managedROM
				}
				rel, cleanErr := cleanRelative(artifact.Path)
				if cleanErr != nil {
					return result, cleanErr
				}
				source := filepath.Join(artifactRoot, rel)
				info, statErr := os.Lstat(source)
				if statErr != nil {
					result.Missing++
					result.Warnings = append(result.Warnings, "missing: "+artifact.Path)
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					result.Warnings = append(result.Warnings, "symlink skipped: "+artifact.Path)
					continue
				}
				if info.IsDir() {
					err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
						if walkErr != nil {
							return walkErr
						}
						if entry.IsDir() {
							return nil
						}
						if entry.Type()&os.ModeSymlink != 0 {
							result.Warnings = append(result.Warnings, "symlink skipped: "+path)
							return nil
						}
						child, relErr := filepath.Rel(artifactRoot, path)
						if relErr != nil {
							return relErr
						}
						return syncOne(edition.ID, artifactRoot, out, child, profile.FileMode, &manifest, &result)
					})
					if err != nil {
						return result, err
					}
					continue
				}
				if err = syncOne(edition.ID, artifactRoot, out, rel, profile.FileMode, &manifest, &result); err != nil {
					return result, err
				}
			}
			for _, asset := range append(append([]catalog.MediaAsset{}, game.Media...), edition.Media...) {
				key := asset.StorageKind + ":" + asset.Path
				if syncedMedia[key] {
					continue
				}
				syncedMedia[key] = true
				mediaRoot := root
				if asset.StorageKind == "managed" {
					mediaRoot = managedMedia
				}
				rel, cleanErr := cleanRelative(asset.Path)
				if cleanErr != nil {
					return result, cleanErr
				}
				info, statErr := os.Lstat(filepath.Join(mediaRoot, rel))
				if statErr != nil || info.IsDir() {
					result.Missing++
					result.Warnings = append(result.Warnings, "missing media: "+asset.Path)
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					result.Warnings = append(result.Warnings, "media symlink skipped: "+asset.Path)
					continue
				}
				if err = syncOne(edition.ID, mediaRoot, out, rel, profile.FileMode, &manifest, &result); err != nil {
					return result, err
				}
			}
		}
	}
	// A reference package is metadata-only, but its paths still describe the
	// target package root. Never serialize this server's library/managed roots
	// into frontend metadata. The user overlays the generated metadata onto an
	// existing target tree with the same safe relative layout.
	portableRoot, portable := out, true
	switch profile.Frontend {
	case "pegasus":
		if portable {
			result.Exported, err = exporter.ExportPegasusPortable(ctx, store, portableRoot, out, profile.Locale)
		} else {
			result.Exported, err = exporter.ExportPegasusWithStorage(ctx, store, root, managedROM, managedMedia, out, profile.Locale)
		}
	case "es-de":
		if portable {
			result.Exported, err = exporter.ExportESDEPortable(ctx, store, portableRoot, out, profile.Locale)
		} else {
			result.Exported, err = exporter.ExportESDEWithStorage(ctx, store, root, managedROM, managedMedia, out, profile.Locale)
		}
	}
	if err != nil {
		return result, err
	}
	for _, config := range renderedConfigs {
		if err = writeBytesAtomic(filepath.Join(out, filepath.FromSlash(config.Path)), []byte(config.Body)); err != nil {
			return result, err
		}
	}
	kinds := map[string]string{}
	for _, item := range plan.Items {
		if item.Action != "missing" && item.Action != "reference" && item.Action != "conflict" {
			kinds[item.Target] = item.Kind
		}
	}
	for _, managedPath := range manifest.ManagedPaths {
		if managedPath == "package-manifest.json" {
			continue
		}
		hash, size, hashErr := hashFile(filepath.Join(out, filepath.FromSlash(managedPath)))
		if hashErr != nil {
			continue
		}
		manifest.ManagedRecords = append(manifest.ManagedRecords, ManagedRecord{Path: managedPath, Kind: kinds[managedPath], Size: size, SHA256: hash})
	}
	result.ManifestFiles = len(manifest.Files)
	if err = writeJSONAtomic(filepath.Join(out, "package-manifest.json"), manifest); err != nil {
		return result, err
	}
	return result, nil
}

func cleanRelative(value string) (string, error) {
	value = filepath.Clean(filepath.FromSlash(strings.TrimSpace(value)))
	if value == "." || value == "" || filepath.IsAbs(value) || value == ".." || strings.HasPrefix(value, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q is not a safe relative path", value)
	}
	return value, nil
}

func syncOne(editionID, root, out, rel, mode string, manifest *Manifest, result *Result) error {
	source := filepath.Join(root, rel)
	target := source
	if mode != "reference" {
		target = filepath.Join(out, rel)
	}
	hash, size, err := hashFile(source)
	if err != nil {
		return err
	}
	manifest.Files = append(manifest.Files, FileRecord{EditionID: editionID, Source: filepath.ToSlash(rel), Target: filepath.ToSlash(rel), Size: size, SHA256: hash})
	if mode == "reference" {
		return nil
	}
	if same, _ := sameFile(target, size, hash); same {
		result.Unchanged++
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if mode == "hardlink" {
		if err = replaceHardlink(source, target); err == nil {
			result.Linked++
			return nil
		}
		result.Warnings = append(result.Warnings, "hardlink unavailable, copied: "+filepath.ToSlash(rel))
	}
	if err = copyAtomic(source, target); err != nil {
		return err
	}
	result.Copied++
	return nil
}

func sameFile(path string, size int64, expected string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() != size {
		return false, err
	}
	hash, _, err := hashFile(path)
	return hash == expected, err
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func replaceHardlink(source, target string) error {
	stage, err := os.MkdirTemp(filepath.Dir(target), ".varkiv-link-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	tmp := filepath.Join(stage, "link")
	if err = os.Link(source, tmp); err != nil {
		return err
	}
	if err := replaceFile(tmp, target); err != nil {
		return err
	}
	return nil
}

func copyAtomic(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, target)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeBytesAtomic(path, data)
}

func writeBytesAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".varkiv-write-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}
