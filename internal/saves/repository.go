package saves

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/portablepath"
)

type Repository struct {
	store *catalog.Store
	root  string
}

type PushInput struct {
	EditionID      string
	DeviceID       string
	DriverID       string
	RelativePath   string
	ScopeType      string
	ScopeKey       string
	BaseRevisionID string
}

type PushResult struct {
	Revision catalog.SaveRevision `json:"revision"`
	Created  bool                 `json:"created"`
	Conflict bool                 `json:"conflict"`
}

type IncomingFile struct {
	LogicalPath string
	Reader      io.Reader
	MTimeNS     int64
	Mode        int64
}

type PushSetInput struct {
	EditionID           string
	DeviceID            string
	DriverID            string
	ScopeType           string
	ScopeKey            string
	BaseRevisionID      string
	ExpectedContentHash string
	Files               []IncomingFile
}

var ErrContentHashMismatch = errors.New("save content hash does not match the negotiated hash")
var ErrBlobIntegrity = errors.New("save blob failed integrity validation")

const (
	MaxRevisionFiles = portablepath.MaxSaveRevisionFiles
	MaxRevisionBytes = portablepath.MaxSaveRevisionBytes
)

var errRevisionLimit = errors.New("save revision exceeds the size or file-count limit")

func copyIncomingSave(dst io.Writer, src io.Reader, remaining int64) (int64, error) {
	if remaining < 0 {
		return 0, errRevisionLimit
	}
	written, err := io.Copy(dst, io.LimitReader(src, remaining+1))
	if err != nil {
		return written, err
	}
	if written > remaining {
		return written, errRevisionLimit
	}
	return written, nil
}

func New(store *catalog.Store, root string) (*Repository, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(abs, "blobs"), 0o755); err != nil {
		return nil, err
	}
	for _, path := range []string{abs, filepath.Join(abs, "blobs")} {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("save repository root must be a real directory")
		}
	}
	return &Repository{store: store, root: abs}, nil
}

func (r *Repository) blobPath(checksum string, createPrefix bool) (string, error) {
	if len(checksum) != 64 {
		return "", ErrBlobIntegrity
	}
	for _, char := range checksum {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", ErrBlobIntegrity
		}
	}
	dir := filepath.Join(r.root, "blobs", checksum[:2])
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if createPrefix {
			if err = os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
		}
	} else if err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) && !createPrefix {
		return filepath.Join(dir, checksum), nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("save blob prefix must be a real directory")
	}
	return filepath.Join(dir, checksum), nil
}

func openVerifiedBlob(path, checksum string, size int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != size {
		return nil, ErrBlobIntegrity
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, ErrBlobIntegrity
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, handle); err != nil || hex.EncodeToString(hash.Sum(nil)) != checksum {
		handle.Close()
		return nil, ErrBlobIntegrity
	}
	if _, err = handle.Seek(0, io.SeekStart); err != nil {
		handle.Close()
		return nil, ErrBlobIntegrity
	}
	return handle, nil
}

func (r *Repository) Push(ctx context.Context, in PushInput, reader io.Reader) (PushResult, error) {
	return r.PushSet(ctx, PushSetInput{
		EditionID: in.EditionID, DeviceID: in.DeviceID, DriverID: in.DriverID, ScopeType: in.ScopeType, ScopeKey: in.ScopeKey, BaseRevisionID: in.BaseRevisionID,
		Files: []IncomingFile{{LogicalPath: in.RelativePath, Reader: reader}},
	})
}

func (r *Repository) PushSet(ctx context.Context, in PushSetInput) (PushResult, error) {
	if strings.TrimSpace(in.EditionID) == "" || strings.TrimSpace(in.DeviceID) == "" {
		return PushResult{}, errors.New("edition_id and device_id are required")
	}
	if in.DriverID == "" {
		in.DriverID = "manual"
	}
	if in.ScopeType == "" {
		in.ScopeType = "game"
	}
	if in.ScopeType != "game" && in.ScopeType != "platform" && in.ScopeType != "container" {
		return PushResult{}, errors.New("scope_type must be game, platform, or container")
	}
	if len(in.Files) == 0 || len(in.Files) > MaxRevisionFiles {
		return PushResult{}, errors.New("a save revision must contain 1..4096 files")
	}
	type stagedFile struct {
		file catalog.NewSaveFile
		temp string
	}
	staged := make([]stagedFile, 0, len(in.Files))
	defer func() {
		for _, item := range staged {
			if item.temp != "" {
				_ = os.Remove(item.temp)
			}
		}
	}()
	seen := map[string]bool{}
	var totalSize int64
	for _, incoming := range in.Files {
		rel, err := cleanRelative(incoming.LogicalPath)
		if err != nil {
			return PushResult{}, err
		}
		logicalPath := filepath.ToSlash(rel)
		if seen[logicalPath] {
			return PushResult{}, errors.New("save revision contains a duplicate logical path")
		}
		seen[logicalPath] = true
		if incoming.Reader == nil {
			return PushResult{}, errors.New("save revision contains a file without a content reader")
		}
		tmp, err := os.CreateTemp(r.root, ".save-upload-*")
		if err != nil {
			return PushResult{}, err
		}
		tempName := tmp.Name()
		h := sha256.New()
		size, copyErr := copyIncomingSave(io.MultiWriter(tmp, h), incoming.Reader, MaxRevisionBytes-totalSize)
		closeErr := tmp.Close()
		if copyErr != nil {
			_ = os.Remove(tempName)
			return PushResult{}, copyErr
		}
		if closeErr != nil {
			_ = os.Remove(tempName)
			return PushResult{}, closeErr
		}
		totalSize += size
		checksum := hex.EncodeToString(h.Sum(nil))
		staged = append(staged, stagedFile{file: catalog.NewSaveFile{LogicalPath: logicalPath, Checksum: checksum, Size: size, MTimeNS: incoming.MTimeNS, Mode: incoming.Mode}, temp: tempName})
	}
	files := make([]catalog.NewSaveFile, len(staged))
	for index := range staged {
		files[index] = staged[index].file
	}
	contentHash := contentHashForSet(files)
	if expected := strings.ToLower(strings.TrimSpace(in.ExpectedContentHash)); expected != "" && contentHash != expected {
		return PushResult{}, ErrContentHashMismatch
	}
	for index := range staged {
		item := &staged[index]
		blobPath, pathErr := r.blobPath(item.file.Checksum, false)
		if pathErr != nil {
			return PushResult{}, pathErr
		}
		if _, statErr := os.Lstat(blobPath); statErr == nil {
			handle, verifyErr := openVerifiedBlob(blobPath, item.file.Checksum, item.file.Size)
			if verifyErr != nil {
				return PushResult{}, verifyErr
			}
			handle.Close()
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return PushResult{}, statErr
		}
	}
	stream, streamErr := r.store.ResolveSaveStream(ctx, in.EditionID, in.DriverID, in.ScopeType, in.ScopeKey)
	if streamErr == nil && strings.TrimSpace(in.BaseRevisionID) != "" {
		base, baseErr := r.store.GetSaveRevision(ctx, in.BaseRevisionID)
		if baseErr != nil {
			return PushResult{}, errors.New("base revision is unavailable")
		}
		if base.StreamID != stream.ID {
			return PushResult{}, errors.New("base revision belongs to a different save stream")
		}
	} else if errors.Is(streamErr, sql.ErrNoRows) && strings.TrimSpace(in.BaseRevisionID) != "" {
		return PushResult{}, errors.New("base revision is unavailable for a new save stream")
	}
	var latest catalog.SaveRevision
	latestErr := streamErr
	if streamErr == nil {
		latest, latestErr = r.store.CurrentStreamRevision(ctx, stream.ID)
	}
	if latestErr == nil && latest.ContentHash == contentHash {
		return PushResult{Revision: latest, Created: false, Conflict: latest.Conflict}, nil
	}
	if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
		return PushResult{}, latestErr
	}
	conflict := latestErr == nil && in.BaseRevisionID != latest.ID
	if conflict {
		if existing, findErr := r.store.FindStreamRevisionByContentHash(ctx, stream.ID, contentHash); findErr == nil {
			return PushResult{Revision: existing, Created: false, Conflict: existing.Conflict}, nil
		} else if !errors.Is(findErr, sql.ErrNoRows) {
			return PushResult{}, findErr
		}
	}
	for index := range staged {
		item := &staged[index]
		blobPath, pathErr := r.blobPath(item.file.Checksum, true)
		if pathErr != nil {
			return PushResult{}, pathErr
		}
		if _, statErr := os.Lstat(blobPath); errors.Is(statErr, os.ErrNotExist) {
			if err := os.Rename(item.temp, blobPath); err != nil {
				return PushResult{}, err
			}
			item.temp = ""
		} else if statErr != nil {
			return PushResult{}, statErr
		} else if handle, verifyErr := openVerifiedBlob(blobPath, item.file.Checksum, item.file.Size); verifyErr != nil {
			return PushResult{}, verifyErr
		} else {
			handle.Close()
		}
		item.file.BlobPath = blobPath
	}
	for index := range staged {
		files[index] = staged[index].file
	}
	revision, err := r.store.AddSaveRevision(ctx, catalog.NewSaveRevision{
		StreamID: stream.ID, EditionID: in.EditionID, DeviceID: in.DeviceID, DriverID: in.DriverID, ScopeType: in.ScopeType, ScopeKey: in.ScopeKey,
		ContentHash: contentHash, Files: files, ParentRevisionID: in.BaseRevisionID, Conflict: conflict,
	})
	if err != nil {
		return PushResult{}, err
	}
	return PushResult{Revision: revision, Created: true, Conflict: conflict}, nil
}

func contentHashForSet(files []catalog.NewSaveFile) string {
	ordered := append([]catalog.NewSaveFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].LogicalPath < ordered[j].LogicalPath })
	h := sha256.New()
	for _, item := range ordered {
		h.Write([]byte(item.LogicalPath))
		h.Write([]byte{0})
		h.Write([]byte(item.Checksum))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(item.Size, 10)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Repository) OpenRevision(ctx context.Context, id string) (*os.File, catalog.SaveRevision, error) {
	revision, err := r.store.GetSaveRevision(ctx, id)
	if err != nil {
		return nil, catalog.SaveRevision{}, err
	}
	if len(revision.Files) != 1 {
		return nil, catalog.SaveRevision{}, errors.New("multi-file revision must be downloaded one file at a time")
	}
	file, err := r.openBlob(revision.Files[0].BlobPath, revision.Files[0].Checksum, revision.Files[0].Size)
	return file, revision, err
}

func (r *Repository) OpenRevisionFile(ctx context.Context, revisionID, fileID string) (*os.File, catalog.SaveFile, error) {
	file, err := r.store.GetSaveFile(ctx, revisionID, fileID)
	if err != nil {
		return nil, catalog.SaveFile{}, err
	}
	handle, err := r.openBlob(file.BlobPath, file.Checksum, file.Size)
	return handle, file, err
}

func (r *Repository) openBlob(blobPath, checksum string, size int64) (*os.File, error) {
	blob, err := filepath.Abs(blobPath)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(r.root, blob)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("save blob path is outside repository")
	}
	expected, err := r.blobPath(checksum, false)
	if err != nil || filepath.Clean(expected) != filepath.Clean(blob) {
		return nil, ErrBlobIntegrity
	}
	return openVerifiedBlob(blob, checksum, size)
}

func cleanRelative(value string) (string, error) {
	return portablepath.CleanSaveLogical(value)
}
