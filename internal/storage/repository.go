package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

type Repository struct {
	LibraryRoot string
	StateRoot   string
	ROMRoot     string
	MediaRoot   string
}

var (
	ErrMediaUnavailable = errors.New("media content is unavailable")
	ErrMediaIntegrity   = errors.New("media content failed integrity validation")
)

type IngestResult struct {
	Game        catalog.ImportedGame
	ROMFiles    int
	ROMBytes    int64
	MediaFiles  int
	MediaBytes  int64
	cleanupPath string
}

func New(libraryRoot, stateRoot string) (*Repository, error) {
	libraryRoot, err := filepath.Abs(libraryRoot)
	if err != nil {
		return nil, err
	}
	stateRoot, err = filepath.Abs(stateRoot)
	if err != nil {
		return nil, err
	}
	repo := &Repository{LibraryRoot: libraryRoot, StateRoot: stateRoot, ROMRoot: filepath.Join(stateRoot, "roms"), MediaRoot: filepath.Join(stateRoot, "media")}
	for _, root := range []string{repo.ROMRoot, filepath.Join(repo.MediaRoot, "blobs", "sha256")} {
		if err = os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	}
	return repo, nil
}

func (r *Repository) ResolveArtifact(item catalog.Artifact) (string, error) {
	root := r.LibraryRoot
	if item.StorageKind == "managed" {
		root = r.ROMRoot
	} else if item.StorageKind != "" && item.StorageKind != "library" {
		return "", fmt.Errorf("unsupported artifact storage kind %q", item.StorageKind)
	}
	return inside(root, item.Path)
}

func (r *Repository) ResolveMedia(item catalog.MediaAsset) (string, error) {
	root := r.LibraryRoot
	if item.StorageKind == "managed" {
		root = r.MediaRoot
	} else if item.StorageKind != "library" {
		return "", fmt.Errorf("unsupported media storage kind %q", item.StorageKind)
	}
	return inside(root, item.Path)
}

func (r *Repository) inspectMedia(item catalog.MediaAsset) (*os.File, string) {
	path, err := r.ResolveMedia(item)
	if err != nil {
		return nil, "unsafe"
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "missing"
		}
		return nil, "unsafe"
	}
	closeWith := func(status string) (*os.File, string) {
		_ = file.Close()
		return nil, status
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return closeWith("unsafe")
	}
	if item.SHA256 == "" {
		return file, "unverified"
	}
	if item.Size < 0 || info.Size() != item.Size {
		return closeWith("changed")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), item.SHA256) {
		return closeWith("changed")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return closeWith("unsafe")
	}
	return file, "available"
}

// InspectMedia performs an explicit availability and identity check without
// changing the file. The caller may persist the returned status separately.
func (r *Repository) InspectMedia(item catalog.MediaAsset) string {
	file, status := r.inspectMedia(item)
	if file != nil {
		_ = file.Close()
	}
	return status
}

// OpenVerifiedMedia resolves the configured storage root without following
// symbolic links and validates known content identity before callers emit any
// response headers or bytes. The returned handle is rewound to the beginning.
func (r *Repository) OpenVerifiedMedia(item catalog.MediaAsset) (*os.File, error) {
	file, status := r.inspectMedia(item)
	switch status {
	case "available", "unverified":
		return file, nil
	case "changed":
		return nil, ErrMediaIntegrity
	default:
		return nil, ErrMediaUnavailable
	}
}

func inside(root, relative string) (string, error) {
	if filepath.IsAbs(filepath.FromSlash(relative)) {
		return "", errors.New("stored path must be relative")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("stored path escapes its storage root")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				break
			}
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("stored path contains a symbolic link")
		}
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		resolvedRoot, resolveRootErr := filepath.EvalSymlinks(root)
		resolvedPath, resolvePathErr := filepath.EvalSymlinks(path)
		if resolveRootErr != nil || resolvePathErr != nil {
			return "", errors.New("stored path cannot be resolved safely")
		}
		resolvedRel, relErr := filepath.Rel(resolvedRoot, resolvedPath)
		if relErr != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator)) {
			return "", errors.New("stored path resolves outside its storage root")
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	return path, nil
}

func (r *Repository) IngestGame(ctx context.Context, game catalog.ImportedGame, romMode, mediaMode string) (IngestResult, error) {
	if romMode == "" {
		romMode = "reference"
	}
	if mediaMode == "" {
		mediaMode = "copy"
	}
	if romMode != "reference" && romMode != "copy" {
		return IngestResult{}, errors.New("rom_storage must be reference or copy")
	}
	if mediaMode != "reference" && mediaMode != "copy" && mediaMode != "ignore" {
		return IngestResult{}, errors.New("media_storage must be reference, copy, or ignore")
	}
	if game.EditionID == "" {
		game.EditionID = catalog.NewID()
	}
	result := IngestResult{Game: game}
	if romMode == "copy" {
		for _, item := range game.Artifacts {
			if item.Missing {
				return IngestResult{}, fmt.Errorf("cannot import missing ROM %q; regenerate the preview after restoring the file", item.Path)
			}
		}
		cleanupPath, err := inside(r.ROMRoot, filepath.Join(safeSegment(game.Platform), safeIdentitySegment(game.EditionID)))
		if err != nil {
			return IngestResult{}, err
		}
		result.cleanupPath = cleanupPath
		if err := os.MkdirAll(filepath.Dir(result.cleanupPath), 0o755); err != nil {
			return IngestResult{}, err
		}
		if err := os.Mkdir(result.cleanupPath, 0o755); err != nil {
			if os.IsExist(err) {
				return IngestResult{}, errors.New("managed ROM destination already exists; resolve the edition conflict before importing")
			}
			return IngestResult{}, err
		}
		if err := r.copyArtifacts(ctx, &result); err != nil {
			_ = os.RemoveAll(result.cleanupPath)
			return IngestResult{}, err
		}
	} else {
		for index := range result.Game.Artifacts {
			item := &result.Game.Artifacts[index]
			item.StorageKind = "library"
			if item.SourcePath == "" {
				item.SourcePath = item.Path
			}
			if item.OriginalName == "" {
				item.OriginalName = filepath.Base(filepath.FromSlash(item.Path))
			}
		}
	}
	if mediaMode == "ignore" {
		result.Game.Media = nil
	} else {
		for index := range result.Game.Media {
			item := &result.Game.Media[index]
			if mediaMode == "copy" {
				stored, size, err := r.copyMedia(ctx, *item)
				if err != nil {
					result.Cleanup()
					return IngestResult{}, err
				}
				*item = stored
				result.MediaFiles++
				result.MediaBytes += size
			} else {
				item.StorageKind = "library"
				if item.SourcePath == "" {
					item.SourcePath = item.Path
				}
			}
		}
	}
	return result, nil
}

func (r IngestResult) Cleanup() {
	if r.cleanupPath != "" {
		_ = os.RemoveAll(r.cleanupPath)
	}
}

func (r *Repository) copyArtifacts(ctx context.Context, result *IngestResult) error {
	base, err := commonParent(r.LibraryRoot, result.Game.Artifacts)
	if err != nil {
		return err
	}
	for index := range result.Game.Artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := &result.Game.Artifacts[index]
		source, err := inside(r.LibraryRoot, item.Path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink ROM import is not allowed: %s", item.Path)
		}
		rel, err := filepath.Rel(base, source)
		if err != nil || rel == "." {
			rel = filepath.Base(source)
		}
		target := filepath.Join(result.cleanupPath, rel)
		size, hash, files, err := copyPathAtomic(ctx, source, target)
		if err != nil {
			return err
		}
		storedRel, err := filepath.Rel(r.ROMRoot, target)
		if err != nil {
			return err
		}
		item.SourcePath = item.Path
		item.Path = filepath.ToSlash(storedRel)
		item.StorageKind = "managed"
		item.OriginalName = filepath.Base(source)
		item.Size, item.SHA256, item.Missing = size, hash, false
		result.ROMBytes += size
		result.ROMFiles += files
	}
	return nil
}

func commonParent(root string, items []catalog.NewArtifact) (string, error) {
	if len(items) == 0 {
		return root, nil
	}
	first, err := inside(root, items[0].Path)
	if err != nil {
		return "", err
	}
	base := filepath.Dir(first)
	for _, item := range items[1:] {
		path, pathErr := inside(root, item.Path)
		if pathErr != nil {
			return "", pathErr
		}
		for {
			rel, relErr := filepath.Rel(base, path)
			if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				break
			}
			parent := filepath.Dir(base)
			if parent == base {
				return "", errors.New("artifact paths do not share a safe parent")
			}
			base = parent
		}
	}
	return base, nil
}

func (r *Repository) copyMedia(ctx context.Context, item catalog.NewMediaAsset) (catalog.NewMediaAsset, int64, error) {
	source, err := inside(r.LibraryRoot, item.Path)
	if err != nil {
		return item, 0, err
	}
	file, err := os.Open(source)
	if err != nil {
		return item, 0, err
	}
	defer file.Close()
	stored, size, hash, mimeType, err := r.StoreMedia(ctx, filepath.Base(source), item.MIMEType, file)
	if err != nil {
		return item, 0, err
	}
	item.SourcePath = item.Path
	item.Path, item.StorageKind = stored, "managed"
	item.OriginalName, item.MIMEType, item.Size, item.SHA256 = filepath.Base(source), mimeType, size, hash
	return item, size, nil
}

func (r *Repository) StoreMedia(ctx context.Context, name, declaredType string, source io.Reader) (string, int64, string, string, error) {
	tmpDir := filepath.Join(r.MediaRoot, ".staging")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", 0, "", "", err
	}
	tmp, err := os.CreateTemp(tmpDir, "media-*")
	if err != nil {
		return "", 0, "", "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), &contextReader{ctx: ctx, reader: source})
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, "", "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 12 || strings.ContainsAny(ext, `/\\`) {
		ext = ""
	}
	mimeType := strings.TrimSpace(strings.Split(declaredType, ";")[0])
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(ext)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	ext = canonicalExtension(mimeType, ext)
	rel := filepath.Join("blobs", "sha256", digest[:2], digest+ext)
	target, err := inside(r.MediaRoot, rel)
	if err != nil {
		return "", 0, "", "", err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", 0, "", "", err
	}
	if info, statErr := os.Lstat(target); os.IsNotExist(statErr) {
		if err = os.Rename(tmpName, target); err != nil {
			return "", 0, "", "", err
		}
	} else if statErr != nil {
		return "", 0, "", "", statErr
	} else if !info.Mode().IsRegular() {
		return "", 0, "", "", errors.New("managed media target is not an exact regular file")
	} else if err = verifyContentAddressedFile(target, digest, size); err != nil {
		return "", 0, "", "", err
	}
	return filepath.ToSlash(rel), size, digest, mimeType, nil
}

func verifyContentAddressedFile(path, expectedHash string, expectedSize int64) error {
	handle, err := os.Open(path)
	if err != nil {
		return errors.New("managed media blob failed integrity validation")
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expectedSize {
		return errors.New("managed media blob failed integrity validation")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, handle); err != nil || hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		return errors.New("managed media blob failed integrity validation")
	}
	return nil
}

func canonicalExtension(mimeType, fallback string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "application/pdf":
		return ".pdf"
	default:
		return fallback
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func copyPathAtomic(ctx context.Context, source, target string) (int64, string, int, error) {
	info, err := os.Stat(source)
	if err != nil {
		return 0, "", 0, err
	}
	if !info.IsDir() {
		size, hash, err := copyFileAtomic(ctx, source, target)
		return size, hash, 1, err
	}
	var total int64
	files := 0
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink inside directory import is not allowed: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		size, _, copyErr := copyFileAtomic(ctx, path, filepath.Join(target, rel))
		if copyErr != nil {
			return copyErr
		}
		total += size
		files++
		return nil
	})
	if err != nil {
		return total, "", files, err
	}
	digest, canonicalSize, err := filehash.Directory(target)
	if err != nil {
		return total, "", files, err
	}
	if canonicalSize != total {
		return total, "", files, errors.New("managed directory size changed during copy")
	}
	return total, digest, files, nil
}

func copyFileAtomic(ctx context.Context, source, target string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, "", err
	}
	in, err := os.Open(source)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".import-*")
	if err != nil {
		return 0, "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), &contextReader{ctx: ctx, reader: in})
	if syncErr := tmp.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, "", err
	}
	if err = os.Rename(tmpName, target); err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func safeSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, runeValue := range value {
		if runeValue >= 'a' && runeValue <= 'z' || runeValue >= '0' && runeValue <= '9' || runeValue == '-' || runeValue == '_' {
			out.WriteRune(runeValue)
		}
	}
	if out.Len() == 0 {
		return "custom"
	}
	return out.String()
}

func safeIdentitySegment(value string) string {
	original := strings.ToLower(strings.TrimSpace(value))
	segment := safeSegment(original)
	if segment == original {
		return segment
	}
	digest := sha256.Sum256([]byte(value))
	return segment + "-" + hex.EncodeToString(digest[:6])
}
