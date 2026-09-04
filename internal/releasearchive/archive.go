package releasearchive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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
)

type Format string

const (
	FormatTarGz Format = "tar.gz"
	FormatZip   Format = "zip"
)

var (
	tarEpoch = time.Unix(0, 0).UTC()
	zipEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
)

type entry struct {
	absPath string
	name    string
	mode    fs.FileMode
	size    int64
	modTime time.Time
	info    fs.FileInfo
}

func Write(sourceDir, prefix, outputPath string, format Format) (err error) {
	if err := validatePrefix(prefix); err != nil {
		return err
	}
	if format != FormatTarGz && format != FormatZip {
		return errors.New("unsupported release archive format")
	}

	sourceAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return errors.New("release archive source is unavailable")
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil {
		return errors.New("release archive output is unavailable")
	}
	if inside(sourceAbs, outputAbs) {
		return errors.New("release archive output must be outside the source")
	}

	entries, err := collect(sourceAbs, prefix)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputAbs), 0o755); err != nil {
		return errors.New("release archive output directory is unavailable")
	}
	out, err := os.OpenFile(outputAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("release archive output already exists")
		}
		return errors.New("release archive output cannot be created")
	}
	created := true
	defer func() {
		if created {
			_ = out.Close()
			_ = os.Remove(outputAbs)
		}
	}()

	switch format {
	case FormatZip:
		err = writeZip(out, entries)
	case FormatTarGz:
		err = writeTarGz(out, entries)
	}
	if err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return errors.New("release archive could not be synchronized")
	}
	if err := out.Chmod(0o644); err != nil {
		return errors.New("release archive permissions could not be finalized")
	}
	if err := out.Close(); err != nil {
		return errors.New("release archive could not be closed")
	}
	created = false
	return nil
}

func validatePrefix(prefix string) error {
	if prefix == "" || prefix == "." || prefix == ".." || path.Clean(prefix) != prefix || strings.Contains(prefix, "/") || strings.Contains(prefix, "\\") {
		return errors.New("release archive prefix must be one portable directory name")
	}
	for _, r := range prefix {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return errors.New("release archive prefix contains unsupported characters")
	}
	return nil
}

func inside(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return true
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func collect(sourceAbs, prefix string) ([]entry, error) {
	rootInfo, err := os.Lstat(sourceAbs)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("release archive source must be a regular directory")
	}

	entries := []entry{{absPath: sourceAbs, name: prefix + "/", mode: rootInfo.Mode(), modTime: rootInfo.ModTime(), info: rootInfo}}
	err = filepath.WalkDir(sourceAbs, func(current string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("release archive source cannot be inspected")
		}
		if current == sourceAbs {
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return errors.New("release archive entry cannot be inspected")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release archive rejects symbolic link: %s", safeRelative(sourceAbs, current))
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("release archive rejects special file: %s", safeRelative(sourceAbs, current))
		}
		rel, err := filepath.Rel(sourceAbs, current)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("release archive entry escaped the source")
		}
		name := prefix + "/" + filepath.ToSlash(rel)
		if info.IsDir() {
			name += "/"
		}
		entries = append(entries, entry{absPath: current, name: name, mode: info.Mode(), size: info.Size(), modTime: info.ModTime(), info: info})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

func safeRelative(root, current string) string {
	rel, err := filepath.Rel(root, current)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "entry"
	}
	return filepath.ToSlash(rel)
}

func writeZip(out io.Writer, entries []entry) error {
	zw := zip.NewWriter(out)
	for _, item := range entries {
		header := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
		header.SetModTime(zipEpoch)
		if item.mode.IsDir() {
			header.Method = zip.Store
			header.SetMode(os.ModeDir | normalizedMode(item.mode, true))
			if _, err := zw.CreateHeader(header); err != nil {
				_ = zw.Close()
				return errors.New("release ZIP directory entry could not be written")
			}
			continue
		}
		header.SetMode(normalizedMode(item.mode, false))
		writer, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return errors.New("release ZIP file entry could not be written")
		}
		if err := copyStable(writer, item); err != nil {
			_ = zw.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return errors.New("release ZIP could not be finalized")
	}
	return nil
}

func writeTarGz(out io.Writer, entries []entry) error {
	gw, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return errors.New("release gzip writer could not be created")
	}
	gw.Header.ModTime = tarEpoch
	gw.Header.OS = 255
	tw := tar.NewWriter(gw)
	for _, item := range entries {
		header := &tar.Header{
			Name:       item.name,
			Mode:       int64(normalizedMode(item.mode, item.mode.IsDir())),
			ModTime:    tarEpoch,
			AccessTime: tarEpoch,
			ChangeTime: tarEpoch,
			Uid:        0,
			Gid:        0,
			Format:     tar.FormatPAX,
		}
		if item.mode.IsDir() {
			header.Typeflag = tar.TypeDir
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = item.size
		}
		if err := tw.WriteHeader(header); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return errors.New("release tar header could not be written")
		}
		if !item.mode.IsDir() {
			if err := copyStable(tw, item); err != nil {
				_ = tw.Close()
				_ = gw.Close()
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return errors.New("release tar could not be finalized")
	}
	if err := gw.Close(); err != nil {
		return errors.New("release gzip could not be finalized")
	}
	return nil
}

func normalizedMode(mode fs.FileMode, directory bool) fs.FileMode {
	if directory {
		return 0o755
	}
	if mode.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func copyStable(dst io.Writer, item entry) error {
	pathBefore, err := os.Lstat(item.absPath)
	if err != nil || !pathBefore.Mode().IsRegular() || !os.SameFile(pathBefore, item.info) || pathBefore.Size() != item.size || pathBefore.Mode() != item.mode || !pathBefore.ModTime().Equal(item.modTime) {
		return fmt.Errorf("release archive entry changed before opening: %s", item.name)
	}
	file, err := os.Open(item.absPath)
	if err != nil {
		return fmt.Errorf("release archive entry became unavailable: %s", item.name)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || !os.SameFile(before, item.info) || before.Size() != item.size || before.Mode() != item.mode || !before.ModTime().Equal(item.modTime) {
		return fmt.Errorf("release archive entry changed before reading: %s", item.name)
	}
	written, err := io.Copy(dst, file)
	if err != nil || written != item.size {
		return fmt.Errorf("release archive entry could not be read completely: %s", item.name)
	}
	after, err := file.Stat()
	pathAfter, pathErr := os.Lstat(item.absPath)
	if err != nil || pathErr != nil || !pathAfter.Mode().IsRegular() || !os.SameFile(after, item.info) || !os.SameFile(pathAfter, item.info) || after.Size() != before.Size() || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("release archive entry changed while reading: %s", item.name)
	}
	return nil
}
