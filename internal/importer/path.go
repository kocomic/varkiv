package importer

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

const maxFrontendMetadataSize = 64 << 20

func readExactRegularFile(path, label string, maxSize int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be an exact regular file, not a directory or symbolic link", label)
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxSize)
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxSize)
	}
	return data, nil
}

func libraryPath(libraryRoot, metadataDir, value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "file://"))
	if value == "" {
		return "", fmt.Errorf("empty artifact path")
	}
	value = filepath.FromSlash(value)
	if !filepath.IsAbs(value) {
		value = filepath.Join(metadataDir, value)
	}
	abs, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("entry is outside the configured library root")
	}
	return filepath.ToSlash(rel), nil
}

// libraryContentRoot resolves an optional, explicitly selected ROM directory.
// Frontend packs commonly keep metadata and ROM archives in separate trees;
// accepting a second root avoids copying or rewriting their metadata while
// retaining the same no-symlink and library-boundary checks as every artifact.
func libraryContentRoot(libraryRoot, metadataDir, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return metadataDir, nil
	}
	rel, err := libraryPath(libraryRoot, libraryRoot, value)
	if err != nil {
		return "", fmt.Errorf("content root: %w", err)
	}
	abs, info, exists, err := exactLibraryEntry(libraryRoot, rel)
	if err != nil {
		return "", fmt.Errorf("content root: %w", err)
	}
	if !exists || !info.IsDir() {
		return "", errors.New("content root must be an existing real directory inside the library root")
	}
	return abs, nil
}

func libraryMedia(libraryRoot, metadataDir, value, kind string) (catalog.NewMediaAsset, bool, error) {
	rel, err := libraryPath(libraryRoot, metadataDir, value)
	if err != nil {
		return catalog.NewMediaAsset{}, false, err
	}
	abs, info, exists, err := exactLibraryEntry(libraryRoot, rel)
	if err != nil {
		return catalog.NewMediaAsset{}, false, err
	}
	if !exists {
		return catalog.NewMediaAsset{}, false, nil
	}
	if info.IsDir() {
		return catalog.NewMediaAsset{}, false, nil
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	digest, _, err := filehash.File(abs)
	if err != nil {
		return catalog.NewMediaAsset{}, false, err
	}
	return catalog.NewMediaAsset{Kind: kind, StorageKind: "library", Path: rel, SourcePath: rel, OriginalName: info.Name(), MIMEType: mimeType, Size: info.Size(), SHA256: digest, SourceType: "frontend-import"}, true, nil
}

func libraryArtifact(libraryRoot, metadataDir, value string) (catalog.NewArtifact, error) {
	rel, err := libraryPath(libraryRoot, metadataDir, value)
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	artifact := catalog.NewArtifact{Path: rel, StorageKind: "library", SourcePath: rel, OriginalName: filepath.Base(filepath.FromSlash(rel)), Role: "rom"}
	abs, info, exists, err := exactLibraryEntry(libraryRoot, rel)
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	if !exists {
		artifact.Missing = true
		return artifact, nil
	}
	if info.IsDir() {
		artifact.SHA256, artifact.Size, err = filehash.Directory(abs)
	} else {
		artifact.SHA256, artifact.Size, err = filehash.File(abs)
	}
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	return artifact, nil
}

// exactLibraryEntry refuses every symbolic-link component below the configured
// root before any bytes are opened. This keeps a metadata file from redirecting
// an otherwise relative ROM or media path into an unrelated host directory.
func exactLibraryEntry(libraryRoot, rel string) (string, os.FileInfo, bool, error) {
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return "", nil, false, err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	cleanRel, err := filepath.Rel(root, abs)
	if err != nil || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", nil, false, errors.New("entry is outside the configured library root")
	}
	current := root
	for _, part := range strings.Split(cleanRel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return abs, nil, false, nil
			}
			return "", nil, false, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, false, errors.New("library entry contains a symbolic link")
		}
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil, false, nil
		}
		return "", nil, false, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, false, err
	}
	resolvedEntry, err := filepath.EvalSymlinks(abs)
	if err != nil || !pathInside(resolvedRoot, resolvedEntry) {
		return "", nil, false, errors.New("library entry resolves outside the configured library root")
	}
	return abs, info, true, nil
}
